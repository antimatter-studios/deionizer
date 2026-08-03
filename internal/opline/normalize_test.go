package opline

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// oldNormalizeValue is the pre-consolidation recursive normalizer that lived,
// byte-identical, in both cmd/deionizer/commands.go and
// internal/decompile/parsejson.go. It is kept here only to prove the shared
// NormalizeJSONValue produces identical output, so a future edit that drifts one
// path from the other is caught.
func oldNormalizeValue(v interface{}) interface{} {
	switch t := v.(type) {
	case json.Number:
		if iv, err := t.Int64(); err == nil {
			return iv
		}
		if fv, err := t.Float64(); err == nil {
			return fv
		}
		return t
	case []interface{}:
		for i := range t {
			t[i] = oldNormalizeValue(t[i])
		}
		return t
	case map[string]interface{}:
		for k := range t {
			t[k] = oldNormalizeValue(t[k])
		}
		return t
	}
	return v
}

// mkNumber runs a JSON scalar through a UseNumber decoder, the exact path that
// feeds the real normalizer, so json.Number values are shaped as in production.
func mkNumber(t *testing.T, raw string) interface{} {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var v interface{}
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
	return v
}

// NormalizeJSONValue must equal the old recursive normalizer on exact-integers,
// floats, non-integral numbers, nested arrays, and objects.
func TestNormalizeJSONValueMatchesOld(t *testing.T) {
	cases := []struct {
		name string
		in   func() interface{}
	}{
		{"exact integer", func() interface{} { return mkNumber(t, "100") }},
		{"zero", func() interface{} { return mkNumber(t, "0") }},
		{"negative integer", func() interface{} { return mkNumber(t, "-7") }},
		{"float", func() interface{} { return mkNumber(t, "1.5") }},
		{"integral-looking float stays int via Int64", func() interface{} { return mkNumber(t, "3") }},
		{"too-big-for-int64 falls to float", func() interface{} { return mkNumber(t, "123456789012345678901234") }},
		{"nested array of ints", func() interface{} { return mkNumber(t, `[1, 2, [3, 4]]`) }},
		{"object with int values", func() interface{} { return mkNumber(t, `{"a": 1, "b": [2, 3]}`) }},
		{"mixed array", func() interface{} { return mkNumber(t, `["s", 1, true, null, 2.5]`) }},
		{"plain string passthrough", func() interface{} { return "hello" }},
		{"bool passthrough", func() interface{} { return true }},
		{"nil passthrough", func() interface{} { return nil }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := NormalizeJSONValue(c.in())
			want := oldNormalizeValue(c.in())
			if !reflect.DeepEqual(got, want) {
				t.Errorf("NormalizeJSONValue = %#v (%T), old = %#v (%T)", got, got, want, want)
			}
		})
	}
}

// Spot-check the concrete types too, so the "exact integer" guarantee is asserted
// directly and not only relative to the old copy.
func TestNormalizeJSONValueTypes(t *testing.T) {
	if got := NormalizeJSONValue(mkNumber(t, "100")); got != int64(100) {
		t.Errorf("integer 100 = %#v (%T), want int64(100)", got, got)
	}
	if got := NormalizeJSONValue(mkNumber(t, "1.5")); got != float64(1.5) {
		t.Errorf("float 1.5 = %#v (%T), want float64(1.5)", got, got)
	}
}

// NormalizeMethods must reach every literal-bearing field: operand CONST values on
// each opline, plus parameter / property / constant defaults.
func TestNormalizeMethods(t *testing.T) {
	methods := []Method{{
		Function: "f",
		Oplines: []Op{{
			Op1: Operand{T: "CONST", Val: mkNumber(t, "1")},
			Op2: Operand{T: "CONST", Val: mkNumber(t, `[2, 3]`)},
			Res: Operand{T: "CONST", Val: mkNumber(t, "4.5")},
		}},
		Params:     []Param{{Name: "p", HasDefault: true, Default: mkNumber(t, "5")}},
		Properties: []Property{{Name: "q", HasDefault: true, Default: mkNumber(t, "6")}},
		Constants:  []ClassConst{{Name: "R", Value: mkNumber(t, "7")}},
	}}
	NormalizeMethods(methods)

	m := methods[0]
	if m.Oplines[0].Op1.Val != int64(1) {
		t.Errorf("Op1.Val = %#v, want int64(1)", m.Oplines[0].Op1.Val)
	}
	if !reflect.DeepEqual(m.Oplines[0].Op2.Val, []interface{}{int64(2), int64(3)}) {
		t.Errorf("Op2.Val = %#v, want [int64(2) int64(3)]", m.Oplines[0].Op2.Val)
	}
	if m.Oplines[0].Res.Val != float64(4.5) {
		t.Errorf("Res.Val = %#v, want float64(4.5)", m.Oplines[0].Res.Val)
	}
	if m.Params[0].Default != int64(5) {
		t.Errorf("Params[0].Default = %#v, want int64(5)", m.Params[0].Default)
	}
	if m.Properties[0].Default != int64(6) {
		t.Errorf("Properties[0].Default = %#v, want int64(6)", m.Properties[0].Default)
	}
	if m.Constants[0].Value != int64(7) {
		t.Errorf("Constants[0].Value = %#v, want int64(7)", m.Constants[0].Value)
	}
}
