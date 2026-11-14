package irbuild

import (
	"cmp"
	"slices"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// Boxed existentials.
//
// The VM's existential is the concrete value, and a dispatch selects the impl
// by the type name the value carries (vm.implOf). Two kinds of value carry no
// name that identifies their impl of a program's interface:
//
//   - a std container instance (`List<Int>`, `Map<String, Int>`, `Maybe<T>`,
//     a Set, a Vector, a Range): its runtime value names none of its type
//     arguments, so `impl Show for List<Int>` and `impl Show for
//     List<String>` cannot be told apart, and no descriptor names the
//     program's impl at all;
//   - a value of an enum with an `embeds` variant: a value widened into
//     that variant is the embedded value itself, which names the embedded
//     type and not the enum.
//
// Where such a value becomes an interface value, and only there, the builder
// boxes it (ir.MakeExistential, rt.Box): a record whose descriptor names the
// static type the checker knew at the erasure. The impls of that type are
// recorded under the same name (irRecordBoxedImpls, and irRecordImpls for an
// enum), the dispatch selects by the box's name and passes the value inside
// (vm.unboxSelves), equality compares two boxes by their names and then their
// values, and everything else sees through the box (rt.Unbox). A
// value whose own runtime type names its impl is erased unchanged
// (irErases), so a struct, a plain enum or a scalar costs nothing.
//
// Where a written expression becomes an interface value is the checker's
// record (analysis.FileAnalysis.Coercions): the interface, the value's type
// and the impl block. coerceRecorded makes the value from it, at an
// annotated binding, a struct or record literal's field, a struct field's
// default and a qualified call's operand; coerceEmpty, which every other
// position converts through, names the record's impl when the expression
// has one. A value enters an interface inside a conversion the checker
// records as a whole, with no record of its own: a function's result
// through an adapter (irfuncwiden.go), a `?` operand's error, an element a
// view converts as it reads it, an operand of `==` against an interface
// value. There the box's impl is the program's one impl of the interface
// instance for the value's kind, which the checker's coherence makes one.
// The kinds that entered each interface, which a Debug rendering of a held
// value names (existentialDebugImpls), are the records' too
// (checkedErasures).

// irBoxes is one unit's boxing state.
type irBoxes struct {
	plans map[irBoxKey]*irBoxPlan
	// order is the pairs with a plan, in the order they were first boxed.
	order []irBoxKey
	// impls are the dispatch entries this unit's container boxes need,
	// recorded on its module once every body is lowered.
	impls []irBoxedImpl
	// enums is the enums this unit boxes, for a program with no registry.
	enums map[ast.Node]bool
}

type irBoxKey struct {
	iface *ifaceDef
	have  kind
}

// irBoxPlan is how a value of one kind is boxed into one existential: the
// symbol its impls are recorded under, which names the box, and the
// container impl entries that symbol needs (none for an enum, whose own
// impls irRecordImpls records).
type irBoxPlan struct {
	typ   *ir.Symbol
	impls []irBoxedImpl
	// noted is whether the unit's module has the impls queued.
	noted bool
}

// irBoxedImpl is one dispatch entry a container box needs: method's impl for
// the box's type is fn, a body owner retains. A nil method is std's Display,
// recorded for an erased rendering to find by the box's name.
type irBoxedImpl struct {
	method, typ, fn *ir.Symbol
	owner           *gen
}

// irBoxTypeToken identifies the symbol a boxed container's impls are
// recorded under, named by the container instance's stored type
// (`List<Int>`, `Map<String, main.Point>`).
type irBoxTypeToken struct{ key string }

// irBoxPlanFor is the plan that boxes have into want, or nil when have
// enters want unboxed or not at all. impl is the impl block the checker
// recorded for the coercion (analysis.Coercion.Impl), nil for a value
// entering an interface inside a conversion the checker records as a whole
// (a function's result through an adapter, a `?` operand's error, an
// operand of `==` against an interface value), where the program's one
// impl of the interface instance for have is the impl.
func (g *gen) irBoxPlanFor(want, have kind, impl *ast.ImplBlock) *irBoxPlan {
	if irConjKind(want) {
		return g.conjBoxPlan(want, have, nil)
	}
	if !irExistentialKind(want) {
		return nil
	}
	if g.stdModule != "" && !(g.stdCases && want.iface.stdOrigin != "") {
		// A std module boxes nothing, but its cases are a program, which
		// holds a std container or leaf as a std interface value
		// (stdprogiface.go) as any program does.
		return nil
	}
	d := want.iface
	if d.universal || d.decl == nil || !d.lowerable {
		return nil
	}
	if d.stdShared && !irStdBoxingIface(d) {
		return nil
	}
	key := irBoxKey{iface: d, have: have}
	if p, done := g.boxes.plans[key]; done {
		return p
	}
	if g.boxes.plans == nil {
		g.boxes.plans = map[irBoxKey]*irBoxPlan{}
	}
	// Recorded before planning, which registers impls and may ask again.
	g.boxes.plans[key] = nil
	p := g.irBoxPlan(d, have, impl)
	g.boxes.plans[key] = p
	if p != nil {
		g.boxes.order = append(g.boxes.order, key)
	}
	return p
}

// irStdBoxingIface reports whether d is a std interface whose existential
// boxes: std's Display and Debug, the two a program holds values as. The
// box gives a container or an `embeds` enum's value the identity a program
// interface's box gives it, so `==`, hashing and an erased Display rendering
// tell a `List<Int>` from a `List<String>` and a `Shape` from the `Circle`
// it embeds. Comparable's two `self` operands refuse every call on its
// existential, and Struct is universal (structs only).
func irStdBoxingIface(d *ifaceDef) bool {
	return d.stdShared && !d.universal && (d.nomi == "Display" || d.nomi == "Debug")
}

func (g *gen) irBoxPlan(d *ifaceDef, have kind, impl *ast.ImplBlock) *irBoxPlan {
	if d.stdShared {
		return g.irStdBoxPlan(d, have)
	}
	if irBoxedEnum(have) {
		if !g.implements(d, have) {
			return nil
		}
		if have.def.genericOf != nil {
			// An instance's impls are recorded under its instance key
			// (irInstanceImpls), as an erased generic struct's are.
			if g.erasedInstances == nil {
				g.erasedInstances = map[kind]bool{}
			}
			g.erasedInstances[have] = true
			if g.reg != nil {
				g.reg.markErased(have.def)
			}
		}
		g.markBoxedEnum(have.def)
		return &irBoxPlan{typ: g.irImplTypeSym(have)}
	}
	if _, scalar := irDispatchScalarName(have); scalar {
		return nil
	}
	vt := g.irValType(have)
	if vt == nil || vt.Erased() {
		return nil
	}
	if _, _, container := stdRecvParts(have); !container && !irStdLeafBoxes(have, vt) && !(d.stdOrigin != "" && irStructuralBoxes(have)) {
		return nil
	}
	name := vt.String()
	p := &irBoxPlan{typ: g.irTypes().Symbol(irBoxTypeToken{name}, name)}
	if d.stdOrigin != "" {
		// A std interface over a std type: std's impl, reached as a direct
		// call reaches it (stdprogiface.go). The orphan rule leaves the
		// program no impl of its own here.
		impls, ok := g.stdProgBoxImpls(d, have, p.typ)
		if !ok {
			return nil
		}
		p.impls = impls
		return p
	}
	for _, m := range d.order {
		var found *stdRecvCand
		for _, c := range g.stdRecvCandidates(have, m.name, d.nomi, nil) {
			if c.d.iface == nil || !sameIfaceInstance(c.d.iface, d) || (impl != nil && c.d.decl != impl) {
				continue
			}
			if found != nil {
				// Two impls of one instance for one kind: the checker's
				// coherence refuses it, and a box could not choose.
				return nil
			}
			found = &c
		}
		if found == nil {
			if m.dispatchable() {
				return nil
			}
			continue
		}
		it := found.d.items[m.name]
		if it == nil || !it.lowerable || irImplSource(it) == nil {
			if m.dispatchable() {
				return nil
			}
			continue
		}
		method := g.irIfaceMethodSym(d, m)
		if method == nil {
			return nil
		}
		fn := found.g.irCalleeSym(it, found.d.recv.nomi()+"."+it.name)
		p.impls = append(p.impls, irBoxedImpl{method: method, typ: p.typ, fn: fn, owner: found.g})
	}
	return p
}

// irStdLeafBoxes reports whether have, a value of a std type, enters a
// program interface's existential boxed: a std type whose value is no record
// (`Decimal`, `Instant`, `Bytes`) names no type the dispatch can read, so
// `impl ToSql for Decimal` is recorded under the box's name. A std struct or
// enum (`Date`, `Json`) is a record naming its type, and enters unboxed.
func irStdLeafBoxes(have kind, vt *ir.ValType) bool {
	if _, std := stdNamedRecv(have); !std {
		return false
	}
	k := vt.Kind()
	return k != ir.KindStruct && k != ir.KindEnum
}

// irStructuralBoxes reports whether have, a tuple or an anonymous record,
// enters a std interface's existential boxed: neither has a declaration
// whose name a dispatch could read, so `Hashable.hash` of one held as
// `Hashable` selects the box's thunk, which hashes it as a direct call does.
func irStructuralBoxes(have kind) bool {
	return irRetainedTupleKind(have) || irRetainedRecordKind(have)
}

// irStdBoxPlan is the plan that boxes have into std's Display or Debug
// existential d. Neither dispatches through the module's impl tables, so the
// plan records no impls and only names the box: an erased Display rendering
// selects the recorded `impl Display` of the box's name (an `embeds` enum's,
// irRecordDisplayImpls) or renders a container structurally
// (vm.displayErased), and Debug renders the value inside. A container enters
// Display boxed only where its Display text is structural
// (irDisplayErasedContainer), as it entered unboxed before.
func (g *gen) irStdBoxPlan(d *ifaceDef, have kind) *irBoxPlan {
	if irBoxedEnum(have) {
		if !g.implements(d, have) {
			return nil
		}
		if have.def.genericOf != nil {
			// An instance's Display is recorded under its instance key
			// (irInstanceImpls), which names the box.
			if g.erasedInstances == nil {
				g.erasedInstances = map[kind]bool{}
			}
			g.erasedInstances[have] = true
			if g.reg != nil {
				g.reg.markErased(have.def)
			}
		}
		return &irBoxPlan{typ: g.irImplTypeSym(have)}
	}
	if _, std := stdNamedRecv(have); std || irStdGenericInstance(have) {
		if _, _, container := stdRecvParts(have); !container {
			return g.irStdNamedBoxPlan(d, have)
		}
	}
	if _, _, container := stdRecvParts(have); !container {
		return nil
	}
	var display *irBoxedImpl
	if d.nomi == "Display" {
		structural := g.irDisplayErasedContainer(existential(d), have)
		if !structural || irHoldsBoxedEnum(have) {
			// A container whose text is not its shape around its elements'
			// (a Maybe, a Result, a Set), or one holding an `embeds` enum's
			// values, whose runtime type is the struct each embeds, so a
			// structural rendering would select the struct's impl. The box
			// renders through std's impl instantiated at the container's
			// own kind, as `"${v}"` does.
			fn, owner := g.irContainerDisplayInstance(have)
			if fn == nil {
				return nil
			}
			display = &irBoxedImpl{fn: fn, owner: owner}
		}
	}
	vt := g.irValType(have)
	if vt == nil || vt.Erased() {
		return nil
	}
	name := vt.String()
	p := &irBoxPlan{typ: g.irTypes().Symbol(irBoxTypeToken{name}, name)}
	if display != nil {
		display.typ = p.typ
		p.impls = []irBoxedImpl{*display}
	}
	return p
}

// irStdNamedBoxPlan is the plan that boxes have, a std type that is no
// container (`Codepoint`, `Duration`, `Date`, `Byte`), into std's Display or
// Debug existential d, when it does not enter unboxed (irErases). Its value
// names no Display body an erased rendering can call: std's impl may be a
// host function (`Codepoint.to_string`) or a body this unit never lowered.
// So the box names a thunk whose body is the direct call
// `Display.to_string(v)` (stdProgThunk), which reaches whatever that call
// reaches, and an erased rendering selects it by the box's name. Debug needs
// no entry: the renderer names the type's Debug body as a container's leaf
// (existentialDebugImpls over the unit's boxes).
func (g *gen) irStdNamedBoxPlan(d *ifaceDef, have kind) *irBoxPlan {
	if g.irErases(existential(d), have) {
		return nil
	}
	vt := g.irValType(have)
	if vt == nil || vt.Erased() {
		return nil
	}
	name := vt.String()
	p := &irBoxPlan{typ: g.irTypes().Symbol(irBoxTypeToken{name}, name)}
	if d.nomi == "Display" {
		m := d.methods["to_string"]
		if m == nil {
			return nil
		}
		fn := g.stdProgThunk(d, m, have, name)
		if fn == nil {
			return nil
		}
		p.impls = []irBoxedImpl{{fn: fn, typ: p.typ, owner: g}}
	}
	return p
}

// irHoldsBoxedEnum reports whether a value of kind k holds, at any depth of
// its containers, tuples and records, a value of an enum that boxes
// (irBoxedEnum).
func irHoldsBoxedEnum(k kind) bool {
	if irBoxedEnum(k) {
		return true
	}
	var parts []kind
	if _, ps, container := stdRecvParts(k); container {
		parts = ps
	} else if (irRetainedRecordKind(k) || irRetainedTupleKind(k)) && k.comp != nil {
		parts = k.comp.parts
	}
	for _, p := range parts {
		if irHoldsBoxedEnum(p) {
			return true
		}
	}
	return false
}

// irDisplayInstanceContainer reports whether have is a std container whose
// Display std answers with an impl instantiated at have, and whose text is
// not structural (irDisplayErasedContainer): a Maybe, a Result or a Set.
func (g *gen) irDisplayInstanceContainer(have kind) bool {
	if _, _, container := stdRecvParts(have); !container || g.stdModule != "" {
		return false
	}
	fn, _ := g.irContainerDisplayInstance(have)
	return fn != nil
}

// irContainerDisplayInstance is std's `Display.to_string` for the container
// kind have, instantiated at have, and the gen that builds it; nil when std
// has no such instance.
func (g *gen) irContainerDisplayInstance(have kind) (*ir.Symbol, *gen) {
	f := g.stdGenericTemplate(irContainerBaseName(have), "to_string")
	if f == nil {
		return nil, nil
	}
	tps, ib := g.stdInsts.stdTemplateParams(f)
	if ib == nil || ib.Interface == nil || typeText(ib.Interface) != "Display" {
		return nil, nil
	}
	args, ok := g.stdRecvTypeArgs(tps, ib, have, false)
	if !ok {
		return nil, nil
	}
	inst, _ := g.stdInsts.instantiate(f, tps, args, g)
	if inst == nil {
		return nil, nil
	}
	return inst.sym, inst.gen
}

// sameIfaceInstance reports whether a and b are one interface at one set of
// type arguments: the declaration, and for a generic interface's instance,
// the arguments.
func sameIfaceInstance(a, b *ifaceDef) bool {
	if a == b {
		return true
	}
	if a.decl != b.decl || (a.instOf == nil) != (b.instOf == nil) {
		return false
	}
	return a.instOf == nil || portableArgsKey(a.instArgs) == portableArgsKey(b.instArgs)
}

// irBoxedEnum reports whether k is a program enum a dispatch cannot select
// an impl for by the value alone: one with an `embeds` variant, whose
// widened values are the embedded values themselves (irDispatchEnum).
func irBoxedEnum(k kind) bool {
	return k.tag == tagNamed && k.def != nil && k.def.isEnum && k.def.preludeOf == nil && !irDispatchEnum(k.def) &&
		irRetainedValueKind(k)
}

// markBoxedEnum records that some file boxes values of d, so the file that
// declares d's impls records them under d's own name.
func (g *gen) markBoxedEnum(d *typeDef) {
	decl := instIdentity(d).decl
	if d.genericOf != nil && d.genericOf.decl != nil {
		decl = d.genericOf.decl
	}
	if decl == nil {
		return
	}
	if g.reg != nil {
		if g.reg.boxedEnums == nil {
			g.reg.boxedEnums = map[string]bool{}
		}
		g.reg.boxedEnums[declKey(decl)] = true
		return
	}
	if g.boxes.enums == nil {
		g.boxes.enums = map[ast.Node]bool{}
	}
	g.boxes.enums[decl] = true
}

// boxedEnumDecl reports whether some file of the program boxes values of the
// enum d.
func (g *gen) boxedEnumDecl(d *typeDef) bool {
	decl := d.decl
	if d.genericOf != nil && d.genericOf.decl != nil {
		decl = d.genericOf.decl
	}
	if decl == nil {
		return false
	}
	if g.reg != nil {
		return g.reg.boxedEnums[declKey(decl)]
	}
	return g.boxes.enums[decl]
}

// coercionAt is the checker's record of the interface value the
// expression n becomes (analysis.FileAnalysis.Coercions), nil when it
// becomes none. A copy of a written expression the builder lowers (a pipe
// stage with its subject spliced in) keeps the written one's position and
// node type, and finds the record by them.
func (g *gen) coercionAt(n ast.Node) *analysis.Coercion {
	fa := g.srcFA()
	if fa == nil || n == nil || len(fa.Coercions) == 0 {
		return nil
	}
	if rec := fa.Coercions[n]; rec != nil {
		return rec
	}
	if g.coercionsFA != fa {
		g.coercionsFA = fa
		g.coercionsAt = make(map[irCoercionPos]*analysis.Coercion, len(fa.Coercions))
		for c, rec := range fa.Coercions {
			line, col := nodePos(c)
			g.coercionsAt[irCoercionPos{line, col, c.NodeType()}] = rec
		}
	}
	line, col := nodePos(n)
	return g.coercionsAt[irCoercionPos{line, col, n.NodeType()}]
}

// irCoercionPos finds a coercion record by its expression's position and
// node type.
type irCoercionPos struct {
	line, col int
	node      string
}

// irCoercion is how a value entered an interface value.
type irCoercion uint8

const (
	// irNotCoerced: the checker recorded no coercion at the expression,
	// or one the value does not enter.
	irNotCoerced irCoercion = iota
	// irCoercedUnboxed: the value is the interface value unchanged, its
	// runtime type naming its impl (irErases).
	irCoercedUnboxed
	// irCoercedBoxed: the value is boxed (eraseBoxed).
	irCoercedBoxed
)

// coerceRecorded makes v, the value of the expression at with kind have,
// the interface value the checker recorded there (coercionAt): boxed when
// have's runtime value names no impl of the interface, else v itself at
// the interface's kind. It is the one place a written expression becomes
// an interface value. want is the position's kind, which the record's
// interface must be, or kindInvalid where the position's kind is not known
// yet (a qualified call's operand, lowered before its callee is selected).
// The answer is irNotCoerced, with v and have, when the checker recorded
// none, or have is already the interface's kind.
func (bl *irScalarBuilder) coerceRecorded(at ast.Node, v ir.Temp, have, want kind) (ir.Temp, kind, irCoercion) {
	rec := bl.g.coercionAt(at)
	if rec == nil {
		return v, have, irNotCoerced
	}
	// kindInvalid: sentinel — the position's kind is not known yet, and the record's interface is it.
	if recorded := bl.g.project(rec.Into()); want == kindInvalid {
		want = recorded
	} else if recorded != want {
		return v, have, irNotCoerced
	}
	if want == have || !irExistentialKind(want) && !irConjKind(want) {
		return v, have, irNotCoerced
	}
	var out ir.Temp
	var how irCoercion
	if irConjKind(want) {
		out, how = bl.intoConjunction(at, v, want, have, rec)
	} else {
		out, how = bl.intoExistential(at, v, want, have, rec.Impl)
	}
	if how == irNotCoerced {
		return v, have, irNotCoerced
	}
	return out, want, how
}

// intoExistential makes v, a value of kind have, a value of the existential
// want: boxed when have's runtime value names no impl of want's interface
// (irBoxPlanFor), unchanged when it does (irErases). impl is the impl the
// checker recorded, or nil (irBoxPlanFor).
func (bl *irScalarBuilder) intoExistential(at ast.Node, v ir.Temp, want, have kind, impl *ast.ImplBlock) (ir.Temp, irCoercion) {
	if p := bl.g.irBoxPlanFor(want, have, impl); p != nil {
		return bl.eraseBoxed(at, v, want, p), irCoercedBoxed
	}
	if bl.g.irErases(want, have) {
		return v, irCoercedUnboxed
	}
	return v, irNotCoerced
}

// recordedImpl is the impl the checker recorded for a value of kind have
// entering want at the expression at: the coercion of the expression
// itself, or a part of its conversion (analysis.FileAnalysis.Conversions:
// a `List<Int>` entering Display inside a `Maybe<List<Int>>` converted to a
// `Maybe<Display>`). Nil when it recorded none there.
func (g *gen) recordedImpl(at ast.Node, want, have kind) *ast.ImplBlock {
	if rec := g.coercionAt(at); rec != nil && g.project(rec.Into()) == want {
		return rec.Impl
	}
	if rec := g.conversionAt(at); rec != nil {
		for _, b := range rec.Boxes {
			if b.Impl != nil && g.project(b.Into()) == want && g.project(b.Value) == have {
				return b.Impl
			}
		}
	}
	return nil
}

// conversionAt is the checker's record of the conversion of the expression
// n below its top (analysis.FileAnalysis.Conversions), nil when it has
// none, found by position for a copy of a written expression as
// coercionAt finds one.
func (g *gen) conversionAt(n ast.Node) *analysis.Conversion {
	fa := g.srcFA()
	if fa == nil || n == nil || len(fa.Conversions) == 0 {
		return nil
	}
	if rec := fa.Conversions[n]; rec != nil {
		return rec
	}
	if g.conversionsFA != fa {
		g.conversionsFA = fa
		g.conversionsAt = make(map[irCoercionPos]*analysis.Conversion, len(fa.Conversions))
		for c, rec := range fa.Conversions {
			line, col := nodePos(c)
			g.conversionsAt[irCoercionPos{line, col, c.NodeType()}] = rec
		}
	}
	line, col := nodePos(n)
	return g.conversionsAt[irCoercionPos{line, col, n.NodeType()}]
}

// eraseBoxed boxes v as the existential want by the plan p: the one
// instruction that makes the static type's identity explicit where the
// value becomes an interface value.
func (bl *irScalarBuilder) eraseBoxed(at ast.Node, v ir.Temp, want kind, p *irBoxPlan) ir.Temp {
	bl.g.noteBoxedImpls(p)
	pure := bl.effectFree(v)
	mk := ir.NewMakeExistential(bl.g.irNodePos(at), bl.f.NewTemp(), p.typ, v)
	bl.b.Append(mk)
	bl.side(mk.Dst(), irScalarSide{k: want, pureMake: pure})
	return mk.Dst()
}

// noteBoxedImpls queues p's dispatch entries for this unit's module, once
// per plan.
func (g *gen) noteBoxedImpls(p *irBoxPlan) {
	if p.noted {
		return
	}
	p.noted = true
	g.boxes.impls = append(g.boxes.impls, p.impls...)
}

// irRecordBoxedImpls records each container box's dispatch entry whose body
// its owner retained.
func (g *gen) irRecordBoxedImpls() {
	for _, e := range g.boxes.impls {
		if !e.owner.retains(e.fn) {
			if e.method != nil {
				g.irMod.ImplementUnretained(e.method)
			}
			continue
		}
		if e.method == nil {
			// std's Display, which an erased rendering finds by the box's
			// name (ir.Module.ImplementDisplay).
			g.irMod.ImplementDisplay(e.typ, e.fn)
			continue
		}
		g.irMod.Implement(e.method, e.typ, e.fn)
	}
}

// checkedErasures is, per interface of the program, the kinds of the values
// the checker recorded entering it in this unit's source
// (analysis.FileAnalysis.Coercions): what a body of the unit may box into
// it. Read once per gen from the record, so the answer does not depend on
// which bodies are lowered first. A coercion whose types name a type
// parameter (in a generic body) is left out; its instances are typed at
// their own call sites.
//
// The interfaces, and each one's kinds, are in source order of the value
// that first entered it, not the record's map order: the answer orders a
// Render's impls (existentialDebugImpls), which are encoded, so a map order
// here would make a lowering's bytes differ from one process to the next.
func (g *gen) checkedErasures() []checkedErasure {
	if g.erasures != nil {
		return g.erasures
	}
	g.erasures = []checkedErasure{}
	fa := g.srcFA()
	if fa == nil {
		return g.erasures
	}
	type entry struct {
		rec                *analysis.Coercion
		line, col          int
		node, haveT, wantT string
	}
	entries := make([]entry, 0, len(fa.Coercions))
	for n, rec := range fa.Coercions {
		if analysis.ContainsTypeParam(rec.Value) || analysis.ContainsTypeParam(rec.Into()) {
			continue
		}
		line, col := nodePos(n)
		entries = append(entries, entry{rec: rec, line: line, col: col,
			node: n.NodeType(), haveT: rec.Value.String(), wantT: rec.Into().String()})
	}
	slices.SortFunc(entries, func(a, b entry) int {
		return cmp.Or(cmp.Compare(a.line, b.line), cmp.Compare(a.col, b.col),
			cmp.Compare(a.node, b.node), cmp.Compare(a.wantT, b.wantT), cmp.Compare(a.haveT, b.haveT))
	})
	at := map[*ifaceDef]int{}
	seen := map[*ifaceDef]map[kind]bool{}
	for _, e := range entries {
		hk := g.project(e.rec.Value)
		if hk.tag == tagInvalid || irHeldKind(hk) {
			continue
		}
		// A value entering a conjunction enters each of its members.
		for _, wk := range conjMembers(g.project(e.rec.Into())) {
			if wk.iface == nil {
				continue
			}
			i, ok := at[wk.iface]
			if !ok {
				i = len(g.erasures)
				at[wk.iface] = i
				g.erasures = append(g.erasures, checkedErasure{iface: wk.iface})
				seen[wk.iface] = map[kind]bool{}
			}
			if !seen[wk.iface][hk] {
				seen[wk.iface][hk] = true
				g.erasures[i].kinds = append(g.erasures[i].kinds, hk)
			}
		}
	}
	return g.erasures
}

// checkedErasure is the kinds checkedErasures found entering one interface.
type checkedErasure struct {
	iface *ifaceDef
	kinds []kind
}
