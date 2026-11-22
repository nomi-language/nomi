package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// Cross-file type identity: a user type declared in one Nomi file named from
// another.
//
// # Each gen holds its own def for another file's type
//
// Every file has its own gen, and a gen's kind comparisons are its own:
// `g.comps`, `g.tids`, `g.implsByIface` and every `k == k` are per gen. A
// named type's identity is the *typeDef its declaration produced, and that
// def belongs to the declaring file's gen (see siblings.go). `scalarKind`
// alone answers only for scalars, which are the same kind in every gen.
//
// So the referencing gen gets its own *typeDef for the same Nomi type, a
// mirror, whose layout is copied from the owner's and which points at the
// owner's def (ownerDef). Pointer equality stays the identity within a gen.
//
// One *typeDef shared by every gen does not work. A shared def's field kinds
// are interned in the owner's `g.comps`, so its field of type `List<Point>`
// is a different *compKind from the `List<Point>` the asking gen builds, and
// the two compare unequal. A mirror re-interns its own components in the
// asking gen.
//
// # Identity is the declaration NODE, never the name
//
// A type identity spelled as a string and matched by name is a silent wrong
// answer. Two files may each declare a `Point`, the checker keeps them apart
// nominally, and a registry keyed on `"Point"` would mix them.
//
// So the key is the `ast.Node` the declaration IS. That is strictly stronger
// than the analyzer's own (Origin, Name) rule that inferred.go and prelude.go
// use — those two read an `analysis.Type`, which carries no declaration pointer,
// so (Origin, Name) is the strongest form available there. Here the pointer is
// available and is used directly.
//
// Lookup follows the proxy chain: a selectively imported type (`import
// app.EffectsApp`) resolves to a PROXY symbol whose own `Node` is the import
// site and whose `Resolved` is the real symbol in the declaring file, carrying
// that file's own `*ast.StructDef` pointer. Reading the first non-nil `Node`
// answers with the import and finds no declaration at all, so every cross-file
// type would look like a stdlib type.
//
// # Privacy is not this file's question
//
// A mirror is built for a non-`pub` declaration too. Whether a file may NAME
// another file's type is the analyzer's rule, and it rejects every spelling
// that does (`other.Hidden`, `import other.Hidden`). What reaches a mirror
// without a name is legal: an instance of another file's generic built in the
// declaring file over the caller's private type argument (`span.ident(Point{x:
// 1})`), an app payload read by a sibling, an inferred position.
//
// A mirror's field DEFAULT is the declaring file's Nomi expression and is
// never lowered here: a literal that omits the field calls an accessor the
// declaring file's gen builds (foreignfielddefault.go).
//
// # What is refused
//
//   - `sibling file type cycle`: file A's type table needs B's while B's needs
//     A's. It is detected while building the tables. A cycle of type
//     declarations is built as a type group (typecycle.go), so this is reached
//     only by a demand the group's edges miss.
//   - `existential over a sibling file type`: a fail-safe, not a reachable
//     key. `hasTID`'s mirror path answers from the owner's table, and
//     `mintTypeIDs` records eagerly in the declaring file, so a boxed value
//     crossing a file boundary has one identity. existential.go's
//     importIfaceKind mirrors the interface's def and propagates the mirror's
//     own reason (a type cycle) instead of this key.

// typeOwner is one user file's type declaration as the whole PROGRAM sees it.
type typeOwner struct {
	// unit is the declaring module's index.
	unit int
	// fileKey is the analyzer's module-path key, for refusal text.
	fileKey string
	nomi    string
	decl    ast.Node
	// isIface marks an `interface` declaration and not a type. Both live in
	// one registry because both are found by the node that declares them.
	// Each lookup checks it (siblingOwner, siblingIfaceOwner), so a type
	// lookup never comes back holding an interface.
	isIface bool
	// instSrc is the OWNER's monomorphized instance def when this record
	// stands for `Holder<Int>` rather than for the declaration `Holder`. It
	// is what importDef interns the mirror under, because every instance of
	// one template shares `decl`. Nil for an ordinary declaration.
	instSrc *typeDef
}

