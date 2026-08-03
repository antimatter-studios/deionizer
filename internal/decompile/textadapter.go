package decompile

import (
	"bufio"
	"io"
	"regexp"
	"strconv"
	"strings"

	"deionizer/internal/opline"
)

// ParseTextDump reads a text opline listing (the reveal shim's text-mode output)
// and adapts it to the frozen []opline.Method JSON schema. This is the bridge
// front-end used for validation when the shim emits text rather than opline JSON
// directly; the decompiler itself only ever sees []opline.Method.
//
// zend56 selects Zend 2.6 quirks (opcode names lack the ZEND_ prefix; string
// operands have no string(N) length prefix; scalar CONSTs are encrypted garbage).
func ParseTextDump(r io.Reader, zend56 bool) ([]opline.Method, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<24)

	var methods []opline.Method
	var cur *opline.Method
	flush := func() {
		if cur != nil && len(cur.Oplines) > 0 {
			finalizeMethod(cur, zend56)
			methods = append(methods, *cur)
		}
		cur = nil
	}
	for sc.Scan() {
		line := sc.Text()
		if h := parseHeader(line); h != nil {
			flush()
			cur = h
			continue
		}
		if cur == nil {
			continue
		}
		if op, ok := parseOpline(line, zend56); ok {
			cur.Oplines = append(cur.Oplines, op)
		}
	}
	flush()
	return methods, sc.Err()
}

var reHeader = regexp.MustCompile(`^########## (.*?)\s+\(file=(.*?)(?:\s+num_args=(\d+))?\)\s*$`)

func parseHeader(line string) *opline.Method {
	m := reHeader.FindStringSubmatch(line)
	if m == nil {
		return nil
	}
	name := strings.TrimSpace(m[1])
	class, fn := "", name
	if i := strings.Index(name, "::"); i >= 0 {
		class = name[:i]
		fn = name[i+2:]
	}
	meth := &opline.Method{Class: class, Function: fn, File: m[2], Vis: "public"}
	if m[3] != "" {
		n, _ := strconv.Atoi(m[3])
		meth.NumVars = n // provisional; refined from RECV count in finalize
	}
	return meth
}

// bracket at end of an opline line: [k=xx oc=NNN(!)? ext=NNN( ho=...)? ( ->Lnn)?]
var reBracket = regexp.MustCompile(`\[k=[0-9a-fA-F]{2} oc=\s*\d+!? ext=(\d+).*?(?:->L(\d+))?\]\s*$`)
var reOpLineStart = regexp.MustCompile(`^\s*(\d+)\s+([A-Z][A-Z0-9_]+)\s`)

func parseOpline(line string, zend56 bool) (opline.Op, bool) {
	var op opline.Op
	start := reOpLineStart.FindStringSubmatch(line)
	if start == nil {
		return op, false
	}
	idx, _ := strconv.Atoi(start[1])
	op.I = idx
	name := start[2]
	if zend56 && !strings.HasPrefix(name, "ZEND_") {
		name = "ZEND_" + name
	}
	op.Op = name

	// Split off the trailing [k=...] metadata bracket.
	bm := reBracket.FindStringSubmatchIndex(line)
	if bm == nil {
		return op, false
	}
	if v, err := strconv.ParseUint(line[bm[2]:bm[3]], 10, 64); err == nil {
		op.Ext = v
	}
	jmpTarget := -1
	if bm[4] >= 0 {
		if v, err := strconv.Atoi(line[bm[4]:bm[5]]); err == nil {
			jmpTarget = v
		}
	}
	body := line[:bm[0]]

	// Cursor past: leading spaces, index digits, spaces, opcode token.
	p := 0
	for p < len(body) && body[p] == ' ' {
		p++
	}
	for p < len(body) && body[p] >= '0' && body[p] <= '9' {
		p++
	}
	for p < len(body) && body[p] == ' ' {
		p++
	}
	// opcode token (as printed, without our ZEND_ synthesis)
	for p < len(body) && body[p] != ' ' {
		p++
	}

	o1, p := scanOperand(body, p, zend56)
	o2, p := scanOperand(body, p, zend56)
	// expect ->
	for p < len(body) && body[p] == ' ' {
		p++
	}
	if p+1 < len(body) && body[p] == '-' && body[p+1] == '>' {
		p += 2
	}
	res, _ := scanOperand(body, p, zend56)

	op.Op1 = toOperand(o1)
	op.Op2 = toOperand(o2)
	op.Res = toOperand(res)

	// Attach the jump target to the operand the CFG pass reads.
	if jmpTarget >= 0 {
		attachJump(&op, jmpTarget)
	}
	return op, true
}

