package decompile

import (
	"strings"
	"testing"

	"deionizer/internal/opline"
)

// FIX 1 — defensive identifier emission. When a recovered/inferred scalar lands in
// PHP identifier position (method/function/static-method name) and is NOT a valid
// bareword identifier, render must emit a lint-safe guarded form, never a bareword
// that would fail `php -l`. These build the call oplines directly and assert the
// guarded shape, so a regression fails fast offline (no reveal image needed).

// A method call whose recovered name starts with a digit ("1Forma") must render via
// the dynamic member form `$o->{'1Forma'}(...)`, never the bareword `$o->1Forma(...)`.
func TestDefensiveMethodBadName(t *testing.T) {
	m := meth(
		opline.Op{Op: "ZEND_INIT_METHOD_CALL", Op1: cv("smarty"), Op2: cs("1Forma")}, // 0
		opline.Op{Op: "ZEND_SEND_VAL", Op1: cs("x")},                                 // 1
		opline.Op{Op: "ZEND_DO_FCALL", Res: tmp(1)},                                  // 2
		opline.Op{Op: "ZEND_RETURN", Op1: tmp(1)},                                    // 3
	)
	got, _ := Render(m)
	if !strings.Contains(got, "$smarty->{'1Forma'}(") {
		t.Fatalf("expected dynamic member form, got:\n%s", got)
	}
	if strings.Contains(got, "->1Forma(") {
		t.Fatalf("emitted a bareword non-identifier method name:\n%s", got)
	}
}

// A dynamic method call `$o->$m()` (op2 is a CV) must render `$o->{$m}()`, never drop
// the sigil to a bareword `$o->m()` (a different, wrong method).
func TestDefensiveMethodDynamicName(t *testing.T) {
	m := meth(
		opline.Op{Op: "ZEND_INIT_METHOD_CALL", Op1: cv("o"), Op2: cv("m")}, // 0
		opline.Op{Op: "ZEND_DO_FCALL", Res: tmp(1)},                        // 1
		opline.Op{Op: "ZEND_RETURN", Op1: tmp(1)},                          // 2
	)
	got, _ := Render(m)
	if !strings.Contains(got, "$o->{$m}(") {
		t.Fatalf("expected dynamic-name member form $o->{$m}(), got:\n%s", got)
	}
}

// A static method call with a non-identifier name must render `Foo::{'1bad'}(...)`.
func TestDefensiveStaticBadName(t *testing.T) {
	m := meth(
		opline.Op{Op: "ZEND_INIT_STATIC_METHOD_CALL", Op1: cs("Foo"), Op2: cs("1bad")}, // 0
		opline.Op{Op: "ZEND_SEND_VAL", Op1: cv("a")},                                   // 1
		opline.Op{Op: "ZEND_DO_FCALL", Res: tmp(1)},                                    // 2
		opline.Op{Op: "ZEND_RETURN", Op1: tmp(1)},                                      // 3
	)
	got, _ := Render(m)
	if !strings.Contains(got, "Foo::{'1bad'}(") {
		t.Fatalf("expected Foo::{'1bad'}(, got:\n%s", got)
	}
}

// A function call with a garbled name (dash/space — impossible as a bareword) must be
// routed through call_user_func('name', ...args), which lints on 5.6 (no direct
// string-literal call), never emitted as the bareword `des3-cbc-raw(...)`.
func TestDefensiveFuncBadName(t *testing.T) {
	m := meth(
		opline.Op{Op: "ZEND_INIT_FCALL_BY_NAME", Op2: cs("des3-cbc-raw")}, // 0
		opline.Op{Op: "ZEND_SEND_VAL", Op1: cv("a")},                      // 1
		opline.Op{Op: "ZEND_DO_FCALL_BY_NAME", Res: tmp(1)},               // 2
		opline.Op{Op: "ZEND_RETURN", Op1: tmp(1)},                         // 3
	)
	got, _ := Render(m)
	if !strings.Contains(got, "call_user_func(") || !strings.Contains(got, "'des3-cbc-raw'") {
		t.Fatalf("expected call_user_func('des3-cbc-raw', ...), got:\n%s", got)
	}
	if strings.Contains(got, "des3-cbc-raw(") {
		t.Fatalf("emitted a bareword non-identifier function name:\n%s", got)
	}
}

// A valid identifier method/function name must stay a bareword (no dynamic-member
// churn), so the guard never perturbs the common case.
func TestDefensiveGoodNamesUnchanged(t *testing.T) {
	m := meth(
		opline.Op{Op: "ZEND_INIT_METHOD_CALL", Op1: cv("o"), Op2: cs("doThing")}, // 0
		opline.Op{Op: "ZEND_DO_FCALL", Res: tmp(1)},                              // 1
		opline.Op{Op: "ZEND_RETURN", Op1: tmp(1)},                                // 2
	)
	got, _ := Render(m)
	if !strings.Contains(got, "$o->doThing(") {
		t.Fatalf("valid method name should stay bareword, got:\n%s", got)
	}
	if strings.Contains(got, "{'doThing'}") {
		t.Fatalf("valid name should not be guarded:\n%s", got)
	}
}
