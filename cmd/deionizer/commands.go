package main

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"deionizer/internal/detect"
	"deionizer/internal/docker"
	"deionizer/internal/opline"
	"deionizer/internal/runtime"
)

//go:embed driver.php
var driverPHP string

// guardConstsEnv names the env var carrying extra bootstrap guard constants for
// the decode driver (comma-separated NAME or NAME=VALUE). It lets a user load a
// module that checks an app-specific guard without that app's name living in the
// tool. Forwarded into the decode container by revealMethods.
const guardConstsEnv = "DEIONIZER_GUARD_CONSTS"

const artifactSuffix = ".decoded-source.php"

// oracleEnsurer returns a detect.EnsureFunc that builds the interface-recovery
// image, closing over the loader download base so the fixed EnsureFunc signature
// carries no config of its own — the value still comes from the command line.
func oracleEnsurer(loaderBaseURL string) detect.EnsureFunc {
	return func(ver, _ /*image*/, platform string) error {
		return docker.EnsureOracleImage(ver, platform, loaderBaseURL)
	}
}

// resolveVersion returns the runtime key for a sample file: forced --php wins,
// else the marker, else a trial-load probe. The second result says WHICH of the
// three answered, because "why did it pick 7.4?" is the first question anyone
// asks when a decode comes back empty.
func resolveVersion(sample string, m *runtime.Matrix, opt options, u *ui) (ver, how string, err error) {
	if opt.php != "" {
		return m.Nearest(opt.php), "forced with --php " + opt.php, nil
	}
	u.tracef("reading ionCube marker from %s", filepath.Base(sample))
	ver = detect.MarkerVersion(sample, m)
	if ver != detect.ProbeSentinel {
		return ver, "from the ionCube marker", nil
	}
	// A version-less marker: the only way to know is to let a loader try.
	u.info("marker carries no version — trial-loading %s to ask the loader", filepath.Base(sample))
	ver, err = detect.ProbeVersion(sample, m, oracleEnsurer(opt.loaderBaseURL))
	if err != nil {
		return "", "", err
	}
	return ver, "from a trial-load probe", nil
}

/* ================================ process ================================= */

// Column widths for the process command's per-project table.
const (
	projectCol = 28
	phpCol     = 5
	filesCol   = 7
)

// printProjectRow prints one aligned row of the process table — the header, a
// per-project result, or an error row. last is the trailing free column (the image
// name or an error), rendered with %s.
func printProjectRow(project, php, files string, last any) {
	fmt.Printf("   %-*s %-*s %-*s %s\n", projectCol, project, phpCol, php, filesCol, files, last)
}

func cmdProcess(args []string, opt options) error {
	if len(args) < 1 {
		return fmt.Errorf("process: need a tree")
	}
	tree, err := filepath.Abs(args[0])
	if err != nil {
		return err
	}
	m := runtime.Load(opt.runtimesPath)

	// The per-project table IS this command's progress report, so per-file lines
	// stay off unless --verbose asks for them.
	u := newUI(opt.verbose)
	u.items = opt.verbose

	projects := findProjects(tree)
	if len(projects) == 0 {
		fmt.Println("   (no encoded projects found)")
		return nil
	}

	mode := "FULL-decode"
	if opt.skeleton {
		mode = "skeleton (interface-only)"
	}
	fmt.Printf(">> processing corpus: %s   [%s]\n", tree, mode)
	printProjectRow("PROJECT", "PHP", "FILES", "IMAGE")

	for _, p := range projects {
		if docker.Interrupted() {
			break
		}
		sample := sampleEncoded(p)
		if sample == "" {
			continue
		}
		ver, how, err := resolveVersion(sample, m, opt, u)
		if err != nil {
			printProjectRow(filepath.Base(p), "?", "ERR", err)
			continue
		}
		rt, _ := m.Get(ver)
		platform := detect.PlatformOf(ver, m)
		u.tracef("%s: php %s (%s)", filepath.Base(p), ver, how)

		var count int
		var image string
		if opt.skeleton {
			image = imageOr(rt.Image, docker.ImageName(ver))
			count, err = runSkeleton(p, ver, image, platform, opt.loaderBaseURL, u)
		} else {
			image = rt.DecodeImage
			var st decodeStats
			ctx := decodeCtx{u: u, ver: ver, platform: platform, guardConsts: opt.guardConsts, timeout: opt.fileTimeout}
			st, err = runFullDecode(ctx, p, opt.extDir, rt)
			count = st.written
		}
		if err != nil {
			printProjectRow(filepath.Base(p), ver, "ERR", err)
			continue
		}
		printProjectRow(filepath.Base(p), ver, fmt.Sprintf("%d", count), image)
	}
	fmt.Println(">> per-project artifacts written in place (<file>" + artifactSuffix + ")")
	return nil
}

