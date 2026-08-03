package opline

import "encoding/json"

// NormalizeMethods rewrites the json.Number payloads a UseNumber decoder leaves on
// every recovered literal into int64/float64, in place, across a whole method
// slice: operand CONST values plus parameter, property and constant defaults. An
// integer literal then renders as `100`, not `100.0`, and keeps its exact value so
// === comparisons and array keys stay right.
//
// Both paths that decode reveal JSON into []Method — the CLI's parseMethodsJSON and
// decompile.ParseJSON — run their decoded methods through this one function, so the
// two cannot drift. It lives here in the tag-neutral schema package because the
// build-tag seam (nodecompiler) forbids the CLI from importing internal/decompile.
func NormalizeMethods(methods []Method) {
	for i := range methods {
		for j := range methods[i].Oplines {
			NormalizeOperand(&methods[i].Oplines[j].Op1)
			NormalizeOperand(&methods[i].Oplines[j].Op2)
			NormalizeOperand(&methods[i].Oplines[j].Res)
		}
		for p := range methods[i].Params {
			methods[i].Params[p].Default = NormalizeJSONValue(methods[i].Params[p].Default)
		}
		for p := range methods[i].Properties {
			methods[i].Properties[p].Default = NormalizeJSONValue(methods[i].Properties[p].Default)
		}
		for c := range methods[i].Constants {
			methods[i].Constants[c].Value = NormalizeJSONValue(methods[i].Constants[c].Value)
		}
	}
}

// NormalizeOperand normalizes one operand's CONST value in place.
func NormalizeOperand(o *Operand) { o.Val = NormalizeJSONValue(o.Val) }

// NormalizeJSONValue converts a json.Number (from a decoder using UseNumber) to an
// int64 when it is integral, else a float64, and recurses into arrays and objects
// so nested numbers — e.g. an array literal ['a' => 1] — are converted too. Any
// other value passes through unchanged.
func NormalizeJSONValue(v interface{}) interface{} {
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
			t[i] = NormalizeJSONValue(t[i])
		}
		return t
	case map[string]interface{}:
		for k := range t {
			t[k] = NormalizeJSONValue(t[k])
		}
		return t
	}
	return v
}
