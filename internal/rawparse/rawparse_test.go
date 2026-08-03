package rawparse

import (
	"encoding/json"
	"os"
	"testing"

	"deionizer/internal/opline"
)

// The matched pair below is captured from testdata/src/*.php — fixtures we author,
// encode with the ionCube trial encoder (PHP 7.4), and dump through BOTH shim
// entry points of the same image, so the two sides cannot drift apart for any
// reason other than a real disagreement. Regenerate both together or not at all.
const (
	rawFixture = "testdata/sample_corpus_74.raw"
	refFixture = "testdata/sample_corpus_74.deionizer_reveal_json.json"
)

// canon runs bytes of a []opline.Method JSON through the frozen schema and back,
// producing a stable, sorted, canonical encoding. Both the Go-produced output
// and the C deionizer_reveal_json reference pass through the SAME pipeline, so any
// representable difference (a field, a value, an operand) surfaces, while
// harmless encoding nuances (int64 vs JSON-float, omitempty) are applied
// uniformly to both sides.
func canon(t *testing.T, b []byte) ([]byte, []opline.Method) {
	t.Helper()
	var ms []opline.Method
	if err := json.Unmarshal(b, &ms); err != nil {
		t.Fatalf("unmarshal []opline.Method: %v", err)
	}
	SortMethods(ms)
	out, err := json.Marshal(ms)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return out, ms
}

func perMethodJSON(t *testing.T, ms []opline.Method) map[string]string {
	t.Helper()
	m := make(map[string]string, len(ms))
	for _, x := range ms {
		b, err := json.Marshal(x)
		if err != nil {
			t.Fatal(err)
		}
		m[x.Class+"::"+x.Function] = string(b)
	}
	return m
}

// TestEquivalence is the red-green lock: the Go interpretation of the minimal
// C dumper's raw blob must equal decode.c's deionizer_reveal_json output, method for
// method and field for field, across the TextFormat+ConfigStorage 7.4 corpus.
func TestEquivalence(t *testing.T) {
	raw, err := os.ReadFile(rawFixture)
	if err != nil {
		t.Fatalf("read raw fixture: %v", err)
	}
	methods, err := Parse(raw, Zend34)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(methods) != 17 {
		t.Fatalf("expected 17 methods, got %d", len(methods))
	}
	SortMethods(methods)
	mineJSON, err := json.Marshal(methods)
	if err != nil {
		t.Fatal(err)
	}

	refBytes, err := os.ReadFile(refFixture)
	if err != nil {
		t.Fatalf("read ref fixture: %v", err)
	}

	mineCanon, mineMs := canon(t, mineJSON)
	refCanon, refMs := canon(t, refBytes)

	if string(mineCanon) == string(refCanon) {
		return // identical
	}

	// Pinpoint the first divergence for a useful failure message.
	mineM := perMethodJSON(t, mineMs)
	refM := perMethodJSON(t, refMs)
	for k, rv := range refM {
		mv, ok := mineM[k]
		if !ok {
			t.Errorf("method %s present in reference, missing in Go output", k)
			continue
		}
		if mv != rv {
			t.Errorf("method %s differs:\n  ref: %s\n  go : %s", k, rv, mv)
		}
	}
	for k := range mineM {
		if _, ok := refM[k]; !ok {
			t.Errorf("method %s produced by Go, absent from reference", k)
		}
	}
	t.Fatalf("Go output not identical to deionizer_reveal_json reference (%d vs %d bytes)", len(mineCanon), len(refCanon))
}

// TestSpotChecks documents a couple of concrete expected decodes so the test
// file reads as an executable spec, not just an opaque byte compare.
func TestSpotChecks(t *testing.T) {
	raw, err := os.ReadFile(rawFixture)
	if err != nil {
		t.Fatal(err)
	}
	methods, err := Parse(raw, Zend34)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]opline.Method{}
	for _, m := range methods {
		byName[m.Class+"::"+m.Function] = m
	}

	isCli, ok := byName["TextFormat::isCli"]
	if !ok {
		t.Fatal("TextFormat::isCli not found")
	}
	// isCli: reads self::$conf['isCLI'] and returns it, so the first opline is the
	// static-property fetch naming the property (see finding 06/12).
	if !isCli.Static || isCli.Vis != "public" {
		t.Errorf("isCli: static=%v vis=%q", isCli.Static, isCli.Vis)
	}
	if len(isCli.Oplines) == 0 || isCli.Oplines[0].Op != "ZEND_FETCH_STATIC_PROP_R" {
		t.Errorf("isCli op0 = %+v", isCli.Oplines[0])
	}
	if got := isCli.Oplines[0].Op1; got.T != "CONST" || got.Val != "conf" {
		t.Errorf("isCli op0.op1 = %+v (want CONST conf)", got)
	}

	// printInfo: signature printInfo($strArr, $arr = []) — a CV param + an
	// empty-array default; exercises CV names, RECV_INIT, and array literals.
	pi, ok := byName["TextFormat::printInfo"]
	if !ok {
		t.Fatal("TextFormat::printInfo not found")
	}
	if len(pi.Params) != 2 || pi.Params[0].Name != "strArr" || pi.Params[1].Name != "arr" {
		t.Errorf("printInfo params = %+v", pi.Params)
	}
	if !pi.Params[1].HasDefault {
		t.Errorf("printInfo $arr should have a default")
	}
	if arr, ok := pi.Params[1].Default.([]interface{}); !ok || len(arr) != 0 {
		t.Errorf("printInfo $arr default = %#v (want empty []interface{})", pi.Params[1].Default)
	}
}

func TestOpcodeTable(t *testing.T) {
	cases := map[uint8]string{
		42: "ZEND_JMP", 62: "ZEND_RETURN", 63: "ZEND_RECV", 64: "ZEND_RECV_INIT",
	}
	for oc, want := range cases {
		if got := Zend34.opcodeName(oc); got != want {
			t.Errorf("opcode %d = %q, want %q", oc, got, want)
		}
	}
	if got := Zend34.opcodeName(250); got != "ZEND_UNKNOWN_250" {
		t.Errorf("unknown opcode fallback = %q", got)
	}
}
