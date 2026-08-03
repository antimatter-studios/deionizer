package decompile

import (
	"strings"
	"testing"

	"deionizer/internal/opline"
)

// These synthetic-opline tests exercise the target-free structurers (loop, if/
// elseif/else, short-circuit join) directly, so a regression in the reconstruction
// logic fails fast without needing a full reveal fixture. Jump targets are set to
// deliberately WRONG values to prove the structurers ignore them.

func cv(name string) opline.Operand { return opline.Operand{T: "CV", Var: name} }
func ci(v int) opline.Operand       { return opline.Operand{T: "CONST", Val: v} }
func cs(v string) opline.Operand    { return opline.Operand{T: "CONST", Val: v} }
func tmp(n int) opline.Operand      { return opline.Operand{T: "TMP", Num: n} }
func jmp(to int) opline.Operand     { return opline.Operand{T: "JMP", Jmp: to} }
func un() opline.Operand            { return opline.Operand{T: "UNUSED"} }

func meth(ops ...opline.Op) opline.Method {
	for i := range ops {
		ops[i].I = i
	}
	return opline.Method{Function: "f", Oplines: ops}
}

// A bottom-tested while, compiled `entry-JMP; body; cond; JMPNZ back`, with BOGUS
// jump targets — must still render as a while keyed off the line regression.
func TestSynthWhileLoop(t *testing.T) {
	m := meth(
		opline.Op{Line: 1, Op: "ZEND_ASSIGN", Op1: cv("x"), Op2: ci(0)},                  // 0
		opline.Op{Line: 2, Op: "ZEND_JMP", Op1: jmp(999)},                                // 1 entry
		opline.Op{Line: 3, Op: "ZEND_ECHO", Op1: cv("x")},                                // 2 body
		opline.Op{Line: 2, Op: "ZEND_IS_SMALLER", Op1: cv("x"), Op2: ci(5), Res: tmp(1)}, // 3 cond
		opline.Op{Line: 2, Op: "ZEND_JMPNZ", Op1: tmp(1), Op2: jmp(999)},                 // 4 latch
		opline.Op{Line: 4, Op: "ZEND_RETURN", Op1: un(), Ext: 0xffffffff},                // 5
	)
	got, _ := Render(m)
	if !strings.Contains(got, "while ($x < 5) {") {
		t.Fatalf("expected while loop, got:\n%s", got)
	}
	if !strings.Contains(got, "echo $x;") {
		t.Fatalf("expected loop body, got:\n%s", got)
	}
	if strings.Contains(got, "decompiler:") {
		t.Fatalf("unexpected note:\n%s", got)
	}
}

