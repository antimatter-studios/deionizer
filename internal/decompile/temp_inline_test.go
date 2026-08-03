package decompile

import (
	"strings"
	"testing"

	"deionizer/internal/opline"
)

// FIX 3 — single-use temp propagation. ionCube renumbers a producer's result slot so
// it disagrees with the slot the immediately-following assignment reads, leaving
// `$x = $_vNN` with the producer's value dropped. linkDiscardedProducers retargets the
// producer to the consumed slot so the value inlines into the one consumer. This is the
// general (non-call) path — the call path is renumberedCallSink and is exercised by the
// harness goldens.
func TestSynthRenumberedTempInline(t *testing.T) {
	v := func(n int) opline.Operand { return opline.Operand{T: "VAR", Num: n} }
	m := meth(
		opline.Op{Op: "ZEND_CONCAT", Op1: cv("a"), Op2: cv("b"), Res: v(2)}, // 0 result v2, then never read
		opline.Op{Op: "ZEND_ASSIGN", Op1: cv("s"), Op2: v(15)},              // 1 $s = v15 (a phantom slot)
		opline.Op{Op: "ZEND_RETURN", Op1: cv("s")},                          // 2
	)
	got, _ := Render(m)
	if !strings.Contains(got, "$s = $a . $b") {
		t.Fatalf("renumbered temp not inlined into its consumer:\n%s", got)
	}
	if strings.Contains(got, "$_v") {
		t.Fatalf("phantom temp still leaked:\n%s", got)
	}
}

// The producer must NOT be hijacked when the read slot is a REAL earlier value (a
// genuine second use), only when it is a phantom — otherwise a live value is corrupted.
func TestSynthRenumberedTempNotHijacked(t *testing.T) {
	v := func(n int) opline.Operand { return opline.Operand{T: "VAR", Num: n} }
	m := meth(
		opline.Op{Op: "ZEND_CONCAT", Op1: cv("a"), Op2: cv("b"), Res: v(15)}, // 0 v15 = $a . $b (a real value)
		opline.Op{Op: "ZEND_CONCAT", Op1: cv("c"), Op2: cv("d"), Res: v(2)},  // 1 v2 = $c . $d, discarded
		opline.Op{Op: "ZEND_ASSIGN", Op1: cv("s"), Op2: v(15)},               // 2 $s = v15 (the REAL earlier value)
		opline.Op{Op: "ZEND_RETURN", Op1: cv("s")},                           // 3
	)
	got, _ := Render(m)
	// $s must read the real v15 ($a . $b), NOT be hijacked by the discarded v2 ($c . $d).
	if !strings.Contains(got, "$s = $a . $b") {
		t.Fatalf("real earlier value was hijacked or lost:\n%s", got)
	}
	if strings.Contains(got, "$c . $d") && strings.Contains(got, "$s = $c . $d") {
		t.Fatalf("discarded producer wrongly hijacked the live slot:\n%s", got)
	}
}
