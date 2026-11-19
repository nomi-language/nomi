package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// irRetainedRangeKind is a Range over any element the checker can make one
// of (a `Comparable` type, spec §3): every such type is a value of the
// callable domain. A Range value
// holds its two ends and nothing more; what reads it (contains, a walk,
// Debug) builds the element's operation itself.
func irRetainedRangeKind(k kind) bool {
	elem, ok := rangeElem(k)
	return ok && irCallableValueKind(elem)
}

// irRangeDistinctElem is a wrapping distinct element, such as Codepoint, whose
// ordering and successor come from its own impls.
func irRangeDistinctElem(elem kind) bool {
	return elem.tag == tagNamed && irWrappingDistinct(elem.def)
}

func (bl *irScalarBuilder) rangeMake(t *ast.RangeLit) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if t.Start == nil || t.End == nil {
		panic("irbuild: rangeMake: parser/checker: `3..` gets a Unit end and is rejected (\"range endpoints must have the same type\"); no open-start syntax")
	}
	start, elem, _, ok := bl.lower(t.Start)
	if !ok {
		return no()
	}
	end, ek, endPure, ok := bl.lower(t.End)
	if !ok || ek != elem {
		return no()
	}
	k := bl.g.rangeKindOf(elem)
	if !irRetainedRangeKind(k) {
		// elem is a lowered value's kind, in the value domain. The checker
		// makes a range only of a Comparable type, which no bare `None`
		// or Infallible is ("range endpoint type List<?1> does not
		// implement Comparable"), so elem is a callable value's kind.
		panic("rangeMake: a Range over " + elem.nomi() + ", which the checker does not make Comparable")
	}
	// The Range holds its ends only. Each reader that orders the element
	// (Range.contains, step_by, a walk, known_count) asks for its
	// comparator (elemComparator) and declines there when it has none.
	n := ir.NewMakeRange(bl.g.irNodePos(t), bl.f.NewTemp(), start, end, t.Inclusive)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: k, pureMake: endPure})
	return n.Dst(), k, endPure, true
}

func (bl *irScalarBuilder) rangeCall(t *ast.Call, args irQualArgs, method string) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if method == "from" || method == "naturals" {
		return bl.rangeFrom(t, args, method)
	}
	if !args.ok || len(args.kinds) == 0 || !irRetainedRangeKind(args.kinds[0]) {
		return no()
	}
	name := "Range." + method
	switch method {
	case "is_bounded":
		if len(args.kinds) != 1 {
			return no()
		}
	case "start", "stop", "is_inclusive":
		// A Range's parts, read off its record with no element method, so
		// a subtype rule generic over a Comparable element reads them as
		// any body does (ClosedRange, BoundedRange).
		if len(args.kinds) != 1 {
			return no()
		}
		elem, _ := rangeElem(args.kinds[0])
		result := kindBool
		switch method {
		case "start":
			result = elem
		case "stop":
			// A Maybe of a program's type is the program's instance, as
			// stepperSignature types a program's step_by.
			maybeElem, shared := bl.g.sharedMaybe(elem)
			if !shared {
				a, anchored := bl.g.preludeNamed("Maybe")
				if !anchored {
					return no()
				}
				maybeElem = bl.g.preludeInstance(a, []kind{elem})
			}
			result = maybeElem
		}
		return bl.qualEmit(t, args, &irQualPlan{token: name, name: name, result: result, host: true})
	case "step_by":
		return bl.rangeStepBy(t, args)
	case "known_count", "is_infinite", "each_while":
		// `impl Iter for Range<T>`'s members, named by the type: the Iter
		// protocol over the Range's view.
		return bl.iterCall(t, args, method, nil)
	case "to_string", "inspect":
		// std's Debug impl for a Range is its Display (`to_string(r)`), so
		// both render as `Debug.inspect(r)` does.
		if len(args.kinds) != 1 {
			return no()
		}
		v, ok := bl.debugOver(t.Args[0], args.temps[0], args.kinds[0], bl.g.operandCheckerType(t.Args[0]))
		if !ok {
			return no()
		}
		return v, kindString, false, true
	case "contains":
		elem, _ := rangeElem(args.kinds[0])
		if len(args.kinds) != 2 || args.kinds[1] != elem {
			return no()
		}
		if elem == kindFloat {
			name = "Range.containsFloat"
			break
		}
		// The comparator is the one `Iter.sort` orders elements by: the
		// program's impl, std's retained Nomi body, or a forwarding body
		// around std's host (Decimal's rt.DecimalCompare), as step_by's is.
		_, elemType := bl.g.recordedTypeArg(t, method, "T")
		ref, ok := bl.elemComparator(t, elem, elemType)
		if !ok {
			return no()
		}
		args.temps = append(args.temps, ref)
	default:
		// A program's own function over a Range goes to its impl
		// (callsProgramFunc), and the checker rejects any other name.
		panic("irbuild: Range." + method + " is no function std declares on a Range")
	}
	return bl.qualEmit(t, args, &irQualPlan{token: name, name: name, result: kindBool, host: true})
}

