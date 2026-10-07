package irbuild

import "strings"

// A MONOMORPHIZED instance of ANOTHER package's generic template — `Holder<Int>`
// where `pub enum Holder<T>` is declared in a sibling file.
//
// # WHY THIS IS A MIRROR AND THE THREE ARMS BESIDE IT ARE NOT
//
// importNamed re-instantiates a prelude instance, a generic std struct and a
// generic std host type: their Go types are `rt.Maybe[T]`, `rt.Set[T]`,
// `rt.Vector[T]`, declared in rt and therefore spellable from every generated
// package, so the honest thing is to rebuild the instance at THIS package's
// spelling of its arguments.
//
// A user template's instance has no such home. `NomiT_Holder_Int` is EMITTED,
// into whichever package the gen that built it writes (generictype.go's intern
// table is per-gen). `Box<Inner>` cannot be rebuilt elsewhere because `Inner`
// is one package's type, but `Holder<Int>`, whose every field is `int64`, has
// no such obstacle, and a mirror does not rebuild a type anyway: it NAMES the
// owner's, through a Go alias, which is the mechanism every ordinary
// cross-file struct reference uses.
//
// So this arm is foreign.go's ordinary mirror applied to a def that happens to
// have been built under a substitution. `importDef` copies the LAYOUT verbatim
// and re-derives only the component KINDS, and a component that is one
// package's own type refuses there, by name, exactly as it does for a
// non-generic mirror. What refuses is a cross-package instance with a
// component this package cannot name.
//
// # IDENTITY IS THE OWNER'S DEF POINTER
//
// `foreignDefs` keys a mirror on the declaration NODE, and that is wrong here
// because every instance of
// one template shares `tpl.decl`, so one key would hand `Holder<Int>` and
// `Holder<String>` the same mirror, and each instantiation would silently read
// another's field types. `foreignInstDefs` is keyed on the owner's instance def instead, which
// is that instantiation's identity in the owning gen by genericInstKey's rule.
//
// # THE OWNER BUILDS IT, WHENEVER IT IS ASKED
//
// Modules are built in `Program.Modules` order — the entry, then
// `proj.Files`, which is not reverse-topological — so an owner can be asked
// for a new instance after its own module walk. The def is complete when
// genericInstance returns, and the impls it registers are queued on the
// owner; irFlushLateInstances builds them once every walk is done.
func (g *gen) importGenericInstance(src *typeDef) (kind, bool) {
	if src.instOrigin != nil {
		// A mirror: the owner's def is the instance's identity, so a mirror
		// of a mirror is never built.
		src = src.instOrigin
	}
	if g.reg != nil && src.genericOf != nil && src.genericOf.decl != nil {
		if o := g.reg.byDecl[src.genericOf.decl]; o != nil && o.unit == g.fileUnit {
			// The instance crosses back to the file that declares its
			// template: its own def is the identity.
			if !src.lowerable {
				g.importWhy = mirrorWhy(src)
				return kindInvalid, false
			}
			return named(src), true
		}
	}
	if d, seen := g.foreignInstDefs[src]; seen {
		if !d.lowerable {
			g.importWhy = mirrorWhy(d)
			return kindInvalid, false
		}
		return named(d), true
	}
	o := g.instanceOwner(src)
	if o == nil {
		g.importWhy = "generic type"
		return kindInvalid, false
	}
	if !g.reg.ensureTypes(o.unit) {
		g.importWhy = "sibling file type cycle"
		return kindInvalid, false
	}
	d := g.importDef(src, o)
	if !d.lowerable {
		g.importWhy = mirrorWhy(d)
		return kindInvalid, false
	}
	return named(d), true
}

// instanceOwner is the registry record for one INSTANCE: the template's owner,
// with the instance's own def, so two instantiations of one template do not
// collide.
//
// Answers nil when the template is not another PACKAGE's: a co-tenant's
// instance is already declared in this Go package and naming it needs no
// alias, and a template the registry never saw is not nameable from here.
func (g *gen) instanceOwner(src *typeDef) *typeOwner {
	if g.reg == nil || src.genericOf == nil || src.genericOf.decl == nil {
		return nil
	}
	o := g.reg.byDecl[src.genericOf.decl]
	if o == nil || o.unit == g.fileUnit || o.pkg == g.pkg {
		return nil
	}
	return &typeOwner{
		unit:    o.unit,
		pkg:     o.pkg,
		fileKey: o.fileKey,
		nomi:    src.nomi,
		decl:    o.decl,
		instSrc: src,
	}
}

// genericTemplateNamed resolves a generic template by the name a use site
// spells, LOCAL or module-qualified, together with the instantiator that puts
// the instance's Go type where it belongs.
//
// One resolver and one instantiator handed back together, rather than a
// local/foreign branch at each of the four use sites (a type annotation, a
// struct literal, a variant call, a variant literal). Those four already
// shared one body per construction shape, and a second copy of each for the
// qualified spelling would drift from the first — the same argument
// calleeTypeQualifier makes for the bare and dotted spellings of a call
// qualifier.
//
// The instantiator differs because WHERE the Go type is declared differs: a
// local template's instance is emitted into this package, and an imported
// one's into the owner's and named here through an alias.
func (g *gen) genericTemplateNamed(name string) (*genericTemplate, func([]kind) (kind, bool), bool) {
	tpl := g.blockLocalTemplate(name)
	if tpl == nil {
		tpl = g.genericTemplates[name]
	}
	if tpl != nil {
		return tpl, func(args []kind) (kind, bool) { return g.genericInstance(tpl, args) }, true
	}
	ref, found := g.foreignTemplate(name)
	if !found {
		return nil, nil, false
	}
	return ref.tpl, func(args []kind) (kind, bool) { return g.instantiateForeign(ref, args) }, true
}

