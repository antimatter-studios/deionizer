// Package decompile turns a revealed opline stream (the frozen
// deionizer/internal/opline schema, emitted as JSON by the C reveal shim)
// back into readable, php -l-clean PHP source.
//
// Two passes:
//  1. Expression folding — INIT/SEND/DO_FCALL collapse to calls, FETCH_* chains
//     to l-/r-values, CONCAT/arith/logic to operator trees, ISSET_ISEMPTY to
//     isset()/empty(), QM_ASSIGN + JMPZ/JMP to ternaries, JMPZ_EX/JMPNZ_EX to
//     && / ||. Temporaries (~T / @V slots) are un-SSA'd into their expressions.
//  2. Control-flow reconstruction — forward JMPZ/JMP to if/else, SWITCH_STRING/
//     LONG + CASE to switch, FE_RESET/FE_FETCH to foreach, backward JMP to
//     while/do, CATCH to try/catch. Genuinely ambiguous wiring is emitted as
//     valid PHP annotated `// decompiler: <note>` — logic is never dropped.
//
// Works for Zend 3.x (PHP 7.4) and Zend 2.6 (PHP 5.6); the opcode set is mostly
// shared and operands arrive already keytab-resolved in the schema.
package decompile

import (
	"fmt"
	"sort"
	"strings"

	"deionizer/internal/opline"
)

// zendBindRef is the ZEND_BIND_REF bit in a ZEND_BIND_LEXICAL opline's
// extended_value: set when the closure captures the variable by reference
// (`use (&$v)`) rather than by value.
const zendBindRef = 1

// Render returns PHP source (signature + body) for one revealed method/function.
func Render(m opline.Method) (string, error) {
	return renderMethod(m, nil, isZend56([]opline.Method{m}), "")
}

// renderMethod renders one method; closures (keyed by "file\nline") lets a
// DECLARE_LAMBDA_FUNCTION inline the closure/arrow body revealed as its own
// method. ns is the file's namespace (see renderFile), threaded so class
// references resolve fully-qualified under a `namespace X;` declaration.
func renderMethod(m opline.Method, closures map[string]*opline.Method, zend56 bool, ns string) (string, error) {
	ev := newEvaluator(&m, ns)
	ev.closures = closures
	ev.zend56 = zend56
	ev.linkDiscardedProducers()
	body := ev.structure(0, len(ev.ops), 1)
	// Drop the implicit final `return;` that Zend appends to every op_array.
	for len(body) > 0 {
		last := strings.TrimSpace(body[len(body)-1])
		if last == "return;" {
			body = body[:len(body)-1]
			continue
		}
		break
	}
	var b strings.Builder
	b.WriteString(ev.signature())
	b.WriteString(" {\n")
	for _, ln := range body {
		b.WriteString(ln)
		b.WriteString("\n")
	}
	b.WriteString("}\n")
	return b.String(), nil
}

// RenderFile groups methods by class and returns a single, php -l-clean PHP file:
// <?php, then each top-level function and each class with its methods. The 5.x
// target is auto-detected from the opcode stream (isZend56).
func RenderFile(methods []opline.Method) (string, error) {
	return renderFile(methods, false)
}

// RenderFileZend56 is RenderFile with the Zend 2.6 (PHP 5.x) target forced on when
// the caller already knows the version (the reveal ran on the 5.6 image). Forcing
// it covers minimal 5.x reveals the opcode heuristic cannot see (see isZend56).
func RenderFileZend56(methods []opline.Method, zend56 bool) (string, error) {
	return renderFile(methods, zend56)
}

func renderFile(methods []opline.Method, forceZend56 bool) (string, error) {
	var b strings.Builder
	emitFileHeader(&b)

	keep := dedupeMethods(methods)
	closureMap, declared := buildClosureMap(methods)

	// A revealed RECV never records pass_by_reference, so recover `&$param` from the
	// call sites: an argument sent by SEND_REF proves that positional parameter of
	// the callee is by-reference.
	byref := inferByRefParams(methods)

	// Detect a Zend 2.6 (PHP 5.x) reveal so renderers avoid 7.0+/7.4+ syntax (arrow
	// fn, `??`) that would not lint on the 5.6 target. The caller may force it when
	// it already knows the version; otherwise fall back to the opcode heuristic.
	zend56 := forceZend56 || isZend56(methods)

	routed := routeMethods(methods, keep, byref, declared)

	// Recover a single file-level namespace from the fully-qualified names of the
	// DECLARED classes and top-level functions. When found, the file emits
	// `namespace X;`, every declaration renders under its bare name, and every class
	// reference is fully-qualified so declaration and references agree. With none —
	// a plain global-scope file — rendering is unchanged, byte-for-byte.
	fileNS := recoverFileNamespace(routed.classOrder, routed.funcs)
	if fileNS != "" {
		b.WriteString("namespace " + fileNS + ";\n\n")
	}

	if err := emitFunctions(&b, routed.funcs, closureMap, zend56, fileNS); err != nil {
		return "", err
	}
	if err := emitClasses(&b, routed, closureMap, zend56, fileNS); err != nil {
		return "", err
	}
	emitMains(&b, routed.mains, closureMap, zend56, fileNS)

	// End the file with exactly one trailing newline — no blank line at EOF.
	return strings.TrimRight(b.String(), "\n") + "\n", nil
}

// emitFileHeader writes the fixed `<?php` banner every decompiled file opens with.
func emitFileHeader(b *strings.Builder) {
	b.WriteString("<?php\n")
	b.WriteString("// Decompiled from revealed ionCube oplines by deionizer (internal/decompile).\n")
	b.WriteString("// Deterministic opcode->PHP lift; `/* inferred */` marks 5.x scalars recovered\n")
	b.WriteString("// via the behavioral side-channel, `// decompiler:` marks reconstruction notes.\n\n")
}

// dedupeMethods marks which method records to keep. The reveal can capture a named
// function/method twice; keep the copy with the most oplines. Distinct closures
// share the name "{closure}" and must NOT be merged.
func dedupeMethods(methods []opline.Method) []bool {
	best := map[string]int{}
	keep := make([]bool, len(methods))
	for i, m := range methods {
		if isClosureName(m.Function) {
			keep[i] = true
			continue
		}
		key := m.Class + "::" + m.Function
		if j, ok := best[key]; ok {
			if len(methods[i].Oplines) > len(methods[j].Oplines) {
				keep[j] = false
				keep[i] = true
				best[key] = i
			}
		} else {
			best[key] = i
			keep[i] = true
		}
	}
	return keep
}

// buildClosureMap pairs each revealed closure body with the DECLARE_LAMBDA that
// creates it and returns the render-time lookup map plus the set of (file,line)
// sites carrying an inlined closure (so those closures are not ALSO emitted as
// free-standing $__ic_closure_N).
//
// Closures all share the name "{closure}" and, when nested/curried (`fn($x) =>
// fn($y) => …`), even the same (file,line) — so a (file,line) key collides and the
// inner closure would overwrite the outer. Each DECLARE_LAMBDA_FUNCTION's op1
// constant is, however, a globally unique mangled key. We pair DECLARE->closure by
// a pre-order walk (each DECLARE at line L claims the next as-yet-unclaimed revealed
// closure whose body starts at L, in reveal order) and index the result by that
// unique key, so the render-time lookup is order-free. Keyless DECLAREs (Zend 4.x /
// PHP 8 leave op1 UNUSED) get a synthetic op1 stamped onto the opline, so this
// MUTATES methods.
func buildClosureMap(methods []opline.Method) (closureMap map[string]*opline.Method, declared map[string]bool) {
	byLine := map[string][]*opline.Method{}
	declared = map[string]bool{}
	closureMap = map[string]*opline.Method{}
	for i := range methods {
		m := &methods[i]
		if isClosureName(m.Function) && !isMainName(m.Function) && len(m.Oplines) > 0 {
			k := m.File + "\n" + itoa(m.Oplines[0].Line)
			byLine[k] = append(byLine[k], m)
			// (file,line) fallback for reveals whose DECLARE op1 key is encrypted
			// (Zend 2.6 / PHP 5.x), which have no same-line closure collisions.
			closureMap[flKey(m.File, m.Oplines[0].Line)] = m
		}
		for _, o := range m.Oplines {
			if o.Op == "ZEND_DECLARE_LAMBDA_FUNCTION" {
				declared[m.File+"\n"+itoa(o.Line)] = true
			}
		}
	}
	lineCursor := map[string]int{}
	synth := 0
	var assignClosures func(m *opline.Method)
	assignClosures = func(m *opline.Method) {
		for oi := range m.Oplines {
			o := &m.Oplines[oi]
			if o.Op != "ZEND_DECLARE_LAMBDA_FUNCTION" {
				continue
			}
			lk := m.File + "\n" + itoa(o.Line)
			q, idx := byLine[lk], lineCursor[lk]
			if idx >= len(q) {
				continue
			}
			lineCursor[lk]++
			// Zend 3.x (PHP 7.4) DECLARE_LAMBDA carries a globally-unique mangled op1
			// key; Zend 4.x (PHP 8) leaves op1 UNUSED. When there is no usable key,
			// synthesise a stable one from the pre-order claim and stamp it onto the op
			// so lookupClosure resolves this exact body (same-line curried arrows would
			// otherwise collide on their shared (file,line)).
			key, _ := o.Op1.Val.(string)
			if key == "" {
				synth++
				key = "__ic_lambda_" + itoa(synth)
				o.Op1 = opline.Operand{T: "CONST", Val: key}
			}
			closureMap[key] = q[idx]
			assignClosures(q[idx]) // pre-order: descend into the just-claimed closure
		}
	}
	for i := range methods {
		// {main} is not a closure but can DECLARE top-level closures, so it too
		// drives closure inlining.
		if !isClosureName(methods[i].Function) || isMainName(methods[i].Function) {
			assignClosures(&methods[i])
		}
	}
	return closureMap, declared
}

// routedMethods is renderFile's classification of the kept records into the
// buckets that each render to a different part of the file.
type routedMethods struct {
	funcs      []opline.Method
	mains      []opline.Method
	classes    map[string][]opline.Method
	classInfo  map[string]opline.Method
	classOrder []string
}

// routeMethods sorts the kept records into free functions, top-level {main} bodies,
// and per-class method/info buckets, applying by-ref recovery and re-routing
// flattened trait methods into the trait's own record. classOrder preserves
// first-seen declaration order.
func routeMethods(methods []opline.Method, keep []bool, byref map[string]map[int]bool, declared map[string]bool) routedMethods {
	var funcs []opline.Method
	var mains []opline.Method
	classes := map[string][]opline.Method{}
	classInfo := map[string]opline.Method{}
	classSeen := map[string]bool{}
	var classOrder []string
	// Zend flattens `use Trait` — a trait method surfaces as a Class::method record
	// tagged with its origin trait. Re-route each such method into the trait's own
	// record (deduped across multiple users) so it renders once as `trait T {…}` and
	// the using class renders `use T;` instead of the duplicated body.
	traitMethodSeen := map[string]bool{}
	traitTargets := map[string]bool{}
	for i, m := range methods {
		if !keep[i] {
			continue
		}
		// The file's top-level {main} op_array (kind:"main", function:"{main}"): its
		// oplines are procedural statements, emitted unwrapped at file scope (after
		// declarations). Checked BEFORE the class-info branch: a {main} record carries
		// a non-empty Kind ("main") that would otherwise route it into classInfo.
		if isMainName(m.Function) || m.Kind == "main" {
			mains = append(mains, m)
			continue
		}
		// A class-info record (kind=class/interface/trait) carries the class's
		// declared properties/constants/parent/interfaces, not a method body.
		if m.Kind != "" {
			classInfo[m.Class] = m
			if !classSeen[m.Class] {
				classSeen[m.Class] = true
				classOrder = append(classOrder, m.Class)
			}
			continue
		}
		// An inlined closure is emitted at its use site, not standalone.
		if isClosureName(m.Function) && len(m.Oplines) > 0 &&
			declared[m.File+"\n"+itoa(m.Oplines[0].Line)] {
			continue
		}
		applyByRef(&m, byref)
		if m.Class == "" {
			funcs = append(funcs, m)
			continue
		}
		// Flattened trait method: belongs to the trait's record, not the using class.
		if m.Trait != "" {
			traitTargets[m.Trait] = true
			if !classSeen[m.Trait] {
				classSeen[m.Trait] = true
				classOrder = append(classOrder, m.Trait)
			}
			key := m.Trait + "::" + strings.ToLower(m.Function)
			if !traitMethodSeen[key] {
				traitMethodSeen[key] = true
				m.Class = m.Trait
				classes[m.Trait] = append(classes[m.Trait], m)
			}
			continue
		}
		if !classSeen[m.Class] {
			classSeen[m.Class] = true
			classOrder = append(classOrder, m.Class)
		}
		classes[m.Class] = append(classes[m.Class], m)
	}
	// A trait referenced only through flattened members (its own declaration wasn't in
	// the revealed file) still needs a record so it renders with the `trait` keyword.
	for t := range traitTargets {
		if _, ok := classInfo[t]; !ok {
			classInfo[t] = opline.Method{Kind: "trait", Class: t}
		}
	}
	return routedMethods{funcs: funcs, mains: mains, classes: classes, classInfo: classInfo, classOrder: classOrder}
}

// recoverFileNamespace returns the single file-level namespace shared by all
// declared classes and top-level functions, or "" for a plain global-scope file. A
// valid unbraced-namespace PHP file declares all its symbols in exactly one
// namespace, so a mix of namespaces (or none) yields "".
func recoverFileNamespace(classOrder []string, funcs []opline.Method) string {
	nsset := map[string]bool{}
	for _, c := range classOrder {
		if ns := namespaceOf(c); ns != "" {
			nsset[ns] = true
		}
	}
	for _, m := range funcs {
		if isQualifiedIdent(m.Function) {
			if ns := namespaceOf(m.Function); ns != "" {
				nsset[ns] = true
			}
		}
	}
	if len(nsset) == 1 {
		for ns := range nsset {
			return ns
		}
	}
	return ""
}

// emitFunctions renders the free (non-class) functions. A revealed anonymous
// closure that was not inlined is emitted as a `$__ic_closure_N` assignment.
func emitFunctions(b *strings.Builder, funcs []opline.Method, closureMap map[string]*opline.Method, zend56 bool, fileNS string) error {
	anon := 0
	for _, m := range funcs {
		s, err := renderMethod(m, closureMap, zend56, fileNS)
		if err != nil {
			return err
		}
		if isClosureName(m.Function) {
			anon++
			b.WriteString(fmt.Sprintf("// decompiler: anonymous closure (revealed as %q)\n", m.Function))
			b.WriteString(fmt.Sprintf("$__ic_closure_%d = %s;\n\n", anon, strings.TrimRight(s, "\n")))
			continue
		}
		b.WriteString(s)
		b.WriteString("\n")
	}
	return nil
}

// emitClasses renders each class/interface/trait/enum in first-seen order: the
// declaration header, its `use Trait;` lines, constants/cases/properties, then the
// method bodies indented one level.
func emitClasses(b *strings.Builder, routed routedMethods, closureMap map[string]*opline.Method, zend56 bool, fileNS string) error {
	for _, cls := range routed.classOrder {
		info, hasInfo := routed.classInfo[cls]
		decl, note := classDecl(cls, fileNS)
		if note != "" {
			b.WriteString(note + "\n")
		}
		prefix, keyword, suffix := "", "class", ""
		if hasInfo {
			prefix, keyword, suffix = classModifiers(info, fileNS)
		}
		b.WriteString(prefix + keyword + " " + decl + suffix + " {\n")
		if hasInfo {
			wrote := false
			// `use Trait;` — the trait's members render in the trait's own record.
			for _, t := range info.Traits {
				if t != "" {
					b.WriteString("    use " + bareClassRef(t, fileNS) + ";\n")
					wrote = true
				}
			}
			if info.Kind == "enum" {
				// An enum case reveals as a class constant flagged enum_case; a backed
				// enum carries the scalar backing value (`case Name = 'H'`), a pure enum
				// none (`case Name;`). A non-case constant inside an enum stays a `const`.
				for _, c := range info.Constants {
					switch {
					case c.IsCase && c.Value != nil:
						b.WriteString("    case " + c.Name + " = " + phpLiteral(c.Value).Text + ";\n")
					case c.IsCase || c.Value == nil:
						// explicit case, or (a reveal without enum_case) a value-less
						// constant — an enum instance, so a pure case.
						b.WriteString("    case " + c.Name + ";\n")
					default:
						b.WriteString("    const " + c.Name + " = " + phpLiteral(c.Value).Text + ";\n")
					}
					wrote = true
				}
			} else {
				for _, c := range info.Constants {
					b.WriteString("    const " + c.Name + " = " + phpLiteral(c.Value).Text + ";\n")
					wrote = true
				}
				for _, p := range info.Properties {
					if p.Trait != "" {
						continue // flattened trait property — declared in the trait's record
					}
					b.WriteString("    " + propertyDecl(p, fileNS) + "\n")
					wrote = true
				}
			}
			if wrote {
				b.WriteString("\n")
			}
		}
		for _, m := range routed.classes[cls] {
			s, err := renderMethod(m, closureMap, zend56, fileNS)
			if err != nil {
				return err
			}
			for _, ln := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
				if ln == "" {
					b.WriteString("\n")
				} else {
					b.WriteString("    " + ln + "\n")
				}
			}
			b.WriteString("\n")
		}
		b.WriteString("}\n\n")
	}
	return nil
}

// emitMains renders the top-level ({main}) procedural statements last, so any
// function/class they reference is declared above (Zend hoists these anyway; this
// keeps the output readable and php -l-clean).
func emitMains(b *strings.Builder, mains []opline.Method, closureMap map[string]*opline.Method, zend56 bool, fileNS string) {
	for _, m := range mains {
		s := renderMainBody(m, closureMap, zend56, fileNS)
		if strings.TrimSpace(s) == "" {
			continue
		}
		b.WriteString("// decompiler: top-level ({main}) statements\n")
		b.WriteString(s)
		b.WriteString("\n\n")
	}
}

// zend56Opcodes are opcodes emitted ONLY by the Zend 2.6 (PHP 5.x) compiler; 7.x
// replaced each — the ADD_STRING/ADD_VAR/ADD_CHAR string rope became ROPE_*, and
// the dedicated compound-assign opcodes became a single ASSIGN_OP+ext. (Note
// DO_FCALL_BY_NAME survives on 7.x for by-name/dynamic calls, so it is NOT a
// marker.) Any one proves a 5.x reveal; the CLI also threads the real version.
var zend56Opcodes = map[string]bool{
	"ZEND_ADD_STRING": true, "ZEND_ADD_VAR": true, "ZEND_ADD_CHAR": true,
	"ZEND_ASSIGN_ADD": true, "ZEND_ASSIGN_SUB": true, "ZEND_ASSIGN_MUL": true,
	"ZEND_ASSIGN_DIV": true, "ZEND_ASSIGN_MOD": true, "ZEND_ASSIGN_CONCAT": true,
	"ZEND_ASSIGN_POW": true, "ZEND_ASSIGN_SL": true, "ZEND_ASSIGN_SR": true,
	"ZEND_ASSIGN_BW_OR": true, "ZEND_ASSIGN_BW_AND": true, "ZEND_ASSIGN_BW_XOR": true,
}

// isZend56 reports whether a reveal came from the Zend 2.6 (PHP 5.x) engine, by
// spotting any 5.x-only opcode across all methods. Decided file-wide (one reveal
// is a single PHP version) so version-sensitive rendering is consistent. It is a
// heuristic FALLBACK: minimal 5.x fixtures (e.g. a closure that neither
// interpolates nor compound-assigns) carry no marker, so the CLI passes the real
// version to RenderFileZend56; this stays conservative to never mis-flag 7.x.
func isZend56(methods []opline.Method) bool {
	for i := range methods {
		for _, o := range methods[i].Oplines {
			if zend56Opcodes[o.Op] {
				return true
			}
		}
	}
	return false
}

// inferByRefParams scans every call site: within an INIT…SEND…DO_FCALL group, the
// Nth argument sent by ZEND_SEND_REF marks positional parameter N of the named
// callee as by-reference (the reveal drops pass_by_reference from RECV). Keyed by
// lower-cased function name (PHP calls are case-insensitive).
func inferByRefParams(methods []opline.Method) map[string]map[int]bool {
	out := map[string]map[int]bool{}
	for i := range methods {
		ops := methods[i].Oplines
		for k := 0; k < len(ops); k++ {
			switch ops[k].Op {
			case "ZEND_INIT_FCALL", "ZEND_INIT_FCALL_BY_NAME", "ZEND_INIT_NS_FCALL_BY_NAME":
			default:
				continue
			}
			name, ok := ops[k].Op2.Val.(string)
			if !ok || name == "" {
				continue
			}
			name = strings.ToLower(name)
			arg := 0
			for j := k + 1; j < len(ops); j++ {
				op := ops[j].Op
				if op == "ZEND_SEND_REF" {
					if out[name] == nil {
						out[name] = map[int]bool{}
					}
					out[name][arg] = true
					arg++
					continue
				}
				if strings.HasPrefix(op, "ZEND_SEND_") {
					arg++
					continue
				}
				break // DO_FCALL, or a nested INIT — stop this group
			}
		}
	}
	return out
}

// applyByRef sets ByRef on m's parameters inferred to be by-reference, on a fresh
// Params copy so the shared reveal data is not mutated.
func applyByRef(m *opline.Method, byref map[string]map[int]bool) {
	refs := byref[strings.ToLower(m.Function)]
	if len(refs) == 0 {
		return
	}
	ps := make([]opline.Param, len(m.Params))
	copy(ps, m.Params)
	for n := range ps {
		if refs[n] {
			ps[n].ByRef = true
		}
	}
	m.Params = ps
}

// isClosureName reports whether a revealed function name is anonymous / pseudo
// (a closure or the file pseudo-main) rather than a real declarable identifier.
func isClosureName(fn string) bool {
	// A namespaced identifier (`App\Admin\probe`) is a REAL top-level function,
	// not a closure — it must not be mistaken for an anonymous body.
	return fn == "" || fn == "{main}" || fn == "{closure}" || (!isIdent(fn) && !isQualifiedIdent(fn))
}

// isMainName reports whether a record is the file's top-level {main} op_array —
// its oplines are procedural statements, not a function/closure body.
func isMainName(fn string) bool { return fn == "{main}" }

// renderMainBody renders a {main} op_array's oplines as top-level statements
// (depth 0, no function/closure wrapper). Closures declared at top level are
// still inlined via the closure map, exactly as inside a function body.
func renderMainBody(m opline.Method, closures map[string]*opline.Method, zend56 bool, ns string) string {
	ev := newEvaluator(&m, ns)
	ev.closures = closures
	ev.zend56 = zend56
	ev.linkDiscardedProducers()
	body := ev.structure(0, len(ev.ops), 0)
	// Drop the implicit terminal return Zend appends to the op_array — `return 1;`
	// for a file {main}, bare `return;` for a function body — so a procedural file's
	// recovered top level ends at its last real statement. Only trailing terminators
	// are dropped, so a user's explicit earlier `return <expr>;` still survives.
	for len(body) > 0 {
		switch strings.TrimSpace(body[len(body)-1]) {
		case "return;", "return 1;":
			body = body[:len(body)-1]
			continue
		}
		break
	}
	return strings.Join(body, "\n")
}