// typeRegistry is every user type declaration in one program, keyed by the node
// that declared it.
type typeRegistry struct {
	byDecl map[ast.Node]*typeOwner
	// fileOf is, by analyzer origin, the first program file with that
	// origin, whose module scope holds the declarations the checker names
	// by that origin (declNode).
	fileOf map[string]originFile
	// ifaceParamDefaults is where each parameter default of an interface
	// function the program's files declare is declared, gathered on first
	// ask (ifaceslotdefault.go).
	ifaceParamDefaults map[ast.Node]ifaceParamDefault
	// linkNames is the linkage path of each declaration of the program's
	// own files (link.go).
	linkNames map[ast.Node]declName
	// imports is the symbol of each linkage name the linked std modules
	// define (programstdlink.go), or nil; linked is their kept lowering.
	imports map[string]*ir.Symbol
	linked  *linkedStd
	// gens is one generator per module, filled by Generate before any type
	// table is built. Indexed by unit.
	gens []*gen
	// pkgOf is each unit's unit package name and owner its component
	// representative. Neither is the unit index: the files of one strongly
	// connected component of the Nomi import graph share one name, the
	// namespace key their gens compare. See component.go.
	pkgOf []string
	owner []int
	// erasedInsts is every generic instance some file holds erased, by the
	// owner's def's portable spelling (erasedKey): an impl another file
	// writes for it is recorded for dispatch there (irInstanceImpls).
	erasedInsts map[string]bool
	// boxedEnums is every enum declaration with an `embeds` variant some
	// file boxes into an existential (irboxed.go), whose impls its declaring
	// file records under the enum's own name for the box's dispatch, by
	// the declaration's spelling (declKey). Both are spelled rather than
	// keyed by pointer so that a lowering of one file (perfile.go) can be
	// given what the program's other files hold erased and box.
	boxedEnums map[string]bool
	// debugRendered is, by its spelling (debugIfaceKey), each interface
	// whose values some body renders through every implementer's Debug in
	// the program (existentialDebugImpls), with the gens of those bodies:
	// such a rendering reads every file's implementers, so a file lowered
	// on its own cannot make it, nor change what another file's makes,
	// without missing (perfile.go, KeptDebugRendering).
	debugRendered map[string]map[*gen]bool
	// perFile is what a lowering of one file is given of its program, or
	// nil (perfile.go).
	perFile *perFileState
	// groupOf is each unit's type group, nil for a unit on no cycle of type
	// references, found on the first ensureTypes. See typecycle.go.
	groupOf     []*typeGroup
	groupsFound bool
}

// originFile is the program file with one analyzer origin: its unit and
// its analysis.
type originFile struct {
	unit int
	fa   *analysis.FileAnalysis
}

// buildTypeRegistry indexes a program's type declarations by the node that
// declared them.
func buildTypeRegistry(p *Program) *typeRegistry {
	owner := componentOwners(p)
	r := &typeRegistry{
		byDecl: make(map[ast.Node]*typeOwner),
		fileOf: make(map[string]originFile),
		pkgOf:  unitPackagesFrom(owner),
		owner:  owner,
	}
	for i := range p.Modules {
		m := &p.Modules[i]
		if m.FA != nil && m.FA.Origin != "" {
			if _, seen := r.fileOf[m.FA.Origin]; !seen {
				r.fileOf[m.FA.Origin] = originFile{unit: i, fa: m.FA}
			}
		}
		for _, n := range m.Nodes {
			switch n.(type) {
			case *ast.FuncDef, *ast.ExternFunc, *ast.OnceBinding:
				continue
			}
			var name string
			iface := false
			switch t := n.(type) {
			case *ast.StructDef:
				name = t.Name
			case *ast.EnumDef:
				name = t.Name
			case *ast.TypeDef:
				if isDistinctRefinementDecl(t) {
					// Erased: no file mirrors it (refinement.go).
					continue
				}
				name = t.Name
			case *ast.InterfaceDef:
				name, iface = t.Name, true
			default:
				continue
			}
			if _, dup := r.byDecl[n]; dup {
				continue
			}
			r.byDecl[n] = &typeOwner{
				unit:    i,
				fileKey: m.Name,
				nomi:    name,
				decl:    n,
				isIface: iface,
			}
		}
	}
	// A std module checked from edited source (a reference editor, `nomi test`
	// of an edited std file) is the entry in place of its shared copy, and a
	// module lowered beside it that imports it (std/sql/testing beside an edited
	// std/sql) names the shared copy's declarations: each of them is the
	// edited declaration's (implFuncTwins).
	for i := range p.Modules {
		for shared, edited := range p.Modules[i].twins {
			if o := r.byDecl[edited]; o != nil && r.byDecl[shared] == nil {
				r.byDecl[shared] = o
			}
		}
	}
	return r
}

