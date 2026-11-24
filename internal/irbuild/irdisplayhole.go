// The machine picks a part's body by the static type of its position
// (vm's renderHook), so a value of an `embeds` enum, which is the embedded
// value at run time, renders through the enum's body where the position is
// the enum's and through the embedded type's own where it is that type's.
package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// RENDERING A VALUE BY DISPLAY.
//
// The checker records how each value the program renders by Display
// renders: an interpolation hole's (analysis.FileAnalysis.HoleDisplays),
// a fixed typed literal's failure (analysis.TypedLiteral.Failure) and a
// failed `!`'s error (analysis.FileAnalysis.UnwrapFailures): its own text,
// the machine's rendering of a scalar or a structural value, each part by
// its own record, the type's impl, or the impl an interface value holds.
// renderDisplay lowers one: it decides nothing about the route, and
// declines where the lowered kind is not the one the record describes. A
// type's impl is called as a recorded call of its function is: from the
// declaration the record names (displayImpl).
//
// A value of a type parameter is recorded as such: in an instance its
// rendering is the checker's at the instance's types (displayAt). A value
// no record describes (`io.print(x)`, `Display.to_string` over a tuple,
// the error `main` returns) renders as the checker renders its type
// (displayOf, analysis.FileAnalysis.DisplayOf), as a hole of that type
// would. Where a frame has no checker type (a field's text for
// ParseFields), the builder renders the value by its kind (kindDisplay);
// such a rendering names no impl, so its impl is the one the kind's name
// selects.

// holeDisplay is the checker's record of how the interpolation hole at
// renders, in the instance being built (displayAt), at the hole's lowered
// kind k. The checker records one for every hole it checks.
func (bl *irScalarBuilder) holeDisplay(at ast.Node, k kind) *analysis.DisplayRender {
	fa := bl.g.srcFA()
	if fa == nil {
		return nil
	}
	rec := fa.HoleDisplays[at]
	if rec == nil && k == kindString {
		// A body the checker never checked has no record: the universal
		// Debug of a block-local type, which the builder synthesizes
		// (registerBlockLocalDebug). Its holes are Strings, each its own
		// text.
		return &analysis.DisplayRender{Form: analysis.DisplayText}
	}
	return bl.g.displayAt(rec, k)
}

// displayAt is rec, a rendering the checker recorded for a value whose
// lowered kind is k, in the instance being built: a rendering of a type
// parameter is the checker's at the instance's types
// (analysis.FileAnalysis.DisplayAt), from the checker's types the frames
// carry (genericSubstType), as calleeAt asks for a call. Where a frame
// has no checker type, it is the rendering of k (kindDisplay).
func (g *gen) displayAt(rec *analysis.DisplayRender, k kind) *analysis.DisplayRender {
	if rec == nil {
		return nil
	}
	if g.inInstance() {
		if at, resolved := g.instanceFA().DisplayAt(rec, g.genericSubstType_); resolved {
			return at
		}
	}
	if rec.Form == analysis.DisplayTypeParam {
		return kindDisplay(k)
	}
	return rec
}

// kindDisplay is how a value of kind k renders where no record of the
// checker's describes it: the checker's rule (analysis.DisplayRender) over
// the lowered kind. It carries no Type, which every rendering the checker
// records does (byKind).
func kindDisplay(k kind) *analysis.DisplayRender {
	r := &analysis.DisplayRender{Form: analysis.DisplayImpl}
	switch {
	case k == kindString:
		r.Form = analysis.DisplayText
	case irScalarLeafKind(k) || isDecimalKind(k):
		r.Form = analysis.DisplayScalar
	case irHeldKind(k):
		r.Form = analysis.DisplayInterface
	case irStructuralDisplayKind(k):
		r.Form = analysis.DisplayStructural
	case irRetainedRecordKind(k) || irRetainedTupleKind(k):
		r.Form = analysis.DisplayParts
		for _, p := range k.comp.parts {
			r.Parts = append(r.Parts, kindDisplay(p))
		}
	}
	return r
}

