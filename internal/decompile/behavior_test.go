package decompile

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The two grading tiers the local encode->decode->run harness applies, run here
// against the frozen reveal captures instead of a live decode — so they run
// ONLINE, on every PR, with no loader, no decode extension, no encoder and no
// Docker:
//
//	LINT     — the rendered file is syntactically valid PHP (`php -l`).
//	BEHAVIOR — the rendered file, run as a whole program through the harness's
//	           own name-independent driver, produces byte-identical stdout to the
//	           original plaintext fixture it was captured from.
//
// Both render through RenderFile at test time rather than reading the committed
// golden, so this grades TODAY's renderer: a change that keeps a golden updated
// but breaks the semantics still fails here.
//
// What this tier canNOT cover: detection and the loader hand-off (stages 1-2).
// Its input is a committed JSON capture, not an encoded file. The full pipeline
// is `chore test:matrix`, which needs vendor assets and stays local — see
// AGENTS.md §5.
//
// Needs a `php` on PATH (override with PHP_BIN). Skips when there is none, EXCEPT
// when DEIONIZER_REQUIRE_PHP is set — CI sets it on the Linux leg so a runner
// that silently lost PHP fails the build instead of quietly grading nothing.

// behaviorOriginals maps a probe capture to the plaintext fixture it was taken
// from, where the two names differ (a version-specific capture of one fixture).
// Everything else is resolved by name under tests/fixtures then
// tests/realworld/fixtures.
var behaviorOriginals = map[string]string{
	"match_expr_81":    "tests/fixtures/match_expr.php",
	"null_coalesce_83": "tests/fixtures/null_coalesce.php",
	"ternary_83":       "tests/fixtures/ternary.php",
}

// knownBehaviorGaps are captures whose render LINTS but does not yet reproduce
// the original's behaviour — real decompiler bugs, recorded so the suite is
// green-when-correct while staying honest about what is broken. A gap that
// starts passing FAILS the test: fix the bug, delete the entry, same commit.
var knownBehaviorGaps = map[string]string{
	// Renders `foreach ($rows as $k => $v)` over the right array but emits the
	// literal's keys in the wrong order: original prints "name,age?", the decode
	// prints "age?,name". Element order in the reconstructed array literal does
	// not follow the ADD_ARRAY_ELEMENT sequence.
	"foreach_key_nested_if": "array-literal element order reversed",
}

func phpBin(t *testing.T) string {
	t.Helper()
	bin := os.Getenv("PHP_BIN")
	if bin == "" {
		bin = "php"
	}
	path, err := exec.LookPath(bin)
	if err == nil {
		return path
	}
	if os.Getenv("DEIONIZER_REQUIRE_PHP") != "" {
		t.Fatalf("DEIONIZER_REQUIRE_PHP is set but %q is not on PATH: %v", bin, err)
	}
	t.Skipf("no %q on PATH — lint/behavior tiers need one (set PHP_BIN, or DEIONIZER_REQUIRE_PHP to make this fatal)", bin)
	return ""
}

// repoRoot is two levels up from internal/decompile.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

// originalFixture returns the plaintext source a capture was taken from, or ""
// when there is none committed (those cells are lint-only).
func originalFixture(root, name string) string {
	if rel, ok := behaviorOriginals[name]; ok {
		if p := filepath.Join(root, rel); fileExists(p) {
			return p
		}
		return ""
	}
	for _, dir := range []string{"tests/fixtures", "tests/realworld/fixtures"} {
		if p := filepath.Join(root, dir, name+".php"); fileExists(p) {
			return p
		}
	}
	return ""
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// renderProbe renders one capture through the current RenderFile and writes it
// to a temp file, returning its path.
func renderProbe(t *testing.T, name string) string {
	t.Helper()
	fh, err := os.Open(filepath.Join(probeDir(), name+".json"))
	if err != nil {
		t.Skipf("probe %s: %v", name, err)
	}
	defer fh.Close()
	methods, err := ParseJSON(fh)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	src, err := RenderFile(methods)
	if err != nil {
		t.Fatalf("render %s: %v", name, err)
	}
	out := filepath.Join(t.TempDir(), name+".decoded-source.php")
	if err := os.WriteFile(out, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestRenderedFilesLint asserts every rendered capture is valid PHP. A render
// that does not even parse is the one failure mode a byte-golden cannot catch:
// the golden happily freezes broken syntax.
func TestRenderedFilesLint(t *testing.T) {
	php := phpBin(t)
	for _, name := range probeGoldens {
		t.Run(name, func(t *testing.T) {
			out, err := exec.Command(php, "-l", renderProbe(t, name)).CombinedOutput()
			if err != nil {
				t.Errorf("php -l rejected the render of %s: %v\n%s", name, err, out)
			}
		})
	}
}

// TestRenderedFilesBehavior runs the original fixture and the rendered decode
// through tests/driver/run_probe.php and requires identical stdout. The driver
// picks the entry point by SHAPE, never by name, so a renamed or restructured
// decode is still graded on what it computes.
func TestRenderedFilesBehavior(t *testing.T) {
	php := phpBin(t)
	root := repoRoot(t)
	driver := filepath.Join(root, "tests", "driver", "run_probe.php")
	if !fileExists(driver) {
		t.Fatalf("behavior driver missing: %s", driver)
	}

	run := func(t *testing.T, target string) string {
		t.Helper()
		// The driver suppresses errors and prints nothing on a fatal, which is
		// exactly how the harness scores a broken decode: empty stdout, FAIL.
		out, _ := exec.Command(php, driver, target).Output()
		return strings.TrimRight(string(out), "\n")
	}

	graded := 0
	for _, name := range probeGoldens {
		t.Run(name, func(t *testing.T) {
			orig := originalFixture(root, name)
			if orig == "" {
				t.Skipf("no plaintext original committed for %s — lint-only", name)
			}
			want := run(t, orig)
			if want == "" {
				t.Fatalf("original fixture %s produced no output — it cannot grade anything", orig)
			}
			graded++

			got := run(t, renderProbe(t, name))
			gap, known := knownBehaviorGaps[name]
			switch {
			case got == want && known:
				t.Fatalf("known gap %q (%s) now PASSES — delete it from knownBehaviorGaps", name, gap)
			case got == want:
				// pass
			case known:
				t.Skipf("known gap: %s\n  original: %q\n  decoded:  %q", gap, want, got)
			default:
				t.Errorf("decoded behaviour differs from the original.\n  original: %q\n  decoded:  %q", want, got)
			}
		})
	}
	// A refactor that stops resolving fixtures would otherwise turn this whole
	// tier into 38 quiet skips.
	if graded < 30 {
		t.Errorf("only %d captures were behaviour-graded; expected >= 30 (fixture lookup broken?)", graded)
	}
}
