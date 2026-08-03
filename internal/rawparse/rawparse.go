// Package rawparse interprets the RAWD1 binary blob a minimal C dumper emits —
// raw opcodes[]/literals[]/vars[] bytes plus a per-opline keytab and a bounded
// heap-region closure — and does ALL the interpretation in Go: keytab XOR,
// opcode-name mapping, operand decode, CONST literal/CV-name resolution,
// JMP-target resolution, param extraction. Output is the shared, frozen
// internal/opline schema, byte-comparable to the reference JSON reveal.
//
// Everything version-specific lives in a ZendLayout table (layout.go) instead
// of in per-version C. stdlib only.
package rawparse

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sort"

	"deionizer/internal/opline"
)

// magic prefix of the C dumper's output.
var magic = []byte("RAWD1\n")

// maxArrayDepth bounds nested-array CONST decoding so a self-referential or
// pathologically deep HashTable can't recurse without end.
const maxArrayDepth = 6

// region is one raw memory window: original virtual address + the bytes.
type region struct {
	va   uint64
	data []byte
}

// regionSet resolves any virtual address to the bytes the dumper captured for
// it, preferring the region that offers the most bytes from va (windows may
// overlap; the widest wins — the seed blocks beat the fixed discovery windows).
type regionSet []region

func (rs regionSet) at(va uint64, n int) []byte {
	var best []byte
	bestAvail := -1
	for _, r := range rs {
		end := r.va + uint64(len(r.data))
		if va >= r.va && va < end {
			avail := int(end - va)
			if avail > bestAvail {
				bestAvail = avail
				off := int(va - r.va)
				m := n
				if off+m > len(r.data) {
					m = len(r.data) - off
				}
				best = r.data[off : off+m]
			}
		}
	}
	return best
}

// rawMethod is the decoded container record for one method (pre-interpretation).
type rawMethod struct {
	class, function, file         string
	fnFlags, numArgs              uint32
	last, lastVar, lastLiteral    uint32
	opcodesVA, literalsVA, varsVA uint64
	keytab                        []byte
	regions                       regionSet
}

// cursor is a bounds-checked little-endian reader over the blob.
type cursor struct {
	b   []byte
	pos int
	err error
}

func (c *cursor) need(n int) bool {
	if c.err != nil {
		return false
	}
	if c.pos+n > len(c.b) {
		c.err = fmt.Errorf("rawparse: truncated blob at %d (need %d, have %d)", c.pos, n, len(c.b)-c.pos)
		return false
	}
	return true
}
func (c *cursor) u8() uint8 {
	if !c.need(1) {
		return 0
	}
	v := c.b[c.pos]
	c.pos++
	return v
}
func (c *cursor) u32() uint32 {
	if !c.need(4) {
		return 0
	}
	v := binary.LittleEndian.Uint32(c.b[c.pos:])
	c.pos += 4
	return v
}
func (c *cursor) u64() uint64 {
	if !c.need(8) {
		return 0
	}
	v := binary.LittleEndian.Uint64(c.b[c.pos:])
	c.pos += 8
	return v
}
func (c *cursor) bytes(n uint32) []byte {
	if !c.need(int(n)) {
		return nil
	}
	v := c.b[c.pos : c.pos+int(n)]
	c.pos += int(n)
	return v
}
func (c *cursor) str() string { return string(c.bytes(c.u32())) }

// Parse decodes the RAWD1 blob and interprets every method into opline.Method
// using the given layout. The result matches decode.c's deionizer_reveal_json output
// field-for-field.
func Parse(blob []byte, L *ZendLayout) ([]opline.Method, error) {
	if len(blob) < len(magic)+8 {
		return nil, errors.New("rawparse: blob too small")
	}
	for i := range magic {
		if blob[i] != magic[i] {
			return nil, errors.New("rawparse: bad magic (not a RAWD1 blob)")
		}
	}
	c := &cursor{b: blob, pos: len(magic)}
	_ = c.u32() // zend marker (informational)
	n := c.u32()
	out := make([]opline.Method, 0, n)
	for i := uint32(0); i < n && c.err == nil; i++ {
		rm := readMethod(c)
		if c.err != nil {
			break
		}
		out = append(out, interpret(rm, L))
	}
	if c.err != nil {
		return nil, c.err
	}
	return out, nil
}

