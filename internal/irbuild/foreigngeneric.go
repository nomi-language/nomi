package irbuild

import "github.com/nomi-language/nomi/internal/analysis"

// A monomorphized instance of another file's generic template: `Holder<Int>`
// where `pub enum Holder<T>` is declared in a sibling file.
//
// # Why this is a mirror and the three arms beside it are not
//
// importNamed re-instantiates a prelude instance, a generic std struct and a
// generic std host type: any gen builds one from its spec, so the instance is
// rebuilt here over this gen's import of its arguments.
//
// A user template's instance is built by the gen that declares the template
// (generictype.go's intern table is per gen), and that gen registers the
// instance's impls on the def it built (instanceImpl). So this gen does not
// build a second instance: it mirrors the owner's def, as every ordinary
// cross-file struct reference does.
//
// This arm is foreign.go's ordinary mirror applied to a def that was built
// under a substitution. `importDef` copies the layout and re-derives only the
// component kinds, and a component importKind refuses refuses the mirror, by
// name, as it does for a non-generic mirror.
//
// # Identity is the owner's def pointer
//
// `foreignDefs` keys a mirror on the declaration node, and that is wrong here
// because every instance of one template shares `tpl.decl`: one key would
// hand `Holder<Int>` and `Holder<String>` the same mirror, and each
// instantiation would read another's field types. `foreignInstDefs` is keyed
// on the owner's instance def, which is that instantiation's identity in the
// owning gen by genericInstKey's rule.
//
// # The owner builds it, whenever it is asked
//
// Modules are built in `Program.Modules` order (the entry, then
// `proj.Files`, which is not reverse-topological), so an owner can be asked
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
	if grp := src.layoutGroup; grp != nil && grp.state == groupResolving {
		// The instance waits for its group's layout (deferInstLayout), so
		// the mirror does too.
		return named(g.pendingMirror(grp, src, o)), true
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
// Answers nil when the template is not another FILE's, or is one the
// registry never saw, which is not nameable from here. A co-tenant's
// template (another file in this file's import cycle) is another file's:
// its instances live in that file's gen, as a non-generic co-tenant type
// does, so they are mirrored the same way.
func (g *gen) instanceOwner(src *typeDef) *typeOwner {
	if g.reg == nil || src.genericOf == nil || src.genericOf.decl == nil {
		return nil
	}
	o := g.reg.byDecl[src.genericOf.decl]
	if o == nil || o.unit == g.fileUnit {
		return nil
	}
	return &typeOwner{
		unit:    o.unit,
		fileKey: o.fileKey,
		nomi:    src.nomi,
		decl:    o.decl,
		instSrc: src,
	}
}

// foreignTemplateRef is one imported module's generic template plus everything
// needed to build an instance of it there and name it here.
type foreignTemplateRef struct {
	src *gen
	tpl *genericTemplate
}

// foreignTemplateAt resolves the template another user file declares as
// `name`, where that file is the one whose analyzer origin is `origin`: the
// template of an instance the CHECKER solved at a position the program never
// spells (`fn t ->` over a `List<Tree<String>>` whose `Tree` another file
// declares). By origin rather than through this file's imports, for
// siblingOwner's reason.
func (g *gen) foreignTemplateAt(origin, name string) (foreignTemplateRef, bool) {
	if g.reg == nil || origin == "" {
		return foreignTemplateRef{}, false
	}
	return g.foreignTemplateOf(g.siblingDecl(origin, name))
}

// foreignTemplateOf is the template the owner o names, when o is another
// file's declaration, a co-tenant's included (instanceOwner), found in the
// declaring gen by the declaration (templatesByDecl).
func (g *gen) foreignTemplateOf(o *typeOwner) (foreignTemplateRef, bool) {
	if o == nil || o.unit == g.fileUnit {
		return foreignTemplateRef{}, false
	}
	if !g.reg.ensureTypes(o.unit) {
		return foreignTemplateRef{}, false
	}
	src := g.reg.gens[o.unit]
	if src == nil {
		panic("irbuild: foreignTemplateOf: every unit has a gen in the registry after ensureTypes succeeds")
	}
	tpl := src.templatesByDecl[o.decl]
	if tpl == nil {
		return foreignTemplateRef{}, false
	}
	return foreignTemplateRef{src: src, tpl: tpl}, true
}

// instantiateForeignAt builds `tpl<args>` in the OWNER's gen and mirrors the
// result here, with types, the checker's types for args or nil, which the
// owner's instance carries.
//
// The arguments are this gen's kinds, so each is handed to the owner through
// importKind, as a cross-file generic function's arguments are
// (siblinggeneric.go): a scalar is itself there, and `Box<Inner>` over this
// file's own `Inner` is the owner's mirror of `Inner`, which importKind
// resolves back to this file's def when the instance's fields come home.
func (g *gen) instantiateForeignAt(ref foreignTemplateRef, args []kind, types []analysis.Type) (kind, bool) {
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
	return g.instantiateThere(ref, there, types)
}

// instantiateThere is instantiateForeignAt for args that are already the
// owner's kinds.
func (g *gen) instantiateThere(ref foreignTemplateRef, args []kind, types []analysis.Type) (kind, bool) {
	k, built := ref.src.genericInstance(ref.tpl, args, types)
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
//
// The impl may be written in a third file (siblingtemplateimpl.go), so every
// gen is asked, each at its own def for the instance.
func (g *gen) instanceImpl(mirror *typeDef, iface, method string) (*gen, *implDef) {
	src := mirror.instOrigin
	if g.reg == nil || src == nil || src.genericOf == nil || src.genericOf.decl == nil {
		return nil, nil
	}
	return g.instanceImplAnywhere(src, iface, method)
}