// byKind reports whether r is a rendering the builder made from a kind
// (kindDisplay) rather than one the checker recorded.
func byKind(r *analysis.DisplayRender) bool {
	return r.Type == nil
}

// displayValue renders an already-lowered value of kind k as its
// interpolation would (displayOf). vt is the value's checker type at
// this instance, or nil.
func (bl *irScalarBuilder) displayValue(at ast.Node, src ir.Temp, k kind, vt analysis.Type) (ir.Temp, bool) {
	return bl.renderDisplay(at, src, k, bl.g.displayOf(vt, k), vt)
}

// displayOf is how a value of checker type vt, lowered at kind k, renders
// by Display where no record of the program's describes it: the checker's
// rendering of vt (analysis.FileAnalysis.DisplayOf), read against the
// program whose impls an instance's types have, as a hole of that type
// records it; where the frame has no checker type, the rendering of k
// (kindDisplay).
func (g *gen) displayOf(vt analysis.Type, k kind) *analysis.DisplayRender {
	if rec := g.instanceFA().DisplayOf(vt); rec != nil {
		return g.displayAt(rec, k)
	}
	return kindDisplay(k)
}

// renderDisplay lowers rec, the rendering of src, a value of kind k, into
// its String. The operand is already lowered, so each route is built over
// it rather than lowering the expression again. vt is the value's checker
// type at this instance where rec, made from k, carries none, or nil.
func (bl *irScalarBuilder) renderDisplay(at ast.Node, src ir.Temp, k kind, rec *analysis.DisplayRender, vt analysis.Type) (ir.Temp, bool) {
	if rec == nil {
		panic("irbuild: an interpolation hole with no HoleDisplays record; the checker records one for every hole (analysis package doc, \"Elaboration\")")
	}
	render := func(r *ir.Render) (ir.Temp, bool) {
		bl.b.Append(r)
		bl.side(r.Dst(), irScalarSide{k: kindString})
		return r.Dst(), true
	}
	switch rec.Form {
	case analysis.DisplayText:
		return src, k == kindString
	case analysis.DisplayScalar:
		if !irScalarLeafKind(k) && !isDecimalKind(k) {
			return ir.NoTemp, false
		}
		return render(ir.NewRenderDisplay(bl.g.irNodePos(at), bl.f.NewTemp(), src))
	case analysis.DisplayStructural:
		if irStructuralDisplayKind(k) {
			return render(ir.NewRenderDisplay(bl.g.irNodePos(at), bl.f.NewTemp(), src))
		}
		if byKind(rec) {
			panic("irbuild: a rendering made from the kind " + k.nomi() + " is structural, which kindDisplay makes only of a structural kind")
		}
		impls, ok := bl.nestedDisplayImpls(k, bl.g.checkerTypeArg(rec.Type))
		if !ok {
			return ir.NoTemp, false
		}
		return render(ir.NewRenderDisplayWith(bl.g.irNodePos(at), bl.f.NewTemp(), src, impls))
	case analysis.DisplayInterface:
		if irConjKind(k) {
			// A conjunction renders through its Display member, which
			// holds the same value (conjunction.go).
			for _, m := range k.comp.parts {
				if irStdDisplayExistential(m) {
					return bl.renderDisplay(at, bl.relabel(at, src, m), m, rec, nil)
				}
			}
			panic("irbuild: a conjunction rendered by Display with no Display member; the checker records DisplayInterface for a conjunction only when Display is one, and rejects any other hole of it (`... does not require Display of its implementors`)")
		}
		if !irExistentialKind(k) {
			// `(7 as Display)`: a value std's Display holds unboxed lowers
			// as itself (irErases), so the value it holds is the operand,
			// and it renders as that value's own type does.
			return bl.renderDisplay(at, src, k, kindDisplay(k), nil)
		}
		if !irStdDisplayExistential(k) {
			return ir.NoTemp, false
		}
		// The VM renders the value the interface value holds through that
		// value's own impl, as `Display.to_string(v)` does.
		return render(ir.NewRenderDisplayErased(bl.g.irNodePos(at), bl.f.NewTemp(), src))
	case analysis.DisplayParts:
		return bl.displayParts(at, src, k, rec.Parts)
	case analysis.DisplayImpl:
		return bl.displayImpl(at, src, k, rec, vt)
	}
	// holeDisplay and displayParts resolve DisplayTypeParam to the
	// instance's rendering before it reaches here.
	panic("irbuild: a hole rendering of an unresolved form")
}

