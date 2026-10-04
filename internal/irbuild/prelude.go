package irbuild

import (
	"strings"
	"sync"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// The two prelude enums, `Maybe<T>` and `Result<T, E>`, as generic Go data.
//
// # The representation needs no new mechanism
//
// A prelude enum is represented like any other enum: a tagged struct with tag
// 0 reserved invalid, one payload field per variant.
// `Maybe<T>` is `rt.Maybe[T]`. Nothing here is a type-parameter DICTIONARY —
// that open question is about DISPATCH (calling an interface method on a type
// parameter) and this file never reaches it. What it does reach, and refuses
// by name, is a prelude enum whose type ARGUMENT is a type parameter of the
// enclosing function: `Some(x)` inside `fn f<T>(x: T)` has no Go type to
// instantiate `rt.Maybe` at without monomorphizing the function, which is the
// rejected design. See preludeArgs.
//
// The Go-spelled `kind` text names the types in rt (rt/prelude.go) rather than
// a per-gen declaration, because a named type's identity here is the *typeDef
// its declaration produced and two gens have no shared type table. A per-gen
// `NomiT_Maybe[T]` would turn one Nomi type into N mutually unequal kinds.
// `rt.List[T]` is the same answer one file over.
//
// # Identity is a declaration POINTER, anchored by (Origin, Name)
//
// A prelude table keyed on the bare name `Maybe` would be a type identity
// spelled as a string and matched by name: a user file may declare its own
// `Maybe`, and lowering that to `rt.Maybe` would mix two Nomi types that the
// checker keeps apart.
//
// So identity is established once per module, in two steps, and neither step
// is a name match at a use site:
//
//  1. ANCHOR. `fa.ModuleScope.Lookup("Maybe")` is resolved and its
//     `analysis.EnumType` must carry `Origin == "std/maybe"` — the analyzer's
//     own nominal-identity rule, the same rule inferred.go's localNamed uses.
//     The symbol's
//     `Node` is then the `*ast.EnumDef` the analyzer itself resolved against,
//     valid for THIS compilation.
//  2. USE. Every `Some`, `None`, `Ok`, `Err` and `Maybe<…>` mention resolves
//     through `fa.References` to a symbol whose `Resolved.Node` is compared to
//     the anchor by POINTER. The prelude variant symbols and the enum symbol
//     share one `*ast.EnumDef` pointer, and `std.Load()` hands out
//     fresh pointers per call — which is exactly why the anchor is taken from
//     the analysis that produced the tree being lowered, and not from a second
//     std load.
//
// # The declaration's SHAPE is validated, not assumed
//
// The tags this file assigns (Some=1, None=2, Ok=1, Err=2) and the rt field
// names it writes (`Some`, `Ok`, `Err`) are a belief about what std/maybe.nomi
// and std/results.nomi declare. So the anchor is only built when the
// `*ast.EnumDef` actually has that shape — same type parameters in the same
// order, same variants in the same order, each payload naming the type
// parameter this file expects. A std edit that reorders a variant produces NO
// anchor, so every prelude construct refuses again, loudly, instead of
// lowering against a layout nobody wrote. TestPreludeShapeMatchesStdSource
// asserts the anchor IS built, so that failure mode cannot pass unnoticed
// either, and TestPreludeLayoutMatchesRT reflects over the real rt types so
// the field names and tags cannot drift apart from the ones rt declares.

// preludeSpec is the static description of one prelude enum: what this
// builder believes std declares, and how rt spells it.
type preludeSpec struct {
	// origin is the analyzer's nominal-identity key for the declaring file.
	origin string
	nomi   string
	// rtType is the Go generic type in rt, without its type arguments.
	rtType string
	// tagField is the exported Go field holding the variant tag.
	tagField string
	// params are the declared type parameter names, in declaration order.
	params   []string
	variants []preludeVariantSpec
	// tryOperand marks a spec `try` is defined over.
	//
	// It is a DECLARED fact rather than a derived one because shape cannot
	// answer it. A shape test ("two variants, the first carries one payload")
	// is satisfied by `Fragment<T>` too (`Static String`, `Dynamic T`), so
	// `try frag` would lower as "unwind on Dynamic, unwrap Static": a wrong
	// answer, held off only by a front-end check the builder never
	// re-evaluates.
	//
	// The set is the checker's (analysis.TryOperandTypeNames) and
	// TestTryOperandSetsAgreeWithTheChecker holds the two equal in both
	// directions, so this is one fact written twice with a tripwire rather than
	// one fact written twice and hoped over.
	tryOperand bool
	// assertionSubject marks a spec an assertion accepts as a SHAPE subject:
	// `assert e` holds when `e` is the CARRYING variant (variants[0]) and fails
	// when it is the other one, instead of requiring a Bool.
	//
	// A DECLARED fact for exactly the reason tryOperand is one, and the same
	// counterexample: `Fragment<T>` is a two-variant prelude enum, so a shape
	// test would admit `assert frag` and lower it as "holds when Static" — a
	// wrong answer held off only by a front-end check the builder never
	// re-evaluates. It is a SEPARATE fact from tryOperand even though the two
	// flags coincide: `try` unwraps a payload and an assertion validates a
	// shape, and they are one set because both are defined over the prelude's
	// carrying/empty pair rather than because either is defined in terms of the
	// other.
	//
	// TestAssertionSubjectSetsAgreeWithTheChecker holds it against the CHECKER's
	// own answer in both directions — behaviourally, by asking the front end
	// whether `assert` accepts a value of each spec's type, because a list
	// compared against a list would still pass if both were wrong.
	assertionSubject bool
}

// preludeVariantSpec is one variant. Its tag is its index + 1: tag 0 stays
// reserved invalid.
type preludeVariantSpec struct {
	nomi string
	// param indexes preludeSpec.params for the variant's single payload's
	// type, or -1 when the payload is not one of them — either because the
	// variant carries nothing, or because its type is CONCRETE and `fixed`
	// names it.
	param int
	// fixed is the payload kind of a variant whose payload type does not
	// mention a type parameter at all: `Fragment.Static` carries a `String`
	// whatever `T` is. kindInvalid when the payload is parametric (param >= 0)
	// or absent.
	//
	// Fragment is the first spec to MIX the two, and the mix is the whole
	// reason this field exists rather than a wider `param` encoding: at
	// `T = String` a concrete-`String` payload and a parametric payload
	// instantiated at `String` render to the SAME Go field type, so neither a
	// field-type check nor any `Fragment<String>` fixture can tell them apart.
	// `matches` therefore compares the DECLARATION's payload type expression,
	// where `String` and `T` are different tokens, and the layout guard
	// reflects over `rt.Fragment[int64]`, where they are different Go types.
	fixed kind
	// namedPayload is the payload of a variant whose type is CONCRETE but not
	// a SCALAR: `Outcome.Failed` carries `std/tasks.Failure`, a monomorphic
	// std enum, whatever `T` is. nil for every other shape.
	//
	// A fourth shape rather than a wider `fixed`, because `fixed` is a kind
	// and a named type's kind is a `*typeDef` pointer that cannot exist at
	// package-variable initialization time. It also keeps `matches`'
	// discharge honest: a scalar payload is checked with `scalarKind` and a
	// named one against the payload enum's own declaration, and those are
	// different questions. See taskoutcome.go, which owns the shape and the
	// argument for why a NAME check is an identity check for this one case.
	namedPayload *namedPayloadSpec
	// field is the exported Go field in rt the payload is stored in, and is
	// the variant's own name — rt names them that way so this file has no
	// second spelling to keep in step.
	field string
}

// payload is the kind this variant carries under one instantiation, and
// whether it carries anything at all.
//
// The one place the parametric/concrete distinction is discharged, so no
// caller repeats the `param >= 0` / `fixed != kindInvalid` pair and none can
// forget the second half, which would drop `Static`'s payload from a def
// that still has a field for it in rt.
func (v preludeVariantSpec) payload(args []kind) (kind, bool) {
	if v.param >= 0 {
		return args[v.param], true
	}
	// kindInvalid: marker — the spec's "this variant's payload is not concrete".
	if v.fixed != kindInvalid {
		return v.fixed, true
	}
	if v.namedPayload != nil {
		return named(v.namedPayload.def()), true
	}
	return kindInvalid, false
}

// carries reports whether this variant has a payload, without needing an
// instantiation to ask.
//
// kindInvalid: marker — as in payload, the spec's absent-concrete-payload mark.
func (v preludeVariantSpec) carries() bool {
	// kindInvalid: marker — as in payload, the spec's absent-concrete-payload mark.
	return v.param >= 0 || v.fixed != kindInvalid || v.namedPayload != nil
}

// preludeSpecs is the whole set: the generic std enums whose Go type rt
// declares by hand. Adding a row is a decision about what the corpus is
// written in terms of, not a mechanical extension.
//
// Two of the three are the PRELUDE proper — in every file's scope whether it
// imports them or not. `Fragment` is not: it is an ordinary `pub enum` in
// std/literals that a file has to import, and it is here because it is the
// parameter type of every typed literal's handler (all 14 `from_fragments`
// declarations in the repo take `List<Fragment<I>>`; the checker's
// checkTaggedString rejects any other parameter shape). The type name stays
// `preludeSpec` because the mechanism is unchanged — an (Origin, Name) anchor
// against a validated declaration shape, instantiated into an rt generic type
// — and the only observable difference is the one the anchor rule already
// gives: a module without `Fragment` in scope gets no anchor and refuses every
// mention, exactly as a module declaring its own `Maybe` does.
var preludeSpecs = []preludeSpec{
	{
		origin: "std/maybe", nomi: "Maybe", rtType: "rt.Maybe", tagField: "Tag",
		params:           []string{"T"},
		tryOperand:       true,
		assertionSubject: true,
		variants: []preludeVariantSpec{
			{nomi: "Some", param: 0, field: "Some"},
			{nomi: "None", param: -1},
		},
	},
	{
		origin: "std/results", nomi: "Result", rtType: "rt.Result", tagField: "Tag",
		params:           []string{"T", "E"},
		tryOperand:       true,
		assertionSubject: true,
		variants: []preludeVariantSpec{
			{nomi: "Ok", param: 0, field: "Ok"},
			{nomi: "Err", param: 1, field: "Err"},
		},
	},
	{
		// `pub enum Fragment<T> { Static String; Dynamic T }` (std/literals).
		// `Static`'s payload is CONCRETE — a literal's static segments are
		// always text, and only its `${…}` slots carry the interface `T` pins.
		origin: "std/literals", nomi: "Fragment", rtType: "rt.Fragment", tagField: "Tag",
		params: []string{"T"},
		variants: []preludeVariantSpec{
			{nomi: "Static", param: -1, fixed: kindString, field: "Static"},
			{nomi: "Dynamic", param: 0, field: "Dynamic"},
		},
	},
	{
		// `pub enum Outcome<T> { Completed T; Cancelled; Failed Failure }`
		// (std/tasks). THREE variants, and the first row to need all three
		// payload shapes at once: parametric, absent, and CONCRETE-but-named.
		//
		// `Cancelled` is also the first payload-less variant any spec declares
		// besides `Maybe.None`, which is what generalized the bare-variant
		// sentinel — see taskoutcome.go for the wrong answer that would
		// otherwise have been emitted, and for the guard that predicted it.
		origin: "std/tasks", nomi: "Outcome", rtType: "rt.Outcome", tagField: "Tag",
		params: []string{"T"},
		variants: []preludeVariantSpec{
			{nomi: "Completed", param: 0, field: "Completed"},
			{nomi: "Cancelled", param: -1},
			{nomi: "Failed", param: -1, namedPayload: outcomeFailure, field: "Failed"},
		},
	},
}

// preludeAnchor is one prelude enum as THIS compilation's analysis sees it.
// decl IS the identity; see the file comment.
type preludeAnchor struct {
	spec *preludeSpec
	decl *ast.EnumDef
}

// loadPreludes resolves the prelude enums against this module's analysis, once.
//
// A module analyzed without an analysis library gets no anchors and refuses
// every prelude construct, which is exactly what it did before this file
// existed.
func (g *gen) loadPreludes() {
	if g.preludesLoaded {
		return
	}
	g.preludesLoaded = true
	g.preludeByDecl, g.preludeByName = preludeAnchorsOf(g.fa)
}

// preludeAnchorsOf resolves the prelude specs against one module's analysis:
// the declaration NODE each spec was reached through, and the anchor under the
// Nomi name.
//
// A free function rather than only a gen method for the reason opaqueAnchors is
// one: the STDLIB SIGNATURE pass (collectStdCandidates) runs before any gen
// exists and has to know whether `Maybe<Int>` in `Float.to_int(x: Float):
// Maybe<Int>` is a representable type. Two implementations of "is this the
// prelude type" is exactly the drift this file's identity rules exist to
// prevent, so there is one.
//
// Never returns nil maps: every caller indexes them.
func preludeAnchorsOf(fa *analysis.FileAnalysis) (map[*ast.EnumDef]*preludeAnchor, map[string]*preludeAnchor) {
	byDecl := map[*ast.EnumDef]*preludeAnchor{}
	byName := map[string]*preludeAnchor{}
	if fa == nil || fa.ModuleScope == nil {
		return byDecl, byName
	}
	for i := range preludeSpecs {
		spec := &preludeSpecs[i]
		sym := fa.ModuleScope.Lookup(spec.nomi)
		if sym == nil {
			// NOT IN SCOPE AT ALL, which for a STDLIB module is the ordinary
			// case rather than an error: std files get no prelude, so a module
			// sees `Maybe` only if it imports it — and a module that merely
			// pattern-matches one with the `.Some(x)` shorthand never writes the
			// name, so importing it would be an unused-import error.
			//
			// `std/random`'s `uniform` body does
			// `case Iter.at(all, i) { .Some(x) -> … }` with no `Maybe` anywhere
			// in the file. `Iter.at`'s own declaration in std/iter says the
			// result IS std/maybe's `Maybe`, so the identity is not in doubt —
			// it simply is not written down in THIS module — and without an
			// anchor `iterMaybe` would refuse
			// `Iter terminal without the prelude Maybe | Iter.at`.
			//
			// The fallback is SHAPE-VALIDATED, which is what makes it safe to
			// authorize a layout. preludeSpecFor exists for the same
			// scope-independence and is deliberately confined to the
			// refusal-REASON path precisely because it skips that validation;
			// this asks the DECLARING module instead, which is
			// stdGenStructValidated's discipline and gives a real *ast.EnumDef
			// to run `spec.matches` against.
			//
			// Only when the name is ABSENT. A module that binds `Maybe` to
			// something else keeps refusing through the arm below, so the
			// shadowing rule this function enforces is untouched — the fallback
			// answers a question the module's scope declines to answer at all,
			// rather than overriding an answer it gave.
			if a := preludeDeclaringAnchor(spec); a != nil {
				byDecl[a.decl] = a
				byName[spec.nomi] = a
			}
			continue
		}
		if sym.Resolved != nil {
			sym = sym.Resolved
		}
		et, isEnum := sym.Type.(*analysis.EnumType)
		if !isEnum || et.Origin != spec.origin || et.Name != spec.nomi {
			// Not the prelude type: either the name resolves to something
			// else here, or a file declares its own. Either way there is no
			// anchor and every use refuses by name.
			continue
		}
		decl, isDecl := sym.Node.(*ast.EnumDef)
		if !isDecl || !spec.matches(decl) {
			continue
		}
		a := &preludeAnchor{spec: spec, decl: decl}
		byDecl[decl] = a
		byName[spec.nomi] = a
	}
	return byDecl, byName
}

// preludeDeclaringAnchor is the anchor for a spec resolved against STD'S OWN
// analysis of the module that declares it, or nil.
//
// stdGenStructValidated's shape exactly, one spec table over: ask the declaring
// module, because that is the one scope in which the answer cannot depend on
// which names an importer chose to import. The validation is the same
// `spec.matches` every anchor goes through, so an anchor from here authorizes a
// layout on the same evidence as one from a module's own scope.
//
// Computed once for the process. The specs are a fixed table, so the answer
// cannot vary between gens, and one computation avoids a std.Load per gen.
func preludeDeclaringAnchor(spec *preludeSpec) *preludeAnchor {
	for _, a := range preludeDeclaringAnchors() {
		if a.spec == spec {
			return a
		}
	}
	return nil
}

var preludeDeclaringAnchors = sync.OnceValue(func() []*preludeAnchor {
	lib := stdAnchorLib()
	out := make([]*preludeAnchor, 0, len(preludeSpecs))
	for i := range preludeSpecs {
		spec := &preludeSpecs[i]
		fa := lib.Files[strings.TrimPrefix(spec.origin, "std/")]
		if fa == nil || fa.ModuleScope == nil {
			continue
		}
		sym := fa.ModuleScope.Lookup(spec.nomi)
		if sym == nil {
			continue
		}
		if sym.Resolved != nil {
			sym = sym.Resolved
		}
		et, isEnum := sym.Type.(*analysis.EnumType)
		if !isEnum || et.Origin != spec.origin || et.Name != spec.nomi {
			continue
		}
		decl, isDecl := sym.Node.(*ast.EnumDef)
		if !isDecl || !spec.matches(decl) {
			continue
		}
		out = append(out, &preludeAnchor{spec: spec, decl: decl})
	}
	return out
})

// preludeSpecFor is the spec a type's own nominal identity names, independent
// of any module's scope.
//
// (Origin, Name) is the analyzer's nominal-identity rule, so this establishes
// the same identity preludeAnchorsOf does — MINUS the declaration-shape
// validation, and minus the `ModuleScope.Lookup` that makes an anchor a
// statement about one module. Both omissions are why it is restricted to the
// refusal-REASON path and never produces a kind: a reason decides which operand
// to NAME, while a kind decides a LAYOUT, and only a shape-validated anchor may
// authorize one.
//
// The scope-independence is the point. `Maybe` and `Result` are in every
// module's scope, so for them the two identities coincide. `Fragment` is an
// ordinary import, and the mentions that matter most are the ones no module
// imports: a typed literal's `<Type>"…"` desugars to a call whose parameter is
// the HANDLER's `List<Fragment<I>>`, declared in std/calendar or std/toml.
func preludeSpecFor(origin, name string) *preludeSpec {
	for i := range preludeSpecs {
		if preludeSpecs[i].origin == origin && preludeSpecs[i].nomi == name {
			return &preludeSpecs[i]
		}
	}
	return nil
}

// matches reports whether decl is the declaration this spec describes.
//
// Everything checked here decides LAYOUT: the type parameter order fixes which
// Go type argument each payload gets, and the variant order fixes the tags. A
// declaration that disagrees is not lowered at all.
//
// # Why the payload check reads the DECLARATION and not the rendered field
//
// A spec's payload is one of three things — absent, the type parameter at
// index `param`, or the concrete kind `fixed` — and this is the only place all
// three are distinguishable. `Fragment.Static String` and `Fragment.Dynamic T`
// both render to a `string` field at `T = String`, which is the only
// instantiation the corpus contains, so a check on the field's kind would
// accept a spec with the two SWAPPED and every `Fragment<String>` program would
// keep working. `Fragment<Int>` would then put a `string` where an `int64`
// belongs. Here the two are different tokens in the source — `String` versus
// `T` — so the swap cannot pass, whatever the instantiation.
func (s *preludeSpec) matches(decl *ast.EnumDef) bool {
	if decl.Name != s.nomi || decl.Opaque || len(decl.TypeParams) != len(s.params) {
		return false
	}
	for i, p := range s.params {
		if decl.TypeParams[i].Name != p {
			return false
		}
	}
	if len(decl.Variants) != len(s.variants) {
		return false
	}
	for i, want := range s.variants {
		got := decl.Variants[i]
		if got.Name != want.nomi {
			return false
		}
		if !want.carries() {
			if got.Kind != "bare" {
				return false
			}
			continue
		}
		if got.Kind != "positional" {
			return false
		}
		st, isSimple := got.DataTypeExpr.(*ast.SimpleType)
		if !isSimple {
			return false
		}
		if want.param >= 0 {
			// Parametric: the payload must NAME the type parameter this spec
			// assigned it, so a reordered `<T, E>` produces no anchor.
			if st.Name != s.params[want.param] {
				return false
			}
			continue
		}
		if want.namedPayload != nil {
			// Concrete but NOT a scalar: the payload names a monomorphic std
			// enum CO-DECLARED with this spec's own type. The name check is an
			// identity check only because of that co-declaration, and the
			// origins are compared here so the licence is enforced at the
			// point it is used rather than trusted from the table.
			//
			// The payload enum's own SHAPE is validated separately, against
			// std's source, by TestOutcomeShapeMatchesStdSource — `decl` is
			// this spec's declaration and does not carry the payload enum's.
			// See taskoutcome.go.
			if want.namedPayload.origin != s.origin || st.Name != want.namedPayload.nomi {
				return false
			}
			continue
		}
		// Concrete: the payload must be the scalar the spec fixed it to.
		// scalarKind is the builder's own closed answer for "what type does
		// this annotation name", so a `fixed` a future row spells wrong — or
		// spells with a type this builder has no scalar for — answers
		// kindInvalid and produces no anchor rather than a guessed layout.
		if scalarKind(got.DataTypeExpr) != want.fixed {
			return false
		}
	}
	return true
}

// preludeAt resolves a source position to the prelude enum the name there
// belongs to, plus the resolved symbol.
//
// The position is the one the analyzer recorded the REFERENCE at: a
// `*ast.TypeIdent`'s own line and column for `Some(3)` and for the `Maybe` in
// `Maybe<Int>`, and the FIELD's for the qualified `Maybe.Some(3)`.
func (g *gen) preludeAt(line, col int) (*preludeAnchor, *analysis.Symbol, bool) {
	g.loadPreludes()
	if g.fa == nil || len(g.preludeByDecl) == 0 {
		return nil, nil, false
	}
	sym, found := g.fa.References[analysis.Pos{Line: line, Col: col}]
	if !found || sym == nil {
		return nil, nil, false
	}
	res := sym
	if res.Resolved != nil {
		res = res.Resolved
	}
	decl, isDecl := res.Node.(*ast.EnumDef)
	if !isDecl {
		return nil, nil, false
	}
	a, isPrelude := g.preludeByDecl[decl]
	if !isPrelude {
		return nil, nil, false
	}
	// sym, not res: the CALL-SITE symbol is where the analyzer records the
	// INSTANTIATED signature, and res is the shared declaration symbol whose
	// signature is still generic.
	return a, sym, true
}

// --- instantiation ----------------------------------------------------------

// preludeInstance is the *typeDef for one instantiation, interned so that two
// mentions of `Maybe<Int>` are one pointer and therefore one kind.
//
// Interning follows composite.go's rule applied to a named type: grouped by
// Nomi spelling and separated by the argument kinds, so one entry cannot be
// reached by two Nomi types.
//
// The instantiated def is a perfectly ordinary enum typeDef — tags, variants,
// payload kinds and slots — so `case`, coercion, payload reads and boxing all
// work through the machinery types.go and case.go already have. Two things
// differ, and both are consequences of the Go type being declared in rt rather
// than emitted here:
//
//   - Slots are NOT deduped by identical Go type. `Result<Int, Int>` carries
//     two int64 fields where a monomorphic enum of the same shape would carry
//     one, because a Go generic struct cannot know whether T and E coincide.
//     Dedup is a storage optimization and never a semantic rule, so this is
//     sound; it is spelled out because assignSlots' comment says the opposite
//     for every other enum.
//   - Nothing emits a declaration for it. typeDecl walks the module's own
//     declarations and these are not among them.
//
// # Where the def LIVES depends on the type arguments, and only on those
//
// An instance every gen renders identically — `rt.Maybe[int64]`
// — is interned PROCESS-WIDE, so every gen holds one pointer for it. An
// instance whose rendering is package-relative — `rt.Maybe[NomiT_Point]` — is
// interned per gen. See sharedPreludeDef for
// why the split exists and what makes the shared half safe.
func (g *gen) preludeInstance(a *preludeAnchor, args []kind) kind {
	return g.preludeInstanceOf(a.spec, args)
}

// preludeInstanceOf is preludeInstance keyed on the SPEC rather than on a
// module's anchor.
//
// The anchor exists to establish, for one module, that a name really is the
// prelude type; once that is settled the instantiation depends on nothing but
// the spec and the arguments. Split out because the stdlib signature pass and
// the extern registry both reach an instantiation with a spec in hand and no
// anchor to offer — see stdprelude.go.
func (g *gen) preludeInstanceOf(spec *preludeSpec, args []kind) kind {
	for _, arg := range args {
		// kindInvalid: propagates — a kind CONSTRUCTOR; preludeArgs rejected the type argument.
		if arg == kindInvalid {
			return kindInvalid
		}
	}
	if k, shared := sharedPreludeInstance(spec, args); shared {
		return k
	}
	nomi := preludeNomi(spec, args)
	if g.preludeInsts == nil {
		g.preludeInsts = map[string][]*typeDef{}
	}
	// Grouped by Nomi spelling and separated by the type ARGUMENTS, for
	// composite.go's reason: two same-named argument types from two files
	// are spelled alike. Keying on the text alone would give `Maybe<Alpha>` and
	// `Maybe<Beta>` one def, and the second one's payload would then refuse with
	// a false reason on a correct program. Quieter than composite.go's panic on
	// the same mistake and therefore worse.
	for _, d := range g.preludeInsts[nomi] {
		if samePartsSlice(d.preludeArgs, args) {
			return named(d)
		}
	}
	d := buildPreludeDef(spec, args)
	g.preludeInsts[nomi] = append(g.preludeInsts[nomi], d)
	g.preludeOrder = append(g.preludeOrder, d)
	return named(d)
}

// preludeTypeOf reads the annotation `Maybe<Int>` / `Result<Int, String>`.
//
// Reports whether the generic type named a prelude enum at all, so an ordinary
// unrepresentable `Foo<Bar>` keeps falling through to its own refusal.
func (g *gen) preludeTypeOf(t *ast.GenericType) (kind, bool) {
	a, _, isPrelude := g.preludeAt(t.Line, t.Col)
	if !isPrelude {
		// A node DERIVE SYNTHESIS wrote, which has no position to resolve by.
		//
		// Asked second for readability and NOT for correctness: asking the
		// fallback FIRST changes no answer, because the two arms' domains are
		// DISJOINT — `IsSynthesizedLine` is true
		// exactly for a position in derive synthesis' reserved band, and
		// `fa.References` never holds one (analysis/builder.go excludes it). So
		// what keeps this off real source is the BAND GATE inside
		// synthPreludeAnchor, not the order; attributing it to the order would
		// leave a reordering merge looking dangerous when it is free, and would
		// hide that deleting the gate is the real hazard. See synthderive.go.
		a, isPrelude = g.synthPreludeAnchor(t)
	}
	if !isPrelude {
		return kindInvalid, false
	}
	if len(t.Params) != len(a.spec.params) {
		// The checker would have rejected this; answering kindInvalid rather
		// than indexing past the spec keeps a malformed tree from panicking.
		return kindInvalid, true
	}
	args := make([]kind, len(t.Params))
	for i, p := range t.Params {
		args[i] = g.typeOf(p)
		// kindInvalid: propagates — type resolution; typeOf`s caller rejects `non-scalar` by position.
		if args[i] == kindInvalid {
			return kindInvalid, true
		}
	}
	return g.preludeInstance(a, args), true
}

// preludeArgKind projects ONE type argument of a prelude instantiation,
// substituting a constant where the checker left the position unsolved.
//
// # Why a constant is the answer, and the corpus is what settles it
//
// `Ok(v)` names T and says nothing about E; `Err(e)` names E and says nothing
// about T. So a Result built by one constructor and never given a second source
// of information arrives here as `Result<Int, ?7>` — well typed, with one
// argument no part of the program ever names.
//
// That position is INFORMATION-FREE, and not merely unknown: no value of that
// type exists anywhere in the program, because if one did it would have solved
// the variable. The corpus proves the "if one did" half by OMISSION —
// `Result.equal?(Ok(4), Err("bad"))` appears in
// 12-derives-and-standard-interfaces/maybe_result_equatable_test.nomi and does
// NOT refuse, because `equal?`'s two parameters share one E and `Err` supplies
// what `Ok` left open. Wherever a sibling position holds the missing half the
// front end has already closed it, so every site that reaches this function is
// one where nothing can close it.
//
// And the answer does not depend on the choice: `case Ok(4) { Ok(n) -> n }`
// binds 4, `Result.equal?(Ok(4), Ok(4))` is True and `Result.hash(Ok(4))` is
// stable, at EVERY instantiation of the open side. Same answer at two
// instantiations means a constant, which is the same reasoning `None` already
// rests on: kindBareNone renders `rt.None[rt.Unit]` (prelude.go's bareNoneCode)
// because a payload-less variant observes no type argument. This is that rule
// with the observation "a variant observes only the parameter it CARRIES", and
// for a single-payload prelude variant the carried parameter is always solved by
// its own argument — so a hole is only ever at a position the payload never
// mentions.
//
// # kindUnit rather than a fresh tag, and why the ambiguity is unreachable
//
// A defaulted `Result<Int, ?>` becomes `Result<Int, Unit>`, indistinguishable
// from a Result whose E genuinely IS Unit. That is sound because the two are
// only ever confusable at a position where one has to become the other, and
// every such position is one the CHECKER accepted: it would have to have
// accepted a `Result<Int, Unit>` flowing into a `Result<Int, String>` slot,
// which is ill typed. Where a declared type IS waiting and disagrees, the
// caller's own `arg.k != want` check refuses by position rather than emitting
// Go that does not compile — loud, and pinned by prelude_test.go.
//
// Unit is also the choice that costs nothing: it is zero-sized and
// package-neutral, so the instance interns process-wide and two independently
// projected `Result<Int, ?>` sites get the SAME def pointer. That is the
// identity rule, and prelude_unsolved.nomi's `first`/`second` pair exists to
// hold it: two `Ok(4)` sites compared to each other have nowhere to happen if
// their holes interned separately.
// It reports whether the position IS a hole, separately from the kind it
// answers, because `kindUnit` at a hole and `kindUnit` at a position whose type
// genuinely IS Unit are indistinguishable in the kind alone, and the caller
// has one thing it may do to a hole and must not do to a real Unit. See
// preludeWantedArgs.
func (g *gen) preludeArgKind(ty analysis.Type) (kind, bool) {
	// kindInvalid: lookup — asks whether the ordinary projection has an answer before deciding the position is a hole; the caller rejects by position.
	if k := g.project(ty); k != kindInvalid {
		return k, false
	}
	if isUnsolved(ty) {
		return kindUnit, true
	}
	return kindInvalid, false
}

// preludeReturn is the enum type a constructor signature returns, or nil.
func preludeReturn(ty analysis.Type) *analysis.EnumType {
	ft, isFunc := ty.(*analysis.FuncType)
	if !isFunc || ft.Return == nil {
		return nil
	}
	et, isEnum := ft.Return.(*analysis.EnumType)
	if !isEnum {
		return nil
	}
	return et
}

// isUnsolved reports whether ty bottoms out in an inference variable the checker
// never bound. It is not the same as "unrepresentable": the program is well
// typed and the position is information-free, which is why preludeArgKind can
// answer it with a constant.
//
// It FOLLOWS THE RESOLUTION CHAIN, exactly as its neighbour isTypeParam does.
// A TypeVar may resolve to ANOTHER unbound TypeVar; `String()` follows the
// chain and renders `?6`. A predicate that stopped at the first link would
// send a chained hole to preludeArgs' `default` arm, filed under `non-scalar
// type argument`, so `bad = Err("bad")` and an inline `Err("other")` would
// refuse under two different names depending on chain depth.
func isUnsolved(ty analysis.Type) bool {
	tv, isVar := ty.(*analysis.TypeVar)
	if !isVar {
		return false
	}
	if tv.Resolved == nil {
		return true
	}
	return isUnsolved(tv.Resolved)
}

// --- construction -----------------------------------------------------------

// resolvedPreludeVariant preserves declaration identity and canonicalizes
// constructor aliases for every caller.
//
// line and col are where the analyzer recorded the VARIANT's reference: the
// TypeIdent's own position for `Some(3)`, the field's for `Maybe.Some(3)`.
//
// `variant` is the LOCAL spelling. `import std/maybe.Maybe.{Some as Just}`
// makes that `Just`, and the spec's variant list holds the declaration's own
// `Some`, so the lookup below takes the canonical name from variantRefAt and
// the position match keeps using the local one. See variantalias.go.
func (g *gen) resolvedPreludeVariant(variant string, line, col int) (*preludeAnchor, *analysis.Symbol, preludeVariantSpec, bool) {
	a, sym, isPrelude := g.preludeAt(line, col)
	if !isPrelude || sym.Name != variant {
		return nil, nil, preludeVariantSpec{}, false
	}
	_, canonical, isVariant := variantRefAt(g.fa, variant, line, col)
	if !isVariant {
		return nil, nil, preludeVariantSpec{}, false
	}
	vs, found := a.spec.variant(canonical)
	return a, sym, vs, found
}

// checkedPreludeArgs reads only fully represented checker type arguments. An
// unresolved position declines.
func (g *gen) checkedPreludeArgs(a *preludeAnchor, sym *analysis.Symbol) ([]kind, bool) {
	et := preludeReturn(sym.CallType)
	if et == nil || len(et.TypeArgs) != len(a.spec.params) {
		return nil, false
	}
	args := make([]kind, len(et.TypeArgs))
	for i, ty := range et.TypeArgs {
		args[i] = g.project(ty)
		if !irRetainedPreludePayload(args[i]) {
			return nil, false
		}
	}
	return args, true
}

// checkedPreludeArgsUnitHoles is checkedPreludeArgs with every argument the
// checker left unsolved read as Unit: `Ok(1) == Ok(1)` constrains no error
// type, so no value the program can build or observe depends on it, as
// bareVariantOwnKind argues for a bare `None`. A solved argument outside the
// retained payloads still declines.
func (g *gen) checkedPreludeArgsUnitHoles(a *preludeAnchor, sym *analysis.Symbol) ([]kind, bool) {
	et := preludeReturn(sym.CallType)
	if et == nil || len(et.TypeArgs) != len(a.spec.params) {
		return nil, false
	}
	args := make([]kind, len(et.TypeArgs))
	for i, ty := range et.TypeArgs {
		args[i] = g.project(unitHoles(ty))
		if !irRetainedPreludePayload(args[i]) {
			return nil, false
		}
	}
	return args, true
}

// unitHoles is ty with every unsolved inference variable read as Unit, through
// enum type arguments (`Maybe<Result<Int, ?>>`), which is how far a prelude
// wrapper nests.
func unitHoles(ty analysis.Type) analysis.Type {
	switch t := ty.(type) {
	case *analysis.TypeVar:
		if t.Resolved == nil {
			return analysis.TypeUnit
		}
		return unitHoles(t.Resolved)
	case *analysis.EnumType:
		changed := false
		args := make([]analysis.Type, len(t.TypeArgs))
		for i, a := range t.TypeArgs {
			args[i] = unitHoles(a)
			changed = changed || args[i] != a
		}
		if !changed {
			return t
		}
		c := *t
		c.TypeArgs = args
		return &c
	}
	return ty
}

// bareNoneCode is what an undischarged `None` would render as, and it exists
// to be UNREACHABLE in emitted output rather than to be correct.
//
// `rt.Maybe[rt.Unit]` is assignable to no other `rt.Maybe[T]`, so a leak past
// coerce is a Go COMPILE error rather than a value that type-checks and
// answers wrongly, which is the failure mode a second encoding of one concept
// invites. The two positions that consume an
// expression without a coercion target refuse it explicitly instead; see
// gen.requireDischarged.
const bareNoneCode = "rt.None[rt.Unit]()"

func (s *preludeSpec) variant(name string) (preludeVariantSpec, bool) {
	for _, v := range s.variants {
		if v.nomi == name {
			return v, true
		}
	}
	return preludeVariantSpec{}, false
}

// preludeOwns reports whether `owner` is the enum-name spelling of d, for a
// pattern head written `Maybe.Some(v)`.
//
// d is already fixed by the SCRUTINEE's kind, whose identity was established at
// the annotation or construction site that produced it. This only asks whether
// the qualifier the programmer wrote agrees with the type being matched, which
// is what case.go's `g.types[owner] != d` asks for a module-declared enum.
//
// It compares the SPEC pointer, which the def already carries. Probing
// `g.preludeInsts` would miss a neutral instance, which lives in the
// process-wide table (stdprelude.go), and matching `d.nomi`'s prefix is a
// name match.
func (g *gen) preludeOwns(owner string, d *typeDef) bool {
	g.loadPreludes()
	a, isPrelude := g.preludeByName[owner]
	return isPrelude && d != nil && d.preludeOf != nil && d.preludeOf.spec == a.spec
}

// projectPreludeEnum is inferred.go's projection for an instantiated prelude
// enum: the type the checker solved for an unannotated position.
//
// It accepts two spellings of one fact, and the second needs its justification
// stated. `Origin` is the analyzer's identity key, so `Origin == spec.origin`
// is the strong form and is what a type reached through a declared annotation
// carries. A type the analyzer produced by INSTANTIATING the generic
// declaration — `Some(1)`'s solved return, and every nested argument inside one
// — carries an EMPTY Origin. Refusing those would lose the whole constructor
// path, so an empty Origin is accepted against an anchor that was itself
// established by (Origin, Name), and only when the module declares no type of
// that name — which is what keeps a user's own `Maybe` out.
func (g *gen) projectPreludeEnum(ty *analysis.EnumType) (kind, bool) {
	g.loadPreludes()
	a, isPrelude := g.preludeByName[ty.Name]
	if !isPrelude {
		a, isPrelude = preludeOriginAnchor(ty.Origin, ty.Name)
		if !isPrelude {
			return kindInvalid, false
		}
	} else if ty.Origin != a.spec.origin {
		if ty.Origin != "" {
			return kindInvalid, false
		}
		// The declaring module's own name is the prelude type, not a shadow.
		if _, shadowed := g.types[ty.Name]; shadowed && (g.fa == nil || g.fa.Origin != a.spec.origin) {
			return kindInvalid, false
		}
	}
	if len(ty.TypeArgs) != len(a.spec.params) {
		return kindInvalid, true
	}
	args := make([]kind, len(ty.TypeArgs))
	for i, arg := range ty.TypeArgs {
		// preludeArgKind, not project: a nested instantiation carries the same
		// holes a constructor's does, and it reached this builder under a
		// DIFFERENT key name because of it. `Some(Ok(42))` is
		// `Maybe<Result<Int, ?9>>`, and the outer Some reported
		// `non-scalar type argument` purely because projecting the inner Result
		// answered kindInvalid — one gap, two key names, on two lines of one
		// corpus file (08-pattern-matching/case_patterns_test.nomi:100).
		// The hole flag is discarded here on purpose: this is a TYPE
		// projection, not an expression at a position, so there is no want to
		// close it from. preludeWantedArgs is the reader that needs it.
		args[i], _ = g.preludeArgKind(arg)
		// kindInvalid: propagates — type projection; the caller rejects `non-scalar` by position.
		if args[i] == kindInvalid {
			return kindInvalid, true
		}
	}
	return g.preludeInstance(a, args), true
}
