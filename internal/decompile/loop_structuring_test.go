package decompile

import (
	"strings"
	"testing"

	"deionizer/internal/opline"
)

// FIX 2 — loop structuring. A while/for loop's entry JMP is a FORWARD jump to the
// condition at the loop foot, shaped exactly like an if/else "skip-else" JMP. When a
// loop immediately follows a guard `if`, the if-structurer used to CLAIM that entry
// JMP as its skip-else, folding the whole loop body into a bogus `elseif` arm and
// orphaning the backward latch as `// unstructured ZEND_JMPNZ`. This is the exact
// shape of a common assignment-in-condition read-loop. The structurer must instead defer the
// loop's entry JMP to the loop, emitting a real `while`.
func TestSynthLoopAfterGuardIf(t *testing.T) {
	m := meth(
		// if (!$x) { $x = 1; }
		opline.Op{Line: 1, Op: "ZEND_BOOL_NOT", Op1: cv("x"), Res: tmp(1)}, // 0
		opline.Op{Line: 1, Op: "ZEND_JMPZ", Op1: tmp(1), Op2: jmp(3)},      // 1 -> after guard body
		opline.Op{Line: 2, Op: "ZEND_ASSIGN", Op1: cv("x"), Op2: ci(1)},    // 2 guard body
		// $d = 0;  (between the guard and the loop)
		opline.Op{Line: 3, Op: "ZEND_ASSIGN", Op1: cv("d"), Op2: ci(0)}, // 3
		// while ($d < 3) { echo $d; }   compiled as entry-JMP; body; cond; JMPNZ back
		opline.Op{Line: 4, Op: "ZEND_JMP", Op1: jmp(999)},                                // 4 entry JMP (bogus target)
		opline.Op{Line: 5, Op: "ZEND_ECHO", Op1: cv("d")},                                // 5 body
		opline.Op{Line: 4, Op: "ZEND_IS_SMALLER", Op1: cv("d"), Op2: ci(3), Res: tmp(2)}, // 6 cond
		opline.Op{Line: 4, Op: "ZEND_JMPNZ", Op1: tmp(2), Op2: jmp(999)},                 // 7 latch (bogus target)
		opline.Op{Line: 6, Op: "ZEND_ECHO", Op1: cv("x")},                                // 8 after loop
		opline.Op{Line: 7, Op: "ZEND_RETURN", Op1: un(), Ext: 0xffffffff},                // 9
	)
	got, _ := Render(m)
	if !strings.Contains(got, "if (!$x) {") {
		t.Fatalf("guard if not recovered:\n%s", got)
	}
	if !strings.Contains(got, "while ($d < 3) {") {
		t.Fatalf("loop after guard-if not structured as while:\n%s", got)
	}
	if strings.Contains(got, "unstructured") {
		t.Fatalf("loop latch left unstructured:\n%s", got)
	}
	if strings.Contains(got, "elseif") {
		t.Fatalf("loop was folded into a bogus elseif:\n%s", got)
	}
	// The loop body's echo and the after-loop echo must both survive, once each.
	if n := strings.Count(got, "echo $d;"); n != 1 {
		t.Fatalf("loop body emitted %d times (want 1):\n%s", n, got)
	}
	if n := strings.Count(got, "echo $x;"); n != 1 {
		t.Fatalf("after-loop stmt emitted %d times (want 1):\n%s", n, got)
	}
}

