package main

// `decode --output <dir>` (MIRROR mode): point it at a tree of ionCube-encoded PHP
// and it deep-decodes every file into a MIRRORED output tree (the source stays
// untouched), then scores each file in a CONFIDENCE REPORT — one machine-readable
// JSON document and one human summary.
//
// This differs from a plain `decode`/`process`, which write <file>.decoded-source.php
// in place next to the encoded original and report only how many artifacts landed.
// The mirror flow answers the question a reviewer actually asks about lost-source
// recovery: "for each file, did we get it back, and can I trust it?" — so it
// records the detected PHP version, how many methods came back, whether the
// recovered PHP passes `php -l` under a version-matched loader, and a short reason
// for anything that is not clean, ending in an N ok / partial / failed rollup.
//
// The confidence stance is deliberately conservative: a file is only "ok" when it
// both revealed methods AND lints clean. Bodies come back through the loader's own
// execution path — recovery reaches exactly the code that can already run, so we
// never claim more than the recovered source can demonstrate.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"deionizer/internal/detect"
	"deionizer/internal/docker"
	"deionizer/internal/runtime"
)

// recovery status values, in worsening order.
const (
	statusOK      = "ok"      // methods revealed AND php -l clean
	statusPartial = "partial" // recovered, but not verified clean (lint failed or was skipped)
	statusFailed  = "failed"  // nothing usable recovered
)

// lint verdicts for the per-file report.
const (
	lintPass    = "pass"
	lintFail    = "fail"
	lintSkipped = "skipped"
)

// fileReport is one encoded file's row in the confidence report.
type fileReport struct {
	File        string `json:"file"`             // path relative to the input tree
	PHPVersion  string `json:"php_version"`      // runtime the loader matched
	DetectedVia string `json:"detected_via"`     // marker / probe / forced
	Status      string `json:"status"`           // ok | partial | failed
	Methods     int    `json:"methods"`          // functions + methods recovered
	Lint        string `json:"lint"`             // pass | fail | skipped
	Output      string `json:"output,omitempty"` // recovered file, relative to the output tree
	Reason      string `json:"reason,omitempty"` // short reason for anything not clean
}

// rollup is the one-line score for the whole tree.
type rollup struct {
	Total   int `json:"total"`
	OK      int `json:"ok"`
	Partial int `json:"partial"`
	Failed  int `json:"failed"`
}

// recoverReport is the machine-readable confidence report written as JSON.
type recoverReport struct {
	Tool      string       `json:"tool"`
	Input     string       `json:"input"`
	Output    string       `json:"output"`
	Generated string       `json:"generated"` // RFC3339
	Files     []fileReport `json:"files"`
	Rollup    rollup       `json:"rollup"`
}

const (
	jsonReportName = "confidence-report.json"
	textReportName = "confidence-report.txt"
)

// runMirrorDecode deep-decodes every encoded file under `input` into the mirrored
// output tree `out` (the source stays untouched) and emits the confidence report
// there. This is `decode --output <dir>`; cmdDecode resolves the input (a tree, a
// single file, or a fetched URL) and the absolute output before calling in.
//
// `root` is the tree the mirror is relative to: `input` itself when it is a
// directory, else the file's parent — so a single-file mirror lands at
// <out>/<basename> rather than <out>/. Every artifact and both report files land
// under `out`.
func runMirrorDecode(input, out string, opt options) error {
	input, err := filepath.Abs(input)
	if err != nil {
		return err
	}
	fi, err := os.Stat(input)
	if err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	root := input
	if !fi.IsDir() {
		root = filepath.Dir(input)
	}
	if out == root {
		return fmt.Errorf("decode: --output must differ from the source directory")
	}

	m := runtime.Load(opt.runtimesPath)
	u := newUI(opt.verbose)
	started := time.Now()

	files := findEncodedFiles(input)
	if len(files) == 0 {
		return fmt.Errorf("decode: no ionCube-encoded PHP found under %s", input)
	}

	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	driverPath, cleanup, err := writeDriver()
	if err != nil {
		return err
	}
	defer cleanup()
	driverDir := filepath.Dir(driverPath)

	u.head("decode %s → %s   [mirror]", input, out)
	u.info("%d encoded %s → %s", len(files), plural(len(files), "file", "files"), out)

	// A version-less marker (//004fb) costs one trial-load container to resolve;
	// every file in a project shares it, so cache by marker to probe once.
	verCache := map[string]resolvedVersion{}

	ctx := decodeCtx{u: u, driverDir: driverDir, guardConsts: opt.guardConsts, timeout: opt.fileTimeout}
	reports := make([]fileReport, 0, len(files))
	for i, f := range files {
		rel := relOf(root, f)
		it := u.item(i+1, len(files), rel)
		fr := recoverFile(ctx, root, f, rel, out, m, opt, verCache)
		reports = append(reports, fr)
		switch fr.Status {
		case statusOK:
			it.ok("php %s · %d %s · lint %s", fr.PHPVersion, fr.Methods, plural(fr.Methods, "method", "methods"), fr.Lint)
		case statusPartial:
			it.skip("php %s · %d %s · %s", fr.PHPVersion, fr.Methods, plural(fr.Methods, "method", "methods"), fr.Reason)
		default:
			it.fail("%s", fr.Reason)
		}
	}

	rep := recoverReport{
		Tool:      "deionizer decode --output",
		Input:     input,
		Output:    out,
		Generated: time.Now().Format(time.RFC3339),
		Files:     reports,
		Rollup:    tally(reports),
	}
	if err := writeReports(out, rep); err != nil {
		return err
	}

	printConfidence(u, rep, time.Since(started))
	return nil
}

