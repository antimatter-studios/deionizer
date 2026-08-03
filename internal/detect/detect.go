// Package detect works out which PHP runtime an ionCube-encoded file needs.
//
// Two paths:
//
//  1. Marker read (cheap, no container): a bundle marker such as
//     `//ICB0 74:0 81:7ce4 82:10be1` lists its target versions directly, so we
//     pick the best one we have a runtime for. A plain marker (`//0046a`,
//     `//004ac`, `//004fb`) carries no version and yields "PROBE".
//
//  2. Trial-load probe (for PROBE markers): load one file under the probe runtime
//     (7.4) via internal/docker and read the loader's own verdict — "encoded by
//     the ionCube Encoder for PHP X.Y" — then map that onto the nearest runtime.
package detect

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"deionizer/internal/docker"
	"deionizer/internal/runtime"
)

// ProbeSentinel is what MarkerVersion returns when a marker is version-less and a
// trial-load is required.
const ProbeSentinel = "PROBE"

var (
	reBundle  = regexp.MustCompile(`//ICB0([^\n?]*)`)
	reTok     = regexp.MustCompile(`\b(\d{2})[:\s]`)
	reVerdict = regexp.MustCompile(`(?i)Encoder for PHP ([0-9]\.[0-9])`)
	// markerPref orders bundle tokens by which runtime we would rather run.
	markerPref = []string{"74", "81", "80", "82", "83", "73", "72", "70", "56"}
)

// IsEncoded reports whether a file looks ionCube-encoded (cheap header sniff).
func IsEncoded(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 400)
	n, _ := f.Read(buf)
	head := string(buf[:n])
	return strings.Contains(head, "ionCube") || strings.Contains(head, "_il_exec")
}

// MarkerVersion reads the file's ionCube marker and returns the runtime key to
// use, or ProbeSentinel when the marker carries no version.
func MarkerVersion(path string, m *runtime.Matrix) string {
	f, err := os.Open(path)
	if err != nil {
		return ProbeSentinel
	}
	defer f.Close()
	buf := make([]byte, 80)
	n, _ := f.Read(buf)
	head := string(buf[:n])

	bm := reBundle.FindStringSubmatch(head)
	if bm == nil {
		return ProbeSentinel
	}
	var toks []string
	for _, t := range reTok.FindAllStringSubmatch(bm[1], -1) {
		toks = append(toks, t[1])
	}
	for _, want := range markerPref {
		if contains(toks, want) {
			return m.Nearest(dot(want))
		}
	}
	if len(toks) > 0 {
		return m.Nearest(dot(toks[0]))
	}
	return ProbeSentinel
}

// PlatformFor is the pattern fallback for versions not in the matrix: PHP < 7.2
// (5.x / 7.0 / 7.1) has no arm64 base image and an x86-64-only loader, so it must
// run emulated. Ports platform_for() in bin/deionizer.
func PlatformFor(ver string) string {
	if strings.HasPrefix(ver, "5.") || ver == "7.0" || ver == "7.1" {
		return "linux/amd64"
	}
	return ""
}

// PlatformOf prefers the matrix's platform for ver, falling back to the pattern.
func PlatformOf(ver string, m *runtime.Matrix) string {
	if r, ok := m.Get(ver); ok && r.Platform != "" {
		return r.Platform
	}
	return PlatformFor(ver)
}

// ParseEncoderVerdict extracts the encoded PHP version ("5.6", "7.2", ...) from a
// loader message, or "" if the message carries no verdict.
func ParseEncoderVerdict(loaderOutput string) string {
	if mm := reVerdict.FindStringSubmatch(loaderOutput); mm != nil {
		return mm[1]
	}
	return ""
}

// EnsureFunc builds (if missing) the runtime image identified by ver/image, under
// the given platform. Supplied by the caller so detect need not own build policy.
type EnsureFunc func(ver, image, platform string) error

// ProbeVersion trial-loads one file under the probe runtime and returns the
// nearest runtime key for the encoder version the loader reports. If nothing is
// reported it falls back to the probe version itself. Ports probe_version().
func ProbeVersion(file string, m *runtime.Matrix, ensure EnsureFunc) (string, error) {
	pv := m.ProbeVersion()
	r, ok := m.Get(pv)
	image := docker.ImageName(pv)
	if ok && r.Image != "" {
		image = r.Image
	}
	platform := PlatformOf(pv, m)
	if ensure != nil {
		if err := ensure(pv, image, platform); err != nil {
			return "", err
		}
	}
	dir := filepath.Dir(file)
	base := filepath.Base(file)
	out, errOut, _ := docker.Run(
		image,
		[]docker.Mount{{Host: dir, Container: "/e", RO: true}},
		[]string{"-r", `error_reporting(0); require "/e/` + base + `";`},
		docker.RunOpts{Entrypoint: "php", Platform: platform, Network: "none"},
	)
	if enc := ParseEncoderVerdict(out + "\n" + errOut); enc != "" {
		return m.Nearest(enc), nil
	}
	return pv, nil
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// dot inserts a decimal point into a two-digit version marker: "74" -> "7.4".
// Callers only ever pass a 2-digit token (reTok captures \d{2}; markerPref holds
// 2-digit codes), so anything else is returned unchanged rather than mis-split.
func dot(tok string) string {
	if len(tok) != 2 {
		return tok
	}
	return tok[:1] + "." + tok[1:]
}
