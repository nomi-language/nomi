package irbuild

// `dbg` in the retained shape. It adds no IR node and no IR field.
//
// # The construct is four nodes the builder already builds
//
//	the operand      `bl.lower`, plus a forced `ir.Copy` for an impure operand
//	the SOURCE TEXT  an `ir.Const` String — `format.RenderNode` of the operand
//	the rendering    `ir.Render` at `RenderDebug`, which `interp` builds at
//	                 `RenderDisplay`
//	the print        `ir.NewHostCall`, which `bl.hostOutput` builds for `io.print`
//
// `dbg` IS TRANSPARENT — it answers the operand — so there is no result
// plumbing: `bl.dbg` returns the operand's own temporary and the `ir.Call`'s
// destination is read by nothing.
//
// # The source text is an operand and the line is the position
//
//   - The source text is an `ir.Const` String operand. The VM cannot
//     re-derive `format.RenderNode(operand)`, since the AST is gone by the
//     time it reads the graph, so the text arrives from the builder.
//
//   - THE LINE comes off `Pos()`. It is already there, mandatory, and checked:
//     `ir.At` panics below line 1 and `ir.Lint`'s RulePositionValid re-asks. A
//     second copy could disagree with the first and nothing would catch it.
//     `ir.NoMatch` does the same (`irnomatch.go`), and `internal/vm` reads the
//     line from the position.
//
// # The rendering is the record's
//
// `dbg e` renders e as `Debug.inspect(e)` does, and the checker records it
// as that use (analysis.FileAnalysis.Operators, the Debug use at e's type).
// dbgRender reads it: an impl machineImpls lists renders by the machine,
// naming the body each nested part's own record names (nestedDebugImpls);
// any other impl is called; an interface value renders through every
// implementor's body; a function value is its name-free constant; a tuple
// or a record renders by its shape.
//
// # The pipe form
//
// `x |> dbg` arrives as an `*ast.Binary` with `Op == "|>"` whose Right is an
// `*ast.Dbg` with a nil `Expr`. `bl.pipe` answers `|>` and passes `t.Left`;
// `bl.dbg` is the prefix spelling and passes `t.Expr`. Both render the
// OPERAND's source text.
//
// # The multi-line layout is unreachable from this shape
//
// `rt.Dbg` has three layout shapes and the operand's SOURCE TEXT picks between
// them. `bl.lower` requires every node of a statement to carry that
// statement's line, so an operand whose text spans
// lines declines before `bl.dbg` is reached, and the empty shape is not
// reachable from source at all (`format.RenderNode` of a real node is never
// empty; rt/dbg.go says so). So a retained `dbg` always takes the SINGLE-LINE
// layout. The other two are `rt`'s and `internal/vm`'s to agree about, and
// they are pinned against each other there rather than here.

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// dbgKey is the printed name of `dbg`'s crossing.
//
// A NAME AND NOT A NOMI DECLARATION, which is the one thing this symbol is
// unlike `io.print`'s: `printKey` is a module-qualified Nomi function and this
// is a keyword. `internal/vm`'s `hosts` map is "keyed on the callee's printed
// name", so the name is this string on both sides.
const dbgKey = "dbg"

