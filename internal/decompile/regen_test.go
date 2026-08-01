package decompile

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// decodedSourceHeader reproduces cmd/deionizer renderDecodedSource's wrapper
// byte-for-byte, so a harness render here equals what `deionizer decode`
// writes when pointed at the JSON reveal image.
func decodedSourceHeader(srcBase string) string {
	var b strings.Builder
	b.WriteString("<?php\n/**\n")
	b.WriteString(" * Decompiled from ionCube opcodes via deionizer.\n")
	b.WriteString(" * Source: " + srcBase + "\n")
	b.WriteString(" */\n\n")
	return b.String()
}

// renderDecodedFixture renders one fixture exactly as the CLI would.
func renderDecodedFixture(t *testing.T, jsonPath string) string {
	t.Helper()
	fh, err := os.Open(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	methods, err := ParseJSON(fh)
	if err != nil {
		t.Fatalf("%s: %v", jsonPath, err)
	}
	body, err := RenderFile(methods)
	if err != nil {
		t.Fatalf("%s: %v", jsonPath, err)
	}
	body = strings.TrimPrefix(strings.TrimLeft(body, " \t\n"), "<?php")
	body = strings.TrimLeft(body, " \t\n")
	stem := strings.TrimSuffix(filepath.Base(jsonPath), ".json") // e.g. loop_switch
	return decodedSourceHeader(stem+".php") + body
}

// countNotes counts genuine `// decompiler:` reconstruction notes: lines whose
// trimmed text starts with the marker. The file header mentions the marker
// mid-line ("`// decompiler:` marks reconstruction notes.") and must not count.
func countNotes(php string) int {
	n := 0
	for _, ln := range strings.Split(php, "\n") {
		// Genuine notes carry the marker (either line-leading, or trailing an
		// `} else {`); the file header mentions it while explaining the marker.
		if strings.Contains(ln, "// decompiler:") && !strings.Contains(ln, "marks reconstruction notes") {
			n++
		}
	}
	return n
}

// fixtureStems lists the probe stems present as fixtures, sorted.
func fixtureStems(t *testing.T) []string {
	ents, err := os.ReadDir(probeDir())
	if err != nil {
		t.Skipf("no probe fixtures: %v", err)
	}
	var out []string
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".json") {
			out = append(out, strings.TrimSuffix(e.Name(), ".json"))
		}
	}
	sort.Strings(out)
	return out
}

// TestNoteCounts renders every probe fixture and reports the number of
// `// decompiler:` notes per file (the fidelity metric this hardening targets):
// a note marks a spot the lift could not reconstruct cleanly, so the total is a
// coarse "how much of the corpus still needs a human" gauge. Always runs (cheap);
// with IC_WRITE=<dir> it also writes the .decoded-source.php files there.
func TestNoteCounts(t *testing.T) {
	stems := fixtureStems(t)
	writeDir := os.Getenv("IC_WRITE")
	grand := 0
	for _, stem := range stems {
		php := renderDecodedFixture(t, filepath.Join(probeDir(), stem+".json"))
		notes := countNotes(php)
		grand += notes
		t.Logf("%-24s notes=%-4d bytes=%d", stem, notes, len(php))
		if writeDir != "" {
			dest := filepath.Join(writeDir, stem+".decoded-source.php")
			if err := os.WriteFile(dest, []byte(php), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Logf("TOTAL // decompiler: notes = %d across %d fixtures", grand, len(stems))
	if writeDir != "" {
		t.Logf("wrote %d .decoded-source.php files to %s", len(stems), writeDir)
	}
}
