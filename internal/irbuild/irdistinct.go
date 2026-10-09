package irbuild

// A wrapping distinct is a value the retained grammar admits, in both
// directions: built with `ir.Make` and read with `ir.Proj`'s `ProjInner`.
//
// No new instruction class is needed. `irProjExpr`'s `ProjInner` arm is how a
// destructuring parameter's names are read, and `internal/vm`'s `projValue`
// answers `ProjInner` off a distinct's record; `ir.Make` builds one.
//
// # One level
//
// `scalarUnwrap` states it for the unwrap direction: `Int`, `Float` and
// `String` unwrap one level and answer the inner WHATEVER that is, so for
// `type A Int` and `type B A`, `Int(b)` yields an `A` and not an Int.
// Requiring the inner to BE one of the four scalars declines the two-step
// shape rather than emitting a conversion Nomi does not perform.
// `irRetainedLeafKind` keeps that rule, so the retained grammar and
// `scalarUnwrap` admit the same set.
//
// A MARKER IS NOT A CONSTRUCTION. `type Expired` has no inner at all —
// `d.inner` is `kindInvalid`, which `zeroSizedOn` reads as "no storage" — so it
// is an `ir.Const` (`irMarkerValue`, `ir.NewMarker`).
//
// # What the node carries, and the name it carries it under
//
// `ir.Make.Typ()` is a `*ir.Symbol` interned on the `*typeDef` POINTER, which
// is this package's identity for one declared type and is module-distinct by
// construction. The NAME on that symbol is display-only by `ir.Type`'s and
// `ir.Decl`'s own rule — but a VM that builds a distinct has to put SOMETHING
// in its type name, and impl dispatch keys on it. A bare name is a silent
// wrong answer rather than a cosmetic one: every impl lookup misses. See
// irdistinct_identity_test.go.