// irDebugValueKind names values the VM can render without nominal dispatch
// or declaration metadata. Tuples supply positional order; records sort names.
// Anchored prelude enums use structural Debug with independently checked payloads.
func irDebugValueKind(k kind) bool {
	if k == kindUnit || k == kindNever || irByteValueKind(k) || isDecimalKind(k) || irScalarLeafKind(k) || irRetainedListKind(k) || irRetainedVectorKind(k) {
		return true
	}
	if k.tag == tagSeq {
		// A lazy Iter renders as the `<iter>` placeholder without running
		// it, but an Iter position holding a source renders as that source
		// (rt.Seq's Src), which may be a Range, a user implementor or a
		// collection of declared values: nestedDebugImpls names their impls.
		return false
	}
	if member, isSet := setElem(k); isSet && irRetainedSetKind(k) {
		// A declared member renders through its Debug impl, which
		// nestedDebugImpls names.
		return irDebugValueKind(member)
	}
	if irListTransportKind(k) && isDecimalKind(k.comp.parts[0]) {
		return true
	}
	if k.tag == tagMap && irRetainedMapKind(k) {
		return irDebugValueKind(k.comp.parts[0]) && irDebugValueKind(k.comp.parts[1])
	}
	if k.tag == tagNamed && irRetainedEnumKind(k.def) && k.def.preludeOf != nil {
		spec := k.def.preludeOf.spec
		if spec != preludeSpecFor("std/maybe", "Maybe") && spec != preludeSpecFor("std/results", "Result") && spec != preludeSpecFor("std/literals", "Fragment") && spec != preludeSpecFor("std/tasks", "Outcome") {
			return false
		}
		for _, v := range k.def.variants {
			for _, p := range v.payloads {
				if !irDebugValueKind(p.k) {
					return false
				}
			}
		}
		return true
	}
	if irConcHandleKind(k) {
		// A channel half or a task renders as its placeholder (`<channel>`,
		// `<task>`), rt.DebugText's Opaque arm, and a `Channel<T>` as the
		// struct std declares around its two halves (rt.DebugText).
		return true
	}
	if irStdMarkerKind(k) {
		// std's payload-free marker (`ChannelClosed`) has no impl of its own
		// and renders as its bare name, as rt's value does.
		return true
	}
	if k.tag == tagNamed && k.def != nil && k.def == namedPayloadDefs()[outcomeFailure] {
		// A task's Failure, Outcome.Failed's payload: two String variants.
		return true
	}
	if !irRetainedTupleKind(k) && !irRetainedRecordKind(k) {
		return false
	}
	for _, part := range k.comp.parts {
		// A function part renders as rt.FunctionInspectText.
		if part.tag != tagFunc && !irDebugValueKind(part) {
			return false
		}
	}
	return true
}

// irStructuralDebugKind is a container whose Debug text is rt.DebugText's with
// no impl consulted: a Vector, List, Map, tuple or record composed only of
// values irDebugValueKind renders (`Vector<Vector<Int>>`).
func irStructuralDebugKind(k kind) bool {
	if irDebugValueKind(k) {
		return true
	}
	if elem, vector := vectorElem(k); vector {
		return irStructuralDebugKind(elem)
	}
	switch k.tag {
	case tagList, tagMap, tagTuple, tagAnonStruct:
		if k.comp == nil || len(k.comp.parts) == 0 {
			return false
		}
		for _, p := range k.comp.parts {
			if !irStructuralDebugKind(p) {
				return false
			}
		}
		return true
	}
	return false
}

// irDbgCallee is the identity token `dbg`'s crossing is interned against.
//
// A TYPE AND NOT THE STRING `"dbg"`, for `irSlotDefault`'s reason at the
// opposite end: `irCalleeSym`'s token space mixes producer POINTERS with
// module-qualified NAMES, and its recorded hazard is "a family added with an
// UNQUALIFIED key would share a token with another family's method of the same
// name". `dbg` has no qualifier to give it, so it gets a type instead —
// `Table.Symbol` keys on `any` and Go compares the dynamic type first, so a
// zero-size struct can collide with nothing.
//
// ONE TOKEN FOR EVERY `dbg` IN THE PROGRAM, which is correct for the same
// reason `printKey` is one token for every `io.print`: the crossing names one
// host procedure, and a Symbol is "a name plus an identity and NOTHING ELSE".
type irDbgCallee struct{}

// dbg lowers `dbg E`: print the operand's source text beside its Debug
// rendering, and answer the operand.
func (bl *irScalarBuilder) dbg(t *ast.Dbg) (ir.Temp, kind, bool, bool) {
	return bl.dbgOf(t, t.Expr)
}

// dbgOf lowers `dbg E` over an EXPLICIT operand, which the pipe form passes
// as `t.Left`.
func (bl *irScalarBuilder) dbgOf(t *ast.Dbg, operand ast.Node) (ir.Temp, kind, bool, bool) {
	if isNilNode(operand) {
		// The parser gives `dbg` its operand, and `bl.pipe` passes a pipe
		// stage's left side.
		panic("irbuild: dbgOf: a `dbg` with no operand")
	}
	// kindInvalid: sentinel — no position supplies a wanted type.
	return bl.dbgWant(t, operand, kindInvalid)
}

