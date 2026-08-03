//go:build nodecompiler

package main

// Fallback seam: opt-in ONLY with `-tags nodecompiler`. Uses the interim
// exact-signature renderer and disables the text-reveal bridge, so the CLI can
// still build if internal/decompile is ever broken. The default build (no tags)
// routes both through internal/decompile (ParseTextDump + RenderFile) via
// decompile_wired.go.

import (
	"fmt"

	"deionizer/internal/opline"
)

func decodeText(_ string, _ bool) ([]opline.Method, error) {
	return nil, fmt.Errorf("text reveal bridge needs the decompiler; rebuild with `-tags decompiler` once internal/decompile is stable")
}

func renderMethodsToPHP(methods []opline.Method, _ bool) (string, error) {
	return renderMethods(methods)
}

// applyTrace is a no-op in the fallback build (the behavioral side-channel lives
// in internal/decompile, which this build tag excludes).
func applyTrace(_ []opline.Method, _ string) {}
