package decompile

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"deionizer/internal/opline"
)

// Switch reconstruction driven by the SWITCH_STRING / SWITCH_LONG jump table.
//
// The loader corrupts the per-opline JMP/JMPNZ target fields (a case chain's
// JMPNZ targets are a pure function of the jump's OWN index, encoding nothing
// about the real body). The one jump signal that survives intact is the switch
// head's op2 jump TABLE: a map of
// label -> body byte-offset. Two facts hold on it and are used here:
//
//   1. Grouping — labels that share a case body share an offset (this is what a
//      jump table IS), so `case 'a': case 'b': <body>` is recovered exactly.
//   2. Monotone layout — distinct offsets increase with body source order, and
//      resolve to exact body oplines via offset/opSize relative to the head.
//
// From these the true case->body pairing is reconstructed. When the table is
// absent or fails validation we fall back to the label+body best-effort skeleton
// (emitSwitchBestEffort) so no logic is ever dropped.

// caseLabelInfo is one recovered `case` label: its rendered text and the
// canonical key used to look it up in the jump table.
type caseLabelInfo struct {
	text  string
	key   string
	keyOK bool
}

// switchGroup is one emitted case group (a body plus the labels that reach it,
// in source order). isDefault marks the `default:` arm.
type switchGroup struct {
	labels    []string
	isDefault bool
	bodyLo    int
	bodyHi    int
}

// emitSwitchTable dispatches a switch whose head is at i. It handles an explicit
// SWITCH_STRING/LONG head (with a jump table) and a bare CASE/JMPNZ chain (int
// switch the optimizer left un-tabled). Returns ok=false to let the caller fall
// back to the best-effort emitter.
func (ev *evaluator) emitSwitchTable(i, hi, d int) (lines []string, next int, ok bool) {
	head := ev.ops[i]
	var subj opline.Operand
	var table map[string]int64
	hasTable := false
	chainStart := i
	if isSwitchHead(head.Op) {
		subj = head.Op1
		table, hasTable = switchTable(head.Op2)
		chainStart = i + 1
	}
	chainSubj, labels, bodiesStart, chainOK := ev.collectCaseChain(chainStart, hi)
	if !chainOK {
		return nil, i, false
	}
	if !isSwitchHead(head.Op) {
		subj = chainSubj
	}
	if !hasTable {
		return nil, i, false // only the table pairing is reliable; bare chains fall back
	}

	groups, mergeLo, gok := ev.planFromTable(i, hi, table, labels, bodiesStart, int64(head.Ext))
	if !gok {
		return nil, i, false
	}
	lines = ev.renderSwitchGroups(d, ev.operandE(subj), groups)
	if mergeLo >= 0 {
		// Fall-through code after the switch (the cases broke to it); structure it
		// at the switch's own depth, not inside a `default:` arm.
		lines = append(lines, ev.structure(mergeLo, hi, d)...)
	}
	return lines, hi, true
}

// planFromTable turns the jump table + ordered labels into body-ordered groups.
// mergeLo (>=0) is the start of post-switch fall-through code that was reached by
// the cases' breaks and must be emitted after the switch, not as a default arm.
func (ev *evaluator) planFromTable(headPos, hi int, table map[string]int64, labels []caseLabelInfo, bodiesStart int, defaultOff int64) ([]switchGroup, int, bool) {
	offs, offToPos, ok := ev.resolveOffsetPositions(headPos, hi, table, labels, bodiesStart, defaultOff)
	if !ok {
		return nil, -1, false
	}
	groups := buildGroups(offs, offToPos, table, labels, defaultOff, hi)
	groups, mergeLo := ev.salvageTrailingDefault(groups)
	return groups, mergeLo, true
}