/* ================================= decode ================================= */

// cmdDecode forces a deep decode of one input: a directory tree (full decode by
// default; --skeleton still available for symmetry), a single local file, or an
// http(s) URL fetched to a temp file first. Where the recovered source lands
// depends on --output:
//
//   - --output DIR   MIRROR mode — every encoded file is decoded into DIR (the
//     source stays untouched) and a confidence report is written there. This is
//     the reviewer-facing flow (see runMirrorDecode).
//   - no --output, a directory   decode in place, writing <file>.decoded-source.php
//     next to each encoded original.
//   - no --output, a single file/URL   decode to stdout, so it can be redirected
//     or piped.
func cmdDecode(args []string, opt options) error {
	if len(args) < 1 {
		return fmt.Errorf("decode: need a directory, file, or URL")
	}
	input, cleanup, err := resolveInput(args[0])
	if err != nil {
		return err
	}
	defer cleanup()

	if opt.output != "" {
		out, err := filepath.Abs(opt.output)
		if err != nil {
			return err
		}
		return runMirrorDecode(input, out, opt)
	}

	fi, err := os.Stat(input)
	if err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	if !fi.IsDir() {
		return decodeOne(input, opt)
	}
	dir := input
	u := newUI(opt.verbose)
	started := time.Now()

	mode := "full decode"
	if opt.skeleton {
		mode = "skeleton (interface-only)"
	}
	u.head("decode %s   [%s]", dir, mode)

	m := runtime.Load(opt.runtimesPath)
	sample := sampleEncoded(dir)
	if sample == "" {
		return fmt.Errorf("decode: no ionCube-encoded PHP found under %s", dir)
	}
	ver, how, err := resolveVersion(sample, m, opt, u)
	if err != nil {
		return err
	}
	rt, _ := m.Get(ver)
	platform := detect.PlatformOf(ver, m)
	u.info("php %s — %s", ver, how)
	if platform != "" {
		u.info("platform %s (emulated)", platform)
	}

	if opt.skeleton {
		image := imageOr(rt.Image, docker.ImageName(ver))
		n, err := runSkeleton(dir, ver, image, platform, opt.loaderBaseURL, u)
		if err != nil {
			return err
		}
		u.head("%d skeleton %s written under %s (PHP %s) in %s",
			n, plural(n, "artifact", "artifacts"), dir, ver, dur(time.Since(started)))
		return nil
	}

	ctx := decodeCtx{u: u, ver: ver, platform: platform, guardConsts: opt.guardConsts, timeout: opt.fileTimeout}
	st, err := runFullDecode(ctx, dir, opt.extDir, rt)
	if err != nil {
		return err
	}
	u.head("%d decoded-source %s written under %s (PHP %s) in %s",
		st.written, plural(st.written, "artifact", "artifacts"), dir, ver, dur(time.Since(started)))
	if st.empty > 0 || st.failed > 0 {
		u.info("%d of %d file(s) yielded nothing, %d failed — rerun with --verbose to see why",
			st.empty, st.files, st.failed)
	}
	return nil
}