import (
	"path/filepath"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// irWrappingDistinct reports whether d is a distinct type WRAPPING one of the
// four scalars: `type Meters Int`, `pub opaque type Duration Int`.
//
// A MARKER IS EXCLUDED and so is a distinct over anything else — see the file
// header on both. `lowerable` is asked because an unlowerable declaration has
// no representation on either side; `foreign` is not asked, because a
// mirror's `*typeDef` is a different pointer for the same Nomi type and
// therefore a different `ir.Symbol`, which is the identity question
// irdistinct_identity_test.go asks rather than this predicate.
func irWrappingDistinct(d *typeDef) bool {
	return d != nil && d.isDistinct && d.lowerable && irScalarLeafKind(d.inner)
}

// irCompositeDistinct reports whether d is a distinct over a tuple, a list
// or map of retained values, a function, or a declared type the VM carries:
// `type Coord (Int, Int)`, `type Headers Map<String, String>`, `type
// Callback (String) -> String`, `type Wrapped Maybe<Int>`. The VM holds one
// as a distinct record over its inner value, which `ir.Make`'s MakeDistinct
// builds and `ProjInner` reads, as for a scalar inner in a boxed position.
func irCompositeDistinct(d *typeDef) bool {
	return irCompositeDistinctIn(d, nil)
}

// irCompositeDistinctIn is irCompositeDistinct inside a walk that is already
// deciding the types on outer, as irRetainedStructKindIn is for a struct.
func irCompositeDistinctIn(d *typeDef, outer []*typeDef) bool {
	if d == nil || !d.isDistinct || !d.lowerable || d.rtOpaque {
		return false
	}
	in := d.inner
	if in.tag != tagTuple && in.tag != tagMap && in.tag != tagList && in.tag != tagFunc && in.tag != tagNamed {
		return false
	}
	if deciding := append(outer[:len(outer):len(outer)], d); irKindReaches(in, deciding) {
		// `type Link Node` where Node holds a `Maybe<Link>`: the inner is
		// decided with the walk's stack, co-inductively, as a struct field
		// that reaches its struct back is (irRecursiveFieldKind). The
		// stack-less predicates below would re-enter this one without end.
		return irRecursiveFieldKind(in, deciding)
	}
	return irRetainedTupleKind(in) || (in.tag == tagMap && in != kindEmptyMap && irRetainedMapKind(in)) ||
		(in.tag == tagList && in != kindEmptyList && irRetainedValueKind(in)) ||
		(in.tag == tagFunc && irCallableValueKind(in)) || irNominalInner(in)
}

// irDistinctCoinductive decides a distinct reached inside a walk: one the
// walk is already deciding (on outer) is assumed retained, as
// irNominalCoinductive assumes a struct or enum.
func irDistinctCoinductive(k kind, outer []*typeDef) bool {
	for _, o := range outer {
		if o == k.def {
			return true
		}
	}
	return irWrappingDistinct(k.def) || irCompositeDistinctIn(k.def, outer)
}

// irEmbedsValueDistinct reports whether v is an `embeds` of a distinct over
// a value, scalar (`type UserId Int`) or composite (`type Wrapped Circle`):
// the variant is built from the inner value and its pattern binds it.
func irEmbedsValueDistinct(v *variantDef) bool {
	return v != nil && v.kind == "embedded" && v.embeds != nil && !v.embeds.rtOpaque && irDistinctOverValue(v.embeds)
}

// irNominalInner reports whether a distinct's inner is itself a declared
// type the VM carries: a struct, an enum (`type Wrapped Maybe<Int>`), or
// another distinct (`type Twice Meters`), or a std Set or Vector (`type Ids
// Set<Int>`). The distinct record holds that value as it holds a tuple.
func irNominalInner(in kind) bool {
	if in.tag != tagNamed || in.def == nil {
		return false
	}
	if _, isSet := setElem(in); isSet {
		return in != kindEmptySet && irRetainedSetKind(in)
	}
	if _, isVector := vectorElem(in); isVector {
		return in != kindEmptyVector && irRetainedValueKind(in)
	}
	n := in.def
	return irRetainedStructKind(n) || irRetainedEnumKind(n) || irWrappingDistinct(n) || irCompositeDistinct(n)
}

// irCallableDistinct is a distinct over a function type, `type Callback
// (String) -> String`, whose values are called like the function.
func irCallableDistinct(k kind) bool {
	return k.tag == tagNamed && irCompositeDistinct(k.def) && k.def.inner.tag == tagFunc
}

// irDistinctOverValue is a distinct whose inner value the builder reads and
// writes: a scalar-wrapping one or a composite one.
func irDistinctOverValue(d *typeDef) bool {
	return irWrappingDistinct(d) || irCompositeDistinct(d)
}

// irRetainedLeafKind is the value domain the retained shape admits: the four
// scalars, Infallible, scalar-wrapping distincts and zero-sized distinct
// markers.
//
// SEPARATE FROM `irScalarLeafKind` RATHER THAN A WIDENING OF IT, because the
// two answer different questions and three callers want the narrow one.
// `irArithKind` is defined over the arithmetic DOMAINS and `rt` has a shape
// per operator per domain, so `binary` and `unary` still ask the scalar
// predicate; an interpolation hole asks it because `displayRendering` has a
// refusing arm for every other kind; and `bl.call`'s result asks it.
func irRetainedLeafKind(k kind) bool {
	if irScalarLeafKind(k) || k == kindNever {
		return true
	}
	return k.tag == tagNamed && (irWrappingDistinct(k.def) || irRetainedMarker(k.def))
}

// distinctMake lowers `Meters(5)` — a wrapping distinct's one constructor —
// as an `ir.Make`.
//
// IT RESOLVES THE NAME AND NOT THE NODE'S REFUSAL PATH. `distinctCallNamed`
// is the lowering's owner for this call and it REPORTS — `marker
// construction`, `call arity`, `argument type mismatch`, an unlowerable
// declaration — which this must not do a second time: the builder declines,
// the caller falls back to `blockInto`, and `gen.call` reaches the same owner
// and reports there. So every check here is a decline and none is a reject.
//
// THE TUPLE-FLAT ARM IS NOT REPRODUCED. `Coord(3, 4)` writes a tuple inner
// flat, which is the other legal arity; a tuple inner is not a scalar so
// `irWrappingDistinct` has already declined the declaration.
func (bl *irScalarBuilder) distinctMake(t *ast.Call, ti *ast.TypeIdent) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	d, found := bl.g.namedType(ti.Name)
	if found && irCompositeDistinct(d) && namedArgNode(t.Args) == nil {
		return bl.compositeDistinctMake(t, d)
	}
	if !found || !irWrappingDistinct(d) || len(t.Args) != 1 {
		return no()
	}
	src, k, pure, ok := bl.lower(t.Args[0])
	if !ok || k != d.inner {
		// The declared inner is what the constructor takes. A mismatch is
		// `distinctCallNamed`'s `argument type mismatch` and is reported
		// there.
		return no()
	}
	m := ir.NewMakeDistinct(bl.g.irNodePos(t), bl.f.NewTemp(), bl.g.irTypeSym(d), src)
	bl.b.Append(m)
	out := named(d)
	bl.side(m.Dst(), irScalarSide{k: out, pureMake: pure})
	return m.Dst(), out, pure, true
}