// resolveOffsetPositions validates that every label resolves in the table, gathers
// the distinct body offsets (including the default arm), resolves each to a
// body-start opline position, and checks those positions increase monotonically
// with the sorted offsets and begin at the body region. offs is sorted ascending.
func (ev *evaluator) resolveOffsetPositions(headPos, hi int, table map[string]int64, labels []caseLabelInfo, bodiesStart int, defaultOff int64) (offs []int64, offToPos map[int64]int, ok bool) {
	// Every label must resolve in the table.
	for _, lb := range labels {
		if !lb.keyOK {
			return nil, nil, false
		}
		if _, present := table[lb.key]; !present {
			return nil, nil, false
		}
	}
	// Distinct offsets = one per body, including the default arm.
	offSet := map[int64]bool{}
	for _, v := range table {
		offSet[v] = true
	}
	offSet[defaultOff] = true
	for o := range offSet {
		offs = append(offs, o)
	}
	sort.Slice(offs, func(a, b int) bool { return offs[a] < offs[b] })

	// Resolve each offset to a body-start opline position via offset/opSize.
	offToPos, ok = ev.resolveOffsets(ev.ops[headPos].I, headPos, hi, offs)
	if !ok {
		return nil, nil, false
	}
	// Positions must be strictly increasing with the (sorted) offsets and start at
	// the body region.
	prev := -1
	for _, o := range offs {
		p := offToPos[o]
		if p <= prev || p < bodiesStart {
			return nil, nil, false
		}
		prev = p
	}
	return offs, offToPos, true
}

// buildGroups assembles the body-ordered case groups: it collects each offset's
// label texts (in source/chain order) and cuts the body of group k as the opline
// span [pos_k, pos_{k+1}), with the last group running to hi. The group whose
// offset is defaultOff is marked isDefault (it may also carry labels — a
// `default:` that shares a case body).
func buildGroups(offs []int64, offToPos map[int64]int, table map[string]int64, labels []caseLabelInfo, defaultOff int64, hi int) []switchGroup {
	labelsByOff := map[int64][]string{}
	for _, lb := range labels {
		o := table[lb.key]
		labelsByOff[o] = append(labelsByOff[o], lb.text)
	}
	var groups []switchGroup
	for idx, o := range offs {
		lo := offToPos[o]
		bhi := hi
		if idx+1 < len(offs) {
			bhi = offToPos[offs[idx+1]]
		}
		g := switchGroup{labels: labelsByOff[o], bodyLo: lo, bodyHi: bhi}
		if o == defaultOff {
			g.isDefault = true // may also carry labels (default shares a case body)
		}
		groups = append(groups, g)
	}
	return groups
}

// salvageTrailingDefault handles a trailing label-less default group whose
// labelled cases break to a shared join: that join is post-switch code (PHP with
// no source `default:` compiles the join as the switch's default target). It
// returns the possibly-trimmed groups and mergeLo — the start of the post-switch
// tail (>=0), or -1 when there is none. Two sub-cases, told apart by whether the
// default arm carries its OWN leading statement:
//   - none (default target is purely `return <expr>` built from case-set vars):
//     the whole arm is the shared tail — emit it after the switch, drop the arm.
//   - present (a real `default: $x = …;` before a later shared exit): keep the
//     `default:` arm, bound it before that exit, and emit the exit onward after.
func (ev *evaluator) salvageTrailingDefault(groups []switchGroup) ([]switchGroup, int) {
	n := len(groups)
	if n < 2 {
		return groups, -1
	}
	last := groups[n-1]
	if !last.isDefault || len(last.labels) != 0 {
		return groups, -1
	}
	casesBreak := false
	for _, g := range groups[:n-1] {
		if ev.caseFallsThrough(g.bodyLo, g.bodyHi) {
			casesBreak = true
			break
		}
	}
	if !casesBreak {
		return groups, -1
	}
	ex := ev.firstUnconditionalExit(last.bodyLo, last.bodyHi)
	if ex > last.bodyLo && ev.hasLeadingStatement(last.bodyLo, ex) {
		groups[n-1].bodyHi = ex
		return groups, ex
	}
	return groups[:n-1], last.bodyLo
}

// hasLeadingStatement reports whether [lo,hi) contains a statement with its own
// side effect (an assignment, echo/print, unset, ++/--, or a call whose result is
// discarded) — i.e. a real `default:` body — as opposed to only building the value
// of a following `return`/exit (a shared post-switch tail).
func (ev *evaluator) hasLeadingStatement(lo, hi int) bool {
	if hi > len(ev.ops) {
		hi = len(ev.ops)
	}
	for k := lo; k < hi; k++ {
		op := ev.ops[k].Op
		switch {
		case strings.HasPrefix(op, "ZEND_ASSIGN"), op == "ZEND_ECHO", op == "ZEND_PRINT",
			strings.HasPrefix(op, "ZEND_UNSET"), op == "ZEND_PRE_INC", op == "ZEND_POST_INC",
			op == "ZEND_PRE_DEC", op == "ZEND_POST_DEC":
			return true
		case op == "ZEND_DO_FCALL", op == "ZEND_DO_FCALL_BY_NAME", op == "ZEND_DO_UCALL", op == "ZEND_DO_ICALL":
			if ev.ops[k].Res.T == "UNUSED" {
				return true
			}
		}
	}
	return false
}