// decodeOne deep-decodes a single encoded file (a local path; a URL has already
// been staged locally by resolveInput) and writes the recovered source to
// stdout. Unlike the tree path, nothing is written next to the input — stdout is
// the artifact, so the ui's progress stream is moved to stderr to keep the
// source pipeable: `deionizer decode foo.php > foo-recovered.php`.
func decodeOne(file string, opt options) error {
	if opt.skeleton {
		return fmt.Errorf("decode: --skeleton works on trees; a single file goes through full decode")
	}
	if !detect.IsEncoded(file) {
		return fmt.Errorf("decode: %s is not ionCube-encoded PHP", file)
	}

	u := newUI(opt.verbose)
	u.out = u.err // source owns stdout in this mode
	started := time.Now()
	u.head("decode %s   [single file → stdout]", file)

	m := runtime.Load(opt.runtimesPath)
	ver, how, err := resolveVersion(file, m, opt, u)
	if err != nil {
		return err
	}
	rt, _ := m.Get(ver)
	platform := detect.PlatformOf(ver, m)
	u.info("php %s — %s", ver, how)
	if platform != "" {
		u.info("platform %s (emulated)", platform)
	}
	if rt.DecodeImage == "" {
		return fmt.Errorf("deep decode not ported for PHP %s (no decode_image)", ver)
	}
	if err := ensureImage(u, "decode", rt.DecodeImage, ver, func() error {
		return docker.EnsureDecodeImage(ver, rt.DecodeImage, platform, extDirContext(opt.extDir))
	}); err != nil {
		return err
	}

	driverPath, cleanup, err := writeDriver()
	if err != nil {
		return err
	}
	defer cleanup()

	ctx := decodeCtx{
		u: u, ver: ver, platform: platform, decodeImage: rt.DecodeImage,
		driverDir: filepath.Dir(driverPath), guardConsts: opt.guardConsts, timeout: opt.fileTimeout,
	}
	methods, err := revealMethods(ctx, filepath.Dir(file), file)
	if err != nil {
		return err
	}
	if len(methods) == 0 {
		return fmt.Errorf("no methods revealed in %s", filepath.Base(file))
	}
	src, err := renderDecodedSource(file, methods, strings.HasPrefix(ver, "5."))
	if err != nil {
		return fmt.Errorf("render: %w", err)
	}
	mc := methodCount(methods)
	u.info("%d %s recovered in %s — decoded source on stdout", mc, plural(mc, "method", "methods"), dur(time.Since(started)))
	fmt.Print(src)
	return nil
}

/* ================================= build ================================== */

func cmdBuild(args []string, opt options) error {
	m := runtime.Load(opt.runtimesPath)
	ver := m.ProbeVersion()
	if len(args) > 0 {
		ver = args[0]
	}
	platform := detect.PlatformOf(ver, m)
	fmt.Printf(">> building runtime image %s (PHP %s)\n", docker.ImageName(ver), ver)
	if err := docker.EnsureOracleImage(ver, platform, opt.loaderBaseURL); err != nil {
		return err
	}
	fmt.Printf(">> ready: %s\n", docker.ImageName(ver))
	return nil
}

/* ================================= shell =================================== */

func cmdShell(args []string, opt options) error {
	if len(args) < 1 {
		return fmt.Errorf("shell: need a directory")
	}
	dir, err := filepath.Abs(args[0])
	if err != nil {
		return err
	}
	m := runtime.Load(opt.runtimesPath)
	ver := m.ProbeVersion()
	if len(args) > 1 {
		ver = args[1]
	}
	platform := detect.PlatformOf(ver, m)
	if err := docker.EnsureOracleImage(ver, platform, opt.loaderBaseURL); err != nil {
		return err
	}
	_, _, err = docker.Run(
		docker.ImageName(ver),
		[]docker.Mount{{Host: dir, Container: "/target", RO: true}},
		nil,
		docker.RunOpts{Platform: platform, Interactive: true, Entrypoint: "bash"},
	)
	return err
}

/* ============================ skeleton runner ============================= */