func readMethod(c *cursor) *rawMethod {
	rm := &rawMethod{}
	rm.class = c.str()
	rm.function = c.str()
	rm.file = c.str()
	rm.fnFlags = c.u32()
	rm.numArgs = c.u32()
	rm.last = c.u32()
	rm.lastVar = c.u32()
	rm.lastLiteral = c.u32()
	rm.opcodesVA = c.u64()
	rm.literalsVA = c.u64()
	rm.varsVA = c.u64()
	hasKt := c.u8()
	ktLen := c.u32()
	kt := c.bytes(ktLen)
	if hasKt != 0 {
		rm.keytab = kt
	}
	rc := c.u32()
	rm.regions = make(regionSet, 0, rc)
	for j := uint32(0); j < rc && c.err == nil; j++ {
		va := c.u64()
		ln := c.u32()
		rm.regions = append(rm.regions, region{va: va, data: c.bytes(ln)})
	}
	return rm
}

// --- interpretation (the logic moved out of C) ---

func (rm *rawMethod) key(i uint32) uint8 {
	if rm.keytab != nil && int(i) < len(rm.keytab) {
		return rm.keytab[i]
	}
	return 0
}

// opAt returns the raw OpSize bytes of opline i.
func (rm *rawMethod) opAt(i uint32, L *ZendLayout) []byte {
	return rm.regions.at(rm.opcodesVA+uint64(i)*uint64(L.OpSize), L.OpSize)
}

// realOpcode = stored byte XOR keytab[i].
func (rm *rawMethod) realOpcode(op []byte, i uint32, L *ZendLayout) uint8 {
	return op[L.OpOpcode] ^ rm.key(i)
}

func (L *ZendLayout) opcodeName(oc uint8) string {
	if n, ok := L.OpcodeNames[oc]; ok {
		return n
	}
	return fmt.Sprintf("ZEND_UNKNOWN_%d", oc)
}

// slot converts a znode var byte-offset to a compiled-variable/temporary slot.
func (L *ZendLayout) slot(varOff uint32) int {
	return int(varOff/uint32(L.ZvalSize) - L.FrameSlot)
}

func interpret(rm *rawMethod, L *ZendLayout) opline.Method {
	isMethod := rm.class != ""
	m := opline.Method{
		Class:    rm.class,
		Function: rm.function,
		File:     rm.file,
		NumVars:  int(rm.lastVar),
		Static:   isMethod && rm.fnFlags&L.AccStatic != 0,
		Params:   rm.params(L),
		Oplines:  rm.oplines(L),
	}
	if isMethod {
		switch {
		case rm.fnFlags&L.AccPrivate != 0:
			m.Vis = "private"
		case rm.fnFlags&L.AccProtected != 0:
			m.Vis = "protected"
		default:
			m.Vis = "public"
		}
		if rm.fnFlags&L.AccAbstract != 0 {
			m.Abstract = true
		}
	}
	if m.Params == nil {
		m.Params = []opline.Param{}
	}
	if m.Oplines == nil {
		m.Oplines = []opline.Op{}
	}
	return m
}

func (rm *rawMethod) oplines(L *ZendLayout) []opline.Op {
	ops := make([]opline.Op, 0, rm.last)
	for i := uint32(0); i < rm.last; i++ {
		op := rm.opAt(i, L)
		if len(op) < L.OpSize {
			break
		}
		oc := rm.realOpcode(op, i, L)
		lineno := binary.LittleEndian.Uint32(op[L.OpLineno:]) & L.LinenoMask
		o := opline.Op{
			I:    int(i),
			Line: int(lineno),
			Op:   L.opcodeName(oc),
			Ext:  uint64(binary.LittleEndian.Uint32(op[L.OpExtended:])),
		}
		opVA := rm.opcodesVA + uint64(i)*uint64(L.OpSize)
		op1Jmp := oc == L.OpJMP
		op2Jmp := oc == L.OpJMPZ || oc == L.OpJMPNZ || oc == L.OpJMPZEX || oc == L.OpJMPNZEX || oc == L.OpJMPZNZ
		o.Op1 = rm.operandOrJmp(op, opVA, L.OpOp1, op[L.OpOp1Type], op1Jmp, L)
		o.Op2 = rm.operandOrJmp(op, opVA, L.OpOp2, op[L.OpOp2Type], op2Jmp, L)
		o.Res = rm.operand(op, opVA, L.OpResult, op[L.OpResultType], L)
		ops = append(ops, o)
	}
	return ops
}