// resolvedVersion caches a marker's version answer so a whole tree of identically
// marked files trial-loads at most once.
type resolvedVersion struct {
	ver, how string
	err      error
}

// recoverFile decodes one encoded file into the output tree and scores it. It
// never returns an error: a per-file failure is a "failed" row, not an aborted
// run — recovering 11 of 12 files is exactly the outcome the report exists to
// show. (A genuinely fatal condition, e.g. an unwritable output tree, is caught
// before the loop.)
func recoverFile(ctx decodeCtx, input, f, rel, out string, m *runtime.Matrix, opt options, cache map[string]resolvedVersion) fileReport {
	fr := fileReport{File: rel, Status: statusFailed, Lint: lintSkipped}

	rv := resolveCached(f, m, opt, ctx.u, cache)
	if rv.err != nil {
		fr.Reason = fmt.Sprintf("version detection failed: %v", rv.err)
		return fr
	}
	fr.PHPVersion, fr.DetectedVia = rv.ver, rv.how

	rt, ok := m.Get(rv.ver)
	if !ok || rt.DecodeImage == "" {
		fr.Reason = fmt.Sprintf("deep decode not ported for PHP %s (no decode image)", rv.ver)
		return fr
	}
	// This file's runtime coordinates — a mirror tree can mix versions, so they are
	// resolved per file onto this call's own ctx copy.
	ctx.ver = rv.ver
	ctx.platform = detect.PlatformOf(rv.ver, m)
	ctx.decodeImage = rt.DecodeImage
	if err := ensureImage(ctx.u, "decode", rt.DecodeImage, rv.ver, func() error {
		return docker.EnsureDecodeImage(rv.ver, rt.DecodeImage, ctx.platform, extDirContext(opt.extDir))
	}); err != nil {
		fr.Reason = fmt.Sprintf("decode image unavailable: %v", err)
		return fr
	}

	methods, err := revealMethods(ctx, input, f)
	if err != nil {
		fr.Reason = fmt.Sprintf("reveal: %v", err)
		return fr
	}
	fr.Methods = methodCount(methods)
	if len(methods) == 0 {
		fr.Reason = "no methods revealed"
		return fr
	}

	src, err := renderDecodedSource(f, methods, strings.HasPrefix(rv.ver, "5."))
	if err != nil {
		fr.Reason = fmt.Sprintf("render: %v", err)
		return fr
	}
	dest := filepath.Join(out, rel)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		fr.Reason = fmt.Sprintf("write: %v", err)
		return fr
	}
	if err := os.WriteFile(dest, []byte(src), 0o644); err != nil {
		fr.Reason = fmt.Sprintf("write: %v", err)
		return fr
	}
	fr.Output = rel

	// Recovered — now the confidence check: does the reconstructed PHP parse under
	// a version-matched engine? Only a clean lint earns "ok".
	pass, verdict, ran := phpLint(out, rel, rv.ver, rt, ctx.platform, ctx.u)
	switch {
	case !ran:
		fr.Lint, fr.Status = lintSkipped, statusPartial
		fr.Reason = "recovered but not lint-verified: " + verdict
	case pass:
		fr.Lint, fr.Status = lintPass, statusOK
	default:
		fr.Lint, fr.Status = lintFail, statusPartial
		fr.Reason = "recovered but php -l failed: " + verdict
	}
	return fr
}

// resolveCached resolves a file's PHP version, caching the answer by the file's
// ionCube marker so identical markers cost at most one trial-load probe.
func resolveCached(f string, m *runtime.Matrix, opt options, u *ui, cache map[string]resolvedVersion) resolvedVersion {
	// A forced --php or a per-file marker key both make a stable cache key; the
	// marker head is what distinguishes version-less files that must be probed.
	key := "php=" + opt.php + "\x00" + markerKey(f)
	if rv, seen := cache[key]; seen {
		return rv
	}
	ver, how, err := resolveVersion(f, m, opt, u)
	rv := resolvedVersion{ver: ver, how: how, err: err}
	cache[key] = rv
	return rv
}