func attachJump(op *opline.Op, target int) {
	switch op.Op {
	case "ZEND_JMP", "ZEND_FAST_CALL", "ZEND_GOTO":
		op.Op1 = opline.Operand{T: "JMP", Jmp: target}
	case "ZEND_JMPZ", "ZEND_JMPNZ", "ZEND_JMPZ_EX", "ZEND_JMPNZ_EX", "ZEND_JMPZNZ",
		"ZEND_JMP_SET", "ZEND_COALESCE", "ZEND_FE_RESET_R", "ZEND_FE_RESET_RW",
		"ZEND_FE_RESET", "ZEND_JMP_NULL":
		op.Op2 = opline.Operand{T: "JMP", Jmp: target}
	default:
		// leave as-is; CFG will fall back to structural detection
	}
}

// rawOperand is the tokenizer's view before mapping to the schema.
type rawOperand struct {
	text string  // literal token text
	kind string  // "unused","cv","tmp","var","str","int","float","bool","null","array","object","other"
	sval string  // string value (kind=str)
	ival int64   // int value
	fval float64 // float value
	bval bool    // bool value
	num  int     // tmp/var slot
	n    int     // array element count
}

func toOperand(r rawOperand) opline.Operand {
	switch r.kind {
	case "unused":
		return opline.Operand{T: "UNUSED"}
	case "cv":
		return opline.Operand{T: "CV", Var: r.sval}
	case "tmp":
		return opline.Operand{T: "TMP", Num: r.num}
	case "var":
		return opline.Operand{T: "VAR", Num: r.num}
	case "str":
		return opline.Operand{T: "CONST", Val: r.sval}
	case "int":
		return opline.Operand{T: "CONST", Val: r.ival}
	case "float":
		return opline.Operand{T: "CONST", Val: r.fval}
	case "bool":
		return opline.Operand{T: "CONST", Val: r.bval}
	case "null":
		return opline.Operand{T: "CONST", Val: nil}
	case "array":
		return opline.Operand{T: "CONST", Val: OpaqueArray{N: r.n}}
	default:
		return opline.Operand{T: "UNUSED"}
	}
}

// scanOperand consumes one operand token starting at/after position p. It tries
// the quoted-string forms, the parenthesised numeric literals, the typed
// array/object/resource forms, then falls back to a bare token.
func scanOperand(s string, p int, zend56 bool) (rawOperand, int) {
	for p < len(s) && s[p] == ' ' {
		p++
	}
	if p >= len(s) {
		return rawOperand{kind: "unused", text: "-"}, p
	}
	rest := s[p:]

	if op, q, ok := scanString(s, p, zend56); ok {
		return op, q
	}
	if op, q, ok := scanNumeric(s, p); ok {
		return op, q
	}
	if strings.HasPrefix(rest, "array(") {
		if close := strings.IndexByte(rest, ')'); close > 0 {
			n, _ := strconv.Atoi(rest[len("array("):close])
			return rawOperand{kind: "array", n: n}, p + close + 1
		}
	}
	if strings.HasPrefix(rest, "object") {
		q := p + len("object")
		if q < len(s) && s[q] == '(' {
			if close := strings.IndexByte(s[q:], ')'); close >= 0 {
				q += close + 1
			}
		}
		return rawOperand{kind: "object", text: "object"}, q
	}
	if strings.HasPrefix(rest, "resource") {
		q := p + len("resource")
		if q < len(s) && s[q] == '(' {
			if close := strings.IndexByte(s[q:], ')'); close >= 0 {
				q += close + 1
			}
		}
		return rawOperand{kind: "object", text: "resource"}, q
	}
	return scanBareToken(s, p)
}

