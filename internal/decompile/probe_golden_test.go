package decompile

import (
	"os"
	"path/filepath"
	"testing"
)

// probeGoldens are frozen deionizer_reveal_json captures (PHP 7.4, image v19 — correct
// operators) of the tests/fixtures/*.php construct probes, each asserted BYTE-FOR-
// BYTE against a committed golden RenderFile output. These are the fresh matched
// pairs from the behavioral harness: a regression that would flip a harness cell to
// FAIL turns `go test` RED here first, offline. Regenerate with GOLDEN_UPDATE=1.
var probeGoldens = []string{
	"ternary",            // full `?:` (JMPZ ternary) + short `?:` (JMP_SET)
	"null_coalesce",      // `??` (COALESCE) + nested int in an array literal
	"switch_fallthrough", // SWITCH_STRING: table labels, IS_EQUAL chain, real default + shared merge
	"references",         // foreach ($a as &$v) by-reference + `$b = &$a`
	"dowhile_loop",       // bottom-tested do { } while (cond)
	"arrow_fn",           // fn(x) => … inlined with auto-captured vars
	"closures",           // closure body inlined at its use site (use()/auto-capture)
	"byref_params",       // &$param recovered from caller SEND_REF; default = 1
	"try_catch_finally",  // try/catch/finally from CATCH + FAST_CALL/FAST_RET
	// PHP 8.x (Zend 4) reveals (image decode81/83:v2):
	"match_expr_81",    // 8.1: match(true){…} from the IS_IDENTICAL/JMPNZ arm chain
	"ternary_83",       // 8.3: `?:`/ternary arms via QM_ASSIGN-shape (opcode mislabelled on 8.3)
	"null_coalesce_83", // 8.3: `??` fallback via QM_ASSIGN-shape recovery
	// Construct fixes locked from real encode->reveal captures (tests/realworld):
	"closures_byref",       // 7.4: `use (&$n)` by-ref capture (BIND_LEXICAL ZEND_BIND_REF)
	"multicatch",           // 7.4: `catch (A | B $e)` merged from consecutive CATCH arms
	"generators",           // 7.4: yield / yield from / keyed yield bodies
	"list_destructuring",   // 7.4: `foreach (… as [$a, $b])` list pattern target
	"arrow_fn_capture",     // 7.4: curried `fn($x) => fn($y) => …` keyed by unique DECLARE
	"named_args",           // 8.1: `box(label: 'A')` named argument from SEND op2
	"match_subject",        // 8.1: `match ($s) { 'a','b' => …, default => … }` jump table
	"first_class_callable", // 8.1: `f(...)`, `$o->m(...)`, `C::m(...)` via CALLABLE_CONVERT
	"nullsafe",             // 8.1: `$o->p?->m()` nullsafe arrow from JMP_NULL
	"ns_fcall",             // 7+: unqualified call in a namespace (NS_FCALL_BY_NAME) renders bare `count($x)`, not the FQN probe name
	"ns_const",             // 7+: unqualified constant in a namespace (FETCH_CONSTANT probe name) renders bare `ENT_QUOTES`, not IC_UNRESOLVED_CONST
	// Control-flow shapes: each pins a structurer form that has no post-dominator
	// merge, a second FE_FETCH target, or a nested-region break target.
	"switch_return_arms",    // 7.4: every arm RETURNs — switch with no shared merge
	"if_elseif_else",        // 7.4: linear if/elseif/elseif/else cascade
	"foreach_key_nested_if", // 7.4: `as $k => $v` key binding + `continue` in a nested if
	"loop_switch",           // 7.4: while loop wrapping a switch (case break != loop break)
	// Core constructs captured from tests/fixtures/*.php on 7.4:
	"arrays_nested_assoc",     // nested associative array literals
	"class_consts",            // class constants + self:: access
	"default_params",          // scalar parameter defaults via RECV_INIT
	"for_loop",                // three-clause for
	"foreach_nested",          // inner loop over each outer element
	"foreach_simple",          // plain value-only foreach
	"methods_static_instance", // static vs instance method dispatch
	"string_concat_interp",    // concat chains + "$var" interpolation
	"variadic_params",         // ...$args collection
	"while_loop",              // pre-tested loop
	// File-level {main} recovery: a pure-procedural file (no fns/classes) whose
	// top-level code lives in the transient {main} op_array (kind:"main"), rendered
	// as unwrapped file-scope statements (define/assign/array/echo/if/foreach).
	"main_procedural",
}

func probeDir() string {
	wd, _ := os.Getwd()
	return filepath.Join(wd, "testdata", "probes")
}

func TestProbeGoldens(t *testing.T) {
	update := os.Getenv("GOLDEN_UPDATE") != ""
	for _, name := range probeGoldens {
		name := name
		t.Run(name, func(t *testing.T) {
			fh, err := os.Open(filepath.Join(probeDir(), name+".json"))
			if err != nil {
				t.Skipf("probe %s: %v", name, err)
			}
			defer fh.Close()
			methods, err := ParseJSON(fh)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			got, err := RenderFile(methods)
			if err != nil {
				t.Fatalf("render %s: %v", name, err)
			}
			path := filepath.Join(goldenDir(), "probe_"+name+".php")
			if update {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				t.Logf("wrote golden probe_%s.php (%d bytes)", name, len(got))
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden probe_%s.php (run GOLDEN_UPDATE=1): %v", name, err)
			}
			if got != string(want) {
				t.Errorf("probe %s drifted.\n--- got ---\n%s\n--- want ---\n%s", name, got, string(want))
			}
		})
	}
}