// operandOrJmp resolves a JMP target index for jump operands, else a normal operand.
func (rm *rawMethod) operandOrJmp(op []byte, opVA uint64, foff int, typ uint8, isJmp bool, L *ZendLayout) opline.Operand {
	if isJmp {
		if idx, ok := rm.jmpIndex(op, opVA, foff, L); ok {
			return opline.Operand{T: "JMP", Jmp: idx}
		}
	}
	return rm.operand(op, opVA, foff, typ, L)
}

func (rm *rawMethod) jmpIndex(op []byte, opVA uint64, foff int, L *ZendLayout) (int, bool) {
	joff := int32(binary.LittleEndian.Uint32(op[foff:]))
	target := int64(opVA) + int64(joff)
	base := int64(rm.opcodesVA)
	end := base + int64(rm.last)*int64(L.OpSize)
	if target >= base && target < end {
		return int((target - base) / int64(L.OpSize)), true
	}
	return 0, false
}

func (rm *rawMethod) operand(op []byte, opVA uint64, foff int, typ uint8, L *ZendLayout) opline.Operand {
	switch typ {
	case L.TUnused:
		return opline.Operand{T: "UNUSED"}
	case L.TConst:
		coff := int32(binary.LittleEndian.Uint32(op[foff:]))
		zaddr := uint64(int64(opVA) + int64(coff))
		zb := rm.regions.at(zaddr, L.ZvalSize)
		if len(zb) < L.ZvalSize {
			return opline.Operand{T: "CONST", Val: nil}
		}
		return opline.Operand{T: "CONST", Val: rm.zval(zb, L, 0)}
	case L.TTmp:
		return opline.Operand{T: "TMP", Num: L.slot(binary.LittleEndian.Uint32(op[foff:]))}
	case L.TVar:
		return opline.Operand{T: "VAR", Num: L.slot(binary.LittleEndian.Uint32(op[foff:]))}
	case L.TCV:
		return opline.Operand{T: "CV", Var: rm.cvName(binary.LittleEndian.Uint32(op[foff:]), L)}
	default:
		return opline.Operand{T: "UNUSED"}
	}
}

// cvName resolves a compiled-variable name via vars[slot].
func (rm *rawMethod) cvName(varOff uint32, L *ZendLayout) string {
	slot := L.slot(varOff)
	if slot < 0 {
		return ""
	}
	pb := rm.regions.at(rm.varsVA+uint64(slot)*uint64(L.PtrSize), L.PtrSize)
	if len(pb) < L.PtrSize {
		return ""
	}
	return rm.zendString(binary.LittleEndian.Uint64(pb), L)
}

// zendString reads a zend_string at va and returns its value.
func (rm *rawMethod) zendString(va uint64, L *ZendLayout) string {
	hdr := rm.regions.at(va, L.StrValOff)
	if len(hdr) < L.StrValOff {
		return ""
	}
	ln := int(binary.LittleEndian.Uint64(hdr[L.StrLenOff:]))
	if ln == 0 {
		return ""
	}
	val := rm.regions.at(va+uint64(L.StrValOff), ln)
	if len(val) < ln {
		return ""
	}
	return string(val)
}

// zval decodes a CONST zval into a Go value matching ic_json_zval_74.
func (rm *rawMethod) zval(zb []byte, L *ZendLayout, depth int) interface{} {
	switch zb[L.ZvalTypeOff] {
	case L.ZNull:
		return nil
	case L.ZFalse:
		return false
	case L.ZTrue:
		return true
	case L.ZLong:
		return int64(binary.LittleEndian.Uint64(zb))
	case L.ZDouble:
		d := math.Float64frombits(binary.LittleEndian.Uint64(zb))
		if math.IsInf(d, 0) || math.IsNaN(d) {
			return nil // C emits null for non-finite
		}
		return d
	case L.ZString:
		return rm.zendString(binary.LittleEndian.Uint64(zb), L)
	case L.ZArray:
		if depth > maxArrayDepth {
			return nil
		}
		return rm.array(binary.LittleEndian.Uint64(zb), L, depth+1)
	default:
		return nil // object / resource / AST -> null
	}
}

