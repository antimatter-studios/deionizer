package decompile

import (
	"os"
	"path/filepath"
	"testing"
)

// goldenMethods pins one control-flow pattern each, rendered through the SINGLE-
// method Render path (TestProbeGoldens covers the whole-file RenderFile path over
// the same fixtures). Each case renders one function out of a frozen
// deionizer_reveal_json capture of a tests/fixtures/*.php construct probe and asserts it
// BYTE-FOR-BYTE against a committed golden, so a control-flow regression turns
// `go test` RED offline. Regenerate intentionally with GOLDEN_UPDATE=1 (then
// eyeball the diff — a golden freezes current behaviour, it does not prove it).
//
// Because the inputs are fixtures we author and encode ourselves, each golden is
// also checkable against its own plaintext source in tests/fixtures/.
var goldenMethods = []struct{ class, fn, golden string }{
	{"switch_fallthrough", "classify", "method_switch_grouped_break.php"},       // switch, grouped cases, break-to-merge
	{"switch_return_arms", "monthToNr", "method_switch_return_arms.php"},        // switch, grouped cases + return arms + default
	{"if_elseif_else", "band", "method_if_elseif_cascade.php"},                  // if / elseif / elseif / else cascade
	{"foreach_key_nested_if", "checkTable", "method_foreach_key_nested_if.php"}, // foreach with $k=>$v key, nested if
	{"loop_switch", "checkPolarity", "method_loop_switch.php"},                  // while loop wrapping a switch
}

func goldenDir() string {
	wd, _ := os.Getwd()
	return filepath.Join(wd, "testdata", "golden")
}

// renderMethodByName loads the probe fixture for stem and renders the named function.
func renderMethodByName(t *testing.T, stem, fn string) (string, bool) {
	t.Helper()
	fh, err := os.Open(filepath.Join(probeDir(), stem+".json"))
	if err != nil {
		t.Skipf("fixture %s: %v", stem, err)
	}
	defer fh.Close()
	methods, err := ParseJSON(fh)
	if err != nil {
		t.Fatalf("%s: %v", stem, err)
	}
	for i := range methods {
		if methods[i].Function == fn {
			s, err := Render(methods[i])
			if err != nil {
				t.Fatalf("render %s::%s: %v", stem, fn, err)
			}
			return s, true
		}
	}
	return "", false
}

func TestGoldenMethods(t *testing.T) {
	update := os.Getenv("GOLDEN_UPDATE") != ""
	if update {
		if err := os.MkdirAll(goldenDir(), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, g := range goldenMethods {
		g := g
		t.Run(g.class+"::"+g.fn, func(t *testing.T) {
			got, ok := renderMethodByName(t, g.class, g.fn)
			if !ok {
				t.Fatalf("method %s::%s not found in fixture", g.class, g.fn)
			}
			path := filepath.Join(goldenDir(), g.golden)
			if update {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				t.Logf("wrote golden %s (%d bytes)", g.golden, len(got))
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden %s (run GOLDEN_UPDATE=1 to create): %v", g.golden, err)
			}
			if got != string(want) {
				t.Errorf("%s::%s output drifted from golden %s.\n--- got ---\n%s\n--- want ---\n%s",
					g.class, g.fn, g.golden, got, string(want))
			}
		})
	}
}
