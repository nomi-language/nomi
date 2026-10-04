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
//   - THE SOURCE TEXT is an `ir.Const` String operand. No consumer can
//     re-derive `format.RenderNode(operand)` — the AST is gone by the time
//     anything reads the graph — so it arrives from the producer.
//
//   - THE LINE comes off `Pos()`. It is already there, mandatory, and checked:
//     `ir.At` panics below line 1 and `ir.Lint`'s RulePositionValid re-asks. A
//     second copy could disagree with the first and nothing would catch it.
//     `ir.NoMatch` does the same (`irnomatch.go`), and `internal/vm` reads the
//     line from the position.
//
// # What is refused
//
// The operand admits Int, Float, Bool, String and lists of those scalars,
// including nested lists, and tuples/records recursively composed from these
// values. Nominal elements are outside this fence because `debugRendering`
// can REFUSE, and `irScalarRender` PANICS on a refusal rather than routing one.
//
// A named operand outside irDebugValueKind renders through a Debug impl
// instead: distinctDebugPlan names one for a distinct, struct or enum, and
// nestedDebugImpls names each nested type's impl for a container, including
// one another file declares for its own type. A value neither covers renders
// as `Debug.inspect(operand)` renders it (debugOver), so `dbg` accepts every
// value Debug.inspect does and declines only where Debug.inspect would.
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
	if k == kindUnit || irByteValueKind(k) || isDecimalKind(k) || irScalarLeafKind(k) || irRetainedListKind(k) || irRetainedVectorKind(k) || k == kindEmptySet {
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
	if elem, vector := vectorElem(k); vector && k != kindEmptyVector {
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
// Go procedure, and a Symbol is "a name plus an identity and NOTHING ELSE".
type irDbgCallee struct{}

// dbg lowers `dbg E`: print the operand's source text beside its Debug
// rendering, and answer the operand.
func (bl *irScalarBuilder) dbg(t *ast.Dbg) (ir.Temp, kind, bool, bool) {
	return bl.dbgOf(t, t.Expr)
}

// dbgOf lowers `dbg E` over an EXPLICIT operand, which the pipe form passes
// as `t.Left`.
func (bl *irScalarBuilder) dbgOf(t *ast.Dbg, operand ast.Node) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if isNilNode(operand) {
		// A pipe stage with no operand: `bl.pipe` passes the left side, so
		// only a stage reached some other way lands here, and it declines.
		return no()
	}
	// kindInvalid: sentinel — no position supplies a wanted type.
	return bl.dbgWant(t, operand, kindInvalid)
}

// dbgWant is dbgOf with the type the position wants for the operand, or
// kindInvalid for none.
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
	if k.tag == tagFunc && bl.g.implsByIface["Debug"][k] == nil {
		// A function value's Debug is a name-free constant, as
		// `Debug.inspect` of one is (synthDebugRender).
		txt := ir.NewString(bl.g.irNodePos(t), bl.f.NewTemp(), renderNode(operand))
		bl.b.Append(txt)
		r := ir.NewString(bl.g.irNodePos(t), bl.f.NewTemp(), rt.FunctionInspectText())
		bl.b.Append(r)
		c := ir.NewHostCall(bl.g.irPos(t.Line, t.Col), bl.f.NewTemp(), irCallSite(t),
			bl.g.irCalleeSym(irDbgCallee{}, dbgKey), txt.Dst(), r.Dst())
		bl.b.Append(c)
		bl.side(c.Dst(), irScalarSide{k: kindUnit})
		return src, k, mobile, true
	}
	plan, distinctDebug := bl.distinctDebugPlan(k)
	if plan == nil && !irDebugValueKind(k) {
		// A std type whose Debug impl is a `host fn` (`impl Debug for
		// Dynamic`) renders through that host, as `Debug.inspect(x)` does.
		if p := bl.stdHostDebugPlan(operand, src, k); p != nil {
			plan, distinctDebug = p, true
		}
	}
	var impls []ir.DebugImpl
	// viaInspect: none of the shapes above names the rendering, so it is
	// built as `Debug.inspect(operand)` builds it (debugOver). `dbg` renders
	// every value Debug.inspect does, through the same impl.
	viaInspect := false
	if !irDebugValueKind(k) && !distinctDebug {
		var ok bool
		if impls, ok = bl.nestedDebugImpls(k); !ok {
			viaInspect = true
		}
	}
	// ONCE, and the two consumers need it for two different reasons. `dbg` is
	// transparent, so the operand is read by the RENDERER and by whatever
	// consumes the `dbg`. In the graph a temporary is already one evaluation,
	// so a second consumer needs nothing here. The Copy forces an impure
	// operand; its condition is `!mobile`.
	if !mobile {
		cp := ir.NewCopy(bl.g.irNodePos(operand), bl.f.NewTemp(), src)
		bl.b.Append(cp)
		bl.side(cp.Dst(), irScalarSide{k: k, copy: irCopyForce})
		src = cp.Dst()
	}
	txt := ir.NewString(bl.g.irNodePos(t), bl.f.NewTemp(), renderNode(operand))
	bl.b.Append(txt)
	var rendered ir.Temp
	if viaInspect {
		r, ok := bl.debugOver(operand, src, k)
		if !ok {
			return no()
		}
		rendered = r
	} else if plan != nil {
		sym := plan.sym
		if sym == nil {
			sym = bl.g.irCalleeSym(plan.token, plan.name)
		}
		var r *ir.Call
		if plan.host {
			r = ir.NewHostCall(bl.g.irNodePos(t), bl.f.NewTemp(), ir.OrdinaryCall, sym, src)
		} else {
			r = ir.NewCall(bl.g.irNodePos(t), bl.f.NewTemp(), ir.OrdinaryCall, sym, src)
		}
		bl.b.Append(r)
		bl.side(r.Dst(), irScalarSide{k: kindString, deferrable: true})
		rendered = r.Dst()
	} else if len(impls) > 0 {
		r := ir.NewRenderDebugWith(bl.g.irNodePos(t), bl.f.NewTemp(), src, impls)
		bl.b.Append(r)
		bl.side(r.Dst(), irScalarSide{k: kindString})
		rendered = r.Dst()
	} else {
		r := ir.NewRenderDebug(bl.g.irNodePos(t), bl.f.NewTemp(), src)
		bl.b.Append(r)
		bl.side(r.Dst(), irScalarSide{k: kindString})
		rendered = r.Dst()
	}
	c := ir.NewHostCall(bl.g.irPos(t.Line, t.Col), bl.f.NewTemp(), irCallSite(t),
		bl.g.irCalleeSym(irDbgCallee{}, dbgKey), txt.Dst(), rendered)
	bl.b.Append(c)
	bl.side(c.Dst(), irScalarSide{k: kindUnit})
	// MOBILE either way: an impure operand was forced above, and a mobile
	// one already was.
	return src, k, true, true
}