// compositeDistinctMake lowers `Coord(3, 4)` and `Coord((3, 4))` over a tuple
// inner, and `Headers({...})` over a map inner: the inner value, built at the
// declared inner type, wrapped by MakeDistinct. A tuple inner written flat
// is one argument per component.
func (bl *irScalarBuilder) compositeDistinctMake(t *ast.Call, d *typeDef) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	var src ir.Temp
	var ok bool
	var k kind
	flat := d.inner.tag == tagTuple && len(t.Args) == len(d.inner.comp.parts) && len(t.Args) > 1
	switch {
	case flat:
		values := make([]ir.Temp, len(t.Args))
		for i, a := range t.Args {
			v, vk, _, vok := bl.lowerWant(a, d.inner.comp.parts[i])
			if !vok || vk != d.inner.comp.parts[i] {
				return no()
			}
			values[i] = v
		}
		n := ir.NewMakeTuple(bl.g.irNodePos(t), bl.f.NewTemp(), values)
		bl.b.Append(n)
		bl.side(n.Dst(), irScalarSide{k: d.inner})
		src, k, ok = n.Dst(), d.inner, true
	case len(t.Args) == 1:
		src, k, _, ok = bl.lowerWant(t.Args[0], d.inner)
	}
	if !ok || k != d.inner {
		return no()
	}
	m := ir.NewMakeDistinct(bl.g.irNodePos(t), bl.f.NewTemp(), bl.g.irTypeSym(d), src)
	bl.b.Append(m)
	out := named(d)
	bl.side(m.Dst(), irScalarSide{k: out})
	return m.Dst(), out, false, true
}

// distinctInner lowers `Int(d)` — a wrapping distinct's unwrap — as an
// `ir.Proj` of kind `ProjInner`.
//
// THE CALLEE SET IS A CLOSED VOCABULARY and not "the scalars": `Int`,
// `Float` and `String` unwrap; `Bool` is deliberately absent, so `Bool(b)` is
// not spellable at all. `scalarUnwrap` reads that as a closed vocabulary and
// this reproduces it rather than deriving one.
//
// A LOCALLY DECLARED `Int` SHADOWS THE BUILTIN, which is `scalarUnwrap`'s own
// first check: a module that declares its own `Int` is `distinctCall`'s
// business, so a name that resolves as a type here is not an unwrap.
//
// `parenType` is false and passed explicitly: this is `scalarUnwrap`'s
// spelling, and the `embeds` coercion through `convertTo` is the other.
func (bl *irScalarBuilder) distinctInner(t *ast.Call, ti *ast.TypeIdent) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	var want kind
	switch ti.Name {
	case "Int":
		want = kindInt
	case "Float":
		want = kindFloat
	case "String":
		want = kindString
	default:
		return no()
	}
	if _, shadowed := bl.g.namedType(ti.Name); shadowed || len(t.Args) != 1 {
		return no()
	}
	src, k, pure, ok := bl.lower(t.Args[0])
	if !ok || k.tag != tagNamed || !irWrappingDistinct(k.def) || k.def.inner != want {
		return no()
	}
	return bl.distinctProjection(t, src, k, false, "irdistinct.go distinctInner"), want, pure, true
}

func (bl *irScalarBuilder) distinctProjection(at ast.Node, src ir.Temp, k kind, parenType bool, site string) ir.Temp {
	want := k.def.inner
	p := ir.NewProjInner(bl.g.irNodePos(at), bl.f.NewTemp(), src,
		bl.g.irTypeSym(k.def), irParamShape(want))
	bl.b.Append(p)
	if irProjKindObserved != nil {
		irProjKindObserved(site, p, want)
	}
	bl.side(p.Dst(), irScalarSide{k: want})
	return p.Dst()
}

// irTypeSym interns a declared type as an `ir.Symbol`.
//
// ONE SITE FOR EVERY `*typeDef` SYMBOL IN THIS PACKAGE, and that is a
// requirement rather than tidiness. `ir.Table.Symbol` interns on the token,
// so the FIRST call for a declaration decides the name every later caller
// sees: two sites passing two spellings would make the name depend on which
// node the lowering built first (for `duration.Duration.as_hours`,
// `irProjInner`'s prologue interns before the body does).
//
// CALLED FROM THE BUILDER TOO, which is the one thing `irScalarBuilder` does
// to the gen that is not read-only. Interning is idempotent, so a DISCARDED
// build leaves at most one interned symbol for the type, a table entry
// nothing reads, and `Table.Symbols()` is not a pinned number. Minting into
// the FUNCTION's namespace instead, the way a local's symbol is minted, would
// make two functions' `Duration` two identities.
func (g *gen) irTypeSym(d *typeDef) *ir.Symbol {
	return g.irTypes().Symbol(d, g.irTypeSymName(d))
}

