package irbuild

import (
	"strings"
	"sync"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// A STDLIB interface, as an existential this builder can represent: a method
// shape plus a dispatch table living in rt. Only `Display`, `Comparable` and
// `Debug` have a row here; a program holding any other std interface gets a
// def built from std's declaration (stdprogiface.go).
//
// # Why a stdlib interface needs this file
//
// `collectStdCandidates` registers stdlib FUNCTIONS; a stdlib interface
// DECLARATION never reaches `g.ifaces`, so without this file (or
// stdprogiface.go) a stdlib interface has no `*ifaceDef` in a gen and
// nothing for an existential kind to point at. There are two doors and both are wired below: reached by ANNOTATION it is
// `typeOf`'s interface arm, reached by INFERENCE it is `project`'s
// `*analysis.InterfaceType` arm. The INFERRED door carries most of the
// population (a `from_fragments: Display` reached through inference).
//
// The interface-QUALIFIED CALL does not need this file. `Display.to_string`,
// `Comparable.compare`, `Equatable.equal`, `Hashable.hash` and `Debug.inspect`
// lower at every receiver whose stdlib impl is lowered, and at a user struct
// with a hand-written impl, through the stdlib FUNCTION index. So the wiring
// adds exactly one arm to the qualified call, for the ERASED receiver, and leaves every
// other route untouched.
//
// `typeRegistry`, which the cross-file mirrors read, is filled by
// `buildTypeRegistry` from the USER's modules. A stdlib
// `*ast.InterfaceDef` is not a key there, so the cross-file mechanism (mirrors,
// private and cycle refusals, one table named from wherever) does not reach a
// stdlib owner; this file does.
//
// # IDENTITY, which is the whole risk and is NOT solved by a name
//
// A table keyed on the NAME `Display` would let a user's interface adopt rt's
// dispatch slots: a wrong ANSWER, not a coverage gap. `checkReservedTypeName`
// (analysis/builder.go) rejects `pub interface Display`, `Struct` and `Debug`
// in a user file ("type name 'Display' is reserved by the language and cannot
// be redeclared as an interface"), while `App`, `Assertable` and `ToJson`
// analyze clean. The line is the 13 PRELUDE-INJECTED interface names against
// the 8 that must be imported.
//
// Every row in this file's table is on the reserved side, so the wrong-answer
// hazard for them is closed by the front end as well as here. The identity rule
// below holds either way: the reservation is a checker rule that could be
// relaxed, and the 8 importable stdlib interfaces are shadowable. See
// structiface.go, which relies on the reservation as its premise and asserts
// it with a paired negative.
//
// `analysis.InterfaceType` carries an `Origin`, so the `(Origin, Name)` rule
// opaque.go and stdenum.go anchor on is available; this file does not need it.
// The declaring module is recoverable off the analyzer's own resolution
// chain: `fa.ModuleScope.Lookup(name)` yields an IMPORT symbol whose `Node` is
// an `*ast.ImportStmt`, and its `ModulePath` is the ORIGIN module even for a
// prelude drill-through re-export: `Display` resolves through
// `std/display`, `Debug` `std/debug`, `Comparable` `std/comparable`, NOT
// through `std/prelude`. `sym.Resolved` then reaches the `*ast.InterfaceDef`.
//
// That gives the same two halves the other families use, through a different
// door:
//
//   - a LOCALLY declared interface has `Resolved == nil` and no import
//     statement, so it can never anchor;
//   - a SIBLING's or a dependency's interface resolves through a module path
//     that is not the spec's, so it can never anchor either;
//   - and `std` is unforgeable as a module name — `analysis/manifest.go`'s
//     `LoadManifest` rejects `[package].name = "std"`.
//
// The anchor is therefore `(declaring module, name, validated declaration
// shape)`, and the resulting `*ifaceDef` POINTER is the identity every
// downstream comparison already uses — `implKeyFor`, `implsByIface`. Nothing
// in the path compares a name.
// TestStdIface_ShadowingDeclarationIsNotTheSharedDef and
// testdata/std_iface_shadow.nomi pin the negative half, which is the half a
// name-keyed lookup gets wrong.
//
// # Why the def is PROCESS-WIDE, and why that is safe here
//
// opaque.go's and stdenum.go's reason, unchanged: a shared def has to be
// shared, not merely may be, because kinds are compared by POINTER across gens.
// It is safe because these defs reach no program's declarations: every
// parameter and result kind is a scalar or a process-wide rt type.
// TestStdIfaceDefsArePackageNeutral is the guard on that precondition rather
// than a comment claiming it.

// stdIfaceSpec is one stdlib interface: what this builder believes std
// declares, and where its dispatch tables live.
type stdIfaceSpec struct {
	// module is the DECLARING module's import path, the identity half the
	// analyzer's missing Origin would otherwise supply. See the file comment.
	module string
	// nomi is the interface's Nomi name, the other half.
	nomi string
	// methods are every function the declaration must declare, IN ORDER. A
	// declaration with more, fewer, or differently-shaped methods produces no
	// anchor at all, so a std edit refuses loudly rather than lowering against
	// a shape nobody wrote.
	methods []stdIfaceMethodSpec
}

// stdIfaceMethodSpec is one function of a std interface.
type stdIfaceMethodSpec struct {
	name   string
	params []stdIfaceParamSpec
	// result is the declared return type as std writes it (typeText); ""
	// means the declaration must have none. A self-typed return is
	// deliberately not expressible: it erases to `any` in the table and only a
	// dictionary-driven call can re-box it, which is dict.go's surface and not
	// this one.
	result string
	// resultKind is the kind the shared def's method returns.
	// TestStdSpecSpellingsAreTheirKinds holds it to the type std writes.
	resultKind func() kind
}

// stdIfaceParamSpec is one declared parameter.
type stdIfaceParamSpec struct {
	name string
	// self marks the receiver position and every other self-typed slot. A self
	// position has no kind until an implementing type supplies one, exactly as
	// resolveIface records for a declared interface.
	self bool
	// nomi is a non-self parameter's type as std writes it (typeText), and
	// kind the kind the shared def's method takes there.
	nomi string
	kind func() kind
}

// stdIfaceSpecs is the whole set, and it is short on purpose: a row is a
// dispatch table in the runtime library every runner links, so adding one is
// a decision rather than a mechanical extension — the bar opaqueSpecs and
// preludeSpecs set.
//
// Every row must be REACHED by something rather than merely be representable.
// TestStdIfaceSpecsAreReachable asserts it over the corpus, which is the guard
// that caught stdenum.go's wrong unreachability claim about `Direction`.
//
// Its second job is to make the mechanism plural. A one-row table proves
// nothing about generality: with `Display` alone, an implementation hardcoded
// to "one receiver, a string back" passes every test that can be written.
// `Comparable` has TWO self-typed positions and a NON-SCALAR result, so the
// self-position list and the result kind both have to be read off the spec.
//
// NOT listed, each for a reason this mechanism cannot paper over:
// `Add`/`Subtract`/`Multiply`/`Divide`/`Iter`/`Literal`/`Steppable` are
// GENERIC; `App` declares a FIELD and no functions; `Struct`'s only method is a
// `host fn`; `Discrete`'s `next` answers `Option<self>`, a self nested in
// the result, as `FromJson` nests `self` INSIDE its return type. Each keeps
// refusing under `stdlib interface`. A Nomi-bodied default is no reason to
// leave a row out: it takes no slot (matches) and is lowered in its
// declaring module per receiver (stdifacedefault.go).
//
// The `FromJson` obstacle is the NESTED self, not self appearing only in the
// return. A method with ZERO self-typed parameters and a BARE `self` return is
// fully served: the
// DICTIONARY carries the TypeID as its own `*rt.TypeID` parameter, so it needs
// no self-typed argument to dispatch on, and it re-boxes the `any` result under
// exactly the TypeID that selected the implementation. With a USER interface,
// `fn make(n: Int) -> self` lowers, while `Result<self, String>`, `Option<self>`
// and `List<self>` refuse; `from_json(json: Json) -> Result<self, ShapeError>` is
// the second group. impl.go's `gapNestedSelfResult` states the mechanism and is
// the row that fires.
//
// `Debug` is a row even though it is universally auto-synthesized and its call
// surface lowers without one, because a row does not, by itself, change any
// route. The qualified call's chain reaches the erased arm only AFTER every
// arm that names a function statically has declined, so a CONCRETE receiver takes the
// direct call and the table is consulted only at positions that would
// otherwise refuse: `fn f(v: Named) { Debug.inspect(v) }` over an erased
// value, and `fn f<T>(v: T) where T: Debug` at the dictionary seam
// (`boundIfaces`).
//
// If an arm ABOVE the erased one in that chain ever routes a
// statically-known receiver through the erased arm's table, this row stops being
// additive. Nothing asserts that a concrete receiver lowers to a direct call:
// the rendered output is identical either way, so no differential fixture can
// see it.
var stdIfaceSpecs = []stdIfaceSpec{{
	module: "std/display",
	nomi:   "Display",
	methods: []stdIfaceMethodSpec{{
		name:       "to_string",
		params:     []stdIfaceParamSpec{{name: "value", self: true}},
		result:     "String",
		resultKind: func() kind { return kindString },
	}},
}, {
	module: "std/comparable",
	nomi:   "Comparable",
	methods: []stdIfaceMethodSpec{{
		name: "compare",
		params: []stdIfaceParamSpec{
			{name: "a", self: true},
			{name: "b", self: true},
		},
		result:     "Ordering",
		resultKind: func() kind { return stdEnumKind(stdEnumOrdering) },
	}},
}, {
	module: "std/debug",
	nomi:   "Debug",
	methods: []stdIfaceMethodSpec{{
		name:       "inspect",
		params:     []stdIfaceParamSpec{{name: "value", self: true}},
		result:     "String",
		resultKind: func() kind { return kindString },
	}},
}}

// stdIfaceDefs is the process-wide *ifaceDef per spec, keyed by spec index.
//
// Each carries a CANONICAL `*ast.InterfaceDef` that no file contains and
// nothing ever lowers. It exists because a declaration node is what an interface
// identity IS everywhere else in this package — `implKeyFor` keys on `d.decl`,
// `buildImplIndex` skips a def without one, and `inheritDefaults` reads
// `m.decl.Body`. Synthesizing one rather than borrowing std's own node is what
// keeps the def process-wide: `std.Load()` is not memoized, so std's pointers
// differ between loads and a def holding one would be stale the moment a second
// program was analyzed in the same process.
//
// The canonical declaration is also what makes the shape check meaningful:
// `matches` asks whether std's declaration AGREES with this one, rather than
// asking a list of ad-hoc questions.
var stdIfaceDefs = sync.OnceValue(func() []*ifaceDef {
	defs := make([]*ifaceDef, len(stdIfaceSpecs))
	for i := range stdIfaceSpecs {
		s := &stdIfaceSpecs[i]
		canon := &ast.InterfaceDef{Name: s.nomi, Public: true}
		d := &ifaceDef{
			nomi:      s.nomi,
			decl:      canon,
			methods:   map[string]*ifaceMethod{},
			lowerable: true,
			// The one place this is set. See ifaceDef.stdShared: it is
			// packageNeutral's O(1) form of the question stdIfaceOf answers by
			// scanning, and that predicate must not depend on this table.
			stdShared: true,
			// A local declaration's unit is -1 and so is this: the def belongs
			// to no unit of the program, which is what the empty `foreign`
			// also says.
			unit: -1,
		}
		for j := range s.methods {
			ms := &s.methods[j]
			im := ast.InterfaceMethod{Name: ms.name}
			m := &ifaceMethod{name: ms.name, result: kindUnit, table: true}
			for _, p := range ms.params {
				// The canonical parameter carries a REAL `self` annotation, and
				// it has to. Without it any AST-derived rule would read self@none
				// off it while the spec row beside it says otherwise; a single
				// derivation of self positions makes the annotation load-bearing.
				var ann ast.TypeExpr
				if p.self {
					ann = &ast.SelfType{}
				}
				im.Params = append(im.Params, ast.Param{Name: p.name, TypeAnnotation: ann})
				if p.self {
					m.params = append(m.params, kindInvalid)
					continue
				}
				m.params = append(m.params, p.kind())
			}
			// The SAME derivation resolveIface uses, over the same shape of
			// input. A spec row is not a declaration, but the self-position
			// model must not be a second encoding of one — that is the drift
			// two of this package's shipped defects came out of. See selfpos.go.
			m.shape = selfShapeOf(im.Params, im.ReturnTypeExpr)
			if ms.result != "" {
				m.result = ms.resultKind()
			}
			canon.Methods = append(canon.Methods, im)
			d.methods[ms.name] = m
			d.order = append(d.order, m)
		}
		for j := range d.order {
			d.order[j].decl = &canon.Methods[j]
			// The SAME function resolveIface routes a declared interface's
			// methods through. A spec row is not a declaration, so it never
			// passes through methodDispatchGap — and two encodings of "which
			// shapes cannot be dispatched on an erased receiver" is exactly
			// the drift that would let `Comparable.compare` lower here and
			// refuse there. See impl.go.
			d.order[j].why = erasedReceiverGap(d.order[j])
		}
		defs[i] = d
	}
	return defs
})

// stdIfaceKind is the existential kind of the i'th spec's values.
func stdIfaceKind(i int) kind { return existential(stdIfaceDefs()[i]) }

// stdIfaceOf reports whether d is one of the shared defs, and which spec.
//
// Pointer identity, not a name: it is the same question stdEnumDef answers for
// a shared enum def, and a name is not an identity.
func stdIfaceOf(d *ifaceDef) (int, bool) {
	if d == nil {
		return -1, false
	}
	for i, sd := range stdIfaceDefs() {
		if sd == d {
			return i, true
		}
	}
	return -1, false
}

// stdIfaceType is the std interface d stands for: a std module's interface
// a program holds values as (ifaceDef.stdOrigin), or one with an rt table
// (stdIfaceOf). Zero for a program's interface.
func (g *gen) stdIfaceType(d *ifaceDef) analysis.StdType {
	if d.stdOrigin != "" {
		return analysis.StdType{Module: d.stdOrigin, Name: d.decl.Name}
	}
	if i, shared := stdIfaceOf(d); shared {
		return analysis.StdType{Module: stdIfaceSpecs[i].module, Name: stdIfaceSpecs[i].nomi}
	}
	return analysis.StdType{}
}

// --- anchoring -------------------------------------------------------------

// stdIfaceAnchors resolves the specs against one module's analysis: the
// declaration NODE each spec was reached through, with its spec index.
//
// A free function rather than a gen method, on opaqueAnchors' and
// stdEnumAnchors' footing: one implementation of "is this the std interface",
// so there is nothing for a second to drift from.
//
// A module analyzed without an analysis library gets no anchors and every
// mention refuses.
//
// The shape check reads std's declaration, which is the same node whichever
// module reached it, and compares its written types by spelling, so an
// anchor does not depend on which other names the anchoring module imports.
func stdIfaceAnchors(fa *analysis.FileAnalysis) map[*ast.InterfaceDef]int {
	byDecl := map[*ast.InterfaceDef]int{}
	if fa == nil || fa.ModuleScope == nil {
		return byDecl
	}
	for i := range stdIfaceSpecs {
		s := &stdIfaceSpecs[i]
		sym := fa.ModuleScope.Lookup(s.nomi)
		if sym == nil {
			continue
		}
		if mod, imported := stdImportModuleOf(sym, stdImporterOf(fa, sym)); !imported || mod != s.module {
			// Not reached through an import of the DECLARING std module: a
			// local declaration, a sibling's, or a dependency's. No anchor,
			// and the ordinary paths report. This is the identity check and it
			// is the whole of it — see the file comment.
			continue
		}
		res := sym
		for res.Resolved != nil {
			res = res.Resolved
		}
		decl, isDecl := res.Node.(*ast.InterfaceDef)
		if !isDecl || !s.matches(decl) {
			continue
		}
		byDecl[decl] = i
	}
	return byDecl
}

// stdIfaceOfDecl is the shared def of the std interface decl, which std
// module origin declares, when a spec describes it, or nil: stdIfaceAnchors'
// identity check for a declaration, whatever scope reached it.
func (g *gen) stdIfaceOfDecl(origin string, decl *ast.InterfaceDef) *ifaceDef {
	for i := range stdIfaceSpecs {
		s := &stdIfaceSpecs[i]
		if s.module == origin && s.nomi == decl.Name && s.matches(decl) {
			return stdIfaceDefs()[i]
		}
	}
	return nil
}

// stdImportModuleOf is the module path an IMPORTED symbol was reached through,
// and false when the symbol is not an import at all.
//
// The analyzer records the ORIGIN module on a re-exported name rather than the
// re-exporting one: the prelude auto-prepend brings `Display` in
// through `std/display`, not through `std/prelude` — which is what makes this
// the declaring module and not merely the nearest one.
//
// `importerKey` is the module key of the file that wrote the import, and it is
// required rather than convenient. The stdlib is ONE Nomi module, so a stdlib
// file names a sibling BARE — `import display.Display` inside std/decimal.nomi
// — while every spec here identifies its module as `std/display`. Qualifying
// by the importer is what tells that apart from a USER file importing its own
// `display.nomi`, which must anchor nothing.
func stdImportModuleOf(sym *analysis.Symbol, importerKey string) (string, bool) {
	imp, isImport := sym.Node.(*ast.ImportStmt)
	if !isImport || sym.Resolved == nil || len(imp.ModulePath) == 0 {
		return "", false
	}
	segs := make([]string, len(imp.ModulePath))
	for i, seg := range imp.ModulePath {
		switch t := seg.(type) {
		case *ast.Ident:
			segs[i] = t.Name
		case *ast.TypeIdent:
			segs[i] = t.Name
		default:
			return "", false
		}
	}
	return strings.Join(analysis.QualifyIntraStdlibImport(importerKey, segs), "/"), true
}

// stdImporterOf is the module key of the file whose import statement bound
// sym, as fa's file sees sym: fa's own key for a name the file binds itself,
// and std/prelude's for a name the file INHERITS from a parent scope.
//
// The second case exists because the prelude reaches a user file two ways.
// With the stdlib on disk the analyzer prepends a copy of the prelude's
// re-exports to the file (`import std::context.{Context}`), and the name is the
// file's own. Without it — an installed binary with no source tree, the tour's
// wasm in a browser — the prelude's module scope is the file's PARENT, and the
// name is the prelude's own import statement, which spells its sibling bare
// (`pub import context.Context`). Qualifying that bare path by the USER
// file's key reads `context` rather than `std/context`, and every anchor keyed
// on a std module then misses, so a boot result holding a `Context` would not
// lower and the VM would refuse every program with a boot.
func stdImporterOf(fa *analysis.FileAnalysis, sym *analysis.Symbol) string {
	if fa == nil {
		return ""
	}
	if fa.ModuleScope != nil && sym != nil && fa.ModuleScope.Symbols[sym.Name] != sym {
		if _, inherited := sym.Node.(*ast.ImportStmt); inherited {
			return "std/prelude"
		}
	}
	return fa.Origin
}

// matches reports whether decl is the declaration this spec describes.
//
// Everything checked here decides REPRESENTATION, and the check is an AGREEMENT
// between std's declaration and the CANONICAL one the shared def was built
// from — so a std edit that adds a required or host-backed method, reorders
// parameters, moves the self position, changes a result type, gives a row's
// method a body or makes it host-backed produces NO anchor, and every
// mention then refuses under
// `stdlib interface` instead of lowering against a shared def whose methods
// no longer describe it.
//
// That is the one failure this file's identity machinery cannot otherwise
// detect: a shape drift is a wrong answer, not a refusal.
//
// An attached `//!` test is deliberately NOT a disqualifier — see
// stdStructSpec.mismatch.
func (s *stdIfaceSpec) matches(decl *ast.InterfaceDef) bool {
	switch {
	case decl.Name != s.nomi, !decl.Public:
		return false
	case len(decl.TypeParams) > 0, len(decl.WhereClauses) > 0:
		// A generic interface needs the dictionary a generic type needs, which
		// is the refusal declareIfaces already records for a declared one.
		return false
	}
	// A Nomi-bodied default is no row of the table: it is lowered in the
	// declaring module's own scope, once per implementor, and reached as a
	// function of that implementor (stdifacedefault.go). So the functions
	// the spec describes are the declaration's others, in order; a default
	// takes no table slot and leaves the anchor as it was.
	var required []*ast.InterfaceMethod
	for i := range decl.Methods {
		im := &decl.Methods[i]
		if stdIfaceNomiDefault(im) {
			continue
		}
		required = append(required, im)
	}
	if len(required) != len(s.methods) {
		return false
	}
	for i := range s.methods {
		if !s.methods[i].matches(required[i]) {
			return false
		}
	}
	return true
}

// stdIfaceOverride reports whether name is an `open` Nomi-bodied default
// of the std interface whose shared def d is, as this module's anchor
// reached its declaration: an impl writing it overrides the default, which
// is no slot of d's table (stdifacedefault.go), so the function is the
// impl's own and every route that finds a written impl function finds it.
func (g *gen) stdIfaceOverride(d *ifaceDef, name string) bool {
	i, shared := stdIfaceOf(d)
	if !shared {
		return false
	}
	g.loadStdIfaces()
	decl := g.stdIfaceDecls[i]
	if decl == nil {
		return false
	}
	for j := range decl.Methods {
		if im := &decl.Methods[j]; im.Name == name {
			return im.Open && stdIfaceNomiDefault(im)
		}
	}
	return false
}

// stdIfaceNomiDefault reports an interface function with a Nomi body, open
// or final: one lowered in its declaring module rather than dispatched
// through a table.
func stdIfaceNomiDefault(im *ast.InterfaceMethod) bool {
	return im.Body != nil && !im.Extern
}

func (ms *stdIfaceMethodSpec) matches(im *ast.InterfaceMethod) bool {
	switch {
	case im.Name != ms.name:
		return false
	case im.Open, im.Extern, im.Body != nil:
		// A row's function is one every impl writes, which the table
		// dispatches. A HOST-backed default has no Nomi body to lower in
		// the declaring module, and no impl writes it, so no table slot can
		// hold it: no anchor. A Nomi-bodied default never reaches here; it
		// is no row (stdIfaceSpec.matches).
		return false
	case len(im.TypeParams) > 0, len(im.WhereClauses) > 0:
		return false
	case len(im.Params) != len(ms.params):
		return false
	}
	for j := range ms.params {
		p := &ms.params[j]
		got := &im.Params[j]
		if got.Destructure != nil || got.Default != nil {
			return false
		}
		if p.self {
			if _, isSelf := got.TypeAnnotation.(*ast.SelfType); !isSelf {
				return false
			}
			continue
		}
		if typeText(got.TypeAnnotation) != p.nomi {
			return false
		}
	}
	if ms.result == "" {
		return im.ReturnTypeExpr == nil
	}
	if _, selfReturn := im.ReturnTypeExpr.(*ast.SelfType); selfReturn {
		return false
	}
	return typeText(im.ReturnTypeExpr) == ms.result
}

// loadStdIfaces resolves the specs against this gen's module, once.
//
// THE DECLARATION INDEX IS DISCARDED, deliberately. The four sibling
// families each keep a `xByDecl` so `buildTypes` can ask "is the declaration
// in front of me the standard type" and adopt the process-wide def instead of
// minting a per-gen shell. That question cannot be asked here: `stdIfaceAnchors`
// requires the name to resolve through an IMPORT of the declaring std module
// (`stdImportModuleOf`), and a module's OWN declaration resolves to the
// `*ast.InterfaceDef` directly, so a declaration index built from this anchor
// can only ever hold a node some other module carries.
//
// Over every stdlib module, none of this family's anchor entries is a
// declaration the anchoring module carries, so such an index would have no
// reader it could serve.
//
// WHAT THAT LEAVES UNGUARDED: `std/display`, `std/debug` and `std/comparable`
// each DECLARE the interface a spec row describes, and `declareIfaces` mints
// an ordinary per-gen `*ifaceDef` for it with `stdShared` unset, which an
// impl block of the module resolves to (solvedIface), while a type the
// checker solved as it projects to the shared def (stdSharedIface). None of
// the three names its own interface in a lowered signature, so nothing
// reaches the difference.
func (g *gen) loadStdIfaces() {
	if g.stdIfacesLoaded {
		return
	}
	g.stdIfacesLoaded = true
	byDecl := stdIfaceAnchors(g.fa)
	g.stdIfaceDecls = make(map[int]*ast.InterfaceDef, len(byDecl))
	for decl, i := range byDecl {
		g.stdIfaceDecls[i] = decl
	}
}

// projectStdIface is inferred.go's projection for a stdlib interface: the
// existential the checker solved for an unannotated position.
//
// This arm carries more of the population than the annotated one: most
// `Display`-in-a-type-role sites are `from_fragments: Display` reached through
// inference.
//
// Identity comes from the module's own anchor and not from the solved type's
// `Origin`. That is sound
// exactly here, and the reason is worth stating rather than assuming: if the
// anchor exists then this module resolves `Display` through an import of
// `std/display`, so there is no second `Display` the solved type could have
// been. Had the module ALSO seen a local or sibling `Display`, the scope lookup
// would have resolved to that one — project wins — and there would be no anchor
// at all.
func (g *gen) projectStdIface(ty *analysis.InterfaceType) (kind, bool) {
	if len(ty.TypeArgs) > 0 || len(ty.TypeParams) > 0 {
		return kindInvalid, false
	}
	d := g.stdSharedIface(analysis.StdType{Module: ty.Origin, Name: ty.Name})
	if d == nil {
		return kindInvalid, false
	}
	return existential(d), true
}

// projectSharedStdIface is projectStdIface with no gen (projectShared): the
// existential of the row whose (module, name) is the solved type's
// (Origin, Name), when std declares it in the row's shape. The Origin is the
// identity, as projectStdOpaque's is, so no module's scope is asked and a
// program's own `Display` (another Origin) never answers.
func projectSharedStdIface(ty *analysis.InterfaceType) kind {
	if len(ty.TypeArgs) > 0 || len(ty.TypeParams) > 0 {
		return kindInvalid
	}
	if d := stdSpecIface(analysis.StdType{Module: ty.Origin, Name: ty.Name}); d != nil {
		return existential(d)
	}
	return kindInvalid
}

// stdSpecIface is the shared def of the stdIfaceSpecs row whose (module,
// name) is id, when std declares it in the row's shape, or nil.
func stdSpecIface(id analysis.StdType) *ifaceDef {
	declared := stdIfaceDeclaredInStd()
	for i := range stdIfaceSpecs {
		if stdIfaceSpecs[i].module == id.Module && stdIfaceSpecs[i].nomi == id.Name && declared[i] {
			return stdIfaceDefs()[i]
		}
	}
	return nil
}

// stdSharedIface is the process-wide def of std interface id, found by its
// identity: a stdIfaceSpecs row's (stdSpecIface), or the universal
// structural marker's when std declares `Struct` in its shape
// (structIfaceMatches). nil for any other interface, and for the zero id.
func (g *gen) stdSharedIface(id analysis.StdType) *ifaceDef {
	if id.IsZero() {
		return nil
	}
	if id.Module == structIfaceModule && id.Name == structIfaceName {
		if decl := stdInterfaceDecl(g.fa, id); decl != nil && structIfaceMatches(decl) {
			return structIfaceDef()
		}
		return nil
	}
	return stdSpecIface(id)
}

// stdIfaceDeclaredInStd is, per spec, whether the spec's declaring std module
// declares it in the spec's shape, asked of std's own analysis once per std
// snapshot, as opaqueDeclaredInStd asks it for opaque rows.
var stdIfaceDeclaredInStd = perStd(func() []bool {
	lib := stdAnchorLib()
	ok := make([]bool, len(stdIfaceSpecs))
	for i := range stdIfaceSpecs {
		s := &stdIfaceSpecs[i]
		fa := lib.Files[strings.TrimPrefix(s.module, "std/")]
		if fa == nil || fa.ModuleScope == nil {
			continue
		}
		sym := resolveSymbol(fa.ModuleScope.Lookup(s.nomi))
		if sym == nil {
			continue
		}
		it, isIface := sym.Type.(*analysis.InterfaceType)
		decl, isDecl := sym.Node.(*ast.InterfaceDef)
		ok[i] = isIface && it.Origin == s.module && isDecl && s.matches(decl)
	}
	return ok
})

// --- the erased-receiver call ------------------------------------------------
