package irbuild

import (
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

// irBoxedErasure reports whether a value of kind have enters the program
// interface existential want boxed.
func (g *gen) irBoxedErasure(want, have kind) bool {
	return g.irBoxPlanFor(want, have) != nil
}

// irBoxPlanFor is the plan that boxes have into want, or nil when have
// enters want unboxed or not at all.
func (g *gen) irBoxPlanFor(want, have kind) *irBoxPlan {
	if !irExistentialKind(want) || g.stdModule != "" {
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
	p := g.irBoxPlan(d, have)
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

func (g *gen) irBoxPlan(d *ifaceDef, have kind) *irBoxPlan {
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
				if g.reg.erasedInsts == nil {
					g.reg.erasedInsts = map[*typeDef]bool{}
				}
				g.reg.erasedInsts[instIdentity(have.def)] = true
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
	if _, _, container := stdRecvParts(have); !container && !irStdLeafBoxes(have, vt) {
		return nil
	}
	name := vt.String()
	p := &irBoxPlan{typ: g.irTypes().Symbol(irBoxTypeToken{name}, name)}
	for _, m := range d.order {
		var found *stdRecvCand
		for _, c := range g.stdRecvCandidates(have, m.name, d.nomi, nil) {
			if c.d.iface == nil || !sameIfaceInstance(c.d.iface, d) {
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
				if g.reg.erasedInsts == nil {
					g.reg.erasedInsts = map[*typeDef]bool{}
				}
				g.reg.erasedInsts[instIdentity(have.def)] = true
			}
		}
		return &irBoxPlan{typ: g.irImplTypeSym(have)}
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
	args, ok := g.stdInstSolve(f, tps, ib, []kind{have}, kindString, have)
	if !ok {
		return nil, nil
	}
	inst, _ := g.stdInsts.instantiate(f, tps, args, nil, g)
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
			g.reg.boxedEnums = map[ast.Node]bool{}
		}
		g.reg.boxedEnums[decl] = true
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
		return g.reg.boxedEnums[decl]
	}
	return g.boxes.enums[decl]
}

// eraseBoxed boxes v, a value of kind have, as the existential want: the
// one instruction that makes the static type's identity explicit where the
// value becomes an interface value. The caller has asked irBoxedErasure.
func (bl *irScalarBuilder) eraseBoxed(at ast.Node, v ir.Temp, want, have kind) ir.Temp {
	p := bl.g.irBoxPlanFor(want, have)
	if p == nil {
		// The caller asks irBoxedErasure first, whose answer is memoized.
		panic("irbuild: eraseBoxed of " + have.nomi() + " as " + want.nomi() + ", which irBoxedErasure refuses")
	}
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
		if e.owner == nil || e.owner.irMod == nil || e.owner.irMod.FuncFor(e.fn) == nil {
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