// Shared stdlib enum identities come from their declaring file, so a value
// constructed by one stdlib implementation matches in another file.
// irTypeSymName is the name a declared type's `ir.Symbol` carries: the
// MODULE-QUALIFIED Nomi spelling, which is the one runtime values key on.
//
// A SYMBOL'S IDENTITY IS ITS TOKEN AND ITS NAME IS FOR READING — `ir.Type`
// and `ir.Decl` both say so — and that stays true. But the VM has to WRITE
// that name into a value: a record carries its type name, impl dispatch,
// `Hash` and `Equal` key on it, and a std distinct is stamped
// `duration.Duration` rather than `Duration`. So the name is also the
// identity as far as runtime values are concerned, and a bare one is a silent
// wrong answer: a value without it misses every impl and `==` falls back to
// structural comparison, with no error. Nothing else would catch a bare name:
// no emitted text reads a symbol's name. irdistinct_identity_test.go holds
// every identity qualified.
//
// THE QUALIFIER IS THE DECLARING MODULE'S AND NOT THIS GEN'S. A MIRROR — a
// `*typeDef` this gen synthesized for another file's declaration, `foreign`
// non-empty — would otherwise be stamped with the module doing the importing,
// which is two names for one Nomi type and is the shape `mintTypeIDs` skips a
// mirror to avoid. `hasTID` answers from the OWNER's table for the same reason.
//
// NOT `qualifiedNomiName` UNCHANGED, because that function reads `g.nomiPath`
// and has no mirror arm; its own callers only ever ask about a type this gen
// declares (`mintTypeIDs` filters `foreign` out first). Sharing the base-name
// rule and adding the arm is what keeps a TypeID's spelling and a Symbol's
// spelling the same string for the same type.
func (g *gen) irTypeSymName(d *typeDef) string {
	if i, ok := stdEnumDef(d); ok {
		spec := stdEnumSpecs[i]
		return irModuleQualifier(spec.origin) + "." + spec.nomi
	}
	if i, ok := stdStructIndex(d); ok {
		spec := stdStructSpecs[i]
		return irModuleQualifier(spec.origin) + "." + spec.nomi
	}
	if d.preludeOf != nil {
		spec := d.preludeOf.spec
		return irModuleQualifier(spec.origin) + "." + spec.nomi
	}
	if spec, ok := namedPayloadSpecOf(d); ok {
		// `std/tasks.Failure`: one process-wide typeDef, as a stdEnumSpecs
		// row is, named by its declaring module.
		return irModuleQualifier(spec.origin) + "." + spec.nomi
	}
	if spec := d.genStructOf; spec != nil {
		// A std generic struct instance (`Generator<Int>`) is its
		// declaration: it renders as `Generator{...}`, and d.nomi
		// spells the arguments only to key the instance.
		return irModuleQualifier(spec.origin) + "." + spec.nomi
	}
	// A std opaque or distinct with an rt Go type (`Codepoint`, `Duration`)
	// is one process-wide typeDef that every gen shares and none declares,
	// so the gen's own qualifier would name it after whichever module uses
	// it: `main.Codepoint`. Its declaring module is the spec's.
	for i, od := range opaqueDefs() {
		if od == d {
			spec := opaqueSpecs[i]
			return irModuleQualifier(spec.origin) + "." + spec.nomi
		}
	}
	// An origin-identified std `host type` (`Regex`, `Context`) is likewise
	// one process-wide typeDef, named by its declaring module: a host handle's
	// TypeName (`regex.Regex`) is what the VM dispatches its Debug impl on.
	for i, hd := range stdHostDefs() {
		if hd == d && stdHostSpecs[i].origin != "" {
			spec := stdHostSpecs[i]
			return irModuleQualifier(spec.origin) + "." + spec.nomi
		}
	}
	if d.foreign != "" {
		if mod := irModuleQualifier(d.foreign); mod != "" {
			return mod + "." + d.nomi
		}
		return d.nomi
	}
	return g.qualifiedNomiName(d.nomi)
}

// irModuleQualifier is the module name a Nomi file key qualifies with.
func irModuleQualifier(key string) string {
	return strings.TrimSuffix(filepath.Base(key), ".nomi")
}