// dbgWant is dbgOf with the type the position wants for the operand, or
// kindInvalid for none. The operand renders as `Debug.inspect(operand)`
// does, from the record the checker made of the `dbg` itself
// (analysis.FileAnalysis.Operators: the Debug use at the operand's type,
// which TestElaborationGate holds every `dbg` to; dbgRender).
func (bl *irScalarBuilder) dbgWant(t *ast.Dbg, operand ast.Node, want kind) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	var src ir.Temp
	var k kind
	var mobile, ok bool
	// kindInvalid: sentinel — no position supplies a wanted type.
	if want != kindInvalid {
		src, k, mobile, ok = bl.lowerTypedOperand(operand, want)
	} else {
		src, k, mobile, ok = bl.lower(operand)
	}
	if !ok {
		return no()
	}
	// The operand is evaluated once. `dbg` is transparent, so the operand is
	// read by the renderer and by whatever reads the `dbg`'s value. In the
	// graph a temporary is already one evaluation, so the second read needs
	// nothing here. The Copy forces an impure operand; its condition is
	// `!mobile`.
	if !mobile {
		cp := ir.NewCopy(bl.g.irNodePos(operand), bl.f.NewTemp(), src)
		bl.b.Append(cp)
		bl.side(cp.Dst(), irScalarSide{k: k, copy: irCopyForce})
		src = cp.Dst()
	}
	txt := ir.NewString(bl.g.irNodePos(t), bl.f.NewTemp(), renderNode(operand))
	bl.b.Append(txt)
	vt := bl.g.operandCheckerType(operand)
	rendered, by := bl.debugRender(t, ir.OrdinaryCall, src, k, vt, bl.dbgRecord(t, vt).Callee)
	ok = by != ""
	if !ok {
		rendered, ok = bl.debugOver(operand, src, k, vt)
	}
	if !ok {
		return no()
	}
	c := ir.NewHostCall(bl.g.irPos(t.Line, t.Col), bl.f.NewTemp(), irCallSite(t),
		bl.g.irCalleeSym(irDbgCallee{}, dbgKey), txt.Dst(), rendered)
	bl.b.Append(c)
	bl.side(c.Dst(), irScalarSide{k: kindUnit})
	// MOBILE either way: an impure operand was forced above, and a mobile
	// one already was.
	return src, k, true, true
}

// debugRender renders src, a value of kind k and checker type kt, for the
// Debug use at (a `dbg`, or a call of `Debug.inspect` or `io.inspect`),
// as c, the record of the `Debug.inspect` it means, says, and answers the
// route that decided it, "" where it declines. One rule serves every
// spelling. Where c names an impl that machineImpls renders, the machine
// renders the value, through the bodies the records of its parts name
// (nestedDebugImpls). Where it names any other impl, that body is called,
// at site (recordedDebugBodyOf). Where it leaves the call to dispatch, an
// interface value renders through every implementor's body
// (existentialDebugImpls), and an `Iter<T>` by its source. Where it
// answers structurally, a function value is its name-free constant and
// any other value renders by its shape. A record of no form here, one in
// an instance whose frame carries no checker type for the operand
// (OperatorPerInstance, unresolved), renders by the value's kind
// (viaDebugNoRecord). A body named for the renderer is only ever a
// nested part's, which has no call site of its own.
func (bl *irScalarBuilder) debugRender(at ast.Node, site ir.CallSite, src ir.Temp, k kind, kt analysis.Type, c analysis.Callee) (ir.Temp, callRoute) {
	pos := bl.g.irNodePos(at)
	call, _ := at.(*ast.Call)
	by := viaDebugRecord
	if p := bl.g.boundStdPlan("Debug", "inspect"); p != nil && (irExistentialKind(k) || irConjKind(k)) &&
		c.Form != analysis.OperatorByImpl && c.Form != analysis.OperatorStructural {
		// In an abstract body, a value held as an interface, which a type
		// parameter left open is, whose type may be any: a call through
		// the bound of Debug's inspect, which the walk answers by every
		// file's implementation (boundcall.go).
		r := ir.NewBoundCall(pos, bl.f.NewTemp(), site, p.sym, src)
		bl.b.Append(r)
		bl.side(r.Dst(), irScalarSide{k: kindString})
		return r.Dst(), by
	}
	switch c.Form {
	case analysis.OperatorByImpl:
		if bl.g.debugByMachine(c, kt) {
			break
		}
		b := bl.recordedDebugBodyOf(c, k, kt)
		if b.sym == nil {
			return ir.NoTemp, ""
		}
		r := ir.NewCall(pos, bl.f.NewTemp(), site, b.sym, src)
		bl.b.Append(r)
		bl.side(r.Dst(), irScalarSide{k: kindString, deferrable: true})
		return r.Dst(), by
	case analysis.OperatorDispatched:
		if heldAs(k) != heldBoxed {
			// An `Iter<T>`: the machine renders it, by its source.
			break
		}
		impls, ok := bl.existentialDebugImpls(k)
		if !ok {
			return ir.NoTemp, ""
		}
		bl.g.verifyCallForm(call, k, "debugRender", analysis.OperatorDispatched)
		return bl.emitDebugRender(pos, src, impls), by
	case analysis.OperatorStructural:
		if k.tag == tagFunc {
			// Any function value: a name-free constant
			// (rt.FunctionInspectText). The operand was evaluated.
			bl.g.verifyCallForm(call, k, "debugRender", analysis.OperatorStructural)
			r := ir.NewString(pos, bl.f.NewTemp(), rt.FunctionInspectText())
			bl.b.Append(r)
			return r.Dst(), by
		}
	default:
		by = viaDebugNoRecord
	}
	var impls []ir.DebugImpl
	if !irStructuralDebugKind(k) {
		var ok bool
		if impls, ok = bl.nestedDebugImpls(at, k, kt); !ok {
			return ir.NoTemp, ""
		}
	}
	bl.g.verifyCallForm(call, k, "debugRender", analysis.OperatorByImpl, analysis.OperatorStructural)
	return bl.emitDebugRender(pos, src, impls), by
}