// classModifiers builds, from a class-info record, the abstract/final prefix, the
// class/interface/trait keyword, and the ` extends…implements…` suffix. ns is the
// file namespace, so parent/interface names qualify consistently (see renderFile).
func classModifiers(info opline.Method, ns string) (prefix, keyword, suffix string) {
	keyword = "class"
	enumBacking := ""
	switch info.Kind {
	case "interface":
		keyword = "interface"
	case "trait":
		keyword = "trait"
	case "enum":
		keyword = "enum"
		// Enums are implicitly final and implicitly implement UnitEnum/BackedEnum;
		// emitting either would be a syntax / redeclaration error.
		info.Final = false
		var keep []string
		for _, in := range info.Interfaces {
			switch strings.TrimPrefix(in, "\\") {
			case "UnitEnum", "BackedEnum":
			default:
				keep = append(keep, in)
			}
		}
		info.Interfaces = keep
		// A backed enum declares its scalar backing type (`enum Suit: string`),
		// inferred from the first case that carries a backing value.
		for _, c := range info.Constants {
			if c.IsCase && c.Value != nil {
				switch c.Value.(type) {
				case string:
					enumBacking = ": string"
				default:
					enumBacking = ": int"
				}
				break
			}
		}
	}
	if info.Abstract && keyword == "class" {
		prefix += "abstract "
	}
	if info.Final {
		prefix += "final "
	}
	var sb strings.Builder
	if info.Parent != "" {
		sb.WriteString(" extends " + bareClassRef(info.Parent, ns))
	}
	if len(info.Interfaces) > 0 {
		join := " implements "
		if keyword == "interface" {
			join = " extends " // an interface extends its parent interfaces
		}
		names := make([]string, 0, len(info.Interfaces))
		for _, in := range info.Interfaces {
			if in != "" {
				names = append(names, bareClassRef(in, ns))
			}
		}
		if len(names) > 0 {
			sb.WriteString(join + strings.Join(names, ", "))
		}
	}
	return prefix, keyword, enumBacking + sb.String()
}

// phpBuiltinType reports whether t is a PHP scalar/pseudo type name (not a class),
// so a type hint like `int` or `?string` is left un-namespaced.
func phpBuiltinType(t string) bool {
	switch strings.ToLower(t) {
	case "int", "float", "string", "bool", "array", "object", "callable",
		"iterable", "void", "mixed", "null", "false", "true", "self", "static",
		"parent", "never":
		return true
	}
	return false
}

// qualifyType fully-qualifies the class name inside a single named type hint when
// a file namespace is active (`?App\X` -> `?\App\X`, `Registry` -> `\Registry`);
// scalar/pseudo builtins, dynamic, already-qualified, and empty types pass
// through. With no file namespace the type is returned verbatim.
func qualifyType(t, ns string) string {
	if ns == "" || t == "" {
		return t
	}
	nullable := strings.HasPrefix(t, "?")
	core := strings.TrimPrefix(t, "?")
	if phpBuiltinType(core) || strings.HasPrefix(core, "\\") {
		return t
	}
	core = "\\" + core
	if nullable {
		return "?" + core
	}
	return core
}

// propertyDecl renders one property declaration: `visibility [static] $name [= default];`.
func propertyDecl(p opline.Property, ns string) string {
	vis := p.Vis
	if vis == "" {
		vis = "public"
	}
	s := vis
	if p.Static {
		s += " static"
	}
	// A `readonly` property (8.1+) requires a declared type and forbids a default;
	// emit it only when reflection recovered the type, else degrade to a plain
	// property so the output stays valid rather than a `readonly`-without-type fatal.
	if p.ReadOnly && p.Type != "" && !p.Static {
		return s + " readonly " + qualifyType(p.Type, ns) + " $" + p.Name + ";"
	}
	s += " $" + p.Name
	if p.HasDefault {
		s += " = " + phpLiteral(p.Default).Text
	}
	return s + ";"
}

// isQualifiedIdent reports whether name is a namespaced identifier
// (Seg\Seg\…\Seg, every segment a valid label) — a REAL namespaced class or
// function name, as distinct from a closure pseudo-name like "{closure}".
func isQualifiedIdent(name string) bool {
	name = strings.TrimPrefix(name, "\\")
	if !strings.Contains(name, "\\") {
		return false
	}
	for _, seg := range strings.Split(name, "\\") {
		if !isIdent(seg) {
			return false
		}
	}
	return true
}

// namespaceOf returns the namespace part of a fully-qualified name:
// `App\Admin\Registry` -> `App\Admin`; `Registry` -> "".
func namespaceOf(fqn string) string {
	fqn = strings.TrimPrefix(fqn, "\\")
	if i := strings.LastIndex(fqn, "\\"); i >= 0 {
		return fqn[:i]
	}
	return ""
}

// baseName returns the final segment of a fully-qualified name:
// `App\Admin\Registry` -> `Registry`; `Registry` -> `Registry`.
func baseName(fqn string) string {
	fqn = strings.TrimPrefix(fqn, "\\")
	if i := strings.LastIndex(fqn, "\\"); i >= 0 {
		return fqn[i+1:]
	}
	return fqn
}

// qualifyClass fully-qualifies a class-name reference (leading "\") for a file
// that carries a `namespace X;` declaration. The reveal stores a class
// reference as its global-rooted FQN, so a leading "\" resolves to exactly that
// class regardless of the enclosing namespace; the self/parent/static keywords,
// dynamic ($…) names, and already-qualified names pass through unchanged. Only
// called when a file namespace is active.
func qualifyClass(n string) string {
	if n == "" || strings.HasPrefix(n, "$") || strings.HasPrefix(n, "\\") {
		return n
	}
	switch n {
	case "self", "parent", "static":
		return n
	}
	return "\\" + n
}

// bareClassRef normalizes a parent/interface/trait name for an
// extends/implements/use clause. With no file namespace (ns==""), it preserves
// the historical behaviour of only dropping a leading separator. With a file
// namespace it fully-qualifies the reference (see qualifyClass) so the
// declaration and every reference name the same class.
func bareClassRef(n, ns string) string {
	if ns == "" {
		return strings.TrimPrefix(n, "\\")
	}
	return qualifyClass(n)
}

// classDecl renders the identifier for a `class X`/`interface X`/… declaration.
// With a file namespace the class is declared under its bare (last-segment)
// name — the enclosing `namespace X;` supplies the rest, so declaration and
// references agree. With no namespace the revealed name is used verbatim; a
// stray namespaced name (a reveal that mixed namespaces, inexpressible in the
// unbraced form) is flattened as a last resort so the output still lints.
func classDecl(name, ns string) (decl, note string) {
	if ns != "" {
		return baseName(name), ""
	}
	if strings.Contains(name, "\\") {
		flat := strings.ReplaceAll(name, "\\", "_")
		return flat, "// decompiler: original FQN " + name
	}
	return name, ""
}

type pendingCall struct {
	callee  string // rendered callee: "strlen", "$o->m", "Foo::bar", "new Foo"
	args    []E
	resSlot string // for NEW: slot to receive the constructed object
	isNew   bool
	byName  bool
}

type evaluator struct {
	m           *opline.Method
	ops         []opline.Op
	byIdx       map[int]int // opline index -> slice position
	tmp         map[string]E
	stack       []*pendingCall
	notes       map[int]string
	foldedUntil map[int]int               // start pos -> exclusive end pos consumed by a fold
	ropes       map[string][]E            // rope slot -> parts
	arrays      map[string][]string       // array slot -> element source
	cursor      int                       // current statement position (for OP_DATA lookahead)
	unhandled   map[string]int            // opcodes that matched no handler (coverage)
	loopDepth   int                       // >0 while inside a foreach/switch/loop body
	feSpans     []feSpan                  // foreach FE_RESET..FE_FREE spans (VAR-slot paired)
	loops       []loopSpan                // while/for spans found by line-regression
	loopByHdr   map[int]int               // loop entry-JMP position -> index into loops
	emitting    map[int]bool              // loop headers currently being emitted (recursion guard)
	nullsafe    map[string]bool           // TMP/VAR slot -> its next ->access is the nullsafe `?->` (JMP_NULL)
	closures    map[string]*opline.Method // DECLARE_LAMBDA op1 mangled key -> closure/arrow method to inline
	tryByLo     map[int]tryRegion         // try-block start position -> reconstructed try/catch/finally
	matchByLo   map[int]matchRegion       // match-chain start position -> reconstructed match expression
	zend56      bool                      // Zend 2.6 (PHP 5.x) target: no arrow-fn / `??` syntax
	ns          string                    // file namespace (non-empty => qualify class references, see renderFile)
}

// classRef renders a class-name reference (new, static call, catch, instanceof,
// class const, static property). When the file carries a namespace declaration
// (ns != ""), a namespaced name is fully-qualified so declaration and reference
// name the same class; with none, the revealed name is returned verbatim (the
// historical behaviour, byte-for-byte).
func (ev *evaluator) classRef(name string) string {
	if ev.ns == "" {
		return name
	}
	return qualifyClass(name)
}

// matchRegion is a `match` expression reconstructed from a conditional arm chain.
type matchRegion struct {
	lo         int
	arms       []matchArm
	defaultVal int // QM_ASSIGN body op for `default =>`, or -1
	resSlot    opline.Operand
	subjOnOp2  bool // the shared subject sits in each compare's op2 (else op1)
	end        int  // resume position (the consumer of resSlot)
}

type matchArm struct {
	cmp   int // compare op: its non-subject operand is the arm condition
	valOp int // QM_ASSIGN-shaped body op: its op1 is the arm's result value
}

// tryRegion is a reconstructed try/catch[/finally], recovered from ZEND_CATCH and
// the FAST_CALL/FAST_RET finally machinery (jump targets are corrupted; these
// opcodes and their ordering are not).
type tryRegion struct {
	tryLo, tryHi         int
	catches              []catchArm
	finallyLo, finallyHi int // -1 if no finally
	end                  int // resume position after the whole construct
}

type catchArm struct {
	class  string
	vari   string
	lo, hi int
}

// loopSpan is one while/for loop recovered by line-regression: a bottom-tested
// loop the Zend compiler emits as `entry-JMP; body; increment?; condition;
// JMP(N)Z back`. Targets are corrupted, so every boundary comes from the source
// `line` field (which regresses at the condition block) and opcode shape.
type loopSpan struct {
	header int    // dispatch position (while: entry-JMP; do-while: body top)
	bodyLo int    // first body opline
	bodyHi int    // exclusive end of body (= increment/condition block start)
	incLo  int    // increment block start (for-loops), == bodyHi
	incHi  int    // increment block end (== condLo)
	condLo int    // condition expression start
	latch  int    // backward JMP/JMPNZ closing the loop
	end    int    // resume position after the loop (latch+1)
	kind   string // "while" (top/for, condition first) or "dowhile" (bottom-tested)
}

// feSpan is one foreach construct, bracketed by the iterator VAR slot the
// FE_RESET produces and the FE_FREE releases — reliable because ionCube
// corrupts jump offsets but not the VAR slot numbers.
type feSpan struct {
	reset, fetch, free int
}

func newEvaluator(m *opline.Method, ns string) *evaluator {
	ev := &evaluator{
		m:           m,
		ops:         m.Oplines,
		byIdx:       map[int]int{},
		tmp:         map[string]E{},
		notes:       map[int]string{},
		foldedUntil: map[int]int{},
		ropes:       map[string][]E{},
		arrays:      map[string][]string{},
		unhandled:   map[string]int{},
		nullsafe:    map[string]bool{},
		ns:          ns, // set before the compute passes: computeTryCatch resolves
		// catch class names via className, which qualifies against ns.
	}
	for pos, op := range m.Oplines {
		ev.byIdx[op.I] = pos
	}
	ev.repairMaskedCondResults()
	ev.computeForeachSpans()
	ev.computeLoops()
	ev.computeTryCatch()
	ev.computeMatch()
	return ev
}

// computeMatch detects a `match` expression compiled as a conditional chain (the
// PHP 8 `match(true){ cond => v, … }` / non-scalar-arm form): N arms each
// `<compare against a shared subject> ; JMPNZ`, a default-dispatch JMP, then N (or
// N+1 with a default) QM_ASSIGN-shaped bodies writing one result slot, which a
// later opline consumes. Scalar-arm `match` uses the SWITCH_STRING/LONG table
// instead (handled in switchcfg). Keyed by the first arm's position.
func (ev *evaluator) computeMatch() {
	ev.matchByLo = map[int]matchRegion{}
	n := len(ev.ops)
	for s := 0; s < n; s++ {
		if r, ok := ev.parseMatch(s); ok {
			ev.matchByLo[s] = r
			s = r.end - 1
		}
	}
}

func (ev *evaluator) parseMatch(s int) (matchRegion, bool) {
	n := len(ev.ops)
	// A match arm never begins on the parameter prologue or plain housekeeping.
	switch ev.ops[s].Op {
	case "ZEND_RECV", "ZEND_RECV_INIT", "ZEND_RECV_VARIADIC", "ZEND_NOP", "ZEND_EXT_NOP",
		"ZEND_BIND_STATIC", "ZEND_BIND_LEXICAL":
		return matchRegion{}, false
	}
	// Collect arms: consecutive [ … <compare> ; JMPNZ ] groups.
	type armc struct{ cmp, jmpnz int }
	var arms []armc
	k := s
	for k < n {
		// find the next JMPNZ without crossing a statement or a non-condition op
		j := k
		for j < n && ev.ops[j].Op != "ZEND_JMPNZ" {
			if !ev.isConditionBuildOp(j) {
				j = -1
				break
			}
			j++
		}
		if j < 0 || j >= n || j == k {
			break
		}
		arms = append(arms, armc{cmp: j - 1, jmpnz: j})
		k = j + 1
	}
	if len(arms) < 2 {
		return matchRegion{}, false
	}
	// default-dispatch JMP after the last arm
	if k >= n || ev.ops[k].Op != "ZEND_JMP" {
		return matchRegion{}, false
	}
	bodiesStart := k + 1
	// A default-less `match` (common for `match($this)` over enum cases) compiles a
	// ZEND_MATCH_ERROR as the no-arm-matched target, sitting between the dispatch JMP
	// and the arm bodies; step over it so the QM_ASSIGN bodies are found.
	if bodiesStart < n && ev.ops[bodiesStart].Op == "ZEND_MATCH_ERROR" {
		bodiesStart++
	}
	// shared subject: the operand common to every arm's compare.
	subjOnOp2 := true
	first := ev.ops[arms[0].cmp]
	for _, a := range arms {
		c := ev.ops[a.cmp]
		if !sameOperand(c.Op2, first.Op2) {
			subjOnOp2 = false
			break
		}
	}
	if !subjOnOp2 {
		for _, a := range arms {
			if !sameOperand(ev.ops[a.cmp].Op1, first.Op1) {
				return matchRegion{}, false
			}
		}
	}
	// bodies: QM_ASSIGN-shaped ops writing one shared slot, in order.
	var bodies []int
	var resSlot opline.Operand
	bp := bodiesStart
	for bp < n {
		o := ev.ops[bp]
		if isQMAssignShape(o) && (len(bodies) == 0 || sameSlot(o.Res, resSlot)) {
			if len(bodies) == 0 {
				resSlot = o.Res
			}
			bodies = append(bodies, bp)
			bp++
			if bp < n && ev.ops[bp].Op == "ZEND_JMP" {
				bp++
			}
			continue
		}
		break
	}
	if len(bodies) != len(arms) && len(bodies) != len(arms)+1 {
		return matchRegion{}, false
	}
	r := matchRegion{lo: s, resSlot: resSlot, defaultVal: -1, subjOnOp2: subjOnOp2, end: bp}
	for i, a := range arms {
		r.arms = append(r.arms, matchArm{cmp: a.cmp, valOp: bodies[i]})
	}
	if len(bodies) == len(arms)+1 {
		r.defaultVal = bodies[len(bodies)-1]
	}
	return r, true
}

// sameOperand reports whether two operands denote the same value (slot, CV, or
// scalar CONST) — used to find a match chain's shared subject.
func sameOperand(a, b opline.Operand) bool {
	if a.T != b.T {
		return false
	}
	switch a.T {
	case "TMP", "VAR":
		return a.Num == b.Num
	case "CV":
		return a.Var == b.Var
	case "CONST":
		return fmt.Sprintf("%v", a.Val) == fmt.Sprintf("%v", b.Val)
	case "UNUSED":
		return true
	}
	return false
}

// emitMatch folds a reconstructed `match` chain into its result slot as a PHP
// `match (subject) { cond => val, … , default => val }` expression, so the opline
// that consumes the slot renders it. Arm conditions and body values are read after
// folding the region; the corrupted per-arm jump targets are never used.
func (ev *evaluator) emitMatch(r matchRegion, d int) {
	for k := r.lo; k < r.end; k++ {
		ev.foldPure(k)
	}
	subjOp := ev.ops[r.arms[0].cmp].Op1
	if r.subjOnOp2 {
		subjOp = ev.ops[r.arms[0].cmp].Op2
	}
	subject := ev.operandE(subjOp)
	ind1 := ind(d + 1)
	var b strings.Builder
	b.WriteString("match (" + subject.wrap(precLowest+1) + ") {\n")
	for _, a := range r.arms {
		cmp := ev.ops[a.cmp]
		// The compare's other operand is the arm label: op1 when the subject sits
		// in op2, op2 otherwise.
		condOp := cmp.Op1
		if !r.subjOnOp2 {
			condOp = cmp.Op2
		}
		cond := ev.operandE(condOp)
		val := ev.operandE(ev.ops[a.valOp].Op1)
		b.WriteString(ind1 + cond.wrap(precLowest+1) + " => " + val.wrap(precLowest+1) + ",\n")
	}
	if r.defaultVal >= 0 {
		val := ev.operandE(ev.ops[r.defaultVal].Op1)
		b.WriteString(ind1 + "default => " + val.wrap(precLowest+1) + ",\n")
	}
	b.WriteString(ind(d) + "}")
	ev.store(r.resSlot, atom(b.String()))
}

// emitMatchTable reconstructs a `match ($subject)` from a ZEND_MATCH jump table.
// op2 maps each case value to its arm's byte offset; the arm bodies follow as
// QM_ASSIGN(value)[+JMP-to-end] laid out in source order, with any default arm
// compiled last. Cases sharing an offset collapse into one comma-separated arm.
// The absolute offsets are used only to GROUP cases and ORDER the arms (both
// order-preserving) — never as jump targets, which ionCube corrupts. The match
// folds into the arms' shared result slot so the consuming RETURN/ASSIGN renders
// it. Returns (nextPos, ok); ok=false leaves the switch best-effort to handle an
// arm shape this does not recognise (e.g. non-literal arm bodies).
func (ev *evaluator) emitMatchTable(i, hi, d int) (int, bool) {
	mo := ev.ops[i]
	tbl, ok := mo.Op2.Val.(map[string]interface{})
	if !ok || len(tbl) == 0 {
		return i, false
	}
	// Collect arm bodies: consecutive QM_ASSIGN(value)[+JMP] writing one shared slot.
	// Matched by SHAPE, not opcode name, because Zend-4/8.3 reveals mislabel the arm
	// QM_ASSIGN (as FAST_CONCAT / EXT_NOP / …) while preserving its single-source shape.
	var arms []E
	var slot opline.Operand
	k := i + 1
	for k < hi && isQMAssignShape(ev.ops[k]) {
		q := ev.ops[k]
		if q.Res.T != "TMP" && q.Res.T != "VAR" {
			return i, false
		}
		if slot.T == "" {
			slot = q.Res
		} else if !sameSlot(slot, q.Res) {
			return i, false
		}
		arms = append(arms, ev.operandE(q.Op1))
		k++
		if k < hi && ev.ops[k].Op == "ZEND_JMP" {
			k++
		}
	}
	if len(arms) == 0 {
		return i, false
	}
	// Group case labels by arm offset; the distinct offsets, ascending, line up with
	// the arm bodies in op order.
	labelsByOff := map[int64][]string{}
	var offsets []int64
	for label, off := range tbl {
		n, ok := toInt64(off)
		if !ok {
			return i, false
		}
		if _, seen := labelsByOff[n]; !seen {
			offsets = append(offsets, n)
		}
		labelsByOff[n] = append(labelsByOff[n], label)
	}
	sort.Slice(offsets, func(a, b int) bool { return offsets[a] < offsets[b] })
	// The arm count is the distinct case offsets, plus one when a default arm (with
	// no case labels) is compiled last.
	if len(arms) != len(offsets) && len(arms) != len(offsets)+1 {
		return i, false
	}
	subject := ev.operandE(mo.Op1)
	ind1 := ind(d + 1)
	var b strings.Builder
	b.WriteString("match (" + subject.wrap(precLowest+1) + ") {\n")
	for n, off := range offsets {
		labels := labelsByOff[off]
		sort.Strings(labels)
		parts := make([]string, len(labels))
		for j, l := range labels {
			parts[j] = matchLabel(l)
		}
		b.WriteString(ind1 + strings.Join(parts, ", ") + " => " + arms[n].wrap(precLowest+1) + ",\n")
	}
	if len(arms) == len(offsets)+1 {
		b.WriteString(ind1 + "default => " + arms[len(arms)-1].wrap(precLowest+1) + ",\n")
	}
	b.WriteString(ind(d) + "}")
	ev.store(slot, atom(b.String()))
	return k, true
}

// matchLabel renders one match case label: a bare integer for a numeric value, else
// a single-quoted string.
func matchLabel(key string) string {
	if _, ok := toInt64(key); ok {
		return key
	}
	return phpQuote(key)
}

// computeTryCatch reconstructs try/catch[/finally] regions. Anchors: ZEND_CATCH
// marks a catch handler (op1 = class, result = the caught var); the try body is the
// code before it (its trailing JMP skips the catch); a finally is the block a
// ZEND_FAST_CALL invokes and a ZEND_FAST_RET closes. The try START is the first
// executable opline of the enclosing scope (leading initialisation inside the try
// is behaviour-neutral) — the reveal carries no try-range table.
func (ev *evaluator) computeTryCatch() {
	ev.tryByLo = map[int]tryRegion{}
	n := len(ev.ops)
	// first executable op (past the RECV/BIND parameter prologue)
	tryLo := 0
	for tryLo < n {
		op := ev.ops[tryLo].Op
		if isParamRecv(op) || op == "ZEND_BIND_STATIC" || op == "ZEND_BIND_LEXICAL" || op == "ZEND_NOP" || op == "ZEND_EXT_NOP" {
			tryLo++
			continue
		}
		break
	}
	// first CATCH
	cp := -1
	for k := tryLo; k < n; k++ {
		if ev.ops[k].Op == "ZEND_CATCH" {
			cp = k
			break
		}
	}
	if cp < 0 {
		return
	}
	tryHi := cp
	if cp-1 >= tryLo && ev.ops[cp-1].Op == "ZEND_JMP" {
		tryHi = cp - 1
	}
	// consecutive catch arms
	var catches []catchArm
	k := cp
	for k < n && ev.ops[k].Op == "ZEND_CATCH" {
		o := ev.ops[k]
		cls := ev.className(o.Op1)
		vari := ""
		if o.Res.T == "CV" {
			vari = o.Res.Var
		} else if o.Op2.T == "CV" {
			vari = o.Op2.Var
		}
		lo := k + 1
		hi := n
		for j := lo; j < n; j++ {
			oj := ev.ops[j].Op
			if oj == "ZEND_CATCH" || oj == "ZEND_FAST_CALL" {
				hi = j
				break
			}
		}
		catches = append(catches, catchArm{class: cls, vari: vari, lo: lo, hi: hi})
		k = hi
	}
	if len(catches) == 0 {
		return
	}
	catches = ev.mergeMultiCatch(catches)
	// finally via FAST_CALL … FAST_RET
	finallyLo, finallyHi, end := -1, -1, catches[len(catches)-1].hi
	if k < n && ev.ops[k].Op == "ZEND_FAST_CALL" {
		fLo := k + 1
		if fLo < n && ev.ops[fLo].Op == "ZEND_JMP" {
			fLo++ // the JMP that skips the finally block
		}
		fRet := -1
		for j := fLo; j < n; j++ {
			if ev.ops[j].Op == "ZEND_FAST_RET" {
				fRet = j
				break
			}
		}
		if fRet >= 0 {
			finallyLo, finallyHi, end = fLo, fRet, fRet+1
		}
	}
	ev.tryByLo[tryLo] = tryRegion{
		tryLo: tryLo, tryHi: tryHi, catches: catches,
		finallyLo: finallyLo, finallyHi: finallyHi, end: end,
	}
}

