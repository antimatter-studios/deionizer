package decompile

import (
	"strings"
	"testing"

	"deionizer/internal/opline"
)

// The self/parent/static class keyword is decoded from the low nibble of a
// ZEND_FETCH_CLASS type. Two sites read it — className (INIT_STATIC_METHOD_CALL,
// FETCH_CLASS_CONSTANT, FETCH_STATIC_PROP) and the ZEND_FETCH_CLASS opcode (NEW /
// instanceof) — and they once disagreed on both the field AND the default. They
// now share fetchClassKeyword; these tests lock the mapping so parent::/static::
// can never silently collapse to self:: (or to each other) again. The existing
// whole-file goldens only exercise self::, so this is the sole coverage of the
// parent/static arms.

func unusedClass(nibble int) opline.Operand { return opline.Operand{T: "UNUSED", Num: nibble} }
func varN(n int) opline.Operand             { return opline.Operand{T: "VAR", Num: n} }

// fetchClassKeyword is the single source of truth for the keyword nibble: 1=self,
// 2=parent, 3=static; anything else (0=DEFAULT / a dropped type) returns ok=false
// so each call site applies its own context-correct fallback.
func TestFetchClassKeywordMapping(t *testing.T) {
	type want struct {
		kw string
		ok bool
	}
	cases := map[int]want{
		0: {"", false}, // ZEND_FETCH_CLASS_DEFAULT — no keyword; caller decides
		1: {"self", true},
		2: {"parent", true},
		3: {"static", true},
		4: {"", false}, // AUTO — not a keyword; caller decides
	}
	for nibble, w := range cases {
		if kw, ok := fetchClassKeyword(nibble); kw != w.kw || ok != w.ok {
			t.Fatalf("fetchClassKeyword(%d) = (%q,%v), want (%q,%v)", nibble, kw, ok, w.kw, w.ok)
		}
	}
}

// className must map an UNUSED class operand's num nibble through the same table,
// so a static method call / class-const / static-prop keeps parent::/static::.
func TestClassNameKeywordNibble(t *testing.T) {
	ev := &evaluator{}
	cases := map[int]string{0: "self", 1: "self", 2: "parent", 3: "static"}
	for nibble, want := range cases {
		if got := ev.className(unusedClass(nibble)); got != want {
			t.Fatalf("className(UNUSED num=%d) = %q, want %q", nibble, got, want)
		}
	}
}

// End-to-end through Render: parent:: and static:: must survive on the static
// method call (className), the class constant (className), and the NEW class ref
// (ZEND_FETCH_CLASS) paths — the two decode sites that were unified.
func TestSynthParentStaticClassRefs(t *testing.T) {
	// parent::foo() / static::foo() — INIT_STATIC_METHOD_CALL reads op1's nibble.
	staticCall := func(nibble int) string {
		m := meth(
			opline.Op{Line: 1, Op: "ZEND_INIT_STATIC_METHOD_CALL", Op1: unusedClass(nibble), Op2: cs("foo"), Res: un()},
			opline.Op{Line: 1, Op: "ZEND_DO_FCALL_BY_NAME", Op1: un(), Op2: un(), Res: varN(1), Ext: 1},
			opline.Op{Line: 1, Op: "ZEND_RETURN", Op1: varN(1)},
			opline.Op{Line: 2, Op: "ZEND_RETURN", Op1: un(), Ext: 0xffffffff},
		)
		got, _ := Render(m)
		return got
	}
	if got := staticCall(2); !strings.Contains(got, "parent::foo()") {
		t.Fatalf("parent:: method call lost; want parent::foo():\n%s", got)
	}
	if got := staticCall(3); !strings.Contains(got, "static::foo()") {
		t.Fatalf("static:: method call lost; want static::foo():\n%s", got)
	}

	// parent::BAR / static::BAR — FETCH_CLASS_CONSTANT reads op1's nibble.
	classConst := func(nibble int) string {
		m := meth(
			opline.Op{Line: 1, Op: "ZEND_FETCH_CLASS_CONSTANT", Op1: unusedClass(nibble), Op2: cs("BAR"), Res: varN(1)},
			opline.Op{Line: 1, Op: "ZEND_RETURN", Op1: varN(1)},
			opline.Op{Line: 2, Op: "ZEND_RETURN", Op1: un(), Ext: 0xffffffff},
		)
		got, _ := Render(m)
		return got
	}
	if got := classConst(2); !strings.Contains(got, "parent::BAR") {
		t.Fatalf("parent::CONST lost; want parent::BAR:\n%s", got)
	}
	if got := classConst(3); !strings.Contains(got, "static::BAR") {
		t.Fatalf("static::CONST lost; want static::BAR:\n%s", got)
	}

	// new parent() / new static() — ZEND_FETCH_CLASS reads the ext nibble.
	newClass := func(nibble int) string {
		m := meth(
			opline.Op{Line: 1, Op: "ZEND_FETCH_CLASS", Op1: un(), Op2: un(), Res: varN(1), Ext: uint64(nibble)},
			opline.Op{Line: 1, Op: "ZEND_NEW", Op1: varN(1), Op2: un(), Res: varN(2)},
			opline.Op{Line: 1, Op: "ZEND_DO_FCALL_BY_NAME", Op1: un(), Op2: un(), Res: varN(3), Ext: 1},
			opline.Op{Line: 1, Op: "ZEND_RETURN", Op1: varN(2)},
			opline.Op{Line: 2, Op: "ZEND_RETURN", Op1: un(), Ext: 0xffffffff},
		)
		got, _ := Render(m)
		return got
	}
	if got := newClass(2); !strings.Contains(got, "new parent") {
		t.Fatalf("new parent lost:\n%s", got)
	}
	if got := newClass(3); !strings.Contains(got, "new static") {
		t.Fatalf("new static lost:\n%s", got)
	}
	// Dropped type nibble (Zend 2.6 / 5.6 `new static()`): a FETCH_CLASS with no
	// keyword nibble and an UNUSED class operand must default to static:: to keep late
	// static binding. Rendering self:: here regressed the 5.6 late-static-binding and
	// static-factory matrix cells — this pins the empirically-correct fallback.
	if got := newClass(0); !strings.Contains(got, "new static") {
		t.Fatalf("new static() with dropped nibble must stay static::, got:\n%s", got)
	}
}