// emitDebugRender appends the Debug rendering of src at pos, through
// impls where there are any, and answers its String.
func (bl *irScalarBuilder) emitDebugRender(pos ir.Pos, src ir.Temp, impls []ir.DebugImpl) ir.Temp {
	var r *ir.Render
	if len(impls) > 0 {
		r = ir.NewRenderDebugWith(pos, bl.f.NewTemp(), src, impls)
	} else {
		r = ir.NewRenderDebug(pos, bl.f.NewTemp(), src)
	}
	bl.b.Append(r)
	bl.side(r.Dst(), irScalarSide{k: kindString})
	return r.Dst()
}

// dbgRecord is the record of `Debug.inspect(operand)` the `dbg` t means,
// for an operand of checker type vt: the call of Debug's inspect, answered
// as the checker answered t (its OperatorUse, at an instance's types), and
// where that names an impl, a call of it (analysis.FileAnalysis.ImplCallee).
// A `dbg` the checker made no record of (code it never checked) is
// answered at vt as the builder asks it (stdIfaceCallRecord).
func (bl *irScalarBuilder) dbgRecord(t *ast.Dbg, vt analysis.Type) *analysis.ResolvedCall {
	use := bl.g.operatorUse(t)
	if use == nil {
		return bl.g.stdIfaceCallRecord(stdDebug, "inspect", 1, vt)
	}
	rc := bl.g.stdIfaceCallRecord(stdDebug, "inspect", 1, nil)
	c := rc.Callee
	if use.Form == analysis.OperatorByImpl && use.Impl != nil && !isNilNode(use.Impl.Method) {
		impl := bl.g.instanceFA().ImplCallee(use.Impl.Method)
		impl.Ref, impl.Qualifier, impl.Owner, impl.Method = c.Ref, c.Qualifier, c.Owner, c.Method
		c = impl
	}
	c.For, c.Form = vt, use.Form
	rc.Callee = c
	return rc
}

// A non-final `dbg`'s value is dropped by irdiscard.go's `irStatementDrop`,
// which also serves an explicit `_ = expr`.