// mergeMultiCatch folds a `catch (A | B $e)` clause back into a single arm. Zend
// compiles one ZEND_CATCH per alternative type; every type but the last carries an
// empty body that is a single forward JMP into the SHARED body held by the final
// type's CATCH. An alternative is recognised structurally — its only op is that JMP
// landing on the next arm's body — which also tells it apart from a genuinely empty
// `catch (A $e) {}`, whose escape JMP targets the finally/end, not the next arm.
func (ev *evaluator) mergeMultiCatch(catches []catchArm) []catchArm {
	var out []catchArm
	var altClasses []string
	for i := 0; i < len(catches); i++ {
		c := catches[i]
		if i+1 < len(catches) && c.hi == c.lo+1 && c.lo < len(ev.ops) &&
			ev.ops[c.lo].Op == "ZEND_JMP" &&
			ev.posOf(ev.jumpTarget(ev.ops[c.lo])) == catches[i+1].lo {
			altClasses = append(altClasses, c.class)
			continue
		}
		if len(altClasses) > 0 {
			c.class = strings.Join(append(altClasses, c.class), " | ")
			altClasses = nil
		}
		out = append(out, c)
	}
	// A dangling alternative (its JMP target never resolved to a following arm)
	// degrades to a plain catch rather than being dropped.
	for _, cl := range altClasses {
		out = append(out, catchArm{class: cl, vari: "e"})
	}
	return out
}

// computeLoops finds bottom-tested while/for loops by line-regression: the Zend
// compiler emits `<entry-JMP@Lh>; <body lines > Lh>; <increment?+condition @Lh>;
// <JMP(N)Z back @Lh>`. The condition/latch block's source line REGRESSES below the
// body lines — the one signal that survives ionCube's target corruption. Each such
// latch is paired to its entry-JMP and its body/condition split, all from `line`.
func (ev *evaluator) computeLoops() {
	ev.loopByHdr = map[int]int{}
	n := len(ev.ops)
	for p := 1; p < n; p++ {
		if op := ev.ops[p].Op; op != "ZEND_JMPNZ" && op != "ZEND_JMP" {
			continue
		}
		if ev.insideForeach(p) {
			continue // foreach back-edges belong to emitForeach
		}
		lh := ev.ops[p].Line
		// footer run [f,p]: trailing oplines sharing the latch's (regressed) line.
		f := p
		for f-1 >= 0 && ev.ops[f-1].Line == lh {
			f--
		}
		if f == 0 || ev.ops[f-1].Line <= lh {
			continue // no line-regression: not a bottom-test latch
		}
		// body [b,f): the contiguous block whose lines exceed the latch line.
		b := f
		for b-1 >= 0 && ev.ops[b-1].Line > lh {
			b--
		}
		if b >= f {
			continue // empty body
		}
		// entry: the opline just above the body must be the loop's entry JMP at lh.
		hj := b - 1
		if hj < 0 || ev.ops[hj].Op != "ZEND_JMP" || ev.ops[hj].Line != lh {
			continue
		}
		// increment block: leading inc/dec ops in the footer whose result is
		// discarded (a `for`'s third clause) precede the condition.
		condLo := f
		for condLo < p {
			switch ev.ops[condLo].Op {
			case "ZEND_PRE_INC", "ZEND_POST_INC", "ZEND_PRE_DEC", "ZEND_POST_DEC",
				"ZEND_ASSIGN_OP", "ZEND_ASSIGN_ADD", "ZEND_ASSIGN_SUB",
				"ZEND_PRE_INC_OBJ", "ZEND_POST_INC_OBJ", "ZEND_PRE_DEC_OBJ", "ZEND_POST_DEC_OBJ":
				condLo++
				if condLo < p && ev.ops[condLo].Op == "ZEND_FREE" {
					condLo++
				}
				continue
			}
			break
		}
		if condLo >= p {
			continue // no condition expression (infinite loop) — leave to fall-through
		}
		ev.loops = append(ev.loops, loopSpan{
			header: hj, bodyLo: b, bodyHi: f, incLo: f, incHi: condLo,
			condLo: condLo, latch: p, end: p + 1, kind: "while",
		})
	}
	ev.computeDoWhileLoops()
	ev.validateLoopNesting()
	for idx, sp := range ev.loops {
		ev.loopByHdr[sp.header] = idx
	}
}

// validateLoopNesting drops the whole loop set for a method unless the spans form
// a clean forest — each [header,end) either disjoint from or fully nested inside
// another, and not straddling a foreach span. Corrupted-target methods with
// continue-heavy deep nesting can yield overlapping spans that would double-emit
// bodies; falling back to the flat rendering there is safer than duplicating code.
func (ev *evaluator) validateLoopNesting() {
	ls := ev.loops
	sort.Slice(ls, func(a, b int) bool { return ls[a].header < ls[b].header })
	for a := 0; a < len(ls); a++ {
		for b := a + 1; b < len(ls); b++ {
			x, y := ls[a], ls[b]
			// x.header <= y.header. Valid iff disjoint (y starts at/after x.end) or
			// nested (y ends at/before x.end). A straddle (y.header < x.end < y.end)
			// is invalid.
			if y.header < x.end && x.end < y.end {
				ev.loops = nil
				return
			}
		}
		for _, sp := range ev.feSpans {
			// A loop must not straddle a foreach (or vice-versa).
			if ls[a].header < sp.reset && sp.reset < ls[a].end && ls[a].end < sp.free {
				ev.loops = nil
				return
			}
		}
	}
	// Deeply nested loops (>=3) magnify the residual corrupted-if merge error inside
	// their bodies into duplicated emission; the flat fallback is safer there. This
	// only trips the rare deep-directory-walk methods, not the common single loops.
	for a := range ls {
		depth := 1
		for b := range ls {
			if ls[b].header < ls[a].header && ls[a].end <= ls[b].end {
				depth++
			}
		}
		if depth >= 3 {
			ev.loops = nil
			return
		}
	}
}

// computeDoWhileLoops detects bottom-tested `do { … } while (cond)` loops, which
// (unlike while/for) have NO entry JMP and NO line regression — the `while(cond)`
// line sits at the bottom, above the body's lines. They are recovered from a
// backward JMPNZ latch whose (here-reliable) target opens a clean, single-entry
// body: the region [target, cond) must be line-monotone with no other loop head,
// foreach reset, or switch inside, which rejects a corrupted backward target that
// would land mid-unrelated-code.
func (ev *evaluator) computeDoWhileLoops() {
	n := len(ev.ops)
	claimed := func(pos int) bool {
		for _, sp := range ev.loops {
			if pos >= sp.header && pos < sp.end {
				return true
			}
		}
		return ev.insideForeach(pos)
	}
	for latchPos := 1; latchPos < n; latchPos++ {
		if ev.ops[latchPos].Op != "ZEND_JMPNZ" || claimed(latchPos) {
			continue
		}
		headerPos := ev.posOf(ev.jumpTarget(ev.ops[latchPos]))
		if headerPos < 0 || headerPos >= latchPos || claimed(headerPos) {
			continue
		}
		// condition run: trailing ops at the latch line, ending at latchPos.
		latchLine := ev.ops[latchPos].Line
		condStart := latchPos
		for condStart-1 > headerPos && ev.ops[condStart-1].Line == latchLine {
			condStart--
		}
		if condStart <= headerPos {
			continue // no distinct body
		}
		// body [headerPos, condStart) must be a clean, single-entry, line-monotone block.
		ok := true
		last := -1
		for pos := headerPos; pos < condStart; pos++ {
			o := ev.ops[pos].Op
			if o == "ZEND_FE_RESET_R" || o == "ZEND_FE_RESET_RW" || o == "ZEND_FE_RESET" ||
				isSwitchHead(o) || (pos != latchPos && (o == "ZEND_JMPNZ" || o == "ZEND_JMPZNZ")) {
				ok = false
				break
			}
			if ln := ev.ops[pos].Line; ln < last {
				ok = false
				break
			} else {
				last = ln
			}
		}
		if !ok {
			continue
		}
		ev.loops = append(ev.loops, loopSpan{
			header: headerPos, bodyLo: headerPos, bodyHi: condStart, incLo: condStart, incHi: condStart,
			condLo: condStart, latch: latchPos, end: latchPos + 1, kind: "dowhile",
		})
	}
}

// insideForeach reports whether position p lies within a foreach's FE_FETCH..
// FE_FREE region (so its back-edge JMP is the foreach's, not a while/for latch).
func (ev *evaluator) insideForeach(p int) bool {
	for _, sp := range ev.feSpans {
		if p > sp.fetch && p <= sp.free {
			return true
		}
	}
	return false
}

// computeForeachSpans pairs every FE_RESET with its FE_FETCH and FE_FREE by the
// iterator VAR slot (the ONE foreach signal ionCube leaves intact — jump offsets
// are corrupted). Nested loops use distinct slots, so the first FE_FREE of a
// given slot after its FE_RESET is unambiguously that loop's end.
func (ev *evaluator) computeForeachSpans() {
	for pos, op := range ev.ops {
		if op.Op != "ZEND_FE_RESET_R" && op.Op != "ZEND_FE_RESET_RW" && op.Op != "ZEND_FE_RESET" {
			continue
		}
		rv := op.Res
		if rv.T != "VAR" && rv.T != "TMP" {
			continue
		}
		free := -1
		for k := pos + 1; k < len(ev.ops); k++ {
			o := ev.ops[k]
			if (o.Op == "ZEND_FE_FREE" || o.Op == "ZEND_SWITCH_FREE") && sameSlot(o.Op1, rv) {
				free = k
				break
			}
		}
		if free < 0 {
			continue
		}
		fetch := -1
		for k := pos + 1; k < free; k++ {
			if strings.HasPrefix(ev.ops[k].Op, "ZEND_FE_FETCH") && sameSlot(ev.ops[k].Op1, rv) {
				fetch = k
				break
			}
		}
		if fetch < 0 {
			continue
		}
		ev.feSpans = append(ev.feSpans, feSpan{reset: pos, fetch: fetch, free: free})
	}
}

// foreachSpanAt returns the span whose FE_RESET is at position pos.
func (ev *evaluator) foreachSpanAt(pos int) (feSpan, bool) {
	for _, sp := range ev.feSpans {
		if sp.reset == pos {
			return sp, true
		}
	}
	return feSpan{}, false
}

// clampOutOfForeach pushes a block boundary that lands strictly inside a foreach
// (between its FE_RESET and its FE_FREE) out to just past the FE_FREE, so a then/
// else block always fully contains any foreach it opens. Corrupted JMPZ targets
// otherwise cut a foreach in half, orphaning its FE_FETCH and body.
func (ev *evaluator) clampOutOfForeach(b int) int {
	for _, sp := range ev.feSpans {
		if sp.reset < b && b <= sp.free {
			b = sp.free + 1
		}
	}
	return b
}

// clampOutOfLoop pushes a block boundary that lands strictly inside a while/for
// loop out to just past its latch, so an enclosing if/else that opens a loop fully
// contains it. Without this, a corrupted then-block target ends before the latch,
// emitLoop consumes past that end, and the loop's tail is re-emitted at the outer
// level (duplicated code).
func (ev *evaluator) clampOutOfLoop(b int) int {
	for _, sp := range ev.loops {
		if sp.header < b && b < sp.end {
			b = sp.end
		}
	}
	return b
}

// enclosingLoop returns the innermost while/for/do-while span whose [header,end)
// contains pos (foreach spans are handled separately). Used to classify a lone JMP
// inside a loop body as a break/continue.
func (ev *evaluator) enclosingLoop(pos int) (loopSpan, bool) {
	best := -1
	for idx, sp := range ev.loops {
		if pos >= sp.header && pos < sp.end {
			if best < 0 || sp.header > ev.loops[best].header {
				best = idx
			}
		}
	}
	if best < 0 {
		return loopSpan{}, false
	}
	return ev.loops[best], true
}

// loopExitClass classifies the JMP at k against the INNERMOST enclosing loop
// (foreach or while/for) from span geometry — never a trusted numeric target beyond
// its landmarks. kind is "break"/"continue"/""; emit reports whether emitting that
// keyword is sound. A for-loop `continue` is a genuine loop exit (kind="continue") but
// emit=false: the loop is rendered as a while with its increment clause at the body
// foot, which a bare `continue` would skip — so it is left as-is rather than changing
// the loop's arithmetic. foreach/while continues are always sound.
func (ev *evaluator) loopExitClass(k int) (kind string, emit bool) {
	tp := ev.posOf(ev.jumpTarget(ev.ops[k]))
	if tp < 0 {
		return "", false
	}
	// Innermost foreach whose body (FE_FETCH..FE_FREE) contains k.
	feStart := -1
	var feSp feSpan
	for _, sp := range ev.feSpans {
		if k > sp.fetch && k <= sp.free && sp.fetch > feStart {
			feStart, feSp = sp.fetch, sp
		}
	}
	lp, lpOk := ev.enclosingLoop(k)
	if feStart >= 0 && (!lpOk || feStart >= lp.header) {
		switch {
		case tp <= feSp.fetch: // back to the FE_FETCH: next iteration
			return "continue", true
		case tp >= feSp.free: // out past the FE_FREE: leave the loop
			return "break", true
		}
		return "", false
	}
	if lpOk {
		switch {
		case tp >= lp.end:
			return "break", true
		case (tp >= lp.condLo && tp <= lp.latch) || tp == lp.header:
			return "continue", lp.incHi == lp.incLo
		}
	}
	return "", false
}

// jmpIsLoopExit reports the JMP at k is a break/continue of the enclosing loop by
// geometry, so the if/else structurer must not claim it as a skip-else (the misread
// that folds a loop into a bogus elseif or drops a guarded continue).
func (ev *evaluator) jmpIsLoopExit(k int) bool {
	kind, _ := ev.loopExitClass(k)
	return kind != ""
}

// loopExit returns the keyword to emit for the lone loop-exit JMP at i, or "" to keep
// skipping it (an unsound for-continue, or a JMP that is not a clean loop exit).
func (ev *evaluator) loopExit(i int) string {
	kind, emit := ev.loopExitClass(i)
	if kind == "" || !emit {
		return ""
	}
	return kind + ";"
}

// condLoopExit renders a BACKWARD conditional jump (JMPZ/JMPNZ) that lands on the
// enclosing loop's continue landmark as a guarded `if (cond) { continue; }`. This is
// the shape of `if (!x) continue;` inside a foreach — compiled to `JMPZ x -> FE_FETCH`
// — which the if/else structurer cannot place (its target is backward) and would emit
// as `// decompiler: unstructured ZEND_JMPZ`. Only the BACKWARD-continue case is taken:
// a normal if jumps FORWARD to skip its body, so a backward jump to the continue point
// is unambiguous; the forward/break case is left to the if-chain (which renders it
// correctly as a trailing if). Returns ok=false otherwise.
func (ev *evaluator) condLoopExit(i, d int) (string, bool) {
	op := ev.ops[i]
	if op.Op != "ZEND_JMPZ" && op.Op != "ZEND_JMPNZ" {
		return "", false
	}
	tp := ev.posOf(ev.jumpTarget(op))
	if tp < 0 || tp > i {
		return "", false // only backward conditional jumps
	}
	kind, emit := ev.loopExitClass(i)
	if kind != "continue" || !emit {
		return "", false
	}
	c := ev.operandE(op.Op1)
	guard := c.wrap(precLowest + 1) // JMPNZ jumps when TRUE -> guard is the condition
	if op.Op == "ZEND_JMPZ" {
		guard = unary("!", c).Text // JMPZ jumps when FALSE -> guard is its negation
	}
	return ind(d) + "if (" + guard + ") { continue; }", true
}

// jmpIsSkipElse reports that the ZEND_JMP at k terminates a then-block as a genuine
// skip-else (a forward jump over the else arm), as opposed to a loop entry JMP, a
// break/continue, or a backward edge — none of which the else logic may claim.
func (ev *evaluator) jmpIsSkipElse(k int) bool {
	if k < 0 || k >= len(ev.ops) || ev.ops[k].Op != "ZEND_JMP" {
		return false
	}
	if ev.isLoopHeader(k) || ev.jmpIsLoopExit(k) {
		return false
	}
	p := ev.posOf(ev.jumpTarget(ev.ops[k]))
	return p < 0 || p > k // forward or unresolved only
}

func (ev *evaluator) signature() string {
	m := ev.m
	params := ev.paramList()
	// A method/function that returns by reference compiles to ZEND_RETURN_BY_REF
	// instead of ZEND_RETURN; recover the `&` so `$x = &f()` binds the reference
	// the callee actually returned (dropping it would silently copy the value).
	ref := ""
	for _, o := range m.Oplines {
		if o.Op == "ZEND_RETURN_BY_REF" {
			ref = "&"
			break
		}
	}
	var sig string
	if isClosureName(m.Function) {
		sig = "function " + ref + "(" + params + ")" // anonymous closure
	} else {
		// A namespaced top-level function (`App\Admin\probe`) declares under its
		// bare name; the file's `namespace X;` supplies the rest.
		name := m.Function
		if isQualifiedIdent(name) {
			name = baseName(name)
		}
		sig = "function " + ref + name + "(" + params + ")"
	}
	if m.Class != "" && !isClosureName(m.Function) {
		prefix := ""
		if m.Vis != "" {
			prefix += m.Vis + " "
		}
		if m.Static {
			prefix += "static "
		}
		if m.Abstract {
			prefix = "abstract " + prefix
		}
		sig = prefix + sig
	}
	return sig
}

// posOf returns the slice position of opline index i, or -1.
func (ev *evaluator) posOf(i int) int {
	if p, ok := ev.byIdx[i]; ok {
		return p
	}
	return -1
}

func ind(n int) string { return strings.Repeat("    ", n) }

// ---- the structurer: emit statements for oplines in [lo,hi) at depth d ----

func (ev *evaluator) structure(lo, hi, d int) []string {
	var out []string
	i := lo
	for i < hi {
		ev.cursor = i
		op := ev.ops[i]
		switch {
		case isParamRecv(op.Op):
			i++
		case isIgnorable(op.Op) || op.Op == "ZEND_GENERATOR_CREATE":
			// Housekeeping/no-op opcodes carry no statement (isIgnorable is the single
			// list, shared with Coverage), plus the generator-frame setup, which also
			// emits nothing. Skipping here is identical to letting them fall to default —
			// stmt/foldPure are no-ops for them — but avoids the wasted probe.
			i++
		case op.Op == "ZEND_YIELD" || op.Op == "ZEND_YIELD_FROM":
			// A generator's `yield`/`yield from`. When the sent-value result is unused
			// or merely freed it is a statement; when a real consumer reads it (e.g.
			// `$x = yield $v`), fold it as an expression into that consumer instead.
			e := ev.yieldExpr(op)
			ev.store(op.Res, atom("("+e+")"))
			if op.Res.T == "UNUSED" || ev.resultDiscarded(i) {
				out = append(out, ind(d)+e+";")
			}
			i++
		case op.Op == "ZEND_MATCH":
			// A `match ($subject)` expression: fold it into its result slot (the
			// consumer — a RETURN/ASSIGN — emits it). Falls through to the switch
			// best-effort only if the arm shape is not recognised.
			if next, ok := ev.emitMatchTable(i, hi, d); ok {
				i = next
			} else {
				lines, next := ev.emitSwitchBestEffort(i, hi, d)
				out = append(out, lines...)
				i = next
			}
		case isSwitchHead(op.Op):
			if lines, next, ok := ev.emitSwitchTable(i, hi, d); ok {
				out = append(out, lines...)
				i = next
			} else {
				lines, next := ev.emitSwitchBestEffort(i, hi, d)
				out = append(out, lines...)
				i = next
			}
		case isSyntheticReturn(op):
			// Zend's implicit trailing `return null` (ext == -1). Never user code;
			// dropping it keeps switch/if bodies from ending in a stray `return;`.
			i++
		case ev.isTryStart(i, hi):
			r := ev.tryByLo[i]
			out = append(out, ev.emitTryCatch(r, d)...)
			i = r.end
		case ev.isMatchStart(i, hi):
			r := ev.matchByLo[i]
			ev.emitMatch(r, d) // folds the match expr into its result slot
			i = r.end
		case ev.isLoopHeader(i):
			lines, next := ev.emitLoop(ev.loopByHdr[i], hi, d)
			out = append(out, lines...)
			i = next
		case op.Op == "ZEND_FE_RESET_R", op.Op == "ZEND_FE_RESET_RW", op.Op == "ZEND_FE_RESET":
			lines, next := ev.emitForeach(i, hi, d)
			out = append(out, lines...)
			i = next
		case op.Op == "ZEND_CATCH":
			// A stray CATCH without our try recognition: annotate, keep going.
			out = append(out, ind(d)+"// decompiler: catch block for "+ev.operandE(op.Op1).Text)
			i++
		case isCondJump(op.Op):
			lines, next := ev.emitConditional(i, hi, d)
			out = append(out, lines...)
			if next > i {
				i = next
			} else {
				i++
			}
		case op.Op == "ZEND_JMP":
			// A lone JMP we did not consume as if/else/loop is a forward exit of the
			// enclosing loop — a `break`/`continue` — or a plain fall-through. When it
			// jumps cleanly to the enclosing loop's increment/condition (continue) or
			// to/past its end (break), emit that keyword; dropping it (the previous
			// behaviour) silently changed the loop's control flow. Otherwise skip: the
			// structure is handled by the enclosing construct, or the target is too
			// corrupted to classify (a backward edge is noted).
			if kw := ev.loopExit(i); kw != "" {
				out = append(out, ind(d)+kw)
				i++
				continue
			}
			t := ev.jumpTarget(op)
			if t >= 0 && ev.posOf(t) <= i && ev.posOf(t) >= lo {
				out = append(out, ind(d)+"// decompiler: loop back-edge to L"+itoa(t))
			}
			i++
		default:
			line, isStmt := ev.stmt(op)
			if isStmt {
				if line != "" {
					out = append(out, ind(d)+line)
				}
			} else if !ev.foldPure(i) {
				if !isIgnorable(op.Op) {
					ev.unhandled[op.Op]++
				}
			}
			i++
		}
	}
	return out
}

// ---- conditional (if / else) and folded ternary/short-circuit ----

// emitConditional dispatches every conditional jump: JMPZ_EX/JMPNZ_EX fold to
// && / ||, JMP_SET to `?:`, COALESCE to `??`, and JMPZ/JMPNZ to a ternary or a
// real if/else. It always returns the next position to resume at.
func (ev *evaluator) emitConditional(i, hi, d int) ([]string, int) {
	op := ev.ops[i]
	switch op.Op {
	case "ZEND_JMPZ_EX":
		ev.foldShortCircuit(i, "&&")
		return nil, ev.foldEnd(i)
	case "ZEND_JMPNZ_EX":
		ev.foldShortCircuit(i, "||")
		return nil, ev.foldEnd(i)
	case "ZEND_JMP_SET":
		ev.foldJmpSet(i)
		return nil, ev.foldEnd(i)
	case "ZEND_COALESCE":
		if line, next, ok := ev.coalesceAssign(i); ok {
			return []string{ind(d) + line}, next
		}
		ev.foldCoalesce(i)
		return nil, ev.foldEnd(i)
	case "ZEND_JMP_NULL":
		// Nullsafe `?->` (8.0+), NOT `??`: mark the fetched base so its following
		// method/property access renders with the nullsafe arrow.
		ev.foldNullsafe(i)
		return nil, i + 1
	}

	// Ternary shape: JMPZ c ->Lf ; <true> QM_ASSIGN a->~x ; JMP ; Lf:<false> QM_ASSIGN b->~x
	// (detected from the reliable JMPZ target + matching QM_ASSIGN result slots;
	// the unconditional JMP target is NOT trusted).
	if op.Op == "ZEND_JMPZ" {
		if end, ok := ev.tryFoldTernary(i); ok {
			return nil, end
		}
	}
	// A backward conditional jump to the enclosing loop's continue point is a guarded
	// `if (cond) continue;` (the common `if (!x) continue;` inside a foreach), which the
	// if/else chain cannot structure from a backward target.
	if line, ok := ev.condLoopExit(i, d); ok {
		return []string{line}, i + 1
	}
	return ev.emitCondChain(i, hi, d)
}

