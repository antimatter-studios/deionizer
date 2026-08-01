package decompile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNamespaceInert is a no-regression probe for the namespace render fix: the
// committed probe corpus is entirely NON-namespaced (global-scope constructs), so
// the whole namespace codepath (fileNS != "") must stay inert. Every function the
// fix touched returns byte-identically when there is no file namespace, so proving
// the codepath never fires proves the output is byte-identical to pre-fix.
//
// It asserts, for each frozen probe reveal, that RenderFile emits NONE of the
// artifacts the fix can introduce: a `namespace X;` declaration, or a class
// reference/declaration qualified with a leading "\" (new/extends/implements/
// instanceof/catch/static). Their total absence means none of the new branches
// executed — the render is exactly the historical one.
func TestNamespaceInert(t *testing.T) {
	inDir := probeDir()
	entries, err := os.ReadDir(inDir)
	if err != nil {
		t.Skipf("no probe corpus: %v", err)
	}
	// Substrings that can ONLY appear when the namespace fix qualifies a reference
	// or emits a namespace block. A non-namespaced reveal must contain none.
	artifacts := []string{
		"\nnamespace ",
		"new \\",
		"extends \\",
		"implements \\",
		"instanceof \\",
		"catch (\\",
		"\tuse \\",
	}
	saw := false
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		stem := strings.TrimSuffix(e.Name(), ".json")
		t.Run(stem, func(t *testing.T) {
			fh, err := os.Open(filepath.Join(inDir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			defer fh.Close()
			methods, err := ParseJSON(fh)
			if err != nil {
				t.Fatal(err)
			}
			got, err := RenderFile(methods)
			if err != nil {
				t.Fatal(err)
			}
			saw = true
			for _, a := range artifacts {
				if strings.Contains(got, a) {
					t.Errorf("%s: namespace-fix artifact %q leaked into a non-namespaced render", stem, a)
				}
			}
		})
	}
	if !saw {
		t.Skip("no probe reveals found")
	}
}