// foreignTemplateRef is one imported module's generic template plus everything
// needed to build an instance of it there and name it here.
type foreignTemplateRef struct {
	owner *typeOwner
	src   *gen
	tpl   *genericTemplate
}

// foreignTemplate resolves `<qualifier>.<Name>` to the template the qualified
// module declares under that name, and a bare `Name` to the template a
// selective import (`import span.Span`) binds it to.
//
// Through `moduleQualifiedOwner` and `foreignOwner`, the same doors a
// non-generic type goes through, so an ALIASED import and a re-export facade
// reach the template by the declaration the analyzer resolved rather than by
// the spelling — modulequaltype.go's rule, applied to a generic declaration.
func (g *gen) foreignTemplate(name string) (foreignTemplateRef, bool) {
	if g.reg == nil {
		return foreignTemplateRef{}, false
	}
	var o *typeOwner
	if dot := strings.LastIndex(name, "."); dot < 0 {
		o = g.foreignOwner(name)
	} else if dot > 0 && dot < len(name)-1 {
		o = g.moduleQualifiedOwner(name[:dot], name[dot+1:])
	}
	return g.foreignTemplateOf(o)
}

// foreignTemplateAt resolves the template another user file declares as
// `name`, where that file is the one whose analyzer origin is `origin`: the
// template of an instance the CHECKER solved at a position the program never
// spells (`|t|` over a `List<Tree<String>>` whose `Tree` another file
// declares). By origin rather than through this file's imports, for
// siblingOwner's reason.
func (g *gen) foreignTemplateAt(origin, name string) (foreignTemplateRef, bool) {
	if g.reg == nil || origin == "" {
		return foreignTemplateRef{}, false
	}
	for unit, src := range g.reg.gens {
		if src == nil || src.fa == nil || src.fa.Origin != origin {
			continue
		}
		if !g.reg.ensureTypes(unit) {
			return foreignTemplateRef{}, false
		}
		tpl := src.genericTemplates[name]
		if tpl == nil || tpl.decl == nil {
			return foreignTemplateRef{}, false
		}
		o := g.reg.byDecl[tpl.decl]
		if o == nil || o.unit != unit {
			return foreignTemplateRef{}, false
		}
		return g.foreignTemplateOf(o)
	}
	return foreignTemplateRef{}, false
}

// foreignTemplateOf is the template the owner o names, when o is another
// package's declaration.
func (g *gen) foreignTemplateOf(o *typeOwner) (foreignTemplateRef, bool) {
	if o == nil || o.unit == g.fileUnit || o.pkg == g.pkg {
		return foreignTemplateRef{}, false
	}
	if !g.reg.ensureTypes(o.unit) {
		return foreignTemplateRef{}, false
	}
	src := g.reg.gens[o.unit]
	if src == nil {
		return foreignTemplateRef{}, false
	}
	tpl := src.genericTemplates[o.nomi]
	if tpl == nil {
		return foreignTemplateRef{}, false
	}
	return foreignTemplateRef{owner: o, src: src, tpl: tpl}, true
}

// instantiateForeign builds `tpl<args>` in the OWNER's gen and mirrors the
// result here.
//
// The arguments are this gen's kinds, so each is handed to the owner through
// importKind, as a cross-file generic function's arguments are
// (siblinggeneric.go): a scalar is itself there, and `Box<Inner>` over this
// file's own `Inner` is the owner's mirror of `Inner`, which importKind
// resolves back to this file's def when the instance's fields come home.
func (g *gen) instantiateForeign(ref foreignTemplateRef, args []kind) (kind, bool) {
	there := make([]kind, len(args))
	for i, a := range args {
		// kindInvalid: propagates — a refused type ARGUMENT already reported.
		if a == kindInvalid {
			return kindInvalid, false
		}
		k, ok := ref.src.importKind(a)
		if !ok {
			return kindInvalid, false
		}
		there[i] = k
	}
	k, built := ref.src.genericInstance(ref.tpl, there)
	if !built || k.def == nil {
		return kindInvalid, false
	}
	return g.importGenericInstance(k.def)
}

// instanceImpl is the impl the OWNER registered for the generic instance a
// mirror names, with the owner's gen: the one of iface, or, with iface empty,
// the one impl that has method. Nil when there is none, or more than one.
//
// Every instance of a template shares its declaration node, so the lookup
// that finds a non-generic mirror's impl by node (mirroredImpl,
// qualSiblingIfaceImplPlan) would hand `Span<String>` the impl of whichever
// instantiation it met first. The owner's instance def (instOrigin) is the
// instantiation's identity, and registerInstanceImpls keyed its impls on it.
func (g *gen) instanceImpl(mirror *typeDef, iface, method string) (*gen, *implDef) {
	src := mirror.instOrigin
	if g.reg == nil || src == nil || src.genericOf == nil || src.genericOf.decl == nil {
		return nil, nil
	}
	o := g.reg.byDecl[src.genericOf.decl]
	if o == nil || o.unit < 0 || o.unit >= len(g.reg.gens) || g.reg.gens[o.unit] == nil {
		return nil, nil
	}
	owner := g.reg.gens[o.unit]
	recv := named(src)
	if iface != "" {
		d := owner.implsByIface[iface][recv]
		if d == nil || (method != "" && d.items[method] == nil) {
			return nil, nil
		}
		return owner, d
	}
	var found *implDef
	for _, byRecv := range owner.implsByIface {
		d := byRecv[recv]
		if d == nil || d.items[method] == nil {
			continue
		}
		if found != nil {
			return nil, nil
		}
		found = d
	}
	if found == nil {
		return nil, nil
	}
	return owner, found
}