// condThenEnd computes the then-block end for the conditional jump at position i
// WITHOUT trusting the corrupted target's numeric value: the target seeds a guess,
// which is then (a) pushed out of any foreach it would bisect, (b) clamped to the
// enclosing window, (c) on a backward/garbage target, replaced by a guard clamp to
// the first unconditional exit, and (d) shrunk to that exit when one precedes any
// nested branch. clamped=true means the end was forced (window/guard), so no
// reliable else-marking JMP can sit at end-1. ok=false only when unstructurable.
func (ev *evaluator) condThenEnd(i, hi int) (thenEnd int, clamped, ok bool) {
	op := ev.ops[i]
	thenEnd = ev.posOf(ev.jumpTarget(op))
	if thenEnd > i {
		// Never bisect a foreach or a while/for loop the then-block opens; the
		// corrupted target otherwise ends mid-construct, orphaning or duplicating it.
		if nc := ev.clampOutOfForeach(thenEnd); nc > thenEnd && nc <= hi {
			thenEnd = nc
		}
		if nc := ev.clampOutOfLoop(thenEnd); nc > thenEnd && nc <= hi {
			thenEnd = nc
		}
	}
	if thenEnd > hi {
		thenEnd, clamped = hi, true
	}
	if thenEnd < 0 || thenEnd <= i {
		if ex := ev.firstUnconditionalExit(i+1, hi); ex >= 0 {
			thenEnd, clamped = ex+1, true
		} else {
			return thenEnd, clamped, false
		}
	}
	if ex := ev.firstUnconditionalExit(i+1, thenEnd); ex >= 0 && ex+1 < thenEnd {
		thenEnd = ex + 1
	}
	return thenEnd, clamped, true
}

// emitCondChain reconstructs a whole if / elseif* / else cascade from the first
// conditional jump at i. Each non-final arm's then-block ends in a "skip" JMP
// (jumping over the remaining arms); its PRESENCE — never its corrupted target —
// marks that another arm follows. The else region is itself an `elseif` when it
// opens with a fresh condition test (a conditional jump reached only through
// condition-building ops), so the cascade is walked iteratively. The terminal
// `else` has no reliable merge target, so its extent is bounded by the preceding
// arm's body length (a cascade's arms are near-parallel) plus a first-exit clamp.
func (ev *evaluator) emitCondChain(i, hi, d int) ([]string, int) {
	arms, elseLo, elseHi, elseInferred, next, ok := ev.collectCondArms(i, hi)
	if !ok {
		op := ev.ops[i]
		return []string{ind(d) + "// decompiler: unstructured " + op.Op + " -> L" + itoa(ev.jumpTarget(op))}, i + 1
	}

	var lines []string
	for idx, a := range arms {
		if idx == 0 {
			lines = append(lines, ind(d)+"if ("+a.cond+") {")
		} else {
			lines = append(lines, ind(d)+"} elseif ("+a.cond+") {")
		}
		lines = append(lines, ev.structure(a.lo, a.hi, d+1)...)
	}
	if elseLo >= 0 {
		hdr := ind(d) + "} else {"
		if elseInferred {
			hdr += " // decompiler: else extent inferred (ionCube obfuscates the merge JMP)"
		}
		lines = append(lines, hdr)
		lines = append(lines, ev.structure(elseLo, elseHi, d+1)...)
	}
	lines = append(lines, ind(d)+"}")
	return lines, next
}

// condArm is one arm of an if/elseif cascade: its rendered condition and the
// opline span [lo,hi) of its then-block body.
type condArm struct {
	cond   string
	lo, hi int
}

// collectCondArms walks the if/elseif*/else cascade from the conditional jump at
// i, returning the collected arms, the else body span (elseLo < 0 when there is no
// else), whether the else extent had to be inferred, and the opline where the
// cascade ends. ok is false only when the very first arm is not structurable — the
// caller then emits an unstructured-jump note. This is pure collection: it folds
// each elseif's condition-building ops so their operands resolve, but emits no
// output lines (emitCondChain owns that).
func (ev *evaluator) collectCondArms(i, hi int) (arms []condArm, elseLo, elseHi int, elseInferred bool, next int, ok bool) {
	elseLo, elseHi = -1, -1
	cur := i
	next = hi
	for {
		op := ev.ops[cur]
		tp, clamped, spanned := ev.condThenEnd(cur, hi)
		if !spanned {
			if len(arms) == 0 {
				return nil, -1, -1, false, i + 1, false
			}
			next = cur + 1
			break
		}
		thenLo, thenHi := cur+1, tp
		hasElse := !clamped && thenHi-1 >= thenLo && ev.jmpIsSkipElse(thenHi-1)
		if hasElse {
			thenHi-- // drop the skip JMP
		}
		elseStart := tp
		// The JMPZ target's NUMBER is unreliable — Zend 4.x (PHP 8.1/8.3) reveals it
		// one past the skip-else JMP, so the target-seeded thenHi both misses the JMP
		// (no else detected) and folds the else body's first op into the then-block.
		// The skip-else JMP's POSITION is reliable, so anchor the then/else split on
		// it instead: the then-block ends at the JMP and the else region opens right
		// after it. Applies on 7.4 too, where the same corruption occurs for some
		// jump shapes; it is a no-op when the target already agreed with the JMP.
		if sj := ev.skipElseJMP(cur, hi); sj >= 0 {
			thenHi = sj
			hasElse = true
			elseStart = sj + 1
		}
		arms = append(arms, condArm{cond: ev.condExpr(op), lo: thenLo, hi: thenHi})
		if !hasElse {
			next = tp
			break
		}
		if hj, isElseif := ev.elseIfHead(elseStart, hi); isElseif {
			// The else region is another condition test => `elseif`. Fold its
			// condition-building ops (so its operands resolve) and continue the chain.
			for k := elseStart; k < hj; {
				ev.foldPure(k)
				if e, done := ev.foldedUntil[k]; done && e > k+1 {
					k = e
				} else {
					k++
				}
			}
			cur = hj
			continue
		}
		// Plain else. Bound it by the preceding arm's body length (near-parallel
		// arms) and the first unconditional exit; never split a foreach.
		sibLen := thenHi - thenLo
		if sibLen < 1 {
			sibLen = 1
		}
		ee := elseStart + sibLen
		if ex := ev.firstUnconditionalExit(elseStart, hi); ex >= 0 && ex+1 < ee {
			ee = ex + 1
		}
		if nc := ev.clampOutOfForeach(ee); nc > ee && nc <= hi {
			ee = nc
		}
		if nc := ev.clampOutOfLoop(ee); nc > ee && nc <= hi {
			ee = nc
		}
		if ee > hi {
			ee = hi
		}
		if ee <= elseStart {
			ee = elseStart + 1
		}
		elseLo, elseHi = elseStart, ee
		next = ee
		// The merge is inferred (no reliable target) unless the else body itself
		// ends in an unconditional exit, which pins the boundary exactly.
		switch ev.ops[elseHi-1].Op {
		case "ZEND_RETURN", "ZEND_RETURN_BY_REF", "ZEND_GENERATOR_RETURN", "ZEND_THROW", "ZEND_EXIT":
		default:
			elseInferred = true
		}
		break
	}
	return arms, elseLo, elseHi, elseInferred, next, true
}

// skipElseJMP locates the skip-else JMP that terminates the then-block of the
// conditional at cur, from opcode STRUCTURE rather than the corrupted jump target.
// A fall-through if/else compiles to `JMPZ cond; <then>; JMP merge; <else>`: the
// JMP after the then-block is what makes an else exist. Its target NUMBER is
// obfuscated (Zend 4.x reveals the paired JMPZ target one past this JMP, so the
// target-seeded then-end misses it), but its POSITION is not — scanning forward
// from the condition, the first FORWARD ZEND_JMP reached before any unconditional
// exit, nested if/switch/loop header, or foreach fetch is that terminator. Its
// index is the then-block end; the else region begins at the next opline.
//
// Returns -1 when the then-block exits (return/throw/exit — a return cascade or an
// if with no else), opens a nested branch whose extent we cannot cheaply span, or
// its trailing JMP is backward (a loop back-edge / continue, never a skip-else). In
// those cases the caller keeps the existing target-seeded then-end + hasElse logic,
// so return cascades, loop bodies, and elseif chains behave exactly as before.
func (ev *evaluator) skipElseJMP(cur, hi int) int {
	if hi > len(ev.ops) {
		hi = len(ev.ops)
	}
	for k := cur + 1; k < hi; k++ {
		op := ev.ops[k].Op
		switch {
		case op == "ZEND_JMP":
			// A while/for loop's entry JMP, and a loop break/continue, are all FORWARD
			// jumps shaped exactly like a skip-else terminator. Claiming one folds the
			// loop (or the guarded body) into a bogus `elseif` arm and orphans the latch
			// (rendered as `// unstructured ZEND_JMP*`). jmpIsSkipElse excludes them.
			if ev.jmpIsSkipElse(k) {
				return k // forward (or unresolved) JMP: the skip-else terminator
			}
			return -1 // loop entry/exit or backward edge: not a skip-else
		case op == "ZEND_RETURN", op == "ZEND_RETURN_BY_REF",
			op == "ZEND_GENERATOR_RETURN", op == "ZEND_THROW", op == "ZEND_EXIT":
			return -1 // then-block exits before any JMP: no fall-through else
		case op == "ZEND_QM_ASSIGN":
			// A JMPZ; QM_ASSIGN; JMP; QM_ASSIGN region is a ternary (both arms write
			// one result slot), not an if/else — its JMP is not a skip-else. When the
			// ternary fold declined it, leave the (degenerate) rendering to the caller.
			return -1
		case op == "ZEND_JMPZ", op == "ZEND_JMPNZ", op == "ZEND_JMPZNZ",
			isSwitchHead(op), op == "ZEND_FE_RESET_R", op == "ZEND_FE_RESET_RW",
			op == "ZEND_FE_RESET", strings.HasPrefix(op, "ZEND_FE_FETCH"),
			ev.isLoopHeader(k):
			return -1 // nested branch/loop head: defer to the target-seeded heuristic
		}
	}
	return -1
}

// elseIfHead reports whether the else region starting at lo opens a fresh
// condition test — i.e. it is an `elseif`. It scans for the arm's conditional jump
// (JMPZ/JMPNZ), permitting only condition-building ops before it; any statement
// (an assignment, echo, return, loop/switch head, a discarded call, ...) means the
// region is a plain `else` block, not an elseif.
func (ev *evaluator) elseIfHead(lo, hi int) (int, bool) {
	if hi > len(ev.ops) {
		hi = len(ev.ops)
	}
	for k := lo; k < hi; k++ {
		switch ev.ops[k].Op {
		case "ZEND_JMPZ", "ZEND_JMPNZ":
			return k, true
		}
		if !ev.isConditionBuildOp(k) {
			return -1, false
		}
	}
	return -1, false
}

// isConditionBuildOp reports whether the opline at k can appear inside a condition
// expression (fetches, calls whose result is used, comparisons, short-circuits,
// housekeeping) rather than being a statement. A call whose result is discarded is
// treated as a statement, so a plain `else` opening with a void call is not
// mistaken for an `elseif`.
func (ev *evaluator) isConditionBuildOp(k int) bool {
	op := ev.ops[k]
	switch op.Op {
	case "ZEND_DO_FCALL", "ZEND_DO_FCALL_BY_NAME", "ZEND_DO_UCALL", "ZEND_DO_ICALL":
		return (op.Res.T == "TMP" || op.Res.T == "VAR") && ev.resultConsumed(k)
	}
	switch op.Op {
	// statement-level markers => not part of a condition
	case "ZEND_ASSIGN", "ZEND_ASSIGN_REF", "ZEND_ASSIGN_DIM", "ZEND_ASSIGN_OBJ",
		"ZEND_ASSIGN_STATIC_PROP", "ZEND_ASSIGN_OP", "ZEND_ASSIGN_DIM_OP", "ZEND_ASSIGN_OBJ_OP",
		"ZEND_ASSIGN_CONCAT", "ZEND_ASSIGN_ADD", "ZEND_ASSIGN_SUB", "ZEND_ASSIGN_MUL",
		"ZEND_ASSIGN_DIV", "ZEND_ECHO", "ZEND_PRINT", "ZEND_RETURN", "ZEND_RETURN_BY_REF",
		"ZEND_GENERATOR_RETURN", "ZEND_THROW", "ZEND_EXIT", "ZEND_UNSET_CV", "ZEND_UNSET_DIM",
		"ZEND_UNSET_OBJ", "ZEND_PRE_INC", "ZEND_PRE_DEC", "ZEND_POST_INC", "ZEND_POST_DEC",
		"ZEND_PRE_INC_OBJ", "ZEND_POST_INC_OBJ", "ZEND_PRE_DEC_OBJ", "ZEND_POST_DEC_OBJ",
		"ZEND_PRE_INC_STATIC_PROP", "ZEND_POST_INC_STATIC_PROP",
		"ZEND_PRE_DEC_STATIC_PROP", "ZEND_POST_DEC_STATIC_PROP",
		"ZEND_JMP", "ZEND_JMPZNZ", "ZEND_BRK", "ZEND_CONT",
		"ZEND_INCLUDE_OR_EVAL", "ZEND_FE_RESET_R", "ZEND_FE_RESET_RW", "ZEND_FE_RESET",
		"ZEND_SWITCH_STRING", "ZEND_SWITCH_LONG", "ZEND_MATCH", "ZEND_CATCH":
		return false
	}
	if strings.HasPrefix(op.Op, "ZEND_FE_FETCH") {
		return false
	}
	return true
}

// firstUnconditionalExit returns the position of the first opline in [lo,hi) that
// unconditionally leaves the current block (return/throw/exit), or -1 if a nested
// branch (a conditional jump, unconditional JMP, switch head, foreach reset, or
// case) is reached first — in which case the block's extent is governed by that
// nested structure, not by a straight-line exit.
func (ev *evaluator) firstUnconditionalExit(lo, hi int) int {
	if hi > len(ev.ops) {
		hi = len(ev.ops)
	}
	for k := lo; k < hi; k++ {
		op := ev.ops[k].Op
		switch {
		case op == "ZEND_RETURN" || op == "ZEND_RETURN_BY_REF" || op == "ZEND_GENERATOR_RETURN" ||
			op == "ZEND_THROW" || op == "ZEND_EXIT":
			return k
		case isCondJump(op) || op == "ZEND_JMP" || op == "ZEND_JMPZNZ" || op == "ZEND_FAST_CALL" ||
			isSwitchHead(op) || op == "ZEND_CASE" || op == "ZEND_CASE_STRICT" || op == "ZEND_CATCH" ||
			op == "ZEND_FE_RESET_R" || op == "ZEND_FE_RESET_RW" || op == "ZEND_FE_RESET" ||
			strings.HasPrefix(op, "ZEND_FE_FETCH"):
			return -1
		}
	}
	return -1
}

func (ev *evaluator) foldEnd(i int) int {
	if e, ok := ev.foldedUntil[i]; ok && e > i {
		return e
	}
	return i + 1
}

// foldJmpSet renders `a ?: b` (JMP_SET). foldCoalesce renders `a ?? b`. Neither
// opcode carries a usable jump operand (op2 is UNUSED — the target lives in a field
// ionCube strips), so the fallback region is bounded structurally: the fallback
// QM_ASSIGN writes the SAME result slot, and the combined value is read by the next
// consumer of that slot. Without this the QM_ASSIGN silently overwrote `a`.
func (ev *evaluator) foldJmpSet(i int) {
	op := ev.ops[i]
	a := ev.operandE(op.Op1)
	join := ev.resultConsumerJoin(i, op.Res)
	if join > i+1 {
		if b, ok := ev.foldFallbackArm(i+1, join, op.Res); ok {
			ev.store(op.Res, E{Text: a.wrap(precTernary+1) + " ?: " + b.wrap(precTernary), Prec: precTernary})
			ev.foldedUntil[i] = join
			return
		}
	}
	ev.store(op.Res, a)
}

// foldNullsafe records that the JMP_NULL base slot begins a nullsafe chain, so the
// following ->method()/->prop access on that slot renders as `?->`. The base value
// is carried into the result slot as the fallback when the chain terminates here.
func (ev *evaluator) foldNullsafe(i int) {
	op := ev.ops[i]
	if op.Op1.T == "TMP" || op.Op1.T == "VAR" {
		ev.nullsafe[slotKey(op.Op1.T, op.Op1.Num)] = true
	}
	ev.store(op.Res, ev.operandE(op.Op1))
}

// arrowOf returns `?->` when a preceding JMP_NULL marked this base slot as the start
// of a nullsafe chain, else the ordinary `->`.
func (ev *evaluator) arrowOf(o opline.Operand) string {
	if (o.T == "TMP" || o.T == "VAR") && ev.nullsafe[slotKey(o.T, o.Num)] {
		return "?->"
	}
	return "->"
}

func (ev *evaluator) foldCoalesce(i int) {
	op := ev.ops[i]
	a := ev.operandE(op.Op1)
	join := ev.resultConsumerJoin(i, op.Res)
	if join > i+1 {
		if b, ok := ev.foldFallbackArm(i+1, join, op.Res); ok {
			ev.store(op.Res, binary(a, "??", b, precCoalesce))
			ev.foldedUntil[i] = join
			return
		}
	}
	ev.store(op.Res, a)
}

// coalesceAssign recognises the `<lval> ??= <rhs>` statement (PHP 7.4+). It lowers
// to a COALESCE that jumps past an assign when the target is already set:
//
//	[FETCH_*_IS -> Tf]                 (only for a dim/prop target)
//	COALESCE   op1=<lval|Tf>  -> R     (result = the existing value, else...)
//	ASSIGN*    <lval> = <rhs> -> Ta    (ASSIGN | ASSIGN_DIM+OP_DATA | ASSIGN_OBJ+OP_DATA)
//	QM_ASSIGN  op1=Ta         -> R     (R := the freshly-assigned value)
//	FREE R                             (statement: the produced value is discarded)
//
// Reconstructing the compound operator is necessary because the lowered form folds
// to a discarded `<lval> ?? (<lval> = <rhs>)` whose result is FREEd — so it would
// otherwise vanish from the output entirely. The trailing FREE of R is required, so
// an ordinary `?? ` expression (whose result a real reader consumes) never matches.
func (ev *evaluator) coalesceAssign(coalesceIdx int) (string, int, bool) {
	coalesce := ev.ops[coalesceIdx]
	resultSlot := coalesce.Res
	if resultSlot.T != "TMP" && resultSlot.T != "VAR" {
		return "", 0, false
	}
	assignIdx, joinIdx := -1, -1
	// The lowered `??=` spans only a handful of oplines (the assign [+OP_DATA], the
	// QM_ASSIGN join, then FREE); bound the scan so a later same-slot write cannot be
	// mistaken for the join.
	const coalesceJoinWindow = 5
	for pos := coalesceIdx + 1; pos < len(ev.ops) && pos <= coalesceIdx+coalesceJoinWindow; pos++ {
		o := ev.ops[pos]
		// The QM_ASSIGN that carries the assigned value into the coalesce result is
		// matched by SHAPE, not name: Zend-4 (PHP 8.3) reveals relabel it (seen as
		// FAST_CONCAT / THROW / …) while preserving the single-source-into-TMP form.
		// It must be tested BEFORE the assign switch below, because for a fraction of
		// encodings the keytab relabels it to ASSIGN/ASSIGN_DIM/ASSIGN_OBJ itself — a
		// name-only switch would then swallow the join as a phantom second assignment,
		// leave joinIdx unset, and silently drop the whole `??=` statement. The join is
		// unambiguous by slot (it writes the coalesce result, which the real
		// assignment never does, so this cannot misfire on the assignment).
		if assignIdx >= 0 && isQMAssignShape(o) && sameSlot(o.Res, resultSlot) {
			joinIdx = pos
			break
		}
		switch o.Op {
		case "ZEND_ASSIGN", "ZEND_ASSIGN_DIM", "ZEND_ASSIGN_OBJ":
			if assignIdx < 0 {
				assignIdx = pos
			}
			continue
		}
	}
	if assignIdx < 0 || joinIdx < 0 || assignIdx >= joinIdx {
		return "", 0, false
	}
	assign := ev.ops[assignIdx]
	if !sameSlot(ev.ops[joinIdx].Op1, assign.Res) {
		return "", 0, false
	}
	// Statement context only: the COALESCE result must be discarded by a FREE.
	next := joinIdx + 1
	if next >= len(ev.ops) || ev.ops[next].Op != "ZEND_FREE" || !sameSlot(ev.ops[next].Op1, resultSlot) {
		return "", 0, false
	}
	next++

	var lval, rhs string
	save := ev.cursor
	ev.cursor = assignIdx // opData() reads the OP_DATA that trails an ASSIGN_DIM/OBJ
	switch assign.Op {
	case "ZEND_ASSIGN":
		lval = ev.lval(assign.Op1)
		rhs = ev.operandE(assign.Op2).wrap(precLowest + 1)
	case "ZEND_ASSIGN_DIM":
		key := ""
		if assign.Op2.T != "UNUSED" {
			key = ev.operandE(assign.Op2).wrap(precLowest + 1)
		}
		lval = ev.operandE(assign.Op1).wrap(precAtom) + "[" + key + "]"
		rhs = ev.opData()
	case "ZEND_ASSIGN_OBJ":
		lval = ev.objBase(assign.Op1) + "->" + ev.propName(assign.Op2)
		rhs = ev.opData()
	}
	ev.cursor = save
	ev.store(resultSlot, atom(lval)) // in case a later opline reads the coalesce result
	return lval + " ??= " + rhs + ";", next, true
}

// foldFallbackArm evaluates a `?:`/`??` fallback in [lo,hi) and returns its value:
// the op that writes result slot res with the QM_ASSIGN shape (see isQMAssignShape;
// some Zend-4/8.3 reveals mislabel that opcode). Preceding ops that build the arm's
// value are folded first. Falls back to whatever landed in the slot otherwise.
func (ev *evaluator) foldFallbackArm(lo, hi int, res opline.Operand) (E, bool) {
	for k := lo; k < hi; k++ {
		o := ev.ops[k]
		if isQMAssignShape(o) && sameSlot(o.Res, res) {
			return ev.operandE(o.Op1), true
		}
		ev.foldPure(k)
	}
	if b, ok := ev.tmp[slotKey(res.T, res.Num)]; ok {
		return b, true
	}
	return E{}, false
}

// isQMAssignShape reports whether op carries a single source value (op1) into a
// TMP/VAR result with no second operand — the shape of ZEND_QM_ASSIGN. Some Zend-4
// (PHP 8.3) reveals mislabel QM_ASSIGN's opcode (as IS_SMALLER_OR_EQUAL / STRLEN /
// UNSET_DIM / ECHO / …) while preserving this shape; ternary/match/coalesce arms are
// therefore matched structurally, only at positions where an arm-assign is expected.
func isQMAssignShape(op opline.Op) bool {
	if op.Op == "ZEND_QM_ASSIGN" {
		return true
	}
	if (op.Res.T != "TMP" && op.Res.T != "VAR") || op.Op2.T != "UNUSED" {
		return false
	}
	switch op.Op1.T {
	case "CONST", "CV", "TMP", "VAR":
		return true
	}
	return false
}

