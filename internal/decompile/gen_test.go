package decompile

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// dumpSpec describes one real captured opline dump and how to adapt it.
type dumpSpec struct {
	path   string // absolute
	out    string // output basename (without .php)
	zend56 bool
}

// corpusDir points at the (unshipped) corpus of captured opcode dumps used by
// the render-all / coverage tests below. That corpus is private working material
// and is NOT part of the repo; set DEIONIZER_CORPUS_DIR to it, or drop it at
// tmp/corpus. The tests skip cleanly when it is absent — the
// decompiler's committed coverage lives in the probe/method/rawparse goldens.
func corpusDir() string {
	if e := os.Getenv("DEIONIZER_CORPUS_DIR"); e != "" {
		return e
	}
	wd, _ := os.Getwd()
	return filepath.Clean(filepath.Join(wd, "..", "..", "tmp", "corpus"))
}

// requireDumps skips the calling test unless the private dump corpus is present.
func requireDumps(t *testing.T) {
	t.Helper()
	if fi, err := os.Stat(corpusDir()); err != nil || !fi.IsDir() {
		t.Skip("dump corpus not present (private; set DEIONIZER_CORPUS_DIR to run)")
	}
}

// allDumps lists the text dumps to lift from the private corpus. The two defaults
// are captures of PUBLIC, open-source code — Smarty's Config_File and PEAR's
// Services_JSON — whose plaintext originals are available to diff a render against.
//
// Set IC_DUMPS=<dir> to additionally lift every *.txt in that directory. Names
// ending in `56.txt` are treated as Zend 2.6 dumps; anything else as Zend 3.4.
func allDumps() []dumpSpec {
	f := corpusDir()
	out := []dumpSpec{
		{filepath.Join(f, "09-dump-config_file.txt"), "config_file", true},
		{filepath.Join(f, "09-dump-services_json.txt"), "services_json", true},
	}
	extra := os.Getenv("IC_DUMPS")
	if extra == "" {
		return out
	}
	paths, _ := filepath.Glob(filepath.Join(extra, "*.txt"))
	sort.Strings(paths)
	for _, p := range paths {
		base := strings.TrimSuffix(filepath.Base(p), ".txt")
		out = append(out, dumpSpec{p, base, strings.HasSuffix(base, "56")})
	}
	return out
}

// TestGenerate renders every dump in the private corpus to <corpus>/lift-out/.
// Run with: go test ./internal/decompile -run TestGenerate -v
func TestGenerate(t *testing.T) {
	requireDumps(t)
	outDir := filepath.Join(corpusDir(), "lift-out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	totalMethods, totalDumps := 0, 0
	for _, d := range allDumps() {
		fh, err := os.Open(d.path)
		if err != nil {
			t.Logf("SKIP %s: %v", d.path, err)
			continue
		}
		methods, err := ParseTextDump(fh, d.zend56)
		fh.Close()
		if err != nil {
			t.Errorf("parse %s: %v", d.path, err)
			continue
		}
		if d.zend56 {
			if tf, err := os.Open(filepath.Join(corpusDir(), "09-trace-behavioral.txt")); err == nil {
				ApplyTraceAll(methods, ParseTraceFile(tf))
				tf.Close()
			}
		}
		if len(methods) == 0 {
			t.Logf("no methods parsed from %s", d.path)
			continue
		}
		php, err := RenderFile(methods)
		if err != nil {
			t.Errorf("render %s: %v", d.path, err)
			continue
		}
		outPath := filepath.Join(outDir, d.out+".php")
		if err := os.WriteFile(outPath, []byte(php), 0o644); err != nil {
			t.Fatal(err)
		}
		totalMethods += len(methods)
		totalDumps++
		t.Logf("%-28s -> %s  (%d methods, %d bytes)", d.out, filepath.Base(outPath), len(methods), len(php))
	}
	t.Logf("TOTAL: %d dumps, %d methods rendered", totalDumps, totalMethods)
}

// TestCoverage aggregates opcode coverage across every dump in the private corpus.
func TestCoverage(t *testing.T) {
	requireDumps(t)
	grandTotal, grandHandled := 0, 0
	unknown := map[string]int{}
	methodsTotal := 0
	for _, d := range allDumps() {
		fh, err := os.Open(d.path)
		if err != nil {
			continue
		}
		ms, err := ParseTextDump(fh, d.zend56)
		fh.Close()
		if err != nil || len(ms) == 0 {
			continue
		}
		methodsTotal += len(ms)
		tot, hnd, unk := Coverage(ms)
		grandTotal += tot
		grandHandled += hnd
		for k, v := range unk {
			unknown[k] += v
		}
	}
	t.Logf("methods=%d oplines=%d handled=%d (%.2f%%)", methodsTotal, grandTotal, grandHandled,
		100*float64(grandHandled)/float64(grandTotal))
	if len(unknown) == 0 {
		t.Logf("unhandled opcodes: none")
	}
	// print unhandled histogram sorted by count
	type kv struct {
		k string
		v int
	}
	var list []kv
	for k, v := range unknown {
		list = append(list, kv{k, v})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].v > list[j].v })
	for _, e := range list {
		t.Logf("  UNHANDLED %-32s %d", e.k, e.v)
	}
}

// TestInspect prints selected small methods for eyeballing during development.
func TestInspect(t *testing.T) {
	if os.Getenv("INSPECT") == "" {
		t.Skip("set INSPECT=1 to print sample renders")
	}
	requireDumps(t)
	fh, err := os.Open(filepath.Join(corpusDir(), "09-dump-config_file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	methods, err := ParseTextDump(fh, true)
	if err != nil {
		t.Fatal(err)
	}
	want := os.Getenv("INSPECT")
	for _, m := range methods {
		if want != "1" && !strings.Contains(strings.ToLower(m.Class+"::"+m.Function), strings.ToLower(want)) {
			continue
		}
		s, _ := Render(m)
		t.Logf("\n===== %s::%s (%d oplines) =====\n%s", m.Class, m.Function, len(m.Oplines), s)
	}
}