// maxDumpStrPreview caps how many bytes of a string CONST the text dump prints
// before truncating with a trailing "..."; the tokenizer must consume exactly
// that many content bytes so its cursor stays aligned with the dump.
const maxDumpStrPreview = 160

// scanString parses the two quoted-string operand forms: the 7.4 length-prefixed
// `string(N) "content"...` and, under zend56, the 5.6 bare `"content"...` with no
// length prefix. ok=false when the operand at p is not a string.
func scanString(s string, p int, zend56 bool) (rawOperand, int, bool) {
	rest := s[p:]
	if strings.HasPrefix(rest, "string(") {
		// 7.4: string(N) "content"(...)?
		close := strings.IndexByte(rest, ')')
		if close > 0 {
			n, _ := strconv.Atoi(rest[len("string("):close])
			q := p + close + 1
			for q < len(s) && s[q] == ' ' {
				q++
			}
			if q < len(s) && s[q] == '"' {
				show := n
				if show > maxDumpStrPreview {
					show = maxDumpStrPreview
				}
				contentStart := q + 1
				contentEnd := contentStart + show
				if contentEnd > len(s) {
					contentEnd = len(s)
				}
				val := s[contentStart:contentEnd]
				q = contentEnd
				if q < len(s) && s[q] == '"' {
					q++
				}
				if strings.HasPrefix(s[q:], "...") {
					q += 3
				}
				return rawOperand{kind: "str", sval: val, n: n}, q, true
			}
		}
	}
	if zend56 && len(rest) > 0 && rest[0] == '"' {
		// 5.6: "content"(...)? with no length prefix; shortest-match close quote.
		end := strings.IndexByte(rest[1:], '"')
		if end >= 0 {
			val := rest[1 : 1+end]
			q := p + 1 + end + 1
			if strings.HasPrefix(s[q:], "...") {
				q += 3
			}
			return rawOperand{kind: "str", sval: val}, q, true
		}
	}
	return rawOperand{}, p, false
}

// scanNumeric parses the parenthesised numeric literal forms `int(N)` and
// `double(N)`. ok=false when the operand at p is neither.
func scanNumeric(s string, p int) (rawOperand, int, bool) {
	rest := s[p:]
	if strings.HasPrefix(rest, "int(") {
		if close := strings.IndexByte(rest, ')'); close > 0 {
			v, _ := strconv.ParseInt(rest[len("int("):close], 10, 64)
			return rawOperand{kind: "int", ival: v}, p + close + 1, true
		}
	}
	if strings.HasPrefix(rest, "double(") {
		if close := strings.IndexByte(rest, ')'); close > 0 {
			v, _ := strconv.ParseFloat(rest[len("double("):close], 64)
			return rawOperand{kind: "float", fval: v}, p + close + 1, true
		}
	}
	return rawOperand{}, p, false
}

// scanBareToken parses a whitespace-delimited operand token: the unused mark, a
// null/bool literal, a CV ($name), a temp/var slot (~T/@V/~/@), or an
// unrecognised bare token. Always matches — it is the tokenizer's fallback.
func scanBareToken(s string, p int) (rawOperand, int) {
	rest := s[p:]
	tok := rest
	if sp := strings.IndexByte(rest, ' '); sp >= 0 {
		tok = rest[:sp]
	}
	q := p + len(tok)
	switch {
	case tok == "-":
		return rawOperand{kind: "unused", text: "-"}, q
	case tok == "null" || tok == "undef":
		return rawOperand{kind: "null"}, q
	case tok == "true":
		return rawOperand{kind: "bool", bval: true}, q
	case tok == "false":
		return rawOperand{kind: "bool", bval: false}, q
	case strings.HasPrefix(tok, "$"):
		return rawOperand{kind: "cv", sval: tok[1:]}, q
	case strings.HasPrefix(tok, "~T"):
		n, _ := strconv.Atoi(tok[2:])
		return rawOperand{kind: "tmp", num: n}, q
	case strings.HasPrefix(tok, "@V"):
		n, _ := strconv.Atoi(tok[2:])
		return rawOperand{kind: "var", num: n}, q
	case strings.HasPrefix(tok, "~"):
		n, _ := strconv.Atoi(tok[1:])
		return rawOperand{kind: "tmp", num: n}, q
	case strings.HasPrefix(tok, "@"):
		n, _ := strconv.Atoi(tok[1:])
		return rawOperand{kind: "var", num: n}, q
	default:
		return rawOperand{kind: "other", text: tok}, q
	}
}

