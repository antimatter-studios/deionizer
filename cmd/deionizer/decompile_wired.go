//go:build !nodecompiler

package main

// Wired decompiler seam: ON BY DEFAULT (internal/decompile is stable). Opt OUT
// with `-tags nodecompiler` to fall back to the interim signature renderer.
// Routes the oplines->PHP step through the decompiler agent's package (internal/decompile):
//   - decodeText           -> decompile.ParseTextDump (deionizer_reveal_dump text bridge)
//   - renderMethodsToPHP   -> decompile.RenderFile     (full oplines -> PHP)
// This is the ONLY compile-time reference to internal/decompile, so the fallback
// build (-tags nodecompiler) compiles without depending on that package.

import (
	"strings"

	"deionizer/internal/decompile"
	"deionizer/internal/opline"
)

func decodeText(text string, zend56 bool) ([]opline.Method, error) {
	return decompile.ParseTextDump(strings.NewReader(text), zend56)
}

func renderMethodsToPHP(methods []opline.Method, zend56 bool) (string, error) {
	return decompile.RenderFileZend56(methods, zend56)
}

// applyTrace fills 5.x encrypted scalar CONSTs from the behavioral side-channel
// (the decode56 zend_execute_internal trace the driver emits): it parses the
// `==== trace: Class::method ====` / `IC_TRACE builtin(args)` lines out of the
// driver's stdout and matches the decrypted built-in arguments back onto each
// method's reconstructed call sites (see internal/decompile/sidechannel.go).
// A no-op when the stdout carries no trace section.
func applyTrace(methods []opline.Method, driverStdout string) {
	tr := decompile.ParseTraceFile(strings.NewReader(driverStdout))
	if len(tr) == 0 {
		return
	}
	decompile.ApplyTraceAll(methods, tr)
}
