package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// Cross-package type identity: a user type declared in one Nomi FILE named
// from another.
//
// # The boundary this crosses, and the one it does not
//
// One Nomi FILE is one package in the builder's Go spellings (see siblings.go),
// and a type crosses that boundary. `scalarKind` alone answers only for
// scalars (`Int` is `int64` in every package), and siblings.go states why a
// named type cannot simply be shared: "a named type's identity IS the
// *typeDef its declaration produced, and a *typeDef belongs to exactly one
// generated package".
//
// The answer is to give the referencing package its OWN *typeDef for the same
// Nomi type —
// a MIRROR — whose Go name is package-qualified and whose layout is copied
// verbatim from the owner's. Two packages then hold two pointers naming one Go
// type, which is exactly what Go's own type checker means by identity across a
// package boundary. Pointer equality stays the identity WITHIN a package, which
// is the only place any of this builder's kind comparisons happen: `g.comps`,
// `g.tids`, `g.implsByIface` and every `k == k` are per-gen.
//
// One *typeDef shared by every gen looks simpler and does not work. A shared
// def's field
// kinds are interned in the OWNER's `g.comps`, so a field of type `List<Point>`
// renders as `*rt.List[NomiT_Point]`: correct in the owner, undefined in every
// other package. Making it correct everywhere means rendering a kind relative
// to the package asking. A mirror re-interns its own components in the asking
// gen instead.
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
// # What is refused, and why each is refused rather than guessed
//
//   - `sibling file import cycle` — a type reference is a Go IMPORT, so it
//     joins the reference graph siblings.go closes, and an edge that closes a
//     cycle is refused at its own position. Nomi permits cyclic file imports
//     deliberately; Go forbids cyclic package imports outright. component.go's
//     partition makes this a fail-safe.
//   - `sibling file type cycle` — file A's type table needs B's while B's needs
//     A's. Distinct from the import-cycle key because it is detected while
//     BUILDING the tables rather than at a reference, and the two can be
//     reached independently.
//   - `existential over a sibling file type` — a fail-safe, not a reachable
//     key. `hasTID`'s mirror path answers from the OWNER's table, and
//     `mintTypeIDs` records eagerly in the declaring file, so a boxed value
//     crossing a file boundary has one identity. existential.go's
//     importIfaceKind mirrors the INTERFACE's def and propagates the mirror's
//     OWN reason (a type cycle) instead of this key.
//   - `generic sibling file type` — a generic declaration is refused in its own
//     file too, and the mirror inherits that.

// typeOwner is one user file's type declaration as the whole PROGRAM sees it.
type typeOwner struct {
	// unit is the declaring module's index, and pkg its generated package.
	unit int
	pkg  string
	// fileKey is the analyzer's module-path key, for refusal text.
	fileKey string
	nomi    string
	decl    ast.Node
	// isIface marks an `interface` declaration rather than a type. Both live
	// in one registry because a NAME reference to either is the same thing to
	// the reference graph — a Go import — and because closeReferences must
	// see an interface reference for exactly that reason. The two are handed
	// out through separate accessors (foreignOwner, foreignIfaceOwner) so a
	// type lookup can never come back holding an interface.
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
	// gens is one generator per module, filled by Generate before any type
	// table is built. Indexed by unit.
	gens []*gen
	// pkgOf is each unit's Go-spelled package and owner its component
	// representative. Neither is the unit index: a strongly-connected
	// component of the Nomi import graph becomes ONE Go package, because Go
	// forbids an import cycle and Nomi deliberately permits one. See
	// component.go.
	pkgOf []string
	owner []int
	// equatable is every DECLARATION the program writes an `impl Equatable`
	// for, gathered once on first ask and nil until then — most programs never
	// ask. Keyed on the declaration NODE because that is the type's identity
	// and the question is asked from a package that holds only a mirror. See
	// declaresEquatable.
	equatable map[ast.Node]bool
	// aliasOwner is the Go-spelled PACKAGE plus mirror name of every foreign
	// alias some gen has already claimed, so exactly one gen declares it.
	//
	// A mirror's `type NomiT_X = pkg.NomiT_X` is deduplicated per GEN by
	// flushForeignAliases, and a gen is one FILE while a generated package is
	// one COMPONENT. So two co-tenants of a merged package each mirroring the
	// same sibling type emitted the alias TWICE into one Go package —
	// `NomiT_ProdLogger redeclared in this block`. Keyed on `(pkg, name)`
	// because a mirror in a DIFFERENT package must still declare its own.
	aliasOwner map[string]bool
}