// runSkeleton runs the interface-recovery image (analyze.php) over a tree, which
// writes <file>.decoded-source.php skeletons in place, and returns how many the
// container's _decoded_report.json reports as produced.
func runSkeleton(dir, ver, image, platform, loaderBaseURL string, u *ui) (int, error) {
	if err := ensureImage(u, "runtime", docker.ImageName(ver), ver, func() error {
		return docker.EnsureOracleImage(ver, platform, loaderBaseURL)
	}); err != nil {
		return 0, err
	}
	// analyze.php is the image ENTRYPOINT; pass the mounted target as its arg.
	// One container walks the whole tree, so there is no per-file line to give:
	// say what is running instead of going quiet.
	if u.items {
		u.info("reflecting the tree in %s …", image)
	}
	out, errOut, err := docker.Run(
		image,
		[]docker.Mount{{Host: dir, Container: "/target"}}, // read-write: artifacts land in place
		[]string{"/target"},
		docker.RunOpts{Platform: platform, Network: "none"},
	)
	u.traceBlock("analyze", out)
	u.traceBlock("analyze:stderr", errOut)
	if err != nil {
		return 0, err
	}
	return producedCount(filepath.Join(dir, "_decoded_report.json")), nil
}

// ensureImage builds an image if it is missing, saying so first: a cold build is
// minutes of docker output with no explanation of what asked for it.
func ensureImage(u *ui, kind, name, ver string, build func() error) error {
	if docker.ImageExists(name) {
		u.tracef("%s image %s already built", kind, name)
		return nil
	}
	u.head("building %s image %s for PHP %s — first run, this takes a few minutes", kind, name, ver)
	start := time.Now()
	if err := build(); err != nil {
		return err
	}
	u.info("built %s in %s", name, dur(time.Since(start)))
	return nil
}

func producedCount(reportPath string) int {
	b, err := os.ReadFile(reportPath)
	if err != nil {
		return 0
	}
	var r struct {
		Produced []string `json:"produced"`
	}
	if json.Unmarshal(b, &r) != nil {
		return 0
	}
	return len(r.Produced)
}

/* =========================== full-decode pipeline ========================= */

// runFullDecode deep-decodes every encoded file under dir: warm+reveal in the
// version's decode image, adapt the reveal output to []opline.Method, decompile
// to PHP, and write <file>.decoded-source.php. Returns the number written.
// decodeStats is what a tree's decode did, in the terms the summary reports:
// files seen, artifacts written, files that revealed nothing, files that errored.
type decodeStats struct{ files, written, empty, failed int }

// decodeCtx carries the invariants threaded through one file's decode: the runtime
// coordinates (ver / platform / decodeImage) plus the run-wide driver directory,
// guard constants, per-file timeout and ui. Bundling them lets the reveal and
// decode helpers take one ctx instead of the same seven parameters each. It is
// passed by value, so a per-file caller (mirror mode, where one tree can mix
// versions) fills ver/platform/decodeImage on its own copy without disturbing the
// others.
type decodeCtx struct {
	u           *ui
	ver         string
	platform    string
	decodeImage string
	driverDir   string
	guardConsts string
	timeout     time.Duration
}

func runFullDecode(ctx decodeCtx, dir, extDir string, rt runtime.Runtime) (decodeStats, error) {
	var st decodeStats
	if rt.DecodeImage == "" {
		return st, fmt.Errorf("deep decode not ported for PHP %s (no decode_image); rerun with --skeleton", ctx.ver)
	}
	if err := ensureImage(ctx.u, "decode", rt.DecodeImage, ctx.ver, func() error {
		return docker.EnsureDecodeImage(ctx.ver, rt.DecodeImage, ctx.platform, extDirContext(extDir))
	}); err != nil {
		return st, err
	}

	driverPath, cleanup, err := writeDriver()
	if err != nil {
		return st, err
	}
	defer cleanup()
	ctx.driverDir = filepath.Dir(driverPath)
	ctx.decodeImage = rt.DecodeImage
	ctx.u.tracef("warm+reveal driver written to %s", driverPath)

	files := findEncodedFiles(dir)
	st.files = len(files)
	if ctx.u.items {
		ctx.u.info("%d encoded %s, decoding with %s", st.files, plural(st.files, "file", "files"), rt.DecodeImage)
	}

	for i, f := range files {
		if docker.Interrupted() {
			ctx.u.info("interrupted — stopping after %d of %d file(s)", i, len(files))
			break
		}
		it := ctx.u.item(i+1, len(files), relOf(dir, f))

		methods, err := revealMethods(ctx, dir, f)
		if err != nil {
			st.failed++
			it.fail("%v", err)
			continue
		}
		if len(methods) == 0 {
			st.empty++
			it.skip("no methods revealed")
			continue
		}
		src, err := renderDecodedSource(f, methods, strings.HasPrefix(ctx.ver, "5."))
		if err != nil {
			st.failed++
			it.fail("render: %v", err)
			continue
		}
		dest := strings.TrimSuffix(f, ".php") + artifactSuffix
		if err := os.WriteFile(dest, []byte(src), 0o644); err != nil {
			return st, err
		}
		st.written++
		mc := methodCount(methods)
		it.ok("%d %s → %s", mc, plural(mc, "method", "methods"), filepath.Base(dest))
	}
	return st, nil
}

