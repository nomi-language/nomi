package irbuild

import (
	"reflect"
	"strings"
	"sync"

	"github.com/nomi-language/nomi/rt"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// A MONOMORPHIC stdlib enum, as a Go type declared in rt:
// `std/comparable.Ordering` and the rows in stdEnumSpecs beside it.
//
// # Not the same population as prelude.go, and not the same as opaque.go
//
// prelude.go's subjects are GENERIC (`Maybe<T>`, `Result<T, E>`): a mention
// carries type arguments, so its *typeDef is INTERNED PER GEN on the rendered
// Go type unless its arguments are package-neutral (stdprelude.go).
// opaque.go's subjects are DISTINCT types over a scalar.
//
// These are neither: a monomorphic ENUM. The mechanism is opaque.go's line for
// line — a PROCESS-WIDE *typeDef, anchored per module by the analyzer's own
// (Origin, Name) rule with
// the declaration's SHAPE validated, and `rtDeclared` so nothing emits a second
// Go declaration for it.
//
// Sharing the def is not an optimization, for the same reason opaque.go's
// comment gives: a stdFunc's parameter and result kinds are built once in
// stdCandidateFor and compared by POINTER against a call site's kinds in a
// different gen, so a per-gen shell would make `Int.compare(a, b)` mismatch
// against its own declared return type. And it is SAFE to share because every
// payload is package-neutral (see stdEnumVariantSpec), which is the
// precondition foreign.go's rule against shared defs exists to protect.
// TestStdEnumDefsArePackageNeutral is the guard.
//
// # What Ordering buys that its own sites do not
//
// `<`, `>`, `<=`, `>=` desugar to `Comparable.compare(a, b)` followed by an
// Ordering match, and Comparable is the STRONG operator demand: `<` on a type
// with no `Comparable` impl is a compile-time missing-impl error, unlike `==`,
// which falls back to structural equality. So the type is the whole ordering
// surface of the language, not one enum — see compareCall in native.go.

// stdEnumSpec is one monomorphic stdlib enum: what this builder believes std
// declares, and which Go type in rt it is.
type stdEnumSpec struct {
	// origin is the declaring file's build key, the analyzer's own identity
	// half. See analysis.EnumType.Origin.
	origin string
	// nomi is the type's Nomi name, the other half.
	nomi string
	// goType is rt's Go type, held as the TYPE rather than as its spelling for
	// the reason opaqueSpec.goType is: a rename or a deletion in rt is then a
	// Go compile error in this file, and the pairing is checked by reflection in tests.
	goType reflect.Type
	// tagField is the exported Go field holding the variant tag.
	tagField string
	// variants are the declared variants in DECLARATION ORDER, which is what
	// fixes the tags: variant i gets tag i+1, and 0 stays reserved invalid. rt
	// declares the same tags as named constants so the pairing is checkable by
	// eye against the std source; TestStdEnumTagsMatchRT asserts the two agree,
	// because this list is the only thing the Go-spelled composite literals are
	// derived from.
	variants []stdEnumVariantSpec
}

// stdEnumVariantSpec is one variant: its name, the payload it carries, and the
// shape std's declaration of that payload must have.
//
// # A payload need not be a scalar
//
// Every payload kind must be PACKAGE-NEUTRAL, so the def stays shareable for
// stdStructSpec's reason. A scalar is the strongest form of package-neutral,
// not the only one: `kind.packageNeutral` also answers true for a `tagNamed`
// whose def is `rtDeclared`, and sharedcomp.go gives a structural kind over
// neutral components PROCESS-WIDE identity. So `*rt.List[rt.Json]` and
// `rt.Map[string, rt.Json]` are neutral, as they are for stdstruct.go's
// `kindOf`.
//
// What a non-scalar payload costs is BOOTSTRAP ORDER when it is
// SELF-REFERENTIAL, which `std/json.Json` is: its payload kinds cannot be built
// until its def exists. stdEnumDefs below is TWO passes for that (every shell
// first, every payload second), which is stdStructDefs' arrangement.
//
// TestStdEnumDefsArePackageNeutral asserts the precondition per slot. A payload
// the predicate answers false for (a `List<Point>` over some gen's own type)
// is not admitted, and the answer is a refusal rather than a wider table.
type stdEnumVariantSpec struct {
	nomi string
	// form is what the variant carries. payloadBare is the zero value, so a row
	// that states nothing carries nothing.
	form stdEnumPayloadForm
	// scalar is the payload kind when form is payloadScalar or
	// payloadStructScalar, and kindInvalid otherwise.
	scalar kind
	// ref is the stdEnumSpecs INDEX a container payload's element names, for the
	// two container forms, and -1 otherwise.
	//
	// Positional rather than by name for stdEnumOrdering's reason, and pinned by
	// TestStdEnumSpecIndicesNameTheirRow so a row inserted above it fails by
	// name rather than silently repointing the payload at another enum. A row
	// may name ITSELF, which is the whole point: `Json.Arr` is `List<Json>`.
	ref int
	// field is the exported Go field in rt the payload is stored in, empty for
	// a bare variant.
	//
	// Several variants may name the SAME field, and that is the builder's own
	// dedup rule rather than a shortcut: one slot per distinct underlying Go
	// type, reused across variants because only one variant is live at a time
	// (types.go's assignSlots). calendar.Error's five variants all carry a
	// String, so rt.CalendarError has one `Msg` field for all five.
	field string
	// nomiField is the NOMI field name a payloadStructScalar variant declares,
	// empty for every other form.
	//
	// Separate from `field` above, which is rt's Go field, because the two are
	// answering different questions and conflating them would be wrong in both
	// directions: `field` is deduped across variants (five calendar variants
	// share `Msg`) while `nomiField` is what the programmer's pattern binds, and
	// rt's Go names are exported and idiomatic while Nomi's are snake_case.
	nomiField string
	// fields are the NAMED fields of a payloadStructFields variant, in
	// declaration order, and empty for every other form.
	//
	// A separate slice rather than a generalisation of `nomiField`/`field`
	// because the two forms differ in what they OWE, not only in arity: this
	// one states a per-field type that need not be a scalar and a per-field Go
	// default, and folding it into the singular pair would put two empty
	// strings on every existing row to describe a shape none of them has.
	fields []stdEnumFieldSpec
	// declType is the Nomi type NAME a payloadScalar variant's declaration must
	// spell when its payload is not one of the five scalars — `UpTo Duration`.
	// Empty for a payload the `scalarKind` comparison can check by itself.
	//
	// Its own field rather than a reuse of `nomiField`, which is empty for
	// every positional row and would therefore have been available: the two
	// answer different questions, and a reader who found a TYPE name in a field
	// documented as "the NOMI field name a struct-shaped variant declares"
	// would be right to distrust everything around it.
	declType string
}

// stdEnumPayloadForm is the closed set of payload shapes this family admits.
//
// A DESCRIPTOR rather than a pair of closures, and that shape is forced rather
// than preferred. A closure calling `listKindIn` would be reachable from
// `stdEnumSpecs`'s own initializer, and `listKindIn` reaches `packageNeutral`
// reaches `stdIfaceSpecs` reaches `stdEnumKind` reaches `stdEnumDefs` reaches
// `stdEnumSpecs` — an initialization cycle the Go compiler rejects by name. That
// is a real fact about this package's tables and not an artifact: the neutrality
// predicate consults the interface family, which is built over enum kinds. So the
// table states WHAT each payload is and the two functions below say what that
// means, which keeps every table row free of function references.
//
// Closed on purpose: a form this switch does not know is a compile error at both
// functions rather than a silent bare variant.
type stdEnumPayloadForm uint8

const (
	// payloadBare carries nothing and gets no slot.
	payloadBare stdEnumPayloadForm = iota
	// payloadScalar carries one of the five scalars.
	payloadScalar
	// payloadListOfEnum carries `List<the spec at ref>`.
	payloadListOfEnum
	// payloadMapStringToEnum carries `Map<String, the spec at ref>`.
	payloadMapStringToEnum
	// payloadStructScalar carries ONE NAMED field of one of the five scalars —
	// the `NotFound {path: String}` shape.
	//
	// The Nomi field NAME is part of the representation here in a way no other
	// form's is, and that is what makes this a separate form rather than
	// payloadScalar with a flag. A positional payload is reached by position, so
	// `IOError.NotFound(p)` and `Error.InvalidValue(m)` bind identically; a
	// struct-shaped one is reached by NAME, and `case e { IOError.NotFound{path}
	// -> … }` binds `path` because the declaration spells it `path`. So the spec
	// has to state the Nomi name, the shape check has to hold std's declaration
	// to it, and the variantDef has to carry it — three places a positional row
	// leaves empty.
	//
	// ONE FIELD, not N. Nothing in the def machinery restricts it (variantDef's
	// payloads is a slice and types.go's struct-variant path reads all of them),
	// but a second field is a second slot and a second dedup decision, and the
	// only declaration reaching THIS form wants one. payloadStructFields below
	// is the wider form; this one stays as it is because its members declare no
	// default and gain nothing from the wider machinery.
	payloadStructScalar
	// payloadStructFields carries N NAMED fields whose types need not be
	// scalars and which MAY carry defaults — the
	// `Exponential {max_restarts: Int = 10, max_elapsed: Duration = …}` shape.
	//
	// THREE THINGS payloadStructScalar refuses and this form admits, each a
	// separate decision:
	//
	//  1. MORE THAN ONE FIELD, which is the widening payloadStructScalar's own
	//     note invited and which costs only a loop.
	//  2. A NON-SCALAR FIELD TYPE. `max_elapsed: Duration` is an opaque std
	//     newtype, so the field states an `opaqueSpecs` INDEX rather than a
	//     kind. An index rather than a kind value for the reason `ref` is one:
	//     a `kind` in this table's initializer would call `opaqueKind`, and a
	//     row holding a function reference is what stdEnumPayloadForm's own
	//     header says produced an initialization cycle.
	//  3. A DEFAULT, which payloadStructScalar rejects outright with a stated
	//     reason — "a default is Nomi source belonging to the declaring module,
	//     and a shared def has no gen to lower it in". THAT REASON IS CORRECT
	//     AND THIS FORM DOES NOT WEAKEN IT: the default here is a GO expression
	//     stated in the spec (`goDeflt`), lowered nowhere, so no gen is asked to
	//     lower std's Nomi source. What the spec then owes is that the two
	//     encodings agree, and TestBackoffDefaultsMatchTheStdDeclaration reads
	//     std's declared default text and rt's constant and holds them equal —
	//     the same standard TestStdEnumTagsMatchRT applies to the tag numbering.
	//
	// The shape check is correspondingly stricter than the others: field COUNT,
	// field NAMES in order, each annotation, and the PRESENCE of a default
	// exactly where the spec says there is one. A std edit that added a third
	// field, renamed one, or dropped a default produces NO ANCHOR.
	payloadStructFields
)

// stdEnumFieldSpec is one NAMED field of a payloadStructFields variant.
//
// `nomi` is what the declaration spells and what a pattern binds; `field` is
// rt's exported Go field. Two names rather than one derived from the other, for
// structScalarVariant's reason: deriving `MaxRestarts` from `max_restarts`
// would be a naming rule this package would then own, and rt's struct is
// hand-written.
type stdEnumFieldSpec struct {
	nomi  string
	field string
	// scalar is the field kind when the field is one of the five scalars, and
	// kindInvalid when `opaque` names the type instead.
	scalar kind
	// opaque is the opaqueSpecs INDEX of a std newtype field, or -1. See
	// payloadStructFields' point 2 for why an index rather than a kind.
	opaque int
	// declType is the Nomi type name the declaration must spell for an opaque
	// field, checked against the annotation. Empty for a scalar field, whose
	// annotation is checked by `scalarKind` instead.
	declType string
	// goDeflt is the Go expression that fills this field when a literal omits
	// it, or "" for a field with no default.
	//
	// A STRING rather than a kind-tagged value, because it is spelled verbatim
	// into a composite literal and its only consumer is that spelling. Its
	// agreement with std's declared default is a TEST's job, not a type's.
	goDeflt string
}

// fieldKind is the kind this field holds.
func (f *stdEnumFieldSpec) fieldKind() kind {
	if f.opaque >= 0 {
		return opaqueKind(f.opaque)
	}
	return f.scalar
}

// bareVariant is the variant spec for a payload-less variant.
func bareVariant(name string) stdEnumVariantSpec {
	return stdEnumVariantSpec{nomi: name, form: payloadBare, scalar: kindInvalid, ref: -1}
}

// scalarVariant is the common carrying row: a payload whose kind is a fixed
// scalar, stored in rt's `field`.
func scalarVariant(name, field string, k kind) stdEnumVariantSpec {
	return stdEnumVariantSpec{nomi: name, form: payloadScalar, scalar: k, ref: -1, field: field}
}

// listOfEnumVariant is a payload holding `List<the enum spec at index i>`.
func listOfEnumVariant(name, field string, i int) stdEnumVariantSpec {
	return stdEnumVariantSpec{
		nomi:   name,
		form:   payloadListOfEnum,
		scalar: kindInvalid,
		ref:    i,
		field:  field,
	}
}

// mapOfEnumVariant is a payload holding `Map<String, the enum spec at index i>`.
//
// The KEY is fixed at String rather than being a parameter, because that is what
// std declares and a second key type would be a second decision nobody has made.
// A std edit that changed it produces no anchor.
func mapOfEnumVariant(name, field string, i int) stdEnumVariantSpec {
	return stdEnumVariantSpec{
		nomi:   name,
		form:   payloadMapStringToEnum,
		scalar: kindInvalid,
		ref:    i,
		field:  field,
	}
}

// structScalarVariant is a STRUCT-SHAPED payload of one named scalar field:
// `NotFound {path: String}`, stored in rt's `goField`.
//
// Both names are required and they are not interchangeable — `nomiField` is what
// std declares and what a pattern binds, `goField` is rt's exported field. Two
// arguments rather than one derived from the other, because deriving `Path` from
// `path` would be a naming rule this package would then have to own, and rt's
// struct is hand-written.
func structScalarVariant(name, nomiField, goField string, k kind) stdEnumVariantSpec {
	return stdEnumVariantSpec{
		nomi:      name,
		form:      payloadStructScalar,
		scalar:    k,
		ref:       -1,
		field:     goField,
		nomiField: nomiField,
	}
}

// structFieldsVariant is a STRUCT-SHAPED payload of N named fields, each
// possibly non-scalar and each possibly defaulted:
// `Exponential {max_restarts: Int = 10, max_elapsed: Duration = …}`.
//
// See payloadStructFields for what this admits that structScalarVariant does
// not, and for why the defaults are Go expressions rather than Nomi ones.
func structFieldsVariant(name string, fields ...stdEnumFieldSpec) stdEnumVariantSpec {
	return stdEnumVariantSpec{
		nomi:   name,
		form:   payloadStructFields,
		scalar: kindInvalid,
		ref:    -1,
		fields: fields,
	}
}

// scalarField and opaqueField are the two field shapes structFieldsVariant
// admits. `goDeflt` is "" for a field with no default.
func scalarField(nomi, goField string, k kind, goDeflt string) stdEnumFieldSpec {
	return stdEnumFieldSpec{nomi: nomi, field: goField, scalar: k, opaque: -1, goDeflt: goDeflt}
}

// opaqueField names a std newtype by its opaqueSpecs INDEX and by the Nomi
// spelling the declaration must use. Both, because the index decides the KIND
// and the spelling is what the shape check holds std's annotation to — deriving
// one from the other would make a std rename anchor against a stale name.
func opaqueField(nomi, goField string, opaqueIdx int, declType, goDeflt string) stdEnumFieldSpec {
	return stdEnumFieldSpec{
		nomi: nomi, field: goField, scalar: kindInvalid,
		opaque: opaqueIdx, declType: declType, goDeflt: goDeflt,
	}
}

// carries reports whether this variant has a payload.
func (v *stdEnumVariantSpec) carries() bool {
	return v.form != payloadBare
}

// payloadKind is the kind this variant carries, given the def SHELL of every
// spec in the table.
//
// Takes the shells rather than calling stdEnumDefs() for stdStructField.kindOf's
// reason: a payload may name a def from the table being built — `Json.Arr` is a
// List over `Json` itself — and no def exists until the two-pass build has made
// every shell. Reading the slice keeps the dependency explicit and
// one-directional, so the OnceValue cannot re-enter itself.
//
// A container interns with a NIL gen, which internComp documents as the stdlib
// signature boundary: with every component package-neutral it lands in the
// PROCESS-WIDE table, so this is the SAME *compKind a call site's own
// `g.listKind` produces and the two compare equal. That equality is the whole
// requirement — a per-gen kind here would match no call site anywhere.
func (v *stdEnumVariantSpec) payloadKind(defs []*typeDef) kind {
	switch v.form {
	case payloadScalar, payloadStructScalar:
		return v.scalar
	case payloadListOfEnum:
		return listKindIn(nil, named(defs[v.ref]))
	case payloadMapStringToEnum:
		return mapKindIn(nil, kindString, named(defs[v.ref]))
	}
	// kindInvalid: marker — payloadBare, "this variant carries nothing".
	return kindInvalid
}

// declaredShapeOK reports whether std's declared payload ANNOTATION is the type
// payloadKind produces.
//
// Deliberately NOT routed through stdTypeKind, which is how
// stdStructSpec.matches checks a field, and the asymmetry is forced rather than
// chosen: stdTypeKind resolves a name through `stdAnchors`, and validating
// `Json.Arr List<Json>` needs the `Json` anchor that this very check is what
// builds. So the annotation is compared STRUCTURALLY. That is no weaker for the
// purpose — a widened `List<String>` fails on the element name exactly as it
// would fail on an interned kind — and it is self-contained, which breaks the
// cycle honestly instead of by ordering luck.
//
// The element type's name is DERIVED from the row `ref` names rather than retyped
// beside it, so a renamed row fails here instead of anchoring against a stale
// spelling.
func (v *stdEnumVariantSpec) declaredShapeOK(te ast.TypeExpr, specs []stdEnumSpec) bool {
	switch v.form {
	case payloadScalar:
		if v.declType != "" {
			// A payload whose type is an opaque std newtype. `scalarKind`
			// answers kindInvalid for one, so the NAME is what the declaration
			// is held to — and holding it to a name is no weaker here than
			// elsewhere in this function, which compares element names for both
			// container forms for the same reason.
			return simpleTypeName(te) == v.declType
		}
		return scalarKind(te) == v.scalar
	case payloadListOfEnum:
		gt, generic := te.(*ast.GenericType)
		if !generic || gt.Name != "List" || len(gt.Params) != 1 {
			return false
		}
		return simpleTypeName(gt.Params[0]) == specs[v.ref].nomi
	case payloadMapStringToEnum:
		gt, generic := te.(*ast.GenericType)
		if !generic || gt.Name != "Map" || len(gt.Params) != 2 {
			return false
		}
		return scalarKind(gt.Params[0]) == kindString &&
			simpleTypeName(gt.Params[1]) == specs[v.ref].nomi
	}
	// payloadBare: a bare variant has no annotation to accept.
	//
	// payloadStructScalar: a struct-shaped variant has FIELDS rather than a
	// single annotation, so there is no `te` to hand this function. `matches`
	// checks it through declaredStructShapeOK instead, and reaching here with
	// that form is a caller bug, not a wide declaration — which is why this
	// returns false rather than growing a case that could not be given the
	// field list.
	return false
}

// declaredStructShapeOK reports whether std's STRUCT-SHAPED variant declaration
// is the one this spec describes: exactly one field, spelled the way the spec
// says, annotated with the spec's scalar, and carrying NO DEFAULT.
//
// The default is rejected rather than honoured, and that is the same standard
// stdstruct.go applies to a defaulted stdlib field: a default is Nomi source
// belonging to the declaring module, and a shared def has no gen to lower it in.
// A std edit that gave `IOError.NotFound` a `path: String = ""` therefore
// produces no anchor and the type keeps refusing, which is a refusal rather than
// a silently-dropped initializer.
func (v *stdEnumVariantSpec) declaredStructShapeOK(fields []ast.StructField) bool {
	if v.form != payloadStructScalar || len(fields) != 1 {
		return false
	}
	f := fields[0]
	return f.Name == v.nomiField && f.Default == nil && scalarKind(f.TypeAnnotation) == v.scalar
}

// declaredStructFieldsOK reports whether std's N-FIELD variant declaration is
// the one this spec describes: the same fields, in the same order, with the
// same names, the same annotations, and a default present at exactly the
// positions the spec says have one.
//
// THE DEFAULT'S PRESENCE IS CHECKED IN BOTH DIRECTIONS, and that asymmetry with
// declaredStructShapeOK above is the point. That one rejects a default outright
// because it has nowhere to put it; this one HAS somewhere (the spec's own Go
// expression), so what it must detect is DISAGREEMENT: a std edit that dropped
// `= 10` would make `Backoff.Exponential{}` a program the front end rejects
// while this builder still filled a value, and one that ADDED a default to a
// field the spec thinks is required would make the two paths accept different
// programs.
//
// What it deliberately does NOT check is the default's VALUE, because the
// annotation is Nomi source and the spec's is Go. That agreement is
// TestBackoffDefaultsMatchTheStdDeclaration's, which can read both and compare
// them; here there is only a `!= nil`.
func (v *stdEnumVariantSpec) declaredStructFieldsOK(fields []ast.StructField) bool {
	if v.form != payloadStructFields || len(fields) != len(v.fields) {
		return false
	}
	for i := range v.fields {
		want, got := &v.fields[i], fields[i]
		if got.Name != want.nomi {
			return false
		}
		if (got.Default != nil) != (want.goDeflt != "") {
			return false
		}
		if want.opaque >= 0 {
			if simpleTypeName(got.TypeAnnotation) != want.declType {
				return false
			}
			continue
		}
		if scalarKind(got.TypeAnnotation) != want.scalar {
			return false
		}
	}
	return true
}

// stdEnumSpecs is the whole set, and every row has to be REACHED by something
// rather than merely be representable — a registry row for a type nothing can
// name is scaffolding. TestStdEnumSpecsAreReachable asserts it over the corpus.
//
// Reachability is judged over every key a type can be reached through, not
// one: `Direction` is reached as `Direction.Ascending` and through `==`.
//
// Several rows also keep the mechanism plural. A one-row table proves nothing
// about generality: the tags below are derived from each spec's own variant
// list, and with one spec a hardcoded `Less=1` would pass every test.
var stdEnumSpecs = []stdEnumSpec{{
	origin:   "std/comparable",
	nomi:     "Ordering",
	goType:   reflect.TypeFor[rt.Ordering](),
	tagField: "Tag",
	variants: []stdEnumVariantSpec{bareVariant("Less"), bareVariant("Equal"), bareVariant("Greater")},
}, {
	origin:   "std/comparable",
	nomi:     "Direction",
	goType:   reflect.TypeFor[rt.Direction](),
	tagField: "Tag",
	variants: []stdEnumVariantSpec{bareVariant("Ascending"), bareVariant("Descending")},
}, {
	// std/calendar's `Disambiguation`: how a wall reading in a DST gap or fold
	// resolves. Bare like the two above, and reached by four corpus files under
	// `type-qualified reference | Disambiguation.Earlier` and its siblings.
	origin:   "std/calendar",
	nomi:     "Disambiguation",
	goType:   reflect.TypeFor[rt.Disambiguation](),
	tagField: "Tag",
	variants: []stdEnumVariantSpec{
		bareVariant("Compatible"), bareVariant("Earlier"), bareVariant("Later"), bareVariant("Reject"),
	},
}, {
	// std/calendar's `Error`, and the first PAYLOAD-carrying row. Every variant
	// carries a String, so all five share one slot — the builder's own dedup
	// rule, and rt.CalendarError declares exactly one `Msg` field for them.
	//
	// It is here because `Result<DateTime, Error>` is the result type of every
	// fallible calendar constructor, so without it no calendar signature
	// projects at all.
	origin:   "std/calendar",
	nomi:     "Error",
	goType:   reflect.TypeFor[rt.CalendarError](),
	tagField: "Tag",
	variants: []stdEnumVariantSpec{
		scalarVariant("InvalidFormat", "Msg", kindString),
		scalarVariant("InvalidValue", "Msg", kindString),
		scalarVariant("UnknownZone", "Msg", kindString),
		scalarVariant("Nonexistent", "Msg", kindString),
		scalarVariant("Ambiguous", "Msg", kindString),
	},
}, {
	// std/dynamic's path segment. It is here because `DecodeError.path` is
	// `List<PathSegment>`, so without it that struct's field resolves to
	// nothing and the whole type refuses as `stdlib type |
	// std/dynamic.DecodeError`.
	//
	// The first row whose two payloads are DIFFERENT Go types. calendar.Error's
	// five variants all carry a String and share one `Msg` field; `Field String`
	// and `Index Int` cannot share, so rt declares one field each.
	origin:   "std/dynamic",
	nomi:     "PathSegment",
	goType:   reflect.TypeFor[rt.DynamicPathSegment](),
	tagField: "Tag",
	variants: []stdEnumVariantSpec{
		scalarVariant("Field", "Field", kindString),
		scalarVariant("Index", "Index", kindInt),
	},
}, {
	// std/decimal's `RoundingMode`. Bare like the first three rows, and the
	// widest at eight variants, which is the point: this table's tags are
	// derived from each spec's own variant ORDER, and a row that exercises more
	// of that arithmetic than two or three is worth having.
	//
	// Reached from 04-scalars-and-text/decimals/decimals_test.nomi and
	// 12-derives-and-standard-interfaces/display_debug_test.nomi. Naming a
	// rounding mode does not bring decimal arithmetic with it; that is the
	// `Decimal` representation's job. See rt/roundingmode.go.
	origin:   "std/decimal",
	nomi:     "RoundingMode",
	goType:   reflect.TypeFor[rt.RoundingMode](),
	tagField: "Tag",
	variants: []stdEnumVariantSpec{
		bareVariant("Up"), bareVariant("Down"), bareVariant("Ceiling"), bareVariant("Floor"),
		bareVariant("HalfUp"), bareVariant("HalfDown"), bareVariant("HalfEven"),
		bareVariant("Unnecessary"),
	},
}, {
	// std/strings' `NormalForm`, the argument `String.normalize` takes. Reached
	// by `type reference | NFC` (2 sites) and `NFD` (1) in
	// 04-scalars-and-text/strings_test.nomi and by `type-qualified reference |
	// NormalForm.NFC` in display_debug_test.nomi.
	//
	// The same scope line as RoundingMode and for a sharper reason: the row
	// makes the FORM nameable and says nothing about normalization, which is a
	// `host fn` with its own implementation. See rt/normalform.go.
	origin:   "std/strings",
	nomi:     "NormalForm",
	goType:   reflect.TypeFor[rt.NormalForm](),
	tagField: "Tag",
	variants: []stdEnumVariantSpec{
		bareVariant("NFC"), bareVariant("NFD"), bareVariant("NFKC"), bareVariant("NFKD"),
	},
}, {
	// std/json's `Json`, and the FIRST SELF-REFERENTIAL row in any of the
	// shared-def families: `Arr List<Json>` and `Obj Map<String, Json>` name the
	// type being declared.
	//
	// Placed after the rows it follows rather than beside a thematic neighbour,
	// and that is not a style choice. Rows here name each other POSITIONALLY
	// (the index constants below), so inserting mid-table repoints every index
	// silently.
	//
	// Without it `Json.decode`'s result type, `Result<Json, Json.DecodeError>`,
	// names something the stdlib signature boundary cannot represent, and it
	// would refuse as `stdlib function outside the scalar subset`.
	//
	// SIX PAYLOAD FIELDS AND NO SHARING, unlike calendar.Error's five variants
	// over one `Msg`: no two of these have the same Go type, so the dedup rule
	// allocates one slot each. rt.Json declares exactly those six plus the tag,
	// and TestStdEnumSlotsMatchRT holds the two lists against each other in both
	// directions.
	origin:   "std/json",
	nomi:     "Json",
	goType:   reflect.TypeFor[rt.Json](),
	tagField: "Tag",
	variants: []stdEnumVariantSpec{
		scalarVariant("String", "String", kindString),
		scalarVariant("Int", "Int", kindInt),
		scalarVariant("Float", "Float", kindFloat),
		scalarVariant("Bool", "Bool", kindBool),
		listOfEnumVariant("Arr", "Arr", stdEnumJson),
		mapOfEnumVariant("Obj", "Obj", stdEnumJson),
		bareVariant("Null"),
	},
}, {
	// std/io's `IOError`, and the FIRST STRUCT-SHAPED PAYLOAD in this family.
	//
	// After the Json row, for the reason that row states: rows name each other
	// POSITIONALLY through the index constants below.
	//
	// `Result<String, IOError>` and `Result<Unit, IOError>` are the result types
	// of `io.read_file` and `io.write_file`, so without it neither signature
	// projects.
	//
	// TWO PAYLOAD FIELDS AND NO SHARING, unlike calendar.Error's five variants
	// over one `Msg`: a struct-shaped payload's field name is part of the
	// declaration being anchored, so `path` and `reason` get one slot each.
	origin:   "std/io",
	nomi:     "IOError",
	goType:   reflect.TypeFor[rt.IOError](),
	tagField: "Tag",
	variants: []stdEnumVariantSpec{
		structScalarVariant("NotFound", "path", "Path", kindString),
		structScalarVariant("Other", "reason", "Reason", kindString),
	},
}, {
	// std/supervisors' `Restart`: whether a stopped supervised task runs again.
	// Three bare variants, so this row is the same shape as Direction's and
	// exercises no new machinery.
	//
	// After the rows above, for the reason the Json row states: rows name each
	// other POSITIONALLY and inserting mid-table repoints every index silently.
	//
	// Reached from 16-concurrency/message_loop/message_loop_test.nomi as
	// `Restart.Permanent`. The row makes the value nameable; supervision itself
	// is `Supervisor`'s. See rt/restart.go.
	origin:   "std/supervisors",
	nomi:     "Restart",
	goType:   reflect.TypeFor[rt.Restart](),
	tagField: "Tag",
	variants: []stdEnumVariantSpec{
		bareVariant("Temporary"), bareVariant("Transient"), bareVariant("Permanent"),
	},
}, {
	// std/supervisors' `GiveUp`: what happens at the moment the runtime decides
	// a task will not run again. Two bare variants.
	//
	// After the rows above, for the reason the Json row states. It is a
	// PARAMETER of `Supervisor.new` (`on_give_up: GiveUp = .Report`), so without
	// it that signature does not project, whatever the call spells: a signature
	// is refused by its WIDEST parameter, not by the arguments a caller passes.
	origin:   "std/supervisors",
	nomi:     "GiveUp",
	goType:   reflect.TypeFor[rt.GiveUp](),
	tagField: "Tag",
	variants: []stdEnumVariantSpec{bareVariant("Report"), bareVariant("Exit")},
}, {
	// std/supervisors' `Wait`: how long `Supervisor.flush` is willing to block.
	//
	// A union rather than a Duration with a magic "forever", which std records
	// as a decision: an unbounded wait is a different thing from a long one,
	// and giving Duration a sentinel would leak into every arithmetic and
	// comparison that touches it.
	//
	// `UpTo Duration` is a positional payload whose type is not a scalar. The
	// FORM is still payloadScalar: `payloadKind` returns `v.scalar` verbatim and
	// nothing downstream of it asks whether the kind is scalar, so an opaque
	// kind flows through the def builder unchanged. The SHAPE CHECK,
	// `declaredShapeOK`, compares `scalarKind(te)`, which is kindInvalid for
	// `Duration`, so the row states the declared type NAME and the check
	// compares that instead, as both container arms of the same function do.
	origin:   "std/supervisors",
	nomi:     "Wait",
	goType:   reflect.TypeFor[rt.Wait](),
	tagField: "Tag",
	variants: []stdEnumVariantSpec{
		bareVariant("Forever"),
		opaquePositionalVariant("UpTo", "UpTo", opaqueDuration, "Duration"),
	},
}, {
	// std/supervisors' `FlushOutcome`: how a `Supervisor.flush` ended. Two bare
	// variants, and the RESULT type of `flush`, so the same
	// widest-position rule applies as for GiveUp.
	origin:   "std/supervisors",
	nomi:     "FlushOutcome",
	goType:   reflect.TypeFor[rt.FlushOutcome](),
	tagField: "Tag",
	variants: []stdEnumVariantSpec{bareVariant("Flushed"), bareVariant("TimedOut")},
}, {
	// std/supervisors' `Backoff`: WHEN a stopped task runs again and how many
	// times, which is a separate question from Restart's WHETHER.
	//
	// `Exponential` is a struct-shaped variant of TWO named fields carrying
	// DEFAULTS, represented through payloadStructFields with a non-scalar field
	// type and a Go-stated default.
	//
	// `backoff: Backoff = .Exponential{}` is a PARAMETER of `Supervisor.new`, so
	// without this row that signature refuses for every caller, including one
	// that never spells a schedule.
	origin:   "std/supervisors",
	nomi:     "Backoff",
	goType:   reflect.TypeFor[rt.Backoff](),
	tagField: "Tag",
	variants: []stdEnumVariantSpec{
		structFieldsVariant("Exponential",
			scalarField("max_restarts", "MaxRestarts", kindInt, "rt.BackoffDefaultMaxRestarts"),
			opaqueField("max_elapsed", "MaxElapsed", opaqueDuration, "Duration", "rt.BackoffDefaultMaxElapsed"),
		),
	},
}, {
	// std/random's `Error`. Here for CalendarError's reason and the same shape of
	// dependency: every fallible `std/random` constructor returns
	// `Result<Generator<T>, Error>`, so with `Generator<T>` represented and this
	// not, no random signature projects at all.
	//
	// THE WIDEST ROW BY PAYLOAD SHAPE, and deliberately so: the first to mix
	// four forms in one enum — two payloadStructFields, two payloadStructScalar,
	// one bare. This table's header says a one-row mechanism proves nothing about
	// generality; the same is true per FORM.
	//
	// Its slots do not collapse the way CalendarError's five do; see
	// rt/randomerror.go for the layout and why Int and Float cannot share.
	origin:   "std/random",
	nomi:     "Error",
	goType:   reflect.TypeFor[rt.RandomError](),
	tagField: "Tag",
	variants: []stdEnumVariantSpec{
		structFieldsVariant("InvalidIntRange",
			scalarField("from", "From", kindInt, ""),
			scalarField("to", "To", kindInt, ""),
		),
		structFieldsVariant("InvalidFloatRange",
			scalarField("from", "FloatFrom", kindFloat, ""),
			scalarField("to", "FloatTo", kindFloat, ""),
		),
		structScalarVariant("InvalidWeight", "weight", "Weight", kindFloat),
		bareVariant("ZeroWeightTotal"),
		structScalarVariant("OsEntropy", "reason", "Reason", kindString),
	},
}}

// opaqueDuration indexes the `Duration` row of opaqueSpecs, which two variant
// payloads above name as their type.
//
// An index rather than a name lookup, for stdEnumOrdering's reason, and pinned
// by TestStdEnumSpecIndicesNameTheirRow so a row inserted above it in
// opaqueSpecs fails by name rather than silently repointing both payloads at
// `Instant` — which is the same int64 underneath and would therefore compile.
const opaqueDuration = 0

// opaquePositionalVariant is scalarVariant for a payload whose type is an
// opaque std newtype rather than one of the five scalars.
//
// The FORM is still payloadScalar, because `payloadKind` returns `v.scalar`
// verbatim and nothing downstream of it cares whether the kind is scalar. What
// had to move is the SHAPE CHECK: `declaredShapeOK`'s payloadScalar arm compares
// `scalarKind(te)`, which answers kindInvalid for `Duration`. So the spec
// carries the declared type NAME and the check compares that instead.
func opaquePositionalVariant(name, goField string, opaqueIdx int, declType string) stdEnumVariantSpec {
	return stdEnumVariantSpec{
		nomi:     name,
		form:     payloadScalar,
		scalar:   opaqueKind(opaqueIdx),
		ref:      -1,
		field:    goField,
		declType: declType,
	}
}

// stdEnumOrdering indexes the Ordering spec, for the two call sites that need
// that particular type rather than any anchored one: the `<` lowering and its
// tests. An index rather than a name lookup, so a rename in the spec is a
// compile error here.
const stdEnumOrdering = 0

// stdEnumDirection indexes std/comparable's Direction spec, the optional
// operand of `Iter.sort` and `Iter.sort_by`.
const stdEnumDirection = 1

// stdEnumPathSegment indexes the PathSegment spec, which std/dynamic's
// `DecodeError.path` names as its element type. An index rather than a name
// lookup for stdEnumOrdering's reason, and pinned by
// TestStdEnumSpecIndicesNameTheirRow so a row inserted above it fails by name
// rather than silently repointing the field at another enum.
const stdEnumPathSegment = 4

// stdEnumJson indexes the Json spec, which its OWN `Arr` and `Obj` payloads name
// as their element type. An index rather than a name lookup for
// stdEnumOrdering's reason, and pinned by TestStdEnumSpecIndicesNameTheirRow.
//
// A self-reference through a constant rather than through a `self` marker on the
// constructor, because the mechanism is not special to self-reference: a payload
// may name ANY row, exactly as stdstruct.go's stdListField does, and Json
// happens to name its own. A dedicated `self` spelling would have made the one
// case that exists read as a feature and the general case unavailable.
const stdEnumJson = 7

// stdEnumDefs is the process-wide *typeDef per spec, keyed by spec index.
//
// Built once for the process, exactly like opaqueDefs, and for the same
// pointer-identity reason. Tags are assigned from the spec's variant order and
// nowhere else, so there is one place the numbering exists.
//
// TWO PASSES, because a payload may name another row — or its OWN. Every shell
// exists before any payload kind is resolved, so `Json.Arr` can be a List over
// the `Json` def without that def having to be finished first. A spec's
// payloadOf reads THIS slice and never stdEnumDefs(), so the OnceValue cannot
// re-enter itself. That is stdStructDefs' arrangement, adopted here rather than
// re-derived: the property both need is that a def's IDENTITY is fixed by its
// shell while its CONTENTS may reference any identity in the table.
//
// The shell carries everything the identity depends on — the Nomi name, the Go
// type, and `rtDeclared`, which is what makes `named(shell)` package-neutral and
// therefore what makes `listKindIn(nil, …)` intern the payload PROCESS-WIDE
// rather than declining. So the ordering is load-bearing in a second way: a
// shell missing `rtDeclared` would not fail here, it would silently produce a
// payload kind interned in no gen, which no call site could ever match.
//
// Slots are built from the SPEC's own `field` names rather than by assignSlots,
// which numbers them `P0`, `P1`, … — rt's struct is hand-written and read by
// people, so its field is `Msg`. The dedup is therefore the spec's to state and
// this loop's to honour: two variants naming one field share one slot, and
// TestStdEnumSlotsMatchRT holds the result against the Go type by reflection so
// a spec that claimed a field rt does not declare fails rather than emitting a
// selector that does not compile.
var stdEnumDefs = sync.OnceValue(func() []*typeDef {
	defs := make([]*typeDef, len(stdEnumSpecs))
	for i := range stdEnumSpecs {
		s := &stdEnumSpecs[i]
		defs[i] = &typeDef{nomi: s.nomi, tagName: s.tagField, isEnum: true, lowerable: true, // rtDeclared, so typeDecl emits nothing for it: the Go type is
			// hand-written in rt/ordering.go, rt/calendar.go or rt/json.go, and
			// a second declaration would be a second Go type for one Nomi type.
			rtDeclared: true}
	}
	for i := range stdEnumSpecs {
		s := &stdEnumSpecs[i]
		d := defs[i]
		slotOf := map[string]int{}
		for j := range s.variants {
			v := &s.variants[j]
			// Tag j+1: 0 is reserved invalid.
			if !v.carries() {
				d.variants = append(d.variants, variantDef{nomi: v.nomi, tag: j + 1, kind: "bare"})
				continue
			}
			if v.form == payloadStructFields {
				// N fields, each with its OWN slot: a struct-shaped variant's
				// fields are live at the same time, so the dedup rule that
				// shares one slot across VARIANTS cannot apply within one.
				ps := make([]payload, 0, len(v.fields))
				for fi := range v.fields {
					f := &v.fields[fi]
					slot, made := slotOf[f.field]
					if !made {
						slot = len(d.slots)
						d.slots = append(d.slots, slotDef{k: f.fieldKind()})
						slotOf[f.field] = slot
					}
					d.slots[slot].users = append(d.slots[slot].users, v.nomi)
					ps = append(ps, payload{nomi: f.nomi, k: f.fieldKind(), goDeflt: f.goDeflt, slot: slot})
				}
				d.variants = append(d.variants, variantDef{
					nomi: v.nomi, tag: j + 1, kind: "struct", payloads: ps,
				})
				continue
			}
			pk := v.payloadKind(defs)
			slot, made := slotOf[v.field]
			if !made {
				slot = len(d.slots)
				// NEVER boxed, and the reason is the same one stdStructDefs
				// gives: boxing is for a payload that can reach its own
				// containing type at a fixed offset, and the only
				// self-reference this family admits is through a `List` or a
				// `Map`, both already indirect. rt/json_test.go asserts that
				// property on the Go side, where it is what makes the
				// declaration compile at all.
				d.slots = append(d.slots, slotDef{k: pk})
				slotOf[v.field] = slot
			}
			d.slots[slot].users = append(d.slots[slot].users, v.nomi)
			// "struct" for a payloadStructScalar row and "positional"
			// otherwise, taken from the SPEC's form rather than from anything
			// about the payload kind — the two are independent, and a scalar
			// payload reaches this line under both forms. The payload's `nomi`
			// is what case.go binds a struct pattern's field from, so it is
			// empty for a positional row and the declared field name for a
			// struct one.
			vkind := "positional"
			if v.form == payloadStructScalar {
				vkind = "struct"
			}
			d.variants = append(d.variants, variantDef{
				nomi:     v.nomi,
				tag:      j + 1,
				kind:     vkind,
				payloads: []payload{{nomi: v.nomiField, k: pk, slot: slot}},
			})
		}
	}
	return defs
})

// stdEnumKind is the kind of the i'th spec's values.
func stdEnumKind(i int) kind { return named(stdEnumDefs()[i]) }

// stdEnumKindOfGoType is the kind an rt signature's Go type names, or
// kindInvalid.
//
// kindOfGoType's direction, and identified by the reflect.Type held in the spec
// rather than by name: `rt.CalendarError` and a hypothetical unrelated rt type
// spelled the same are different `reflect.Type` values, which is the same reason
// opaqueKindOfGoType compares types instead of names.
func stdEnumKindOfGoType(t reflect.Type) kind {
	for i := range stdEnumSpecs {
		if stdEnumSpecs[i].goType == t {
			return named(stdEnumDefs()[i])
		}
	}
	return kindInvalid
}

// --- anchoring -------------------------------------------------------------

// stdEnumAnchors resolves the specs against one module's analysis: the
// declaration NODE each spec was reached through, and the spec index under the
// Nomi name.
//
// A free function rather than a gen method for opaqueAnchors' reason: the
// STDLIB SIGNATURE pass (collectStdCandidates, via stdTypeKind) runs before any
// gen exists and has to know whether the `Ordering` in `Int.compare(a: Int, b:
// Int): Ordering` is a representable type. Two implementations of "is this the
// std type" is the drift the identity rules exist to prevent, so there is one.
//
// A module analyzed without an analysis library gets no anchors and every
// mention refuses, which is what happened before this file existed.
func stdEnumAnchors(fa *analysis.FileAnalysis) (map[*ast.EnumDef]int, map[string]int) {
	byDecl := map[*ast.EnumDef]int{}
	byName := map[string]int{}
	if fa == nil || fa.ModuleScope == nil {
		return byDecl, byName
	}
	for i := range stdEnumSpecs {
		s := &stdEnumSpecs[i]
		// The name resolves to nothing here, to something else, or to a
		// declaration of the same name in another file. In every one of those
		// cases there is no anchor and the ordinary paths report.
		decl := stdEnumDeclIn(fa, s)
		if decl == nil {
			continue
		}
		byDecl[decl] = i
		byName[s.nomi] = i
	}
	return byDecl, byName
}

// stdEnumSynthAnchors is the enum specs a DERIVE-SYNTHESIZED signature in fa may
// name, indexed under the Nomi name — the COMPILER-KNOWN route, resolved against
// each spec's OWN declaring module rather than against fa.
//
// # Why a synthesized declaration needs its own route
//
// stdEnumAnchors above resolves through `fa.ModuleScope`, i.e. through the
// deriving file's IMPORTS, and for a hand-written declaration that is exactly
// right: `Int.compare(a: Int, b: Int): Ordering` is written in std/int.nomi,
// which imports `std/comparable.Ordering`, and a file that did not import it
// could not write that signature at all.
//
// A DERIVE-SYNTHESIZED declaration is not written in the file and does not
// resolve like one. analysis/derive_synthesis.go says so in as many words about
// this exact name: "the `Ordering` enum (Comparable signatures + the qualifier
// of `Ordering.Less` value/pattern refs) ... Synthesized code is compiler output
// — these names resolve through the compiler-known route, never through the
// deriving file's imports". So `derive Comparable for Codepoint` yields
// `compare(a: Codepoint, b: Codepoint): Ordering` in a file that never mentions
// `Ordering`, and the module-scope lookup answers nil.
//
// Four std files derive Comparable, and three of them (std/bool,
// std/codepoints, std/duration) never mention `Ordering` in their own source.
// Without this route each would refuse as `stdlib function outside the scalar
// subset` for a signature every element of which has a representation, a key
// naming a missing representation where the cause is a missing IMPORT.
//
// # Why this cannot be a blanket relaxation, and what keeps it narrow
//
// The module-scope rule is the identity rule: a file declaring its OWN
// `Ordering` must not have std's substituted for it, and that failure is a wrong
// ANSWER rather than a compile error. So this admits a spec only when
// `fa.ModuleScope.Lookup` binds the name to NOTHING AT ALL. When it binds to
// anything — std's own `Ordering` or a local one — stdEnumAnchors has already
// decided, correctly, and this map is silent on that name. The two are therefore
// disjoint by construction, which is what lets stdAnchors.forSynthesized union
// them without either overriding the other.
//
// Applied ONLY to a synthesized declaration, which is a property of the AST node
// (ast.ImplBlock.SynthOriginLine, "Zero on every hand-written block") rather
// than an inference from the name. A hand-written std signature naming an
// unimported type keeps refusing, because that program does not exist: the front
// end rejects it.
func stdEnumSynthAnchors(fa *analysis.FileAnalysis) map[string]int {
	byName := map[string]int{}
	if fa == nil || fa.ModuleScope == nil {
		return byName
	}
	declared := stdEnumDeclaredInStd()
	for i := range stdEnumSpecs {
		s := &stdEnumSpecs[i]
		if fa.ModuleScope.Lookup(s.nomi) != nil {
			// The ordinary path has an answer for this name — yes or no — and
			// it is the authoritative one. See the header.
			continue
		}
		if !declared[i] {
			continue
		}
		byName[s.nomi] = i
	}
	return byName
}

// stdEnumDeclaredInStd is, per spec, whether the spec's declaring std module
// declares it in the spec's shape — stdEnumDeclIn asked of std's own analysis,
// once per process.
//
// Once is sound because the answer is a function of the embedded stdlib and the
// spec table, both fixed per binary, and only a bool leaves: no declaration
// pointer from this analysis reaches the caller. A std.Load per call would
// make stdEnumSynthAnchors nearly every load that lowering hello world runs.
var stdEnumDeclaredInStd = sync.OnceValue(func() []bool {
	lib := stdAnchorLib()
	ok := make([]bool, len(stdEnumSpecs))
	for i := range stdEnumSpecs {
		s := &stdEnumSpecs[i]
		ok[i] = stdEnumDeclIn(lib.Files[strings.TrimPrefix(s.origin, "std/")], s) != nil
	}
	return ok
})

// stdEnumDeclIn is the declaration a spec's name resolves to in fa, validated
// against the spec's shape, or nil.
//
// Factored out of stdEnumAnchors' loop so the compiler-known route above applies
// the SAME identity and shape rules to the declaring module that the ordinary
// route applies to the importing one — a second spelling of "is this the std
// enum" is the drift this file's header names.
func stdEnumDeclIn(fa *analysis.FileAnalysis, s *stdEnumSpec) *ast.EnumDef {
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
	et, isEnum := sym.Type.(*analysis.EnumType)
	if !isEnum || et.Origin != s.origin || et.Name != s.nomi {
		return nil
	}
	decl, isDecl := sym.Node.(*ast.EnumDef)
	if !isDecl || !s.matches(decl) {
		return nil
	}
	return decl
}

// matches reports whether decl is the declaration this spec describes.
//
// Everything checked here decides REPRESENTATION. The variant list decides the
// TAGS, so a std edit that reorders `Ordering`'s variants must produce NO anchor
// rather than lower against a numbering nobody wrote — which is the one failure
// this whole file's identity machinery cannot otherwise detect, since a
// permuted tag is a wrong answer and not a compile error. `Public` because a
// file-private declaration cannot be the type another module names; `Opaque` is
// rejected because an opaque enum's `case` is a use-site rule this does not
// reproduce.
//
// Each variant's PAYLOAD is checked too, and against the same standard: a bare
// spec variant demands a bare declaration, and a carrying spec variant demands a
// POSITIONAL declaration whose single type is the one the spec's own `shape`
// predicate accepts. So a std edit that gave `Error.UnknownZone` an Int payload,
// or a second one, or a struct shape, produces no anchor rather than writing a
// String into a field that no longer holds one — and the same holds one level up
// for a container: `Json.Arr List<Int>` would not anchor `Json` at all.
func (s *stdEnumSpec) matches(decl *ast.EnumDef) bool {
	if decl.Name != s.nomi || !decl.Public || decl.Opaque {
		return false
	}
	if len(decl.TypeParams) > 0 || len(decl.WhereClauses) > 0 {
		return false
	}
	if len(decl.Items) > 0 {
		// A type body item is layout-relevant. An attached `//!` test is not,
		// and is deliberately not checked — see stdStructSpec.matches.
		return false
	}
	for _, dec := range decl.Decorators {
		// `derive` is the one decorator that is not a gap: std/comparable
		// carries `derive Display for Ordering`, which the analyzer also
		// synthesizes into an ordinary impl block that lowers through the
		// ordinary path. Any other decorator is a real refusal and must not be
		// redirected past.
		if dec.Name != "derive" {
			return false
		}
	}
	if len(decl.Variants) != len(s.variants) {
		return false
	}
	for i := range s.variants {
		want := &s.variants[i]
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
		if want.form == payloadStructScalar {
			if got.Kind != "struct" || !want.declaredStructShapeOK(got.Fields) {
				return false
			}
			continue
		}
		if want.form == payloadStructFields {
			if got.Kind != "struct" || !want.declaredStructFieldsOK(got.Fields) {
				return false
			}
			continue
		}
		if got.Kind != "positional" || !want.declaredShapeOK(got.DataTypeExpr, stdEnumSpecs) {
			return false
		}
	}
	return true
}

// loadStdEnums resolves the specs against this gen's module, once.
func (g *gen) loadStdEnums() {
	if g.stdEnumsLoaded {
		return
	}
	g.stdEnumsLoaded = true
	g.stdEnumByDecl, g.stdEnumByName = stdEnumAnchors(g.fa)
	stdSynthEnumAnchors(g.fa, g.stdEnumByName)
}

// stdEnumDeclared reports the spec index for an enum declaration this module
// carries, or -1. Read by buildTypes: an anchored declaration adopts the shared
// def instead of getting its own shell, which is what keeps std/comparable's
// own gen from naming a Go type it never declares.
func (g *gen) stdEnumDeclared(decl *ast.EnumDef) int {
	g.loadStdEnums()
	if i, anchored := g.stdEnumByDecl[decl]; anchored {
		return i
	}
	return -1
}

// stdEnumNamed resolves a type NAME to the shared def, for a module that
// MENTIONS one of these types without declaring it.
//
// The anchor was established through the module's own scope, which is what the
// checker resolves a bare type name through, so no per-mention re-check is
// needed — opaqueNamed's reasoning exactly. A module declaring its own
// `Ordering` never reaches this: namedType and typeOf consult g.types first,
// and buildTypes put the local declaration there.
func (g *gen) stdEnumNamed(name string) (*typeDef, bool) {
	g.loadStdEnums()
	i, anchored := g.stdEnumByName[name]
	if !anchored {
		return nil, false
	}
	return stdEnumDefs()[i], true
}

// stdEnumDef reports whether d is one of the shared defs, and which spec.
//
// Pointer identity, not a name: it is the same question preludeOwns answers for
// an interned prelude instance, and a name is not an identity.
func stdEnumDef(d *typeDef) (int, bool) {
	for i, sd := range stdEnumDefs() {
		if sd == d {
			return i, true
		}
	}
	return 0, false
}

// stdEnumOwns reports whether `owner` is the type-name spelling of d, for a
// pattern head written `Ordering.Less`.
//
// Not an identity check and does not need to be: d is already fixed by the
// SCRUTINEE's kind. This only asks whether the qualifier the programmer wrote
// agrees with the type being matched, which is what case.go's
// `g.types[owner] != d` asks for a module-declared enum. See preludeOwns.
func (g *gen) stdEnumOwns(owner string, d *typeDef) bool {
	i, isStd := stdEnumDef(d)
	if !isStd {
		return false
	}
	g.loadStdEnums()
	if j, anchored := g.stdEnumByName[owner]; anchored {
		return i == j
	}
	// `random.Error.OsEntropy{reason}`: the enum named through its module's
	// qualifier, which binds no bare name here.
	return strings.Contains(owner, ".") && g.qualifiedStdDef(owner) == d
}

// projectStdEnum is inferred.go's projection for a monomorphic stdlib enum:
// the type the checker solved for an unannotated position.
//
// Identified by the solved type's (Origin, Name), which is the analyzer's own
// identity rule, against a spec validated in its DECLARING module
// (stdEnumDeclaredInStd): stdStructOfType's rule for std records. Not by this
// module's anchor, because the positions that reach a projection are the ones
// where the program never writes the name. `Date.parse(s) |>
// Result.map_err(|e| "${e}")` solves `e` as std/calendar's `Error` in a file
// that imports only `Date`, and an anchor keyed on this file's scope refused
// the parameter there while lowering it once the file imported `Error`. The
// Origin keeps a user's own `Error` out, since its origin is the user's file.
// Unlike projectPreludeEnum there is no empty-Origin case to accommodate: an
// Ordering is never produced by instantiating anything.
func (g *gen) projectStdEnum(ty *analysis.EnumType) (kind, bool) {
	if len(ty.TypeArgs) > 0 || ty.Origin == "" {
		return kindInvalid, false
	}
	declared := stdEnumDeclaredInStd()
	for i := range stdEnumSpecs {
		if stdEnumSpecs[i].origin == ty.Origin && stdEnumSpecs[i].nomi == ty.Name && declared[i] {
			return stdEnumKind(i), true
		}
	}
	return kindInvalid, false
}

// stdEnumVariantAt resolves a variant reference at a source position to the
// shared def and the variant it names.
//
// `variant` is the LOCAL spelling — how the programmer wrote it here — and the
// declaration's own name for it comes back from variantRefAt. The two differ
// for `import std/json.Json.{String as JStr}`: comparing the canonical name
// against the local spelling after following the resolution would be
// vacuously right without an alias and wrong with one. See variantalias.go
// for the symbol shapes.
func (g *gen) stdEnumVariantAt(variant string, line, col int) (*typeDef, *variantDef, bool) {
	g.loadStdEnums()
	ref, canonical, isVariant := variantRefAt(g.fa, variant, line, col)
	if !isVariant {
		return nil, nil, false
	}
	sym := ref
	if sym.Resolved != nil {
		sym = sym.Resolved
	}
	decl, isDecl := sym.Node.(*ast.EnumDef)
	if !isDecl {
		return nil, nil, false
	}
	i, anchored := g.stdEnumByDecl[decl]
	if !anchored {
		// The module's anchor table is keyed on the TYPE NAME being in module
		// scope, and a variant-only selective import does not put it there. See
		// stdEnumSpecOfSymbol.
		i, anchored = stdEnumSpecOfSymbol(sym, decl)
	}
	if !anchored {
		return nil, nil, false
	}
	d := stdEnumDefs()[i]
	v := d.variant(canonical)
	if v == nil {
		return nil, nil, false
	}
	return d, v, true
}

// stdEnumSpecOfSymbol resolves a spec from the analyzer's own symbol for a
// VARIANT, for the import form that never binds the type name.
//
// # Why the type-name lookup is not enough
//
// stdEnumAnchors resolves each spec through `fa.ModuleScope.Lookup(s.nomi)`, so
// a spec anchors only in a module that has the TYPE NAME in scope. That is the
// whole surface for a type a program writes down — `m: RoundingMode` needs
// `RoundingMode` imported — and it is NOT the surface for a variant:
//
//	import std/decimal.RoundingMode.{Floor, HalfEven, Up}
//
// binds three variants and does not bind `RoundingMode`, so with a name lookup
// alone every bare `HalfEven` would refuse `type reference` while the type has
// a representation (04-scalars-and-text/decimals/decimals_test.nomi).
//
// The identical shape for `Ordering`,
//
//	std/comparable.Ordering.{Equal, Greater, Less}
//
// anchors either way, because `Ordering` is in the PRELUDE and therefore in
// module scope whether a file imports it or not. So `Ordering` alone cannot
// witness this rule.
//
// # Why the variant's own symbol is a STRONGER anchor, not a looser one
//
// Both halves of the analyzer's nominal-identity rule are present here and
// neither is weakened: `sym.Type` is the `*analysis.EnumType` the checker solved
// for the variant, carrying Origin AND Name, and `sym.Node` is the declaration
// the SHAPE check runs against. That is the same pair stdEnumAnchors compares,
// established from the analyzer's resolution of the reference rather than from a
// name lookup — and a resolution cannot be shadowed the way a scope lookup can.
// A user enum spelling both names the way std does has a different Origin and
// gets nothing; TestStdEnumVariantAnchorIsIdentityNotName is that case.
//
// Deliberately NOT written into g.stdEnumByDecl. That table is the module's
// anchor set and other readers ask it whether a type is NAMEABLE here —
// stdEnumNamed for a bare type name, stdEnumOwns for a pattern qualifier,
// projectStdEnum for a solved type. A variant-only import does not make the type
// nameable, so seeding the table from one would answer yes to a question whose
// answer is no, and `m: RoundingMode` would lower in a file that never imported
// the name.
//
// # A CARRYING variant's symbol types as a CONSTRUCTOR
//
// A BARE variant's symbol carries an `*analysis.EnumType`, the value itself. A
// carrying variant's symbol carries the constructor's signature instead:
// `std/json.Json.Obj` reports `(Map<String, Json>) -> Json` while `Json.Null`
// reports `Json`. Reading only the first shape would admit `Null` and refuse
// `String("v")` in the same file, and the corpus reaches this path mostly
// through `std/decimal.RoundingMode` and `std/comparable.Ordering`, both of
// whose variants are BARE.
//
// Reading through the RETURN is not a weakening: it is the same
// `*analysis.EnumType`, so Origin and Name are the same two facts the bare arm
// compares. prelude.go's preludeReturn is the same unwrap for the same reason.
func stdEnumSpecOfSymbol(sym *analysis.Symbol, decl *ast.EnumDef) (int, bool) {
	ty := sym.Type
	if ft, isFunc := ty.(*analysis.FuncType); isFunc && ft.Return != nil {
		ty = ft.Return
	}
	et, isEnum := ty.(*analysis.EnumType)
	if !isEnum || len(et.TypeArgs) > 0 {
		return 0, false
	}
	for i := range stdEnumSpecs {
		s := &stdEnumSpecs[i]
		if et.Origin != s.origin || et.Name != s.nomi {
			continue
		}
		if !s.matches(decl) {
			return 0, false
		}
		return i, true
	}
	return 0, false
}

// --- the ordering operators ------------------------------------------------

// compareResolves reports whether `Comparable.compare` at kind k resolves
// statically: from the module's own impl table, the built-in List order, a
// sibling file's impl, the stdlib's, or rt's scalar order.
//
// The ORDER is the intent rather than a conflict resolution. A LOCAL impl is
// asked first: a program declaring `impl Comparable for T` owns T's ordering.
// A SIBLING FILE's impl comes next (siblingcompare.go). The stdlib index is
// last, for the types std itself declares an impl for (`derive Comparable for
// Duration`, `impl Comparable for String`), and after it rt's scalar order.
//
// Every route verifies the SIGNATURE: two parameters of the receiver's kind
// and a result that is the shared Ordering def by pointer. A `compare`
// returning something else is not this operator's callee, and pointer
// identity is what keeps a user's own `Ordering` out.
//
// `at` is a node rather than the operator, because two spellings reach here —
// `a < b` and `List.compare(a, b)` — and one of them is not a binary.
func (g *gen) compareResolves(at ast.Node, k, ord kind) bool {
	if k.tag == tagList {
		// std's `impl Comparable for List<T>` is generic, so the index cannot
		// serve it; lists.go resolves it from the element kind instead.
		return g.listOrders(k, at)
	}
	if impl := g.implsByIface["Comparable"][k]; impl != nil {
		if !impl.lowerable {
			return false
		}
		it := impl.items["compare"]
		return it != nil && len(it.params) == 2 && it.params[0] == k && it.params[1] == k && it.result == ord
	}
	if _, found := g.siblingCompareAt(k, ord, at); found {
		return true
	}
	if g.stdCompareAt(k, ord) != nil {
		return true
	}
	// LAST, so std wins whenever std can answer. One scalar reaches here,
	// Bool, whose ordering has no route through the index in either
	// direction; scalarorder.go carries both closed doors.
	return scalarCompares(k)
}

// stdCompareAt resolves std's `Comparable.compare` at receiver kind k, checking
// the signature the call site assumes.
//
// Deliberately NOT stdlibImplOf, and the difference is one line: stdlibImplOf
// returns nil for a receiver whose `def` is nil, i.e. every scalar. That guard
// is load-bearing where it is — `stdlibEquality` shares it, and admitting a
// scalar there would change which function `==` on a String resolves to, which
// is a decision about a different operator — but the element of a `List<Int>`
// IS a scalar and its ordering has to come from somewhere. So the ordering
// operators get their own lookup, with the same signature verification, and the
// equality path keeps its guard untouched.
//
// One place decides "which function is `Comparable.compare` at kind k" for
// everything in this file: the `<` operator, `List.compare` and the element
// comparator a list of lists needs all arrive here.
func (g *gen) stdCompareAt(k, ord kind) *stdFunc {
	if g.std == nil {
		return nil
	}
	f := stdPick(g.std.byIface["Comparable.compare"][k], []kind{k, k})
	if f == nil || f.why != "" || len(f.params) != 2 {
		return nil
	}
	if f.params[0] != k || f.params[1] != k || f.result != ord {
		return nil
	}
	return f
}