// ensureTypes builds unit's type table if it has not been built, reporting
// whether it is now usable.
//
// Demand-driven rather than topologically ordered, because the demand IS the
// dependency: file B's `struct Wrapper { p: Point }` needs A's table at the
// moment it resolves `Point`, and nothing else needs to know the order. The
// busy flag catches the case a topological sort would have had to reject
// anyway — A's table needing B's while B's needs A's — and reports it as
// `sibling file type cycle` rather than resolving against a half-built table.
//
// Files whose type declarations refer to each other are a type group and are
// built together, phase by phase (typecycle.go). While a group is being built,
// a member's table answers true once its shells and generic templates are
// registered; ensureLayout and ensureIfaces ask for more.
func (r *typeRegistry) ensureTypes(unit int) bool {
	if unit < 0 || unit >= len(r.gens) {
		panic("irbuild: ensureTypes: unit index always comes from typeOwner.unit, an index into r.gens")
	}
	g := r.gens[unit]
	if g == nil {
		panic("irbuild: ensureTypes: every registry unit has a gen")
	}
	if g.typesDone {
		return true
	}
	if grp := r.groupFor(unit); grp != nil {
		if grp.state == groupIdle {
			r.buildGroup(grp)
		}
		return grp.state >= groupResolving
	}
	if g.typesBusy {
		// Its own build is under way: a file asking for its types while it
		// resolves fields gets pending mirrors (deferringGroup).
		return g.building != nil && g.building.state >= groupResolving
	}
	g.typesBusy = true
	g.building = &typeGroup{units: []int{unit}}
	g.declareTypes()
	g.typesBusy = false
	return true
}

// resolvedTypeSymbol follows a name in one file's scope to the symbol that
// DECLARED it, through any chain of selective-import proxies.
//
// See the file comment: reading the first non-nil Node answers with the import
// site. Bounded because a proxy chain is data, not a bounded shape — the same
// bound moduleScopeOf uses one file over.
func resolvedTypeSymbol(fa *analysis.FileAnalysis, name string) *analysis.Symbol {
	if fa == nil || fa.ModuleScope == nil {
		return nil
	}
	return resolvedScopeSymbol(fa.ModuleScope, name)
}

// resolvedScopeSymbol is resolvedTypeSymbol over an ARBITRARY scope, which is
// what a module-qualified reference needs: `sqlite.Conn` resolves `Conn` in the
// scope the qualifier is bound to, never in this file's own. See
// modulequaltype.go.
func resolvedScopeSymbol(scope *analysis.Scope, name string) *analysis.Symbol {
	if scope == nil {
		return nil
	}
	sym := scope.Lookup(name)
	for range 8 {
		if sym == nil {
			return nil
		}
		if sym.Resolved == nil {
			return sym
		}
		sym = sym.Resolved
	}
	return nil
}

// ownerOfSymbol is the registry entry a resolved symbol names, or nil when it names nothing this program declares or names
// something in THIS file.
//
// Split out because a module-qualified reference reaches the same registry
// through a different scope. See modulequaltype.go.
func (g *gen) ownerOfSymbol(sym *analysis.Symbol) *typeOwner {
	if g.reg == nil || sym == nil || sym.Node == nil {
		return nil
	}
	o := g.reg.byDecl[sym.Node]
	if o == nil || o.unit == g.fileUnit {
		return nil
	}
	return o
}