// revealMethods runs one encoded file through the decode image's warm+reveal
// driver and returns the recovered oplines as []opline.Method. timeout is the
// per-file watchdog: the container is force-removed if it exceeds it, and the
// output captured up to that point is still mined for a reveal (the driver
// flushes a structural reveal BEFORE the warm loop that can run away), so a
// pathological file degrades to a structural decode or a clean error, never a
// hang.
func revealMethods(ctx decodeCtx, root, file string) ([]opline.Method, error) {
	rel := relOf(root, file)
	// Case-preserved basename (sans .php): it is always a substring of this file's
	// own /enc/<rel> path, so the shim's filename filter selects exactly this
	// file's methods. (Lowercasing broke the match on mixed-case names like
	// TextFormat.class.php.)
	filter := strings.TrimSuffix(filepath.Base(file), ".php")

	// Forward the guard-constant list (if the caller supplied one via --guard-consts)
	// into the container so driver.php can define app-specific bootstrap guards
	// without them being baked into the tool. See driver.php and docs/USER-GUIDE.md.
	var env []string
	if ctx.guardConsts != "" {
		env = []string{guardConstsEnv + "=" + ctx.guardConsts}
	}

	out, errOut, runErr := docker.Run(
		ctx.decodeImage,
		[]docker.Mount{
			{Host: ctx.driverDir, Container: "/drv", RO: true},
			{Host: root, Container: "/enc"},
		},
		[]string{"/drv/driver.php", "/enc/" + rel, filter},
		docker.RunOpts{Platform: ctx.platform, Network: "none", Env: env, Timeout: ctx.timeout},
	)
	// The container's own diagnostics are where a failed reveal explains itself,
	// and they are captured (not streamed) so the JSON stays parseable — under
	// --verbose they get shown rather than dropped.
	ctx.u.traceBlock("reveal:stderr", errOut)

	timedOut := errors.Is(runErr, docker.ErrTimeout)

	// PRECEDENCE: the authoritative shutdown reveal on STDOUT wins — even an empty
	// one — so a finished run never falls through to the STDERR safety net. Only
	// when STDOUT carried no reveal (the container was killed mid-run) does the
	// pre-warm safety net on STDERR get a turn.
	if methods, found, err := extractPrimaryReveal(out, ctx.ver, ctx.u); found {
		return methods, err
	}
	if methods := extractSafetyNet(errOut, ctx.u); len(methods) > 0 {
		return methods, nil
	}
	if timedOut {
		// The watchdog fired and no usable reveal survived: a clean per-file error
		// (the run continues to the next file) rather than a hang.
		return nil, fmt.Errorf("decode timed out (%v) before a usable reveal — file likely runs an unbounded bootstrap/warm; skipped", runErr)
	}
	return nil, fmt.Errorf("no reveal output (deionizer_reveal_json/deionizer_reveal_dump unavailable in %s); stderr: %s",
		ctx.decodeImage, firstLine(errOut))
}

