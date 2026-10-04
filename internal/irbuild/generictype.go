package irbuild

import (
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// A USER-DECLARED generic struct, MONOMORPHIZED per instantiation.
//
// # Five populations under one key
//
// `generic type` is emitted from several sites and covers five populations
// that need five different things:
//
//	A  a generic DECLARATION             types.go declModifiers
//	B  an instantiation at CONCRETE args  sigreason.go genericRefusal
//	C  an instantiation at a TYPE PARAM   sigreason.go genericRefusal
//	D  a BARE type parameter              sigreason.go
//	E  a STDLIB generic named type        sigreason.go genericRefusal
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
// # Why monomorphization and not a Go generic type
//
// Not a preference. `kind` is `{tag, *typeDef}` and a named type's identity IS
// its `*typeDef` pointer (types.go's header), so every downstream mechanism —
// field access, boxing, slot layout, the impl tables keyed on `kind`, the
// inspector cache — reads a def with CONCRETE field kinds. A single
// `type NomiT_Box[T any] struct{ F_item T }` would need each of those to carry
// a substitution alongside the pointer, which is a second identity model beside
// the one the package is built on. One def per instantiation reuses all of it:
// `Box<Inner>` is an ordinary struct def whose field kind happens to have been
// computed under a substitution, and nothing downstream can tell.
//
// That is also what stdgenstruct.go and prelude.go do for the std and prelude
// families — `buildGenStructDef` and `buildPreludeDef` each construct one
// `*typeDef` per argument tuple. This is the same mechanism over the
// declaration kind neither of those can read: a USER declaration, whose Go
// type this package must EMIT rather than find in rt.
//
// # PER-GEN, not process-wide
//
// stdgenstruct.go interns process-wide because every admitted argument is
// `packageNeutral` — `rt.Set[int64]` renders identically in every generated
// package. A user instance cannot be: `Box<Inner>`'s field type is
// `NomiT_Inner`, a type ONE generated package declares. So the intern table is
// `g.genericInsts`, per gen, and the Go type is emitted into the package that
// created it. A cross-package instance is named through foreign.go's mirror
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
// `nomi` is what the derived inspector emits (`rt.InspectStruct(def.nomi, …)`)
// and what `qualifiedNomiName` module-qualifies for `rt.TypeID`:
//
//	b = Box{item: Inner{value: 7}}   ->   Debug.inspect(b) == "Box{item: Inner{value: 7}}"
//
// No type arguments. So an instance's `nomi` is "Box" and NOT "Box<Inner>",
// which is the opposite of genStructNames' choice for the std family — and the
// difference is not an inconsistency. A std generic struct never reaches the
// derived inspector, because std writes its own `impl Debug`; its `nomi` is a
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
// reports it. Only an INSTANTIATION is routed here, so `namedType` has no arm
// for a template.
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
	// built — a decorator, a body item, or an impl block naming it that cannot
	// be instantiated. Empty when instantiation is admitted.
	//
	// Carried on the TEMPLATE rather than recomputed per instantiation so
	// every use site reports one reason, and so the impl scan is paid once.
	why       string
	whyDetail string
	// impls are the impl blocks whose receiver names this template, each to be
	// registered ONCE PER INSTANCE under the substitution frame. See
	// genericimpl.go, which owns the mechanism and the reasoning.
	impls []*genericImplTemplate
	// hostImpls are the inherent blocks on this template whose every item is
	// a Go-bound function. They register nowhere: a call to one of them
	// crosses to the one Go function whatever T is (hostTemplateImplPlan).
	hostImpls []*ast.ImplBlock
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
// nothing and is refused BY NAME at the construction (see genericBareVariant)
// rather than by walling the declaration every other construction of it needs.
// See genericenum.go.
func (g *gen) collectGenericTemplates(nodes []ast.Node) {
	for _, n := range nodes {
		var name string
		var tps []ast.TypeParam
		switch decl := n.(type) {
		case *ast.StructDef:
			name, tps = decl.Name, decl.TypeParams
		case *ast.EnumDef:
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
		if g.genericTemplates == nil {
			g.genericTemplates = map[string]*genericTemplate{}
		}
		g.genericTemplates[name] = tpl
	}
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
	var items []ast.Node
	keyword := "struct"
	switch decl := tpl.decl.(type) {
	case *ast.StructDef:
		decorators, items = decl.Decorators, decl.Items
	case *ast.EnumDef:
		decorators, items = decl.Decorators, decl.Items
		keyword = "enum"
	}
	for _, dec := range decorators {
		if dec.Name == "derive" {
			// Metadata about an impl the front end synthesized, which
			// collectGenericImpls sees on its own terms. declModifiers' reason.
			continue
		}
		return "decorator", tpl.nomi + " @" + dec.Name
	}
	if len(items) > 0 {
		return "type body item", keyword + " " + tpl.nomi
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

// genericInstance is the def for `tpl<args>`, interned per gen.
//
// Inserted into the table BEFORE its fields are resolved, so a self-referential
// template (`struct Node<T> { next: Node<T> }`) finds the same def rather than
// recursing forever, and the boxing pass below then breaks the cycle exactly as
// buildTypes does for a module declaration.
func (g *gen) genericInstance(tpl *genericTemplate, args []kind) (kind, bool) {
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
			// leaves genericRefusal's `generic type` to name it.
			return kindInvalid, false
		}
	}
	key := genericInstKey(tpl, args)
	for _, d := range g.genericInsts[key] {
		if samePartsSlice(d.genericArgs, args) {
			return named(d), true
		}
	}
	d := &typeDef{ // The BARE declaration name, because this is what the derived inspector
		// renders and what qualifiedNomiName module-qualifies. See the header.
		nomi: tpl.nomi, decl: tpl.decl, line: tpl.decl.LineNum(), isEnum: tpl.enumDecl() != nil, genericOf: tpl, genericArgs: args, lowerable: true}
	if g.genericInsts == nil {
		g.genericInsts = map[string][]*typeDef{}
	}
	g.genericInsts[key] = append(g.genericInsts[key], d)
	g.genericInstOrder = append(g.genericInstOrder, d)

	g.pushGenericSubst(tpl, args)
	switch decl := tpl.decl.(type) {
	case *ast.StructDef:
		g.resolveStructFields(d, decl, nil)
	case *ast.EnumDef:
		g.resolveEnumVariants(d, decl, nil)
	}
	g.popGenericSubst()

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
	for i := range d.fields {
		for _, c := range appendInlineDefs(nil, d.fields[i].k) {
			if c == d || c.reaches(d) {
				d.fields[i].boxed = true
				break
			}
		}
	}
	// AFTER the field boxing, which is buildTypes' order and is load-bearing
	// there for a reason that applies here unchanged: a boxed field is a
	// pointer, a pointer is never zero-sized, and zero-sizedness decides whether
	// a payload gets a slot at all.
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
// arguments for the length of one field walk.
//
// A STACK, because a nested instantiation resolves its own fields inside this
// one: `Crate<Inner>`'s field `box: Box<T>` pushes `Box`'s frame over `Crate`'s
// while `Box<Inner>` is built. Innermost-first, which is the shadowing rule
// typeOf's dictTypeParamKind arm already states for a function's parameters.
func (g *gen) pushGenericSubst(tpl *genericTemplate, args []kind) {
	frame := make(map[string]kind, len(tpl.params))
	for i, p := range tpl.params {
		frame[p] = args[i]
	}
	g.genericSubst = append(g.genericSubst, frame)
}

func (g *gen) popGenericSubst() {
	g.genericSubst = g.genericSubst[:len(g.genericSubst)-1]
}

// genericSubstKind is the argument a type parameter stands for inside the
// instance being built, and whether the name is one.
//
// Consulted from typeOf's *ast.SimpleType arm and therefore reached at EVERY
// depth for free: `items: List<T>` and `f: (T) -> Bool` recurse through typeOf,
// so one insertion point covers the container and function spellings without an
// arm of their own. That is the same property dict.go claims for its own arm.
func (g *gen) genericSubstKind(name string) (kind, bool) {
	for i := len(g.genericSubst) - 1; i >= 0; i-- {
		if k, found := g.genericSubst[i][name]; found {
			return k, true
		}
	}
	return kindInvalid, false
}

// --- reading an annotation, an inferred type and a literal -------------------

// userGenericTypeOf reads the annotation `Box<Inner>`, declining when the name
// is not a user generic declaration so `Foo<Bar>` keeps falling through to its
// own refusal.
//
// A type ARGUMENT is resolved by the ordinary g.typeOf, so `Box<List<Int>>`
// works and `Box<SomeUnrepresentableType>` declines on the kindInvalid gate in
// genericInstance rather than admitting a def with a field this package cannot
// emit.
//
// It takes the NAME and the arguments rather than the node, because the two
// spellings put the qualifier in different places: `Box<Inner>` is one
// *ast.GenericType, and `shapes.Holder<Int>` is an *ast.QualifiedType whose
// MEMBER is one. Passing the joined name is what lets both reach
// genericTemplateNamed, which is the single resolver for a template named
// locally or through a module.
func (g *gen) userGenericTypeOf(name string, params []ast.TypeExpr) (kind, bool) {
	_, instantiate, isTemplate := g.genericTemplateNamed(name)
	if !isTemplate {
		return kindInvalid, false
	}
	args := make([]kind, len(params))
	for i, p := range params {
		args[i] = g.typeOf(p)
	}
	return instantiate(args)
}

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

// projectGenericInstance is userGenericTypeOf for a type the CHECKER solved
// rather than the program wrote — an unannotated lambda parameter, a binding.
//
// An annotated `b: Box<Inner>` lowering while an inferred `|b|` over the same
// value refuses would not be a smaller subset, it would be two answers to one
// question. inferred.go's Map, Distinct and AnonStruct arms exist for the same
// reason.
//
// Identity is the analyzer's own `(Origin, Name)` rule and not a name match, so
// a sibling module's same-named `Box` answers only for its own origin: this
// file's templates for this file's origin, and the declaring file's for
// another's (foreignTemplateAt).
func (g *gen) projectGenericInstance(origin, name string, typeArgs []analysis.Type) (kind, bool) {
	// An empty Origin is the checker's on a type argument inside a variant
	// constructor's instantiated result (`Wrapper<Wrapper<Int>>`); the name
	// resolves among this module's own templates.
	if g.fa == nil {
		return kindInvalid, false
	}
	var instantiate func([]kind) (kind, bool)
	if origin != "" && origin != g.fa.Origin {
		// Another file's template: the instance is built there and
		// mirrored here, as a spelled `span.Span<Int>` is.
		ref, found := g.foreignTemplateAt(origin, name)
		if !found {
			return kindInvalid, false
		}
		instantiate = func(args []kind) (kind, bool) { return g.instantiateForeign(ref, args) }
	} else {
		tpl := g.genericTemplates[name]
		if tpl == nil {
			return kindInvalid, false
		}
		instantiate = func(args []kind) (kind, bool) { return g.genericInstance(tpl, args) }
	}
	args := make([]kind, len(typeArgs))
	for i, a := range typeArgs {
		args[i] = g.project(a)
	}
	return instantiate(args)
}

// templateInstanceOf is the instance of a template already resolved by name
// (instantiate, from genericTemplateNamed) at the type arguments of t, a type
// the checker solved for a construction of it. The template is the caller's
// resolution, so t's Origin is not consulted: the checker leaves it unset on
// a variant constructor's instantiated result.
func (g *gen) templateInstanceOf(instantiate func([]kind) (kind, bool), t analysis.Type) kind {
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
		args[i] = g.project(a)
	}
	k, ok := instantiate(args)
	if !ok {
		return kindInvalid
	}
	return k
}

// checkedStructTypeArgs projects the instantiated field-label types recorded
// by the checker. It never evaluates field expressions or guesses an omitted
// parameter. Other representations decline.
func (g *gen) checkedStructTypeArgs(t *ast.StructLit, tpl *genericTemplate) ([]kind, bool) {
	sd := tpl.structDecl()
	if g.fa == nil || sd == nil {
		return nil, false
	}
	return g.checkedFieldTypeArgs(t, tpl, sd.Fields)
}

// checkedVariantTypeArgs is checkedStructTypeArgs for a struct-shaped
// variant of a generic enum template (`shapes.Holder.Of{value: 5}`).
func (g *gen) checkedVariantTypeArgs(t *ast.StructLit, tpl *genericTemplate, variant string) ([]kind, bool) {
	ed := tpl.enumDecl()
	if g.fa == nil || ed == nil {
		return nil, false
	}
	for _, v := range ed.Variants {
		if v.Name == variant && v.Kind == "struct" {
			return g.checkedFieldTypeArgs(t, tpl, v.Fields)
		}
	}
	return nil, false
}

// checkedFieldTypeArgs solves tpl's arguments from the checker's
// instantiated field-label types of t against the declared fields.
func (g *gen) checkedFieldTypeArgs(t *ast.StructLit, tpl *genericTemplate, declared []ast.StructField) ([]kind, bool) {
	byName := map[string]ast.TypeExpr{}
	for _, f := range declared {
		byName[f.Name] = f.TypeAnnotation
	}
	params := templateParamSet(tpl)
	solved := map[string]kind{}
	for _, f := range t.Fields {
		ann := byName[f.Name]
		if ann == nil || !namesTypeParam(ann, params) {
			continue
		}
		labels := g.fa.References
		if analysis.IsPunnedField(f) {
			// `Span{start, stop}`: References at a punned label holds the
			// variable, and the field's instantiated type is kept apart.
			labels = g.fa.PunnedFieldLabels
		}
		sym := labels[analysis.Pos{Line: f.Line, Col: f.Col}]
		if sym == nil || sym.Kind != analysis.SymbolField {
			return nil, false
		}
		k := g.project(sym.Type)
		if !irRetainedLeafKind(k) && !irCallableValueKind(k) {
			return nil, false
		}
		g.unifyTypeParams(ann, k, params, solved)
	}
	args, ok := templateArgs(tpl, solved)
	if !ok {
		return nil, false
	}
	for _, k := range args {
		// A leaf, or any value the IR carries: the instance's own layout is
		// checked where the literal is built (irRetainedStructKind).
		if !irRetainedLeafKind(k) && !irRetainedValueKind(k) {
			return nil, false
		}
	}
	return args, true
}

// unifyTypeParams matches a declared annotation against an actual kind, binding
// every type parameter the annotation names.
//
// Structural and one-directional: the annotation is the pattern and the kind is
// the subject. A parameter already bound to a DIFFERENT kind is left at its
// first binding rather than overwritten, so a literal the front end would have
// rejected cannot silently pick the last field's answer — the caller then builds
// an instance whose field kinds disagree with the values, and structValue
// refuses each mismatched field by kind. Never picking is the rule impl.go's
// duplicate-impl arm states: do not let ordering decide.
//
// `params` is the SET of names to treat as parameters, rather than a
// *genericTemplate, because a generic FUNCTION's type parameters ask this
// function exactly the same question a generic STRUCT's do — "which parameter
// was `T` bound to, given this annotation and this actual kind" — and the only
// thing a template would be read for is `templateParamSet`. Two unifiers would
// be two answers to one question. Taking the set also stops the leaf arm
// rebuilding the same map at every SimpleType it visits.
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
		if actual.def != nil && actual.def.genericOf != nil &&
			actual.def.genericOf.nomi == a.Name &&
			len(actual.def.genericArgs) == len(a.Params) {
			for i, p := range a.Params {
				g.unifyTypeParams(p, actual.def.genericArgs[i], params, solved)
			}
			return
		}
		// `items: List<T>`, `m: Maybe<T>` — a structural or prelude container
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

// templateParamSet is the template's parameters as a set, for namesTypeParam and
// the unifier's leaf test.
func templateParamSet(tpl *genericTemplate) map[string]bool {
	out := make(map[string]bool, len(tpl.params))
	for _, p := range tpl.params {
		out[p] = true
	}
	return out
}

// kindsNomi is an argument list's Nomi spelling, for a refusal detail only.
func kindsNomi(args []kind) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = a.nomi()
	}
	return strings.Join(parts, ", ")
}