// mirrorOf is this gen's mirror of one owner's declaration, built once per
// declaration node and cached on it.
//
// Every question that reaches a declaration of another file (what the
// analyzer solved at origin O, the declaration a reference resolved to)
// reaches the same *typeDef here, because that pointer is the type's
// identity and two mirrors of one declaration would be two unequal kinds
// for one type. See siblingOwner.
//
// # A mirror does not ask whether the declaration is `pub`
//
// Nomi's `pub` decides whether another FILE MAY NAME a type, and the analyzer
// rejects every spelling that names a private one. The askers that reach a
// mirror of a private declaration spell no name: an ambient app read
// (`15-app-and-defer/app_field_permission/`, whose payload `struct Settings`
// is private to the entry), inference, and an instance of another file's
// generic over the caller's private type argument, which the declaring file
// builds. All three are legal programs, so the mirror is built for them.
//
// A PROPERTY OF THE NODE rather than of the asker, so every asker gets ONE
// answer for one declaration. Deciding it per call site would let the first
// caller's question install a mirror the second caller must not have, which is
// exactly the cache-order hazard the one-mirror-per-node rule above exists to
// prevent.
func (g *gen) mirrorOf(o *typeOwner) *typeDef {
	if d, ok := g.foreignDefs[o.decl]; ok {
		return d
	}
	var d *typeDef
	// A member of this file's type group whose fields are being resolved has
	// no layout to copy yet. See typecycle.go.
	deferred := g.reg.deferringGroup(g.fileUnit, o.unit)
	switch {
	case deferred == nil && !g.reg.ensureLayout(o.unit):
		d = g.blockedMirror(o, "sibling file type cycle", o.fileKey+"."+o.nomi)
	default:
		src := g.reg.defOf(o)
		switch {
		case src == nil:
			// The declaring file registered no shell for it, which happens
			// only for a duplicate declaration its own file refuses.
			d = g.blockedMirror(o, "unlowered type", o.fileKey+"."+o.nomi)
		case deferred != nil:
			return g.pendingMirror(deferred, src, o)
		default:
			d = g.importDef(src, o)
		}
	}
	g.foreignDefs[o.decl] = d
	return d
}

// siblingOwner is the declaration of `name` in the user module whose analyzer
// ORIGIN is `origin`, resolved WITHOUT consulting this file's imports.
//
// The origin comparison is the identity check, and it is why this cannot be a
// name lookup. A mirror crossing a file boundary is keyed on the declaration
// NODE, and every impl lookup keys on that: resolve `Widget` to the wrong
// file's declaration and the miss is SILENT — `Display` renders the anonymous
// form, `==` falls back to structural comparison and answers False, ordering
// traps. Two sibling files may each declare `Widget`, and only the origin the
// analyzer solved says which one a given inferred position meant.
//
// The declaration is the one the name has in the declaring file's module
// scope (siblingDecl), which the checker resolved: the declaring gen's own
// table holds it only once that gen has built its types.
func (g *gen) siblingOwner(origin, name string) *typeOwner {
	o := g.siblingDecl(origin, name)
	if o == nil || o.isIface {
		// isIface: a type lookup must never come back holding an
		// interface.
		return nil
	}
	return o
}

// blockedMirror is a mirror that names the other file's type and refuses every
// use of it, under the reason it was refused for.
//
// A refused mirror rather than a miss, because a MISS reports `non-local type`
// — "no such type here" — and that is the wrong report for a type that exists
// and closes a type cycle. siblings.go's
// stdFileQualifier comment makes the same point about mis-named refusals.
func (g *gen) blockedMirror(o *typeOwner, why, detail string) *typeDef {
	return &typeDef{nomi: o.nomi, decl: o.decl, line: o.decl.LineNum(), foreign: o.fileKey, why: why, whyDetail: detail, lowerable: why == ""}
}

// importDef builds this gen's mirror of another gen's typeDef.
//
// The layout is copied: field order, variant tags, slot assignment and
// boxing. The mirror names the type the owner declared and does not declare a
// second one. Only the kinds are re-derived, through importKind, so every
// component is interned in this gen.
func (g *gen) importDef(src *typeDef, o *typeOwner) *typeDef {
	d := g.mirrorShell(src, o)
	if !src.lowerable {
		refuseMirror(d, src, o)
		return d
	}
	if g.importMirrorKinds(d, src, o) {
		g.copyMirrorLayout(d, src, o)
	}
	return d
}