// extractPrimaryReveal reads the authoritative shutdown reveal off the container's
// STDOUT: a JSON reveal (deionizer_reveal_json) if present, else a text reveal
// (deionizer_reveal_dump). found is true whenever such a block was present at all — an
// empty JSON reveal is still authoritative and MUST NOT fall through to the safety
// net — so the caller returns (methods, err) verbatim on found. Trace and
// reflection enrichment happen here because they belong to this finished run.
func extractPrimaryReveal(out, ver string, u *ui) (methods []opline.Method, found bool, err error) {
	if j := between(out, "\x1eICO_JSON_BEGIN\x1e", "\x1eICO_JSON_END\x1e"); j != "" {
		// C shim exposed deionizer_reveal_json -> []opline.Method JSON directly.
		methods, err = parseMethodsJSON(j)
		if err != nil {
			return nil, true, err
		}
		u.tracef("json reveal — %d %s, %d oplines", len(methods), plural(len(methods), "method", "methods"), oplineCount(methods))
		// 5.x scalar recovery: fill encrypted CONSTs from the driver's behavioral
		// trace (the passive reveal leaves Zend-2.6 scalar literals encrypted; the
		// zend_execute_internal side-channel logs the real decrypted built-in args).
		applyTrace(methods, out)
		// 5.x class metadata: fill property defaults + array/expr constant values the
		// driver recovered via reflection (the loader's own in-place decode).
		applyReflectMeta(methods, between(out, "\x1eICO_META_BEGIN\x1e", "\x1eICO_META_END\x1e"))
		return methods, true, nil
	}
	if t := between(out, "\x1eICO_TEXT_BEGIN\x1e", "\x1eICO_TEXT_END\x1e"); t != "" {
		// Text reveal (deionizer_reveal_dump) -> decompile.ParseTextDump bridge.
		methods, err = decodeText(t, strings.HasPrefix(ver, "5."))
		if err != nil {
			return nil, true, err
		}
		u.tracef("text reveal — %d %s, %d oplines", len(methods), plural(len(methods), "method", "methods"), oplineCount(methods))
		applyTrace(methods, out)
		return methods, true, nil
	}
	return nil, false, nil
}

// extractSafetyNet reads the pre-warm structural reveal the driver flushes to
// STDERR under its own sentinels before the warm loop that can run away. It is used
// only when no primary reveal survived on STDOUT (the container was killed mid-run:
// a watchdog timeout, a Ctrl-C, or a crash before shutdown), so a pathological file
// still decodes structurally instead of failing. Trace enrichment is skipped — those
// lines belong to the run that got killed. Returns nil when the block is absent,
// unparseable, or empty.
func extractSafetyNet(errOut string, u *ui) []opline.Method {
	j := between(errOut, "\x1eICO_EARLY_JSON_BEGIN\x1e", "\x1eICO_EARLY_JSON_END\x1e")
	if j == "" {
		return nil
	}
	methods, err := parseMethodsJSON(j)
	if err != nil || len(methods) == 0 {
		return nil
	}
	u.tracef("safety-net reveal (pre-warm, STDERR) — %d %s, %d oplines",
		len(methods), plural(len(methods), "method", "methods"), oplineCount(methods))
	return methods
}

func parseMethodsJSON(s string) ([]opline.Method, error) {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '['); i >= 0 {
		if j := strings.LastIndexByte(s, ']'); j > i {
			s = s[i : j+1]
		}
	}
	var methods []opline.Method
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber() // keep integer literals exact; float64 would break === and array keys
	if err := dec.Decode(&methods); err != nil {
		return nil, fmt.Errorf("parse reveal JSON: %w", err)
	}
	opline.NormalizeMethods(methods)
	return methods, nil
}

// reflectMeta is the per-class reflection metadata the 5.x driver emits between
// the ICO_META sentinels: property defaults and class-constant values the loader
// decodes in place when reflection reads them (getDefaultProperties / getConstants).
// The passive C reveal cannot serialize these (defaults are encrypted at rest,
// array constants render null), so Go fills them onto the class-info by name.
type reflectMeta struct {
	Properties map[string]interface{} `json:"properties"`
	Constants  map[string]interface{} `json:"constants"`
	// PropInfo carries property readonly + declared type (8.1+ reflection). Keyed by
	// property name; each value is {readonly?:bool, type?:string}.
	PropInfo map[string]propInfo `json:"prop_info"`
}

type propInfo struct {
	ReadOnly bool   `json:"readonly"`
	Type     string `json:"type"`
}