// A non-final `dbg`'s value is dropped by irdiscard.go's `irStatementDrop`,
// which also serves an explicit `_ = expr`.

// stdHostDebugPlan is the host crossing a std type's `impl Debug` declares as
// a `host fn`, selected as `Debug.inspect(operand)` selects it, or nil.
func (bl *irScalarBuilder) stdHostDebugPlan(operand ast.Node, src ir.Temp, k kind) *irQualPlan {
	if k.tag != tagNamed || k.def == nil || bl.g.implsByIface["Debug"][k] != nil {
		return nil
	}
	line, col := nodePos(operand)
	call := &ast.Call{Args: []ast.Node{operand}, Line: line, Col: col}
	args := irQualArgs{temps: []ir.Temp{src}, kinds: []kind{k}, mobile: []bool{true}, ok: true}
	p := bl.stdIfacePlan(call, args, "Debug", "inspect")
	if p == nil || !p.host || p.result != kindString {
		return nil
	}
	return p
}

// nestedDebugImpls collects the Debug impls a container's nominal leaves call.
// Native container Debug renders each struct or enum element through its own
// impl, so the VM is told which retained body renders which runtime type.
// A value widened into an `embeds` variant keeps the embedded value's runtime
// type, so an enum whose derived Debug delegates to its embedded values names
// their impls too; a written enum Debug has no such delegation and declines.
// It answers false for any leaf outside that domain or with no nominal leaf.
func (bl *irScalarBuilder) nestedDebugImpls(k kind) ([]ir.DebugImpl, bool) {
	var impls []ir.DebugImpl
	seen := map[string]bool{}
	sawSeq := false
	register := func(k kind) (bool, bool) {
		og, self := bl.g, k
		d := bl.g.implsByIface["Debug"][k]
		if d == nil && k.def != nil && k.def.instOrigin != nil {
			// Another file's generic instance: the owner built its impl
			// against its own instance def.
			og, d = bl.g.instanceImpl(k.def, "Debug", "inspect")
			self = named(k.def.instOrigin)
		}
		if d == nil && k.tag == tagNamed && k.def != nil && k.def.instOrigin == nil {
			// Another file's type: the Debug impl its declaring file holds,
			// written or synthesized, which `Debug.inspect(x)` reaches the
			// same way (qualSiblingIfaceImplPlan).
			site, params, result, owning := bl.g.siblingImplMember(k.def, "inspect", "Debug")
			if site == nil || len(params) != 1 || params[0] != k || result != kindString {
				return false, false
			}
			name := bl.g.irTypeSym(k.def).Name()
			if !seen[name] {
				seen[name] = true
				impls = append(impls, ir.DebugImpl{Type: name, Fn: owning.irCalleeSym(site.item, site.symName)})
			}
			return site.synth, true
		}
		if d == nil || !d.lowerable {
			return false, false
		}
		it := d.items["inspect"]
		if it == nil || !it.lowerable || irImplSource(it) == nil || len(it.params) != 1 || it.params[0] != self || it.result != kindString {
			return false, false
		}
		name := bl.g.irTypeSym(k.def).Name()
		if !seen[name] {
			seen[name] = true
			impls = append(impls, ir.DebugImpl{Type: name, Fn: og.irCalleeSym(it, self.nomi()+".inspect")})
		}
		return d.synth, true
	}
	var walk func(k kind) bool
	// attempt walks k and keeps what it registered only if the walk
	// succeeds, for a source an Iter position may or may not hold.
	attempt := func(k kind) {
		n := len(impls)
		if walk(k) {
			return
		}
		for _, im := range impls[n:] {
			delete(seen, im.Type)
		}
		impls = impls[:n]
	}
	walk = func(k kind) bool {
		if k.tag == tagSeq && k.comp != nil && len(k.comp.parts) == 1 {
			// An `Iter<T>` renders as `<iter>` when it is a pipeline and as
			// its source when it views one (rt.Seq's Src): a collection of T
			// (a Map's element is its (K, V) tuple), a Range of T, or a value
			// whose own type implements Iter over T. Each such source whose
			// Debug can be named is named; one that cannot is left to the
			// renderer's refusal, since the position may never hold it.
			sawSeq = true
			elem := seqElem(k)
			attempt(elem)
			// A Range renders through std's generic `impl Debug for
			// Range<T>`, instantiated at this element. rangeKindOf's
			// refusal is no Range, which irIterRangeElem declines.
			rk := bl.g.rangeKindOf(elem)
			if _, walks := irIterRangeElem(rk); walks {
				if sym := bl.stdInstDebugSym(rk); sym != nil {
					name := bl.g.irTypeSym(rk.def).Name()
					if !seen[name] {
						seen[name] = true
						impls = append(impls, ir.DebugImpl{Type: name, Fn: sym})
					}
				}
			}
			for _, d := range bl.g.implOrder {
				if d.recv.tag != tagNamed || d.recv.def == nil || bl.g.implsByIface["Iter"][d.recv] != d {
					continue
				}
				if it, userElem := bl.userSource(d.recv); it != nil && userElem == elem {
					attempt(d.recv)
				}
			}
			return true
		}
		if member, isSet := setElem(k); isSet && k != kindEmptySet {
			// rt's structural Set Debug renders each member through the
			// same hook.
			return walk(member)
		}
		if p := bl.stdDebugPlan(k); p != nil {
			// A std named type renders through std's own retained impl.
			name := bl.g.irTypeSym(k.def).Name()
			if !seen[name] {
				seen[name] = true
				impls = append(impls, ir.DebugImpl{Type: name, Fn: p.sym})
			}
			return true
		}
		if k.tag == tagNamed && k.def != nil && k.def.preludeOf != nil && irRetainedEnumKind(k.def) {
			for _, v := range k.def.variants {
				for _, pl := range v.payloads {
					if !walk(pl.k) {
						return false
					}
				}
			}
			return true
		}
		if irNominalElemKind(k) {
			synth, ok := register(k)
			if !ok {
				return false
			}
			for _, v := range k.def.variants {
				if v.kind != "embedded" {
					continue
				}
				if !synth {
					return false
				}
				if _, ok := register(named(v.embeds)); !ok {
					return false
				}
			}
			return true
		}
		if elem, vector := vectorElem(k); vector && k != kindEmptyVector {
			return walk(elem)
		}
		switch {
		case k.tag == tagList && k.comp != nil && len(k.comp.parts) == 1:
			return walk(k.comp.parts[0])
		case irMapTransportKind(k), k.tag == tagMap && k != kindEmptyMap && irRetainedMapKind(k):
			return walk(k.comp.parts[0]) && walk(k.comp.parts[1])
		case irRetainedTupleKind(k) || irRetainedRecordKind(k):
			for _, part := range k.comp.parts {
				// A function part renders as rt.FunctionInspectText.
				if part.tag != tagFunc && !walk(part) {
					return false
				}
			}
			return true
		}
		return irDebugValueKind(k)
	}
	if !walk(k) || (len(impls) == 0 && !sawSeq) {
		return nil, false
	}
	return impls, true
}

// stdInstDebugSym is the instance of std's generic `impl Debug` for the
// container kind k (`impl Debug for Range<T>` at `Range<Int>`), for a
// renderer that calls it by runtime type rather than at a call site, or nil
// when std has no such template or it does not instantiate at k.
func (bl *irScalarBuilder) stdInstDebugSym(k kind) *ir.Symbol {
	s := bl.g.stdInsts
	base := irContainerBaseName(k)
	if s == nil || base == "" {
		return nil
	}
	f := bl.g.stdGenericTemplate(base, "inspect")
	if !stdGenericTemplateUsable(f) {
		return nil
	}
	tps, ib := s.stdTemplateParams(f)
	if len(tps) == 0 || ib == nil || ib.Interface == nil || typeText(ib.Interface) != "Debug" {
		return nil
	}
	// kindInvalid: sentinel — no call site supplies a result kind.
	args, ok := bl.g.stdInstSolve(f, tps, ib, []kind{k}, kindInvalid, k)
	if !ok {
		return nil
	}
	inst, _ := s.instantiate(f, tps, args, nil, bl.g)
	if inst == nil || len(inst.view.params) != 1 || inst.view.params[0] != k || inst.view.result != kindString {
		return nil
	}
	return inst.sym
}
