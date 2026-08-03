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
	opline.NormalizeMethods(methods)
	return methods, nil
}
