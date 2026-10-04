package irbuild

import (
	"reflect"
	"strings"
	"sync"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"

	"github.com/nomi-language/nomi/rt"
)

// A STDLIB interface, as an existential this builder can represent: a method
// shape plus a dispatch table living in rt.
//
// # Why a stdlib interface needs this file
//
// `collectStdCandidates` registers stdlib FUNCTIONS; a stdlib interface
// DECLARATION never reaches `g.ifaces`, so without this file a stdlib interface
// has no `*ifaceDef` in any gen and nothing for an existential kind to point
// at. There are two doors and both are wired below: reached by ANNOTATION it is
// `typeOf`'s interface arm, reached by INFERENCE it is `project`'s
// `*analysis.InterfaceType` arm. The INFERRED door carries most of the
// population (a `from_fragments: Display` reached through inference).
//
// The interface-QUALIFIED CALL does not need this file. `Display.to_string`,
// `Comparable.compare`, `Equatable.equal?`, `Hashable.hash` and `Debug.inspect`
// lower at every receiver whose stdlib impl is lowered, and at a user struct
// with a hand-written impl, through the stdlib FUNCTION index. So the wiring
// adds exactly one arm to `implCall`, for the ERASED receiver, and leaves every
// other route untouched.
//
// `foreignIfaceOwner` looks a name up in `typeRegistry`, which
// `buildTypeRegistry` fills from the USER's modules. A stdlib
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
// downstream comparison already uses — `implKeyFor`, `tableCall`'s
// `recv.k.iface != d`, `implsByIface`. Nothing in the path compares a name.
// TestStdIface_TwoSameNamedInterfacesDoNotCollapse and
// testdata/std_iface_shadow.nomi pin the negative half, which is the half a
// name-keyed lookup gets wrong.
//
// # Why the def is PROCESS-WIDE, and why that is safe here
//
// opaque.go's and stdenum.go's reason, unchanged: a shared def has to be
// shared, not merely may be, because kinds are compared by POINTER across gens.
// It is SAFE because nothing about these defs is package-relative — every
// parameter and result kind is a scalar or a process-wide rt type, and the
// table is a variable in rt rather than in some package's output.
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

// stdIfaceMethodSpec is one function, and the rt table that dispatches it.
type stdIfaceMethodSpec struct {
	name   string
	params []stdIfaceParamSpec
	// result is the Nomi name of the declared return type; "" means the
	// declaration must have none. A self-typed return is deliberately not
	// expressible: it erases to `any` in the table and only a dictionary-driven
	// call can re-box it, which is dict.go's surface and not this one.
	result string
	// resultKind is the kind that name must resolve to in the anchoring
	// module, checked through stdTypeKind — the same function a stdlib
	// SIGNATURE resolves through, so there is one answer to "is this the std
	// type" rather than two.
	resultKind func() kind
	// rtVar is the dispatch table itself. Held as the VARIABLE rather than as
	// its spelling for the reason opaqueSpec.goType is held as a type: a
	// rename or a deletion in rt is then a Go compile error in this file, and
	// the emitted spelling is DERIVED, so nobody types the pairing and it
	// cannot disagree with itself.
	rtVar any
}