// mirrorShell is a mirror's identity with no layout yet, registered in the
// intern map.
//
// Registered BEFORE its components are imported, so a recursive type (`struct
// Tree { left: Tree }`, whose field is boxed) reaches its own mirror rather
// than recursing forever.
func (g *gen) mirrorShell(src *typeDef, o *typeOwner) *typeDef {
	defer constructingType()()
	d := &typeDef{nomi: src.nomi, decl: src.decl, line: src.line, isEnum: src.isEnum, isDistinct: src.isDistinct, foreign: o.fileKey, lowerable: src.lowerable, ownerDef: src}
	if o.instSrc != nil {
		// A MONOMORPHIZED instance, interned by the owner's def rather than
		// by the declaration node every instance of the template shares. The
		// insertion stays ahead of the component walk for the same
		// recursion-breaking reason.
		if g.foreignInstDefs == nil {
			g.foreignInstDefs = map[*typeDef]*typeDef{}
		}
		g.foreignInstDefs[o.instSrc] = d
		// The TEMPLATE travels with the mirror, because a use site's
		// template (genericTemplateOf) must be the one the value was made
		// from. It is the OWNER gen's template pointer, which is what
		// `foreignTemplateAt` resolves the declaration to here, so the two
		// sides compare equal.
		//
		// `genericArgs` deliberately does NOT travel: they are the owner's
		// kinds, and re-deriving them here would be a second import of
		// components importDef already imports. A reader that needs them
		// imports the owner's (instanceArgs).
		d.genericOf = src.genericOf
		d.instOrigin = o.instSrc
	} else {
		g.foreignDefs[o.decl] = d
	}
	return d
}

// refuseMirror makes a mirror of a declaration its own file refuses report
// what a use in that file would.
//
// Its own file reports why at its own position; every use here reports the
// same thing through declRefusal, so one refusal reads the same from either
// side of a file boundary. `unlowered type` is only the fallback.
func refuseMirror(d, src *typeDef, o *typeOwner) {
	defer changingTypes()()
	d.lowerable = false
	if construct, detail, named := declRefusal(src); named {
		d.why, d.whyDetail = construct, o.fileKey+"."+detail
		return
	}
	d.why, d.whyDetail = "unlowered type", o.fileKey+"."+src.nomi
}

// importMirrorKinds re-derives the owner's inner, field and payload kinds into
// a mirror, reporting whether every one imported. A failure refuses the mirror
// under the component's reason.
//
// The layout that depends on the whole type graph (which fields are boxed,
// which slot a payload uses) is copied apart, by copyMirrorLayout, because a
// group of files whose types refer to each other imports every mirror's kinds
// before any of them has a layout. See typecycle.go.
func (g *gen) importMirrorKinds(d, src *typeDef, o *typeOwner) bool {
	defer changingTypes()()
	fail := func(why, detail string) bool {
		d.lowerable, d.why, d.whyDetail = false, why, detail
		return false
	}
	// kindInvalid: marker — asks whether the distinct has an inner at all; the fail arm reports.
	if src.inner != kindInvalid {
		k, ok := g.importKind(src.inner)
		if !ok {
			return fail(g.importWhy, o.fileKey+"."+src.nomi+" wraps "+src.inner.nomi())
		}
		d.inner = k
		d.spread = src.spread
	}
	for _, f := range src.fields {
		k, ok := g.importKind(f.k)
		if !ok {
			return fail(g.importWhy, o.fileKey+"."+src.nomi+"."+f.nomi+": "+f.k.nomi())
		}
		d.fields = append(d.fields, fieldDef{nomi: f.nomi, k: k, deflt: f.deflt})
		d.note(k)
	}
	for _, v := range src.variants {
		vd := variantDef{nomi: v.nomi, tag: v.tag, kind: v.kind, spread: v.spread}
		for _, p := range v.payloads {
			k, ok := g.importKind(p.k)
			if !ok {
				return fail(g.importWhy, o.fileKey+"."+src.nomi+"."+v.nomi+": "+p.k.nomi())
			}
			vd.payloads = append(vd.payloads, payload{nomi: p.nomi, k: k, deflt: p.deflt, slot: p.slot})
			d.note(k)
		}
		if v.embeds != nil {
			k, ok := g.importKind(named(v.embeds))
			if !ok || k.def == nil {
				return fail(g.importWhy, o.fileKey+"."+src.nomi+"."+v.nomi+" embeds "+v.embeds.nomi)
			}
			vd.embeds = k.def
		}
		d.variants = append(d.variants, vd)
	}
	return true
}

