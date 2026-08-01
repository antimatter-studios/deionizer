package decompile

import (
	"encoding/json"
	"io"

	"deionizer/internal/opline"
)

// ParseJSON reads the opline JSON emitted by the C reveal shim (decode.c /
// decode56.c) into []opline.Method — the production input path for the
// decompiler. The shim emits an array of Method objects matching the frozen
// deionizer/internal/opline schema.
//
// encoding/json decodes each Operand.Val (interface{}) to the natural Go type:
// JSON string->string, number->float64, bool->bool, null->nil, array->[]interface{},
// object->map[string]interface{} — all of which phpLiteral already renders.
func ParseJSON(r io.Reader) ([]opline.Method, error) {
	var methods []opline.Method
	dec := json.NewDecoder(r)
	dec.UseNumber() // keep integer literals exact instead of float64
	if err := dec.Decode(&methods); err != nil {
		return nil, err
	}
	for i := range methods {
		for j := range methods[i].Oplines {
			normalizeOperandNumbers(&methods[i].Oplines[j].Op1)
			normalizeOperandNumbers(&methods[i].Oplines[j].Op2)
			normalizeOperandNumbers(&methods[i].Oplines[j].Res)
		}
		for p := range methods[i].Params {
			methods[i].Params[p].Default = normalizeValue(methods[i].Params[p].Default)
		}
		for p := range methods[i].Properties {
			methods[i].Properties[p].Default = normalizeValue(methods[i].Properties[p].Default)
		}
		for c := range methods[i].Constants {
			methods[i].Constants[c].Value = normalizeValue(methods[i].Constants[c].Value)
		}
	}
	return methods, nil
}

// normalizeOperandNumbers converts json.Number payloads to int64/float64 so the
// literal renderer prints `5` rather than a quoted number, matching text-adapter
// behavior. It recurses into array/object CONST values so nested ints (e.g. an
// array literal `['a' => 1]`) are normalised too, not just top-level scalars.
func normalizeOperandNumbers(o *opline.Operand) {
	o.Val = normalizeValue(o.Val)
}

func normalizeValue(v interface{}) interface{} {
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
			t[i] = normalizeValue(t[i])
		}
		return t
	case map[string]interface{}:
		for k := range t {
			t[k] = normalizeValue(t[k])
		}
		return t
	}
	return v
}