// stdIfaceParamSpec is one declared parameter.
type stdIfaceParamSpec struct {
	name string
	// self marks the receiver position and every other self-typed slot. A self
	// position has no kind until an implementing type supplies one, exactly as
	// resolveIface records for a declared interface.
	self bool
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
// `host fn`; `Discrete` and `Iter` carry Nomi-bodied defaults whose bodies would
// have to be monomorphized in a module that cannot see the declaring file's
// scope; `FromJson` nests `self` INSIDE its return type. Each keeps refusing
// under `stdlib interface`.
//
// The `FromJson` obstacle is the NESTED self, not self appearing only in the
// return. A method with ZERO self-typed parameters and a BARE `self` return is
// fully served: the
// DICTIONARY carries the TypeID as its own `*rt.TypeID` parameter, so it needs
// no self-typed argument to dispatch on, and it re-boxes the `any` result under
// exactly the TypeID that selected the implementation. With a USER interface,
// `fn make(n: Int): self` lowers, while `Result<self, String>`, `Maybe<self>`
// and `List<self>` refuse; `from_json(json: Json): Result<self, ShapeError>` is
// the second group. impl.go's `gapNestedSelfResult` states the mechanism and is
// the row that fires. See ifaceturbofish_selfreturn_test.go, which pins both
// halves.
//
// `Debug` is a row even though it is universally auto-synthesized and its call
// surface lowers without one, because a row does not, by itself, change any
// route. `implCall`'s chain reaches `stdIfaceCall` only AFTER every arm that
// names a function statically has declined, so a CONCRETE receiver takes the
// direct call and the table is consulted only at positions that would
// otherwise refuse: `fn f(v: Named) { Debug.inspect(v) }` over an erased
// value, and `fn f<T>(v: T) where T: Debug` at the dictionary seam in
// `dictBoundIface`.
//
// If an arm ABOVE `stdIfaceCall` in `implCall`'s chain ever routes a
// statically-known receiver through `rt.MDebugInspect`, this row stops being
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
		rtVar:      rt.MDisplayToString,
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
		rtVar:      rt.MComparableCompare,
	}},
}, {
	module: "std/debug",
	nomi:   "Debug",
	methods: []stdIfaceMethodSpec{{
		name:       "inspect",
		params:     []stdIfaceParamSpec{{name: "value", self: true}},
		result:     "String",
		resultKind: func() kind { return kindString },
		rtVar:      rt.MDebugInspect,
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
			// to no generated module, which is what the empty `foreign` also
			// says and what keeps useIfacePkg from registering an import for a
			// table that lives in rt.
			unit: -1,
		}
		for j := range s.methods {
			ms := &s.methods[j]
			im := ast.InterfaceMethod{Name: ms.name}
			m := &ifaceMethod{name: ms.name, result: kindUnit, table: rtMethodName(ms.rtVar) != ""}
			for _, p := range ms.params {
				// The canonical parameter carries a REAL `self` annotation, and
				// it has to. Without it any AST-derived rule would read self@none
				// off it while the spec row beside it says otherwise; a single
				// derivation of self positions makes the annotation load-bearing.
				// TestStdIface_CanonicalASTAgreesWithTheSpecRow is the guard.
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

// rtMethodName is the emitted spelling of an rt dispatch table, derived from
// the variable rather than typed out beside it.
//
// Guarded: anything that is not a `*rt.Method` declared in exactly the rt package
// answers "", which makes the spec row produce a table-less method — refused at
// every dispatch site — rather than emitting an identifier nothing declares.
func rtMethodName(v any) string {
	t := reflect.TypeOf(v)
	if t == nil || t.Kind() != reflect.Pointer {
		return ""
	}
	e := t.Elem()
	if e.PkgPath() != rtModulePath || !strings.HasPrefix(e.Name(), "Method[") {
		return ""
	}
	name := rtTableVarName(v)
	if name == "" {
		return ""
	}
	return "rt." + name
}

// rtTableVarName recovers WHICH rt table a value is, by identity against the
// declared set.
//
// A reflect value cannot name the variable it was read out of, so the pairing
// has to exist somewhere; one switch, in the same file as the specs that use
// it, is the smallest place it can be. TestStdIfaceTableNamesResolve asserts
// every spec resolves to a non-empty name, so a row added without a pairing
// fails there rather than silently emitting a table-less method.
func rtTableVarName(v any) string {
	switch v {
	case any(rt.MDisplayToString):
		return "MDisplayToString"
	case any(rt.MComparableCompare):
		return "MComparableCompare"
	case any(rt.MDebugInspect):
		return "MDebugInspect"
	}
	return ""
}

// --- anchoring -------------------------------------------------------------

// stdIfaceAnchors resolves the specs against one module's analysis: the
// declaration NODE each spec was reached through, and the spec index under the
// Nomi name.
//
// A free function rather than a gen method, on opaqueAnchors' and
// stdEnumAnchors' footing: one implementation of "is this the std interface",
// so there is nothing for a second to drift from.
//
// A module analyzed without an analysis library gets no anchors and every
// mention refuses.
// It resolves against the base anchors — every family but this one — because
// stdAnchorsOf consults THIS function and the two would otherwise call each
// other forever (a stack overflow, not a subtle wrong answer).
// The restriction is real and fail-safe rather than accidental: a spec method
// that named another stdlib interface in a parameter or result would find
// kindInvalid there, fail stdIfaceNames, and produce NO anchor, so every
// mention of that interface refuses loudly instead of lowering against a table
// typed on a kind the base pass could not build. No spec row names one.
func stdIfaceAnchors(fa *analysis.FileAnalysis) (map[*ast.InterfaceDef]int, map[string]int) {
	return stdIfaceAnchorsWith(fa, stdAnchorsOf(fa))
}

// stdIfaceAnchorsWith is stdIfaceAnchors against anchors the caller already
// built, which is how stdAnchorsOf reaches it without recursing.
func stdIfaceAnchorsWith(fa *analysis.FileAnalysis, anchors stdAnchors) (map[*ast.InterfaceDef]int, map[string]int) {
	byDecl := map[*ast.InterfaceDef]int{}
	byName := map[string]int{}
	if fa == nil || fa.ModuleScope == nil {
		return byDecl, byName
	}
	// The non-self parameter and result types resolve through the same anchors
	// a stdlib SIGNATURE resolves through, so `Ordering` means the shared rt
	// enum here or it means nothing. A module that cannot name `Ordering`
	// therefore cannot anchor `Comparable`, which is the fail-safe direction:
	// the table's result type would have had nothing to render as.
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
		if !isDecl || !s.matches(decl, anchors) {
			continue
		}
		byDecl[decl] = i
		byName[s.nomi] = i
	}
	return byDecl, byName
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
// re-exports to the file (`import std/context.{Context}`), and the name is the
// file's own. Without it — an installed binary with no source tree, the tour's
// wasm in a browser — the prelude's module scope is the file's PARENT, and the
// name is the prelude's own import statement, which spells its sibling bare
// (`import context.{Context} export`). Qualifying that bare path by the USER
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
// from — so a std edit that adds a method, reorders parameters, moves the self
// position, changes a result type, adds a default body or makes a method
// host-backed produces NO anchor, and every mention then refuses under
// `stdlib interface` instead of lowering against a table whose Go type no
// longer describes it.
//
// That is the one failure this file's identity machinery cannot otherwise
// detect: a shape drift is a wrong answer, not a compile error.
//
// An attached `//!` test is deliberately NOT a disqualifier — see
// stdStructSpec.matches.
func (s *stdIfaceSpec) matches(decl *ast.InterfaceDef, anchors stdAnchors) bool {
	switch {
	case decl.Name != s.nomi, !decl.Public:
		return false
	case len(decl.TypeParams) > 0, len(decl.WhereClauses) > 0:
		// A generic interface needs the dictionary a generic type needs, which
		// is the refusal declareIfaces already records for a declared one.
		return false
	case len(decl.Fields) > 0:
		// A field requirement is a construct this builder refuses on its own
		// terms; redirecting the interface to an rt table would silently drop
		// that refusal.
		return false
	case len(decl.Methods) != len(s.methods):
		return false
	}
	for i := range s.methods {
		if !s.methods[i].matches(&decl.Methods[i], anchors) {
			return false
		}
	}
	return true
}

func (ms *stdIfaceMethodSpec) matches(im *ast.InterfaceMethod, anchors stdAnchors) bool {
	switch {
	case im.Name != ms.name:
		return false
	case im.Open, im.Extern, im.Body != nil:
		// A DEFAULT — Nomi-bodied or host-backed — is monomorphized into each
		// implementing block by inheritDefaults, which resolves the body's
		// names in the IMPLEMENTING module's scope. For a stdlib interface that
		// scope is never the declaring one, so a default is exactly the
		// silent-wrong-answer hazard existential.go refuses
		// `sibling file interface default` for — and with no `portableDefault`
		// question available to ask, because the declaring file is not a unit
		// of this program. No anchor.
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
		if !stdIfaceNames(got.TypeAnnotation, p.nomi, p.kind(), anchors) {
			return false
		}
	}
	if ms.result == "" {
		return im.ReturnTypeExpr == nil
	}
	if _, selfReturn := im.ReturnTypeExpr.(*ast.SelfType); selfReturn {
		return false
	}
	return stdIfaceNames(im.ReturnTypeExpr, ms.result, ms.resultKind(), anchors)
}

// stdIfaceNames reports whether te spells `nomi` AND resolves to exactly the
// kind the spec declares.
//
// Both halves, deliberately. The SPELLING is what a reader checks against the
// std source by eye. The KIND is what the emitted table's Go type was built
// from, resolved through stdTypeKind — the same function stdlib signatures go
// through — so an `Ordering` that is some other module's `Ordering` produces no
// anchor rather than a table typed on the wrong Go type.
func stdIfaceNames(te ast.TypeExpr, nomi string, want kind, anchors stdAnchors) bool {
	st, isSimple := te.(*ast.SimpleType)
	if !isSimple || st.Name != nomi {
		return false
	}
	// kindInvalid: lookup — an unanchored name answers kindInvalid, which
	// cannot equal a spec's kind, so the comparison is the whole check.
	return stdTypeKind(te, anchors) == want
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
// each DECLARE
// the interface a spec row describes, `declareIfaces` mints an ordinary
// per-gen `*ifaceDef` for it with `stdShared` unset, and `stdIfaceNamed`
// prefers that local def by design (a user declaration must win). None of
// the three names its own interface in a lowered signature, so nothing
// reaches it. Closing it would need an anchor that does NOT require an
// import, which is a different resolution rule from this one.
func (g *gen) loadStdIfaces() {
	if g.stdIfacesLoaded {
		return
	}
	g.stdIfacesLoaded = true
	_, g.stdIfaceByName = stdIfaceAnchors(g.fa)
	g.structIfaceOK = structIfaceAnchored(g.fa)
}

// stdIfaceNamed resolves a type NAME to the shared def, for a module that
// MENTIONS a stdlib interface.
//
// A USER declaration of the same name always wins, and the check is HERE
// rather than in the anchor because the analyzer's module scope is not a
// shadow oracle for interfaces: a file declaring its own
// `pub interface Display` still resolves `Display` in `fa.ModuleScope` to
// STD's declaration through the prelude auto-prepend — the local declaration
// is simply not there under that name, because `defineInterface` does not
// register it the way `defineStruct` does. So the anchor alone says "std
// declares a Display this module can see", which is true and is not the
// question.
//
// `ifaceNamed` is this package's own answer to WHICH Display, and it is the
// one every other resolution path reads: this module's declaration first, then
// a sibling file's mirror. Asking it here is what makes the preference
// uniform, and it is what keeps `projectStdIface` — whose input is a solved
// type carrying only a NAME — from adopting rt's table for a user's interface,
// which would be a wrong answer rather than a refusal.
func (g *gen) stdIfaceNamed(name string) (*ifaceDef, bool) {
	if _, user := g.ifaceNamed(name); user {
		return nil, false
	}
	g.loadStdIfaces()
	if name == structIfaceName {
		// The universal structural marker, whose def is NOT a row in this
		// file's table because it is not a dispatch table. Asked here rather
		// than beside the table so every consumer of `stdIfaceNamed` —
		// `typeOf`, `projectStdIface`, `resolveImplDef`, `dictBoundIface`,
		// `sigreason` — reaches it through the one function, which is what
		// keeps the user-declaration-wins check above from having a second
		// copy. See structiface.go.
		if !g.structIfaceOK {
			return nil, false
		}
		return structIfaceDef(), true
	}
	i, anchored := g.stdIfaceByName[name]
	if !anchored {
		return nil, false
	}
	return stdIfaceDefs()[i], true
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
	d, anchored := g.stdIfaceNamed(ty.Name)
	if !anchored {
		return kindInvalid, false
	}
	return existential(d), true
}

// --- the erased-receiver call ------------------------------------------------