type kv struct {
	strKey string
	numKey uint64
	hasStr bool
	val    interface{}
}

// array decodes a HashTable, returning []interface{} for a packed list or
// map[string]interface{} otherwise — matching ic_json_array_74 list detection.
func (rm *rawMethod) array(ht uint64, L *ZendLayout, depth int) interface{} {
	hdr := rm.regions.at(ht, L.HTNumUsedOff+4)
	if len(hdr) < L.HTNumUsedOff+4 {
		return []interface{}{}
	}
	arData := binary.LittleEndian.Uint64(hdr[L.HTArDataOff:])
	nUsed := binary.LittleEndian.Uint32(hdr[L.HTNumUsedOff:])
	items := make([]kv, 0, nUsed)
	for i := uint32(0); i < nUsed; i++ {
		b := rm.regions.at(arData+uint64(i)*uint64(L.BucketSize), L.BucketSize)
		if len(b) < L.BucketSize {
			break
		}
		if b[L.ZvalTypeOff] == 0 { // IS_UNDEF hole
			continue
		}
		e := kv{val: rm.zval(b[:L.ZvalSize], L, depth)}
		keyPtr := binary.LittleEndian.Uint64(b[L.BucketKeyOff:])
		if keyPtr != 0 {
			e.strKey = rm.zendString(keyPtr, L)
			e.hasStr = true
		} else {
			e.numKey = binary.LittleEndian.Uint64(b[L.BucketHOff:])
		}
		items = append(items, e)
	}
	isList := true
	for i, e := range items {
		if e.hasStr || e.numKey != uint64(i) {
			isList = false
			break
		}
	}
	if isList {
		list := make([]interface{}, len(items))
		for i, e := range items {
			list[i] = e.val
		}
		return list
	}
	obj := make(map[string]interface{}, len(items))
	for _, e := range items {
		if e.hasStr {
			obj[e.strKey] = e.val
		} else {
			obj[fmt.Sprintf("%d", e.numKey)] = e.val
		}
	}
	return obj
}

// params extracts declared parameters from the leading RECV* oplines.
func (rm *rawMethod) params(L *ZendLayout) []opline.Param {
	var ps []opline.Param
	for i := uint32(0); i < rm.last; i++ {
		op := rm.opAt(i, L)
		if len(op) < L.OpSize {
			break
		}
		oc := rm.realOpcode(op, i, L)
		if oc != L.OpRECV && oc != L.OpRECVInit && oc != L.OpRECVVariadic {
			continue
		}
		p := opline.Param{HasDefault: oc == L.OpRECVInit}
		if op[L.OpResultType] == L.TCV {
			p.Name = rm.cvName(binary.LittleEndian.Uint32(op[L.OpResult:]), L)
		}
		if oc == L.OpRECVInit && op[L.OpOp2Type] == L.TConst {
			opVA := rm.opcodesVA + uint64(i)*uint64(L.OpSize)
			coff := int32(binary.LittleEndian.Uint32(op[L.OpOp2:]))
			zb := rm.regions.at(uint64(int64(opVA)+int64(coff)), L.ZvalSize)
			if len(zb) >= L.ZvalSize {
				p.Default = rm.zval(zb, L, 0)
			}
		}
		if oc == L.OpRECVVariadic {
			p.Variadic = true
		}
		ps = append(ps, p)
	}
	return ps
}

// SortMethods orders methods by class then function (stable canonical order for
// equivalence comparison — the C iteration order is hash-table dependent).
func SortMethods(ms []opline.Method) {
	sort.SliceStable(ms, func(i, j int) bool {
		if ms[i].Class != ms[j].Class {
			return ms[i].Class < ms[j].Class
		}
		return ms[i].Function < ms[j].Function
	})
}