// Opline stride in bytes = sizeof(zend_op), which differs by Zend major.
const (
	strideZend3  = 32 // Zend 3.x (PHP 7 / 8)
	strideZend26 = 48 // Zend 2.6 (PHP 5.x)
)

// resolveOffsets maps each byte-offset to an opline position. ionCube stores the
// table targets as byte offsets; the opline stride is strideZend3 or strideZend26
// and the base is the head or 0. We pick the (stride, base) under which every
// offset lands on a real opline in the body region, which uniquely disambiguates.
func (ev *evaluator) resolveOffsets(headI, headPos, hi int, offs []int64) (map[int64]int, bool) {
	type cand struct {
		stride int64
		base   int
	}
	for _, c := range []cand{{strideZend3, headI}, {strideZend26, headI}, {strideZend3, 0}, {strideZend26, 0}} {
		out := make(map[int64]int, len(offs))
		good := true
		seen := map[int]bool{}
		for _, o := range offs {
			if o < 0 || o%c.stride != 0 {
				good = false
				break
			}
			targetI := c.base + int(o/c.stride)
			p := ev.posOf(targetI)
			if p <= headPos || p > hi || seen[p] {
				good = false
				break
			}
			seen[p] = true
			out[o] = p
		}
		if good {
			return out, true
		}
	}
	return nil, false
}

// renderSwitchGroups emits the PHP switch from body-ordered groups.
func (ev *evaluator) renderSwitchGroups(d int, subj E, groups []switchGroup) []string {
	var lines []string
	lines = append(lines, ind(d)+"switch ("+subj.wrap(precLowest+1)+") {")
	ev.loopDepth++
	defer func() { ev.loopDepth-- }()
	for _, g := range groups {
		for _, lb := range g.labels {
			lines = append(lines, ind(d+1)+"case "+lb+":")
		}
		if g.isDefault {
			lines = append(lines, ind(d+1)+"default:")
		}
		body := ev.structure(g.bodyLo, g.bodyHi, d+2)
		// The case body's terminating `break` is an unconditional JMP that
		// structure() skips (its target is corrupted); re-materialise it as
		// `break;` when the body does not already exit via return/throw/exit.
		if ev.caseHasBreak(g.bodyLo, g.bodyHi) && !bodyExits(body) {
			body = append(body, ind(d+2)+"break;")
		}
		lines = append(lines, body...)
	}
	lines = append(lines, ind(d)+"}")
	return lines
}

// caseHasBreak reports whether the last meaningful opline of a case body region
// is an unconditional JMP (the compiled `break`). Trailing housekeeping oplines
// (FREE/NOP) and the synthetic epilogue return are ignored.
func (ev *evaluator) caseHasBreak(lo, hi int) bool {
	if hi > len(ev.ops) {
		hi = len(ev.ops)
	}
	for k := hi - 1; k >= lo; k-- {
		op := ev.ops[k]
		if isIgnorable(op.Op) || isSyntheticReturn(op) {
			continue
		}
		return op.Op == "ZEND_JMP"
	}
	return false
}

// caseFallsThrough reports whether a case body reaches the switch's merge via a
// break (a trailing unconditional JMP with no earlier return/throw/exit) rather
// than exiting on its own. A dead break-JMP after a `return` (which the compiler
// still emits) does NOT count as falling through.
func (ev *evaluator) caseFallsThrough(lo, hi int) bool {
	if hi > len(ev.ops) {
		hi = len(ev.ops)
	}
	last := -1
	for k := hi - 1; k >= lo; k-- {
		if isIgnorable(ev.ops[k].Op) || isSyntheticReturn(ev.ops[k]) {
			continue
		}
		last = k
		break
	}
	if last < 0 || ev.ops[last].Op != "ZEND_JMP" {
		return false // no trailing break => body exits or is empty
	}
	for k := lo; k < last; k++ {
		switch ev.ops[k].Op {
		case "ZEND_RETURN", "ZEND_RETURN_BY_REF", "ZEND_GENERATOR_RETURN", "ZEND_THROW", "ZEND_EXIT":
			return false // body exits before the (dead) break
		}
	}
	return true
}