// copyMirrorLayout copies the owner's boxing and slot assignment into a mirror
// whose kinds importMirrorKinds imported.
func (g *gen) copyMirrorLayout(d, src *typeDef, o *typeOwner) {
	defer changingTypes()()
	for i := range d.fields {
		d.fields[i].boxed = src.fields[i].boxed
	}
	for i := range d.variants {
		for j := range d.variants[i].payloads {
			d.variants[i].payloads[j].slot = src.variants[i].payloads[j].slot
		}
	}
	for _, s := range src.slots {
		k, ok := g.importKind(s.k)
		if !ok {
			d.lowerable, d.why, d.whyDetail = false, g.importWhy, o.fileKey+"."+src.nomi+" stores "+s.k.nomi()
			return
		}
		d.slots = append(d.slots, slotDef{k: k, boxed: s.boxed})
	}
}

// importKind re-derives one of the owner's kinds in this gen.
//
// A scalar is the same kind in every gen and answers itself. A structural
// type is rebuilt through the same constructor that made it, so it interns in
// this gen's own table over this gen's parts, which is why mirrors exist and
// shared defs do not. A named type recurses into another mirror.
//
// The reason for a refusal is left in g.importWhy rather than returned, because
// every caller reports it against a different subject (a field, a payload, a
// slot) and threading a second return through the recursion would say the same
// thing four times.
func (g *gen) importKind(k kind) (kind, bool) {
	switch k.tag {
	case tagInvalid:
		g.importWhy = "unlowered type"
		return kindInvalid, false
	case tagUnit, tagInt, tagFloat, tagString, tagBool, tagNever:
		return k, true
	case tagSeq:
		elem, ok := g.importKind(k.comp.parts[0])
		if !ok {
			return kindInvalid, false
		}
		return seqKindIn(g, elem), true
	case tagAnonStruct:
		parts := make([]kind, 0, len(k.comp.parts))
		for _, p := range k.comp.parts {
			ip, ok := g.importKind(p)
			if !ok {
				return kindInvalid, false
			}
			parts = append(parts, ip)
		}
		// anonStructKind is the only constructor of a record kind, so k has
		// one name per part and at least one part, and every imported part
		// is a kind: the record interns.
		rk := g.anonStructKind(k.comp.names, parts)
		// kindInvalid: sentinel — the invariant above, which no checked program breaks.
		if rk == kindInvalid {
			panic("irbuild: importKind could not intern the record " + k.nomi() + " from its imported parts")
		}
		return rk, true
	case tagIface:
		// An interface's kind, or a conjunction's, which is its members'
		// (conjunction.go), each imported.
		members := conjMembers(k)
		for i, m := range members {
			if _, isStd := stdIfaceOf(m.iface); isStd {
				// A stdlib interface's *ifaceDef is process-wide, so the
				// existential over it is the same kind in every gen: there
				// is nothing to re-derive and no second def to intern. The
				// refusal below is about a def interned in the owner's gen,
				// which this is not. See stdiface.go.
				continue
			}
			im, ok := g.importIfaceKind(m.iface)
			if !ok {
				return kindInvalid, false
			}
			if !irConjKind(k) {
				return im, true
			}
			members = append([]kind(nil), members...)
			members[i] = im
		}
		if !irConjKind(k) {
			return k, true
		}
		return g.conjKind(members), true
	case tagNamed:
		return g.importNamed(k.def)
	case tagFunc:
		params := make([]kind, 0, len(k.comp.parts)-1)
		for _, p := range funcParams(k) {
			ip, ok := g.importKind(p)
			if !ok {
				return kindInvalid, false
			}
			params = append(params, ip)
		}
		res, ok := g.importKind(funcResult(k))
		if !ok {
			return kindInvalid, false
		}
		return g.funcKind(params, res), true
	case tagList:
		elem, ok := g.importKind(k.comp.parts[0])
		if !ok {
			return kindInvalid, false
		}
		return g.listKind(elem), true
	case tagMap:
		key, ok := g.importKind(k.comp.parts[0])
		if !ok {
			return kindInvalid, false
		}
		val, ok := g.importKind(k.comp.parts[1])
		if !ok {
			return kindInvalid, false
		}
		return g.mapKind(key, val), true
	case tagTuple:
		parts := make([]kind, 0, len(k.comp.parts))
		for _, p := range k.comp.parts {
			ip, ok := g.importKind(p)
			if !ok {
				return kindInvalid, false
			}
			parts = append(parts, ip)
		}
		return g.tupleKind(parts), true
	case tagTypeParam:
		// A bound-free type parameter's kind is held only by its own
		// generic declaration's signature and shell, and is never mirrored.
		g.importWhy = "unlowered type"
		return kindInvalid, false
	}
	// Every tag has an arm above (TestImportKindHasAnArmForEveryTag).
	panic("irbuild: importKind reached with a kind of no tag: " + k.nomi())
}

