package decompile

import (
	"strings"
	"testing"
)

// fixtureJSON is a hand-authored opline JSON matching the frozen schema, exactly
// what the C reveal shim will emit. It models:
//
//	function foo($x) {
//	    if (isset($x)) {
//	        return strlen($x);
//	    }
//	    return 0;
//	}
const fixtureJSON = `[
 {"class":"","function":"foo","file":"/enc/x.php","params":[{"name":"x"}],"oplines":[
   {"i":0,"opcode":"ZEND_RECV","op1":{"t":"UNUSED"},"op2":{"t":"UNUSED"},"result":{"t":"CV","var":"x"}},
   {"i":1,"opcode":"ZEND_ISSET_ISEMPTY_VAR","op1":{"t":"CV","var":"x"},"op2":{"t":"UNUSED"},"result":{"t":"TMP","num":1},"ext":0},
   {"i":2,"opcode":"ZEND_JMPZ","op1":{"t":"TMP","num":1},"op2":{"t":"JMP","jmp":7},"result":{"t":"UNUSED"}},
   {"i":3,"opcode":"ZEND_INIT_FCALL_BY_NAME","op1":{"t":"UNUSED"},"op2":{"t":"CONST","val":"strlen"},"result":{"t":"UNUSED"}},
   {"i":4,"opcode":"ZEND_SEND_VAR","op1":{"t":"CV","var":"x"},"op2":{"t":"UNUSED"},"result":{"t":"UNUSED"}},
   {"i":5,"opcode":"ZEND_DO_FCALL_BY_NAME","op1":{"t":"UNUSED"},"op2":{"t":"UNUSED"},"result":{"t":"VAR","num":2}},
   {"i":6,"opcode":"ZEND_RETURN","op1":{"t":"VAR","num":2},"op2":{"t":"UNUSED"},"result":{"t":"UNUSED"}},
   {"i":7,"opcode":"ZEND_RETURN","op1":{"t":"CONST","val":0},"op2":{"t":"UNUSED"},"result":{"t":"UNUSED"}}
 ]}
]`

func TestParseJSONContract(t *testing.T) {
	methods, err := ParseJSON(strings.NewReader(fixtureJSON))
	if err != nil {
		t.Fatal(err)
	}
	if len(methods) != 1 {
		t.Fatalf("want 1 method, got %d", len(methods))
	}
	php, err := Render(methods[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"function foo($x) {",
		"if (isset($x)) {",
		"return strlen($x);",
		"return 0;",
	} {
		if !strings.Contains(php, want) {
			t.Errorf("rendered PHP missing %q:\n%s", want, php)
		}
	}
	// the discarded-vs-used liveness: strlen($x) must be inside the return, not a
	// separate statement.
	if strings.Count(php, "strlen($x)") != 1 {
		t.Errorf("expected exactly one strlen($x):\n%s", php)
	}
}

func TestRenderPublicAPISmoke(t *testing.T) {
	methods, _ := ParseJSON(strings.NewReader(fixtureJSON))
	file, err := RenderFile(methods)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(file, "<?php") {
		t.Errorf("RenderFile must start with <?php:\n%s", file)
	}
}