// debugImplSym is the Debug body of k, a named type: its impl in this file,
// the impl another file's generic instance was built against, or another
// file's type's impl, written or synthesized. synth says the impl is
// synthesized; ok is false when no lowerable body renders k.
func (bl *irScalarBuilder) debugImplSym(k kind) (sym *ir.Symbol, synth, ok bool) {
	og, self := bl.g, k
	d := bl.g.implsByIface[stdIfaceImplKey(stdDebug)][k]
	if d == nil && k.def != nil && k.def.instOrigin != nil {
		// Another file's generic instance: the owner built its impl
		// against its own instance def.
		og, d = bl.g.instanceImpl(k.def, stdIfaceImplKey(stdDebug), "inspect")
		self = named(k.def.instOrigin)
	}
	if d == nil && k.tag == tagNamed && k.def != nil && k.def.instOrigin == nil {
		// Another file's type: the Debug impl its declaring file holds,
		// written or synthesized, which `Debug.inspect(x)` reaches the
		// same way (qualSiblingIfaceImplPlan).
		site, params, result, owning := bl.g.siblingImplMember(k.def, "inspect", stdIfaceImplKey(stdDebug))
		if site == nil || len(params) != 1 || params[0] != k || result != kindString {
			return nil, false, false
		}
		return owning.irImplSym(site.item), site.synth, true
	}
	if d == nil || !d.lowerable {
		return nil, false, false
	}
	it := d.items["inspect"]
	if it == nil || !it.lowerable || irImplSource(it) == nil || len(it.params) != 1 || it.params[0] != self || it.result != kindString {
		return nil, false, false
	}
	return og.irImplSym(it), d.synth, true
}

// boxedEnumDebugImpls names the Debug body of k, an `embeds` enum with a
// hand-written `impl Debug`, for a value of it held boxed as an interface's
// existential: the box names the enum (irBoxPlan), and the renderer offers
// the box to the impl recorded under that name before it looks inside
// (rt.DebugText), so a value widened into an `embeds` variant renders
// through the enum's impl and not the embedded type's. A synthesized Debug
// renders each embedded value through its own impl, which nestedDebugImpls
// names.
func (bl *irScalarBuilder) boxedEnumDebugImpls(k kind) ([]ir.DebugImpl, bool) {
	if !irBoxedEnum(k) {
		return nil, false
	}
	sym, synth, ok := bl.debugImplSym(k)
	if !ok || synth {
		return nil, false
	}
	return []ir.DebugImpl{{Type: bl.g.irImplTypeSym(k).Name(), Fn: sym}}, true
}

