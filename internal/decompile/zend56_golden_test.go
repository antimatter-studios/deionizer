package decompile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// zend56Goldens are frozen deionizer_reveal_json captures (PHP 5.6, image
// deionizer-ext56:v2) of tests/fixtures/*.php probes, PLUS the driver's frozen
// behavioral trace (<name>.trace), asserted BYTE-FOR-BYTE against a committed
// RenderFileZend56 output. They lock the whole 5.6 deep-decode path offline (no
// docker): the scalar recovery (ApplyTrace filling encrypted CONSTs — e.g.
// implode's separator) AND the 5.6-specific renderers (no arrow-fn, closure
// use(), compound-assign to an object property, class-const/property placeholders).
// A regression that would flip a `VERSIONS=5.6 ./tests/run.sh` cell turns this RED
// first. Regenerate intentionally with GOLDEN_UPDATE=1.
var zend56Goldens = []string{
	"closures",                // closure use()-capture prologue -> `function () use ()` (not fn=>)
	"methods_static_instance", // $this->prop += $by compound-assign; static factory; get_class
	"class_consts",            // Class::CONST + implode(self::LABELS); const-name placeholders
	"while_loop",              // implode separator "," recovered from the behavioral trace
	"foreach_simple",          // keyed/valued foreach; implode separator recovered
}

func probe56Dir() string {
	wd, _ := os.Getwd()
	return filepath.Join(wd, "testdata", "probes56")
}

func TestZend56Goldens(t *testing.T) {
	update := os.Getenv("GOLDEN_UPDATE") != ""
	for _, name := range zend56Goldens {
		name := name
		t.Run(name, func(t *testing.T) {
			fh, err := os.Open(filepath.Join(probe56Dir(), name+".json"))
			if err != nil {
				t.Skipf("probe56 %s: %v", name, err)
			}
			defer fh.Close()
			methods, err := ParseJSON(fh)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			// Apply the frozen behavioral trace exactly as the CLI does: encrypted
			// 5.x scalar CONSTs the reveal marked enc=true are filled from the real
			// decrypted built-in arguments the driver logged.
			if tb, err := os.ReadFile(filepath.Join(probe56Dir(), name+".trace")); err == nil {
				ApplyTraceAll(methods, ParseTraceFile(strings.NewReader(string(tb))))
			}
			got, err := RenderFileZend56(methods, true)
			if err != nil {
				t.Fatalf("render %s: %v", name, err)
			}
			path := filepath.Join(goldenDir(), "probe56_"+name+".php")
			if update {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				t.Logf("wrote golden probe56_%s.php (%d bytes)", name, len(got))
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden probe56_%s.php (run GOLDEN_UPDATE=1): %v", name, err)
			}
			if got != string(want) {
				t.Errorf("probe56 %s drifted.\n--- got ---\n%s\n--- want ---\n%s", name, got, string(want))
			}
		})
	}
}

// TestZend56ScalarRecovery asserts the behavioral side-channel actually fills an
// encrypted scalar CONST: while_loop's implode() separator is enc=true in the raw
// reveal and must become "," after ApplyTrace (proving the wiring, independent of
// the full-file golden).
func TestZend56ScalarRecovery(t *testing.T) {
	fh, err := os.Open(filepath.Join(probe56Dir(), "while_loop.json"))
	if err != nil {
		t.Skipf("while_loop.json: %v", err)
	}
	defer fh.Close()
	methods, err := ParseJSON(fh)
	if err != nil {
		t.Fatal(err)
	}
	before, err := RenderFileZend56(methods, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(before, "implode(/* inferred */ null,") {
		t.Fatalf("expected an unfilled implode separator before trace; got:\n%s", before)
	}
	tb, err := os.ReadFile(filepath.Join(probe56Dir(), "while_loop.trace"))
	if err != nil {
		t.Fatal(err)
	}
	ApplyTraceAll(methods, ParseTraceFile(strings.NewReader(string(tb))))
	after, err := RenderFileZend56(methods, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(after, "implode(',',") {
		t.Fatalf("expected implode separator recovered to ',' after trace; got:\n%s", after)
	}
}