// resultConsumerJoin returns the position of the first opline after i that READS
// slot res (as op1 or op2). The intervening ops WRITE res (the ternary/coalesce
// fallback); the reader is where the combined value is consumed. -1 if none.
func (ev *evaluator) resultConsumerJoin(i int, res opline.Operand) int {
	if res.T != "TMP" && res.T != "VAR" {
		return -1
	}
	for k := i + 1; k < len(ev.ops); k++ {
		o := ev.ops[k]
		if sameSlot(o.Op1, res) || sameSlot(o.Op2, res) {
			return k
		}
	}
	return -1
}

// tryFoldTernary recognizes the QM_ASSIGN diamond from the RELIABLE JMPZ target
// plus matching QM_ASSIGN result slots — it never trusts the unconditional JMP
// target (corrupted in these dumps). Layout:
//
//	JMPZ cond -> Lf         (Lf reliable)
//	<true-arm pure ops> QM_ASSIGN a -> ~x ; JMP        (indices Lf-2, Lf-1)
//	Lf: <false-arm pure ops> QM_ASSIGN b -> ~x         (falseQM found by slot match)
//
// Returns (resumePos, true) if folded; resumePos is just past the false QM_ASSIGN.
func (ev *evaluator) tryFoldTernary(i int) (int, bool) {
	op := ev.ops[i]
	if op.Op != "ZEND_JMPZ" {
		return 0, false
	}
	// The JMPZ target Lf is the merge point: ops[Lf-1] is the true-arm's skip JMP and
	// ops[Lf-2] its QM_ASSIGN. Zend 3.x (PHP 7.4) reveals the JMPZ target exactly at
	// Lf; Zend 4.x (PHP 8) reveals it as Lf+1 (one past the merge). Resolve it once,
	// then accept the raw target first (7.4-exact), then one-before (8.x quirk) — only
	// one can satisfy the JMP/QM_ASSIGN shape, so there is no ambiguity.
	mergeTarget := ev.posOf(ev.jumpTarget(op))
	if mergeTarget < 0 || mergeTarget > len(ev.ops) {
		return 0, false
	}
	falseArmStart := -1
	for _, cand := range []int{mergeTarget, mergeTarget - 1} {
		if cand > i+1 && cand <= len(ev.ops) && cand-2 >= i &&
			ev.ops[cand-1].Op == "ZEND_JMP" && isQMAssignShape(ev.ops[cand-2]) {
			falseArmStart = cand
			break
		}
	}
	if falseArmStart < 0 {
		return 0, false
	}
	trueQM := ev.ops[falseArmStart-2]
	// false-arm: first QM_ASSIGN-shaped op at/after falseArmStart writing the same
	// slot. Bound the scan to a few ops past the merge so a same-slot write in an
	// enclosing expression is not mistaken for the false arm.
	const falseArmWindow = 8
	falseQMPos := -1
	for pos := falseArmStart; pos < len(ev.ops) && pos < falseArmStart+falseArmWindow; pos++ {
		if isQMAssignShape(ev.ops[pos]) && sameSlot(ev.ops[pos].Res, trueQM.Res) {
			falseQMPos = pos
			break
		}
		if isBlockTerminator(ev.ops[pos].Op) {
			break
		}
	}
	if falseQMPos < 0 {
		return 0, false
	}
	falseQM := ev.ops[falseQMPos]

	for pos := i + 1; pos < falseArmStart-2; pos++ {
		ev.foldPure(pos)
	}
	trueVal := ev.operandE(trueQM.Op1)
	for pos := falseArmStart; pos < falseQMPos; pos++ {
		ev.foldPure(pos)
	}
	falseVal := ev.operandE(falseQM.Op1)
	cond := ev.operandE(op.Op1)

	var tern E
	if simplifyBoolTernary(trueVal, falseVal) {
		// `cond ? true : false` == the boolean condition itself.
		tern = E{Text: cond.wrap(precLowest + 1), Prec: cond.Prec}
	} else {
		tern = E{Text: cond.wrap(precTernary+1) + " ? " + trueVal.wrap(precTernary+1) + " : " + falseVal.wrap(precTernary), Prec: precTernary}
	}
	ev.store(trueQM.Res, tern)
	ev.foldedUntil[i] = falseQMPos + 1
	return falseQMPos + 1, true
}

func simplifyBoolTernary(t, f E) bool {
	return t.Text == "true" && f.Text == "false"
}

func isBlockTerminator(op string) bool {
	switch op {
	case "ZEND_RETURN", "ZEND_RETURN_BY_REF", "ZEND_GENERATOR_RETURN", "ZEND_THROW",
		"ZEND_EXIT", "ZEND_JMPZ", "ZEND_JMPNZ":
		return true
	}
	return false
}

// condExpr renders the branch condition, honoring JMPNZ (jump-when-true) polarity.
func (ev *evaluator) condExpr(op opline.Op) string {
	c := ev.operandE(op.Op1)
	if op.Op == "ZEND_JMPNZ" {
		// jump taken when TRUE -> the fall-through is the false case, so the
		// "if" we emit guards the NOT of the tested value.
		return unary("!", c).Text
	}
	// JMPZ jumps when FALSE -> fall-through executes when TRUE: guard = cond.
	return c.wrap(precLowest + 1)
}

// ---- foreach ----

func (ev *evaluator) emitForeach(i, hi, d int) (lines []string, next int) {
	reset := ev.ops[i]
	arr := ev.operandE(reset.Op1)
	sp, ok := ev.foreachSpanAt(i)
	if !ok {
		// No VAR-paired FE_FETCH/FE_FREE (rare/malformed): keep the array live and
		// flag it rather than dropping logic.
		ev.foldPure(i)
		return []string{ind(d) + "// decompiler: foreach reset without fetch"}, i + 1
	}
	// value + optional key; bodyLo skips a key-assign the fetch emits.
	valExpr, keyExpr, bodyLo := ev.feTargets(sp.fetch)
	bodyHi := sp.free
	// The back-edge JMP is the last op before FE_FREE (its target is corrupted, so
	// it is identified positionally, not by offset); drop it from the body.
	if bodyHi-1 >= bodyLo && ev.ops[bodyHi-1].Op == "ZEND_JMP" {
		bodyHi--
	}
	header := "foreach (" + arr.wrap(precLowest+1) + " as "
	if keyExpr != "" {
		header += keyExpr + " => "
	}
	header += valExpr + ") {"
	lines = append(lines, ind(d)+header)
	ev.loopDepth++
	lines = append(lines, ev.structure(bodyLo, bodyHi, d+1)...)
	ev.loopDepth--
	lines = append(lines, ind(d)+"}")
	return lines, sp.free + 1
}

// isLoopHeader reports whether an emitted while/for/do-while loop begins at
// position i. A do-while's header equals its body's first opline, so it is
// suppressed while that loop is already being emitted (recursion guard).
func (ev *evaluator) isLoopHeader(i int) bool {
	_, ok := ev.loopByHdr[i]
	return ok && !ev.emitting[i]
}

// emitLoop renders a bottom-tested while/for loop (span idx) as a `while`. The
// increment clause of a `for` is placed at the end of the body (equivalent
// semantics); an assignment inside the condition (e.g. `while (($e=$d->read()) !==
// false)`) is rendered inline via the transient CV-expression override. Boundaries
// are all line-derived; the corrupted latch target is never read.
func (ev *evaluator) emitLoop(idx, hi, d int) (lines []string, next int) {
	sp := ev.loops[idx]
	if ev.emitting == nil {
		ev.emitting = map[int]bool{}
	}
	ev.emitting[sp.header] = true
	defer delete(ev.emitting, sp.header)
	// Evaluate the condition block (below the body). foldPure handles pure ops; an
	// assignment-in-condition (`while (($e = $d->read()) !== false)`) is materialised
	// by stmt(), which stores the `($e = …)` expression into the result temp the
	// following compare reads — so the assignment shows inline in the rendered cond.
	for k := sp.condLo; k < sp.latch; k++ {
		if !ev.foldPure(k) {
			ev.stmt(ev.ops[k])
		}
	}
	latch := ev.ops[sp.latch]
	var cond string
	if latch.Op == "ZEND_JMPNZ" {
		cond = ev.operandE(latch.Op1).wrap(precLowest + 1)
	} else {
		cond = "true" // unconditional back-JMP: while (true) with inner breaks
	}

	ev.loopDepth++
	if sp.kind == "dowhile" {
		lines = append(lines, ind(d)+"do {")
		lines = append(lines, ev.structure(sp.bodyLo, sp.bodyHi, d+1)...)
		lines = append(lines, ind(d)+"} while ("+cond+");")
		ev.loopDepth--
		return lines, sp.end
	}
	lines = append(lines, ind(d)+"while ("+cond+") {")
	lines = append(lines, ev.structure(sp.bodyLo, sp.bodyHi, d+1)...)
	if sp.incHi > sp.incLo {
		lines = append(lines, ev.structure(sp.incLo, sp.incHi, d+1)...)
	}
	ev.loopDepth--
	lines = append(lines, ind(d)+"}")
	return lines, sp.end
}

// isTryStart reports whether a reconstructed try/catch begins at i within [.,hi).
func (ev *evaluator) isTryStart(i, hi int) bool {
	r, ok := ev.tryByLo[i]
	return ok && r.end <= hi
}

// isMatchStart reports whether a reconstructed match chain begins at i within
// [.,hi). `match` is PHP 8.0+, so it never fires on a Zend 2.6 (5.x) target.
func (ev *evaluator) isMatchStart(i, hi int) bool {
	if ev.zend56 {
		return false
	}
	r, ok := ev.matchByLo[i]
	return ok && r.end <= hi
}

// emitTryCatch renders a try/catch[/finally] region.
func (ev *evaluator) emitTryCatch(r tryRegion, d int) []string {
	var lines []string
	lines = append(lines, ind(d)+"try {")
	lines = append(lines, ev.structure(r.tryLo, r.tryHi, d+1)...)
	for _, c := range r.catches {
		v := c.vari
		if v == "" {
			v = "e"
		}
		lines = append(lines, ind(d)+"} catch ("+c.class+" $"+v+") {")
		lines = append(lines, ev.structure(c.lo, c.hi, d+1)...)
	}
	if r.finallyLo >= 0 {
		lines = append(lines, ind(d)+"} finally {")
		lines = append(lines, ev.structure(r.finallyLo, r.finallyHi, d+1)...)
	}
	lines = append(lines, ind(d)+"}")
	return lines
}

// lookupClosure returns the closure/arrow Method a DECLARE_LAMBDA_FUNCTION targets,
// matched by the op's globally-unique op1 mangled key (so curried closures sharing a
// (file,line) resolve to distinct bodies).
func (ev *evaluator) lookupClosure(op opline.Op) *opline.Method {
	if ev.closures == nil {
		return nil
	}
	if key, _ := op.Op1.Val.(string); key != "" {
		if cm := ev.closures[key]; cm != nil {
			return cm
		}
	}
	// Fallback for reveals with no usable key (encrypted op1 on Zend 2.6).
	return ev.closures[flKey(ev.m.File, op.Line)]
}

// lexicalBinds collects the captured variables of a closure from the BIND_LEXICAL
// ops that immediately follow its DECLARE at position i (op2 is the captured CV).
// A by-reference capture (`use (&$v)`) is marked by the ZEND_BIND_REF bit in the
// opline's extended_value; dropping it would silently downgrade the capture to
// by-value and break any closure that mutates the outer variable.
func (ev *evaluator) lexicalBinds(i int) []string {
	var uses []string
	for k := i + 1; k < len(ev.ops); k++ {
		if ev.ops[k].Op != "ZEND_BIND_LEXICAL" {
			break
		}
		if v := ev.ops[k].Op2; v.T == "CV" {
			ref := ""
			if ev.ops[k].Ext&zendBindRef != 0 {
				ref = "&"
			}
			uses = append(uses, ref+"$"+v.Var)
		}
	}
	return uses
}

// closureCapturePrologue detects the 5.6 lexical-capture prologue a closure body
// carries — after the parameter RECVs, one `FETCH_R <name> -> Tk ; ASSIGN $v, Tk`
// pair per `use($v)` variable (the fetched name is the encrypted capture, the
// ASSIGN target is the readable local). It returns the first real body opline and
// the captured variables (with `$`). No match ⇒ (first non-RECV op, nil).
func (ev *evaluator) closureCapturePrologue() (bodyLo int, captured []string) {
	i := 0
	for i < len(ev.ops) && isParamRecv(ev.ops[i].Op) {
		i++
	}
	for i+1 < len(ev.ops) {
		f, a := ev.ops[i], ev.ops[i+1]
		fetch := f.Op == "ZEND_FETCH_R" || f.Op == "ZEND_FETCH_W" || f.Op == "ZEND_FETCH_RW"
		if !fetch || !(f.Res.T == "TMP" || f.Res.T == "VAR") || a.Op1.T != "CV" || !sameSlot(a.Op2, f.Res) {
			break
		}
		// By-value capture `use ($v)`: FETCH_<name> + ASSIGN $v = fetched.
		if a.Op == "ZEND_ASSIGN" {
			captured = append(captured, "$"+a.Op1.Var)
			i += 2
			continue
		}
		// By-reference capture `use (&$v)`: FETCH_W(<encrypted name>) + ASSIGN_REF
		// $v = &fetched. The captured name is encrypted in the FETCH but readable as
		// the ASSIGN_REF's CV target; prefix `&` so the reference is preserved.
		if a.Op == "ZEND_ASSIGN_REF" {
			captured = append(captured, "&$"+a.Op1.Var)
			i += 2
			continue
		}
		break
	}
	return i, captured
}

// hasByRefUse reports whether any captured variable is a by-reference `use (&$v)`.
func hasByRefUse(uses []string) bool {
	for _, u := range uses {
		if strings.HasPrefix(u, "&") {
			return true
		}
	}
	return false
}

// mergeUses appends the elements of b not already in a (used to combine
// DECLARE-site BIND_LEXICAL captures with a 5.6 body-prologue's).
func mergeUses(a, b []string) []string {
	for _, x := range b {
		seen := false
		for _, y := range a {
			if x == y {
				seen = true
				break
			}
		}
		if !seen {
			a = append(a, x)
		}
	}
	return a
}

// renderClosureExpr inlines a closure/arrow body as a PHP expression. A single
// `return <expr>` body renders as an auto-capturing `fn (…) => <expr>` (correct for
// by-value capture); a multi-statement body renders as `function (…) use (…) { … }`.
func (ev *evaluator) renderClosureExpr(cm *opline.Method, uses []string) E {
	sub := newEvaluator(cm, ev.ns)
	sub.closures = ev.closures
	sub.zend56 = ev.zend56
	// 5.6 carries a closure's `use($v)` captures as a body prologue (a
	// FETCH_R<name>+ASSIGN $v pair per capture) rather than DECLARE-site
	// BIND_LEXICAL ops; hoist those to `use(...)` and start the body past them so
	// they are not emitted as `$v = $<encrypted>` statements (a parse error).
	bodyLo, captured := sub.closureCapturePrologue()
	uses = mergeUses(uses, captured)
	body := sub.structure(bodyLo, len(sub.ops), 1)
	for len(body) > 0 && strings.TrimSpace(body[len(body)-1]) == "return;" {
		body = body[:len(body)-1]
	}
	params := sub.paramList()
	// The arrow-fn form (`fn (…) => expr`, auto-capturing) is PHP 7.4+; on a Zend
	// 2.6 target it would not lint, so fall through to `function (…) use (…) {…}`.
	// It also captures strictly by value, so a by-reference `use (&$v)` must never
	// collapse to it — that would silently sever the reference.
	if len(body) == 1 && !ev.zend56 && !hasByRefUse(uses) {
		s := strings.TrimSpace(body[0])
		if strings.HasPrefix(s, "return ") && strings.HasSuffix(s, ";") {
			expr := strings.TrimSuffix(strings.TrimPrefix(s, "return "), ";")
			return atom("fn (" + params + ") => " + expr)
		}
	}
	var b strings.Builder
	b.WriteString("function (" + params + ")")
	if len(uses) > 0 {
		b.WriteString(" use (" + strings.Join(uses, ", ") + ")")
	}
	b.WriteString(" {\n")
	for _, ln := range body {
		b.WriteString(ln + "\n")
	}
	b.WriteString("}")
	return atom(b.String())
}