// importNamed mirrors a named type reached as another type's component.
//
// A prelude instance is re-instantiated and not mirrored: any gen builds one
// from its anchor, and the `T` inside it is one gen's kind, so the instance is
// rebuilt over this gen's import of its type arguments, through the same
// preludeInstance every other site goes through. See prelude.go.
func (g *gen) importNamed(src *typeDef) (kind, bool) {
	if src.preludeOf != nil {
		// The OWNER established the identity — this def carries the spec it
		// was built from — so what is left is the shape validation an anchor
		// carries, which std's own declaration supplies without a scope. See
		// preludeorigin.go.
		a, anchored := preludeSpecAnchor(src.preludeOf.spec)
		if !anchored {
			panic("irbuild: importNamed: std's own declaration does not anchor the prelude enum " + src.nomi)
		}
		args := make([]kind, 0, len(src.preludeArgs))
		for _, arg := range src.preludeArgs {
			ia, ok := g.importKind(arg)
			if !ok {
				return kindInvalid, false
			}
			args = append(args, ia)
		}
		k := g.preludeInstance(a, args)
		// kindInvalid: reports — sets importWhy, which the caller rejects under.
		if k == kindInvalid {
			g.importWhy = "unlowered type"
			return kindInvalid, false
		}
		return k, true
	}
	if spec, srcArgs, isGenStruct := genStructOf(named(src)); isGenStruct {
		// A generic std struct instance: `Set<Int>`, `Channel<Request>`.
		// Re-instantiated and not mirrored, for the prelude arm's reason: any
		// gen builds one from its spec, and the `T` inside it is one gen's
		// kind, so the instance is rebuilt over this gen's import of its type
		// arguments.
		//
		// A process-wide instance comes back unchanged: every argument is
		// neutral, so importKind hands each one back identically and
		// genStructInstance finds the same shared entry. So this arm is
		// correct for both halves of the split and does not ask which half
		// it is looking at.
		args := make([]kind, 0, len(srcArgs))
		for _, arg := range srcArgs {
			ia, ok := g.importKind(arg)
			if !ok {
				return kindInvalid, false
			}
			args = append(args, ia)
		}
		k, ok := g.genStructInstance(spec, args...)
		if !ok {
			g.importWhy = "unlowered type"
			return kindInvalid, false
		}
		return k, true
	}
	if spec, srcArgs, isGenHost := genHostOf(named(src)); isGenHost {
		// A GENERIC STD HOST instance — `Sender<Request>`, `Vector<Point>`.
		// The arm above, over the leaf family.
		args := make([]kind, 0, len(srcArgs))
		for _, arg := range srcArgs {
			ia, ok := g.importKind(arg)
			if !ok {
				return kindInvalid, false
			}
			args = append(args, ia)
		}
		k, ok := g.genHostInstance(spec, args...)
		if !ok {
			g.importWhy = "unlowered type"
			return kindInvalid, false
		}
		return k, true
	}
	if src.genericOf != nil {
		// A user generic instance: `Holder<Int>`, built in the gen that
		// declares the template. Mirrored and not re-instantiated, the
		// opposite of the three arms above: the owner's gen registered the
		// instance's impls on the def it built, so this gen names that def
		// through a mirror and builds no second instance
		// (foreigngeneric.go).
		//
		// Ahead of the `reg.byDecl` walk below, which would answer with the
		// template's owner: every instance of one template shares `decl`, so
		// that route mirrors the declaration `Holder`, whose def carries
		// declModifiers' `generic type`, and reports the instantiation under
		// the family refusal and not under anything true of the instance.
		return g.importGenericInstance(src)
	}
	if src.rtDeclared || src.stdOrigin != "" {
		// An anchored stdlib type rt declares: `Duration`, `Instant`,
		// `Ordering`, `DateTime`, `Diagnostic`. Its *typeDef is process-wide,
		// so it is the same kind here as in the owner: there is nothing to
		// re-derive and no second def to intern. importKind's tagIface arm
		// makes the same argument for a stdlib interface, and
		// `named(src).packageNeutral()` is true for this def by the rule
		// stdprelude.go states and TestStdStructDefsArePackageNeutral pins.
		//
		// It must come before `g.reg.byDecl[src.decl]` below: a shared def
		// carries no declaration node (buildTypes adopts the shared def and
		// mints none), so that lookup would miss and refuse `non-local
		// type` for a type that is fully representable and package-neutral
		// (`helper.format(at: Instant)` in a sibling file, say).
		return named(src), src.lowerable
	}
	o := g.reg.byDecl[src.decl]
	if o == nil {
		// A type the owner declared that the registry never saw. Only a
		// synthesized declaration can reach this, and one is not nameable
		// from another file.
		g.importWhy = "non-local type"
		return kindInvalid, false
	}
	if o.unit == g.fileUnit {
		// The owner's component is declared in THIS file — the reference
		// crosses back. Its own def is the identity; the mirror must not be a
		// second one.
		if local := g.reg.defOf(o); local != nil && local.decl == src.decl {
			return named(local), local.lowerable
		}
		g.importWhy = "non-local type"
		return kindInvalid, false
	}
	if d, ok := g.foreignDefs[o.decl]; ok {
		if !d.lowerable {
			g.importWhy = mirrorWhy(d)
			return kindInvalid, false
		}
		return named(d), true
	}
	// A member of this file's type group whose fields are being resolved has
	// no layout to copy yet. See typecycle.go.
	deferred := g.reg.deferringGroup(g.fileUnit, o.unit)
	if deferred == nil && !g.reg.ensureLayout(o.unit) {
		g.importWhy = "sibling file type cycle"
		return kindInvalid, false
	}
	inner := g.reg.defOf(o)
	if inner == nil {
		g.importWhy = "unlowered type"
		return kindInvalid, false
	}
	if deferred != nil {
		return named(g.pendingMirror(deferred, inner, o)), true
	}
	d := g.importDef(inner, o)
	if !d.lowerable {
		g.importWhy = mirrorWhy(d)
		return kindInvalid, false
	}
	return named(d), true
}

// walkNames collects names from a node tree, with `pick` deciding which names
// each node contributes and whether to descend into it.
//
// Generic over `pick` because there are two questions with the same shape and
// one traversal: which NAMES OF ANY KIND a node mentions (existential.go's portability check for a sibling
// file's interface default), and which type DECLARATIONS it contains at a
// nested position (sigreason.go's nestedTypeDeclKey). pick returning true
// stops the walk below that node.
//
// Generic over the ACCUMULATOR too, because the second question's answer is a
// name-to-KEY map rather than a set. A concrete `map[string]bool` here would
// make that caller either smuggle its map through a closure or copy the walk.
func walkNames[T any](n ast.Node, out T, pick func(ast.Node, T) bool) {
	ast.Inspect(n, func(n ast.Node) bool { return !pick(n, out) })
}
