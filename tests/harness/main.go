// Command harness is the RED-GREEN correctness harness for the deionizer
// decoder. For every ground-truth PHP fixture, PHP version and obfuscation level
// it: encodes the fixture with the official ionCube trial encoder, decodes it
// with our tool (`deionizer decode`), then diffs the decoded PHP against the original
// at three tiers — artifact produced, lints clean, and (the headline metric)
// reproduces the original's runtime output byte-for-byte.
//
// It is deliberately self-contained: stdlib only, no imports of the project's
// other packages, and it never touches go.mod. Encoding runs the x86-64 Linux
// encoder inside a linux/amd64 container; behavioral comparison runs inside the
// version-matched deionizer php image.
//
// Output: a machine-readable results.json and a Markdown dashboard.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

/* ------------------------------- config ---------------------------------- */

type config struct {
	repoRoot     string
	decoder      string
	encoderDir   string // host dir holding ioncube_encoderNN_15.0_64 binaries
	encoderImage string // linux/amd64 image used to run the x86-64 encoder
	runtimesYML  string
	fixturesDir  string
	driverDir    string
	workDir      string
	resultsPath  string
	dashboard    string
	methods      []string // encoding-method axis (plain, optimise-*, obfuscate-all, dynamic-keys)
	versionsOnly []string // optional explicit version filter
	fixtureOnly  []string // optional explicit fixture-name filter
}

// obfuscationKey is the fixed key used for the obfuscate-all method so runs are
// reproducible byte-for-byte.
const obfuscationKey = "deionizer-harness"

// methodDynkeys is handled out-of-band (its own annotated fixture), not as a
// per-construct column in the main encode loop.
const methodDynkeys = "dynamic-keys"

// allMethods is the full encoding-method sweep, in matrix order.
func allMethods() []string {
	return []string{"plain", "optimise-none", "optimise-more", "optimise-max", "obfuscate-all", methodDynkeys}
}

// methodEncodeArgs maps an encoding method to the extra trial-encoder flags it
// adds on top of the common `<src> -o <enc> --replace-target`. The bool reports
// whether the method is a recognised regular (per-construct) method.
//
// Note: the encoder rejects an explicit `--optimise none` ("Only 'more' or 'max'
// are valid"); omitting the flag IS the no-optimisation baseline, so both plain
// and optimise-none encode with no --optimise flag and are equivalent.
func methodEncodeArgs(method string) ([]string, bool) {
	switch method {
	case "plain", "optimise-none":
		return nil, true
	case "optimise-more":
		return []string{"--optimise", "more"}, true
	case "optimise-max":
		return []string{"--optimise", "max"}, true
	case "obfuscate-all":
		return []string{"--obfuscate", "all", "--obfuscation-key", obfuscationKey}, true
	}
	return nil, false
}

