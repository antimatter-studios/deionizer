// Package runtime holds the deionizer runtime matrix: for each PHP version
// we support, which container image recovers its interface (analyze.php), which
// image deep-decodes its bodies (reveal + decode), and whether it must run under
// an emulated x86-64 platform.
//
// The matrix is defined as Go defaults AND embedded from runtimes.yml (the yaml
// wins so the single source of truth stays human-editable). An external override
// file may be layered on for ad-hoc runs (Load's overlayPath, from --runtimes).
// Parsing is a tiny hand-rolled reader for our fixed 2-level yaml — no third-party
// deps, so `go build` never needs the network.
package runtime

import (
	_ "embed"
	"os"
	"strings"
)

//go:embed runtimes.yml
var embeddedYAML string

// Runtime is one PHP version's container matrix entry.
type Runtime struct {
	Image       string // interface-recovery image (analyze.php) — every version
	Platform    string // "linux/amd64" to force emulation, "" for native
	DecodeImage string // deep-decode image (reveal + decode), "" where not ported
}

// Matrix is the whole runtime table plus the probe version used for trial-loads.
type Matrix struct {
	Probe    string
	Runtimes map[string]Runtime
}

// defaults mirrors runtimes.yml so the binary still works if the embed is empty.
func defaults() *Matrix {
	return &Matrix{
		Probe: "7.4",
		Runtimes: map[string]Runtime{
			"5.6": {Image: "deionizer:php5.6", Platform: "linux/amd64", DecodeImage: "deionizer-ext56:v3"},
			"7.2": {Image: "deionizer:php7.2", Platform: "", DecodeImage: "deionizer-ext72:v2"},
			"7.4": {Image: "deionizer:php7.4", Platform: "", DecodeImage: "deionizer-ext:v20"},
			"8.1": {Image: "deionizer:php8.1", Platform: "", DecodeImage: "deionizer-ext81:v4"},
			"8.3": {Image: "deionizer:php8.3", Platform: "", DecodeImage: "deionizer-ext83:v4"},
		},
	}
}

// Load returns the runtime matrix: Go defaults, overlaid by the embedded
// runtimes.yml, overlaid by the file at overlayPath when that argument is
// non-empty and readable. The overlay path is passed in by the caller (from a
// flag) rather than read from the environment, so configuration is explicit.
func Load(overlayPath string) *Matrix {
	m := defaults()
	if embeddedYAML != "" {
		parseInto(m, embeddedYAML)
	}
	if overlayPath != "" {
		if b, err := os.ReadFile(overlayPath); err == nil {
			parseInto(m, string(b))
		}
	}
	return m
}

// Get returns the runtime entry for an exact version key.
func (m *Matrix) Get(ver string) (Runtime, bool) {
	r, ok := m.Runtimes[ver]
	return r, ok
}

// ProbeVersion is the version key used to trial-load version-less markers.
func (m *Matrix) ProbeVersion() string {
	if m.Probe == "" {
		return "7.4"
	}
	return m.Probe
}

// Nearest collapses an encoded target version (e.g. "5.3", "7.2", "8.0") onto the
// nearest runtime we actually run. Exact match wins; otherwise an ordered
// preference list (per exact version first, then per major); otherwise any
// runtime with the same major; else unchanged.
//
// Routing follows what each loader ACTUALLY accepts, not mere numeric proximity:
// an ionCube loader decodes its own minor plus a bounded span of older minors
// (the 8.3 loader accepts 8.2+8.3; the 8.1 loader accepts only 8.1). So an
// unmatched minor must climb to the runtime whose loader spans it — 8.2 belongs
// on the 8.3 runtime, NOT the numerically-closer 8.1. A per-version list is the
// only way to distinguish 8.0 from 8.2, which share a major but route apart.
func (m *Matrix) Nearest(dotted string) string {
	if _, ok := m.Runtimes[dotted]; ok {
		return dotted
	}
	major := dotted
	if i := strings.IndexByte(dotted, '.'); i >= 0 {
		major = dotted[:i]
	}
	// Per-version routing wins over the per-major default where a loader's actual
	// acceptance span diverges from numeric nearness.
	prefer, ok := map[string][]string{
		"8.0": {"8.1", "8.3"}, // 8.1 loader is nearest for 8.0
		"8.2": {"8.3"},        // ONLY the 8.3 loader decodes 8.2 — never fall back to 8.1
	}[dotted]
	if !ok {
		prefer = map[string][]string{
			"5": {"5.6"},
			"7": {"7.4", "7.2", "7.0"},
			"8": {"8.3", "8.1"}, // unknown 8.x: prefer the newest loader
		}[major]
	}
	for _, p := range prefer {
		if _, ok := m.Runtimes[p]; ok {
			return p
		}
	}
	for k := range m.Runtimes {
		if strings.SplitN(k, ".", 2)[0] == major {
			return k
		}
	}
	return dotted
}

// parseInto merges a fixed 2-level runtimes.yml document into m. The format is:
// top-level scalars (probe) plus a `runtimes:` map of version keys, each with
// image/platform/decode_image fields. Quotes and comments are stripped.
func parseInto(m *Matrix, doc string) {
	var cur string
	inRuntimes := false
	for _, raw := range strings.Split(doc, "\n") {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if !strings.HasPrefix(line, " ") { // top-level key
			k, v, _ := cut(trimmed, ":")
			if strings.TrimSpace(k) == "runtimes" {
				inRuntimes = true
				continue
			}
			inRuntimes = false
			if key := strings.TrimSpace(k); key == "probe" {
				m.Probe = unquote(strings.TrimSpace(v))
			}
			continue
		}
		if !inRuntimes {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if strings.HasSuffix(trimmed, ":") && indent <= 4 { // a version key: "5.6":
			cur = unquote(strings.TrimSuffix(trimmed, ":"))
			if _, ok := m.Runtimes[cur]; !ok {
				m.Runtimes[cur] = Runtime{}
			}
			continue
		}
		if cur == "" {
			continue
		}
		k, v, ok := cut(trimmed, ":")
		if !ok {
			continue
		}
		r := m.Runtimes[cur]
		val := unquote(strings.TrimSpace(v))
		switch strings.TrimSpace(k) {
		case "image":
			r.Image = val
		case "platform":
			r.Platform = val
		case "decode_image":
			r.DecodeImage = val
		}
		m.Runtimes[cur] = r
	}
}

func cut(s, sep string) (before, after string, found bool) {
	if i := strings.Index(s, sep); i >= 0 {
		return s[:i], s[i+len(sep):], true
	}
	return s, "", false
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}