// paramList renders this method's parameters as a `$a, &$b, $c = 1` clause.
func (ev *evaluator) paramList() string {
	var parts []string
	for _, p := range ev.m.Params {
		s := ""
		if p.ByRef {
			s += "&"
		}
		if p.Variadic {
			s += "..."
		}
		s += "$" + p.Name
		if p.HasDefault && !p.Variadic {
			s += " = " + phpLiteral(p.Default).Text
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, ", ")
}

// feTargets returns (valueExpr, keyExpr, bodyLo) for a FE_FETCH. bodyLo is the
// first body opline, past any key-assign the fetch emits. 7.4 with `$k => $v`
// puts the value in op2 (a CV) and the key in the result temp, then ASSIGNs that
// temp to the key CV; 5.6 carries the key in a following OP_DATA.
func (ev *evaluator) feTargets(fp int) (val, key string, bodyLo int) {
	f := ev.ops[fp]
	bodyLo = fp + 1
	if v, k, blo, ok := ev.feTargets56(fp); ok {
		return v, k, blo
	}
	if f.Op2.T == "CV" {
		val = "$" + f.Op2.Var
	} else if f.Res.T == "CV" {
		val = "$" + f.Res.Var
	} else {
		val = ev.operandE(f.Res).Text
		if val == "" {
			val = "$_v" + itoa(f.Res.Num)
		}
	}
	// `foreach ($a as &$v)` compiles to FE_FETCH_RW — carry the by-reference `&`.
	if f.Op == "ZEND_FE_FETCH_RW" {
		val = "&" + val
	}
	key, bodyLo = ev.feTargetsKeyed(fp, f, bodyLo)
	// list/[] destructuring target: `foreach (… as [$a, $b])` stores the element in
	// a temp (op2) that the following FETCH_LIST_R+ASSIGN pairs tear apart. Rebuild
	// the pattern and skip those pairs so they are not emitted as body statements
	// reading an unassigned temp.
	if (f.Op2.T == "VAR" || f.Op2.T == "TMP") && bodyLo < len(ev.ops) &&
		ev.ops[bodyLo].Op == "ZEND_FETCH_LIST_R" && sameSlot(ev.ops[bodyLo].Op1, f.Op2) {
		if pat, next, ok := ev.destructurePattern(f.Op2, bodyLo); ok {
			val = pat
			bodyLo = next
		}
	}
	return
}

// feTargets56 handles the Zend 2.6 (PHP 5.x) FE_FETCH shape, where the fetch
// leaves the value in its result slot and (for a keyed loop) the key in a
// following OP_DATA, then binds them with ASSIGN_REF/ASSIGN into the user CVs
// (`&$v` iff ASSIGN_REF). It hoists those binds into the `foreach (… as [$k =>]
// [&]$v)` header so they are not emitted as `$v = &$_vN; $k = $_tN;` body
// statements, returning the value/key text and the body start past the binds. ok
// is false when this is not that shape (7.x carries the value CV in FE_FETCH op2
// directly), so the caller uses the 7.x path.
func (ev *evaluator) feTargets56(fp int) (val, key string, bodyLo int, ok bool) {
	f := ev.ops[fp]
	if !(ev.zend56 && f.Op == "ZEND_FE_FETCH" && f.Op2.T != "CV") {
		return "", "", 0, false
	}
	valSlot := f.Res
	i := fp + 1
	var keySlot opline.Operand
	haveKey := false
	if i < len(ev.ops) && ev.ops[i].Op == "ZEND_OP_DATA" {
		if ev.ops[i].Res.T == "TMP" || ev.ops[i].Res.T == "VAR" {
			keySlot, haveKey = ev.ops[i].Res, true
		}
		i++
	}
	valName, byref, gotVal := "", false, false
	keyName, gotKey := "", false
	// At most two binds follow the fetch: the value CV, then (keyed) the key CV.
	for n := 0; n < 2 && i < len(ev.ops); n++ {
		o := ev.ops[i]
		if !gotVal && (o.Op == "ZEND_ASSIGN_REF" || o.Op == "ZEND_ASSIGN") &&
			o.Op1.T == "CV" && sameSlot(o.Op2, valSlot) {
			valName, byref, gotVal = o.Op1.Var, o.Op == "ZEND_ASSIGN_REF", true
			i++
			continue
		}
		if haveKey && !gotKey && o.Op == "ZEND_ASSIGN" &&
			o.Op1.T == "CV" && sameSlot(o.Op2, keySlot) {
			keyName, gotKey = o.Op1.Var, true
			i++
			continue
		}
		break
	}
	if !gotVal {
		return "", "", 0, false
	}
	val = "$" + valName
	if byref {
		val = "&" + val
	}
	if gotKey {
		key = "$" + keyName
	}
	return val, key, i, true
}

// feTargetsKeyed recovers the `$k =>` key CV of a keyed foreach and the body start
// past the key-binding op: 7.4 puts the key in the FE_FETCH result temp and ASSIGNs
// it to the key CV next; 5.6 carries it in a following OP_DATA. Returns the passed
// bodyLo (and empty key) for an unkeyed loop.
func (ev *evaluator) feTargetsKeyed(fp int, f opline.Op, bodyLo int) (key string, newBodyLo int) {
	newBodyLo = bodyLo
	// 7.4 keyed loop: FE_FETCH result temp -> ASSIGN key-CV.
	if f.Op2.T == "CV" && (f.Res.T == "TMP" || f.Res.T == "VAR") &&
		fp+1 < len(ev.ops) {
		if nx := ev.ops[fp+1]; nx.Op == "ZEND_ASSIGN" && nx.Op1.T == "CV" && sameSlot(nx.Op2, f.Res) {
			key = "$" + nx.Op1.Var
			newBodyLo = fp + 2
		}
	}
	// 5.6 keyed loop: key in a following OP_DATA.
	if key == "" && fp+1 < len(ev.ops) && ev.ops[fp+1].Op == "ZEND_OP_DATA" {
		if kd := ev.ops[fp+1]; kd.Op1.T == "CV" {
			key = "$" + kd.Op1.Var
			newBodyLo = fp + 2
		}
	}
	return key, newBodyLo
}

// destructurePattern rebuilds a `list()`/`[]` assignment target from the
// FETCH_LIST_R + ASSIGN pairs the compiler emits to tear apart a source slot (the
// element temp of a `foreach (… as [$a, $b])`). It returns the pattern text, the
// position just past the consumed pairs, and whether any were found. Nested
// patterns (`[[$p], [$q]]`) recurse on a fetched sub-slot.
func (ev *evaluator) destructurePattern(src opline.Operand, from int) (string, int, bool) {
	type elem struct {
		keyed  bool
		key    string
		target string
	}
	var elems []elem
	pos := from
	expect := int64(0)
	for pos < len(ev.ops) {
		fl := ev.ops[pos]
		if fl.Op != "ZEND_FETCH_LIST_R" || !sameSlot(fl.Op1, src) {
			break
		}
		target, next := "", pos
		if pos+1 < len(ev.ops) {
			if as := ev.ops[pos+1]; as.Op == "ZEND_ASSIGN" && as.Op1.T == "CV" && sameSlot(as.Op2, fl.Res) {
				target, next = "$"+as.Op1.Var, pos+2
			}
		}
		if target == "" {
			if sub, sn, ok := ev.destructurePattern(fl.Res, pos+1); ok {
				target, next = sub, sn
			} else {
				break
			}
		}
		e := elem{target: target}
		if n, ok := toInt64(fl.Op2.Val); ok {
			if n != expect {
				e.keyed, e.key = true, ev.operandE(fl.Op2).Text
			}
			expect = n + 1
		} else {
			e.keyed, e.key = true, ev.operandE(fl.Op2).Text
		}
		elems = append(elems, e)
		pos = next
	}
	if len(elems) == 0 {
		return "", from, false
	}
	var parts []string
	for _, e := range elems {
		if e.keyed {
			parts = append(parts, e.key+" => "+e.target)
		} else {
			parts = append(parts, e.target)
		}
	}
	return "[" + strings.Join(parts, ", ") + "]", pos, true
}

// loopBounds finds the end of a loop whose header is at hp: the position after
// the backward JMP targeting hp (bodyHi excludes that JMP), and the position to
// resume at (after any FE_FREE). Falls back to the enclosing hi.
func (ev *evaluator) loopBounds(hp, hi int) (bodyHi, after int) {
	depth := 0
	for k := hp + 1; k < hi; k++ {
		o := ev.ops[k]
		if o.Op == "ZEND_FE_FETCH_R" || o.Op == "ZEND_FE_FETCH_RW" || o.Op == "ZEND_FE_FETCH" {
			depth++
		}
		if o.Op == "ZEND_JMP" {
			t := ev.jumpTarget(o)
			if ev.posOf(t) == hp {
				if depth > 0 {
					depth--
					continue
				}
				after = k + 1
				// skip a trailing FE_FREE
				if after < hi && (ev.ops[after].Op == "ZEND_FE_FREE" || ev.ops[after].Op == "ZEND_SWITCH_FREE") {
					after++
				}
				return k, after
			}
		}
	}
	// no clean back-edge (obfuscated target): take the FE_FREE as the boundary.
	for k := hp + 1; k < hi; k++ {
		if ev.ops[k].Op == "ZEND_FE_FREE" {
			return k, k + 1
		}
	}
	return hi, hi
}

// ---- switch ----

func (ev *evaluator) emitSwitchBestEffort(i, hi, d int) (lines []string, next int) {
	head := ev.ops[i]
	subj := ev.operandE(head.Op1)
	labels, bodyStart := ev.pairLabels(i, hi)
	bodies := ev.splitCaseBodies(bodyStart, hi, d)

	lines = append(lines, ind(d)+"switch ("+subj.wrap(precLowest+1)+") {")
	if len(labels) != len(bodies) {
		lines = append(lines, ind(d+1)+fmt.Sprintf("// decompiler: %d case label(s), %d body block(s); ionCube obfuscates the", len(labels), len(bodies)))
		lines = append(lines, ind(d+1)+"// switch jump table, so exact label->body grouping is not recoverable here.")
		lines = append(lines, ind(d+1)+"// All labels and all bodies are preserved below; pairing is best-effort.")
	}
	// Best-effort 1:1 pairing (correct when counts match, e.g. no fall-through).
	nb := len(bodies)
	for idx, lb := range labels {
		lines = append(lines, ind(d+1)+"case "+lb+":")
		bi := idx
		if bi >= nb {
			bi = nb - 1
		}
		if nb > 0 && (len(labels) == nb) {
			lines = append(lines, bodies[bi]...)
		}
	}
	if len(labels) != nb {
		// counts differ (fall-through groups collapsed): emit every distinct body
		// after the labels so no logic is lost, dropping break-only/empty noise.
		seen := map[string]bool{}
		for _, b := range bodies {
			var kept []string
			for _, ln := range b {
				t := strings.TrimSpace(ln)
				if t == "break;" || t == "" {
					continue
				}
				kept = append(kept, ln)
			}
			if len(kept) == 0 {
				continue
			}
			key := strings.Join(kept, "\n")
			if seen[key] {
				continue
			}
			seen[key] = true
			lines = append(lines, kept...)
		}
	}
	lines = append(lines, ind(d)+"}")
	return lines, hi
}

// pairLabels collects the switch's case-label values, walking the CASE(+JMPNZ/
// JMPZ) pairs after the head and the optional default-dispatch JMP. It returns the
// label texts in source order and the opline where the case bodies begin.
func (ev *evaluator) pairLabels(i, hi int) (labels []string, bodyStart int) {
	k := i + 1
	for k < hi {
		o := ev.ops[k]
		if o.Op == "ZEND_CASE" || o.Op == "ZEND_CASE_STRICT" {
			labels = append(labels, ev.operandE(o.Op2).Text)
			k++
			if k < hi && (ev.ops[k].Op == "ZEND_JMPNZ" || ev.ops[k].Op == "ZEND_JMPZ") {
				k++
			}
			continue
		}
		break
	}
	// After the case chain, an optional JMP is the default dispatch.
	if k < hi && ev.ops[k].Op == "ZEND_JMP" {
		k++
	}
	return labels, k
}

// splitCaseBodies structures the switch's case bodies — runs terminated by a
// RETURN, a break (JMP, rendered as `break;`), or the region end — into one line
// block each, in source order. FREE/SWITCH_FREE housekeeping ops are skipped. The
// bodies are structured under an incremented loopDepth so their break/continue see
// the switch.
func (ev *evaluator) splitCaseBodies(bodyStart, hi, d int) [][]string {
	var bodies [][]string
	end := hi
	ev.loopDepth++
	defer func() { ev.loopDepth-- }()
	k := bodyStart
	for k < end {
		o := ev.ops[k]
		if o.Op == "ZEND_FREE" || o.Op == "ZEND_SWITCH_FREE" {
			k++
			continue
		}
		if o.Op == "ZEND_RETURN" || o.Op == "ZEND_RETURN_BY_REF" || o.Op == "ZEND_GENERATOR_RETURN" {
			bodies = append(bodies, ev.structure(bodyStart, k+1, d+2))
			k++
			bodyStart = k
			continue
		}
		if o.Op == "ZEND_JMP" {
			bl := ev.structure(bodyStart, k, d+2)
			bl = append(bl, ind(d+2)+"break;")
			bodies = append(bodies, bl)
			k++
			bodyStart = k
			continue
		}
		k++
	}
	if bodyStart < end {
		bodies = append(bodies, ev.structure(bodyStart, end, d+2))
	}
	return bodies
}

// ---- statement emission for a single opline ----

// incDecLvalKind selects how a ++/-- opcode composes its lvalue operand(s).
type incDecLvalKind int

const (
	incDecCV         incDecLvalKind = iota // op1 is the whole lvalue ($x)
	incDecObj                              // op1 = object (UNUSED => $this), op2 = property
	incDecStaticProp                       // op1 = property CONST, op2 = class ref
)

// incDecOp folds one of the 12 ++/-- opcodes: delta is "++"/"--", prefix
// distinguishes ++$x from $x++, and kind picks the lvalue composition.
type incDecOp struct {
	delta  string
	prefix bool
	kind   incDecLvalKind
}

var incDecOps = map[string]incDecOp{
	"ZEND_PRE_INC":  {"++", true, incDecCV},
	"ZEND_PRE_DEC":  {"--", true, incDecCV},
	"ZEND_POST_INC": {"++", false, incDecCV},
	"ZEND_POST_DEC": {"--", false, incDecCV},

	"ZEND_PRE_INC_OBJ":  {"++", true, incDecObj},
	"ZEND_PRE_DEC_OBJ":  {"--", true, incDecObj},
	"ZEND_POST_INC_OBJ": {"++", false, incDecObj},
	"ZEND_POST_DEC_OBJ": {"--", false, incDecObj},

	"ZEND_PRE_INC_STATIC_PROP":  {"++", true, incDecStaticProp},
	"ZEND_PRE_DEC_STATIC_PROP":  {"--", true, incDecStaticProp},
	"ZEND_POST_INC_STATIC_PROP": {"++", false, incDecStaticProp},
	"ZEND_POST_DEC_STATIC_PROP": {"--", false, incDecStaticProp},
}

// incDecStmt renders a ++/-- statement and stores the value the opcode leaves in
// op.Res: a prefix op yields (++$x) as the value and `++$x;` as the statement, a
// postfix op yields the prior value $x and `$x++;`.
func (ev *evaluator) incDecStmt(op opline.Op, d incDecOp) string {
	var lv string
	switch d.kind {
	case incDecObj:
		// Compose the $obj->prop lvalue (not the whole op1), otherwise `$this->n++`
		// would increment `$this`.
		lv = ev.objLval(op.Op1, op.Op2)
	case incDecStaticProp:
		// Compose `Class::$prop` so `static::$count++` survives as a statement.
		lv = ev.className(op.Op2) + "::$" + ev.constStr(op.Op1)
	default:
		lv = ev.lval(op.Op1)
	}
	if d.prefix {
		expr := d.delta + lv
		ev.store(op.Res, atom("("+expr+")"))
		return expr + ";"
	}
	ev.store(op.Res, atom(lv))
	return lv + d.delta + ";"
}

func (ev *evaluator) stmt(op opline.Op) (string, bool) {
	if d, ok := incDecOps[op.Op]; ok {
		return ev.incDecStmt(op, d), true
	}
	switch op.Op {
	case "ZEND_ECHO":
		return "echo " + ev.operandE(op.Op1).wrap(precLowest+1) + ";", true
	case "ZEND_PRINT":
		e := "print " + ev.operandE(op.Op1).wrap(precLowest+1)
		ev.store(op.Res, atom("("+e+")"))
		return e + ";", true
	case "ZEND_RETURN", "ZEND_RETURN_BY_REF", "ZEND_GENERATOR_RETURN":
		// Bare `return;`: the synthetic op_array epilogue (isSyntheticReturn owns the
		// sentinel-ext check), or a user return of no value — op1 UNUSED or an explicit
		// `return null`.
		if isSyntheticReturn(op) || op.Op1.T == "UNUSED" || (op.Op1.T == "CONST" && op.Op1.Val == nil) {
			return "return;", true
		}
		return "return " + ev.operandE(op.Op1).wrap(precLowest+1) + ";", true
	case "ZEND_THROW":
		return "throw " + ev.operandE(op.Op1).wrap(precLowest+1) + ";", true
	case "ZEND_EXIT":
		if op.Op1.T == "UNUSED" {
			return "exit;", true
		}
		return "exit(" + ev.operandE(op.Op1).wrap(precLowest+1) + ");", true
	case "ZEND_ASSIGN":
		v := ev.operandE(op.Op2)
		s := ev.lval(op.Op1) + " = " + v.wrap(precLowest+1)
		ev.store(op.Res, atom("("+s+")"))
		ev.store(op.Op1, v) // subsequent reads of the CV see the new value
		return s + ";", true
	case "ZEND_ASSIGN_REF":
		s := ev.lval(op.Op1) + " = &" + ev.operandE(op.Op2).wrap(precUnary)
		return s + ";", true
	case "ZEND_ASSIGN_DIM":
		val := ev.opData()
		key := ""
		if op.Op2.T != "UNUSED" {
			key = ev.operandE(op.Op2).wrap(precLowest + 1)
		}
		return ev.operandE(op.Op1).wrap(precAtom) + "[" + key + "] = " + val + ";", true
	case "ZEND_ASSIGN_OBJ":
		val := ev.opData()
		return ev.objBase(op.Op1) + "->" + ev.propName(op.Op2) + " = " + val + ";", true
	case "ZEND_ASSIGN_STATIC_PROP":
		val := ev.opData()
		return ev.className(op.Op2) + "::$" + ev.constStr(op.Op1) + " = " + val + ";", true
	case "ZEND_ASSIGN_OBJ_OP":
		val := ev.opData()
		return ev.objBase(op.Op1) + "->" + ev.propName(op.Op2) + " " + assignOp(op.Ext) + "= " + val + ";", true
	case "ZEND_ASSIGN_DIM_OP":
		val := ev.opData()
		key := ev.operandE(op.Op2).wrap(precLowest + 1)
		return ev.operandE(op.Op1).wrap(precAtom) + "[" + key + "] " + assignOp(op.Ext) + "= " + val + ";", true
	case "ZEND_ASSIGN_OP":
		return ev.lval(op.Op1) + " " + assignOp(op.Ext) + "= " + ev.operandE(op.Op2).wrap(precLowest+1) + ";", true
	case "ZEND_ASSIGN_CONCAT", "ZEND_ASSIGN_ADD", "ZEND_ASSIGN_SUB", "ZEND_ASSIGN_MUL",
		"ZEND_ASSIGN_DIV", "ZEND_ASSIGN_MOD", "ZEND_ASSIGN_POW", "ZEND_ASSIGN_SL",
		"ZEND_ASSIGN_SR", "ZEND_ASSIGN_BW_OR", "ZEND_ASSIGN_BW_AND", "ZEND_ASSIGN_BW_XOR":
		return ev.compoundAssign(op, compoundAssignOp[op.Op]), true
	case "ZEND_DO_FCALL", "ZEND_DO_FCALL_BY_NAME", "ZEND_DO_UCALL", "ZEND_DO_ICALL":
		e, wasNew, ok := ev.finishCall(op)
		if !ok {
			return "", true // handled (unmatched call), emit nothing
		}
		if wasNew {
			// the constructed object lives in the NEW result slot; the ctor call
			// itself is not a statement.
			return "", true
		}
		// ionCube's 7.x/8.x production encoder can renumber a call's result slot so
		// it disagrees with the slot the immediately-following ZEND_ASSIGN reads
		// (see renumberedCallSink): the call's own result then looks discarded and
		// the assignment references an un-produced phantom slot ($x = $_vNN). Fold
		// the call expression into that sink so the assignment recovers `$x = <call>`.
		if sink, ok := ev.renumberedCallSink(ev.cursor); ok {
			ev.store(sink, e)
			return "", true
		}
		// A result in a TMP/VAR slot is only an expression if it is actually read
		// before being overwritten; otherwise the call's return value is discarded
		// (common on 5.6, where every call allocates a result slot) and the call
		// is a statement in its own right.
		if (op.Res.T == "TMP" || op.Res.T == "VAR") && ev.resultConsumed(ev.cursor) {
			ev.store(op.Res, e)
			return "", true
		}
		return e.Text + ";", true
	case "ZEND_INCLUDE_OR_EVAL":
		return ev.includeExpr(op) + ";", true
	case "ZEND_UNSET_CV":
		return "unset($" + op.Op1.Var + ");", true
	case "ZEND_UNSET_VAR":
		// Zend 2.6 compiles `unset($x)` to ZEND_UNSET_VAR with the name in op1 (a CV,
		// or a CONST when the name is a variable-variable). Dropping it silently is a
		// behavioral bug for `foreach (… as &$v) …; unset($v);` — without the unset,
		// a later `foreach (… as $v)` writes THROUGH the dangling reference and
		// corrupts the last element.
		if op.Op1.T == "CV" {
			return "unset($" + op.Op1.Var + ");", true
		}
		name := ev.constStr(op.Op1)
		if isIdent(name) {
			return "unset($" + name + ");", true
		}
		return "unset(${" + ev.operandE(op.Op1).wrap(precLowest+1) + "});", true
	case "ZEND_UNSET_DIM":
		return "unset(" + ev.operandE(op.Op1).wrap(precAtom) + "[" + ev.operandE(op.Op2).wrap(precLowest+1) + "]);", true
	case "ZEND_UNSET_OBJ":
		return "unset(" + ev.operandE(op.Op1).wrap(precAtom) + "->" + ev.propName(op.Op2) + ");", true
	case "ZEND_BRK":
		if ev.loopDepth > 0 {
			return "break;", true
		}
		return "// decompiler: break (loop not reconstructed)", true
	case "ZEND_CONT":
		if ev.loopDepth > 0 {
			return "continue;", true
		}
		return "// decompiler: continue (loop not reconstructed)", true
	}
	return "", false
}

// yieldExpr renders a ZEND_YIELD / ZEND_YIELD_FROM as its PHP expression:
// `yield`, `yield <value>`, `yield <key> => <value>`, or `yield from <source>`.
func (ev *evaluator) yieldExpr(op opline.Op) string {
	if op.Op == "ZEND_YIELD_FROM" {
		return "yield from " + ev.operandE(op.Op1).wrap(precLowest+1)
	}
	if op.Op1.T == "UNUSED" {
		return "yield"
	}
	val := ev.operandE(op.Op1).wrap(precLowest + 1)
	if op.Op2.T != "UNUSED" {
		return "yield " + ev.operandE(op.Op2).wrap(precLowest+1) + " => " + val
	}
	return "yield " + val
}

// resultDiscarded reports whether the result slot of the op at position i is never
// read, or is read only to be freed — the signal that a value-producing op (e.g.
// `yield`) is used as a statement rather than feeding a real consumer.
func (ev *evaluator) resultDiscarded(i int) bool {
	res := ev.ops[i].Res
	if res.T != "TMP" && res.T != "VAR" {
		return res.T == "UNUSED"
	}
	reads := func(o opline.Operand) bool { return o.T == res.T && o.Num == res.Num }
	for k := i + 1; k < len(ev.ops); k++ {
		o := ev.ops[k]
		if reads(o.Op1) || reads(o.Op2) {
			return o.Op == "ZEND_FREE" || o.Op == "ZEND_FE_FREE"
		}
		if o.Res.T == res.T && o.Res.Num == res.Num && isValueProducing(o.Op) {
			return false
		}
	}
	return true
}

// resultConsumed reports whether the result slot of the op at position i is read
// by a later opline before being overwritten (simple forward liveness). Used to
// decide if a call's return value is used (expression) or discarded (statement).
func (ev *evaluator) resultConsumed(i int) bool {
	res := ev.ops[i].Res
	if res.T != "TMP" && res.T != "VAR" {
		return false
	}
	// The 5.6 call-result VAR off-by-one artifact means a read of slot n±1 can be a
	// read of this result. But once an intervening value-producing op REDEFINES n±1
	// (e.g. a following call whose own result lands there), a later read of that slot
	// is the new value, not this result's alias — so stop absorbing it. Without this,
	// two adjacent unused-result calls collapse to one (the second is dropped).
	adjAlive := map[int]bool{res.Num - 1: true, res.Num + 1: true}
	reads := func(o opline.Operand) bool {
		if o.T != res.T {
			return false
		}
		if o.Num == res.Num {
			return true
		}
		return res.T == "VAR" && adjAlive[o.Num]
	}
	for k := i + 1; k < len(ev.ops); k++ {
		o := ev.ops[k]
		if reads(o.Op1) || reads(o.Op2) {
			return true
		}
		// A later write to the same slot ends this definition's live range; a write
		// to an adjacent slot severs the off-by-one alias for that slot.
		if o.Res.T == res.T && isValueProducing(o.Op) {
			if o.Res.Num == res.Num {
				return false
			}
			delete(adjAlive, o.Res.Num)
		}
	}
	return false
}

func isValueProducing(op string) bool {
	// opcodes that (re)define their result slot with a fresh value
	switch op {
	case "ZEND_OP_DATA", "ZEND_FREE", "ZEND_FE_FREE", "ZEND_SWITCH_FREE":
		return false
	}
	return true
}

// isCallOp reports the DO_FCALL family (a call whose result lands in a result slot).
func isCallOp(op string) bool {
	switch op {
	case "ZEND_DO_FCALL", "ZEND_DO_FCALL_BY_NAME", "ZEND_DO_UCALL", "ZEND_DO_ICALL":
		return true
	}
	return false
}

// linkDiscardedProducers repairs ionCube's result-slot renumbering that surfaces as
// `$x = $_vNN`: a value-producing op whose own result slot is then never read, followed
// immediately by an assignment that reads a DIFFERENT, phantom (never earlier-produced)
// slot as its value. The producer is retargeted to that phantom slot, so its computed
// value flows into the single consuming assignment instead of being dropped while the
// assignment references an un-produced slot. This is the general single-use-temp
// inlining; it is the dominant source of `$_vNN` leaks on Zend 2.6 (PHP 5.x). The exact
// 7.x call path owned by renumberedCallSink is left untouched.
func (ev *evaluator) linkDiscardedProducers() {
	n := len(ev.ops)
	for i := 0; i+1 < n; i++ {
		op := ev.ops[i]
		res := op.Res
		if (res.T != "TMP" && res.T != "VAR") || !isValueProducing(op.Op) {
			continue
		}
		// Leave the 7.x call renumbering to renumberedCallSink (unchanged behaviour).
		if !ev.zend56 && isCallOp(op.Op) {
			continue
		}
		if ev.resultConsumed(i) {
			continue // the producer's own slot is read: not a renumber victim
		}
		if !ev.assignReadsOp2AsValue(i + 1) {
			continue
		}
		sink := ev.ops[i+1].Op2
		if sink.T != res.T || sink.Num == res.Num {
			continue
		}
		// The sink must be a STRICT single-use phantom: its only appearance in the whole
		// op_array is this one consuming read. A slot that appears more than once may be a
		// live value or — crucially — an lvalue (a foreach value/iterator, an assignment
		// target). Retargeting into it, or into a slot the 5.x ±1 adjacent-slot fallback
		// aliases to one, lands a producer expression in a write context
		// (`foreach ($a as f())`). Requiring a single appearance rules all of that out.
		if ev.slotAppearances(sink) != 1 {
			continue
		}
		ev.ops[i].Res.Num = sink.Num
	}
}

// slotAppearances counts every op1/op2/result use of a TMP/VAR slot across the op_array.
func (ev *evaluator) slotAppearances(s opline.Operand) int {
	c := 0
	hit := func(o opline.Operand) {
		if o.T == s.T && o.Num == s.Num {
			c++
		}
	}
	for k := range ev.ops {
		hit(ev.ops[k].Op1)
		hit(ev.ops[k].Op2)
		hit(ev.ops[k].Res)
	}
	return c
}

// renumberedCallSink recognises ionCube's call-result slot renumbering on the
// 7.x/8.x opcode path. The production encoder (observed on a PHP 7.4 corpus)
// can report a DO_FCALL's result in a VAR/TMP slot whose number disagrees with the
// slot the immediately-following ZEND_ASSIGN reads — a constant +15 offset across
// every call->assign in those files (`V113 = call(); $x = V128;`). The call's own
// result slot is then never read (so resultConsumed sees it as discarded) and the
// assignment references a slot no opline ever produced, which renders as the
// unset synthetic temp `$x = $_vNN`. The trial encoder does not apply this
// renumbering, so it cannot be reproduced from a self-encoded fixture; the guard
// is proven instead by an opline golden (renumber_reveal) and re-validated against
// the real corpora.
//
// When the op at i is a call whose next op is a ZEND_ASSIGN reading a VAR/TMP that
// is (a) a different slot than the call's own result and (b) never produced by any
// earlier opline (a genuine phantom, not a real earlier value the call is merely
// discarding), that assignment is consuming the renumbered call result: return the
// sink operand so the caller folds the call expression into it.
//
// Guarded to the 7.x/8.x path (!zend56); the phantom precondition additionally
// keeps every file WITHOUT this exact shape byte-identical — including the 7.2/7.4
// non-namespaced target (no such sites) and, together with the zend56 guard, all 5.x
// corpora whose own `$_vNN` residue is a different (off-by-one) artifact.
func (ev *evaluator) renumberedCallSink(i int) (opline.Operand, bool) {
	if ev.zend56 {
		return opline.Operand{}, false
	}
	res := ev.ops[i].Res
	if res.T != "TMP" && res.T != "VAR" {
		return opline.Operand{}, false
	}
	if i+1 >= len(ev.ops) || !ev.assignReadsOp2AsValue(i+1) {
		return opline.Operand{}, false
	}
	sink := ev.ops[i+1].Op2
	if sink.T != res.T || sink.Num == res.Num {
		return opline.Operand{}, false
	}
	// The sink must be a phantom: nothing before the assignment produces it. A real
	// earlier value there means the call result is genuinely discarded — leave it.
	for k := 0; k <= i; k++ {
		o := ev.ops[k]
		if o.Res.T == sink.T && o.Res.Num == sink.Num && isValueProducing(o.Op) {
			return opline.Operand{}, false
		}
	}
	return sink, true
}

// assignReadsOp2AsValue reports whether the opline at j is an assignment that reads
// its op2 as the plain right-hand-side value — the position a renumbered call
// result flows into. Plain `$x = <rhs>` (ZEND_ASSIGN) and simple-variable compound
// assigns `$x .= <rhs>` / `$x += <rhs>` (ZEND_ASSIGN_OP and the dedicated
// ZEND_ASSIGN_CONCAT/arith family) qualify. A compound assign whose target is an
// array element or property carries its RHS in a trailing ZEND_OP_DATA and uses op2
// for the key/UNUSED instead, so it is excluded to avoid hijacking a non-value op2.
func (ev *evaluator) assignReadsOp2AsValue(j int) bool {
	switch ev.ops[j].Op {
	case "ZEND_ASSIGN", "ZEND_ASSIGN_OP":
		return true
	case "ZEND_ASSIGN_CONCAT", "ZEND_ASSIGN_ADD", "ZEND_ASSIGN_SUB", "ZEND_ASSIGN_MUL",
		"ZEND_ASSIGN_DIV", "ZEND_ASSIGN_MOD", "ZEND_ASSIGN_POW", "ZEND_ASSIGN_SL",
		"ZEND_ASSIGN_SR", "ZEND_ASSIGN_BW_OR", "ZEND_ASSIGN_BW_AND", "ZEND_ASSIGN_BW_XOR":
		return j+1 >= len(ev.ops) || ev.ops[j+1].Op != "ZEND_OP_DATA"
	}
	return false
}

// foldPure evaluates a value-producing opline into its result temp. Returns true
// if it recognized the opcode.
// binaryOp folds a pure two-operand opcode to `a SYM b` at a fixed precedence.
// left selects the left-associative builder (binaryL, used for . + - * / % so a
// chain of same-precedence ops does not over-parenthesise the left side).
type binaryOp struct {
	sym  string
	prec int
	left bool
}

// binaryOps is the single source for every opcode that lifts straight to an
// infix expression, replacing ~20 identical `ev.store(op.Res, binary(...))` cases.
var binaryOps = map[string]binaryOp{
	"ZEND_CONCAT":              {".", precAdd, true},
	"ZEND_FAST_CONCAT":         {".", precAdd, true},
	"ZEND_ADD":                 {"+", precAdd, true},
	"ZEND_SUB":                 {"-", precAdd, true},
	"ZEND_MUL":                 {"*", precMul, true},
	"ZEND_DIV":                 {"/", precMul, true},
	"ZEND_MOD":                 {"%", precMul, true},
	"ZEND_POW":                 {"**", precPow, false},
	"ZEND_SL":                  {"<<", precShift, false},
	"ZEND_SR":                  {">>", precShift, false},
	"ZEND_BW_AND":              {"&", precBitAnd, false},
	"ZEND_BW_OR":               {"|", precBitOr, false},
	"ZEND_BW_XOR":              {"^", precBitXor, false},
	"ZEND_BOOL_XOR":            {"xor", precLowest + 1, false},
	"ZEND_IS_EQUAL":            {"==", precEq, false},
	"ZEND_IS_NOT_EQUAL":        {"!=", precEq, false},
	"ZEND_IS_IDENTICAL":        {"===", precEq, false},
	"ZEND_IS_NOT_IDENTICAL":    {"!==", precEq, false},
	"ZEND_IS_SMALLER":          {"<", precCmp, false},
	"ZEND_IS_SMALLER_OR_EQUAL": {"<=", precCmp, false},
	"ZEND_SPACESHIP":           {"<=>", precCmp, false},
	// switch/match subject comparison lifts to the same `==` the source wrote.
	"ZEND_CASE":        {"==", precEq, false},
	"ZEND_CASE_STRICT": {"==", precEq, false},
}

func (ev *evaluator) foldPure(i int) bool {
	op := ev.ops[i]
	if bo, ok := binaryOps[op.Op]; ok {
		build := binary
		if bo.left {
			build = binaryL
		}
		ev.store(op.Res, build(ev.operandE(op.Op1), bo.sym, ev.operandE(op.Op2), bo.prec))
		return true
	}
	switch op.Op {
	case "ZEND_INIT_FCALL", "ZEND_INIT_FCALL_BY_NAME":
		if lit, bad := badFuncNameLiteral(op.Op2); bad {
			// Garbled/inferred function name — not a bareword identifier. Route through
			// call_user_func so the statement stays php -l-clean on 5.6 (no direct
			// string-literal call) while preserving the recovered name.
			ev.push(&pendingCall{callee: "call_user_func", args: []E{atom(lit)}, byName: true})
			return true
		}
		ev.push(&pendingCall{callee: ev.calleeName(op.Op2), byName: true})
		return true
	case "ZEND_INIT_NS_FCALL_BY_NAME":
		// PHP emits NS_FCALL_BY_NAME for an UNqualified call written inside a
		// namespace (`count($x)`). It stores the namespace-probe name
		// (`Ns\Sub\count`) and resolves it namespace-first with a global-function
		// fallback. The source wrote the bare name, and emitting the full FQN
		// calls a function that (for the common builtin-fallback case) does not
		// exist, so render only the final segment as the source did.
		if lit, bad := badFuncNameLiteral(op.Op2); bad {
			ev.push(&pendingCall{callee: "call_user_func", args: []E{atom(lit)}, byName: true})
			return true
		}
		ev.push(&pendingCall{callee: unqualifyName(ev.calleeName(op.Op2)), byName: true})
		return true
	case "ZEND_INIT_METHOD_CALL":
		obj := ev.operandE(op.Op1)
		recv := obj.wrap(precAtom)
		if op.Op1.T == "UNUSED" {
			recv = "$this"
		}
		ev.push(&pendingCall{callee: recv + ev.arrowOf(op.Op1) + ev.memberCallName(op.Op2)})
		return true
	case "ZEND_INIT_STATIC_METHOD_CALL":
		// className decodes a self/parent/static class ref from an UNUSED op1's
		// FETCH_CLASS type (num low nibble), a literal CONST name, or a dynamic
		// class expression — so `static::m()` is not flattened to `self::m()`.
		ev.push(&pendingCall{callee: ev.className(op.Op1) + "::" + ev.memberCallName(op.Op2)})
		return true
	case "ZEND_INIT_DYNAMIC_CALL", "ZEND_INIT_USER_CALL":
		ev.push(&pendingCall{callee: ev.operandE(op.Op2).wrap(precAtom)})
		return true
	case "ZEND_NEW":
		cls := ev.className(op.Op1)
		pc := &pendingCall{callee: "new " + cls, isNew: true}
		if op.Res.T == "TMP" || op.Res.T == "VAR" {
			pc.resSlot = slotKey(op.Res.T, op.Res.Num)
		}
		ev.push(pc)
		return true
	case "ZEND_SEND_VAL", "ZEND_SEND_VAL_EX", "ZEND_SEND_VAR", "ZEND_SEND_VAR_EX",
		"ZEND_SEND_VAR_NO_REF", "ZEND_SEND_VAR_NO_REF_EX", "ZEND_SEND_REF",
		"ZEND_SEND_FUNC_ARG", "ZEND_SEND_USER":
		if pc := ev.sendTarget(); pc != nil {
			arg := ev.operandE(op.Op1)
			// Named argument (8.0+): the parameter name rides in op2 as a CONST
			// string (`box(label: 'A')`). Positional sends leave op2 UNUSED.
			if name, ok := op.Op2.Val.(string); ok && op.Op2.T == "CONST" && name != "" {
				arg = atom(name + ": " + arg.wrap(precLowest+1))
			}
			pc.args = append(pc.args, arg)
		}
		return true
	case "ZEND_SEND_UNPACK":
		if pc := ev.sendTarget(); pc != nil {
			pc.args = append(pc.args, atom("..."+ev.operandE(op.Op1).wrap(precUnary)))
		}
		return true
	case "ZEND_SEND_ARRAY":
		return true
	case "ZEND_CALLABLE_CONVERT":
		// First-class callable syntax `f(...)` / `$o->m(...)` / `C::m(...)`: the
		// preceding INIT_* pushed the target, and CALLABLE_CONVERT turns it into a
		// Closure without invoking it (no SEND/DO_FCALL follows).
		if pc := ev.pop(); pc != nil {
			ev.store(op.Res, atom(pc.callee+"(...)"))
		} else {
			ev.store(op.Res, atom("/* decompiler: callable-convert without init */"))
		}
		return true
	case "ZEND_DO_FCALL", "ZEND_DO_FCALL_BY_NAME", "ZEND_DO_UCALL", "ZEND_DO_ICALL":
		e, wasNew, ok := ev.finishCall(op)
		if ok && !wasNew {
			ev.store(op.Res, e)
		}
		return true
	case "ZEND_QM_ASSIGN", "ZEND_COALESCE":
		ev.store(op.Res, ev.operandE(op.Op1))
		return true
	case "ZEND_BOOL_NOT":
		ev.store(op.Res, unary("!", ev.operandE(op.Op1)))
		return true
	case "ZEND_BOOL":
		ev.store(op.Res, ev.operandE(op.Op1))
		return true
	case "ZEND_BW_NOT":
		ev.store(op.Res, unary("~", ev.operandE(op.Op1)))
		return true
	case "ZEND_CAST":
		ev.store(op.Res, unary(castType(op.Ext), ev.operandE(op.Op1)))
		return true
	case "ZEND_FETCH_STATIC_PROP_R", "ZEND_FETCH_STATIC_PROP_W", "ZEND_FETCH_STATIC_PROP_IS",
		"ZEND_FETCH_STATIC_PROP_RW", "ZEND_FETCH_STATIC_PROP_FUNC_ARG", "ZEND_FETCH_STATIC_PROP_UNSET":
		ev.store(op.Res, atom(ev.className(op.Op2)+"::$"+ev.constStr(op.Op1)))
		return true
	case "ZEND_FETCH_DIM_R", "ZEND_FETCH_DIM_W", "ZEND_FETCH_DIM_IS", "ZEND_FETCH_DIM_RW",
		"ZEND_FETCH_DIM_FUNC_ARG", "ZEND_FETCH_DIM_UNSET", "ZEND_FETCH_LIST_R", "ZEND_FETCH_LIST_W":
		key := ""
		if op.Op2.T != "UNUSED" {
			key = ev.operandE(op.Op2).wrap(precLowest + 1)
		}
		ev.store(op.Res, atom(ev.operandE(op.Op1).wrap(precAtom)+"["+key+"]"))
		return true
	case "ZEND_FETCH_OBJ_R", "ZEND_FETCH_OBJ_W", "ZEND_FETCH_OBJ_IS", "ZEND_FETCH_OBJ_RW",
		"ZEND_FETCH_OBJ_FUNC_ARG", "ZEND_FETCH_OBJ_UNSET":
		recv := ev.operandE(op.Op1)
		base := recv.wrap(precAtom)
		if op.Op1.T == "UNUSED" {
			base = "$this"
		}
		ev.store(op.Res, atom(base+ev.arrowOf(op.Op1)+ev.propName(op.Op2)))
		return true
	case "ZEND_FETCH_R", "ZEND_FETCH_W", "ZEND_FETCH_IS", "ZEND_FETCH_RW",
		"ZEND_FETCH_FUNC_ARG", "ZEND_FETCH_UNSET", "ZEND_FETCH_GLOBAL", "ZEND_FETCH_LOCAL":
		// Zend 2.6 (5.x) routes a static-property access through a plain ZEND_FETCH_*
		// whose extended_value top nibble is ZEND_FETCH_STATIC_MEMBER (0x30000000):
		// op1 is the property name, op2 the class (a FETCH_CLASS temp or a class-name
		// CONST). 7.x has a dedicated ZEND_FETCH_STATIC_PROP_* opcode, so guard on the
		// fetch-type bits to keep 7.x byte-identical.
		if (op.Ext & zendFetchTypeMask) == zendFetchStaticMember {
			ev.store(op.Res, atom(ev.className(op.Op2)+"::$"+ev.constStr(op.Op1)))
			return true
		}
		name := ev.constStr(op.Op1)
		if isIdent(name) {
			ev.store(op.Res, atom("$"+name))
		} else {
			// encrypted / non-name variable — render a php -l-clean variable-variable
			// (`${expr}`) instead of `$<comment>`.
			ev.store(op.Res, atom("${"+ev.operandE(op.Op1).wrap(precLowest+1)+"}"))
		}
		return true
	case "ZEND_FETCH_THIS":
		ev.store(op.Res, atom("$this"))
		return true
	case "ZEND_FETCH_CLASS_CONSTANT":
		// op1 is the class operand (a CONST/CV class name, or UNUSED carrying the
		// self/parent/static fetch type in num — 513/514/515), op2 the constant
		// name. Always render Class::CONST via className(op1); an UNUSED op1 must
		// not degrade to a bare, undefined-at-runtime constant name.
		ev.store(op.Res, atom(ev.className(op.Op1)+"::"+ev.classConstName(op.Op2)))
		return true
	case "ZEND_FETCH_CONSTANT":
		// Global constants and — on Zend 2.6 (5.6), which has no dedicated
		// ZEND_FETCH_CLASS_CONSTANT — class constants both arrive here. A CONST/CV
		// op1 is the class name of a Class::CONST; otherwise op2 is a bare global
		// constant. (A self:: class-const on 5.6 has op1 = the FETCH_CLASS VAR, so
		// it renders bare — behaviorally correct for a non-overridden constant.)
		if op.Op1.T == "CONST" || op.Op1.T == "CV" {
			ev.store(op.Res, atom(ev.className(op.Op1)+"::"+ev.classConstName(op.Op2)))
		} else {
			// A bare global constant. An unqualified constant read inside a
			// namespace (`ENT_QUOTES`) is stored with the namespace-probe name
			// (`Ns\ENT_QUOTES`) and resolved namespace-first with a global
			// fallback, so unqualify to the final segment the source wrote — the
			// full FQN is undefined at runtime and the backslashes fail isIdent,
			// which would otherwise drop a fully-recoverable name to a placeholder.
			name := unqualifyName(ev.constStr(op.Op2))
			if !isIdent(name) {
				name = icUnresolvedConst
			}
			ev.store(op.Res, atom(name))
		}
		return true
	case "ZEND_ISSET_ISEMPTY_VAR", "ZEND_ISSET_ISEMPTY_CV":
		ev.store(op.Res, atom(issetKind(op.Ext)+"("+ev.operandE(op.Op1).wrap(precLowest+1)+")"))
		return true
	case "ZEND_ISSET_ISEMPTY_DIM_OBJ":
		ev.store(op.Res, atom(issetKind(op.Ext)+"("+ev.operandE(op.Op1).wrap(precAtom)+"["+ev.operandE(op.Op2).wrap(precLowest+1)+"])"))
		return true
	case "ZEND_ISSET_ISEMPTY_PROP_OBJ":
		base := ev.operandE(op.Op1)
		b := base.wrap(precAtom)
		if op.Op1.T == "UNUSED" {
			b = "$this"
		}
		ev.store(op.Res, atom(issetKind(op.Ext)+"("+b+"->"+ev.propName(op.Op2)+")"))
		return true
	case "ZEND_ISSET_ISEMPTY_STATIC_PROP":
		ev.store(op.Res, atom(issetKind(op.Ext)+"("+ev.className(op.Op2)+"::$"+ev.constStr(op.Op1)+")"))
		return true
	case "ZEND_INSTANCEOF":
		ev.store(op.Res, binary(ev.operandE(op.Op1), "instanceof", atom(ev.className(op.Op2)), precInstanceof))
		return true
	case "ZEND_TYPE_CHECK":
		ev.store(op.Res, atom(typeCheckFn(op.Ext)+"("+ev.operandE(op.Op1).wrap(precLowest+1)+")"))
		return true
	case "ZEND_DEFINED":
		ev.store(op.Res, atom("defined("+phpQuote(ev.constStr(op.Op1))+")"))
		return true
	case "ZEND_STRLEN":
		ev.store(op.Res, atom("strlen("+ev.operandE(op.Op1).wrap(precLowest+1)+")"))
		return true
	case "ZEND_COUNT":
		ev.store(op.Res, atom("count("+ev.operandE(op.Op1).wrap(precLowest+1)+")"))
		return true
	case "ZEND_IN_ARRAY":
		ev.store(op.Res, atom("in_array("+ev.operandE(op.Op1).wrap(precLowest+1)+", "+ev.operandE(op.Op2).wrap(precLowest+1)+")"))
		return true
	case "ZEND_INIT_ARRAY":
		ev.beginArray(op)
		return true
	case "ZEND_ADD_ARRAY_ELEMENT":
		ev.addArrayElement(op)
		return true
	case "ZEND_ADD_ARRAY_UNPACK":
		ev.addArrayUnpack(op)
		return true
	case "ZEND_ROPE_INIT":
		ev.ropes[slotKey(op.Res.T, op.Res.Num)] = []E{ev.operandE(op.Op2)}
		return true
	case "ZEND_ROPE_ADD":
		k := slotKey(op.Op1.T, op.Op1.Num)
		ev.ropes[k] = append(ev.ropes[k], ev.operandE(op.Op2))
		ev.store(op.Res, ev.buildRope(ev.ropes[k]))
		return true
	case "ZEND_ROPE_END":
		k := slotKey(op.Op1.T, op.Op1.Num)
		rope := append(ev.ropes[k], ev.operandE(op.Op2))
		ev.store(op.Res, ev.buildRope(rope))
		return true
	case "ZEND_ADD_STRING", "ZEND_ADD_VAR", "ZEND_ADD_CHAR":
		// 5.6 string builder: result accumulates op1 . op2
		var left E
		if op.Op1.T == "UNUSED" {
			left = atom("")
		} else {
			left = ev.operandE(op.Op1)
		}
		right := ev.operandE(op.Op2)
		// ZEND_ADD_CHAR carries op2 as the character's integer code (Zend 2.6 stores
		// a single-char rope segment as an IS_LONG), so render it as that one-char
		// string literal rather than the bare number.
		if op.Op == "ZEND_ADD_CHAR" && op.Op2.T == "CONST" && !op.Op2.Encrypted {
			if b, ok := charByteOf(op.Op2.Val); ok {
				right = atom(phpQuote(string([]byte{b})))
			}
		}
		var combined E
		if left.Text == "" {
			combined = right
		} else {
			combined = binaryL(left, ".", right, precAdd)
		}
		ev.store(op.Res, combined)
		return true
	case "ZEND_QM_ASSIGN_LONG":
		ev.store(op.Res, ev.operandE(op.Op1))
		return true
	case "ZEND_GET_CLASS":
		ev.store(op.Res, atom("get_class("+ev.operandE(op.Op1).wrap(precLowest+1)+")"))
		return true
	case "ZEND_CLONE":
		ev.store(op.Res, atom("clone "+ev.operandE(op.Op1).wrap(precUnary)))
		return true
	case "ZEND_FETCH_CLASS":
		// resolves a class reference into a temp for a following NEW / static call.
		var name string
		if kw, ok := fetchClassKeyword(int(op.Ext & 0x0f)); ok {
			name = kw
		} else if op.Op2.T == "CONST" {
			// DEFAULT/AUTO with a named class in op2. Qualify it for a namespaced file
			// (Zend 2.6 / 5.6 resolves `new X` / instanceof through this FETCH_CLASS temp
			// rather than a CONST op1, so the reference must qualify here too, see classRef).
			name = ev.classRef(ev.constStr(op.Op2))
		} else if op.Op2.T != "UNUSED" {
			name = ev.operandE(op.Op2).Text
		} else {
			// No keyword nibble and no operand: `new static()` on Zend 2.6/5.6 reveals
			// with the type nibble dropped, so default to static:: to keep late static
			// binding (rendering self:: here would break `new static()` in a subclass).
			name = "static"
		}
		ev.store(op.Res, atom(name))
		return true
	case "ZEND_DECLARE_LAMBDA_FUNCTION", "ZEND_DECLARE_ANON_CLASS":
		// The closure/arrow body is revealed as its own Method, matched here by
		// (file,line). Inline it with its captured vars (from the following
		// BIND_LEXICAL ops). Falls back to a placeholder if the body is unavailable.
		if cm := ev.lookupClosure(op); cm != nil && op.Op == "ZEND_DECLARE_LAMBDA_FUNCTION" {
			ev.store(op.Res, ev.renderClosureExpr(cm, ev.lexicalBinds(i)))
			return true
		}
		ev.store(op.Res, atom("function () { /* decompiler: closure body dumped as a separate method */ }"))
		return true
	case "ZEND_JMPZ_EX":
		ev.foldShortCircuit(i, "&&")
		return true
	case "ZEND_JMPNZ_EX":
		ev.foldShortCircuit(i, "||")
		return true
	}
	return false
}

// foldShortCircuit renders `a && b` / `a || b` from JMPZ_EX/JMPNZ_EX + tail. The
// jump target is ionCube-corrupted, so the right-hand region is bounded by where
// the result slot is finally CONSUMED (shortCircuitJoin), not by the target.
func (ev *evaluator) foldShortCircuit(i int, op string) {
	cur := ev.ops[i]
	a := ev.operandE(cur.Op1)
	slot := slotKey(cur.Res.T, cur.Res.Num)
	join := ev.shortCircuitJoin(i, cur.Res)
	if join > i+1 {
		for k := i + 1; k < join; k++ {
			ev.foldPure(k)
		}
		if b, ok := ev.tmp[slot]; ok {
			prec := precAnd
			if op == "||" {
				prec = precOr
			}
			ev.store(cur.Res, binary(a, op, b, prec))
			ev.foldedUntil[i] = join
			return
		}
	}
	ev.store(cur.Res, a)
}

// shortCircuitJoin finds where a JMPZ_EX/JMPNZ_EX chain's boolean result (in slot
// res) is finally consumed — the first later opline that reads res and is NOT
// another chain link writing res. Intermediate `_EX` links (a || b || c) are
// skipped so the whole chain folds; the corrupted jump target is never used.
func (ev *evaluator) shortCircuitJoin(i int, res opline.Operand) int {
	for k := i + 1; k < len(ev.ops); k++ {
		o := ev.ops[k]
		if (o.Op == "ZEND_JMPZ_EX" || o.Op == "ZEND_JMPNZ_EX") && sameSlot(o.Res, res) {
			continue // chain link; keep scanning for the final consumer
		}
		if sameSlot(o.Op1, res) || sameSlot(o.Op2, res) {
			return k
		}
	}
	return -1
}

// ---- helpers on the evaluator ----

func (ev *evaluator) push(pc *pendingCall) { ev.stack = append(ev.stack, pc) }
func (ev *evaluator) top() *pendingCall {
	if len(ev.stack) == 0 {
		return nil
	}
	return ev.stack[len(ev.stack)-1]
}
func (ev *evaluator) pop() *pendingCall {
	if len(ev.stack) == 0 {
		return nil
	}
	pc := ev.stack[len(ev.stack)-1]
	ev.stack = ev.stack[:len(ev.stack)-1]
	return pc
}

// sendTarget returns the pending call an argument SEND belongs to. On Zend 2.6 a
// direct call to a known function is `SEND_*…; ZEND_DO_FCALL <name>` with NO
// preceding INIT_FCALL, so the first SEND finds an empty stack — synthesize a
// nameless pending call (finishCall fills its callee from the DO_FCALL's op1).
// 7.x always INITs first, so top() is never nil there and this path stays dormant.
func (ev *evaluator) sendTarget() *pendingCall {
	if pc := ev.top(); pc != nil {
		return pc
	}
	if ev.zend56 {
		pc := &pendingCall{}
		ev.push(pc)
		return pc
	}
	return nil
}

// directCallName reads the callee of a Zend 2.6 bare ZEND_DO_FCALL from its op1: a
// readable CONST identifier is a named function; a CV/VAR/TMP is a dynamic callee
// (`$fn()`). An encrypted/unreadable CONST returns ok=false so the caller keeps the
// safe "unmatched" rendering rather than emitting a fatal `null()` call.
func (ev *evaluator) directCallName(o opline.Operand) (string, bool) {
	if o.T == "CONST" && !o.Encrypted {
		if s, ok := o.Val.(string); ok && isIdent(s) {
			return s, true
		}
	}
	if o.T == "CV" || o.T == "VAR" || o.T == "TMP" {
		return ev.operandE(o).wrap(precAtom), true
	}
	return "", false
}

func (ev *evaluator) finishCall(op opline.Op) (e E, wasNew, ok bool) {
	pc := ev.pop()
	if pc == nil {
		// Zend 2.6 zero-argument direct call: `ZEND_DO_FCALL <name>` with no SENDs
		// and thus no synthesized pending call — build it straight from op1, but only
		// when op1 names a readable callee. An ENCRYPTED name (e.g. {main}'s own
		// `echo probe()`, whose literal never decrypts) must NOT become `null()`: that
		// is a runtime-fatal call and would kill the whole program before the
		// name-independent runner can fall back to the reconstructed entry point.
		if ev.zend56 {
			if name, ok := ev.directCallName(op.Op1); ok {
				return call(name, nil), false, true
			}
		}
		return atom("/* decompiler: unmatched call */"), false, false
	}
	// Zend 2.6 direct call: ZEND_DO_FCALL carries the callee name in op1 (no INIT),
	// so a pending call synthesized by the first SEND has no callee yet. If the name
	// is unrecoverable (encrypted), keep the conservative "unmatched" rendering rather
	// than emitting a callee-less `(args)` expression.
	if pc.callee == "" {
		name, ok := ev.directCallName(op.Op1)
		if !ok {
			return atom("/* decompiler: unmatched call */"), false, false
		}
		pc.callee = name
	}
	if pc.isNew {
		ne := call(pc.callee, pc.args)
		if pc.resSlot != "" {
			ev.tmp[pc.resSlot] = ne
		}
		return ne, true, true
	}
	return call(pc.callee, pc.args), false, true
}

func (ev *evaluator) store(o opline.Operand, e E) {
	if o.T == "TMP" || o.T == "VAR" {
		ev.tmp[slotKey(o.T, o.Num)] = e
	} else if o.T == "CV" {
		ev.tmp["CV:"+o.Var] = e // remembered but $name is always renderable
	}
}

// lval renders an assignable target.
func (ev *evaluator) lval(o opline.Operand) string {
	switch o.T {
	case "CV":
		return "$" + o.Var
	case "TMP", "VAR":
		if e, ok := ev.tmp[slotKey(o.T, o.Num)]; ok {
			return e.Text
		}
	}
	return ev.operandE(o).Text
}

// objLval renders an object-property lvalue `$obj->prop` from an inc/dec (or
// similar) op whose op1 is the object (UNUSED => $this) and op2 the property.
func (ev *evaluator) objLval(obj, prop opline.Operand) string {
	return ev.objBase(obj) + "->" + ev.propName(prop)
}

// opData reads the value carried by the OP_DATA following an ASSIGN_DIM/OBJ.
func (ev *evaluator) opData() string {
	if ev.cursor+1 < len(ev.ops) && ev.ops[ev.cursor+1].Op == "ZEND_OP_DATA" {
		return ev.operandE(ev.ops[ev.cursor+1].Op1).wrap(precLowest + 1)
	}
	return "null /* decompiler: OP_DATA not found */"
}

// compoundAssignOp maps each dedicated compound-assign opcode (5.6 has these as
// distinct opcodes, unlike 7.x's single ZEND_ASSIGN_OP+ext) to its `X op= v`
// operator.
var compoundAssignOp = map[string]string{
	"ZEND_ASSIGN_CONCAT": ".", "ZEND_ASSIGN_ADD": "+", "ZEND_ASSIGN_SUB": "-",
	"ZEND_ASSIGN_MUL": "*", "ZEND_ASSIGN_DIV": "/", "ZEND_ASSIGN_MOD": "%",
	"ZEND_ASSIGN_POW": "**", "ZEND_ASSIGN_SL": "<<", "ZEND_ASSIGN_SR": ">>",
	"ZEND_ASSIGN_BW_OR": "|", "ZEND_ASSIGN_BW_AND": "&", "ZEND_ASSIGN_BW_XOR": "^",
}

// zendAssignObj / zendAssignDim are the ZEND_ASSIGN_OBJ / ZEND_ASSIGN_DIM opcode
// numbers, reused by 5.6 as the extended_value of a compound-assign to flag that
// the target is an object property or an array element.
const (
	zendAssignObj = 136
	zendAssignDim = 147
)

// compoundAssign renders `X op= v`. On 5.6 a compound assign to an object property
// (`$o->p += v`) or array element (`$a[$k] += v`) carries the container in op1,
// the property/key in op2 and the value in a trailing OP_DATA, with ext set to
// ZEND_ASSIGN_OBJ / ZEND_ASSIGN_DIM. The direct-variable form (`$x += v`) has the
// value inline in op2 and no OP_DATA. Without this, the object/array form rendered
// an empty left-hand side (` += v`), a parse error.
func (ev *evaluator) compoundAssign(op opline.Op, opStr string) string {
	if ev.cursor+1 < len(ev.ops) && ev.ops[ev.cursor+1].Op == "ZEND_OP_DATA" {
		val := ev.opData()
		if op.Ext == zendAssignDim {
			key := ""
			if op.Op2.T != "UNUSED" {
				key = ev.operandE(op.Op2).wrap(precLowest + 1)
			}
			return ev.operandE(op.Op1).wrap(precAtom) + "[" + key + "] " + opStr + "= " + val + ";"
		}
		return ev.objBase(op.Op1) + "->" + ev.propName(op.Op2) + " " + opStr + "= " + val + ";"
	}
	return ev.lval(op.Op1) + " " + opStr + "= " + ev.operandE(op.Op2).wrap(precLowest+1) + ";"
}

func (ev *evaluator) constStr(o opline.Operand) string {
	if o.T == "CONST" {
		if s, ok := o.Val.(string); ok {
			return s
		}
	}
	if o.T == "CV" {
		return o.Var
	}
	return ev.operandE(o).Text
}

// calleeName renders a call target from an INIT_FCALL* op2: a CONST string is a
// plain function name; a CV/VAR/TMP is a dynamic callee (`$fn(...)`), rendered
// with its variable sigil so `$add10(5)` does not degrade to `add10(5)`.
func (ev *evaluator) calleeName(o opline.Operand) string {
	if o.T == "CONST" {
		if s, ok := o.Val.(string); ok {
			return s
		}
	}
	if o.T == "CV" || o.T == "VAR" || o.T == "TMP" {
		return ev.operandE(o).wrap(precAtom)
	}
	return ev.constStr(o)
}

// icBadNameComment tags a recovered name that could not be emitted as a bareword
// identifier, so a human reader sees the substitution was defensive, not original.
const icBadNameComment = "/*IC_BADNAME*/ "

// icBadName is the lint-safe placeholder string for a member name that could not be
// recovered at all (an encrypted/non-string operand in identifier position).
const icBadName = "IC_UNRESOLVED_NAME"

// memberCallName renders the NAME portion of a `->`/`::` method call (the text that
// follows the arrow or `::`) so it is ALWAYS php -l-clean, even when the recovered
// name is not a valid identifier. A valid identifier is emitted bare; a recovered
// non-identifier string (garbled/inferred, e.g. "1Forma") or a dynamic value uses
// the dynamic member form `{...}`, which PHP accepts for ANY string or expression
// on every version; a truly unrecoverable name degrades to a guarded placeholder.
// This never emits a bareword that is not a valid identifier — the sole cause of
// the 5.x identifier-position lint failures.
func (ev *evaluator) memberCallName(o opline.Operand) string {
	if o.T == "CONST" {
		if s, ok := o.Val.(string); ok {
			if isIdent(s) {
				return s
			}
			return "{" + phpQuote(s) + "}"
		}
		return "{" + icBadNameComment + phpQuote(icBadName) + "}"
	}
	if o.T == "UNUSED" {
		return "{" + icBadNameComment + phpQuote(icBadName) + "}"
	}
	return "{" + ev.operandE(o).wrap(precLowest+1) + "}"
}

// badFuncNameLiteral reports a FUNCTION callee whose recovered name is not a valid
// (optionally namespace-qualified) identifier — a garbled/inferred literal. PHP 5.6
// cannot call a string literal directly (`'x-y'(...)` is 7.0+ syntax), so the caller
// routes such a name through `call_user_func('name', ...args)`, which is lint-clean
// on every version and preserves the recovered text as a string. Returns the guarded
// literal to use as call_user_func's first argument.
func badFuncNameLiteral(o opline.Operand) (string, bool) {
	if o.T != "CONST" {
		return "", false
	}
	s, ok := o.Val.(string)
	if !ok {
		return "", false
	}
	if isIdent(s) || isQualifiedIdent(s) {
		return "", false
	}
	return icBadNameComment + phpQuote(s), true
}

// unqualifyName reduces a namespace-qualified probe name to the bare name the
// source actually wrote. PHP stores such a name for an UNqualified function call
// (NS_FCALL_BY_NAME) or an unqualified constant read inside a namespace, both of
// which it resolves namespace-first with a global fallback; the full FQN names a
// symbol that (for the common builtin case) does not exist. A dynamic value
// already rendered with a `$` sigil is passed through untouched.
func unqualifyName(name string) string {
	if name == "" || strings.HasPrefix(name, "$") {
		return name
	}
	if i := strings.LastIndex(name, `\`); i >= 0 {
		return name[i+1:]
	}
	return name
}

// icUnresolvedConst is a php -l-clean placeholder for a class-constant name that
// could not be recovered (5.x encrypts the const-name literal at rest and it never
// flows through an observable call). A class constant cannot be named dynamically,
// so an identifier placeholder is the only lint-safe rendering.
const icUnresolvedConst = "IC_UNRESOLVED_CONST"

// Zend 2.6 (PHP 5.x) FETCH-type bits, stored in a ZEND_FETCH_* op's extended_value
// top nibble. STATIC_MEMBER (0x30000000) marks a `Class::$prop` static-property
// access; 7.x lifted this to its own opcode, so these gate 5.x-only rendering.
const (
	zendFetchTypeMask     = 0xF0000000
	zendFetchStaticMember = 0x30000000
)

// classConstName renders the constant name of a `Class::NAME` access, falling back
// to icUnresolvedConst when the 5.x name literal is encrypted/unreadable.
func (ev *evaluator) classConstName(o opline.Operand) string {
	if o.T == "CONST" {
		if s, ok := o.Val.(string); ok && isIdent(s) {
			return s
		}
	}
	if o.T == "CV" {
		return o.Var
	}
	name := ev.constStr(o)
	if isIdent(name) {
		return name
	}
	return icUnresolvedConst
}

func (ev *evaluator) propName(o opline.Operand) string {
	if o.T == "CONST" {
		if s, ok := o.Val.(string); ok {
			if isIdent(s) {
				return s
			}
			return "{" + phpQuote(s) + "}"
		}
		// non-string CONST (e.g. 5.x encrypted property name, unreadable)
		return "{'/* decompiler: property name unresolved */'}"
	}
	if o.T == "UNUSED" {
		return "{'/* decompiler: property name unresolved */'}"
	}
	return "{" + ev.operandE(o).wrap(precLowest+1) + "}"
}

// objBase renders the object of an -> access; UNUSED means $this.
func (ev *evaluator) objBase(o opline.Operand) string {
	if o.T == "UNUSED" {
		return "$this"
	}
	return ev.operandE(o).wrap(precAtom)
}

// fetchClassKeyword decodes the shared self/parent/static keyword from the low
// nibble of a ZEND_FETCH_CLASS type: 1=self, 2=parent, 3=static (Zend's
// ZEND_FETCH_CLASS_SELF/PARENT/STATIC). This one decode is used by every self/
// parent/static class-ref site (className below and ZEND_FETCH_CLASS in foldPure),
// so the two never disagree on the keyword. ok is false for 0=DEFAULT (a named
// class, not a keyword) or a reveal that dropped the type — the caller then applies
// its OWN fallback, which legitimately differs by context: a static-member access
// (className) degrades to the conservative self::, but a NEW/instanceof class ref
// (FETCH_CLASS) degrades to static::, because `new static()` on Zend 2.6/5.6
// reveals with the type nibble dropped to 0 and must keep late static binding.
func fetchClassKeyword(nibble int) (string, bool) {
	switch nibble {
	case 1:
		return "self", true
	case 2:
		return "parent", true
	case 3:
		return "static", true
	}
	return "", false
}

func (ev *evaluator) className(o opline.Operand) string {
	if o.T == "CONST" {
		if s, ok := o.Val.(string); ok {
			return ev.classRef(s)
		}
	}
	if o.T == "UNUSED" {
		// self/parent/static reveal as an UNUSED class operand carrying the
		// ZEND_FETCH_CLASS_* type in num (low nibble; higher bits are autoload/
		// silent/exception flags). A dropped type (e.g. the 5.6 shim) degrades to the
		// conservative self::, behaviorally correct for a non-overridden static member.
		if kw, ok := fetchClassKeyword(o.Num & 0x0F); ok {
			return kw
		}
		return "self"
	}
	return ev.operandE(o).Text
}

func (ev *evaluator) includeExpr(op opline.Op) string {
	kind := map[uint64]string{1: "include", 2: "include_once", 4: "require", 8: "require_once"}[op.Ext]
	if kind == "" {
		kind = "include"
	}
	if op.Ext == 0 {
		return "eval(" + ev.operandE(op.Op1).wrap(precLowest+1) + ")"
	}
	return kind + " " + ev.operandE(op.Op1).wrap(precLowest+1)
}

func (ev *evaluator) buildRope(parts []E) E {
	if len(parts) == 0 {
		return atom("''")
	}
	acc := parts[0]
	for _, p := range parts[1:] {
		acc = binaryL(acc, ".", p, precAdd)
	}
	return acc
}

// beginArray/addArrayElement fold INIT_ARRAY + ADD_ARRAY_ELEMENT chains.
func (ev *evaluator) beginArray(op opline.Op) {
	slot := slotKey(op.Res.T, op.Res.Num)
	el := ev.arrayElem(op)
	ev.arrays[slot] = []string{}
	if el != "" {
		ev.arrays[slot] = append(ev.arrays[slot], el)
	}
	ev.store(op.Res, atom("["+strings.Join(ev.arrays[slot], ", ")+"]"))
}

func (ev *evaluator) addArrayElement(op opline.Op) {
	slot := slotKey(op.Op1.T, op.Op1.Num)
	if op.Res.T == "TMP" || op.Res.T == "VAR" {
		slot = slotKey(op.Res.T, op.Res.Num)
	}
	el := ev.arrayElem(op)
	ev.arrays[slot] = append(ev.arrays[slot], el)
	ev.store(op.Res, atom("["+strings.Join(ev.arrays[slot], ", ")+"]"))
}

// addArrayUnpack folds ZEND_ADD_ARRAY_UNPACK (a `...$spread` element inside an
// array literal, PHP 7.4+). The array temp is the Res slot shared with the
// enclosing INIT_ARRAY/ADD_ARRAY_ELEMENT chain; op1 is the spread source.
func (ev *evaluator) addArrayUnpack(op opline.Op) {
	slot := slotKey(op.Res.T, op.Res.Num)
	el := "..." + ev.operandE(op.Op1).wrap(precLowest+1)
	ev.arrays[slot] = append(ev.arrays[slot], el)
	ev.store(op.Res, atom("["+strings.Join(ev.arrays[slot], ", ")+"]"))
}

func (ev *evaluator) arrayElem(op opline.Op) string {
	if op.Op1.T == "UNUSED" {
		return ""
	}
	val := ev.operandE(op.Op1).wrap(precLowest + 1)
	if op.Op2.T != "UNUSED" {
		return ev.operandE(op.Op2).wrap(precLowest+1) + " => " + val
	}
	return val
}

// ---- small classifiers ----

func isParamRecv(op string) bool {
	return op == "ZEND_RECV" || op == "ZEND_RECV_INIT" || op == "ZEND_RECV_VARIADIC"
}

// isIgnorable lists opcodes that carry no user-visible statement/expression on
// their own (housekeeping, arg-type checks, silence markers, iterator frees).
// They are intentionally skipped, not counted as unhandled.
func isIgnorable(op string) bool {
	switch op {
	case "ZEND_NOP", "ZEND_EXT_NOP", "ZEND_EXT_STMT", "ZEND_EXT_FCALL_BEGIN",
		"ZEND_EXT_FCALL_END", "ZEND_TICKS", "ZEND_FREE", "ZEND_FE_FREE",
		"ZEND_SWITCH_FREE", "ZEND_OP_DATA", "ZEND_CHECK_FUNC_ARG",
		"ZEND_CHECK_UNDEF_ARGS", "ZEND_VERIFY_RETURN_TYPE", "ZEND_BIND_STATIC",
		"ZEND_BIND_LEXICAL", "ZEND_BEGIN_SILENCE", "ZEND_END_SILENCE",
		"ZEND_DECLARE_CONST", "ZEND_DECLARE_CLASS", "ZEND_DECLARE_FUNCTION",
		"ZEND_TICKS_OP", "ZEND_ASSIGN_STATIC_PROP_REF":
		return true
	}
	return false
}

// Coverage renders every method and reports opcode coverage: total oplines seen,
// the count matched to a handler, and the histogram of opcodes that matched none.
func Coverage(methods []opline.Method) (total, handled int, unknown map[string]int) {
	unknown = map[string]int{}
	for i := range methods {
		m := methods[i]
		ev := newEvaluator(&m, "")
		_ = ev.structure(0, len(ev.ops), 1)
		total += len(ev.ops)
		for op, n := range ev.unhandled {
			unknown[op] += n
		}
	}
	tot := 0
	for _, n := range unknown {
		tot += n
	}
	handled = total - tot
	return
}

// isBoolProducer reports whether an opcode computes a boolean into its result
// slot (a comparison, type/identity check, isset/empty, or instanceof) — the ops
// whose result a conditional jump consumes as its condition.
func isBoolProducer(op string) bool {
	switch op {
	case "ZEND_TYPE_CHECK", "ZEND_INSTANCEOF", "ZEND_DEFINED",
		"ZEND_IS_IDENTICAL", "ZEND_IS_NOT_IDENTICAL",
		"ZEND_IS_EQUAL", "ZEND_IS_NOT_EQUAL",
		"ZEND_IS_SMALLER", "ZEND_IS_SMALLER_OR_EQUAL",
		"ZEND_CASE", "ZEND_CASE_STRICT", "ZEND_BOOL", "ZEND_BOOL_NOT",
		"ZEND_ISSET_ISEMPTY_CV", "ZEND_ISSET_ISEMPTY_VAR",
		"ZEND_ISSET_ISEMPTY_DIM_OBJ", "ZEND_ISSET_ISEMPTY_PROP_OBJ",
		"ZEND_ISSET_ISEMPTY_STATIC_PROP", "ZEND_IN_ARRAY":
		return true
	}
	return false
}

// repairMaskedCondResults rebinds a boolean-producing op whose result slot the
// reveal masks to UNUSED (a Zend-4.x/ionCube quirk observed on TYPE_CHECK and
// CASE_STRICT feeding a conditional jump) to the TMP slot the immediately-following
// JMPZ/JMPNZ actually reads — otherwise the jump's condition renders from a stale
// slot. On 7.4 the result slot is revealed intact, so the UNUSED guard makes this a
// no-op there; it never overwrites a slot the reveal already supplied.
func (ev *evaluator) repairMaskedCondResults() {
	for k := 0; k+1 < len(ev.ops); k++ {
		o := &ev.ops[k]
		if o.Res.T != "UNUSED" || !isBoolProducer(o.Op) {
			continue
		}
		if nx := ev.ops[k+1]; (nx.Op == "ZEND_JMPZ" || nx.Op == "ZEND_JMPNZ") && nx.Op1.T == "TMP" {
			o.Res = nx.Op1
		}
	}
}

func isCondJump(op string) bool {
	switch op {
	case "ZEND_JMPZ", "ZEND_JMPNZ", "ZEND_JMPZ_EX", "ZEND_JMPNZ_EX", "ZEND_JMPZNZ",
		"ZEND_JMP_SET", "ZEND_COALESCE", "ZEND_JMP_NULL":
		return true
	}
	return false
}
func isSwitchHead(op string) bool {
	return op == "ZEND_SWITCH_STRING" || op == "ZEND_SWITCH_LONG" || op == "ZEND_MATCH"
}

// isSyntheticReturn reports whether op is Zend's implicit trailing `return null`
// (op_array epilogue): a RETURN with the sentinel ext (uint32 -1) and no real
// operand. The compiler appends it to every op_array; it is never user source.
func isSyntheticReturn(op opline.Op) bool {
	switch op.Op {
	case "ZEND_RETURN", "ZEND_RETURN_BY_REF", "ZEND_GENERATOR_RETURN":
	default:
		return false
	}
	if op.Ext != 0xffffffff {
		return false
	}
	return op.Op1.T == "UNUSED" || (op.Op1.T == "CONST" && op.Op1.Val == nil)
}

func (ev *evaluator) jumpTarget(op opline.Op) int {
	if op.Op1.T == "JMP" {
		return op.Op1.Jmp
	}
	if op.Op2.T == "JMP" {
		return op.Op2.Jmp
	}
	return -1
}

func sameSlot(a, b opline.Operand) bool {
	return a.T == b.T && a.Num == b.Num && (a.T == "TMP" || a.T == "VAR")
}

func issetKind(ext uint64) string {
	if ext&1 != 0 {
		return "empty"
	}
	return "isset"
}

func assignOp(ext uint64) string {
	switch ext {
	case 1:
		return "+"
	case 2:
		return "-"
	case 3:
		return "*"
	case 4:
		return "/"
	case 5:
		return "%"
	case 6:
		return "<<"
	case 7:
		return ">>"
	case 8:
		return "."
	case 9:
		return "|"
	case 10:
		return "&"
	case 11:
		return "^"
	case 12:
		return "**"
	}
	return "."
}

func castType(ext uint64) string {
	switch ext {
	case 4:
		return "(int)"
	case 5:
		return "(float)"
	case 6:
		return "(string)"
	case 7:
		return "(array)"
	case 8:
		return "(object)"
	case 10:
		return "(bool)"
	}
	return "(string)"
}

func typeCheckFn(ext uint64) string {
	// ext is a type mask (1<<IS_*). Map the common single-type checks.
	switch ext {
	case 1 << 1: // IS_NULL
		return "is_null"
	case (1 << 2) | (1 << 3): // IS_FALSE|IS_TRUE
		return "is_bool"
	case 1 << 4: // IS_LONG
		return "is_int"
	case 1 << 5: // IS_DOUBLE
		return "is_float"
	case 1 << 6: // IS_STRING
		return "is_string"
	case 1 << 7: // IS_ARRAY
		return "is_array"
	case 1 << 8: // IS_OBJECT
		return "is_object"
	case 1 << 9: // IS_RESOURCE
		return "is_resource"
	case 1 << 12: // IS_CALLABLE (approx)
		return "is_callable"
	}
	return "is_scalar /* decompiler: type mask " + itoa(int(ext)) + " */"
}

func isIdent(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (i > 0 && c >= '0' && c <= '9') {
			continue
		}
		return false
	}
	return true
}

func itoa(i int) string { return fmt.Sprintf("%d", i) }

// flKey is the (file,line) fallback key for the closure map, used when a
// DECLARE_LAMBDA op1 mangled key is unavailable (encrypted on Zend 2.6). The "@fl"
// prefix keeps it disjoint from the mangled keys (which begin with a NUL byte).
func flKey(file string, line int) string { return "@fl\x00" + file + "\x00" + itoa(line) }

// stable ordering helper used by RenderFile's map iteration (kept for tests)
var _ = sort.Strings
