package decompile

import (
	"bufio"
	"io"
	"regexp"
	"strconv"
	"strings"

	"deionizer/internal/opline"
)

// Behavioral side-channel (09-trace-behavioral.txt style): on Zend 2.6, scalar
// and non-name string literals stay encrypted in the pool (see 09-php5x-decode.md).
// The decode56 execute_internal hook logs the REAL decrypted arguments each
// encoded body passes to a built-in. ApplyTrace matches those logged calls back
// onto a method's reconstructed call sites and fills the encrypted CONST operands
// with their true values (clearing Encrypted). Anything still unresolved keeps
// its `/* inferred */` marker.

type traceCall struct {
	fn   string
	args []traceVal
}

// ApplyTraceAll applies a parsed behavioral trace to every method it can match
// by lowercased class::function. Safe no-op for methods absent from the trace.
func ApplyTraceAll(methods []opline.Method, tr map[string][]traceCall) {
	for i := range methods {
		key := strings.ToLower(methods[i].Class + "::" + methods[i].Function)
		if calls, ok := tr[key]; ok {
			ApplyTrace(&methods[i], calls)
		}
	}
}

// traceVal is a decoded argument: a scalar (str/int) and/or array elements.
type traceVal struct {
	isStr bool
	isInt bool
	str   string
	ival  int64
	arr   []traceVal // for [..] arrays
}

// ParseTraceFile parses the behavioral trace into class::method -> ordered calls.
func ParseTraceFile(r io.Reader) map[string][]traceCall {
	out := map[string][]traceCall{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<16), 1<<22)
	reHdr := regexp.MustCompile(`^====\s*.*?:\s*(\S+)\s*====`)
	var cur string
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\n")
		if m := reHdr.FindStringSubmatch(line); m != nil {
			cur = strings.ToLower(m[1])
			continue
		}
		t := strings.TrimSpace(line)
		if cur == "" || !strings.HasPrefix(t, "IC_TRACE ") {
			continue
		}
		body := strings.TrimPrefix(t, "IC_TRACE ")
		// Skip the on/off banners and the driver's own warm machinery (any
		// Reflection* call it makes to invoke a method lands in the trace too).
		if strings.HasPrefix(body, "==") || strings.HasPrefix(body, "Reflection") {
			continue
		}
		open := strings.IndexByte(body, '(')
		if open < 0 || !strings.HasSuffix(body, ")") {
			continue
		}
		fn := strings.TrimSpace(body[:open])
		argstr := body[open+1 : len(body)-1]
		out[cur] = append(out[cur], traceCall{fn: strings.ToLower(fn), args: parseTraceArgs(argstr)})
	}
	return out
}

// parseTraceArgs splits top-level comma-separated args and decodes each.
func parseTraceArgs(s string) []traceVal {
	parts := splitTopLevel(s)
	vals := make([]traceVal, 0, len(parts))
	for _, p := range parts {
		vals = append(vals, parseTraceVal(strings.TrimSpace(p)))
	}
	return vals
}

func parseTraceVal(s string) traceVal {
	if s == "" {
		return traceVal{}
	}
	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		inner := s[1 : len(s)-1]
		var arr []traceVal
		for _, el := range splitTopLevel(inner) {
			el = strings.TrimSpace(el)
			if i := strings.Index(el, "=>"); i >= 0 { // k=>v
				el = strings.TrimSpace(el[i+2:])
			}
			arr = append(arr, parseTraceVal(el))
		}
		return traceVal{arr: arr}
	}
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return traceVal{isStr: true, str: s[1 : len(s)-1]}
	}
	if strings.HasPrefix(s, "int(") && strings.HasSuffix(s, ")") {
		if v, err := strconv.ParseInt(s[4:len(s)-1], 10, 64); err == nil {
			return traceVal{isInt: true, ival: v}
		}
	}
	return traceVal{} // object/resource/unknown
}