// displayImpl calls the impl of Display for a value of kind k: std's
// instance for a std container (ifaceContainerCall), else want, the impl's
// function the rendering names (DisplayRender.Method), reached from its
// declaration as a recorded call reaches its function (recordedCallPlan).
// A rendering made from k alone (byName, kindDisplay) names none: its impl
// is the one k's name selects (qualImplPlan). The value's checker type,
// the rendering's Type or else vt, is what a std container's instance
// carries.
func (bl *irScalarBuilder) displayImpl(at ast.Node, src ir.Temp, k kind, rec *analysis.DisplayRender, vt analysis.Type) (ir.Temp, bool) {
	want, byName := rec.Method, byKind(rec)
	if !byName {
		vt = bl.g.checkerTypeArg(rec.Type)
	}
	line, col := nodePos(at)
	// The call of the impl's function on the value, its one operand: for a
	// recorded rendering, of the function the checker answers a call of
	// want reaches (analysis.FileAnalysis.ImplCallee).
	call := &ast.Call{Args: []ast.Node{at}, Line: line, Col: col}
	carried := analysis.PositionalCall(1, 1)
	switch {
	case byName:
		call.Func = &ast.FieldAccess{
			Object: &ast.TypeIdent{Name: "Display", Line: line, Col: col},
			Field:  &ast.Ident{Name: "to_string", Line: line, Col: col},
			Line:   line, Col: col,
		}
		carried = bl.g.stdIfaceCallRecord(stdDisplayType, "to_string", 1, vt)
	case !isNilNode(want):
		carried.Callee = bl.g.instanceFA().ImplCallee(want)
	}
	defer bl.g.carryCall(call, carried)()
	args := irQualArgs{temps: []ir.Temp{src}, kinds: []kind{k}, mobile: []bool{true}, ok: true}
	if v, rk, _, ok, handled := bl.ifaceContainerCall(call, args, "to_string", vt); handled {
		return v, ok && rk == kindString
	}
	if k.tag != tagNamed || k.def == nil {
		return ir.NoTemp, false
	}
	var plan *irQualPlan
	switch {
	case byName:
		plan = bl.qualImplPlan(call, args, "Display", "to_string")
	case !isNilNode(want):
		plan = bl.recordedCallPlan(call, args, "Display.to_string")
	}
	if plan == nil || plan.result != kindString {
		return ir.NoTemp, false
	}
	v, _, _, ok := bl.qualEmit(call, args, plan)
	return v, ok
}

