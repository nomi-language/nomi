package irbuild

import (
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// A type named through a WHOLE-FILE import's qualifier — `sqlite.Conn`,
// `store.SqliteStore` — resolved to the same mirror the bare imported name
// already resolves to.
//
// # Why a qualified name needs its own path
//
// Two sibling files, with no go.mod, no `gopkg`, no host type and no opaque
// wrapper:
//
//	lib.nomi   pub struct Plain { v: Int }
//
//	main.nomi  import { lib }
//	           fn f() -> Result<lib.Plain, String>
//
//	main.nomi  import { lib.{Plain} }
//	           fn f() -> Result<Plain, String>
//
// Same type, same declaring file, same signature position, one difference in
// spelling. `foreignDecl` resolves a bare name through `g.fa.ModuleScope`,
// where a SELECTIVE import binds the type itself; a whole-file import binds
// only the QUALIFIER, as a `SymbolModule` carrying the declaring file's own
// scope, and this file is what reads that scope for a type.
//
// The analyzer does the same: `registerModuleQualifiedTypes`
// (analysis/type_builder.go) registers every type member of an imported
// module's scope under `<alias>.<Name>` so its own `ResolveTypeExpr` answers
// for the QualifiedType branch. So this follows a resolution the front end
// already performs; it is not a new language rule.
//
// Corpus fixtures mostly write `import x.{T}` and then `T`. Realistic
// multi-module projects write `import sqlite::sqlite` and then `sqlite.Conn`,
// which is the spelling this file serves.
//
// # A user's FFI handle needs nothing more
//
// A USER's `opaque type RawConn go sqlite.Conn` does not need the pair the
// stdlib co-located adapters have, a handle type in `rt` plus a compiler-side
// host package row; `rt` must not name a user's Go type
// (TestRuntimeImportsOnlyItsAllowlist) and a user cannot extend the
// compiler's list. A user handle reaches every gen by its own route:
// hostpkg.go's `hostTypeDefs` interns one `*typeDef` per (import path, Go type)
// process-wide, with `rtDeclared: true`, so `importNamed`'s `src.rtDeclared`
// arm hands the same def back to a referencing gen with nothing re-derived.
// The only thing this file adds is the spelling that names the struct holding
// it. A user handle gets no `Debug`, no `==` and no hash, because hostTypeDef
// declines all three deliberately (its header says why).
//
// # WHAT IS DELIBERATELY NOT REACHED
//
// A qualified GENERIC instantiation (`sqlite.Pool<Conn>`) declines here and
// keeps refusing under `generic type`, exactly as `Probe.Reading<T>` does one
// function over: the member must be an `*ast.SimpleType`, because a generic
// declaration is refused in its own file too and a mirror inherits that.
//
// A qualified interface (`store.Store` where `Store` is an `interface`) is not
// a type mirror: `foreignOwner`'s rule is that a type lookup never comes back
// holding an interface, since a type mirror's layout and tags mean nothing
// for one. It resolves instead through moduleQualifiedIface, to the interface
// mirror the bare imported name reaches (foreignIface), so `db: sql.Conn` and
// `db: Conn` are one existential.

// moduleQualifiedType resolves `<qualifier>.<Name>` to this gen's mirror of
// the declaration the qualifier's module exports under that name.
//
// Resolution is by DECLARATION NODE, through the analyzer's own scope for the
// qualifier, so an aliased import (`import sqlite::sqlite as db`) and a renamed
// re-export reach the same `*typeDef` — which is the type's identity here, and
// the reason foreign.go's header gives for never keying on a name.
//
// The mirror is registered in `g.types` under the DOTTED spelling it was
// reached by, so every later mention of `sqlite.Conn` in this file reaches one
// def rather than minting a second. That cannot collide with a namespaced
// DECLARATION of the same spelling (`pub enum sqlite.Conn`): namedType checks
// `g.types` before it gets here, so a local declaration wins, which is the
// precedence namedType's own comment states.
func (g *gen) moduleQualifiedType(qualifier, name string) (*typeDef, bool) {
	o := g.moduleQualifiedOwner(qualifier, name)
	if o == nil {
		return nil, false
	}
	d := g.mirrorOf(o)
	if g.visibleBlockModule(qualifier) == nil {
		// Only the file's binding is cached under the spelling: a block's
		// import of the qualifier holds for that block alone.
		g.types[qualifier+"."+name] = d
	}
	return d, true
}

// moduleQualifiedOwner is the registry entry `<qualifier>.<name>` names,
// WITHOUT minting a mirror or registering one.
//
// Split out because moduleQualifiedBare has to ask the same question of every
// qualifier the file binds and mint for AT MOST ONE of them: minting inside
// the scan would register a mirror per module that happens to export the name,
// including on the ambiguous path where the scan declines and no mirror should
// exist at all.
func (g *gen) moduleQualifiedOwner(qualifier, name string) *typeOwner {
	if g.reg == nil || g.fa == nil || g.fileUnit < 0 {
		return nil
	}
	return g.typeOwnerIn(g.moduleScope(qualifier), name)
}

// typeOwnerIn is the registry entry a file's member scope exports as name,
// or nil for an interface or no type.
func (g *gen) typeOwnerIn(scope *analysis.Scope, name string) *typeOwner {
	if scope == nil || g.reg == nil || g.fileUnit < 0 {
		return nil
	}
	o := g.ownerOfSymbol(resolvedScopeSymbol(scope, name))
	if o == nil || o.isIface {
		return nil
	}
	return o
}

// moduleQualifiedIface resolves `<qualifier>.<Name>` to this gen's def for
// the interface the qualifier's module exports under that name, by
// declaration node, as moduleQualifiedType does for a type. It answers for a
// program's own modules and for the std modules lowered with the program
// (programStdModules), which the registry holds; other std interfaces are
// stdProgQualifiedIface's.
func (g *gen) moduleQualifiedIface(t *ast.QualifiedType) (*ifaceDef, bool) {
	qualifier, name, ok := qualifiedTypeParts(t)
	if !ok {
		return nil, false
	}
	return g.moduleQualifiedIfaceNamed(qualifier, name)
}

// qualifiedModuleIface is qualifiedStdIface for a program module's
// interface an impl header names through its qualifier (`impl other.Shape
// for Sq`), read through the qualifier's scope so a file's own interface
// of the same name does not shadow it.
func (g *gen) qualifiedModuleIface(te ast.TypeExpr) (*ifaceDef, bool) {
	qt, isQualified := te.(*ast.QualifiedType)
	if !isQualified {
		return nil, false
	}
	qualifier, name, named := qualifiedTypeParts(qt)
	if !named || ast.IsPublic(qualifier) {
		return nil, false
	}
	return g.moduleQualifiedIfaceNamed(qualifier, name)
}

// moduleQualifiedIfaceOwner reports whether n, the owner of a reference
// `<qualifier>.<Name>.member`, names an interface a program module exports
// through a qualifier this body does not bind as a value.
func (bl *irScalarBuilder) moduleQualifiedIfaceOwner(n ast.Node) bool {
	fa, isFA := n.(*ast.FieldAccess)
	if !isFA || fa.Field == nil {
		return false
	}
	q, isIdent := ast.QualifierIdent(fa.Object)
	if !isIdent {
		return false
	}
	if bl.namesLocal(q) {
		return false
	}
	_, ok := bl.g.moduleQualifiedIfaceNamed(q.Name, fa.Field.Name)
	return ok
}

// ifaceKey is the key the impls of the interface the file whose analyzer
// origin is origin declares as name are filed under in implsByIface and
// instImpls, and the one every lookup of them reads: the interface's
// identity, (declaring file, name). It is the same in every gen, so two
// interfaces of one name (two files' `Show`, a file's own `ToJson` and
// std's) are two keys, and a gen reads another gen's impls under the key
// that gen files them under. Every key comes from here: implsKey for a def
// this gen holds, stdIfaceImplKey for a std interface the builder names,
// implIfaceKey for an impl block's header.
func ifaceKey(origin, name string) string {
	return origin + "." + name
}

// implsKey is ifaceKey for d, this gen's def of an interface: a local
// declaration, a sibling file's mirror, a std module's interface or an
// instance of any of them, which shares its template's key.
func (g *gen) implsKey(d *ifaceDef) string {
	if d == nil {
		return ""
	}
	return ifaceKey(g.ifaceOrigin(d), d.nomi)
}

// ifaceOrigin is the analyzer origin of the file that declares d.
func (g *gen) ifaceOrigin(d *ifaceDef) string {
	switch {
	case d.universal:
		return structIfaceModule
	case d.foreign != "":
		return d.foreign
	}
	if std := g.stdIfaceType(d); !std.IsZero() {
		return std.Module
	}
	if g.fa == nil {
		return analysis.OriginUnresolved
	}
	return g.fa.Origin
}

// stdIfaceImplKey is ifaceKey for std interface iface.
func stdIfaceImplKey(iface analysis.StdType) string {
	return ifaceKey(iface.Module, iface.Name)
}

// implIfaceKey is the key impl block d, whose header is te and names name,
// is filed under (ifaceKey): its interface's def's, or, for an interface
// the builder holds no def of (std's generic `Iter` in a std module), the
// one the checker binds the header to. A derived Debug in a std module
// that imports no `Debug` is std's by construction, as resolveImplDef says.
func (g *gen) implIfaceKey(d *implDef, te ast.TypeExpr, name string) string {
	for _, def := range []*ifaceDef{d.iface, d.stdIface} {
		if def != nil {
			return g.implsKey(def)
		}
	}
	if it := g.headerIface(te); it != nil {
		return ifaceKey(it.Origin, it.Name)
	}
	if d.synth && name == "Debug" {
		return g.implsKey(g.stdDebugDef())
	}
	return ifaceKey(analysis.OriginUnresolved, name)
}

// headerIface is the interface te, an impl header or a call's owner, names
// in this file, or nil: the declaration the checker resolved it to, or for
// one the front end or the builder wrote (a `derive`'s header), whose
// position holds no reference to an interface, the declaration its name
// has in this file's scope, through its qualifier's when it has one.
func (g *gen) headerIface(te ast.TypeExpr) *analysis.InterfaceType {
	if g.fa == nil || te == nil {
		return nil
	}
	scope := g.fa.ModuleScope
	if q, qualified := te.(*ast.QualifiedType); qualified {
		scope = scopeOfRef(g.fa, refAt(g.fa, &ast.Ident{Line: q.ModuleLine, Col: q.ModuleCol}), q.Module)
		te = q.Member
	}
	var name string
	var line, col int
	switch h := te.(type) {
	case *ast.SimpleType:
		name, line, col = h.Name, h.Line, h.Col
	case *ast.GenericType:
		name, line, col = h.Name, h.Line, h.Col
	default:
		return nil
	}
	sym := resolveSymbol(g.fa.References[analysis.Pos{Line: line, Col: col}])
	if sym == nil || sym.Kind != analysis.SymbolInterface {
		sym = resolvedScopeSymbol(scope, name)
	}
	if sym == nil {
		return nil
	}
	it, _ := sym.Type.(*analysis.InterfaceType)
	return it
}

// moduleQualifiedIfaceNamed is moduleQualifiedIface reached from the two
// segments of a call's owner (`storage.Store`, of `storage.Store.exists(s,
// "x")`). The def is registered in g.ifaces under the dotted spelling, so the
// type-qualified route (ifaceNamed) reaches the one mirror the bare imported
// name reaches, defaults and all.
func (g *gen) moduleQualifiedIfaceNamed(qualifier, name string) (*ifaceDef, bool) {
	if g.reg == nil || g.fa == nil || g.fileUnit < 0 {
		return nil, false
	}
	scope := g.moduleScope(qualifier)
	if scope == nil {
		return nil, false
	}
	o := g.ownerOfSymbol(resolvedScopeSymbol(scope, name))
	if o == nil || !o.isIface {
		return nil, false
	}
	d := g.mirrorIface(o)
	if _, taken := g.ifaces[qualifier+"."+name]; !taken && g.visibleBlockModule(qualifier) == nil {
		g.ifaces[qualifier+"."+name] = d
	}
	return d, true
}

// moduleQualifiedBare is the DOTTED spelling for a declaration named BARE,
// found by asking every whole-file import this file binds.
//
// # The dot shorthand records a bare name and the qualifier is not in it
//
// `b: shapes.Shape = .Ring{r: 7}` is resolved by the analyzer, which writes
// `ast.DotVariantType.ResolvedEnum = "Shape"`: the declared name, with no
// module in it. This file's tables bind only the qualifier, so a lookup of
// `Shape` alone misses and the literal would refuse as
// `struct literal | Shape.Ring`.
//
// # AMBIGUITY DECLINES, IT DOES NOT PICK
//
// Two imported modules may each export a `Shape`, and the shorthand says
// nothing about which. Resolving the first one found would be a WRONG ANSWER
// with no diagnostic — a variant tag over another type's slot layout — so a
// second, different declaration makes this answer false and the caller's
// existing refusal stands.
//
// The qualifiers are scanned in sorted order so which mirror is registered
// does not depend on Go's map iteration; the answer is order-independent
// already, because every qualifier is asked before one is chosen.
func (g *gen) moduleQualifiedBare(name string) (string, bool) {
	if g.fa == nil || g.fa.ModuleScope == nil {
		return "", false
	}
	quals := make([]string, 0, len(g.fa.ModuleScope.Symbols))
	for q, sym := range g.fa.ModuleScope.Symbols {
		if sym != nil && sym.Kind == analysis.SymbolModule {
			quals = append(quals, q)
		}
	}
	slices.Sort(quals)
	var owner *typeOwner
	spelling := ""
	for _, q := range quals {
		// The file's bindings, which the cache below is keyed on, whatever a
		// visible block rebinds q to.
		o := g.typeOwnerIn(moduleScopeOf(g.fa, q), name)
		if o == nil {
			continue
		}
		if owner == nil {
			owner, spelling = o, q+"."+name
			continue
		}
		if owner.decl != o.decl {
			return "", false
		}
	}
	if owner == nil {
		return "", false
	}
	g.types[spelling] = g.mirrorOf(owner)
	return spelling, true
}

// moduleQualifiedDotted is moduleQualifiedType reached from a NAME rather than
// from an *ast.QualifiedType — `shapes.Shape`, the owner half of
// `shapes.Shape.Ring`.
//
// # THE SPLIT WAS BY SEGMENT COUNT, AND THAT IS WHAT THIS CLOSES
//
// A module-qualified reference to a DECLARATION already lowered:
// `shapes.Circle{r: 1}` arrives as an *ast.QualifiedType and namespacedType
// answers it. A module-qualified reference to a VARIANT of one did not, and
// every spelling of it failed at the same place — `g.namedType("shapes.Shape")`
// — because the owner is handed around this package as a dotted STRING:
//
//	shapes.Shape.Ring{r: 2}   a variant's brace literal
//	shapes.Shape.Square(4)    variantCall
//	shapes.Bag.Items[1, 2]    a variant's attached literal, through a TypeIdent
//	                          synthesized with the dotted owner as its Name
//	shapes.Colour.Red         dottedTypeQualifier -> namedType
//
// So one arm in namedType serves all four, and the alternative — a
// module-qualified branch at each of those sites — is the resolver-family
// defect this package has recorded six instances of.
//
// SPLIT AT THE LAST DOT, which is exact rather than a search, for
// dottedqual.go's reason one level up: every segment but the last belongs to
// the owner, because Nomi has no nested-module member access and a type
// member's name is one segment. A three-segment spelling never reaches here
// whole — `shapes.Shape.Ring` is split by the caller into owner and member
// before the owner is looked up — so `shapes.Shape` is what arrives.
//
// A miss is a plain decline and the caller's existing refusal stands, which is
// what keeps `non-local type` and `file member` meaning what they meant.
func (g *gen) moduleQualifiedDotted(name string) (*typeDef, bool) {
	dot := strings.LastIndex(name, ".")
	if dot <= 0 || dot == len(name)-1 {
		return nil, false
	}
	return g.moduleQualifiedType(name[:dot], name[dot+1:])
}

// qualifiedStdDef is the def of a std type named through a whole-module
// import's qualifier (`random.Error`, the owner of `random.Error.OsEntropy{...}`),
// identified by the analyzer's symbol in the qualifier's scope
// (qualifiedStdKind), or nil. Not an arm of namedType, whose dotted answers
// are this program's own declarations: dottedTypeQualifier reads a dotted
// name found there as a namespaced owner, and a std module's qualifier is not
// one.
func (g *gen) qualifiedStdDef(name string) *typeDef {
	dot := strings.LastIndex(name, ".")
	if dot <= 0 || dot == len(name)-1 {
		return nil
	}
	k, isStd := g.qualifiedStdKind(&ast.QualifiedType{Module: name[:dot], Member: &ast.SimpleType{Name: name[dot+1:]}})
	if !isStd || k.tag != tagNamed || k.def == nil {
		return nil
	}
	return k.def
}

// qualifiedTypeParts splits an *ast.QualifiedType into a qualifier and a plain
// member name, declining anything else.
//
// One place, because typeOf and typeRefusal must agree about which spellings
// are resolvable at all — the property
// TestSigReason_NamesAReasonForExactlyWhatTypeOfRefuses pins.
func qualifiedTypeParts(t *ast.QualifiedType) (qualifier, name string, ok bool) {
	if t == nil || t.Member == nil {
		return "", "", false
	}
	st, simple := t.Member.(*ast.SimpleType)
	if !simple {
		return "", "", false
	}
	if mod, rest, dotted := strings.Cut(t.Module, "."); dotted && !ast.IsPublic(mod) {
		// A literal's prefix splits at its last segment
		// (`json.Json.ShapeError{...}` has the qualifier `json.Json`):
		// the module is the first, and the rest is the dotted name its
		// scope binds (`Json.ShapeError`).
		return mod, rest + "." + st.Name, true
	}
	return t.Module, st.Name, true
}

// qualifiedStdKind is the kind `random.Error` names after `import std::random`: a
// monomorphic std type reached through a whole-module import's qualifier,
// reporting whether the name was one.
//
// The registry namespacedType consults holds the program's own declarations
// only, and the std families' name lookups (stdEnumNamed, opaqueNamed, ...)
// are anchored on names this file's scope binds, which a whole-module import
// does not. The qualifier's scope does bind it, and the checker resolved the
// symbol there to a type with a std origin, so the answer is that type's
// projection: the same answer an inferred position over it gets.
func (g *gen) qualifiedStdKind(t *ast.QualifiedType) (kind, bool) {
	if gt, isGeneric := t.Member.(*ast.GenericType); isGeneric {
		return g.qualifiedStdGenericKind(t.Module, gt)
	}
	qualifier, name, ok := qualifiedTypeParts(t)
	if !ok {
		return kindInvalid, false
	}
	sym := resolvedScopeSymbol(g.moduleScope(qualifier), name)
	if sym == nil {
		return kindInvalid, false
	}
	var origin string
	switch ty := sym.Type.(type) {
	case *analysis.StructType:
		origin = ty.Origin
	case *analysis.EnumType:
		origin = ty.Origin
	case *analysis.DistinctType:
		origin = ty.Origin
	case *analysis.InterfaceType:
		// `sql.Conn`, `display.Display`: an existential over the std
		// interface, as the bare name is.
		origin = ty.Origin
	case *analysis.PrimitiveType:
		return g.qualifiedStdHostKind(sym)
	default:
		return kindInvalid, false
	}
	if !strings.HasPrefix(origin, "std/") {
		return kindInvalid, false
	}
	k := g.project(sym.Type)
	// kindInvalid: propagates — a std type with no representation is still the
	// type the qualifier names, and typeOf's caller reports it.
	return k, true
}

// qualifiedStdGenericKind is qualifiedStdKind for an instantiation,
// `tasks.Task<Int>` or `channels.Channel<String>`: the qualifier's scope binds
// the generic declaration, whose (Origin, Name) selects the std generic host
// type or generic std struct the arguments instantiate, as an inferred
// position over the same type does (genHostOfType, genStructOfType).
func (g *gen) qualifiedStdGenericKind(qualifier string, gt *ast.GenericType) (kind, bool) {
	sym := resolvedScopeSymbol(g.moduleScope(qualifier), gt.Name)
	if sym == nil {
		return kindInvalid, false
	}
	var origin string
	switch ty := sym.Type.(type) {
	case *analysis.StructType:
		origin = ty.Origin
	case *analysis.DistinctType:
		origin = ty.Origin
	case *analysis.EnumType:
		// `maybe.Maybe<Int>`, `result.Result<Int, String>`: a prelude enum
		// named through its std file's qualifier is the instance the bare
		// name gives (typeOf), found by the declaration it binds.
		return g.qualifiedPreludeKind(sym, gt)
	default:
		return kindInvalid, false
	}
	if !strings.HasPrefix(origin, "std/") {
		return kindInvalid, false
	}
	args := make([]kind, len(gt.Params))
	for i, p := range gt.Params {
		args[i] = g.typeOf(p)
	}
	if k, ok := g.genHostOfType(origin, gt.Name, args); ok {
		return k, true
	}
	if k, ok := g.genStructOfType(origin, gt.Name, args); ok {
		return k, true
	}
	// kindInvalid: propagates — a std generic type this builder has no
	// instance for is still the type the qualifier names, and typeOf's caller
	// reports it.
	return kindInvalid, true
}

// qualifiedPreludeKind is the prelude instance a qualified generic enum names
// (`maybe.Maybe<Int>`): the declaration the qualifier's scope binds selects
// the anchor, and the arguments instantiate it as typeOf's do. A
// generic enum that is no prelude family is not answered here.
func (g *gen) qualifiedPreludeKind(sym *analysis.Symbol, gt *ast.GenericType) (kind, bool) {
	g.loadPreludes()
	decl, isDecl := sym.Node.(*ast.EnumDef)
	if !isDecl {
		return kindInvalid, false
	}
	a, isPrelude := g.preludeByDecl[decl]
	if !isPrelude {
		return kindInvalid, false
	}
	if len(gt.Params) != len(a.spec.params) {
		return kindInvalid, true
	}
	args := make([]kind, len(gt.Params))
	for i, p := range gt.Params {
		args[i] = g.typeOf(p)
		// kindInvalid: propagates — typeOf's caller reports the annotation.
		if args[i] == kindInvalid {
			return kindInvalid, true
		}
	}
	return g.preludeInstance(a, args), true
}

// qualifiedStdHostKind is qualifiedStdKind for a monomorphic std `host type`
// (`supervisors.Supervisor`), whose solved type is a *PrimitiveType: an
// analyzer singleton by pointer, as stdHostAnchors identifies one, and an
// origin row by the type's own (Origin, Name), which a user's own host type of
// the same name does not share.
func (g *gen) qualifiedStdHostKind(sym *analysis.Symbol) (kind, bool) {
	p, _ := sym.Type.(*analysis.PrimitiveType)
	for i := range stdHostSpecs {
		s := &stdHostSpecs[i]
		switch {
		case s.prim != nil:
			if sym.Type == analysis.Type(s.prim) {
				return stdHostKind(i), true
			}
		case s.origin != "":
			if p != nil && p.Origin == s.origin && p.Name_ == s.nomi {
				return stdHostKind(i), true
			}
		}
	}
	return kindInvalid, false
}
