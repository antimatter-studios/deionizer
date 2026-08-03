package detect

import (
	"os"
	"path/filepath"
	"testing"

	"deionizer/internal/runtime"
)

// testMatrix mirrors the shape of runtimes.yml without depending on the embed, so
// these cases stay stable if the shipped matrix gains or loses a version.
func testMatrix() *runtime.Matrix {
	return &runtime.Matrix{
		Probe: "7.4",
		Runtimes: map[string]runtime.Runtime{
			"5.6": {Image: "deionizer:php5.6", Platform: "linux/amd64"},
			"7.2": {Image: "deionizer:php7.2"},
			"7.4": {Image: "deionizer:php7.4"},
			"8.1": {Image: "deionizer:php8.1"},
			"8.3": {Image: "deionizer:php8.3"},
		},
	}
}

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// IsEncoded is documented as a cheap first-400-bytes sniff, NOT a validator: it
// must accept both marker dialects and reject plain PHP, and a false positive is
// expected to fail later rather than here.
func TestIsEncoded(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{"ioncube marker", "<?php //004fb\n// ionCube Loader\n", true},
		{"il_exec form", "<?php\n_il_exec();\n", true},
		{"plain php", "<?php\necho 'hello';\n", false},
		{"empty", "", false},
		{"marker past the 400-byte window", "<?php\n" + string(make([]byte, 500)) + "ionCube", false},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if got := IsEncoded(writeFile(t, "f.php", c.content)); got != c.want {
				t.Errorf("IsEncoded = %v, want %v", got, c.want)
			}
		})
	}
}

func TestIsEncodedMissingFile(t *testing.T) {
	if IsEncoded(filepath.Join(t.TempDir(), "nope.php")) {
		t.Error("IsEncoded on a missing file = true, want false")
	}
}

// MarkerVersion resolves an //ICB0 bundle marker to a runtime we actually have,
// and yields ProbeSentinel whenever the marker carries no version.
func TestMarkerVersion(t *testing.T) {
	m := testMatrix()
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"bundle prefers 74", "<?php //ICB0 74:0 81:7ce4 82:10be1\n", "7.4"},
		{"bundle without 74 prefers 81", "<?php //ICB0 81:7ce4 82:10be1\n", "8.1"},
		{"bundle 82 alone maps to the 8.3 loader", "<?php //ICB0 82:10be1\n", "8.3"},
		{"version-less marker probes", "<?php //004fb\n", ProbeSentinel},
		{"no marker at all", "<?php echo 1;\n", ProbeSentinel},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if got := MarkerVersion(writeFile(t, "f.php", c.content), m); got != c.want {
				t.Errorf("MarkerVersion = %q, want %q", got, c.want)
			}
		})
	}
}

func TestMarkerVersionMissingFile(t *testing.T) {
	if got := MarkerVersion(filepath.Join(t.TempDir(), "nope.php"), testMatrix()); got != ProbeSentinel {
		t.Errorf("MarkerVersion on a missing file = %q, want %q", got, ProbeSentinel)
	}
}

// PHP < 7.2 has no arm64 base image and ships only an x86-64 loader, so it must
// be forced to emulate; everything newer runs native.
func TestPlatformFor(t *testing.T) {
	cases := map[string]string{
		"5.6": "linux/amd64",
		"7.0": "linux/amd64",
		"7.1": "linux/amd64",
		"7.2": "",
		"7.4": "",
		"8.3": "",
	}
	for ver, want := range cases {
		if got := PlatformFor(ver); got != want {
			t.Errorf("PlatformFor(%q) = %q, want %q", ver, got, want)
		}
	}
}

// PlatformOf prefers whatever the matrix says, and only falls back to the pattern
// when the matrix has no opinion.
func TestPlatformOf(t *testing.T) {
	m := testMatrix()
	if got := PlatformOf("5.6", m); got != "linux/amd64" {
		t.Errorf("PlatformOf(5.6) = %q, want linux/amd64 (from the matrix)", got)
	}
	if got := PlatformOf("7.4", m); got != "" {
		t.Errorf("PlatformOf(7.4) = %q, want \"\"", got)
	}
	// 7.1 is absent from the matrix, so the pattern fallback decides.
	if got := PlatformOf("7.1", m); got != "linux/amd64" {
		t.Errorf("PlatformOf(7.1) = %q, want linux/amd64 (pattern fallback)", got)
	}
}

func TestParseEncoderVerdict(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"typical loader line", "the file was encoded by the ionCube Encoder for PHP 7.2 and", "7.2"},
		{"case insensitive", "ENCODER FOR PHP 8.1", "8.1"},
		{"no verdict", "some unrelated loader error", ""},
		{"empty", "", ""},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if got := ParseEncoderVerdict(c.in); got != c.want {
				t.Errorf("ParseEncoderVerdict(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