// rangeFrom builds the unbounded ranges `Range.from(n)`, of any element
// with an order (an Int, a Float, a String), and `Range.naturals()`.
func (bl *irScalarBuilder) rangeFrom(t *ast.Call, args irQualArgs, method string) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	want := 0
	if method == "from" {
		want = 1
	}
	if !args.ok || len(args.kinds) != want {
		return no()
	}
	elem := kindInt
	if want == 1 {
		elem = args.kinds[0]
	}
	k := bl.g.rangeKindOf(elem)
	if !irRetainedRangeKind(k) {
		return no()
	}
	if ok := bl.g.rangeOrders(elem, t); !ok {
		return no()
	}
	start := ir.NoTemp
	if want == 1 {
		start = args.temps[0]
	}
	n := ir.NewMakeRange(bl.g.irNodePos(t), bl.f.NewTemp(), start, ir.NoTemp, false)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: k})
	return n.Dst(), k, false, true
}

// rangeStructLit builds std/ranges' own `Range{start: n, end: None,
// inclusive: False}`, the body of `Range.from` and `Range.naturals`, as the
// unbounded range rangeFrom builds at a call site. The VM holds a Range
// as its own record (MakeRange), not as a declared struct, so only this
// shape is admitted: a start of a kind a range holds, a bare `None` end
// and a `False` inclusive. The call sites never needed the body; a function value
// (`Iter.map(xs, Range.from)`) does.
func (bl *irScalarBuilder) rangeStructLit(t *ast.StructLit) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if len(t.Fields) != 3 {
		panic("irbuild: rangeStructLit: Range is an opaque struct, its literal lowers only in std/ranges, whose two bodies have three fields")
	}
	var start ast.Node
	for _, f := range t.Fields {
		switch f.Name {
		case "start":
			start = f.Value
		case "stop":
			if name, _, _, bare := preludeValueName(f.Value); !bare || name != "None" {
				panic("irbuild: rangeStructLit: std/ranges' Range.from/naturals write `end: None`")
			}
		case "inclusive":
			if name, _, _, bare := preludeValueName(f.Value); !bare || name != "False" {
				panic("irbuild: rangeStructLit: std/ranges' bodies write `inclusive: False`")
			}
		default:
			panic("irbuild: rangeStructLit: std/ranges' bodies name only start, stop and inclusive, not " + f.Name)
		}
	}
	if start == nil {
		panic("irbuild: rangeStructLit: std/ranges' bodies set start")
	}
	v, k, _, ok := bl.lower(start)
	if !ok {
		return no()
	}
	rk := bl.g.rangeKindOf(k)
	if !irRetainedRangeKind(rk) {
		panic("irbuild: rangeStructLit: Range.from<T> is instantiated only at a Comparable T, whose Range is retained")
	}
	n := ir.NewMakeRange(bl.g.irNodePos(t), bl.f.NewTemp(), v, ir.NoTemp, false)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: rk})
	return n.Dst(), rk, false, true
}

