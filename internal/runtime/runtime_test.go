package runtime

import "testing"

// matrix mirrors the shipped runtimes (5.6, 7.2, 7.4, 8.1, 8.3) so Nearest is
// tested against the exact set of runtimes we actually run, independent of any
// IONCUBE_ORACLE_RUNTIMES override in the environment.
func matrix() *Matrix {
	return &Matrix{
		Probe: "7.4",
		Runtimes: map[string]Runtime{
			"5.6": {Image: "deionizer:php5.6"},
			"7.2": {Image: "deionizer:php7.2"},
			"7.4": {Image: "deionizer:php7.4"},
			"8.1": {Image: "deionizer:php8.1"},
			"8.3": {Image: "deionizer:php8.3"},
		},
	}
}

func TestNearest(t *testing.T) {
	m := matrix()
	cases := []struct {
		in, want string
	}{
		// Exact matches pass through untouched.
		{"5.6", "5.6"},
		{"7.2", "7.2"},
		{"7.4", "7.4"},
		{"8.1", "8.1"},
		{"8.3", "8.3"},

		// The routing bug this guards: 8.2 decodes ONLY under the 8.3 loader, so it
		// must climb to 8.3 and never collapse onto the numerically-closer 8.1.
		{"8.2", "8.3"},
		// 8.0 stays on the nearest sensible loader, 8.1.
		{"8.0", "8.1"},
		// A future/unknown 8.x prefers the newest available loader.
		{"8.4", "8.3"},

		// Lower majors keep their established routing.
		{"7.0", "7.4"},
		{"7.1", "7.4"},
		{"7.3", "7.4"},
		{"5.3", "5.6"},
	}
	for _, c := range cases {
		if got := m.Nearest(c.in); got != c.want {
			t.Errorf("Nearest(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestNearestEmbedded confirms the shipped embedded matrix (runtimes.yml) routes
// the critical 8.2 case correctly through the real Load() path.
func TestNearestEmbedded(t *testing.T) {
	t.Setenv("IONCUBE_ORACLE_RUNTIMES", "") // ignore any ad-hoc override
	m := Load("")
	if got := m.Nearest("8.2"); got != "8.3" {
		t.Fatalf("embedded matrix Nearest(8.2) = %q, want 8.3", got)
	}
	if got := m.Nearest("8.0"); got != "8.1" {
		t.Fatalf("embedded matrix Nearest(8.0) = %q, want 8.1", got)
	}
}