// displayParts renders a tuple or anonymous record whose parts are not all
// structural (`(1, None)`, `{a: 1, b: Some(2)}`): each part by its own
// record, and the same-shaped composite of those Strings structurally, so
// rt's tuple and record layout is the one layout. A part with no rendering
// declines the whole.
func (bl *irScalarBuilder) displayParts(at ast.Node, src ir.Temp, k kind, parts []*analysis.DisplayRender) (ir.Temp, bool) {
	// The checker records parts only for a hole of a tuple or record type,
	// one per component, and the hole's lowered value has that type's kind:
	// a tuple or record the callable domain holds, which is retained
	// (irLoweredTupleRetained, irLoweredRecordRetained).
	if k.tag == tagAnonStruct {
		irLoweredRecordRetained(k, "a hole's parts")
	} else {
		irLoweredTupleRetained(k, "a hole's parts")
	}
	if len(parts) != len(k.comp.parts) {
		panic("irbuild: a hole of " + k.nomi() + " whose Display record has another number of parts")
	}
	pos := bl.g.irNodePos(at)
	if !irHeldValue(bl.f, src, bl.sides) {
		cp := ir.NewCopy(pos, bl.f.NewTemp(), src)
		bl.b.Append(cp)
		bl.side(cp.Dst(), irScalarSide{k: k, copy: irCopyHold})
		src = cp.Dst()
	}
	record := k.tag == tagAnonStruct
	texts := make([]ir.Temp, len(k.comp.parts))
	strs := make([]kind, len(k.comp.parts))
	for i, part := range k.comp.parts {
		var p *ir.Proj
		if record {
			p = ir.NewProjRecordField(pos, bl.f.NewTemp(), src, k.comp.names[i], irParamShape(part))
		} else {
			p = ir.NewProjSlot(pos, bl.f.NewTemp(), src, i, irParamShape(part))
		}
		bl.b.Append(p)
		bl.side(p.Dst(), irScalarSide{k: part})
		rec := parts[i]
		if rec != nil && rec.Form == analysis.DisplayTypeParam {
			rec = kindDisplay(part)
		}
		text, ok := bl.renderDisplay(at, p.Dst(), part, rec, nil)
		if !ok {
			return ir.NoTemp, false
		}
		texts[i], strs[i] = text, kindString
	}
	var made *ir.Make
	var mk kind
	if record {
		mk = bl.g.anonStructKind(k.comp.names, strs)
		made = ir.NewMakeRecord(pos, bl.f.NewTemp(), k.comp.names, texts)
	} else {
		mk = bl.g.tupleKind(strs)
		made = ir.NewMakeTuple(pos, bl.f.NewTemp(), texts)
	}
	bl.b.Append(made)
	bl.side(made.Dst(), irScalarSide{k: mk})
	r := ir.NewRenderDisplay(pos, bl.f.NewTemp(), made.Dst())
	bl.b.Append(r)
	bl.side(r.Dst(), irScalarSide{k: kindString})
	return r.Dst(), true
}

// nestedDisplayImpls names the Display bodies a rendering by shape of a
// value of kind k and checker type kt calls (analysis.DisplayStructural,
// rt.DisplayShapeText): for each part, at any depth, the rendering a hole
// of the part's type records (displayOf) says how it renders. A String, a
// scalar, a Byte and an interface value name nothing: the machine renders
// the first three as std's impls do, and the value an interface value
// holds through the body the machine holds for its type. A tuple's and an
// anonymous record's parts, and a declared type's whose Display is derived,
// its fields, payloads, embedded values and inner value, render by their
// own records, a recursive type's once. A part of Infallible holds no
// value and names nothing. A List, a Map, a Set and a Vector
// whose record names std's impl render by the machine as that impl writes
// them, each element by its own record. Any other part names the body its
// record names (recordedRenderBody). A part with no checker type renders as
// its kind does (kindDisplay), which names no body; a part whose rendering
// names no body the renderer can call declines the whole.
//
// The machine picks a part's body by the static type of its position
// (vm's renderHook), so a value of an `embeds` enum, which is the embedded
// value at run time, renders through the enum's body where the position is
// the enum's and through the embedded type's own where it is that type's.
func (bl *irScalarBuilder) nestedDisplayImpls(k kind, kt analysis.Type) ([]ir.PartImpl, bool) {
	var impls []ir.PartImpl
	type implKey struct{ name, inst string }
	seen := map[implKey]bool{}
	shaped := map[kind]bool{}
	var walk func(k kind, kt analysis.Type) bool
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
	// comps are the parts of k, a tuple, an anonymous record or a std
	// container, whose rendering the record says is by its parts.
	comps := func(k kind) []kind {
		if k.comp == nil {
			panic("irbuild: a part rendered by its parts lowers to " + k.nomi() + ", a kind with no parts; the checker records parts only for a tuple, an anonymous record or a container")
		}
		return k.comp.parts
	}
	walk = func(k kind, kt analysis.Type) bool {
		if k == kindNever {
			// A part of Infallible holds no value to render.
			return true
		}
		// A part with no checker type renders as its kind does
		// (kindDisplay), which names no body.
		rec := bl.g.displayOf(kt, k)
		switch rec.Form {
		case analysis.DisplayText, analysis.DisplayScalar, analysis.DisplayInterface:
			return true
		case analysis.DisplayParts:
			for i, part := range comps(k) {
				if !walk(part, partCheckerType(kt, k, i)) {
					return false
				}
			}
			return true
		case analysis.DisplayStructural:
			if k.tag != tagNamed || k.def == nil || irStructuralDisplayKind(k) {
				return true
			}
			if _, isSet := setElem(k); isSet {
				// An empty Set (analysis's emptyCollection).
				return true
			}
			if _, vector := vectorElem(k); vector {
				return true
			}
			return parts(k, kt)
		case analysis.DisplayImpl:
			if k == irByteKind() || isDecimalKind(k) || irScalarLeafKind(k) {
				return true
			}
			if std := bl.g.instanceFA().StdFuncOf(rec.Method); machineDisplays[std] {
				if member, isSet := setElem(k); isSet {
					return walk(member, partCheckerType(kt, k, 0))
				}
				if elem, vector := vectorElem(k); vector {
					return walk(elem, partCheckerType(kt, k, 0))
				}
				for i, part := range comps(k) {
					if !walk(part, partCheckerType(kt, k, i)) {
						return false
					}
				}
				return true
			}
			if k.tag != tagNamed || k.def == nil {
				panic("irbuild: a part whose Display is an impl lowers to " + k.nomi() + ", which is no declared type; the checker records an impl only for a declared type or a std container")
			}
			b := bl.recordedRenderBody(stdDisplay, "to_string", k, kt)
			if b.sym == nil {
				return false
			}
			key := implKey{bl.g.irTypeSym(k.def).Name(), bl.g.irValType(k).InstanceKey()}
			if !seen[key] {
				seen[key] = true
				impls = append(impls, ir.PartImpl{Type: key.name, Inst: key.inst, Fn: b.sym})
			}
			return true
		}
		// displayAt answers a type parameter's rendering at the instance,
		// or by the kind.
		panic("irbuild: a part's Display rendering of an unresolved form")
	}
	if !walk(k, kt) {
		return nil, false
	}
	return impls, true
}