// buildTypeRegistry indexes a program's type declarations by the node that
// declared them.
func buildTypeRegistry(p *Program) *typeRegistry {
	owner := componentOwners(p)
	r := &typeRegistry{
		byDecl: make(map[ast.Node]*typeOwner),
		pkgOf:  unitPackagesFrom(owner),
		owner:  owner,
	}
	for i := range p.Modules {
		m := &p.Modules[i]
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
				pkg:     r.pkgOf[i],
				fileKey: m.Name,
				nomi:    name,
				decl:    n,
				isIface: iface,
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
func (r *typeRegistry) ensureTypes(unit int) bool {
	if unit < 0 || unit >= len(r.gens) {
		return false
	}
	g := r.gens[unit]
	if g == nil {
		return false
	}
	if g.typesDone {
		return true
	}
	if g.typesBusy {
		return false
	}
	g.typesBusy = true
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

// foreignOwner resolves a TYPE name this file does not declare to the user file
// that does, or nil when nothing in the program declares it (a stdlib type, an
// interface, or no type at all).
func (g *gen) foreignOwner(name string) *typeOwner {
	o := g.foreignDecl(name)
	if o == nil || o.isIface {
		return nil
	}
	return o
}

// foreignIfaceOwner is foreignOwner for an `interface` declaration.
//
// Separate accessor rather than one that answers for both, because every
// caller of foreignOwner goes on to build a type MIRROR — a Go alias, a
// layout, variant tags — and an interface has none of those. A single
// accessor would make `foreignType("Logger")` answer with an owner and
// report the interface as `unlowered type`, which is a mis-named refusal.
func (g *gen) foreignIfaceOwner(name string) *typeOwner {
	o := g.foreignDecl(name)
	if o == nil || !o.isIface {
		return nil
	}
	return o
}

// foreignDecl resolves a name to the sibling-file declaration it names, type or
// interface alike.
func (g *gen) foreignDecl(name string) *typeOwner {
	if g.reg == nil || g.fa == nil || g.fileUnit < 0 {
		return nil
	}
	return g.ownerOfSymbol(resolvedTypeSymbol(g.fa, name))
}

// ownerOfSymbol is foreignDecl's second half: the registry entry a resolved
// symbol names, or nil when it names nothing this program declares or names
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

// foreignType resolves a type name to a MIRROR of another file's declaration,
// reporting whether the name named one at all.
//
// The mirror is registered in g.types under the name it was reached by, so
// typeOf, the IR builder's construction and variant resolution and the `case`
// machinery all reach it through the one map a type name is ever looked up in
// (gen.types' comment). Nothing downstream distinguishes a mirror from a local
// declaration except the two places that must: a field default cannot be
// emitted here, and an existential cannot mint a second runtime identity.
func (g *gen) foreignType(name string) (*typeDef, bool) {
	o := g.foreignOwner(name)
	if o == nil {
		return nil, false
	}
	d := g.mirrorOf(o)
	g.types[name] = d
	return d, true
}

// mirrorOf is this package's mirror of one owner's declaration, built once per
// declaration NODE and cached on it.
//
// Split out of foreignType because a mirror is reachable by two different
// questions and only one of them has a name to ask with. foreignType asks
// "what does THIS FILE's imports bind `Widget` to". project() asks "what did
// the ANALYZER solve, at origin O" — for positions where the program never
// writes the name at all, so there is no import to resolve through. Both must
// reach the same *typeDef, because that pointer IS the type's identity here
// and two mirrors of one declaration would be two Go types. See siblingOwner.
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
	switch {
	case !g.reg.ensureTypes(o.unit):
		d = g.blockedMirror(o, "sibling file type cycle", o.fileKey+"."+o.nomi)
	default:
		src := g.reg.gens[o.unit].types[o.nomi]
		if src == nil {
			// The declaring file registered no shell for it, which happens
			// only for a duplicate declaration its own file refuses.
			d = g.blockedMirror(o, "unlowered type", o.fileKey+"."+o.nomi)
		} else {
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
// A linear scan over the program's modules rather than an index: it runs only
// for a nominal type whose origin is NOT this file's, which is a path taken
// once per distinct inferred sibling type, and `gens` is one entry per source
// FILE. An index would also have to live on typeRegistry, which is shared.
func (g *gen) siblingOwner(origin, name string) *typeOwner {
	if g.reg == nil || origin == "" {
		return nil
	}
	for unit, src := range g.reg.gens {
		if src == nil || src.fa == nil || src.fa.Origin != origin {
			continue
		}
		d := src.types[name]
		if d == nil || d.decl == nil {
			return nil
		}
		o := g.reg.byDecl[d.decl]
		if o == nil || o.isIface || o.unit != unit {
			// isIface: a type lookup must never come back holding an
			// interface, which is the invariant foreignOwner keeps for the
			// by-name path.
			return nil
		}
		return o
	}
	return nil
}

// namedType resolves a type NAME to this package's def for it: its own
// declaration, or a mirror of another file's.
//
// The one place a type name is looked up, which is what gen.types' comment
// claims for that map; the map alone is not the whole answer. Local wins: a file that declares `Point` and imports another
// `Point` is a front-end error, and if it ever stops being one the local
// declaration is the one the checker resolves.
func (g *gen) namedType(name string) (*typeDef, bool) {
	// A type declared in an enclosing BLOCK, which the name-keyed table below
	// cannot hold: two bodies may each declare `Point`. Innermost-first, which
	// is the analyzer's own resolution order. See blocklocaltype.go.
	if d, isBlockLocal := g.blockLocalNamed(name); isBlockLocal {
		return d, true
	}
	if d, local := g.types[name]; local {
		return d, true
	}
	// A stdlib opaque newtype, whose Go type rt declares. Ahead of the mirror
	// route because it is not a user file's type at all, and mutually exclusive
	// with one: the anchor requires the analyzer to have resolved the name to a
	// std origin. See opaque.go.
	if d, isOpaque := g.opaqueNamed(name); isOpaque {
		return d, true
	}
	// A monomorphic stdlib enum, same story: `Ordering` is rt.Ordering, and the
	// anchor requires the analyzer to have resolved the name to a std origin.
	// See stdenum.go.
	if d, isStdEnum := g.stdEnumNamed(name); isStdEnum {
		return d, true
	}
	// A stdlib opaque STRUCT, same story once more: `DateTime` is rt.DateTime.
	// See stdstruct.go.
	if d, isStdStruct := g.stdStructNamed(name); isStdStruct {
		return d, true
	}
	// A stdlib `pub host type`, same story a fourth time: `Bytes` is rt.Bytes.
	// See stdhost.go.
	if d, isStdHost := g.stdHostNamed(name); isStdHost {
		return d, true
	}
	// A stdlib nil-inner MARKER, same story a fifth time: `ChannelClosed` is
	// rt.ChannelClosed. See stdgenhost.go.
	if d, isMarker := g.stdMarkerNamed(name); isMarker {
		return d, true
	}
	// A named PAYLOAD of a generic std enum: `Failure` is rt.Failure. Same
	// story a sixth time — the anchor requires the analyzer to have resolved
	// the name to a std origin. It is not an stdEnumSpecs row; see
	// taskoutcome.go.
	if d, isPayload := g.namedPayloadNamed(name); isPayload {
		return d, true
	}
	if d, isForeign := g.foreignType(name); isForeign {
		return d, true
	}
	// `shapes.Shape` — a declaration named through a WHOLE-FILE import's
	// qualifier. LAST, so every arm above keeps the answer it had: a local
	// declaration, a namespaced one registered under its own dotted name, and
	// a stdlib anchor are all asked first, and this runs only where the name
	// resolved to nothing. See modulequaltype.go.
	return g.moduleQualifiedDotted(name)
}

// userTypeNamed reports that a type NAME, in a user unit, resolves to a type a
// user file declares: this file's own declaration, one in an enclosing block,
// or another file's reached through an import. Nominal identity is (declaring
// file, name), so a user `Task` is not std/tasks' `Task`, and an arm that
// recognises a stdlib type by its spelling (`Task.spawn`, `Sender.send`) must
// not claim a call on it. A stdlib module's own unit declares no user type.
func (g *gen) userTypeNamed(name string) bool {
	if g.stdModule != "" {
		return false
	}
	if _, isBlockLocal := g.blockLocalNamed(name); isBlockLocal {
		return true
	}
	if _, local := g.types[name]; local {
		return true
	}
	_, isForeign := g.foreignType(name)
	return isForeign
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

// foreignAlias is one `type <local> = <target>` declaration.
type foreignAlias struct {
	local  string
	target string
	// nomi is the Nomi type it names, file-qualified, for the comment.
	nomi string
}

// importDef builds this package's mirror of another package's typeDef.
//
// The LAYOUT is copied verbatim — field order, Go field names, variant tags,
// slot assignment, boxing — because the layout is a property of the Go type the
// owner declared and this package is naming that same type, not declaring a
// second one. Only the KINDS are re-derived, through importKind, so every
// component renders relative to this package.
//
// Registered in the intern map BEFORE its components are imported, so a
// recursive type (`struct Tree { left: Tree }`, whose field is boxed) reaches
// its own mirror rather than recursing forever.
func (g *gen) importDef(src *typeDef, o *typeOwner) *typeDef {
	d := &typeDef{nomi: src.nomi, decl: src.decl, line: src.line, isEnum: src.isEnum, isDistinct: src.isDistinct, tagName: src.tagName, foreign: o.fileKey, lowerable: src.lowerable, ownerDef: src}
	if o.instSrc != nil {
		// A MONOMORPHIZED instance, interned by the owner's def rather than
		// by the declaration node every instance of the template shares. The
		// insertion stays ahead of the component walk for the same
		// recursion-breaking reason.
		if g.foreignInstDefs == nil {
			g.foreignInstDefs = map[*typeDef]*typeDef{}
		}
		g.foreignInstDefs[o.instSrc] = d
		// The TEMPLATE travels with the mirror, because a qualifier at a use
		// site names the template and `declaredAs` has to be able to say the
		// name and the value agree. It is the OWNER gen's template pointer,
		// which is what `foreignTemplate` resolves the qualifier to here, so
		// the two sides compare equal.
		//
		// `genericArgs` deliberately does NOT travel: they are the owner's
		// kinds, and re-deriving them here would be a second import of
		// components importDef already imports. unifyTypeParams reads both
		// together and so declines a mirror.
		d.genericOf = src.genericOf
		d.instOrigin = o.instSrc
	} else {
		g.foreignDefs[o.decl] = d
	}
	if !src.lowerable {
		// Its own file refuses it and reports why at its own position; every
		// use here reports the same thing a use in that file would, through
		// declRefusal, so one refusal reads the same from either side of a file
		// boundary. `unlowered type` is only the fallback.
		if construct, detail, named := declRefusal(src); named {
			d.why, d.whyDetail = construct, o.fileKey+"."+detail
			return d
		}
		d.why, d.whyDetail = "unlowered type", o.fileKey+"."+src.nomi
		return d
	}
	fail := func(why, detail string) *typeDef {
		d.lowerable, d.why, d.whyDetail = false, why, detail
		return d
	}
	// kindInvalid: marker — asks whether the distinct has an inner at all; the fail arm reports.
	if src.inner != kindInvalid {
		k, ok := g.importKind(src.inner)
		if !ok {
			return fail(g.importWhy, o.fileKey+"."+src.nomi+" wraps "+src.inner.nomi())
		}
		d.inner = k
	}
	for _, f := range src.fields {
		k, ok := g.importKind(f.k)
		if !ok {
			return fail(g.importWhy, o.fileKey+"."+src.nomi+"."+f.nomi+": "+f.k.nomi())
		}
		d.fields = append(d.fields, fieldDef{nomi: f.nomi, k: k, deflt: f.deflt, boxed: f.boxed})
		d.note(k)
	}
	for _, v := range src.variants {
		vd := variantDef{nomi: v.nomi, tag: v.tag, kind: v.kind}
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
	for _, s := range src.slots {
		k, ok := g.importKind(s.k)
		if !ok {
			return fail(g.importWhy, o.fileKey+"."+src.nomi+" stores "+s.k.nomi())
		}
		d.slots = append(d.slots, slotDef{k: k, boxed: s.boxed, users: s.users})
	}
	return d
}

// importKind re-derives one of the owner's kinds relative to THIS package.
//
// A scalar is the same kind in every package and answers itself. A structural
// type is rebuilt through the same constructor that made it, so it re-interns
// in this gen's own table against this gen's rendering of its parts — which is
// the whole reason mirrors exist rather than shared defs. A named type recurses
// into another mirror.
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
	case tagUnit, tagInt, tagFloat, tagString, tagBool,
		tagEmptyList, tagBareNone, tagEmptyMap, tagEmptySet, tagEmptyVector:
		return k, true
	case tagIface:
		if _, isStd := stdIfaceOf(k.iface); isStd {
			// A STDLIB interface's *ifaceDef is PROCESS-WIDE and its dispatch
			// table is a variable in rt, so `rt.Dyn` over it is literally the
			// same kind in every package: there is nothing to re-derive, no
			// second def to intern, and no second table to mint. The refusal
			// below is about a def interned in the OWNER's gen, which this is
			// not. See stdiface.go.
			return k, true
		}
		return g.importIfaceKind(k.iface)
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
	}
	g.importWhy = "unlowered type"
	return kindInvalid, false
}

// importNamed mirrors a named type reached as another type's component.
//
// A PRELUDE instance is re-instantiated rather than mirrored: its Go type is
// `rt.Maybe[T]`, declared in rt and therefore already package-neutral, but the
// `T` inside it is not — so the instance has to be rebuilt at this package's
// spelling of its type arguments, through the same preludeInstance every other
// site goes through. See prelude.go.
func (g *gen) importNamed(src *typeDef) (kind, bool) {
	if src.preludeOf != nil {
		g.loadPreludes()
		a := g.preludeByName[src.preludeOf.spec.nomi]
		if a == nil {
			// The OWNER established the identity — this def carries the spec
			// it was built from — so what is missing is only the shape
			// validation an anchor also carries, and std's own declaration
			// supplies that without a scope. See preludeorigin.go.
			var anchored bool
			if a, anchored = preludeSpecAnchor(src.preludeOf.spec); !anchored {
				g.importWhy = "prelude enum without an anchor"
				return kindInvalid, false
			}
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
		// A GENERIC STD STRUCT instance — `Set<Int>`, `Channel<Request>`.
		// Re-instantiated rather than mirrored, for importNamed's prelude
		// reason and not for a weaker version of it: the Go type `rt.Channel[T]`
		// is declared in rt and is package-neutral, but the `T` inside it need
		// not be, so the instance has to be rebuilt at THIS package's spelling
		// of its type arguments and through the same door every other site
		// goes through.
		//
		// A PROCESS-WIDE instance survives this walk unchanged rather than
		// bypassing it: every argument is neutral, so importKind hands each
		// one back identically and genStructInstance finds the same shared
		// entry. So this arm is correct for both halves of the split and does
		// not need to ask which half it is looking at.
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
		// A USER GENERIC INSTANCE — `Holder<Int>`, declared in the owner's
		// package. MIRRORED rather than re-instantiated, which is the
		// opposite of the three arms above and is forced by where the Go type
		// lives: `rt.Maybe[T]`, `rt.Set[T]` and `rt.DateTime` are declared in
		// rt and can be rebuilt anywhere, while `NomiT_Holder_Int` is EMITTED
		// into exactly one generated package. Rebuilding it here would
		// declare a SECOND Go type with the same layout, and a value of one
		// is not assignable to the other, so a call across the boundary would
		// not compile.
		//
		// Ahead of the `reg.byDecl` walk below, which would answer with the
		// TEMPLATE's owner: every instance of one template shares `decl`, so
		// that route mirrors the declaration `Holder` — whose def carries
		// declModifiers' `generic type` — and reports the instantiation under
		// the family refusal rather than under anything true of the instance.
		return g.importGenericInstance(src)
	}
	if src.rtDeclared {
		// AN ANCHORED STDLIB TYPE WHOSE GO TYPE RT DECLARES — `Duration`,
		// `Instant`, `Ordering`, `DateTime`, `Diagnostic`. Its *typeDef is
		// PROCESS-WIDE and its Go spelling is `rt.X` in every generated
		// package, so it is literally the same kind here as in the owner:
		// there is nothing to re-derive and no second def to intern. Exactly
		// the argument importKind's tagIface arm makes for a stdlib interface,
		// and `named(src).packageNeutral()` is true for this def by the rule
		// stdprelude.go states and TestStdStructDefsArePackageNeutral pins.
		//
		// It must come before `g.reg.byDecl[src.decl]` below: a shared def
		// carries NO declaration node (buildTypes adopts the shared def rather
		// than minting one), so that lookup would miss and refuse `non-local
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
		if local := g.types[src.nomi]; local != nil && local.decl == src.decl {
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
	if !g.reg.ensureTypes(o.unit) {
		g.importWhy = "sibling file type cycle"
		return kindInvalid, false
	}
	inner := g.reg.gens[o.unit].types[o.nomi]
	if inner == nil {
		g.importWhy = "unlowered type"
		return kindInvalid, false
	}
	d := g.importDef(inner, o)
	if !d.lowerable {
		g.importWhy = mirrorWhy(d)
		return kindInvalid, false
	}
	return named(d), true
}

// --- what a mirror cannot see for itself ------------------------------------

// foreignNoEquatableImpl establishes, for a MIRROR, that no file in the program
// writes an `impl Equatable` for the type — the question noEquatableImpl asks
// of a local declaration by reading its own table.
//
// It has to be POSITIVE, and the reason is the one noEquatableImpl gives: a
// missing impl is exactly when `==` is structural equality, and an impl this
// builder cannot CALL from here is when `==` calls something instead, so a
// structural answer is right in the first case and a silent wrong answer in the
// second. A failed lookup cannot tell those apart. Answering false
// unconditionally would be correct and would refuse every `==` on a sibling
// file's struct, including the common case where the program declares no impl
// at all.
//
// The PROGRAM, not the owner's file. Nomi's orphan rule admits an `impl Equatable for Point` in Point's
// MODULE or in Equatable's, and a Nomi module is several FILES — so `struct
// Point` in a.nomi and `impl Equatable for Point` in b.nomi is legal, and
// reading only a.nomi's gen would answer "no impl" for a type that has one.
//
// Nothing here is keyed on a name. The declaration NODE is the identity, the
// mirror carries the owner's own node verbatim (importDef), and a receiver is
// matched by that node — so two files each declaring a `Point` cannot answer
// for each other. The interface is matched on its SPELLING, which is what the
// local arm does too, and the error direction is the safe one: a user who
// declares their own `interface Equatable` makes this answer false and
// over-refuse, never the reverse.
func (g *gen) foreignNoEquatableImpl(d *typeDef) bool {
	if g.reg == nil || d.decl == nil {
		return false
	}
	o := g.reg.byDecl[d.decl]
	if o == nil {
		// Not a user declaration this program indexed. A stdlib-anchored def
		// reached through a sibling file gets here, and its impls live in the
		// stdlib index rather than in any gen's table, so absence is not
		// established.
		return false
	}
	// The owner's own def, reached through the node rather than trusted from
	// the mirror: `src.decl != d.decl` means the owner's table no longer holds
	// the declaration this mirror was built from, and a mirror whose original
	// cannot be found establishes nothing.
	owner := g.reg.gens[o.unit]
	if owner == nil {
		return false
	}
	if src := owner.types[o.nomi]; src == nil || src.decl != d.decl {
		return false
	}
	return !g.reg.declaresEquatable(d.decl)
}

// declaresEquatable reports whether ANY file in the program writes an
// `impl Equatable` for this declaration, lowerable or not.
//
// "Or not" is the load-bearing half. An impl that exists and cannot be lowered
// must make the comparison REFUSE, not silently become structural — the same
// rule noEquatableImpl's std probe follows by reading `byIface` before the
// `why` filtering stdlibImplOf applies. A `derive Equatable` counts: the front
// end turns it into an impl block and `==` dispatches to it.
//
// Answers false — "no impl" — only once every gen's table is COMPLETE. While
// any is short of declareFuncs the answer is "declared", which over-refuses,
// because a half-built table cannot be told apart from an empty one.
func (r *typeRegistry) declaresEquatable(decl ast.Node) bool {
	if r.equatable == nil {
		for _, g := range r.gens {
			if g == nil || !g.funcsDone {
				return true
			}
		}
		r.equatable = map[ast.Node]bool{}
		for _, g := range r.gens {
			for _, impl := range g.implOrder {
				if impl.ifaceName != "Equatable" || impl.recv.tag != tagNamed || impl.recv.def == nil {
					continue
				}
				if impl.recv.def.decl != nil {
					r.equatable[impl.recv.def.decl] = true
				}
			}
		}
	}
	return r.equatable[decl]
}

// --- the reference graph ----------------------------------------------------

// typeNamesIn collects every type name the node mentions, at any depth.
//
// Reflective for the reason childNodes is: the AST has no visitor and a
// hand-written switch over every node kind that can hold a type expression
// would silently miss the next one added. Deliberately a SUPERSET — it counts a
// name in a subtree that will be refused, and a name that a local binding
// shadows — because over-counting can only over-refuse an import cycle, while
// under-counting would let a cycle between unit packages through unreported.
//
// It differs from childNodes in exactly one way, and that way is the point:
// childNodes drops every ast.TypeExpr (a declared type's representation is
// typeOf's answer, not a blocker in its own right), and a type reference across
// a file boundary is precisely a TypeExpr.
func typeNamesIn(n ast.Node, out map[string]bool) {
	walkNames(n, out, pickTypeName)
}

// pickTypeName contributes a node's TYPE names and reports whether the walk
// should stop there.
func pickTypeName(n ast.Node, out map[string]bool) bool {
	switch t := n.(type) {
	case *ast.SimpleType:
		out[t.Name] = true
		return true
	case *ast.GenericType:
		out[t.Name] = true
	case *ast.QualifiedType:
		out[t.Module] = true
	case *ast.TypeIdent:
		out[t.Name] = true
		return true
	}
	return false
}

// walkNames collects names from a node tree, with `pick` deciding which names
// each node contributes and whether to descend into it.
//
// Generic over `pick` because there are THREE questions with the same shape and
// one traversal: which TYPES a node mentions (the reference graph), which NAMES
// OF ANY KIND it mentions (existential.go's portability check for a sibling
// file's interface default), and which type DECLARATIONS it contains at a
// nested position (sigreason.go's nestedTypeDeclKey). pick returning true
// stops the walk below that node.
//
// Generic over the ACCUMULATOR too, because the third question's answer is a
// name-to-KEY map rather than a set. A concrete `map[string]bool` here would
// make that caller either smuggle its map through a closure or copy the walk.
func walkNames[T any](n ast.Node, out T, pick func(ast.Node, T) bool) {
	ast.Inspect(n, func(n ast.Node) bool { return !pick(n, out) })
}
