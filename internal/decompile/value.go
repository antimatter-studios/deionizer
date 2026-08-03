package decompile

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"deionizer/internal/opline"
)

// E is a rendered PHP (sub)expression plus the binding precedence of its
// top-level operator, so a parent can decide whether to parenthesise it.
// Higher prec binds tighter. Atoms (vars, literals, calls) use precAtom.
type E struct {
	Text string
	Prec int
}

const (
	precLowest     = 0 // assignment / ternary
	precTernary    = 4
	precCoalesce   = 5
	precOr         = 6 // ||
	precAnd        = 7 // &&
	precBitOr      = 8
	precBitXor     = 9
	precBitAnd     = 10
	precEq         = 11 // == != === !==
	precCmp        = 12 // < <= > >=
	precShift      = 13
	precAdd        = 14 // + - .
	precMul        = 15 // * / %
	precInstanceof = 17
	precUnary      = 18 // ! ~ (cast) -x
	precPow        = 19
	precAtom       = 100
)

func atom(s string) E { return E{Text: s, Prec: precAtom} }

// wrap returns e.Text, parenthesised if its precedence is below need.
func (e E) wrap(need int) string {
	if e.Prec < need {
		return "(" + e.Text + ")"
	}
	return e.Text
}

// binary builds "a OP b" at the given precedence.
func binary(a E, op string, b E, prec int) E {
	return E{Text: a.wrap(prec) + " " + op + " " + b.wrap(prec+1), Prec: prec}
}

// binaryL builds a left-associative "a OP b" (used for . + - etc so a chain of
// same-precedence ops does not over-parenthesise the left side).
func binaryL(a E, op string, b E, prec int) E {
	return E{Text: a.wrap(prec) + " " + op + " " + b.wrap(prec+1), Prec: prec}
}

func unary(op string, a E) E {
	return E{Text: op + a.wrap(precUnary), Prec: precUnary}
}

// call renders callee(args...) as an atom.
func call(callee string, args []E) E {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = a.wrap(precLowest + 1)
	}
	return atom(callee + "(" + strings.Join(parts, ", ") + ")")
}

// phpLiteral renders a resolved CONST operand value as PHP source.
func phpLiteral(v interface{}) E {
	switch t := v.(type) {
	case nil:
		return atom("null")
	case bool:
		if t {
			return atom("true")
		}
		return atom("false")
	case string:
		return atom(phpQuote(t))
	case int:
		return atom(strconv.Itoa(t))
	case int64:
		return atom(strconv.FormatInt(t, 10))
	case uint64:
		return atom(strconv.FormatUint(t, 10))
	case float64:
		return atom(phpFloat(t))
	case jsonNumber:
		return atom(string(t))
	case OpaqueArray:
		if t.N == 0 {
			return atom("[]")
		}
		return E{Text: fmt.Sprintf("[/* %d element(s): array literal not expanded in dump */]", t.N), Prec: precAtom}
	case []interface{}:
		parts := make([]string, len(t))
		for i, el := range t {
			parts[i] = phpLiteral(el).Text
		}
		return atom("[" + strings.Join(parts, ", ") + "]")
	case map[string]interface{}:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(t))
		for _, k := range keys {
			parts = append(parts, phpQuote(k)+" => "+phpLiteral(t[k]).Text)
		}
		return atom("[" + strings.Join(parts, ", ") + "]")
	default:
		return atom(fmt.Sprintf("/* const %v */ null", v))
	}
}

// jsonNumber lets the JSON adapter preserve exact integer text if it wants.
type jsonNumber string

// OpaqueArray marks a CONST array whose elements the dump did not expand.
type OpaqueArray struct{ N int }

func phpFloat(f float64) string {
	if math.IsInf(f, 1) {
		return "INF"
	}
	if math.IsInf(f, -1) {
		return "-INF"
	}
	if math.IsNaN(f) {
		return "NAN"
	}
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eEnN") {
		s += ".0"
	}
	return s
}

// phpQuote renders a Go string as a single-quoted PHP string literal, or a
// double-quoted one when the value contains bytes that read better escaped.
func phpQuote(s string) string {
	// Prefer single quotes: only ' and \ need escaping and there are no
	// interpolation surprises.
	if isCleanSingle(s) {
		return "'" + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `'`, `\'`) + "'"
	}
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '$':
			b.WriteString(`\$`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if c < 0x20 || c >= 0x7f {
				b.WriteString(fmt.Sprintf(`\x%02x`, c))
			} else {
				b.WriteByte(c)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// charByteOf extracts a single byte (0..255) from a constant operand value that
// holds an integer character code — the shape ZEND_ADD_CHAR uses on Zend 2.6.
func charByteOf(v interface{}) (byte, bool) {
	switch t := v.(type) {
	case int64:
		if t >= 0 && t < 256 {
			return byte(t), true
		}
	case int:
		if t >= 0 && t < 256 {
			return byte(t), true
		}
	case float64:
		if t == float64(int64(t)) && t >= 0 && t < 256 {
			return byte(int64(t)), true
		}
	}
	return 0, false
}

func isCleanSingle(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c >= 0x7f {
			return false
		}
		if c == '$' { // fine in single quotes, but keep it simple/clean
			continue
		}
	}
	return true
}

// operandE turns a schema Operand into an expression, given the current
// evaluator (to resolve temp/var slots). CV -> $name, CONST -> literal,
// TMP/VAR -> the folded sub-expression (or a slot placeholder if unknown).
func (ev *evaluator) operandE(op opline.Operand) E {
	switch op.T {
	case "CV":
		return atom("$" + op.Var)
	case "CONST":
		if op.Encrypted {
			lit := phpLiteral(op.Val)
			return E{Text: "/* inferred */ " + lit.Text, Prec: lit.Prec}
		}
		return phpLiteral(op.Val)
	case "TMP":
		if e, ok := ev.tmp[slotKey("TMP", op.Num)]; ok {
			return e
		}
		if e, ok := ev.adjacentSlot("TMP", op.Num); ok {
			return e
		}
		return atom(fmt.Sprintf("$_t%d", op.Num))
	case "VAR":
		if e, ok := ev.tmp[slotKey("VAR", op.Num)]; ok {
			return e
		}
		// 5.6 dumps can report a call's result VAR one slot off from where it is
		// consumed (decode56 slot-numbering artifact); the JSON shim emits matching
		// slots. Fall back to an immediately-adjacent stored slot.
		if e, ok := ev.adjacentSlot("VAR", op.Num); ok {
			return e
		}
		return atom(fmt.Sprintf("$_v%d", op.Num))
	case "UNUSED", "JMP", "":
		return atom("")
	default:
		return atom("")
	}
}

func slotKey(t string, n int) string { return t + strconv.Itoa(n) }

// adjacentSlot handles the 5.6 off-by-one slot artifact: if slot n is unresolved
// but n-1 (then n+1) holds a folded expression that has not yet been consumed as
// this operand, use it. Conservative: only ±1.
func (ev *evaluator) adjacentSlot(t string, n int) (E, bool) {
	if e, ok := ev.tmp[slotKey(t, n-1)]; ok {
		return e, true
	}
	if e, ok := ev.tmp[slotKey(t, n+1)]; ok {
		return e, true
	}
	return E{}, false
}