// nestedDebugImpls collects the Debug impls a container's nominal leaves call.
// Native container Debug renders each struct or enum element through its own
// impl, so the VM is told which retained body renders which runtime type.
// A value widened into an `embeds` variant keeps the embedded value's runtime
// type, so an enum whose derived Debug delegates to its embedded values names
// their impls too; a written enum Debug has no such delegation and declines.
// A declared type whose Debug is its shape names no body, and its fields'
// and payloads' bodies are named (debugByShape). It answers false for any leaf
// outside that domain or with no nominal leaf.
// at is the `dbg`, `Debug.inspect` or `io.debug` the renderer serves, nil
// for an interface's implementor (noteDebugImpl).
func (bl *irScalarBuilder) nestedDebugImpls(at ast.Node, k kind, kt analysis.Type) ([]ir.DebugImpl, bool) {
	var impls []ir.DebugImpl
	// seen is keyed by runtime type name and instance key: two instances of
	// one generic type (`Box<Bool>`, `Box<String>`) share the name and each
	// renders through its own body.
	type implKey struct{ name, inst string }
	seen := map[implKey]bool{}
	add := func(k kind, fn *ir.Symbol) {
		name := bl.g.irTypeSym(k.def).Name()
		inst := bl.g.irValType(k).InstanceKey()
		if seen[implKey{name, inst}] {
			return
		}
		seen[implKey{name, inst}] = true
		impls = append(impls, ir.DebugImpl{Type: name, Inst: inst, Fn: fn})
	}
	// within is the arm a part is reached through that names a set of
	// bodies no single record answers (noteDebugImpl).
	var within debugImplClass
	// pick names b as the Debug body of the part k, of checker type kt.
	pick := func(k kind, kt analysis.Type, b debugBody) {
		class := within
		if class == "" && b.structural {
			class = debugImplBodyLocal
		}
		bl.noteDebugImpl(at, class, kt)
		add(k, b.sym)
	}
	// body is the Debug body of the named part k: the one the checker's
	// record of `Debug.inspect` at kt names (recordedDebugBody), and for a
	// part with no checker type, byKind's. Where the record answers
	// structurally, k is a type declared in a body whose file's bodies the
	// record could not read (debugBody.structural), and its kind names
	// the universal Debug its block holds.
	body := func(k kind, kt analysis.Type, byKind func() debugBody) debugBody {
		if kt == nil {
			return byKind()
		}
		b := bl.recordedDebugBody(k, kt)
		if b.structural {
			b = byKind()
			b.structural = true
		}
		return b
	}
	// register names the body of k, a declared type, and says whether the
	// front end synthesized it.
	register := func(k kind, kt analysis.Type) (synth, ok bool) {
		b := body(k, kt, func() debugBody {
			sym, synth, ok := bl.debugImplSym(k)
			if !ok {
				return debugBody{}
			}
			return debugBody{sym: sym, synth: synth}
		})
		if b.sym == nil {
			return false, false
		}
		pick(k, kt, b)
		return b.synth, true
	}
	// walk walks k, and kt, the checker's type k was read from where one
	// is known (partCheckerType), which a std instance it names is
	// entered with.
	var walk func(k kind, kt analysis.Type) bool
	// A declared type for which neither the record nor its kind names a
	// Debug body and whose Debug is its shape (debugByShape) renders as
	// the literal that builds it (rt.DebugText), so only its parts are
	// walked (parts). shaped holds the types walked so, which a recursive
	// type reaches again.
	shaped := map[kind]bool{}
	parts := func(k kind, kt analysis.Type) bool {
		if shaped[k] {
			return true
		}
		shaped[k] = true
		d := k.def
		if d.isDistinct {
			// kindInvalid: marker — a zero-sized marker renders as its name.
			if d.inner == kindInvalid {
				return true
			}
			var inner analysis.Type
			if dt, ok := analysis.ResolveTypeVar(kt).(*analysis.DistinctType); ok {
				inner = analysis.DistinctInner(dt)
			}
			return walk(d.inner, inner)
		}
		for _, f := range d.fields {
			if !walk(f.k, declaredPartType(kt, "", f.nomi)) {
				return false
			}
		}
		for _, v := range d.variants {
			if v.embeds != nil {
				if !walk(named(v.embeds), embeddedVariantType(kt, v.nomi)) {
					return false
				}
				continue
			}
			for _, p := range v.payloads {
				pt := variantPayloadType(kt, v.nomi)
				if p.nomi != "" {
					pt = declaredPartType(kt, v.nomi, p.nomi)
				}
				if !walk(p.k, pt) {
					return false
				}
			}
		}
		return true
	}
	// attempt walks k and keeps what it registered only if the walk
	// succeeds, for a source an Iter position may or may not hold.
	attempt := func(k kind) {
		n := len(impls)
		if walk(k, nil) {
			return
		}
		for _, im := range impls[n:] {
			delete(seen, implKey{im.Type, im.Inst})
		}
		impls = impls[:n]
	}
	walk = func(k kind, kt analysis.Type) bool {
		if heldAs(k) == heldBoxed {
			// An interface value inside a tuple, record or list renders as
			// the value it holds, through every implementer's Debug, as a
			// top-level one does (existentialDebugImpls).
			got, ok := bl.existentialDebugImpls(k)
			if !ok {
				return false
			}
			bl.noteDebugImpl(at, debugImplExistential, nil)
			for _, im := range got {
				key := implKey{im.Type, im.Inst}
				if !seen[key] {
					seen[key] = true
					impls = append(impls, im)
				}
			}
			return true
		}
		// machine: the record of `Debug.inspect` at kt names an impl the
		// machine renders (machineImpls), so k names no body of its own
		// and only its parts are walked. A part with no record is chosen
		// by its kind.
		machine := kt != nil && bl.g.debugByMachine(bl.g.stdIfaceCallRecord(stdDebug, "inspect", 1, kt).Callee, kt)
		if !machine && (k.tag == tagFunc || irDynamicKind(k) || irContextKind(k) || irTypeWitnessKind(k)) {
			// A function renders as rt.FunctionInspectText wherever it
			// sits: a tuple's part, a list's element, a map's value. A
			// Dynamic renders through rt.DynamicInspect and a Context
			// through rt.ContextInspect, the host functions std's `impl
			// Debug` of each declares, which rt.DebugText calls for one in
			// any part, and a `Type<T>` witness through
			// rt.TypeWitnessInspect.
			return true
		}
		if k.tag == tagSeq && k.comp != nil && len(k.comp.parts) == 1 {
			// An `Iter<T>` renders as `<iter>` when it is a pipeline and as
			// its source when it views one (rt.Seq's Src): a collection of T
			// (a Map's element is its (K, V) tuple), a Range of T, or a value
			// whose own type implements Iter over T. Each such source whose
			// Debug can be named is named; one that cannot is left to the
			// renderer's refusal, since the position may never hold it.
			elem := seqElem(k)
			outer := within
			within = debugImplIterSources
			defer func() { within = outer }()
			bl.noteDebugImpl(at, within, nil)
			attempt(elem)
			// A Range renders through std's generic `impl Debug for
			// Range<T>`, instantiated at this element. rangeKindOf's
			// refusal is no Range, which irIterRangeElem declines.
			rk := bl.g.rangeKindOf(elem)
			if _, walks := irIterRangeElem(rk); walks {
				// The Range is no value the checker typed: its element's
				// source is.
				if sym := bl.stdInstDebugSym(rk, nil); sym != nil {
					pick(rk, nil, debugBody{sym: sym})
				}
			}
			for _, d := range bl.g.implOrder {
				if d.recv.tag != tagNamed || d.recv.def == nil || bl.g.implsByIface[stdIfaceImplKey(stdIter)][d.recv] != d {
					continue
				}
				if it, userElem := bl.userSource(d.recv, userIterImpl{owner: bl.g, d: d}); it != nil && userElem == elem {
					attempt(d.recv)
				}
			}
			return true
		}
		if member, isSet := setElem(k); isSet {
			// rt's structural Set Debug renders each member through the
			// same hook.
			return walk(member, partCheckerType(kt, k, 0))
		}
		if f := bl.stdDebugAt(k); !machine && f != nil {
			// A std named type renders through std's own retained impl.
			b := body(k, kt, func() debugBody {
				if p := bl.stdDebugPlanOf(f, k); p != nil {
					return debugBody{sym: p.sym}
				}
				return debugBody{}
			})
			if b.sym != nil {
				pick(k, kt, b)
				return true
			}
		}
		if _, _, isGen := genStructOf(k); !machine && isGen {
			// An instance of a generic std struct (`Captured<Int>`) renders
			// through std's Debug template instantiated at it.
			if b := body(k, kt, func() debugBody { return debugBody{sym: bl.stdInstDebugSym(k, nil)} }); b.sym != nil {
				pick(k, kt, b)
				return true
			}
		}
		if k.tag == tagNamed && k.def != nil && k.def.preludeOf != nil && irRetainedEnumKind(k.def) {
			for _, v := range k.def.variants {
				for _, pl := range v.payloads {
					if !walk(pl.k, variantPayloadType(kt, v.nomi)) {
						return false
					}
				}
			}
			return true
		}
		if !machine && k.tag == tagNamed && (irWrappingDistinct(k.def) || irCompositeDistinct(k.def) || irRetainedMarker(k.def)) {
			// A distinct or a marker in a container is a boxed value that
			// carries its type: its Debug is its impl, named for the
			// renderer, or its shape, rt's structural `Meters(1)`.
			b := body(k, kt, func() debugBody {
				plan := bl.distinctDebugPlan(k)
				if plan == nil {
					return debugBody{}
				}
				if plan.sym != nil {
					return debugBody{sym: plan.sym}
				}
				return debugBody{sym: bl.g.irCalleeSym(plan.token, plan.name)}
			})
			if b.sym == nil {
				// std's own markers (`ChannelClosed`) have no impl here and
				// render as their name, structurally.
				return irStdMarkerKind(k) || bl.g.debugByShape(k, kt) && parts(k, kt)
			}
			pick(k, kt, b)
			return true
		}
		if !machine && irNominalElemKind(k) {
			synth, ok := register(k, kt)
			if !ok {
				return bl.g.debugByShape(k, kt) && parts(k, kt)
			}
			for _, v := range k.def.variants {
				if v.kind != "embedded" {
					continue
				}
				if !synth {
					return false
				}
				if _, ok := register(named(v.embeds), embeddedVariantType(kt, v.nomi)); !ok {
					return false
				}
			}
			return true
		}
		if elem, vector := vectorElem(k); vector {
			return walk(elem, partCheckerType(kt, k, 0))
		}
		switch {
		case k.tag == tagList && k.comp != nil && len(k.comp.parts) == 1:
			return walk(k.comp.parts[0], partCheckerType(kt, k, 0))
		case irMapTransportKind(k), k.tag == tagMap && irRetainedMapKind(k):
			return walk(k.comp.parts[0], partCheckerType(kt, k, 0)) && walk(k.comp.parts[1], partCheckerType(kt, k, 1))
		case irRetainedTupleKind(k) || irRetainedRecordKind(k):
			for i, part := range k.comp.parts {
				if !walk(part, partCheckerType(kt, k, i)) {
					return false
				}
			}
			return true
		}
		// A part the machine renders with no parts of its own (a scalar,
		// a marker, a Dynamic, a Context) names nothing.
		return irDebugValueKind(k) || machine && (k.comp == nil || len(k.comp.parts) == 0)
	}
	if !walk(k, kt) {
		return nil, false
	}
	// Every part renders: through the impls named, or structurally (a
	// function, a marker, a leaf), with none to name.
	return impls, true
}