// resolveMethods picks the encoding-method axis from the environment. METHODS
// takes precedence; otherwise the legacy OBF filter is translated (none->plain,
// all->obfuscate-all); with neither set the full sweep runs.
func resolveMethods() []string {
	if v := os.Getenv("METHODS"); v != "" {
		return strings.Fields(v)
	}
	if v := os.Getenv("OBF"); v != "" {
		var out []string
		for _, o := range strings.Fields(v) {
			switch o {
			case "none":
				out = append(out, "plain")
			case "all":
				out = append(out, "obfuscate-all")
			default:
				out = append(out, o) // allow OBF to name a method directly
			}
		}
		return out
	}
	return allMethods()
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func loadConfig() config {
	root, _ := os.Getwd()
	c := config{
		repoRoot:     root,
		decoder:      env("DECODER", "/tmp/deionizer"),
		encoderDir:   env("ENCODER_DIR", filepath.Join(root, "examples/ioncube-trial/ioncube_encoder_evaluation/bin")),
		encoderImage: env("ENCODER_IMAGE", "debian:bookworm-slim"),
		runtimesYML:  env("RUNTIMES", filepath.Join(root, "runtimes.yml")),
		fixturesDir:  env("FIXTURES_DIR", filepath.Join(root, "tests/fixtures")),
		driverDir:    env("DRIVER_DIR", filepath.Join(root, "tests/driver")),
		workDir:      env("WORK_DIR", filepath.Join(root, "tests/results/work")),
		resultsPath:  env("RESULTS", filepath.Join(root, "tests/results/results.json")),
		dashboard:    env("DASHBOARD", filepath.Join(root, "tests/results/dashboard.md")),
		methods:      resolveMethods(),
	}
	if v := os.Getenv("VERSIONS"); v != "" {
		c.versionsOnly = strings.Fields(v)
	}
	if v := os.Getenv("FIXTURES"); v != "" {
		c.fixtureOnly = strings.Fields(v)
	}
	return c
}

/* ------------------------------- model ----------------------------------- */

type fixture struct {
	Name      string
	Path      string
	Construct string
	Min, Max  float64
}

type runtime struct {
	Ver         string
	Platform    string
	Image       string
	DecodeImage string
}

// Cell is one (construct x version x method) result.
type Cell struct {
	Construct  string  `json:"construct"`
	Version    string  `json:"version"`
	Method     string  `json:"method"`
	Applicable bool    `json:"applicable"`
	Status     string  `json:"status"` // ok | na | pending
	Reason     string  `json:"reason,omitempty"`
	Artifact   bool    `json:"artifact"`
	Lint       string  `json:"lint"` // pass | fail | na
	LintMsg    string  `json:"lint_msg,omitempty"`
	Behavior   string  `json:"behavior"` // pass | fail | error | na | pending
	Expected   string  `json:"expected"`
	Actual     string  `json:"actual"`
	Similarity float64 `json:"similarity_pct"`
}

type Results struct {
	Generated string         `json:"generated"`
	Decoder   string         `json:"decoder"`
	Encoder   string         `json:"encoder"`
	Versions  []string       `json:"versions_run"`
	Pending   []PendingVer   `json:"pending_versions"`
	Methods   []string       `json:"methods"`
	Summary   Summary        `json:"summary"`
	Cells     []Cell         `json:"cells"`
	Dynkeys   *DynkeysReport `json:"dynkeys"`
}

type PendingVer struct {
	Version string `json:"version"`
	Reason  string `json:"reason"`
}

type Summary struct {
	ApplicableCells int                 `json:"applicable_cells"`
	ArtifactCount   int                 `json:"artifact"`
	LintPass        int                 `json:"lint_pass"`
	BehaviorPass    int                 `json:"behavior_pass"`
	SuccessRatePct  float64             `json:"success_rate_pct"`
	ByMethod        map[string]RatePair `json:"by_method"`
	ByVersion       map[string]RatePair `json:"by_version"`
}

type RatePair struct {
	Pass  int     `json:"pass"`
	Total int     `json:"total"`
	Pct   float64 `json:"pct"`
}

type DynkeysReport struct {
	Encoded      bool            `json:"encoded"`
	EncodeNote   string          `json:"encode_note"`
	Artifact     bool            `json:"artifact"`
	DecodeNote   string          `json:"decode_note"`
	RecoveredLen int             `json:"recovered_len"`
	Snippet      string          `json:"snippet"`
	Methods      map[string]bool `json:"methods_recovered"` // method name -> body appears in recovered source
	DkProtected  []string        `json:"dk_protected"`      // methods carrying @ioncube.dk annotations
	Version      string          `json:"version"`           // runtime the observation ran on
	Behavior     string          `json:"behavior"`          // pass | fail | error — decoded whole-program output vs original
	Expected     string          `json:"expected"`          // original fixture's full stdout
	Actual       string          `json:"actual"`            // decoded artifact's full stdout
}

/* -------------------------------- main ----------------------------------- */

func main() {
	c := loadConfig()
	if _, err := os.Stat(c.decoder); err != nil {
		fatal("decoder not found at %s (set DECODER=...)", c.decoder)
	}
	fixtures := loadFixtures(c)
	if len(fixtures) == 0 {
		fatal("no fixtures under %s", c.fixturesDir)
	}
	allRT := loadRuntimes(c.runtimesYML)

	var runnable []runtime
	var pending []PendingVer
	for _, rt := range allRT {
		if len(c.versionsOnly) > 0 && !contains(c.versionsOnly, rt.Ver) {
			continue
		}
		if rt.DecodeImage == "" {
			pending = append(pending, PendingVer{rt.Ver, "no decode_image in runtimes.yml (deep decode not ported)"})
			continue
		}
		runnable = append(runnable, rt)
	}

	// Split the method axis: regular methods drive the per-construct matrix;
	// dynamic-keys is an out-of-band observation on its own annotated fixture.
	var regular []string
	runDK := false
	for _, m := range c.methods {
		if m == methodDynkeys {
			runDK = true
			continue
		}
		if _, ok := methodEncodeArgs(m); !ok {
			fmt.Printf(">> skipping unknown method %q\n", m)
			continue
		}
		regular = append(regular, m)
	}

	fmt.Printf(">> harness: %d fixtures x %d versions x %d methods%s\n",
		len(fixtures), len(runnable), len(regular), dkNote(runDK))

	os.MkdirAll(c.workDir, 0o755)
	os.MkdirAll(filepath.Dir(c.resultsPath), 0o755)

	var cells []Cell
	for _, rt := range runnable {
		for _, m := range regular {
			fmt.Printf(">> cell group: PHP %s / method=%s\n", rt.Ver, m)
			cells = append(cells, runGroup(c, rt, m, fixtures)...)
		}
	}

	var dk *DynkeysReport
	if runDK {
		dk = runDynkeys(c, runnable)
	}

	res := assemble(c, runnable, pending, regular, cells, dk)
	writeResults(c.resultsPath, res)
	writeDashboard(c.dashboard, res)

	fmt.Printf(">> overall behavioral success rate: %.1f%% (%d/%d applicable cells)\n",
		res.Summary.SuccessRatePct, res.Summary.BehaviorPass, res.Summary.ApplicableCells)
	fmt.Printf(">> results:   %s\n", c.resultsPath)
	fmt.Printf(">> dashboard: %s\n", c.dashboard)
}

/* ---------------------------- group execution ---------------------------- */

// runGroup encodes every applicable fixture for one (version, method), decodes
// the tree in one pass, then batch-compares originals vs decoded artifacts.
func runGroup(c config, rt runtime, method string, fixtures []fixture) []Cell {
	verF := mustFloat(rt.Ver)

	// Partition into applicable / not-applicable-for-this-version.
	var applic []fixture
	var cells []Cell
	for _, f := range fixtures {
		if verF+1e-9 < f.Min || verF > f.Max+1e-9 {
			cells = append(cells, Cell{
				Construct: f.Construct, Version: rt.Ver, Method: method,
				Applicable: false, Status: "na",
				Reason: fmt.Sprintf("construct needs PHP %s..%s", trimZero(f.Min), trimZero(f.Max)),
				Lint:   "na", Behavior: "na",
			})
			continue
		}
		applic = append(applic, f)
	}
	if len(applic) == 0 {
		return cells
	}

	cellDir := filepath.Join(c.workDir, fmt.Sprintf("php%s-%s", rt.Ver, method))
	os.RemoveAll(cellDir)
	srcDir := filepath.Join(cellDir, "src")
	encDir := filepath.Join(cellDir, "enc")
	os.MkdirAll(srcDir, 0o755)

	for _, f := range applic {
		copyFile(f.Path, filepath.Join(srcDir, f.Name+".php"))
	}
	// driver + comparator alongside, so a single mount reaches them.
	copyFile(filepath.Join(c.driverDir, "run_probe.php"), filepath.Join(cellDir, "run_probe.php"))
	copyFile(filepath.Join(c.driverDir, "compare.php"), filepath.Join(cellDir, "compare.php"))

	// 1. encode the whole dir at once.
	if err := encodeDir(c, rt, method, cellDir); err != nil {
		fmt.Printf("   [encode] PHP %s/%s: %v\n", rt.Ver, method, err)
	}
	// 2. decode the whole tree at once.
	decodeTree(c, rt.Ver, encDir)

	// 3. build manifest + batch compare.
	type item struct {
		Name     string `json:"name"`
		Original string `json:"original"`
		Decoded  string `json:"decoded"`
	}
	var items []item
	for _, f := range applic {
		items = append(items, item{
			Name:     f.Name,
			Original: "/orig/" + f.Name + ".php",
			Decoded:  "/work/enc/" + f.Name + artifactSuffix,
		})
	}
	manifest := map[string]interface{}{"driver": "/work/run_probe.php", "items": items}
	mb, _ := json.Marshal(manifest)
	os.WriteFile(filepath.Join(cellDir, "manifest.json"), mb, 0o644)

	cmp := batchCompare(c, rt, cellDir)

	byName := map[string]compareResult{}
	for _, r := range cmp {
		byName[r.Name] = r
	}
	for _, f := range applic {
		r := byName[f.Name]
		decodedHost := filepath.Join(encDir, f.Name+artifactSuffix)
		sim := similarity(readFile(f.Path), stripDecodedHeader(readFile(decodedHost)))
		cells = append(cells, Cell{
			Construct:  f.Construct,
			Version:    rt.Ver,
			Method:     method,
			Applicable: true,
			Status:     "ok",
			Artifact:   r.Artifact,
			Lint:       orNA(r.Lint),
			LintMsg:    r.LintMsg,
			Behavior:   orFail(r.Behavior),
			Expected:   r.Expected,
			Actual:     r.Actual,
			Similarity: sim,
		})
	}
	return cells
}

const artifactSuffix = ".decoded-source.php"

/* ------------------------------ encode ----------------------------------- */

func encodeDir(c config, rt runtime, method, cellDir string) error {
	nn := strings.ReplaceAll(rt.Ver, ".", "")
	bin := "/enc/ioncube_encoder" + nn + "_15.0_64"
	args := []string{
		"run", "--rm", "--platform", "linux/amd64",
		"-v", c.encoderDir + ":/enc:ro",
		"-v", cellDir + ":/work",
		c.encoderImage,
		bin, "/work/src", "-o", "/work/enc", "--replace-target",
	}
	extra, _ := methodEncodeArgs(method)
	args = append(args, extra...)
	out, err := runCmd(c.repoRoot, "docker", args...)
	if err != nil {
		return fmt.Errorf("%v: %s", err, firstLine(out))
	}
	return nil
}

/* ------------------------------ decode ----------------------------------- */

func decodeTree(c config, ver, encDir string) {
	if _, err := os.Stat(encDir); err != nil {
		return // nothing encoded
	}
	// deionizer decode writes <file>.decoded-source.php next to each encoded file.
	runCmd(c.repoRoot, c.decoder, "decode", encDir, "--php", ver)
}

/* ------------------------------ compare ---------------------------------- */

type compareResult struct {
	Name     string `json:"name"`
	Artifact bool   `json:"artifact"`
	Lint     string `json:"lint"`
	LintMsg  string `json:"lint_msg"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
	Behavior string `json:"behavior"`
}

func batchCompare(c config, rt runtime, cellDir string) []compareResult {
	args := []string{"run", "--rm", "--entrypoint", "php"}
	if rt.Platform != "" {
		args = append(args, "--platform", rt.Platform)
	}
	args = append(args,
		"-v", cellDir+":/work",
		"-v", c.fixturesDir+":/orig:ro",
		rt.Image,
		"/work/compare.php", "/work/manifest.json",
	)
	out, err := runCmd(c.repoRoot, "docker", args...)
	if err != nil {
		fmt.Printf("   [compare] PHP %s: %v: %s\n", rt.Ver, err, firstLine(out))
	}
	var parsed struct {
		Results []compareResult `json:"results"`
	}
	if e := json.Unmarshal([]byte(extractJSON(out)), &parsed); e != nil {
		fmt.Printf("   [compare] PHP %s: bad JSON: %v\n", rt.Ver, e)
	}
	return parsed.Results
}

/* ------------------------------ dynkeys ---------------------------------- */

func runDynkeys(c config, runnable []runtime) *DynkeysReport {
	// Prefer 7.4 for the dynamic-keys observation (the documented probe target).
	var rt *runtime
	for i := range runnable {
		if runnable[i].Ver == "7.4" {
			rt = &runnable[i]
		}
	}
	if rt == nil && len(runnable) > 0 {
		rt = &runnable[len(runnable)-1]
	}
	if rt == nil {
		return nil
	}
	src := filepath.Join(c.fixturesDir, "dynkeys", "probe-dynkeys.php")
	if _, err := os.Stat(src); err != nil {
		return nil
	}
	rep := &DynkeysReport{Version: rt.Ver}
	cellDir := filepath.Join(c.workDir, "dynkeys")
	os.RemoveAll(cellDir)
	srcDir := filepath.Join(cellDir, "src")
	encDir := filepath.Join(cellDir, "enc")
	os.MkdirAll(srcDir, 0o755)
	copyFile(src, filepath.Join(srcDir, "probe-dynkeys.php"))
	copyFile(filepath.Join(c.driverDir, "run_probe.php"), filepath.Join(cellDir, "run_probe.php"))

	// Dynamic keys are driven by the //@ioncube.dk annotations in the source,
	// NOT a CLI flag; encode with default options (no --obfuscate).
	nn := strings.ReplaceAll(rt.Ver, ".", "")
	bin := "/enc/ioncube_encoder" + nn + "_15.0_64"
	out, err := runCmd(c.repoRoot, "docker",
		"run", "--rm", "--platform", "linux/amd64",
		"-v", c.encoderDir+":/enc:ro", "-v", cellDir+":/work",
		c.encoderImage, bin, "/work/src", "-o", "/work/enc", "--replace-target",
	)
	rep.EncodeNote = firstLine(out)
	if err != nil {
		rep.EncodeNote = fmt.Sprintf("encode failed: %v: %s", err, firstLine(out))
		return rep
	}
	enc := filepath.Join(encDir, "probe-dynkeys.php")
	if b, e := os.ReadFile(enc); e == nil && len(b) > 0 {
		rep.Encoded = true
	}
	decodeTree(c, rt.Ver, encDir)
	dec := filepath.Join(encDir, "probe-dynkeys"+artifactSuffix)
	// Methods present in the original; the two carrying @ioncube.dk annotations
	// are the security-relevant ones (their bytecode stays encrypted until first
	// call with the correct runtime key, so a passive reveal should not see them).
	origSrc := readFile(src)
	origMethods := []string{"__construct", "checkVendorCode", "classify", "summarize", "transform"}
	rep.DkProtected = dkAnnotatedMethods(origSrc)

	if b, e := os.ReadFile(dec); e == nil && len(b) > 0 {
		rep.Artifact = true
		body := stripDecodedHeader(string(b))
		rep.RecoveredLen = len(strings.TrimSpace(body))
		rep.Snippet = snippet(body, 40)
		rep.Methods = map[string]bool{}
		for _, m := range origMethods {
			// a recovered body is present if the method name appears followed by "("
			rep.Methods[m] = strings.Contains(body, m+"(") || strings.Contains(body, m+" (")
		}
		rep.DecodeNote = "decoder produced an artifact; the dynamic-key-protected method bodies were withheld by the Loader (see per-method recovery)"
	} else {
		rep.DecodeNote = "decoder produced NO artifact for the dynamic-key-encoded file"
	}

	// Behavioral grade (name-independent, same driver as the main matrix): run the
	// ORIGINAL and the DECODED artifact as whole programs and diff their stdout.
	// The protected bodies stay encrypted, so the decode cannot reproduce the full
	// output — this is the recorded observation, not a target to defeat.
	rep.Expected = runDriverInImage(c, *rt, cellDir, "/work/src/probe-dynkeys.php")
	rep.Actual = runDriverInImage(c, *rt, cellDir, "/work/enc/probe-dynkeys"+artifactSuffix)
	switch {
	case strings.TrimSpace(rep.Expected) == "":
		rep.Behavior = "error"
	case rep.Actual == rep.Expected:
		rep.Behavior = "pass"
	default:
		rep.Behavior = "fail"
	}
	return rep
}

// runDriverInImage runs run_probe.php on one target inside the version-matched
// php image and returns its stdout (stderr discarded), mirroring compare.php's
// per-file capture for the single dynamic-keys observation.
func runDriverInImage(c config, rt runtime, cellDir, targetRel string) string {
	args := []string{"run", "--rm", "--entrypoint", "php"}
	if rt.Platform != "" {
		args = append(args, "--platform", rt.Platform)
	}
	args = append(args, "-v", cellDir+":/work", rt.Image, "/work/run_probe.php", targetRel)
	out, _ := runCmd(c.repoRoot, "docker", args...)
	return out
}

/* ----------------------------- assemble ---------------------------------- */

func assemble(c config, runnable []runtime, pending []PendingVer, methods []string, cells []Cell, dk *DynkeysReport) Results {
	sort.Slice(cells, func(i, j int) bool {
		if cells[i].Construct != cells[j].Construct {
			return cells[i].Construct < cells[j].Construct
		}
		if cells[i].Version != cells[j].Version {
			return cells[i].Version < cells[j].Version
		}
		return cells[i].Method < cells[j].Method
	})

	var vers []string
	for _, rt := range runnable {
		vers = append(vers, rt.Ver)
	}
	sum := Summary{ByMethod: map[string]RatePair{}, ByVersion: map[string]RatePair{}}
	for _, cell := range cells {
		if !cell.Applicable {
			continue
		}
		sum.ApplicableCells++
		if cell.Artifact {
			sum.ArtifactCount++
		}
		if cell.Lint == "pass" {
			sum.LintPass++
		}
		pass := cell.Behavior == "pass"
		if pass {
			sum.BehaviorPass++
		}
		o := sum.ByMethod[cell.Method]
		o.Total++
		if pass {
			o.Pass++
		}
		sum.ByMethod[cell.Method] = o
		v := sum.ByVersion[cell.Version]
		v.Total++
		if pass {
			v.Pass++
		}
		sum.ByVersion[cell.Version] = v
	}
	if sum.ApplicableCells > 0 {
		sum.SuccessRatePct = pct(sum.BehaviorPass, sum.ApplicableCells)
	}
	for k, v := range sum.ByMethod {
		v.Pct = pct(v.Pass, v.Total)
		sum.ByMethod[k] = v
	}
	for k, v := range sum.ByVersion {
		v.Pct = pct(v.Pass, v.Total)
		sum.ByVersion[k] = v
	}

	return Results{
		Generated: time.Now().Format(time.RFC3339),
		Decoder:   c.decoder,
		Encoder:   "ionCube PHP Encoder 15.0 (evaluation), ioncube_encoderNN_15.0_64",
		Versions:  vers,
		Pending:   pending,
		Methods:   methods,
		Summary:   sum,
		Cells:     cells,
		Dynkeys:   dk,
	}
}

/* ------------------------------ helpers ---------------------------------- */

func loadFixtures(c config) []fixture {
	entries, err := os.ReadDir(c.fixturesDir)
	if err != nil {
		fatal("read fixtures dir: %v", err)
	}
	var out []fixture
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".php") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".php")
		if len(c.fixtureOnly) > 0 && !contains(c.fixtureOnly, name) {
			continue
		}
		path := filepath.Join(c.fixturesDir, e.Name())
		src := readFile(path)
		out = append(out, fixture{
			Name:      name,
			Path:      path,
			Construct: headerVal(src, "construct", name),
			Min:       parseVer(headerVal(src, "minphp", "5.6")),
			Max:       parseVer(headerVal(src, "maxphp", "8.4")),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Construct < out[j].Construct })
	return out
}

var headerRe = regexp.MustCompile(`(?m)^//\s*([a-z]+):\s*(.+?)\s*$`)

func headerVal(src, key, def string) string {
	for _, m := range headerRe.FindAllStringSubmatch(src, -1) {
		if m[1] == key {
			return m[2]
		}
	}
	return def
}

// loadRuntimes hand-parses the small runtimes.yml (no yaml dep): each 2-space
// quoted version key, with its platform/image/decode_image at 4-space indent.
func loadRuntimes(path string) []runtime {
	src := readFile(path)
	verRe := regexp.MustCompile(`^  "(\d+\.\d+)":\s*$`)
	kvRe := regexp.MustCompile(`^    (\w+):\s*"(.*)"\s*$`)
	var out []runtime
	var cur *runtime
	inRuntimes := false
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(line, "runtimes:") {
			inRuntimes = true
			continue
		}
		if !inRuntimes {
			continue
		}
		if m := verRe.FindStringSubmatch(line); m != nil {
			out = append(out, runtime{Ver: m[1]})
			cur = &out[len(out)-1]
			continue
		}
		if cur == nil {
			continue
		}
		if m := kvRe.FindStringSubmatch(line); m != nil {
			switch m[1] {
			case "platform":
				cur.Platform = m[2]
			case "image":
				cur.Image = m[2]
			case "decode_image":
				cur.DecodeImage = m[2]
			}
		}
	}
	for i := range out {
		if out[i].Image == "" {
			out[i].Image = "deionizer:php" + out[i].Ver
		}
	}
	sort.Slice(out, func(i, j int) bool { return mustFloat(out[i].Ver) < mustFloat(out[j].Ver) })
	return out
}

// similarity is an approximate token-multiset overlap (Jaccard) between two PHP
// sources after stripping comments/whitespace. It is a structural closeness
// gauge, not an AST proof; the headline correctness metric is behavioral.
var tokenRe = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*|\d+\.?\d*|[^\sA-Za-z0-9_]`)

func similarity(a, b string) float64 {
	ta := tokenize(a)
	tb := tokenize(b)
	if len(ta) == 0 && len(tb) == 0 {
		return 0
	}
	ca := counts(ta)
	cb := counts(tb)
	inter, union := 0, 0
	seen := map[string]bool{}
	for k, va := range ca {
		seen[k] = true
		vb := cb[k]
		inter += min(va, vb)
		union += max(va, vb)
	}
	for k, vb := range cb {
		if !seen[k] {
			union += vb
		}
	}
	if union == 0 {
		return 0
	}
	return pct(inter, union)
}

func tokenize(s string) []string {
	return tokenRe.FindAllString(stripComments(s), -1)
}

func counts(toks []string) map[string]int {
	m := map[string]int{}
	for _, t := range toks {
		m[t]++
	}
	return m
}

var blockCommentRe = regexp.MustCompile(`(?s)/\*.*?\*/`)
var lineCommentRe = regexp.MustCompile(`(?m)(//|#).*$`)

func stripComments(s string) string {
	s = blockCommentRe.ReplaceAllString(s, " ")
	s = lineCommentRe.ReplaceAllString(s, " ")
	return s
}

// stripDecodedHeader removes the decoder's fixed banner so similarity/snippets
// compare only recovered code.
func stripDecodedHeader(s string) string {
	if i := strings.Index(s, "*/"); i >= 0 && strings.Contains(s[:max(i, 0)+2], "Decompiled from ionCube") {
		s = s[i+2:]
	}
	return strings.TrimLeft(s, " \t\r\n")
}

// dkAnnotatedMethods returns the names of functions/methods that carry an
// @ioncube.dk annotation (the method declared on the next `function NAME(` line).
func dkAnnotatedMethods(src string) []string {
	lines := strings.Split(src, "\n")
	fnRe := regexp.MustCompile(`function\s+([A-Za-z_]\w*)\s*\(`)
	var out []string
	for i, ln := range lines {
		if !strings.Contains(ln, "@ioncube.dk") && !strings.Contains(ln, "@ioncube.dynamickey") {
			continue
		}
		for j := i + 1; j < len(lines) && j < i+6; j++ {
			if m := fnRe.FindStringSubmatch(lines[j]); m != nil {
				out = append(out, m[1])
				break
			}
		}
	}
	return out
}

func snippet(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = append(lines[:n], "... (truncated)")
	}
	return strings.Join(lines, "\n")
}

func extractJSON(s string) string {
	i := strings.IndexByte(s, '{')
	j := strings.LastIndexByte(s, '}')
	if i >= 0 && j > i {
		return s[i : j+1]
	}
	return "{}"
}

func runCmd(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func writeResults(path string, r Results) {
	b, _ := json.MarshalIndent(r, "", "  ")
	os.WriteFile(path, b, 0o644)
}

/* ------------------------------- tiny utils ------------------------------ */

func readFile(p string) string { b, _ := os.ReadFile(p); return string(b) }

func copyFile(src, dst string) {
	b, err := os.ReadFile(src)
	if err != nil {
		return
	}
	os.MkdirAll(filepath.Dir(dst), 0o755)
	os.WriteFile(dst, b, 0o644)
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func parseVer(s string) float64 { return mustFloat(strings.TrimSpace(s)) }

func mustFloat(s string) float64 {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return f
}

func trimZero(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

func dkNote(on bool) string {
	if on {
		return " (+ dynamic-keys observation)"
	}
	return ""
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func orNA(s string) string {
	if s == "" {
		return "na"
	}
	return s
}
func orFail(s string) string {
	if s == "" {
		return "fail"
	}
	return s
}

func pct(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b) * 100
}

func fatal(f string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, "harness: "+f+"\n", a...)
	os.Exit(1)
}
