package irbuild

import (
	"fmt"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// A USER-DECLARED generic struct, MONOMORPHIZED per instantiation.
//
// # Five populations under one key
//
// `generic type` is reported from several sites and covers five populations
// that need five different things:
//
//	A  a generic DECLARATION             types.go declModifiers
//	B  an instantiation at CONCRETE args  sigreason.go recordedTypeRefusal
//	C  an instantiation at a TYPE PARAM   sigreason.go recordedTypeRefusal
//	D  a BARE type parameter              sigreason.go
//	E  a STDLIB generic named type        sigreason.go recordedTypeRefusal
//
// This file is A and B. D is a generic FUNCTION's type parameter and belongs to
// the dictionary seam dict.go implements. E is `Channel<Int>` and
// `Type<Marker>`, served by stdgenstruct.go rows. C — `p: Pair<E>`,
// `h: Holder<T>`, `value: Wrapper<T>` — is a template instantiated at ANOTHER
// declaration's type parameter, and it is walled here: there is no concrete
// argument at that position, so there is nothing to monomorphize AT. It is
// reached when the enclosing generic FUNCTION is itself monomorphized per call
// site (genericmono.go).
//
// # One def per instantiation
//
// `kind` is `{tag, *typeDef}` and a named type's identity is its `*typeDef`
// pointer (types.go's header), so every downstream mechanism (field access,
// boxing, slot layout, the impl tables keyed on `kind`) reads a def with
// concrete field kinds. One def per instantiation reuses all of it:
// `Box<Inner>` is an ordinary struct def whose field kind was computed under a
// substitution, and nothing downstream can tell.
//
// That is also what stdgenstruct.go and prelude.go do for the std and prelude
// families: `buildGenStructDef` and `buildPreludeDef` each construct one
// `*typeDef` per argument tuple. This is the same mechanism over a user
// declaration.
//
// # Per gen, not process-wide
//
// stdgenstruct.go interns process-wide because every admitted argument is
// `packageNeutral`. A user instance cannot be: `Box<Inner>`'s field kind is
// this gen's def for `Inner`. So the intern table is `g.genericInsts`, per
// gen. Another file's instance is named through foreign.go's mirror
// (foreigngeneric.go).
//
// # A declaration-level bound is admitted
//
// A `where T: Comparable` on the DECLARATION is admitted. A concrete type
// argument discharges it, and a violating instantiation never reaches the
// builder, because the front end rejects it (see genericimpl.go's
// registerInstanceImpls).
//
// # Impls on an instance
//
// `g.implsByIface[iface][kind]` is keyed on the `*typeDef` pointer, and an
// instance def is a NEW pointer. genericimpl.go registers every impl block
// naming the template once per instance, so `==`, `Display` and ordering find
// the user's own impl rather than a structural fallback.
//
// # The rendered name is the bare declaration name
//
// `nomi` is the name the derived Debug rendering shows and what
// `qualifiedNomiName` module-qualifies for the type's run-time name:
//
//	b = Box{item: Inner{value: 7}}   ->   Debug.inspect(b) == "Box{item: Inner{value: 7}}"
//
// No type arguments. So an instance's `nomi` is "Box" and NOT "Box<Inner>",
// which is the opposite of the choice for the std family — and the
// difference is not an inconsistency. A std generic struct never reaches the
// derived Debug rendering, because std writes its own `impl Debug`; its `nomi` is a
// collision-detection key and a refusal spelling, not a rendering. Here `nomi`
// IS the rendering.
//
// Two instances of one template therefore share a `nomi`, so
// `qualifiedNomiName` gives `Box<Int>` and `Box<Inner>` the same runtime type
// name. TestGenericInstanceNomiNameIsTheBareName pins the name, and
// TestGenericInstanceTypeIDIsModuleQualified pins the qualification, so a
// change to either fails rather than silently re-renders.

// genericTemplate is one user generic STRUCT or ENUM declaration, kept beside
// the refused declaration def rather than instead of it.
//
// The def in `g.types` keeps declModifiers' `generic type` refusal, so a BARE
// `Box` mention (a generic name used without arguments, which is not a type)
// reports it. Only an INSTANTIATION is routed here.
//
// # ONE `decl` FIELD FOR TWO DECLARATION KINDS, NOT TWO FIELDS
//
// Every reader either SWITCHES on the shape — the field/variant resolver, the
// type-argument recovery — or asks a question BOTH shapes answer, which is the
// position and the identity the instance def carries. Two fields would admit a
// reader that consults the one that is nil and silently treats an enum
// template as having no fields. `structDecl` and `enumDecl` answer nil for the
// other kind, so a reader that needs one has to say which.
type genericTemplate struct {
	// nomi is the declaration's name, which is also every instance's rendered
	// name. See the header.
	nomi string
	decl ast.Node
	// params are the type-parameter names in declaration order. Positional,
	// because a type ARGUMENT list is positional.
	params []string
	// why and whyDetail are the reason no instance of this template can be
	// built: a decorator, or an impl block naming it that cannot be
	// instantiated. Empty when instantiation is admitted.
	//
	// Carried on the TEMPLATE rather than recomputed per instantiation so
	// every use site reports one reason, and so the impl scan is paid once.
	why       string
	whyDetail string
	// impls are the impl blocks whose receiver names this template, each to be
	// registered ONCE PER INSTANCE under the substitution frame. See
	// genericimpl.go, which owns the mechanism and the reasoning.
	impls []*genericImplTemplate
	// scope is the block that declares a block-local template, nil for a
	// module-level one. Its instances are resolved, and their impls built,
	// with that block's declarations visible, as its fields may name them.
	scope *blockTypeDecls
	// siblingImpls are the impl blocks other files write over this template,
	// and registered is every instance whose impls are registered. See
	// siblingtemplateimpl.go.
	siblingImpls []siblingTemplateImpl
	registered   []*typeDef
}

// structDecl is the template's declaration when it is a struct, and nil when it
// is an enum. enumDecl is the mirror. See the type's header for why the two are
// derived from one field rather than stored in two.
func (tpl *genericTemplate) structDecl() *ast.StructDef {
	sd, _ := tpl.decl.(*ast.StructDef)
	return sd
}

func (tpl *genericTemplate) enumDecl() *ast.EnumDef {
	ed, _ := tpl.decl.(*ast.EnumDef)
	return ed
}

func (tpl *genericTemplate) distinctDecl() *ast.TypeDef {
	td, _ := tpl.decl.(*ast.TypeDef)
	return td
}

// collectGenericTemplates registers every user generic struct AND enum
// declaration.
//
// Called from buildTypes AFTER the module's own declarations are resolved,
// because templateWall reads `g.nodes` for impl blocks and an instantiation is
// only ever reached later, from a body or a signature.
//
// Enums are templates too. `Wrapper.Wrapped(42)`'s payload annotation IS `T`,
// so the payload-carrying construction recovers its arguments by the same
// unification a struct literal uses; the PAYLOAD-FREE `Wrapper.Empty` names
// nothing and is built at the instance the checker solved for its site (see
// genericVariantBare). See genericenum.go.
func (g *gen) collectGenericTemplates(nodes []ast.Node) {
	for _, n := range nodes {
		var name string
		var tps []ast.TypeParam
		switch decl := n.(type) {
		case *ast.StructDef:
			name, tps = decl.Name, decl.TypeParams
		case *ast.EnumDef:
			name, tps = decl.Name, decl.TypeParams
		case *ast.TypeDef:
			name, tps = decl.Name, decl.TypeParams
		default:
			continue
		}
		if len(tps) == 0 {
			continue
		}
		if _, dup := g.genericTemplates[name]; dup {
			// Two declarations of one name is a front-end error that buildTypes
			// already refuses on the def. Keeping the first here too, so the
			// two tables cannot disagree about which declaration `Box` is.
			continue
		}
		if g.genericTemplates == nil {
			g.genericTemplates = map[string]*genericTemplate{}
			g.templatesByDecl = map[ast.Node]*genericTemplate{}
		}
		tpl := g.newGenericTemplate(n, name, tps, nodes)
		g.genericTemplates[name] = tpl
		g.templatesByDecl[n] = tpl
	}
}

// newGenericTemplate is the template for the generic declaration n, with its
// wall and the impl blocks among nodes that name it.
func (g *gen) newGenericTemplate(n ast.Node, name string, tps []ast.TypeParam, nodes []ast.Node) *genericTemplate {
	tpl := &genericTemplate{nomi: name, decl: n}
	for _, tp := range tps {
		tpl.params = append(tpl.params, tp.Name)
	}
	tpl.why, tpl.whyDetail = g.templateWall(tpl)
	if tpl.why == "" {
		// The impl blocks naming this template, plus the reason -- if any --
		// that one of them cannot be instantiated at ANY argument tuple.
		// Split from templateWall because templateWall's clauses are
		// properties of the TYPE DECLARATION, while these are properties
		// of a separate declaration that happens to name it.
		tpl.why, tpl.whyDetail = g.collectGenericImpls(tpl, nodes)
	}
	return tpl
}

// templateWall is the reason no instance of this declaration may be built, or
// two empty strings.
//
// Impl blocks naming the template are handled by genericimpl.go, which
// registers them once per instance; the reasons an impl block can wall a
// template (an INHERENT block, a block-level `where` on a foreign subject) live
// there, in collectGenericImpls.
//
// The two clauses here mirror declModifiers exactly, minus `generic type`
// itself. They are re-stated rather than read off the def because the def's
// refusal LIST also contains `generic type`, and replaying that at an
// instantiation site would refuse what this file lowers. Keep the two in step:
// two statements of one rule that can be edited apart will disagree.
//
// It takes the TEMPLATE rather than a declaration node so the struct and the
// enum reach one body: both clauses read fields both declaration kinds have,
// and `keyword` is the only thing that differs.
func (g *gen) templateWall(tpl *genericTemplate) (string, string) {
	var decorators []ast.Decorator
	switch decl := tpl.decl.(type) {
	case *ast.StructDef:
		decorators = decl.Decorators
	case *ast.EnumDef:
		decorators = decl.Decorators
	case *ast.TypeDef:
		decorators = decl.Decorators
	}
	for _, dec := range decorators {
		if dec.Name == "derive" {
			// Metadata about an impl the front end synthesized, which
			// collectGenericImpls sees on its own terms. declModifiers' reason.
			continue
		}
		return "decorator", tpl.nomi + " @" + dec.Name
	}
	// An attached `//!` test does not wall a template, for declModifiers'
	// reason: a line in a doc comment cannot change what the type is, and the
	// prompt is a test case of its own (tests.go's attachedTestCases).
	return "", ""
}

// implReceiverBaseName is the type name an impl block's receiver names, at any
// generic spelling.
//
// Both spellings are read as one fact because the front end uses both and not
// interchangeably: a synthesized `derive Equatable for Box` carries an
// *ast.SimpleType receiver, while the universal `impl Debug for Box<T>` and
// every hand-written `impl<T> … for Box<T>` carry an *ast.GenericType. A scan
// with one arm would cover one provenance and miss the other.
func implReceiverBaseName(te ast.TypeExpr) string {
	switch t := te.(type) {
	case *ast.SimpleType:
		return t.Name
	case *ast.GenericType:
		return t.Name
	}
	return ""
}

// --- the per-gen intern table ------------------------------------------------

// typeInstCap and typeInstSizeCap bound the instances genericInstance
// builds. It refuses an instance built inside typeInstCap others, or one
// whose arguments are made of more than typeInstSizeCap types
// (kindsHoldAtMost), with the reason `generic instantiation depth` naming
// it. An instance is built inside another when the outer one's fields or
// impl signatures name it. A type whose parts hold itself at a strictly
// larger argument (`inner: Option<Nested<List<T>>>` in `struct Nested<T>`)
// would make that chain endless, and the checker rejects it (polymorphic
// recursion, internal/analysis's instantiation_cycle.go), so the bounds
// refuse only what the checker cannot see: an impl of a generic interface
// at a larger instance of its receiver (`impl Deeper<Box<Box<T>>> for
// Box<T>`), a cycle through a type whose declaration the checker's walk
// does not find, or a builder that resolved an impl's signature at the
// wrong arguments. A same-argument mention (`next: Option<Node<T>>`)
// re-finds the interned instance and costs nothing.
//
// Both are needed. A chain that grows by one type at each instance
// (`Box<Box<T>>`) reaches the depth first. One that doubles
// (`Box<(T, T)>`, `P<P<A, B>, P<A, B>>`) holds 2^d types at depth d, and
// every walk over a type (its spelling, its linkage key, the projection of
// the checker's type for it) costs that much, so it is stopped by size
// while the walks are still short. The values are fences, as monoInstCap's
// is: across `std/` and `tests/` no instance is built inside more than two
// others, and none has arguments made of more than 16 types.
const (
	typeInstCap     = 32
	typeInstSizeCap = 256
)

// kindsHoldAtMost reports whether the kinds ks are made of at most n types
// in all, each counting itself and the kinds it is made of: a composite's
// parts, a std instance's arguments (kindParts), a program instance's and an
// interface instance's arguments. It stops counting once the count passes
// n, so it takes at most n steps however large the kinds are: a kind shares
// its parts, and `(X, X)` nested d deep is d kinds and 2^d types.
func kindsHoldAtMost(ks []kind, n int) bool {
	var within func(ks []kind) bool
	within = func(ks []kind) bool {
		for _, k := range ks {
			if n--; n < 0 {
				return false
			}
			parts := kindParts(k)
			switch {
			case k.def != nil && len(k.def.genericArgs) > 0:
				parts = k.def.genericArgs
			case k.iface != nil:
				parts = k.iface.instArgs
			}
			if !within(parts) {
				return false
			}
		}
		return true
	}
	return within(ks)
}

// genericInstance is the def for `tpl<args>`, interned per gen.
//
// Inserted into the table BEFORE its fields are resolved, so a self-referential
// template (`struct Node<T> { next: Node<T> }`) finds the same def rather than
// recursing forever, and the boxing pass below then breaks the cycle exactly as
// buildTypes does for a module declaration.
func (g *gen) genericInstance(tpl *genericTemplate, args []kind, types []analysis.Type) (kind, bool) {
	defer changingTypes()()
	if g.genericInstCalls++; g.genericInstBudget > 0 && g.genericInstCalls > g.genericInstBudget {
		panic(fmt.Sprintf("irbuild: genericInstance was called more than %d times", g.genericInstBudget))
	}
	if tpl.why != "" {
		return kindInvalid, false
	}
	if len(args) != len(tpl.params) {
		// A front-end error; the analyzer solves arity. Declining rather than
		// building a def whose parameter list and argument list disagree.
		return kindInvalid, false
	}
	for _, a := range args {
		// kindInvalid: propagates — a refused type ARGUMENT was named at its own position.
		if a == kindInvalid {
			return kindInvalid, false
		}
		if a.tag == tagTypeParam {
			// Population C. `Box<T>` inside a generic FUNCTION has no concrete
			// argument, so there is nothing to monomorphize at. Declining
			// leaves recordedTypeRefusal's `generic type` to name it.
			return kindInvalid, false
		}
	}
	defer g.enterTypeScope(tpl.scope)()
	key := genericInstKey(tpl, args)
	for _, d := range g.genericInsts[key] {
		// Two bodies may each declare a block-local `Box`, so the name in
		// the key does not decide the template.
		if d.genericOf == tpl && samePartsSlice(d.genericArgs, args) {
			if d.genericTypes == nil {
				d.genericTypes = types
			}
			// A second mention gets the first one's answer: a refused
			// instance stays refused.
			if !d.lowerable {
				return kindInvalid, false
			}
			return named(d), true
		}
	}
	d := &typeDef{ // The bare declaration name, because this is what the derived Debug
		// rendering shows and what qualifiedNomiName module-qualifies. See the header.
		nomi: tpl.nomi, decl: tpl.decl, line: tpl.decl.LineNum(), isEnum: tpl.enumDecl() != nil, isDistinct: tpl.distinctDecl() != nil, genericOf: tpl, genericArgs: args, genericTypes: types, lowerable: true}
	if g.genericInsts == nil {
		g.genericInsts = map[string][]*typeDef{}
	}
	g.genericInsts[key] = append(g.genericInsts[key], d)
	g.genericInstOrder = append(g.genericInstOrder, d)
	if deep := g.genericInstNesting >= typeInstCap; deep || !kindsHoldAtMost(args, typeInstSizeCap) {
		// Interned refused, so a second mention gets the same answer.
		d.lowerable = false
		d.why = "generic instantiation depth"
		d.whyDetail = fmt.Sprintf("the arguments of %s are made of more than %d types; the chain does not terminate",
			tpl.nomi, typeInstSizeCap)
		if deep {
			d.whyDetail = fmt.Sprintf("%s<%s> is built inside %d other instances; the chain does not terminate",
				tpl.nomi, kindsNomi(args), g.genericInstNesting)
		}
		return kindInvalid, false
	}
	g.genericInstNesting++
	defer func() { g.genericInstNesting-- }()

	g.pushGenericSubst(substType, tpl, args, types)
	switch decl := tpl.decl.(type) {
	case *ast.StructDef:
		g.resolveStructFields(d, decl, nil)
	case *ast.EnumDef:
		g.resolveEnumVariants(d, decl, nil)
	case *ast.TypeDef:
		g.resolveDistinctInner(d, decl, nil)
	}
	g.popGenericSubst()

	if g.deferInstLayout(d) {
		return named(d), true
	}

	// Boxing, slot assignment and the lowerability relaxation, which buildTypes
	// does in three passes over the whole module and which are done here per
	// instance. Sound in this order because an instance is built BOTTOM-UP: every
	// argument kind and every nested instance was settled before this def's
	// fields were resolved, so the only unsettled edge is a self-reference, and
	// that is what the boxing pass is for.
	//
	// # The slot pass is the enum's boxing pass too
	//
	// An enum's payload boxing is decided INSIDE `assignSlots`, on
	// `slotDef.boxed`, through the same `appendInlineDefs` walk the loop below
	// applies to a struct FIELD. A payload has no `boxed` field of its own,
	// because storage is the slot's and a payload only points at one.
	//
	// So the loop below does nothing for an enum — `d.fields` is EMPTY for one,
	// exactly as it is in buildTypes, where the identical loop runs over every
	// def. Two passes over one list, each doing the half that is its own.
	d.boxFields()
	d.assignSlots()
	g.settleLowerable([]*typeDef{d})
	if !d.lowerable {
		return kindInvalid, false
	}
	// The impl blocks naming this template, registered against THIS def so
	// every consultation of `implsByIface` finds the user's own impl rather
	// than establishing that none exists. AFTER settleLowerable, because an
	// impl signature mentions `Box<T>` and resolving it has to find a def whose
	// fields and lowerability are already settled. See genericimpl.go.
	if why, detail := g.registerInstanceImpls(tpl, d, args); why != "" {
		// The instance is NOT admitted, and the def stays in the intern table
		// deliberately: a second mention of the same instantiation must get the
		// same answer, and re-running the registration would report the same
		// reason twice. genericArgRefusal names it at the use site.
		d.lowerable = false
		d.why, d.whyDetail = why, detail
		return kindInvalid, false
	}
	return named(d), true
}

// deferInstLayout queues an instance whose layout cannot be decided yet, and
// reports whether it did.
//
// An instance built while a type group resolves its fields may store a
// declaration whose fields are not resolved yet: in `struct Parent { t:
// Tree<Int> }` before `struct Child { n: Int }`, `Tree<Int>`'s `Node Child`
// payload is built while Child has no fields, so laying the instance out now
// would call Child zero-sized and give it no slot. The same holds for an
// instance built in a finished file over a mirror of such a declaration
// (`Pair<Int, Child>` with Pair in another file). A queued instance is boxed,
// slotted and settled with the group's own declarations (typeGroup.layOut)
// and its impls registered after that, so declaration order changes nothing.
//
// An instance already refused by its own fields, or by a component settled
// unlowerable, is not queued: the caller refuses it at once.
func (g *gen) deferInstLayout(d *typeDef) bool {
	defer changingTypes()()
	grp := g.instLayoutGroup(d)
	if grp == nil || len(d.refusals) > 0 {
		return false
	}
	for _, c := range d.mentions {
		if !c.lowerable {
			return false
		}
	}
	d.layoutGroup = grp
	grp.insts = append(grp.insts, deferredInst{g: g, d: d})
	return true
}

// instLayoutGroup is the group whose layout an instance must wait for: this
// file's own while it resolves its fields, or the group of a component that
// is not laid out yet. Nil when the instance can be laid out now.
func (g *gen) instLayoutGroup(d *typeDef) *typeGroup {
	if grp := g.building; grp != nil && grp.state == groupResolving {
		return grp
	}
	for _, c := range d.mentions {
		if grp := c.layoutGroup; grp != nil && grp.state == groupResolving {
			return grp
		}
	}
	return nil
}

// registerDeferredInstImpls is registerInstanceImpls for an instance
// deferInstLayout queued, inside its template's scope as genericInstance
// registers one built whole.
func (g *gen) registerDeferredInstImpls(d *typeDef) (string, string) {
	defer g.enterTypeScope(d.genericOf.scope)()
	return g.registerInstanceImpls(d.genericOf, d, d.genericArgs)
}

// genericInstKey is the intern key: the template pointer plus every argument's
// kind identity (kind.key).
//
// Keyed on the FULL instantiation and never on the template alone, for
// sharedGenStructDefs' reason — a base-name key is a silent overwrite that hands
// one instantiation another's field types. The template pointer is in the key so
// two same-named templates from two files (which foreign.go can put in one gen)
// cannot collide, and `samePartsSlice` at the call site is the exact check
// behind the rendered-name approximation.
func genericInstKey(tpl *genericTemplate, args []kind) string {
	var b strings.Builder
	b.WriteString(tpl.nomi)
	b.WriteByte('\x00')
	for _, a := range args {
		b.WriteString(a.key())
		b.WriteByte('\x00')
	}
	return b.String()
}

// --- the substitution frame --------------------------------------------------

// pushGenericSubst makes the template's parameters resolve to the instance's
// arguments for the length of one field walk. types is the instance's
// arguments as the checker's types, nil where the entry read none
// (substFrame).
//
// A STACK, because a nested instantiation resolves its own fields inside this
// one: `Crate<Inner>`'s field `box: Box<T>` pushes `Box`'s frame over `Crate`'s
// while `Box<Inner>` is built. Innermost-first, which is the shadowing rule
// typeOf's dictTypeParamKind arm already states for a function's parameters.
func (g *gen) pushGenericSubst(entry substEntry, tpl *genericTemplate, args []kind, types []analysis.Type) {
	g.pushSubst(entry, tpl, newSubstFrame(tpl.params, args, types))
}

func (g *gen) popGenericSubst() { g.popSubst() }

// substFrame is one frame of g.genericSubst: the type parameters of the
// instance being lowered, by name, each as the kind the builder lowers it
// at and, where the entry that made the instance read one, as the checker's
// type it stands for (genericSubstType). The checker's type comes from what
// the checker recorded where the instance was asked for: a call's type
// arguments (analysis.CallTypeArgs), with an enclosing instance's types put
// for its own type parameters (checkerTypeArg), or the type arguments of a
// type the checker solved (project). An entry that reads its arguments off
// annotations or operand kinds has none, and the builder never makes one
// up from a kind. A call's and an operator's record is read at these types
// (calleeAt, operatorUseAt); under test pushSubst holds each to its kind
// (verifySubst).
//
// opens marks the frame of a declaration the builder entered while it was
// lowering another one (substOpens): the frames below it are that other
// declaration's, which this one cannot name, so a lookup stops at it
// (visibleSubst).
type substFrame struct {
	kinds   map[string]kind
	checked map[string]analysis.Type
	opens   bool
}

// newSubstFrame is the frame binding params to args and, where types has
// one, to the checker's type. types is nil or as long as args.
func newSubstFrame(params []string, args []kind, types []analysis.Type) substFrame {
	f := substFrame{kinds: make(map[string]kind, len(params))}
	for i, p := range params {
		f.kinds[p] = args[i]
		if i < len(types) && types[i] != nil {
			if f.checked == nil {
				f.checked = make(map[string]analysis.Type, len(params))
			}
			f.checked[p] = types[i]
		}
	}
	return f
}

// substEntry is the way an instance's frame is entered: what kind of
// declaration is instantiated, which says where its checker types come
// from. The census (substCensus) counts by it.
type substEntry uint8

const (
	// substMono is a program generic fn's instance (monoInstanceAt,
	// siblingGenericInstance).
	substMono substEntry = iota
	// substNested is a generic nested fn's instance (genericNestedFunc).
	substNested
	// substType is a generic type's instance while its fields resolve
	// (genericInstance).
	substType
	// substImpl is an impl block of a generic type's instance, its
	// functions and accessors (registerInstanceImpls, flushGenericImpls).
	substImpl
	// substMethod is an impl function with type parameters of its own
	// (methodInstance), under its block's frame.
	substMethod
	// substIface is a generic interface's instance while its declaration
	// resolves (ifaceInstAtTyped).
	substIface
	// substIfaceImpl is the generic interface an impl block implements
	// (`impl Holds<Int> for Box`), at the block's arguments, around the code
	// the block takes from the interface's declaration: the defaults it
	// inherits (enterIfaceCode). The block's own functions cannot name the
	// interface's parameters and are never under it.
	substIfaceImpl
	// substIfaceDefault is an interface default instantiated per receiver.
	substIfaceDefault
	// substParamDefault is a generic callee's parameter default lowered at
	// a call, at the call's type arguments (defaultInFrame).
	substParamDefault
	// substStd is a std generic function's instance (stdInstances.queue).
	substStd
	// substAlias is a generic typealias's target at a use's arguments.
	substAlias
	// substRefinement is a generic refinement's base at a use's arguments.
	substRefinement
	// substProbe is a frame that binds a std impl block's parameters to
	// trial kinds to read its receiver's shape: no instance, never counted.
	substProbe
	substEntries
)

// substScope is what an entry's frame says about the frames below it.
//
// A type parameter is its declaration, not its spelling. The checker
// resolves a written type name in a lexical scope, innermost declaration
// first, and records the parameter it found by its spelling
// (analysis.TypeParam_), which names one declaration among those in scope
// there. The frame stack is the builder's copy of that scope: it holds the
// frames of the declaration being built and of the declarations it is
// written inside. The builder also enters a declaration while it lowers
// another one: `Box<Option<Int>>`'s impl functions are registered while
// the call in `fn mk<S>` that first names the instance is lowered, so
// mk's frame is below the block's. Box's block declares `fn make<S>`,
// whose `S` no frame of the block binds, and mk's `S` must not answer for
// it. So an entry that opens a declaration hides the frames below it.
// The same holds within one impl block: `impl Holds<List<T>> for Box<T>`
// binds the block's `T` to Int and the interface's `T` to List<Int>, and
// only the interface's defaults are written where the interface's `T` is
// in scope, so its frame opens too.
type substScope uint8

const (
	substScopeUnset substScope = iota
	// substOpens: the frame binds every type parameter its declaration can
	// name, and the frames below it are another declaration's, which a
	// lookup does not reach.
	substOpens
	// substNests: the frame binds the parameters of a declaration written
	// inside the one whose frames are below it, which it can name: a
	// nested fn inside its enclosing fn.
	substNests
)

// substEntryInfo is each entry's census name and scope. Every entry has
// both (TestEverySubstEntryHasANameAndAScope).
var substEntryInfo = [substEntries]struct {
	name  string
	scope substScope
}{
	substMono:         {"program generic fn", substOpens},
	substNested:       {"generic nested fn", substNests},
	substType:         {"generic type fields", substOpens},
	substImpl:         {"impl of a generic type", substOpens},
	substMethod:       {"generic impl fn", substOpens},
	substIface:        {"generic interface", substOpens},
	substIfaceImpl:    {"generic interface of an impl", substOpens},
	substIfaceDefault: {"interface default", substOpens},
	substParamDefault: {"parameter default at a call", substOpens},
	substStd:          {"std generic fn", substOpens},
	substAlias:        {"generic typealias", substOpens},
	substRefinement:   {"generic refinement", substOpens},
	substProbe:        {"probe", substOpens},
}

func (e substEntry) String() string { return substEntryInfo[e].name }

// pushSubst enters f, the frame of owner's instance (a template, an
// instance or a declaration: what tells one instance from another in the
// census).
func (g *gen) pushSubst(entry substEntry, owner any, f substFrame) {
	if substVerify {
		g.verifySubst(entry, owner, f)
	}
	f.opens = substEntryInfo[entry].scope == substOpens
	g.genericSubst = append(g.genericSubst, f)
}

func (g *gen) popSubst() {
	g.genericSubst = g.genericSubst[:len(g.genericSubst)-1]
}

// visibleSubst is the frames the declaration being built can name: the
// innermost frame that opens a declaration (substOpens) and the frames
// above it, innermost last. Every read of the stack goes through it.
func (g *gen) visibleSubst() []substFrame {
	for i := len(g.genericSubst) - 1; i >= 0; i-- {
		if g.genericSubst[i].opens {
			return g.genericSubst[i:]
		}
	}
	return g.genericSubst
}

// genericSubstKind is the argument a type parameter stands for inside the
// instance being built, and whether the name is one.
//
// Consulted from typeOf's *ast.SimpleType arm and therefore reached at EVERY
// depth for free: `items: List<T>` and `f: (T) -> Bool` recurse through typeOf,
// so one insertion point covers the container and function spellings without an
// arm of their own. That is the same property dict.go claims for its own arm.
func (g *gen) genericSubstKind(name string) (kind, bool) {
	if g == nil {
		// No gen (projectShared), so no instance's frame.
		return kindInvalid, false
	}
	frames := g.visibleSubst()
	for i := len(frames) - 1; i >= 0; i-- {
		if k, found := frames[i].kinds[name]; found {
			return k, true
		}
	}
	return kindInvalid, false
}

// genericSubstType is the checker's type a type parameter stands for inside
// the instance being built, from the frame genericSubstKind reads the
// parameter's kind from, and whether that frame's entry had one.
func (g *gen) genericSubstType(name string) (analysis.Type, bool) {
	frames := g.visibleSubst()
	for i := len(frames) - 1; i >= 0; i-- {
		f := frames[i]
		if _, found := f.kinds[name]; found {
			t, ok := f.checked[name]
			return t, ok
		}
	}
	if ds := g.defaultSelf; name == stdIfaceDefaultSelfParam && ds != nil && ds.typ != nil {
		// A program interface's default lowered for one implementor binds
		// its `self` (the interface's SelfParam) through defaultSelf rather
		// than a frame.
		return ds.typ, true
	}
	return nil, false
}

// checkerTypeArg is t, a type argument the checker recorded inside the body
// being lowered, with each of the enclosing instances' type parameters it
// names replaced by the checker's type the parameter stands for
// (genericSubstType), or nil when one has none.
func (g *gen) checkerTypeArg(t analysis.Type) analysis.Type {
	if t == nil {
		return nil
	}
	out, all := analysis.ReplaceTypeParams(t, func(p *analysis.TypeParam_) (analysis.Type, bool) {
		return g.genericSubstType(p.Name_)
	})
	if !all {
		return nil
	}
	return out
}

// operandCheckerType is the checker's type of n, an operand of the body
// being lowered (ExprTypes), at this instance (checkerTypeArg), or nil
// when the checker recorded none or it names a parameter with no checker
// type here. An entry that reads an instance's arguments off an operand's
// kind reads their checker types off this.
func (g *gen) operandCheckerType(n ast.Node) analysis.Type {
	if n == nil {
		return nil
	}
	return g.checkerTypeArg(g.checkedExprType(n))
}

// valueCheckerType is operandCheckerType of n where it is the type of a
// value of kind k, the value n lowered to: nil where the checker recorded
// for n a type of another kind (the type it converts to), or none.
func (g *gen) valueCheckerType(n ast.Node, k kind) analysis.Type {
	if t := g.operandCheckerType(n); t != nil && g.project(t) == k {
		return t
	}
	return nil
}

// firstOperandType is operandCheckerType of t's first operand, the
// receiver of each call that asks, or for an operand the builder wrote,
// which has no type of its own, the type the call's record is for
// (Callee.For). Nil when it has neither.
func (g *gen) firstOperandType(t *ast.Call) analysis.Type {
	if len(t.Args) == 0 {
		return nil
	}
	if ot := g.operandCheckerType(t.Args[0]); ot != nil {
		return ot
	}
	return g.checkerTypeArg(g.calleeOf(t).For)
}

// checkerTypeArgs is checkerTypeArg over the type arguments of a type the
// checker solved, nil when none has a checker type.
func (g *gen) checkerTypeArgs(typeArgs []analysis.Type) []analysis.Type {
	var out []analysis.Type
	for i, t := range typeArgs {
		if c := g.checkerTypeArg(t); c != nil {
			if out == nil {
				out = make([]analysis.Type, len(typeArgs))
			}
			out[i] = c
		}
	}
	return out
}

// typeInstantiator builds the instance of a generic type template at args,
// with types the checker's types for them or nil (genericTemplateOf).
type typeInstantiator func(args []kind, types []analysis.Type) (kind, bool)

// --- reading an annotation, an inferred type and a literal -------------------

// kindParts is a composite kind's component kinds, positionally, or nil.
//
// One reader of three storage shapes rather than three call-site switches: a
// STRUCTURAL kind keeps its components on the interned *compKind, a PRELUDE
// instance on preludeArgs, a GENERIC STD STRUCT instance on genStructArgs. The
// unifier asks one question — "what is this container's i-th component" — and a
// switch that had learned only two of the three shapes would silently fail to
// bind a parameter under the third.
//
// A USER generic instance is deliberately absent: unifyTypeParams matches one
// against the DECLARED name first, because `box: Box<T>` has to check that the
// value really is a `Box` and not some other one-argument container, and reads
// genericArgs there.
func kindParts(k kind) []kind {
	if k.comp != nil {
		return k.comp.parts
	}
	if k.def == nil {
		return nil
	}
	if len(k.def.preludeArgs) > 0 {
		return k.def.preludeArgs
	}
	if len(k.def.genHostArgs) > 0 {
		return k.def.genHostArgs
	}
	return k.def.genStructArgs
}

// projectGenericInstance is the instance of a generic type the checker
// solved, written (`b: Box<Inner>`, whose type expression's record is
// projected) or not (an unannotated lambda parameter, a binding), so an
// annotation and an inference over one value are one answer.
//
// The template is the one the checker's type names (templateAt). args is
// typeArgs projected, where the caller has projected them already, and nil
// otherwise. Each argument is projected once: an instance nested d deep
// inside instances of itself (`Box<Box<Box<Int>>>`) would cost 2^d
// projections if each level projected its arguments twice.
func (g *gen) projectGenericInstance(params []*analysis.TypeParam_, origin, name string, typeArgs []analysis.Type, args []kind) (kind, bool) {
	// With no gen (projectShared) there are no templates.
	if g == nil || g.fa == nil {
		return kindInvalid, false
	}
	_, instantiate, ok := g.templateAt(params, origin, name)
	if !ok {
		return kindInvalid, false
	}
	if args == nil {
		args = make([]kind, len(typeArgs))
		for i, a := range typeArgs {
			args[i] = g.project(a)
		}
	}
	return instantiate(args, g.checkerTypeArgs(typeArgs))
}

// genericTemplateOf is the template of the generic declaration the
// checker's nominal type t names, an instance's or the declaration's own
// type, with the function that instantiates it (templateAt). ok is false
// for a type of no program file's generic declaration. Every caller lowers
// a checked body, so the gen has its file's analysis.
func (g *gen) genericTemplateOf(t analysis.Type) (*genericTemplate, typeInstantiator, bool) {
	switch ty := analysis.ResolveClosed(t).(type) {
	case *analysis.StructType:
		return g.templateAt(ty.TypeParamDefs, ty.Origin, ty.Name)
	case *analysis.EnumType:
		return g.templateAt(ty.TypeParamDefs, ty.Origin, ty.Name)
	case *analysis.DistinctType:
		return g.templateAt(ty.TypeParamDefs, ty.Origin, ty.Name)
	}
	return nil, nil, false
}

// templateAt is the template of the generic declaration the checker names
// (origin, name), whose type parameters are params, with the function that
// instantiates it. Identity is the analyzer's own `(Origin, Name)` rule and
// not a name match, so a sibling module's same-named `Box` answers only for
// its own origin. A block-local template is found by params, which every
// instance of it shares, so an instance that escapes the block declaring it
// is found where its name does not resolve (blockLocalTemplateOf). Then
// this module's (ownTemplate), whose instance is built in this gen; an
// empty origin is this module's, the checker's on a type argument inside a
// variant constructor's instantiated result (`Wrapper<Wrapper<Int>>`).
// Another program file's is instantiated in the declaring gen and mirrored
// here (foreignTemplateAt), as a spelled `span.Span<Int>` is.
func (g *gen) templateAt(params []*analysis.TypeParam_, origin, name string) (*genericTemplate, typeInstantiator, bool) {
	if len(params) == 0 {
		return nil, nil, false
	}
	if g.otherFileOrigin(origin) {
		ref, found := g.foreignTemplateAt(origin, name)
		if !found {
			return nil, nil, false
		}
		return ref.tpl, func(args []kind, types []analysis.Type) (kind, bool) { return g.instantiateForeignAt(ref, args, types) }, true
	}
	tpl := g.blockLocalTemplateOf(params)
	if tpl == nil {
		tpl = g.ownTemplate(origin, name)
	}
	if tpl == nil {
		return nil, nil, false
	}
	return tpl, func(args []kind, types []analysis.Type) (kind, bool) { return g.genericInstance(tpl, args, types) }, true
}

// otherFileOrigin reports whether origin, a checker type's declaring
// origin, is another program file's than this gen's: not empty (this
// module's), not a body's, and not this file's own.
func (g *gen) otherFileOrigin(origin string) bool {
	return origin != "" && !analysis.IsBodyOrigin(origin) && origin != g.fa.Origin
}

// receiverTemplate is the template of the generic declaration impl block
// ib's receiver names, by the type the checker recorded for the receiver
// (genericTemplateOf), and whether another program file declares it. ok is
// false for a receiver of no program file's generic declaration.
func (g *gen) receiverTemplate(ib *ast.ImplBlock) (tpl *genericTemplate, foreign, ok bool) {
	rec, _ := g.recordedTypeExpr(ib.Receiver)
	if tpl, _, ok = g.genericTemplateOf(rec); !ok {
		return nil, false, false
	}
	return tpl, g.otherFileOrigin(originOfType(analysis.ResolveClosed(rec))), true
}

// receiverTypeParamNames is the type parameter each of impl block ib's
// receiver's type arguments is, by argument position: the name where the
// checker recorded the argument as a type parameter of the block (`T` of
// `impl Box<T>`), "" where it is a type (`Int` of `impl Box<Int>`). nil
// when the receiver has no type arguments.
func (g *gen) receiverTypeParamNames(ib *ast.ImplBlock) []string {
	recv := ib.Receiver
	if qt, qualified := recv.(*ast.QualifiedType); qualified {
		recv = qt.Member
	}
	gt, generic := recv.(*ast.GenericType)
	if !generic || len(gt.Params) == 0 {
		return nil
	}
	out := make([]string, len(gt.Params))
	for i, arg := range gt.Params {
		rec, _ := g.recordedTypeExpr(arg)
		if tp, isParam := rec.(*analysis.TypeParam_); isParam {
			out[i] = tp.Name_
		}
	}
	return out
}

// ownTemplate is the template of the declaration (origin, name) names in
// this module, or nil. An empty origin is this module's (the checker's on a
// variant constructor's instantiated result), and a body's origin names no
// module-level declaration.
func (g *gen) ownTemplate(origin, name string) *genericTemplate {
	if origin == "" {
		origin = g.fa.Origin
	}
	if analysis.IsBodyOrigin(origin) {
		return nil
	}
	n := g.declNode(origin, name)
	if isNilNode(n) {
		return nil
	}
	return g.templatesByDecl[n]
}

// templateInstanceOf is the instance of a template the caller already has
// (instantiate, from genericTemplateOf) at the type arguments of t, a type
// the checker solved for a construction of it. The template is the caller's,
// so t's Origin is not consulted: the checker leaves it unset on a variant
// constructor's instantiated result.
func (g *gen) templateInstanceOf(instantiate typeInstantiator, t analysis.Type) kind {
	if tv, isVar := t.(*analysis.TypeVar); isVar {
		if tv.Resolved == nil {
			return kindInvalid
		}
		return g.templateInstanceOf(instantiate, tv.Resolved)
	}
	var typeArgs []analysis.Type
	switch ty := t.(type) {
	case *analysis.EnumType:
		typeArgs = ty.TypeArgs
	case *analysis.StructType:
		typeArgs = ty.TypeArgs
	default:
		return kindInvalid
	}
	if len(typeArgs) == 0 {
		return kindInvalid
	}
	args := make([]kind, len(typeArgs))
	for i, a := range typeArgs {
		// An argument nothing determines (`E.X(1)`'s other parameter) is
		// the type the checker closed it as.
		args[i] = g.project(a)
	}
	k, ok := instantiate(args, g.checkerTypeArgs(typeArgs))
	if !ok {
		return kindInvalid
	}
	return k
}

// literalTypeArgs is the type arguments of the instance the checker typed
// t, a construction, as (`Box{v: 1}` a Box<Int>, `Holder.Of{value: 5}` a
// Holder<Int>, `Config(defaults())` a Config<Int>), n of them, in this
// gen's kinds. A parameter nothing in the
// literal determines (`Either.L{a: 1}`'s B: spec, "Determined type
// arguments") is the type the checker closed it as. Nothing is solved from
// the field values.
func (g *gen) literalTypeArgs(t ast.Node, n int) ([]kind, []analysis.Type, bool) {
	var typeArgs []analysis.Type
	switch ty := analysis.ResolveClosed(g.checkedExprType(t)).(type) {
	case *analysis.EnumType:
		typeArgs = ty.TypeArgs
	case *analysis.StructType:
		typeArgs = ty.TypeArgs
	}
	if n == 0 || len(typeArgs) != n {
		return nil, nil, false
	}
	args := make([]kind, n)
	for i, a := range typeArgs {
		k := g.project(a)
		// A leaf, or any value the IR carries: the instance's own layout is
		// checked where the literal is built (irRetainedStructKind). Unit is
		// a field like any other (`Captured<Unit>`'s value).
		if k != kindUnit && !irRetainedLeafKind(k) && !irCallableValueKind(k) {
			return nil, nil, false
		}
		args[i] = k
	}
	return args, g.checkerTypeArgs(typeArgs), true
}

// instanceArgs is generic instance d's type arguments in this gen's kinds.
// A mirror of another file's instance carries none of its own (mirrorShell):
// they are read off the owner's def and imported.
func (g *gen) instanceArgs(d *typeDef) ([]kind, bool) {
	if d.instOrigin == nil {
		return d.genericArgs, true
	}
	args := make([]kind, len(d.instOrigin.genericArgs))
	for i, a := range d.instOrigin.genericArgs {
		k, ok := g.importKind(a)
		if !ok {
			return nil, false
		}
		args[i] = k
	}
	return args, true
}

// unifyTypeParams matches a declared annotation against an actual kind,
// binding every type parameter in params the annotation names. A parameter
// already bound keeps its first binding. It reads which instance of an
// impl block a receiver kind is (stdRecvTypeArgs: `List<T>` against
// `List<Int>`), which is dispatch at an instance's kinds; a call's type
// arguments are the checker's (calleeTypeArgs), never solved here.
func (g *gen) unifyTypeParams(ann ast.TypeExpr, actual kind, params map[string]bool, solved map[string]kind) {
	switch a := ann.(type) {
	case *ast.SimpleType:
		if !params[a.Name] {
			return
		}
		if _, dup := solved[a.Name]; dup {
			return
		}
		solved[a.Name] = actual
	case *ast.GenericType:
		// `box: Box<T>` against the kind of a nested `Box{…}` literal. The
		// instance carries its own arguments, so the match reads them back
		// rather than re-deriving them — which is why genericArgs is on the
		// def at all.
		if actual.def != nil && actual.def.genericOf != nil && actual.def.genericOf.nomi == a.Name {
			if args, ok := g.instanceArgs(actual.def); ok && len(args) == len(a.Params) {
				for i, p := range a.Params {
					g.unifyTypeParams(p, args[i], params, solved)
				}
				return
			}
		}
		// `items: List<T>`, `m: Option<T>` — a structural or prelude container
		// whose component kinds are what the parameter is matched against.
		parts := kindParts(actual)
		if len(parts) != len(a.Params) {
			return
		}
		for i, p := range a.Params {
			g.unifyTypeParams(p, parts[i], params, solved)
		}
	case *ast.FuncType:
		parts := kindParts(actual)
		want := len(a.Params)
		if a.Return != nil {
			want++
		}
		if len(parts) != want {
			return
		}
		for i, p := range a.Params {
			g.unifyTypeParams(p, parts[i], params, solved)
		}
		if a.Return != nil {
			g.unifyTypeParams(a.Return, parts[len(parts)-1], params, solved)
		}
	}
}

// kindsNomi is an argument list's Nomi spelling, for a refusal detail only.
func kindsNomi(args []kind) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = a.nomi()
	}
	return strings.Join(parts, ", ")
}