// if / elseif / else with corrupted merge targets must chain, not split into three
// separate ifs, and the terminal else must be bounded by the sibling arm length.
func TestSynthIfElseifElse(t *testing.T) {
	m := meth(
		opline.Op{Line: 1, Op: "ZEND_IS_EQUAL", Op1: cv("t"), Op2: ci(1), Res: tmp(1)}, // 0
		opline.Op{Line: 1, Op: "ZEND_JMPZ", Op1: tmp(1), Op2: jmp(4)},                  // 1 -> elseif
		opline.Op{Line: 2, Op: "ZEND_ASSIGN", Op1: cv("r"), Op2: cs("a")},              // 2 then
		opline.Op{Line: 2, Op: "ZEND_JMP", Op1: jmp(999)},                              // 3 skip (bogus)
		opline.Op{Line: 3, Op: "ZEND_IS_EQUAL", Op1: cv("t"), Op2: ci(2), Res: tmp(2)}, // 4 elseif cond
		opline.Op{Line: 3, Op: "ZEND_JMPZ", Op1: tmp(2), Op2: jmp(8)},                  // 5 -> else
		opline.Op{Line: 4, Op: "ZEND_ASSIGN", Op1: cv("r"), Op2: cs("b")},              // 6 elseif body
		opline.Op{Line: 4, Op: "ZEND_JMP", Op1: jmp(999)},                              // 7 skip (bogus)
		opline.Op{Line: 5, Op: "ZEND_ASSIGN", Op1: cv("r"), Op2: cs("c")},              // 8 else body
		opline.Op{Line: 6, Op: "ZEND_ECHO", Op1: cv("r")},                              // 9 merge
		opline.Op{Line: 7, Op: "ZEND_RETURN", Op1: un(), Ext: 0xffffffff},              // 10
	)
	got, _ := Render(m)
	for _, want := range []string{"if ($t == 1) {", "} elseif ($t == 2) {", "} else {", "echo $r;"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	// The merge (echo $r) must sit OUTSIDE the chain, exactly once.
	if n := strings.Count(got, "echo $r;"); n != 1 {
		t.Fatalf("merge emitted %d times (want 1):\n%s", n, got)
	}
}

// A guard `if (c) return;` whose then-target is corrupted must clamp to the exit,
// not swallow the rest of the function.
func TestSynthGuardReturn(t *testing.T) {
	m := meth(
		opline.Op{Line: 1, Op: "ZEND_IS_IDENTICAL", Op1: cv("x"), Op2: ci(0), Res: tmp(1)}, // 0
		opline.Op{Line: 1, Op: "ZEND_JMPZ", Op1: tmp(1), Op2: jmp(999)},                    // 1 bogus fwd
		opline.Op{Line: 2, Op: "ZEND_RETURN", Op1: cs("zero")},                             // 2 guard body
		opline.Op{Line: 3, Op: "ZEND_ECHO", Op1: cv("x")},                                  // 3 after guard
		opline.Op{Line: 4, Op: "ZEND_RETURN", Op1: un(), Ext: 0xffffffff},                  // 4
	)
	got, _ := Render(m)
	if !strings.Contains(got, "if ($x === 0) {") || !strings.Contains(got, "return 'zero';") {
		t.Fatalf("guard not structured:\n%s", got)
	}
	// echo must be AFTER the closed guard, not inside it.
	iEcho := strings.Index(got, "echo $x;")
	iClose := strings.Index(got, "return 'zero';")
	if iEcho < iClose {
		t.Fatalf("echo swallowed into guard:\n%s", got)
	}
}

// Operator precedence must parenthesise correctly: a*b+c needs no parens on the
// mul, but (a+b)*c must keep them. Folded from arithmetic oplines (no jumps).
func TestSynthOperatorPrecedence(t *testing.T) {
	// $r = $a + $b * $c;  -> a + b * c   (no parens)
	m1 := meth(
		opline.Op{Line: 1, Op: "ZEND_MUL", Op1: cv("b"), Op2: cv("c"), Res: tmp(1)},
		opline.Op{Line: 1, Op: "ZEND_ADD", Op1: cv("a"), Op2: tmp(1), Res: tmp(2)},
		opline.Op{Line: 1, Op: "ZEND_RETURN", Op1: tmp(2)},
	)
	if got, _ := Render(m1); !strings.Contains(got, "return $a + $b * $c;") {
		t.Fatalf("mul-in-add mis-parenthesised:\n%s", got)
	}
	// $r = ($a + $b) * $c; -> (a + b) * c   (parens preserved)
	m2 := meth(
		opline.Op{Line: 1, Op: "ZEND_ADD", Op1: cv("a"), Op2: cv("b"), Res: tmp(1)},
		opline.Op{Line: 1, Op: "ZEND_MUL", Op1: tmp(1), Op2: cv("c"), Res: tmp(2)},
		opline.Op{Line: 1, Op: "ZEND_RETURN", Op1: tmp(2)},
	)
	if got, _ := Render(m2); !strings.Contains(got, "return ($a + $b) * $c;") {
		t.Fatalf("add-in-mul lost parens:\n%s", got)
	}
}

// A `??=` compound-assign whose QM_ASSIGN "join" opline is keytab-mislabeled as an
// ASSIGN-family opcode must still be recognised — otherwise the whole statement is
// silently dropped. ionCube's non-deterministic encoding relabels that join to a
// random opcode per encode; for a fraction of encodings it lands on ASSIGN /
// ASSIGN_DIM / ASSIGN_OBJ, which a name-only scan would swallow as a phantom second
// assignment. coalesceAssign matches the join by SHAPE+result-slot instead, so the
// mislabel is harmless. (Regression: the 8.x `??=` intermittent scalar/statement drop.)
func TestSynthCoalesceAssignMislabeledJoin(t *testing.T) {
	// $x ??= 'set' — the join (op2) reveals as ZEND_ASSIGN, not ZEND_QM_ASSIGN.
	simple := meth(
		opline.Op{Line: 1, Op: "ZEND_ASSIGN", Op1: cv("x"), Op2: opline.Operand{T: "CONST", Val: nil}}, // 0 $x = null
		opline.Op{Line: 2, Op: "ZEND_COALESCE", Op1: cv("x"), Res: tmp(11)},                            // 1
		opline.Op{Line: 2, Op: "ZEND_ASSIGN", Op1: cv("x"), Op2: cs("set"), Res: tmp(12)},              // 2 real assign
		opline.Op{Line: 2, Op: "ZEND_ASSIGN", Op1: tmp(12), Op2: un(), Res: tmp(11)},                   // 3 join, MISLABELED
		opline.Op{Line: 2, Op: "ZEND_FREE", Op1: tmp(11)},                                              // 4 discards -> statement
		opline.Op{Line: 3, Op: "ZEND_RETURN", Op1: un(), Ext: 0xffffffff},                              // 5
	)
	if got, _ := Render(simple); !strings.Contains(got, "$x ??= 'set';") {
		t.Fatalf("mislabeled-join ??= dropped; expected \"$x ??= 'set';\":\n%s", got)
	}

	// $data['b'] ??= 2 — dim target, join (op4) reveals as ZEND_ASSIGN_DIM.
	dim := meth(
		opline.Op{Line: 1, Op: "ZEND_FETCH_DIM_IS", Op1: cv("data"), Op2: cs("b"), Res: tmp(7)}, // 0
		opline.Op{Line: 1, Op: "ZEND_COALESCE", Op1: tmp(7), Res: tmp(8)},                       // 1
		opline.Op{Line: 1, Op: "ZEND_ASSIGN_DIM", Op1: cv("data"), Op2: cs("b"), Res: tmp(9)},   // 2 real assign
		opline.Op{Line: 1, Op: "ZEND_OP_DATA", Op1: ci(2)},                                      // 3 rhs value
		opline.Op{Line: 1, Op: "ZEND_ASSIGN_DIM", Op1: tmp(9), Op2: un(), Res: tmp(8)},          // 4 join, MISLABELED
		opline.Op{Line: 1, Op: "ZEND_FREE", Op1: tmp(8)},                                        // 5
		opline.Op{Line: 2, Op: "ZEND_RETURN", Op1: un(), Ext: 0xffffffff},                       // 6
	)
	if got, _ := Render(dim); !strings.Contains(got, "$data['b'] ??= 2;") {
		t.Fatalf("mislabeled-join ??= (dim) dropped; expected \"$data['b'] ??= 2;\":\n%s", got)
	}
}

// clampOutOfLoop / clampOutOfForeach push a boundary out of a construct they open.
func TestSynthClamps(t *testing.T) {
	ev := &evaluator{
		loops:   []loopSpan{{header: 2, end: 9}},
		feSpans: []feSpan{{reset: 12, fetch: 13, free: 18}},
	}
	if got := ev.clampOutOfLoop(5); got != 9 {
		t.Fatalf("clampOutOfLoop(5)=%d want 9", got)
	}
	if got := ev.clampOutOfLoop(20); got != 20 {
		t.Fatalf("clampOutOfLoop(20)=%d want 20 (outside)", got)
	}
	if got := ev.clampOutOfForeach(15); got != 19 {
		t.Fatalf("clampOutOfForeach(15)=%d want 19", got)
	}
	if got := ev.clampOutOfForeach(2); got != 2 {
		t.Fatalf("clampOutOfForeach(2)=%d want 2 (outside)", got)
	}
}