// applyReflectMeta merges the driver's reflection metadata onto the CLASS-INFO
// records: a property gains its recovered default, a constant its recovered value
// (including arrays). Scalars-only C emissions are overridden by the richer
// reflection value. A no-op when the block is absent (7.x) or empty.
func applyReflectMeta(methods []opline.Method, metaJSON string) {
	metaJSON = strings.TrimSpace(metaJSON)
	if metaJSON == "" || metaJSON == "{}" {
		return
	}
	var meta map[string]reflectMeta
	dec := json.NewDecoder(strings.NewReader(metaJSON))
	dec.UseNumber() // keep ints exact (const PI = 3, not 3.0)
	if err := dec.Decode(&meta); err != nil {
		return
	}
	for i := range methods {
		m := &methods[i]
		if m.Kind == "" || m.Kind == "main" {
			continue // only class-info records carry props/consts
		}
		cm, ok := meta[m.Class]
		if !ok {
			continue
		}
		for p := range m.Properties {
			if v, ok := cm.Properties[m.Properties[p].Name]; ok {
				m.Properties[p].HasDefault = true
				m.Properties[p].Default = opline.NormalizeJSONValue(v)
			}
			if pi, ok := cm.PropInfo[m.Properties[p].Name]; ok {
				m.Properties[p].ReadOnly = pi.ReadOnly
				m.Properties[p].Type = pi.Type
			}
		}
		for c := range m.Constants {
			if v, ok := cm.Constants[m.Constants[c].Name]; ok {
				m.Constants[c].Value = opline.NormalizeJSONValue(v)
			}
		}
	}
}

// renderDecodedSource turns the recovered methods into a PHP source file.
//
// The oplines->PHP step goes through renderMethodsToPHP, which is the CLI's
// integration seam with the decompiler agent: the default build uses an interim
// exact-signature renderer (renderMethods); a build with `-tags decompiler` wires
// the real decompile.RenderFile once internal/decompile compiles cleanly.
func renderDecodedSource(file string, methods []opline.Method, zend56 bool) (string, error) {
	body, err := renderMethodsToPHP(methods, zend56)
	if err != nil {
		return "", err
	}
	// decompile.RenderFile may emit its own "<?php" opener; drop it so our
	// required header is the single file header.
	body = strings.TrimPrefix(strings.TrimLeft(body, " \t\n"), "<?php")
	body = strings.TrimLeft(body, " \t\n")

	var b strings.Builder
	b.WriteString("<?php\n/**\n")
	b.WriteString(" * Decompiled from ionCube opcodes via deionizer.\n")
	b.WriteString(" * Source: " + filepath.Base(file) + "\n")
	b.WriteString(" */\n\n")
	b.WriteString(body)
	return b.String(), nil
}

// renderMethods is the interim oplines->PHP renderer (see renderDecodedSource).
// Groups methods by class and emits exact signatures with body placeholders.
func renderMethods(methods []opline.Method) (string, error) {
	byClass := map[string][]opline.Method{}
	var order []string
	for _, m := range methods {
		if _, seen := byClass[m.Class]; !seen {
			order = append(order, m.Class)
		}
		byClass[m.Class] = append(byClass[m.Class], m)
	}

	var b strings.Builder
	for _, cls := range order {
		ms := byClass[cls]
		if cls == "" {
			for _, m := range ms {
				b.WriteString(renderFunction(m, ""))
				b.WriteString("\n")
			}
			continue
		}
		b.WriteString("class " + cls + " {\n")
		for _, m := range ms {
			b.WriteString(renderFunction(m, "    "))
		}
		b.WriteString("}\n\n")
	}
	return b.String(), nil
}

func renderFunction(m opline.Method, indent string) string {
	var sig strings.Builder
	sig.WriteString(indent)
	if m.Class != "" {
		if m.Vis != "" {
			sig.WriteString(m.Vis + " ")
		}
		if m.Static {
			sig.WriteString("static ")
		}
	}
	sig.WriteString("function " + m.Function + "(" + renderParams(m.Params) + ")")
	if m.Abstract {
		sig.WriteString(";\n")
		return sig.String()
	}
	sig.WriteString(" {\n")
	sig.WriteString(indent + "    /* body: " + fmt.Sprintf("%d", len(m.Oplines)) + " oplines — full decompiler (decompile.RenderFile) pending */\n")
	sig.WriteString(indent + "}\n")
	return sig.String()
}