// rangeStepBy lowers `Range.step_by(r, by)` as a host call. The element's
// comparator and its `Steppable.step_by` body travel as operands: Go delivers
// both as function references, and the VM walks the range with
// the same rt.StepByRangeSeq driver over the linked functions.
func (bl *irScalarBuilder) rangeStepBy(t *ast.Call, args irQualArgs) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if len(args.kinds) != 2 || !irRetainedValueKind(args.kinds[1]) {
		return no()
	}
	elem, _ := rangeElem(args.kinds[0])
	by := args.kinds[1]
	if ok := bl.g.rangeOrders(elem, t); !ok {
		return no()
	}
	stepSym, stepParams, stepResult, ok := bl.elemStepper(elem, by)
	if !ok {
		bl.g.reject("Range.step_by over an unsteppable element", elem.nomi()+" by "+by.nomi(), t)
		return no()
	}
	_, elemType := bl.g.recordedTypeArg(t, "step_by", "T")
	cmpFn, ok := bl.elemComparator(t, elem, elemType)
	if !ok {
		return no()
	}
	stepRef := ir.NewRefFunc(bl.g.irNodePos(t), bl.f.NewTemp(), stepSym)
	bl.b.Append(stepRef)
	bl.side(stepRef.Dst(), irScalarSide{k: funcKindIn(bl.g, stepParams, stepResult)})
	args.temps = append(args.temps, cmpFn, stepRef.Dst())
	return bl.qualEmit(t, args, &irQualPlan{token: "Range.step_by", name: "Range.step_by", result: seqKindIn(bl.g, elem), host: true})
}

// elemStepper is elem's `Steppable.step_by` at step kind by: the callee
// symbol of its retained body, with its parameter and result kinds. The
// program's impl comes first, this file's or the file declaring elem, as
// sortComparator chooses a comparator; std's impl (Int, Decimal, Codepoint)
// otherwise.
func (bl *irScalarBuilder) elemStepper(elem, by kind) (*ir.Symbol, []kind, kind, bool) {
	g := bl.g
	if irNominalElemKind(elem) || irRangeDistinctElem(elem) {
		for _, d := range g.implsOf(elem) {
			if d.recv != elem || d.ifaceKey != stdIfaceImplKey(stdSteppable) || !d.lowerable {
				continue
			}
			it := d.items["step_by"]
			if it == nil || !it.lowerable || irImplSource(it) == nil || !g.stepperSignature(it.params, it.result, elem, by) {
				continue
			}
			return g.irCalleeSym(it, elem.nomi()+"."+it.name), it.params, it.result, true
		}
		if site, params, result, owning := g.siblingImplMember(elem.def, "step_by", stdIfaceImplKey(stdSteppable)); site != nil && g.stepperSignature(params, result, elem, by) {
			return owning.irCalleeSym(site.item, site.symName), params, result, true
		}
	}
	maybeElem, shared := g.sharedMaybe(elem)
	if !shared {
		return nil, nil, kindInvalid, false
	}
	step := g.stdIfaceFnAt("Steppable.step_by", elem, []kind{elem, by}, maybeElem)
	if step == nil || step.irBody == nil {
		return nil, nil, kindInvalid, false
	}
	return step.irBody.Sym(), step.params, step.result, true
}

// stepperSignature reports whether a program impl's `step_by` is
// `(elem, by): Maybe<elem>`.
func (g *gen) stepperSignature(params []kind, result, elem, by kind) bool {
	if len(params) != 2 || params[0] != elem || params[1] != by {
		return false
	}
	a, anchored := g.preludeNamed("Maybe")
	return anchored && result == g.preludeInstance(a, []kind{elem})
}