// finalizeMethod derives params from RECV/RECV_INIT, guesses static/instance,
// and flags encrypted 5.6 scalar CONSTs (names in call position stay readable).
func finalizeMethod(m *opline.Method, zend56 bool) {
	usesThis := false
	for i := range m.Oplines {
		op := &m.Oplines[i]
		switch op.Op {
		case "ZEND_RECV":
			if op.Res.T == "CV" {
				m.Params = append(m.Params, opline.Param{Name: op.Res.Var})
			}
		case "ZEND_RECV_INIT":
			p := opline.Param{Name: "", HasDefault: true}
			if op.Res.T == "CV" {
				p.Name = op.Res.Var
			}
			if op.Op2.T == "CONST" {
				p.Default = op.Op2.Val
			}
			m.Params = append(m.Params, p)
		case "ZEND_RECV_VARIADIC":
			p := opline.Param{Variadic: true}
			if op.Res.T == "CV" {
				p.Name = op.Res.Var
			}
			m.Params = append(m.Params, p)
		}
		if op.Op1.T == "CV" && op.Op1.Var == "this" {
			usesThis = true
		}
		// implicit-$this forms: an instance method-call / property fetch / obj
		// assign whose object operand is UNUSED means "$this->…".
		switch op.Op {
		case "ZEND_INIT_METHOD_CALL", "ZEND_FETCH_OBJ_R", "ZEND_FETCH_OBJ_W",
			"ZEND_FETCH_OBJ_IS", "ZEND_FETCH_OBJ_RW", "ZEND_FETCH_OBJ_FUNC_ARG",
			"ZEND_FETCH_OBJ_UNSET", "ZEND_ASSIGN_OBJ", "ZEND_ASSIGN_OBJ_OP",
			"ZEND_ISSET_ISEMPTY_PROP_OBJ", "ZEND_FETCH_THIS", "ZEND_UNSET_OBJ":
			if op.Op1.T == "UNUSED" {
				usesThis = true
			}
		}
	}
	// A method named like its class (PHP 5 style) or __construct is a constructor
	// and can never be static.
	isCtor := m.Function == "__construct" || (m.Class != "" && strings.EqualFold(m.Function, classLeaf(m.Class)))
	m.Static = m.Class != "" && !usesThis && !isCtor

	if zend56 {
		markEncrypted56(m)
	}
}

// markEncrypted56 flags CONST scalars that Zend 2.6 leaves undecrypted at rest.
// Function/method/class NAME operands (op2 of INIT_*CALL*, op1/op2 of static
// calls) are the interned lowercase copies and read cleanly, so they stay
// un-flagged; everything else scalar is marked inferred.
func markEncrypted56(m *opline.Method) {
	namePos := func(op *opline.Op) (o1, o2 bool) {
		switch op.Op {
		case "ZEND_INIT_FCALL_BY_NAME", "ZEND_INIT_FCALL", "ZEND_INIT_NS_FCALL_BY_NAME",
			"ZEND_INIT_METHOD_CALL":
			return false, true
		case "ZEND_INIT_STATIC_METHOD_CALL":
			return true, true
		case "ZEND_FETCH_CONSTANT", "ZEND_FETCH_CLASS_CONSTANT":
			return false, true
		}
		return false, false
	}
	for i := range m.Oplines {
		op := &m.Oplines[i]
		n1, n2 := namePos(op)
		if op.Op1.T == "CONST" && !n1 && isScalarConst(op.Op1) {
			op.Op1.Encrypted = true
		}
		if op.Op2.T == "CONST" && !n2 && isScalarConst(op.Op2) {
			op.Op2.Encrypted = true
		}
	}
}

func classLeaf(class string) string {
	if i := strings.LastIndex(class, "\\"); i >= 0 {
		return class[i+1:]
	}
	return class
}

func isScalarConst(o opline.Operand) bool {
	switch o.Val.(type) {
	case string, int, int64, float64:
		return true
	}
	return false
}
