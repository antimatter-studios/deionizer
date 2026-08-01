package decompile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRenumberedCallResultCoalesce guards the ionCube 7.x/8.x call-result slot
// renumbering fix (renumberedCallSink in render.go).
//
// The production ionCube encoder (observed on a PHP 7.4 corpus) reports a
// DO_FCALL's result in a VAR slot whose number disagrees with the slot the
// immediately-following assignment reads — a constant +15 offset. The call then
// looks discarded and the assignment references an un-produced phantom slot,
// which used to render as the unset synthetic temp `$x = $_vNN`.
//
// The trial (evaluation) encoder numbers call-result slots consistently, so this
// exact shape CANNOT be produced by self-encoding a fixture — the real corpora
// were encoded by the production encoder. This test therefore pins the fix from a
// hand-authored reveal (testdata/renumber_reveal.json) that reproduces the renumber
// for both a plain `$x = call(...)` (ZEND_ASSIGN) and a compound `$x .= call(...)`
// (ZEND_ASSIGN_OP). It fails before the fix (output contains `$_v28` / `$_v30`)
// and passes after; it is re-validated end-to-end against real 7.4 corpora
// (147 -> 0 `$_vNN`) in the round that introduced it.
func TestRenumberedCallResultCoalesce(t *testing.T) {
	fh, err := os.Open(filepath.Join("testdata", "renumber_reveal.json"))
	if err != nil {
		t.Fatalf("open reveal: %v", err)
	}
	defer fh.Close()
	methods, err := ParseJSON(fh)
	if err != nil {
		t.Fatalf("parse reveal: %v", err)
	}
	out, err := RenderFile(methods)
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	// The renumbered call result must NOT leak as an unset synthetic temp.
	if strings.Contains(out, "$_v") {
		t.Errorf("renumbered call result leaked as a synthetic temp; want none.\n%s", out)
	}

	// Plain assignment recovers `$conflicts = $this->detectConflicts($vars['rules']);`
	// (was `$conflicts = $_v28;`), and the by-ref-candidate array element survives.
	wantPlain := "$conflicts = $this->detectConflicts($vars['rules']);"
	if !strings.Contains(out, wantPlain) {
		t.Errorf("plain call->assign not coalesced; want %q.\n%s", wantPlain, out)
	}

	// Compound assignment recovers `$html .= App\Admin\renderRow($vars['tpl']);`
	// (was `$html .= $_v30;`).
	wantCompound := "renderRow($vars['tpl'])"
	if !strings.Contains(out, wantCompound) || !strings.Contains(out, ".= ") {
		t.Errorf("compound call->assign_op not coalesced; want %q with `.=`.\n%s", wantCompound, out)
	}

	// The coalesced call must NOT ALSO appear as a discarded expression statement
	// (belt-and-suspenders against a double emission).
	if strings.Contains(out, "$this->detectConflicts($vars['rules']);\n") &&
		strings.Count(out, "detectConflicts($vars['rules'])") != 1 {
		t.Errorf("call emitted twice (discarded + assigned).\n%s", out)
	}
}