func renderParams(params []opline.Param) string {
	parts := make([]string, 0, len(params))
	for _, p := range params {
		s := ""
		if p.ByRef {
			s += "&"
		}
		if p.Variadic {
			s += "..."
		}
		s += "$" + p.Name
		if p.HasDefault {
			b, _ := json.Marshal(p.Default)
			s += " = " + string(b)
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, ", ")
}

/* ================================ helpers ================================= */

// writeDriver materialises the embedded warm+reveal driver into a temp dir so it
// can be bind-mounted into the container.
func writeDriver() (path string, cleanup func(), err error) {
	d, err := os.MkdirTemp("", "deionizer-driver-*")
	if err != nil {
		return "", func() {}, err
	}
	p := filepath.Join(d, "driver.php")
	if err := os.WriteFile(p, []byte(driverPHP), 0o644); err != nil {
		os.RemoveAll(d)
		return "", func() {}, err
	}
	return p, func() { os.RemoveAll(d) }, nil
}

// extDirContext locates the decode-extension source tree — the build context
// for the deep-decode images. This tree is NOT shipped with the tool; deep decode
// requires you to supply your own decode-extension source. The caller passes an
// explicit override (from --ext-dir); when empty this falls back to a neutral
// local path (tmp/deionizer-ext). Returns "" when nothing is found, which callers
// turn into a clean "deep decode not available" error — the interface-recovery
// (skeleton) path needs none of this.
func extDirContext(override string) string {
	cands := []string{}
	if override != "" {
		cands = append(cands, override)
	}
	cands = append(cands, "tmp/deionizer-ext", "../tmp/deionizer-ext")
	for _, c := range cands {
		if abs, err := filepath.Abs(c); err == nil {
			if fi, err := os.Stat(abs); err == nil && fi.IsDir() {
				return abs
			}
		}
	}
	return ""
}

// findProjects returns the immediate subdirectories of tree that contain encoded
// PHP; if none qualify, tree itself when it directly holds encoded PHP.
func findProjects(tree string) []string {
	entries, err := os.ReadDir(tree)
	if err != nil {
		return nil
	}
	var projects []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(tree, e.Name())
		if sampleEncoded(p) != "" {
			projects = append(projects, p)
		}
	}
	if len(projects) == 0 && sampleEncoded(tree) != "" {
		projects = []string{tree}
	}
	sort.Strings(projects)
	return projects
}

// sampleEncoded returns the first (sorted) ionCube-encoded .php file under dir.
func sampleEncoded(dir string) string {
	files := findEncodedFiles(dir)
	if len(files) == 0 {
		return ""
	}
	return files[0]
}

// findEncodedFiles walks dir for ionCube-encoded .php files, skipping our own
// .decoded-source.php artifacts. Sorted for deterministic sampling.
func findEncodedFiles(dir string) []string {
	var out []string
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.EqualFold(filepath.Ext(path), ".php") {
			return nil
		}
		if strings.HasSuffix(path, artifactSuffix) {
			return nil
		}
		if detect.IsEncoded(path) {
			out = append(out, path)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// oplineCount totals the recovered oplines across methods — the one number that
// says whether a reveal got real bodies or just signatures.
func oplineCount(methods []opline.Method) int {
	n := 0
	for _, m := range methods {
		n += len(m.Oplines)
	}
	return n
}

// methodCount counts only real functions/methods, excluding the class-info
// records (Kind!="") that ride in the same slice — those describe the class
// itself, not a method, so they must not inflate the reported method count.
func methodCount(methods []opline.Method) int {
	n := 0
	for _, m := range methods {
		if m.Kind == "" {
			n++
		}
	}
	return n
}

func relOf(root, file string) string {
	if r, err := filepath.Rel(root, file); err == nil {
		return r
	}
	return filepath.Base(file)
}

func between(s, begin, end string) string {
	i := strings.Index(s, begin)
	if i < 0 {
		return ""
	}
	i += len(begin)
	j := strings.Index(s[i:], end)
	if j < 0 {
		return ""
	}
	return s[i : i+j]
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func imageOr(primary, fallback string) string {
	if primary != "" {
		return primary
	}
	return fallback
}