// A read-loop `while (($e = read()) !== false)` — an assignment INSIDE the condition,
// preceded by a guard if — is the marquee real-world shape. The assignment must hoist
// into the rendered condition (no `$e` use-before-assign) and the latch must not leak.
func TestSynthReadLoopAssignCond(t *testing.T) {
	m := meth(
		// $d = opendir();  (a call producing the handle)
		opline.Op{Line: 1, Op: "ZEND_INIT_FCALL", Op2: cs("opendir")},    // 0
		opline.Op{Line: 1, Op: "ZEND_SEND_VAL", Op1: cs(".")},            // 1
		opline.Op{Line: 1, Op: "ZEND_DO_FCALL", Res: tmp(1)},             // 2
		opline.Op{Line: 1, Op: "ZEND_ASSIGN", Op1: cv("d"), Op2: tmp(1)}, // 3
		// while (($e = readdir($d)) !== false) { echo $e; }
		opline.Op{Line: 2, Op: "ZEND_JMP", Op1: jmp(999)},                                           // 4 entry JMP
		opline.Op{Line: 3, Op: "ZEND_ECHO", Op1: cv("e")},                                           // 5 body
		opline.Op{Line: 2, Op: "ZEND_INIT_FCALL", Op2: cs("readdir")},                               // 6 cond: readdir($d)
		opline.Op{Line: 2, Op: "ZEND_SEND_VAR", Op1: cv("d")},                                       // 7
		opline.Op{Line: 2, Op: "ZEND_DO_FCALL", Res: tmp(2)},                                        // 8
		opline.Op{Line: 2, Op: "ZEND_ASSIGN", Op1: cv("e"), Op2: tmp(2), Res: tmp(3)},               // 9 $e = readdir(...)
		opline.Op{Line: 2, Op: "ZEND_IS_NOT_IDENTICAL", Op1: tmp(3), Op2: cs("false"), Res: tmp(4)}, // 10 !== false
		opline.Op{Line: 2, Op: "ZEND_JMPNZ", Op1: tmp(4), Op2: jmp(999)},                            // 11 latch
		opline.Op{Line: 4, Op: "ZEND_RETURN", Op1: un(), Ext: 0xffffffff},                           // 12
	)
	got, _ := Render(m)
	if !strings.Contains(got, "while (") {
		t.Fatalf("read-loop not structured as while:\n%s", got)
	}
	if !strings.Contains(got, "$e = readdir($d)") {
		t.Fatalf("assignment-in-condition not hoisted into the while:\n%s", got)
	}
	if strings.Contains(got, "unstructured") {
		t.Fatalf("latch left unstructured:\n%s", got)
	}
	// $e must be assigned (in the condition) before it is echoed in the body — the
	// condition line must appear before the body's use in the rendered source.
	iCond := strings.Index(got, "$e = readdir")
	iUse := strings.Index(got, "echo $e;")
	if iCond < 0 || iUse < 0 || iCond > iUse {
		t.Fatalf("$e used before assignment (read-loop mis-hoisted):\n%s", got)
	}
}

// A guarded continue inside a foreach — `if (!$x) continue;` compiled as a BACKWARD
// `JMPZ $x -> FE_FETCH` — must render as `if (!$x) { continue; }`, not the previous
// `// decompiler: unstructured ZEND_JMPZ` note. This is the dominant shape of the
// automagick corpus's unstructured-jump notes.
func TestSynthForeachGuardedContinue(t *testing.T) {
	it := opline.Operand{T: "VAR", Num: 4}
	m := meth(
		opline.Op{Line: 1, Op: "ZEND_FE_RESET_R", Op1: cv("items"), Res: it},       // 0 reset
		opline.Op{Line: 1, Op: "ZEND_FE_FETCH_R", Op1: it, Op2: cv("x"), Ext: 160}, // 1 fetch (value $x)
		opline.Op{Line: 2, Op: "ZEND_JMPZ", Op1: cv("x"), Op2: jmp(1)},             // 2 if(!x) continue -> FE_FETCH
		opline.Op{Line: 3, Op: "ZEND_ECHO", Op1: cv("x")},                          // 3 body
		opline.Op{Line: 1, Op: "ZEND_JMP", Op1: jmp(1)},                            // 4 back-edge
		opline.Op{Line: 1, Op: "ZEND_FE_FREE", Op1: it},                            // 5 free
		opline.Op{Line: 4, Op: "ZEND_RETURN", Op1: un(), Ext: 0xffffffff},          // 6
	)
	got, _ := Render(m)
	if !strings.Contains(got, "foreach ($items as $x) {") {
		t.Fatalf("foreach not recovered:\n%s", got)
	}
	if !strings.Contains(got, "if (!$x) { continue; }") {
		t.Fatalf("guarded continue not recovered:\n%s", got)
	}
	if strings.Contains(got, "unstructured") {
		t.Fatalf("guarded continue left unstructured:\n%s", got)
	}
	if !strings.Contains(got, "echo $x;") {
		t.Fatalf("body dropped:\n%s", got)
	}
}