// debugByShape reports whether k, a declared type, has a Debug that is its
// shape: it is not opaque and has no written or derived Debug, and the
// machine renders it as the literal that builds it (rt.DebugText). kt is
// its checker type, whose record of `Debug.inspect` answers so
// structurally (analysis's noImplUse). With no checker type there is no
// record to ask, and the declaration says (debugBlockless). The Debug
// renderer asks it of a part none of whose bodies it can name
// (nestedDebugImpls), and a box asks it of a value entering std's Debug
// (bindsImpl).
func (g *gen) debugByShape(k kind, kt analysis.Type) bool {
	if k.tag != tagNamed || k.def == nil || k.def.preludeOf != nil || opaqueType(kt) {
		return false
	}
	if kt != nil {
		return g.stdIfaceCallRecord(stdDebug, "inspect", 1, kt).Callee.Form == analysis.OperatorStructural
	}
	return k.def.decl != nil && !declOpaque(k.def.decl) && g.debugBlockless(k.def)
}

// debugBlockless reports whether d, a declared type, has no Debug block in
// any unit of the program this gen lowers with, its instances' among them.
func (g *gen) debugBlockless(d *typeDef) bool {
	gens := []*gen{g}
	if g.reg != nil {
		gens = append(gens, g.reg.gens...)
	}
	if s := g.stdInsts; s != nil {
		gens = append(gens, s.program...)
	}
	for _, og := range gens {
		if og == nil {
			continue
		}
		for k := range og.implsByIface[stdIfaceImplKey(stdDebug)] {
			if k.def != nil && (k.def.decl == d.decl || k.def.instOrigin != nil && k.def.instOrigin.decl == d.decl) {
				return false
			}
		}
	}
	return true
}