// phpLint runs `php -l` on the recovered file under the version-matched image and
// reports pass/fail plus a short verdict. ran is false only when no PHP image is
// available at all (verdict then explains the skip).
func phpLint(outRoot, rel, ver string, rt runtime.Runtime, platform string, u *ui) (pass bool, verdict string, ran bool) {
	image := rt.DecodeImage
	if !docker.ImageExists(image) {
		image = docker.ImageName(ver)
	}
	if !docker.ImageExists(image) {
		return false, "no php image available to lint under PHP " + ver, false
	}
	stdout, stderr, err := docker.Run(
		image,
		[]docker.Mount{{Host: outRoot, Container: "/w", RO: true}},
		[]string{"-l", "/w/" + rel},
		docker.RunOpts{Platform: platform, Network: "none", Entrypoint: "php"},
	)
	u.traceBlock("lint:stdout", stdout)
	u.traceBlock("lint:stderr", stderr)
	if err == nil && strings.Contains(stdout, "No syntax errors") {
		return true, "", true
	}
	return false, lintVerdict(stdout + "\n" + stderr), true
}

// lintVerdict pulls the single most informative line out of `php -l` output — the
// parse-error line if there is one, else the first non-empty line.
func lintVerdict(out string) string {
	var first string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if first == "" {
			first = line
		}
		if strings.Contains(line, "error") || strings.Contains(line, "Error") {
			return line
		}
	}
	if first == "" {
		return "no lint output"
	}
	return first
}

// markerKey returns the encoded file's first-line ionCube marker, the token that
// tells whether two files will resolve to the same PHP version.
func markerKey(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return path // fall back to a per-file key: worst case, no cache sharing
	}
	defer f.Close()
	buf := make([]byte, 80)
	n, _ := f.Read(buf)
	head := string(buf[:n])
	if i := strings.IndexByte(head, '\n'); i >= 0 {
		head = head[:i]
	}
	return strings.TrimSpace(head)
}

// tally rolls the per-file statuses up into the score line.
func tally(reports []fileReport) rollup {
	r := rollup{Total: len(reports)}
	for _, fr := range reports {
		switch fr.Status {
		case statusOK:
			r.OK++
		case statusPartial:
			r.Partial++
		default:
			r.Failed++
		}
	}
	return r
}

// writeReports persists the JSON confidence report and a plain-text mirror of the
// human summary into the output tree.
func writeReports(out string, rep recoverReport) error {
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, jsonReportName), append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(out, textReportName), []byte(renderText(rep)), 0o644)
}

/* ------------------------------- rendering -------------------------------- */

// nameCol caps the file column so a long path cannot shove the status columns off
// the right edge of an 80-column terminal.
const nameCol = 34

// printConfidence writes the human summary to stdout (headings + table + rollup)
// and, for every not-clean file, a reason beneath it.
func printConfidence(u *ui, rep recoverReport, elapsed time.Duration) {
	fmt.Fprint(u.out, renderText(rep))
	u.head("recovered in %s — report: %s", dur(elapsed), filepath.Join(rep.Output, jsonReportName))
}

// renderText is the whole human summary as a string (also written to the .txt
// report so a saved run reads the same as the terminal).
func renderText(rep recoverReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, ">> confidence report: %s\n", rep.Input)
	fmt.Fprintf(&b, "   %-*s %-5s %7s  %-7s %s\n", nameCol, "FILE", "PHP", "METHODS", "LINT", "STATUS")

	rows := append([]fileReport(nil), rep.Files...)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].File < rows[j].File })
	for _, fr := range rows {
		fmt.Fprintf(&b, "   %-*s %-5s %7d  %-7s %s\n",
			nameCol, truncTail(fr.File, nameCol), dash(fr.PHPVersion), fr.Methods, fr.Lint, fr.Status)
	}

	r := rep.Rollup
	fmt.Fprintf(&b, "   %s\n", strings.Repeat("-", nameCol+26))
	fmt.Fprintf(&b, "   %d %s: %d ok, %d partial, %d failed\n",
		r.Total, plural(r.Total, "file", "files"), r.OK, r.Partial, r.Failed)

	notes := false
	for _, fr := range rows {
		if fr.Status == statusOK || fr.Reason == "" {
			continue
		}
		if !notes {
			fmt.Fprintf(&b, "\n   notes:\n")
			notes = true
		}
		fmt.Fprintf(&b, "   · %-*s %s\n", nameCol, truncTail(fr.File, nameCol), fr.Reason)
	}
	return b.String()
}

func dash(s string) string {
	if s == "" {
		return "?"
	}
	return s
}