// bodyExits reports whether the rendered body already ends in an unconditional
// exit statement, so no `break;` should follow.
func bodyExits(body []string) bool {
	for i := len(body) - 1; i >= 0; i-- {
		s := strings.TrimSpace(body[i])
		if s == "" || strings.HasPrefix(s, "//") {
			continue
		}
		return strings.HasPrefix(s, "return") || strings.HasPrefix(s, "throw") ||
			s == "exit;" || strings.HasPrefix(s, "exit(") || s == "break;" || s == "continue;"
	}
	return false
}

// collectCaseChain walks a run of CASE (+ following conditional jump) pairs from
// start, returning the switch subject (first CASE op1), the ordered labels, and
// the position where case bodies begin (past an optional default-dispatch JMP).
func (ev *evaluator) collectCaseChain(start, hi int) (subj opline.Operand, labels []caseLabelInfo, bodiesStart int, ok bool) {
	k := start
	first := true
	for k < hi {
		o := ev.ops[k]
		if o.Op == "ZEND_CASE" || o.Op == "ZEND_CASE_STRICT" {
			key, kok := caseKey(o.Op2)
			labels = append(labels, caseLabelInfo{text: ev.operandE(o.Op2).Text, key: key, keyOK: kok})
			if first {
				subj = o.Op1
				first = false
			}
			k++
			if k < hi && (ev.ops[k].Op == "ZEND_JMPNZ" || ev.ops[k].Op == "ZEND_JMPZ") {
				k++
			}
			continue
		}
		// PHP 7.4 SWITCH_STRING/LONG emits a hash-collision fallback chain of bare
		// `IS_EQUAL subj,label ; JMPNZ` (no ZEND_CASE) — recognise it too, taking the
		// CONST operand as the label. The jump table (op2 of the head) remains the
		// authority for label->body grouping.
		if (o.Op == "ZEND_IS_EQUAL" || o.Op == "ZEND_IS_IDENTICAL") &&
			k+1 < hi && ev.ops[k+1].Op == "ZEND_JMPNZ" {
			lbl := o.Op2
			sub := o.Op1
			if o.Op1.T == "CONST" && o.Op2.T != "CONST" { // label may sit in either slot
				lbl, sub = o.Op1, o.Op2
			}
			if key, kok := caseKey(lbl); kok {
				labels = append(labels, caseLabelInfo{text: ev.operandE(lbl).Text, key: key, keyOK: kok})
				if first {
					subj = sub
					first = false
				}
				k += 2
				continue
			}
		}
		break
	}
	if k < hi && ev.ops[k].Op == "ZEND_JMP" {
		k++ // default-dispatch JMP
	}
	return subj, labels, k, len(labels) > 0
}

// switchTable decodes a SWITCH_STRING/LONG jump-table operand (a CONST whose Val
// is a label->byte-offset map) into key->offset. ok=false when the operand is not
// a decodable table (e.g. the text bridge's OpaqueArray placeholder).
func switchTable(o opline.Operand) (map[string]int64, bool) {
	if o.T != "CONST" {
		return nil, false
	}
	m, ok := o.Val.(map[string]interface{})
	if !ok || len(m) == 0 {
		return nil, false
	}
	out := make(map[string]int64, len(m))
	for k, v := range m {
		iv, iok := toInt64(v)
		if !iok {
			return nil, false
		}
		out[k] = iv
	}
	return out, true
}

// caseKey renders a CASE label operand as its canonical jump-table key.
func caseKey(o opline.Operand) (string, bool) {
	if o.T != "CONST" {
		return "", false
	}
	switch v := o.Val.(type) {
	case string:
		return v, true
	case int64:
		return strconv.FormatInt(v, 10), true
	case int:
		return strconv.Itoa(v), true
	case float64:
		return strconv.FormatInt(int64(v), 10), true
	case json.Number:
		return v.String(), true
	}
	return "", false
}

func toInt64(v interface{}) (int64, bool) {
	switch t := v.(type) {
	case int64:
		return t, true
	case int:
		return int64(t), true
	case float64:
		return int64(t), true
	case json.Number:
		n, err := t.Int64()
		return n, err == nil
	case string:
		n, err := strconv.ParseInt(t, 10, 64)
		return n, err == nil
	}
	return 0, false
}