// stdInstDebugSym is the instance of std's generic `impl Debug` for the
// container kind k (`impl Debug for Range<T>` at `Range<Int>`) or the generic
// std struct instance k (`Captured<Int>`), for a renderer that calls it by
// runtime type rather than at a call site, or nil when std has no such
// template or it does not instantiate at k. kt is the checker's type k was
// read from, or nil (stdRecvInstance).
func (bl *irScalarBuilder) stdInstDebugSym(k kind, kt analysis.Type) *ir.Symbol {
	base := irContainerBaseName(k)
	if spec, _, isGen := genStructOf(k); isGen && base == "" {
		base = spec.nomi
	}
	if base == "" {
		return nil
	}
	return bl.stdInstDebugSymOf(bl.g.stdGenericTemplate(base, "inspect"), k, kt)
}

// stdInstDebugSymOf is the instance at k of f, a std generic `impl
// Debug`'s `inspect`, or nil.
func (bl *irScalarBuilder) stdInstDebugSymOf(f *stdFunc, k kind, kt analysis.Type) *ir.Symbol {
	if f == nil || !stdGenericTemplateUsable(f) {
		return nil
	}
	// A Unit argument is admitted: k is a built instance, so Unit is its
	// argument (`Captured<Unit>`), not a hole.
	inst := bl.g.stdRecvInstance(f, "Debug", k, kt, true)
	if inst == nil || len(inst.view.params) != 1 || inst.view.params[0] != k || inst.view.result != kindString {
		return nil
	}
	return inst.sym
}
