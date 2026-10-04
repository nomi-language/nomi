package irbuild

import (
	"reflect"
	"strings"
	"sync"

	"github.com/nomi-language/nomi/rt"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// The stdlib's scalar newtypes — `pub opaque type Duration Int`,
// `pub type Toml String` — as Go types declared in rt.
//
// The file is named after the first five rows and the family is not: what a
// row describes is a std DISTINCT over a scalar, and `opaque` is one of the
// two clauses one may be declared with. See opaqueSpecs on why the clause
// turned out not to be a representation question, and declForm on why each row
// still has to name it.
//
// # This is not a handle
//
// Duration and Instant are not `host type`s the Nomi side never inspects. std
// declares them `opaque type X Int`: nominal newtypes over Int, with Nomi
// bodies for nearly everything (`Duration.seconds` is
// `Duration(n * 1_000_000_000)`). There is no handle to box.
//
// The `<opaque T>` Debug rendering is not owed here either: the analyzer
// synthesizes it as an ordinary Nomi `impl Debug` AST node
// (analysis.synthesizeOpaqueNameOnlyDebug) before the builder sees the tree,
// so irbuild lowers it through the impl path it already has.
//
// # Two populations, two mechanisms
//
// A STDLIB newtype's Go type is declared in rt (`rt.Duration`, `rt.Toml`). A
// USER's `opaque type Meters Int` is a type def named `NomiT_Meters` in its own
// unit package and crosses a file boundary through foreign.go's mirror,
// exactly like any other distinct type. The asymmetry is not a shortcut: rt
// IMPLEMENTS the stdlib's externs and so has to be able to name their types,
// and rt implements nothing of a user's — a user declaration inside rt would
// put user types in the runtime library.
//
// TestOpaqueUserTypeTakesTheMirrorRoute asserts the negative half of that
// directly, because it is the half a name-keyed registry would get wrong.
//
// # Identity: the anchor, and one shared *typeDef
//
// A table keyed on "Duration" would be a type identity spelled as a string: a
// user file may declare its own, and lowering that to rt.Duration would mix two
// Nomi types the checker keeps apart. So a spec is anchored per module against
// the analyzer's own (Origin, Name) rule, and the declaration's SHAPE is
// checked — opaque, non-generic, inner exactly Int — so a std edit produces no
// anchor and every mention refuses loudly rather than lowering against a layout
// nobody wrote. Same two steps prelude.go takes for Maybe and Result.
//
// The *typeDef is PROCESS-WIDE, one per spec, and that is a deliberate exception
// to foreign.go's rule against shared defs. foreign.go's reason is that "a
// shared def's field kinds are interned in the OWNER's g.comps", so a component
// renders correctly in one package and nowhere else. An opaque spec has one
// component and it is a SCALAR — int64 in every package, interned nowhere — and
// it is declared in rt. Nothing about it is package-relative.
//
// It has to be shared, not merely may be: a stdFunc's parameter and result kinds
// are built once in stdCandidateFor and compared by POINTER against a call
// site's argument kinds in a different gen, so a per-gen def would make
// `Duration.as_nanos(d)` mismatch against itself.
//
// TestOpaqueDefsArePackageNeutral is the guard on the precondition rather than a
// comment claiming it.

// declForm is the DECLARATION CLAUSE a spec describes, and it is required.
//
// A bool would default to one of the two answers, so a row added without
// thinking about the clause would silently claim it. The zero value here
// claims neither and matches() refuses it, so a forgotten field produces no
// anchor and every mention of the type refuses loudly — which is the direction
// every identity check in this family fails in.
type declForm int

const (
	// formUnset is the zero value and matches nothing.
	formUnset declForm = iota
	// formOpaque is `pub opaque type X <scalar>`.
	formOpaque
	// formDistinct is `pub type X <scalar>`.
	formDistinct
)

// opaqueSpec is one stdlib distinct newtype over a scalar: what the builder
// believes std declares, and which Go type in rt it is.
type opaqueSpec struct {
	// origin is the declaring file's build key, the analyzer's own identity
	// half. See analysis.DistinctType.Origin.
	origin string
	// nomi is the type's Nomi name, the other half.
	nomi string
	// form is the declaration clause. Required; see declForm.
	form declForm
	// inner is the kind the declaration must wrap. Only a scalar may appear
	// here: it is what makes the shared def package-neutral, and
	// TestOpaqueDefsArePackageNeutral enforces it.
	inner kind
	// goType is rt's Go type, held as the TYPE rather than as its spelling for
	// the same reason stdlibBinding holds a func value rather than a name: a
	// rename or a deletion in rt is then a Go compile error in this file, and
	// the pairing is checked by reflection in tests.
	goType reflect.Type
}

// opaqueSpecs is the whole set, and it is short on purpose.
//
// A row here is a Go type rt declares by hand, so adding one is a decision
// rather than a mechanical extension — the same bar preludeSpecs sets. A row
// does not need an rt extern over the type: `NonZeroInt` and `PositiveInt`
// have none, because every function over them in std is ordinary Nomi. What a
// row is for is letting a SIGNATURE name the type.
//
// # The shape this table admits
//
// `pub [opaque] type X <scalar>`, with the clause named per row: matches()
// requires the row's `form`, Public, no body/items, no decorator but `derive`,
// and an inner type scalarKind admits.
//
// `opaque` is a USE-SITE rule the front end enforces, not a representation. A
// distinct over a scalar is a Go defined type over that scalar whether or not
// callers outside the declaring file may construct and destructure one.
// Nothing downstream of the shared def reads the clause; `Toml("x")` and
// `Toml(raw) = t` lower through the same distinct-constructor and
// distinct-pattern paths a USER's own `type Meters Int` uses, and
// testdata/toml_use.nomi exercises both from a module that merely imports the
// type.
//
// A row still must not redirect a declaration whose SHAPE it has not checked.
// `form` is that check, and it is required rather than defaulted, so a std edit
// from `pub type Toml String` to `pub opaque type Toml String` produces NO
// anchor and every mention refuses, the same direction a changed `inner` fails
// in. Both clauses are represented: `std/toml`'s `pub type Toml String`, and
// `std/calendar`'s ten civil units (`pub type Years Int` through
// `pub type Nanoseconds Int`).
//
// TestOpaqueSpecsAreLoadBearing checks, per row, that some std DECLARATION's
// signature names the type, which is this table's actual bar and the reason
// `NonZeroInt` belongs in it with zero corpus mentions.
//
// `Codepoint`, `NonZeroInt` and `PositiveInt` are reached mostly through
// `Maybe<…>`. A prelude instance over package-neutral arguments is interned
// PROCESS-WIDE, for exactly the reason this file's header gives for sharing an
// opaque def, so `Maybe<NonZeroInt>` is a kind a call site in another gen
// compares equal to. See stdprelude.go.
var opaqueSpecs = []opaqueSpec{
	{origin: "std/duration", nomi: "Duration", form: formOpaque, inner: kindInt, goType: reflect.TypeFor[rt.Duration]()},
	{origin: "std/instant", nomi: "Instant", form: formOpaque, inner: kindInt, goType: reflect.TypeFor[rt.Instant]()},
	{origin: "std/int", nomi: "NonZeroInt", form: formOpaque, inner: kindInt, goType: reflect.TypeFor[rt.NonZeroInt]()},
	{origin: "std/int", nomi: "PositiveInt", form: formOpaque, inner: kindInt, goType: reflect.TypeFor[rt.PositiveInt]()},
	{origin: "std/codepoints", nomi: "Codepoint", form: formOpaque, inner: kindInt, goType: reflect.TypeFor[rt.Codepoint]()},
	{origin: "std/toml", nomi: "Toml", form: formDistinct, inner: kindString, goType: reflect.TypeFor[rt.Toml]()},
	// std/random's PRNG register. A row with NO extern over it, which this
	// table's own header says is the normal case rather than the exception:
	// `Seed.from_int(n: Int): Seed` is the Nomi body `Seed(n)`. See
	// rt/random.go for why no rule belongs beside the type.
	{origin: "std/random", nomi: "Seed", form: formOpaque, inner: kindInt, goType: reflect.TypeFor[rt.Seed]()},
	// std/calendar's civil periods. Ten rows for ten declarations rather than
	// one parameterised entry, because each is a DISTINCT Nomi type with a
	// distinct rt Go type and the whole point of the family is that
	// `Add<Hours, DateTime>` and `Add<Minutes, DateTime>` must not share a
	// signature. The order is std's own declaration order — largest unit
	// first — because that is the order a reader checks them against.
	{origin: "std/calendar", nomi: "Years", form: formDistinct, inner: kindInt, goType: reflect.TypeFor[rt.Years]()},
	{origin: "std/calendar", nomi: "Months", form: formDistinct, inner: kindInt, goType: reflect.TypeFor[rt.Months]()},
	{origin: "std/calendar", nomi: "Weeks", form: formDistinct, inner: kindInt, goType: reflect.TypeFor[rt.Weeks]()},
	{origin: "std/calendar", nomi: "Days", form: formDistinct, inner: kindInt, goType: reflect.TypeFor[rt.Days]()},
	{origin: "std/calendar", nomi: "Hours", form: formDistinct, inner: kindInt, goType: reflect.TypeFor[rt.Hours]()},
	{origin: "std/calendar", nomi: "Minutes", form: formDistinct, inner: kindInt, goType: reflect.TypeFor[rt.Minutes]()},
	{origin: "std/calendar", nomi: "Seconds", form: formDistinct, inner: kindInt, goType: reflect.TypeFor[rt.Seconds]()},
	{origin: "std/calendar", nomi: "Milliseconds", form: formDistinct, inner: kindInt, goType: reflect.TypeFor[rt.Milliseconds]()},
	{origin: "std/calendar", nomi: "Microseconds", form: formDistinct, inner: kindInt, goType: reflect.TypeFor[rt.Microseconds]()},
	{origin: "std/calendar", nomi: "Nanoseconds", form: formDistinct, inner: kindInt, goType: reflect.TypeFor[rt.Nanoseconds]()},
}

// opaqueDefs is the process-wide *typeDef per spec, keyed by spec index.
var opaqueDefs = sync.OnceValue(func() []*typeDef {
	defs := make([]*typeDef, len(opaqueSpecs))
	for i := range opaqueSpecs {
		s := &opaqueSpecs[i]
		defs[i] = &typeDef{nomi: s.nomi, isDistinct: true, inner: s.inner, lowerable: true, // rtDeclared, so typeDecl emits nothing for it: the Go type is
			// hand-written in rt/opaque.go, and a second declaration would be
			// a second Go type for one Nomi type.
			rtDeclared: true}
	}
	return defs
})

// opaqueKind is the kind of the i'th spec's values.
func opaqueKind(i int) kind { return named(opaqueDefs()[i]) }

// codepointKind is the kind of std's `Codepoint`, the type of a codepoint
// literal (`'a'`). The literal names std's declaration whatever the file has
// in scope, so it is looked up by the spec's (origin, name) and not through
// the module's anchors.
func codepointKind() kind {
	for i := range opaqueSpecs {
		if opaqueSpecs[i].origin == "std/codepoints" && opaqueSpecs[i].nomi == "Codepoint" {
			return opaqueKind(i)
		}
	}
	return kindInvalid
}

// opaqueKindOfGoType is the kind an rt signature's Go type names, or
// kindInvalid.
//
// The direction kindOfGoType needs. It is what keeps
// `rt.DurationToString(rt.Duration) string` from projecting onto `(Int) ->
// String` and silently binding to an unrelated declaration — which is what
// typing the extern over plain int64 would have done.
func opaqueKindOfGoType(t reflect.Type) kind {
	for i := range opaqueSpecs {
		if opaqueSpecs[i].goType == t {
			return named(opaqueDefs()[i])
		}
	}
	return kindInvalid
}

// --- anchoring -------------------------------------------------------------

// opaqueAnchors resolves the opaque specs against one module's analysis: the
// declaration NODE it reached each spec through, and the spec index under the
// Nomi name.
//
// A free function rather than a gen method because two callers need it and only
// one is a gen: the STDLIB SIGNATURE pass (collectStdCandidates) runs before any
// gen exists and has to know whether `Duration` in `Duration.seconds(n: Int):
// Duration` is a representable type. Two implementations of "is this the std
// type" is exactly the drift this file's identity rules exist to prevent, so
// there is one.
//
// A module analyzed without an analysis library gets no anchors and every
// mention refuses, which is what happened before this file existed.
func opaqueAnchors(fa *analysis.FileAnalysis) (map[*ast.TypeDef]int, map[string]int) {
	byDecl := map[*ast.TypeDef]int{}
	byName := map[string]int{}
	if fa == nil || fa.ModuleScope == nil {
		return byDecl, byName
	}
	for i := range opaqueSpecs {
		s := &opaqueSpecs[i]
		decl := opaqueDeclIn(fa, s)
		if decl == nil {
			continue
		}
		byDecl[decl] = i
		byName[s.nomi] = i
	}
	return byDecl, byName
}

// opaqueDeclIn is the declaration a spec's name resolves to in fa, validated
// against the spec's shape, or nil. Shared by opaqueAnchors and
// opaqueDeclaredInStd so the importing module and the declaring one apply one
// identity rule.
func opaqueDeclIn(fa *analysis.FileAnalysis, s *opaqueSpec) *ast.TypeDef {
	if fa == nil || fa.ModuleScope == nil {
		return nil
	}
	sym := fa.ModuleScope.Lookup(s.nomi)
	if sym == nil {
		return nil
	}
	if sym.Resolved != nil {
		sym = sym.Resolved
	}
	dt, isDistinct := sym.Type.(*analysis.DistinctType)
	if !isDistinct || dt.Origin != s.origin || dt.Name != s.nomi {
		// The name resolves to something else here, or to a declaration in
		// another file of the same name. Either way there is no anchor and
		// the ordinary paths report.
		return nil
	}
	decl, isDecl := sym.Node.(*ast.TypeDef)
	if !isDecl || !s.matches(decl) {
		return nil
	}
	return decl
}

// loadOpaques resolves the specs against this gen's module, once.
func (g *gen) loadOpaques() {
	if g.opaquesLoaded {
		return
	}
	g.opaquesLoaded = true
	g.opaqueByDecl, g.opaqueByName = opaqueAnchors(g.fa)
}

// matches reports whether decl is the declaration this spec describes.
//
// Everything checked here decides REPRESENTATION, and each check fails in the
// direction that produces NO anchor rather than a wrong layout. The CLAUSE is
// checked against the row's own `form` rather than being fixed at `opaque`,
// because both clauses have the same representation and only the row knows
// which one std wrote; a row whose form is unset therefore matches nothing.
// `Public` because a file-private declaration cannot be the type another
// module names. The inner type because `rt.Duration` is int64, so a std edit
// to `opaque type Duration Float` must produce no anchor rather than lower
// against the wrong width. A `type Name` marker has a nil InnerTypeExpr, which
// scalarKind answers kindInvalid for, so it cannot match either.
func (s *opaqueSpec) matches(decl *ast.TypeDef) bool {
	want := formOpaque
	if !decl.Opaque {
		want = formDistinct
	}
	if decl.Name != s.nomi || s.form != want || !decl.Public {
		return false
	}
	if decl.HasBody || len(decl.Items) > 0 {
		// A body or a type body item is layout-relevant. An attached `//!` test
		// is not, and is deliberately not checked — see stdStructSpec.matches.
		return false
	}
	for _, dec := range decl.Decorators {
		// `derive` is the one decorator that is not a gap. std/duration carries
		// three (`derive Equatable / Hashable / Comparable for Duration`), each
		// of which is ALSO synthesized into an ordinary impl block that lowers
		// through the ordinary path — so it is metadata about work already
		// done, which is exactly what declModifiers says about it. Any other
		// decorator is a real refusal and must not be redirected past.
		if dec.Name != "derive" {
			return false
		}
	}
	return scalarKind(decl.InnerTypeExpr) == s.inner
}

// opaqueDeclared reports the spec index for a type declaration this module
// carries, or -1. Read by buildTypes: an anchored declaration adopts the shared
// def instead of getting its own shell.
func (g *gen) opaqueDeclared(decl *ast.TypeDef) int {
	g.loadOpaques()
	if i, anchored := g.opaqueByDecl[decl]; anchored {
		return i
	}
	return -1
}

// opaqueNamed resolves a type NAME to the shared def, for a module that MENTIONS
// one of these types without declaring it.
//
// The anchor was established through the module's own scope, which is what the
// checker resolves a bare type name through, so no per-mention re-check is
// needed here — unlike prelude.go, whose subjects are VARIANT names that two
// enums may share. A module declaring its own `Duration` never reaches this:
// namedType consults g.types first, and buildTypes put the local declaration
// there.
func (g *gen) opaqueNamed(name string) (*typeDef, bool) {
	g.loadOpaques()
	i, anchored := g.opaqueByName[name]
	if !anchored {
		return nil, false
	}
	return opaqueDefs()[i], true
}

// projectStdOpaque is inferred.go's projection for one of these types: the
// DistinctType the checker solved for a position the program never annotated.
//
// stdenum.go's projectStdEnum with `DistinctType` in place of `EnumType`, and
// the same rule: identified by the solved type's (Origin, Name) against a spec
// whose declaration was validated through `matches` in its DECLARING module
// (opaqueDeclaredInStd), not by this module's anchor. A projection answers for
// positions where the program never writes the name, so an anchor keyed on
// this file's imports would refuse `|d|` over a std function's `Duration`
// result in a file that does not import `Duration`. The Origin is what keeps a
// user module's own `Days` out.
func (g *gen) projectStdOpaque(ty *analysis.DistinctType) (kind, bool) {
	if len(ty.TypeArgs) > 0 || ty.Origin == "" {
		return kindInvalid, false
	}
	declared := opaqueDeclaredInStd()
	for i := range opaqueSpecs {
		if opaqueSpecs[i].origin == ty.Origin && opaqueSpecs[i].nomi == ty.Name && declared[i] {
			return opaqueKind(i), true
		}
	}
	return kindInvalid, false
}

// opaqueDeclaredInStd is, per spec, whether the spec's declaring std module
// declares it in the spec's shape: opaqueDeclIn asked of std's own analysis,
// once per process, as stdEnumDeclaredInStd asks it for enums.
var opaqueDeclaredInStd = sync.OnceValue(func() []bool {
	lib := stdAnchorLib()
	ok := make([]bool, len(opaqueSpecs))
	for i := range opaqueSpecs {
		s := &opaqueSpecs[i]
		ok[i] = opaqueDeclIn(lib.Files[strings.TrimPrefix(s.origin, "std/")], s) != nil
	}
	return ok
})
