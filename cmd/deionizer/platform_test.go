package main

import (
	"strings"
	"testing"

	"deionizer/internal/detect"
	"deionizer/internal/runtime"
)

// oldPlatformOf is the pre-consolidation copy that lived in mirror.go. It is kept
// here only to prove the consolidation onto detect.PlatformOf is behaviour-
// preserving: for every runtime version the two MUST agree.
func oldPlatformOf(ver string, m *runtime.Matrix) string {
	if r, ok := m.Get(ver); ok && r.Platform != "" {
		return r.Platform
	}
	if strings.HasPrefix(ver, "5.") || ver == "7.0" || ver == "7.1" {
		return "linux/amd64"
	}
	return ""
}

// The mirror path now calls detect.PlatformOf instead of its own platformOf copy.
// Lock that the strings are identical for every shipped version, against the real
// embedded matrix, so a future divergence is caught here.
func TestPlatformOfMatchesOldCopy(t *testing.T) {
	m := runtime.Load("")
	for _, ver := range []string{"5.6", "7.2", "7.4", "8.1", "8.3"} {
		want := oldPlatformOf(ver, m)
		if got := detect.PlatformOf(ver, m); got != want {
			t.Errorf("PlatformOf(%q) = %q, old copy = %q", ver, got, want)
		}
	}
}