// machineDisplays are std's Display impls of the containers the machine
// renders as the impl writes them inside a rendering by shape
// (nestedDisplayImpls, rt.DisplayShapeText): `[a, b]`, `{k => v}`,
// `Set[a]`, `Vector[a]`, each element by its own Display.
var machineDisplays = map[analysis.StdFunc]bool{
	{Module: "std/lists", Owner: "List", Impl: "Display", Name: "to_string"}:     true,
	{Module: "std/maps", Owner: "Map", Impl: "Display", Name: "to_string"}:       true,
	{Module: "std/sets", Owner: "Set", Impl: "Display", Name: "to_string"}:       true,
	{Module: "std/vectors", Owner: "Vector", Impl: "Display", Name: "to_string"}: true,
}

// irStructuralDisplayKind reports a kind whose Display text is structural all
// the way down: the four unnamed scalars, and records, tuples, lists and maps
// composed only of them. No
// nominal type is reachable inside one, so no impl can answer for any part
// and rt.DisplayShapeText is the text for the whole.
func irStructuralDisplayKind(k kind) bool {
	switch {
	case k == kindInt || k == kindFloat || k == kindString || k == kindBool:
		return true
	case irRetainedRecordKind(k) || irRetainedTupleKind(k):
		if k.comp == nil {
			return false
		}
		for _, p := range k.comp.parts {
			if !irStructuralDisplayKind(p) {
				return false
			}
		}
		return true
	case k.tag == tagList && k.comp != nil && len(k.comp.parts) == 1:
		return irStructuralDisplayKind(k.comp.parts[0])
	case k.tag == tagNamed:
		elem, vector := vectorElem(k)
		return vector && irStructuralDisplayKind(elem)
	case k.tag == tagMap && k.comp != nil && len(k.comp.parts) == 2:
		return irStructuralDisplayKind(k.comp.parts[0]) && irStructuralDisplayKind(k.comp.parts[1])
	}
	return false
}