// discreteRef is elem's `Discrete.<name>` as a function value, chosen as
// `Iter.sort` chooses a comparator (sortComparator): this file's impl, the
// declaring file's, or std's retained body at stdResult. A user distinct
// (`type Id Int` with `impl Discrete for Id`) walks and counts by its own
// impl.
func (bl *irScalarBuilder) discreteRef(at ast.Node, elem kind, name string, params []kind, stdResult kind) (ir.Temp, bool) {
	pos := bl.g.irNodePos(at)
	ref := func(sym *ir.Symbol, result kind) (ir.Temp, bool) {
		r := ir.NewRefFunc(pos, bl.f.NewTemp(), sym)
		bl.b.Append(r)
		bl.side(r.Dst(), irScalarSide{k: funcKindIn(bl.g, params, result)})
		return r.Dst(), true
	}
	if irNominalElemKind(elem) || irRangeDistinctElem(elem) {
		if it := bl.localDiscreteItem(elem, name, params); it != nil {
			return ref(bl.g.irCalleeSym(it, elem.nomi()+"."+it.name), it.result)
		}
		if sym, result := bl.siblingDiscreteSym(elem, name, params); sym != nil {
			return ref(sym, result)
		}
	}
	f := bl.g.stdIfaceFnAt("Discrete."+name, elem, params, stdResult)
	if f == nil || f.irBody == nil {
		return ir.NoTemp, false
	}
	return ref(f.irBody.Sym(), f.result)
}

// localDiscreteItem is this file's `impl Discrete for elem` member name,
// written or inherited, with a body the builder can retain.
func (bl *irScalarBuilder) localDiscreteItem(elem kind, name string, params []kind) *implItem {
	impl := bl.g.implsByIface[stdIfaceImplKey(stdDiscrete)][elem]
	if impl == nil || !impl.lowerable {
		return nil
	}
	it := impl.items[name]
	if it == nil || !it.lowerable || len(it.params) != len(params) {
		return nil
	}
	for i, p := range params {
		if it.params[i] != p {
			return nil
		}
	}
	return it
}

// siblingDiscreteSym is the callee symbol and result of the `impl Discrete
// for elem` member name another file of the program writes for the type it
// declares, as siblingCompareSym finds a sibling's compare, or nil.
func (bl *irScalarBuilder) siblingDiscreteSym(elem kind, name string, params []kind) (*ir.Symbol, kind) {
	if elem.tag != tagNamed || elem.def == nil {
		return nil, kindInvalid
	}
	call := &ast.Call{Args: make([]ast.Node, len(params))}
	args := irQualArgs{temps: make([]ir.Temp, len(params)), kinds: params, mobile: make([]bool, len(params)), ok: true}
	for i := range args.temps {
		args.temps[i], args.mobile[i] = ir.NoTemp, true
	}
	p := bl.qualSiblingIfaceImplPlan(call, args, elem.def, name, stdIfaceImplKey(stdDiscrete))
	if p == nil || p.sym == nil {
		return nil, kindInvalid
	}
	return p.sym, p.result
}

// rangeWalksAt is gen.rangeWalks for an element whose Discrete impl the
// program writes: std's impls answer only its own types, and discreteRef
// resolves the program's (declining when it names none).
func (bl *irScalarBuilder) rangeWalksAt(elem kind, at ast.Node) bool {
	if bl.ownsDiscrete(elem) {
		return bl.g.rangeOrders(elem, at)
	}
	return bl.g.rangeWalks(elem, at)
}

// ownsDiscrete reports whether the program writes elem's `Discrete.next`,
// in this file or the one declaring elem.
func (bl *irScalarBuilder) ownsDiscrete(elem kind) bool {
	if !irNominalElemKind(elem) && !irRangeDistinctElem(elem) {
		return false
	}
	if bl.localDiscreteItem(elem, "next", []kind{elem}) != nil {
		return true
	}
	sym, _ := bl.siblingDiscreteSym(elem, "next", []kind{elem})
	return sym != nil
}

// inheritsStepsBetween reports whether elem's Discrete impl is the
// program's and leaves `steps_between` to the interface's default, which
// answers None: a Range of elem then has no count, which a view with no
// Count answers.
func (bl *irScalarBuilder) inheritsStepsBetween(elem kind) bool {
	if !bl.ownsDiscrete(elem) || bl.localDiscreteItem(elem, "steps_between", []kind{elem, elem}) != nil {
		return false
	}
	sym, _ := bl.siblingDiscreteSym(elem, "steps_between", []kind{elem, elem})
	return sym == nil
}