// splitTopLevel splits on commas not nested in [] or "".
func splitTopLevel(s string) []string {
	var out []string
	depth, inStr := 0, false
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			inStr = !inStr
		case inStr:
		case c == '[':
			depth++
		case c == ']':
			depth--
		case c == ',' && depth == 0:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// ApplyTrace fills a method's encrypted CONST operands from the behavioral trace.
// It reconstructs each call site (INIT..SEND..DO_FCALL, incl INIT_ARRAY args) and
// matches them, in order, to trace calls of the same builtin name.
func ApplyTrace(m *opline.Method, calls []traceCall) {
	if len(calls) == 0 {
		return
	}
	// arrayEls[slot] = ordered pointers to element value operands (op1) for a temp
	// built by INIT_ARRAY/ADD_ARRAY_ELEMENT.
	arrayEls := map[int][]*opline.Operand{}
	type site struct {
		fn    string
		fills []*opline.Operand // scalar fill targets, in arg order
	}
	var stack []*site
	ti := 0 // trace call cursor

	nextTrace := func(fn string) *traceCall {
		for j := ti; j < len(calls); j++ {
			if calls[j].fn == fn {
				ti = j + 1
				return &calls[j]
			}
		}
		return nil
	}

	for i := range m.Oplines {
		op := &m.Oplines[i]
		switch op.Op {
		case "ZEND_INIT_FCALL_BY_NAME", "ZEND_INIT_FCALL", "ZEND_INIT_NS_FCALL_BY_NAME":
			stack = append(stack, &site{fn: strings.ToLower(constString(op.Op2))})
		case "ZEND_INIT_METHOD_CALL", "ZEND_INIT_STATIC_METHOD_CALL", "ZEND_INIT_DYNAMIC_CALL":
			stack = append(stack, &site{fn: strings.ToLower(constString(op.Op2))})
		case "ZEND_INIT_ARRAY":
			if op.Res.T == "TMP" || op.Res.T == "VAR" {
				arrayEls[op.Res.Num] = collectArrayEl(op)
			}
		case "ZEND_ADD_ARRAY_ELEMENT":
			slot := op.Op1.Num
			if op.Res.T == "TMP" || op.Res.T == "VAR" {
				slot = op.Res.Num
			}
			arrayEls[slot] = append(arrayEls[slot], collectArrayEl(op)...)
		case "ZEND_SEND_VAL", "ZEND_SEND_VAL_EX", "ZEND_SEND_VAR", "ZEND_SEND_VAR_EX",
			"ZEND_SEND_VAR_NO_REF", "ZEND_SEND_VAR_NO_REF_EX":
			if len(stack) == 0 {
				continue
			}
			s := stack[len(stack)-1]
			if op.Op1.T == "CONST" {
				s.fills = append(s.fills, &op.Op1)
			} else if (op.Op1.T == "TMP" || op.Op1.T == "VAR") && arrayEls[op.Op1.Num] != nil {
				s.fills = append(s.fills, arrayEls[op.Op1.Num]...)
			} else {
				s.fills = append(s.fills, nil) // a variable arg: consumes one trace slot
			}
		case "ZEND_DO_FCALL", "ZEND_DO_FCALL_BY_NAME", "ZEND_DO_UCALL", "ZEND_DO_ICALL":
			if len(stack) == 0 {
				continue
			}
			s := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if tc := nextTrace(s.fn); tc != nil {
				fillSite(s.fills, tc.args)
			}
		}
	}
}

// collectArrayEl returns the value operand of an INIT_ARRAY/ADD_ARRAY_ELEMENT if
// it is a CONST (a fillable literal element); otherwise a nil placeholder.
func collectArrayEl(op *opline.Op) []*opline.Operand {
	if op.Op1.T == "CONST" {
		return []*opline.Operand{&op.Op1}
	}
	if op.Op1.T == "UNUSED" {
		return nil
	}
	return []*opline.Operand{nil}
}

// fillSite zips fill targets to (flattened) trace arg values, filling encrypted
// CONST scalars with their real observed values.
func fillSite(fills []*opline.Operand, args []traceVal) {
	flat := flattenTrace(args)
	n := len(fills)
	if len(flat) < n {
		n = len(flat)
	}
	for i := 0; i < n; i++ {
		tgt := fills[i]
		// Only fill a still-ENCRYPTED literal. A CONST the reveal already read (or the
		// in-place capture recovered) is ground truth; overwriting it from the trace
		// corrupts it when a looped call site produces more runtime trace entries than
		// there are static call sites, so nextTrace zips a later call's args onto an
		// earlier site (e.g. `implode(',', $m)` clobbered to `'.'`).
		if tgt == nil || tgt.T != "CONST" || !tgt.Encrypted {
			continue
		}
		v := flat[i]
		switch {
		case v.isStr:
			tgt.Val = v.str
			tgt.Encrypted = false
		case v.isInt:
			tgt.Val = v.ival
			tgt.Encrypted = false
		}
	}
}

// flattenTrace expands array args to their elements so a preg_replace([a,b,c], "")
// call's flattened values line up with the [a,b,c] element operands then "".
func flattenTrace(args []traceVal) []traceVal {
	var out []traceVal
	for _, a := range args {
		if a.arr != nil {
			out = append(out, a.arr...)
		} else {
			out = append(out, a)
		}
	}
	return out
}

func constString(o opline.Operand) string {
	if o.T == "CONST" {
		if s, ok := o.Val.(string); ok {
			return s
		}
	}
	return ""
}
