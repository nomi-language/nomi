package irbuild

import (
	"reflect"
	"strings"
	"sync"

	"github.com/nomi-language/nomi/rt"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// A stdlib RECORD STRUCT, as a Go struct declared in rt: `calendar.DateTime`,
// `compiler.Diagnostic` and the other rows of stdStructSpecs.
//
// # Why this is a fourth family and not a row in one of the three
//
// opaque.go's subjects are `opaque type X Int` — distinct types over a SCALAR,
// so `inner` is one kind and the whole def is a leaf. stdenum.go's are
// monomorphic ENUMS. prelude.go's are GENERIC enums. std/calendar's civil
// ladder is none of those: `pub opaque struct DateTime { instant_nanos: Int;
// zone: String }` is a record with named fields, and the field NAMES are part
// of the interface, because the type's own impls read them (`impl Equatable for
// DateTime` is `a.instant_nanos == b.instant_nanos`).
//
// opaqueSpec.matches rejects it explicitly and correctly — `decl.HasBody` is a
// refusal declModifiers owns, and a field set is not an `inner` kind. So this
// is the same MECHANISM (an (Origin, Name) anchor against a validated
// declaration shape, a process-wide *typeDef, `rtDeclared` so nothing emits a
// second Go declaration) over a different declaration kind.
//
// # Sharing the def, and why the argument carries over unchanged
//
// foreign.go rejects sharing a *typeDef across gens because "a shared def's
// field kinds are interned in the OWNER's g.comps". A row here is admissible
// exactly when every field kind is PACKAGE-NEUTRAL. `rt.DateTime` renders
// `rt.DateTime` in every gen and its fields are `int64` and `string`, interned
// nowhere; see stdStructField.kindOf for the non-scalar fields.
//
// It has to be shared rather than merely may be, for opaque.go's reason: a
// stdFunc's parameter and result kinds are built once in stdCandidateFor and
// compared by POINTER against a call site's argument kinds in a different gen,
// so a per-gen def would make `DateTime.with_zone(d, z)` mismatch against
// itself.
//
// # What the shape check is FOR
//
// Every field's NAME, ORDER and TYPE is checked against the spec, and the
// declaration must be `pub opaque struct` with no type parameters, no body
// items and no decorator but `derive`. That is not defensiveness: the builder
// constructs records and reads fields from the SPEC, so a std edit
// that renamed `zone`, reordered the fields, or changed `instant_nanos: Int` to
// `Float` would otherwise lower against a layout nobody wrote — and unlike a
// type error, a permuted field is a wrong ANSWER rather than an error. A declaration that fails the check produces no anchor, and every
// mention then refuses loudly through the ordinary paths.
//
// TestStdStructSpecsMatchStdSource is what keeps the check from being vacuous:
// without it a spec could drift from std and every guard here would pass
// against a declaration nothing anchors.
//
// stdStructField is one field of a stdlib record struct: the Nomi name, the
// exported Go field in rt, and the kind it holds.
type stdStructField struct {
	// nomi is the field name as std declares it, and what a field selector in
	// std's own source resolves through.
	nomi string
	// kindOf is the field's kind, given the def shell of every spec in the
	// table.
	//
	// A FUNCTION rather than a kind, and the reason is that a field may name
	// another row: std/assertions' `values: List<AssertionValue>` is a List
	// over the def the row beside it owns, and neither def exists until the
	// one-pass build below has made both shells. Handing the whole slice in
	// keeps the dependency explicit and one-directional — a row reads the
	// shells, never stdStructDefs(), so the OnceValue cannot re-enter itself.
	//
	// Not restricted to scalars. What the field must be is PACKAGE-NEUTRAL,
	// which is what makes the shared def correct in every gen (see the
	// header); sharedcomp.go gives structural kinds process-wide identity.
	// `*rt.List[rt.NomiAssertionValue]` and `rt.Maybe[string]` name rt and
	// process-wide defs and nothing else, so they are neutral by
	// construction; `List<Point>` is not. TestStdStructDefsArePackageNeutral
	// asserts the property.
	kindOf func(defs []*typeDef) kind
	// deflt is the field's DEFAULT, when std declares one, and nil when the
	// field is required at every construction site. A spec and a declaration
	// must agree in BOTH directions -- a declared default with no spec row for
	// it produces no anchor, and so does a spec default the declaration does
	// not carry.
	deflt *stdFieldDefault
}

// withDefault attaches a default to a field row, so the three constructors
// above stay single-purpose and a defaulted field reads as the field it is plus
// the default it carries.
func withDefault(f stdStructField, d *stdFieldDefault) stdStructField {
	f.deflt = d
	return f
}

// stdScalarField is the common row: a field whose kind is fixed.
func stdScalarField(nomi string, k kind) stdStructField {
	return stdStructField{nomi: nomi, kindOf: func([]*typeDef) kind { return k }}
}

// stdListField is a field holding `List<the spec at index i>`.
func stdListField(nomi string, i int) stdStructField {
	return stdStructField{nomi: nomi, kindOf: func(defs []*typeDef) kind {
		return listKindIn(nil, named(defs[i]))
	}}
}

// stdStringListField is a field holding `List<String>`.
//
// The sibling of stdListField over a SCALAR element rather than a row of this
// table, so it needs the defs slice for nothing and ignores it. Separate from
// stdScalarField because the kind is structural: it interns with a nil gen,
// which internComp documents as the stdlib signature boundary, and with a
// package-neutral element it lands in the PROCESS-WIDE table — the same entry a
// call site's own `g.listKind` produces, which is the equality the whole
// arrangement needs.
func stdStringListField(nomi string) stdStructField {
	return stdStructField{nomi: nomi, kindOf: func([]*typeDef) kind {
		return listKindIn(nil, kindString)
	}}
}

// stdStructRowField is a field holding the STRUCT spec at index i directly:
// `Response.status: Status`.
//
// It reads the shells slice for the reason stdListField does — neither def
// exists until the two-pass build has made both — so a row may name a row
// declared after it and the OnceValue still cannot re-enter itself. Neutral by
// construction: every def in that slice is `rtDeclared`, which is exactly what
// `kind.packageNeutral` answers true for.
func stdStructRowField(nomi string, i int) stdStructField {
	return stdStructField{nomi: nomi, kindOf: func(defs []*typeDef) kind {
		return named(defs[i])
	}}
}

// stdListOfEnumField is a field holding `List<the ENUM spec at index i>`.
//
// The one field constructor that reaches OUT of this table. stdListField indexes
// stdStructDefs, which is why it takes the shells slice: neither def exists
// until the two-pass build has made both. An ENUM def has no such ordering
// problem, because stdEnumSpecs names no struct and stdEnumDefs is therefore
// already complete by the time any struct field is resolved. The dependency is
// one-way and acyclic, and TestStdStructDefsArePackageNeutral covers the
// resulting kind exactly as it covers every other.
func stdListOfEnumField(nomi string, i int) stdStructField {
	return stdStructField{nomi: nomi, kindOf: func([]*typeDef) kind {
		return listKindIn(nil, named(stdEnumDefs()[i]))
	}}
}

// stdMaybeField is a field holding `Maybe<k>`.
//
// Routed through sharedPreludeInstance rather than buildPreludeDef, so the def
// a field carries is the SAME pointer a signature position or a call site gets
// for the same instantiation — the identity rule stdprelude.go exists to state.
func stdMaybeField(nomi string, of func(defs []*typeDef) kind) stdStructField {
	return stdStructField{nomi: nomi, kindOf: func(defs []*typeDef) kind {
		k, shared := sharedPreludeInstance(preludeSpecFor("std/maybe", "Maybe"), []kind{of(defs)})
		if !shared {
			// Unreachable while every argument here is neutral, and honoured
			// rather than assumed: a def interned nowhere would be a kind no
			// call site could ever match.
			return kindInvalid
		}
		return k
	}}
}

// stdMapField is a field holding `Map<key, val>` — the first stdlib struct
// field whose type is a CONTAINER.
//
// It reads as impossible at first: `kindOf` is handed only the defs, `mapKind`
// is a method, and the Map kind needs an interning table to live in. The route
// is `mapKindIn` with a NIL gen, which `internComp` documents as the stdlib
// signature boundary — with both components package-neutral it interns in the
// PROCESS-WIDE table, so this field's kind is the SAME entry a call site's
// `g.mapKind` produces and the two compare equal. That is the identity rule
// stdprelude.go states for prelude instances, holding here for the same reason
// and by the same mechanism.
//
// key and val are functions rather than kinds so a field can name a def from
// the table being built, exactly as stdMaybeField's `of` does. Both must be
// package-neutral: a non-neutral component with a nil gen panics in internComp
// rather than yielding a kind interned nowhere, which is the loud version of
// the failure that guard exists for.
func stdMapField(nomi string, key, val func(defs []*typeDef) kind) stdStructField {
	return stdStructField{nomi: nomi, kindOf: func(defs []*typeDef) kind {
		return mapKindIn(nil, key(defs), val(defs))
	}}
}

// stdOpaqueKind is the kind of the std newtype declared as `origin.nomi` —
// `stdOpaqueKind("std/toml", "Toml")`.
//
// By NAME rather than by index. `opaqueKind` takes a position in `opaqueSpecs`,
// and a positional reference from this table would be a second encoding of that
// table's ORDER: the calendar block there is ordered largest-unit-first because
// that is the order a reader checks it in, so a row inserted for readability
// would silently repoint a field here. The name is what std actually declares.
//
// kindInvalid when no spec matches, which propagates as a refusal rather than
// producing a field whose kind is a lie. Unreachable while every caller names a
// spec that exists, and honoured rather than asserted for the same reason
// stdMaybeField honours its `!shared` case.
func stdOpaqueKind(origin, nomi string) kind {
	for i := range opaqueSpecs {
		if opaqueSpecs[i].origin == origin && opaqueSpecs[i].nomi == nomi {
			return opaqueKind(i)
		}
	}
	return kindInvalid
}

// stdHostOriginKind is the kind of the stdHostSpecs row identified by ORIGIN and
// name — `stdHostOriginKind("std/context", "Context")`.
//
// For the rows that have no blessed singleton to be keyed on. That table has
// two channels and this is the second one: `Byte`,
// `Bytes` and `Decimal` are `*analysis.PrimitiveType` singletons a pointer
// comparison identifies, while `Context`, `Dynamic`, `Supervisor` and the two
// Bool markers get a per-load `*PrimitiveType` and can only be anchored by where
// they were declared.
//
// By NAME rather than by index, for stdOpaqueKind's reason: a positional
// reference from a field row would be a second encoding of stdHostSpecs' order,
// and that table's own comments say rows are APPENDED precisely so nothing
// shifts.
//
// kindInvalid when no row matches, which propagates as a refusal rather than a
// field whose kind is a lie.
func stdHostOriginKind(origin, nomi string) kind {
	for i := range stdHostSpecs {
		if stdHostSpecs[i].origin == origin && stdHostSpecs[i].nomi == nomi {
			return stdHostKind(i)
		}
	}
	return kindInvalid
}

// stdFieldDefault is a stdlib field DEFAULT: the shape std's declaration must
// have, and the value the builder uses where a construction site omits the
// field.
//
// # Why the value is written here and not lowered from std's AST
//
// A field default is an expression of the DECLARING module, and only that
// module's gen can resolve its names. A user struct's default reached from
// another file is lowered there, in an accessor (foreignfielddefault.go). A
// std default is instead stated here in the builder's own vocabulary, so it
// has no names to resolve. `shape` is what keeps that honest: it is checked against std's
// real declaration, and a std edit that changed a default produces NO anchor
// rather than a construction site quietly built from a stale one.
//
// # Why `value` returns an UNTYPED literal where it can
//
// `None` and `[]` are returned as kindBareNone and kindEmptyList — the same
// sentinels a user writing `None` or `[]` produces — so the field's real kind is
// reached through the ordinary `coerce` that discharges them. Nothing here
// renders a Go type argument, so a Maybe or a List default cannot disagree with
// the field it fills.
type stdFieldDefault struct {
	// shape reports whether std's declared default is the expression `value`
	// implements. Structural rather than textual: this package has no
	// expression printer, and a text compare would bind the spec to std's
	// formatting.
	shape func(ast.Node) bool
	// lit is the same default as the IR builder constructs it; see
	// irstdfielddefault.go. The zero value is a default the builder does not
	// construct.
	lit stdDefaultLit
}

// stdStringDefault is `= "..."` on a String field.
func stdStringDefault(want string) *stdFieldDefault {
	return &stdFieldDefault{shape: func(n ast.Node) bool {
		s, isStr := n.(*ast.StringLit)
		// Triple and Raw are checked so a std edit that changed the
		// SPELLING to an equal value still anchors, while one that changed
		// the value does not.
		return isStr && s.Value == want
	}, lit: stdDefaultLit{form: stdDefaultString, text: want}}
}

// stdNoneDefault is `= None` on a Maybe field, the spelling std writes it in.
// What the check is FOR is a default that changed VALUE — to `Some(0)`, or to a
// call — and the shape below rejects every one of those.
func stdNoneDefault() *stdFieldDefault {
	return &stdFieldDefault{
		shape: func(n ast.Node) bool {
			// `None` in a field-default position parses as a TypeIdent rather
			// than an Ident, because a default shares the type grammar's
			// lookahead, as in std/assertions' own declaration.
			ti, isType := n.(*ast.TypeIdent)
			return isType && ti.Name == "None"
		},
		lit: stdDefaultLit{form: stdDefaultVariant, text: "None"},
	}
}

// stdEmptyListDefault is `= []` on a List field.
func stdEmptyListDefault() *stdFieldDefault {
	return &stdFieldDefault{shape: func(n ast.Node) bool {
		l, isList := n.(*ast.ListLit)
		return isList && len(l.Items) == 0 && l.TypeName == nil
	}, lit: stdDefaultLit{form: stdDefaultEmptyList}}
}

// stdEmptyMapDefault is `= Map.empty()` on a Map field.
//
// The only default in this family whose declared expression is a CALL rather than
// a literal, and the shape check is correspondingly a shape check on the callee:
// `Map.empty` as a field access on the `Map` type-ident, with no arguments. A
// `Map.empty(x)` or a `Foo.empty()` therefore produces no anchor, which is what
// keeps the value below from standing in for an expression it does not implement.
//
// The value is the untyped `kindEmptyMap` sentinel — the same one `Map.empty()`
// produces at a user call site (maps.go) — so the field's real key and value
// kinds are reached through the ordinary `coerce` that discharges it. Nothing
// here renders a Go type argument, so this default cannot disagree with the field
// it fills.
func stdEmptyMapDefault() *stdFieldDefault {
	return &stdFieldDefault{shape: func(n ast.Node) bool {
		call, isCall := n.(*ast.Call)
		if !isCall || len(call.Args) != 0 {
			return false
		}
		fa, isField := call.Func.(*ast.FieldAccess)
		if !isField || fa.Field == nil || fa.Field.Name != "empty" {
			return false
		}
		ti, isType := fa.Object.(*ast.TypeIdent)
		return isType && ti.Name == "Map"
	}, lit: stdDefaultLit{form: stdDefaultEmptyMap}}
}

// stdStructSpec is one stdlib record struct: what this builder believes std
// declares, and which Go type in rt it is.
type stdStructSpec struct {
	// origin is the declaring file's build key, the analyzer's own identity
	// half. See analysis.StructType.Origin.
	origin string
	// nomi is the type's Nomi name, the other half.
	nomi string
	// goType is rt's Go type, held as the TYPE rather than as its spelling for
	// opaqueSpec.goType's reason: a rename or a deletion in rt is then a Go
	// compile error in this file, and the pairing is checked by reflection in tests.
	goType reflect.Type
	// fields are the declared fields in DECLARATION ORDER, which is what the
	// order check is against.
	fields []stdStructField
	// transparent records that std declares this WITHOUT `opaque`, so its
	// fields are readable — and constructible — outside the declaring module.
	//
	// It is a declared expectation rather than a relaxation, and matches()
	// checks it in BOTH directions: a spec that says opaque must not anchor a
	// transparent declaration and vice versa. The distinction is a use-site
	// SURFACE, not a representation: an opaque struct can only be built by
	// std's own functions, so the builder never constructs one or reads
	// a field of one at a user position, while a transparent one is
	// `d.message` in ordinary user code — which is exactly what
	// std/compiler.Diagnostic is for. Both surfaces are field reads and writes
	// off this spec's own field list, so admitting the transparent case adds no
	// mechanism; what it adds is the requirement that the field list be
	// EXACTLY right, and that is what the order-and-type check below is.
	transparent bool
}

// stdStructSpecs is the whole set, and every row has to be REACHED by something
// rather than merely be representable — a registry row for a type nothing can
// name is scaffolding. TestStdStructSpecsAreReachable asserts it over the
// corpus, the way TestStdEnumSpecsAreReachable does for the enum family; that
// guard has already caught one wrong claim about reachability made from a
// single key's details.
var stdStructSpecs = []stdStructSpec{{
	origin: "std/calendar",
	nomi:   "NaiveDateTime",
	goType: reflect.TypeFor[rt.NaiveDateTime](),
	fields: []stdStructField{
		stdScalarField("year", kindInt),
		stdScalarField("month", kindInt),
		stdScalarField("day", kindInt),
		stdScalarField("hour", kindInt),
		stdScalarField("minute", kindInt),
		stdScalarField("second", kindInt),
		stdScalarField("nanosecond", kindInt),
	},
}, {
	origin: "std/calendar",
	nomi:   "DateTime",
	goType: reflect.TypeFor[rt.DateTime](),
	fields: []stdStructField{
		stdScalarField("instant_nanos", kindInt),
		stdScalarField("zone", kindString),
	},
}, {
	origin: "std/calendar",
	nomi:   "OffsetDateTime",
	goType: reflect.TypeFor[rt.OffsetDateTime](),
	fields: []stdStructField{
		stdScalarField("instant_nanos", kindInt),
		stdScalarField("offset_seconds", kindInt),
	},
}, {
	// std/compiler's diagnostic record: the whole payload of `compiler.check`,
	// and the first row here that std declares TRANSPARENT.
	//
	// Its Go type is in rt for the ordinary reason — one Nomi type, one Go
	// representation, package-neutral in every gen — even though
	// the FUNCTION that produces it is in nomi/stdcompiler and reaches the
	// analyzer. Splitting it that way is what keeps a program that merely names
	// `Diagnostic` in a signature from linking the front end.
	origin: "std/compiler",
	nomi:   "Diagnostic",
	goType: reflect.TypeFor[rt.Diagnostic](),
	fields: []stdStructField{
		stdScalarField("line", kindInt),
		stdScalarField("col", kindInt),
		stdScalarField("message", kindString),
	},
	transparent: true,
}, {
	// std/compiler's hover payload. Reached ONLY as the `Ok` side of
	// `compiler.hover`'s `Result<Hover, String>`, which is what makes it the
	// first row here whose kind a call site sees through
	// preludeKindOfGoType rather than directly: `rt.Result[rt.Hover, string]`
	// projects onto `Result<Hover, String>` because the recursion bottoms out
	// in this spec.
	//
	// Both fields are Strings and BOTH are read by user code — `hover.signature`
	// in every one of the corpus's `hover_signature` helpers — so the field
	// order and the Go names are load-bearing exactly as everywhere else here.
	origin: "std/compiler",
	nomi:   "Hover",
	goType: reflect.TypeFor[rt.Hover](),
	fields: []stdStructField{
		stdScalarField("signature", kindString),
		stdScalarField("markdown", kindString),
	},
	transparent: true,
}, {
	// std/assertions' five value types, and they are the first rows here that
	// name EACH OTHER — `AssertionFailure.values` is a `List<AssertionValue>`
	// and an `AssertionValue.pipeline` is a `List<AssertionPipelineStage>`. The
	// table is therefore ordered leaves-first and the indices below are what a
	// row uses to reach the one it depends on.
	//
	// All five are TRANSPARENT: a program reads `failure.expression` and
	// `left.value`, which is the whole point of the type existing. None of them
	// is CONSTRUCTIBLE from user code in the corpus, but transparency is a
	// property of the declaration rather than of what a corpus happens to do,
	// and std declares them without `opaque`.
	//
	// Their Go types are the Nomi-facing family in rt/nomiassertion.go, which
	// is deliberately NOT rt's reporter struct: see that file's header for why
	// one type cannot serve both readers.
	origin: "std/assertions",
	nomi:   "AssertionPipelineStage",
	goType: reflect.TypeFor[rt.NomiAssertionPipelineStage](),
	fields: []stdStructField{
		stdScalarField("expression", kindString),
		stdScalarField("value", kindString),
	},
	transparent: true,
}, {
	origin: "std/assertions",
	nomi:   "AssertionValue",
	goType: reflect.TypeFor[rt.NomiAssertionValue](),
	fields: []stdStructField{
		stdScalarField("expression", kindString),
		stdScalarField("value", kindString),
		stdListField("pipeline", stdSpecAssertionPipelineStage),
	},
	transparent: true,
}, {
	origin: "std/assertions",
	nomi:   "AssertionBinding",
	goType: reflect.TypeFor[rt.NomiAssertionBinding](),
	fields: []stdStructField{
		stdScalarField("name", kindString),
		stdScalarField("expression", kindString),
		stdScalarField("value", kindString),
		stdListField("pipeline", stdSpecAssertionPipelineStage),
	},
	transparent: true,
}, {
	origin: "std/assertions",
	nomi:   "AssertionDetail",
	goType: reflect.TypeFor[rt.NomiAssertionDetail](),
	fields: []stdStructField{
		stdScalarField("label", kindString),
		stdScalarField("value", kindString),
	},
	transparent: true,
}, {
	origin: "std/assertions",
	nomi:   "AssertionFailure",
	goType: reflect.TypeFor[rt.NomiAssertionFailure](),
	fields: []stdStructField{
		stdScalarField("line", kindInt),
		stdScalarField("keyword", kindString),
		stdScalarField("expression", kindString),
		stdScalarField("reason", kindString),
		stdMaybeField("actual", func([]*typeDef) kind { return kindString }),
		stdMaybeField("binding", func(defs []*typeDef) kind {
			return named(defs[stdSpecAssertionBinding])
		}),
		stdListField("values", stdSpecAssertionValue),
		stdListField("details", stdSpecAssertionDetail),
	},
	transparent: true,
}, {
	// A row whose declaration carries FIELD DEFAULTS. Its four field TYPES
	// are expressible by the three constructors above; the defaults are what
	// withDefault adds.
	origin: "std/assertions",
	nomi:   "AssertionDetails",
	goType: reflect.TypeFor[rt.NomiAssertionDetails](),
	fields: []stdStructField{
		withDefault(stdScalarField("reason", kindString),
			stdStringDefault("assertion failed")),
		withDefault(stdMaybeField("actual", func([]*typeDef) kind { return kindString }),
			stdNoneDefault()),
		withDefault(stdMaybeField("expected", func([]*typeDef) kind { return kindString }),
			stdNoneDefault()),
		withDefault(stdListField("details", stdSpecAssertionDetail),
			stdEmptyListDefault()),
	},
	transparent: true,
}, {
	// std/dynamic's structured decode failure. Every field is expressible
	// and it carries no default; its `path` element names an enum row.
	//
	// A row whose field reaches the ENUM table rather than another
	// struct row. See stdListOfEnumField for why that direction is safe.
	origin: "std/dynamic",
	nomi:   "DecodeError",
	goType: reflect.TypeFor[rt.DynamicDecodeError](),
	fields: []stdStructField{
		stdListOfEnumField("path", stdEnumPathSegment),
		stdScalarField("expected", kindString),
		stdScalarField("got", kindString),
	},
	transparent: true,
}, {
	// std/compiler's whole-project input, a row whose field is a CONTAINER
	// (`03-tooling-and-diagnostics/compiler_project/compiler_project_test.nomi`
	// constructs one).
	//
	// Three fields, three different projections:
	//
	//   - `entry_point` is a scalar, covered by every row above.
	//   - `files` is a `Map<String, String>`. See stdMapField: the kind is built
	//     with a NIL gen and interns in the PROCESS-WIDE table because both
	//     components are package-neutral, so it is the same entry a call site's
	//     own `mapKind` produces. That equality is the whole requirement — a
	//     per-gen kind here would match no call site anywhere.
	//   - `manifest` is a `Maybe<Toml>` with a DEFAULT of `None`. `Toml` is
	//     std's `pub type Toml String` and already has an `opaqueSpecs` row, so
	//     no new runtime type was needed; the default matters because a zero
	//     `Maybe` has Tag 0, which is neither Some nor None and matches no arm
	//     of a `case` rather than failing.
	//
	// NOT transparent: std declares it `pub struct Project` without `opaque`,
	// but nothing in the corpus reads a field off one — it is constructed and
	// handed to `compiler.check_project`. Marked transparent anyway, because
	// transparency is a property of the DECLARATION and not of what a corpus
	// happens to do, which is the rule the assertions block states.
	origin: "std/compiler",
	nomi:   "Project",
	goType: reflect.TypeFor[rt.Project](),
	fields: []stdStructField{
		stdScalarField("entry_point", kindString),
		stdMapField("files",
			func([]*typeDef) kind { return kindString },
			func([]*typeDef) kind { return kindString }),
		withDefault(stdMaybeField("manifest", func([]*typeDef) kind {
			return stdOpaqueKind("std/toml", "Toml")
		}), stdNoneDefault()),
	},
	transparent: true,
}, {
	// std/calendar's two CIVIL types. Placed after the rows above rather than
	// with the other calendar rows, and that is not a style choice: inserting
	// them mid-table would repoint every `stdSpec*` index constant below,
	// because rows in this table name each other POSITIONALLY.
	//
	// The `Date"…"` / `Time"…"` typed literal and `calendar.Date.parse` need
	// these rows: `literal.go` resolves the handler and `stdlibCall` lowers it,
	// and a signature has to be able to NAME the result type.
	origin: "std/calendar",
	nomi:   "Date",
	goType: reflect.TypeFor[rt.Date](),
	fields: []stdStructField{
		stdScalarField("year", kindInt),
		stdScalarField("month", kindInt),
		stdScalarField("day", kindInt),
	},
}, {
	// A SEPARATE row from Date rather than a shared shape, and the reason is
	// semantic rather than tidiness: std gives each its own `derive
	// Equatable`/`Hashable`/`Comparable`, which generate over the fields that
	// EXIST. One record with both field sets would make `Date"2026-05-04"`
	// structurally equal to `NaiveDateTime"2026-05-04T00:00:00"` — a wrong
	// ANSWER, not a wasted field. rt/calendar_civil_test.go pins that.
	origin: "std/calendar",
	nomi:   "Time",
	goType: reflect.TypeFor[rt.Time](),
	fields: []stdStructField{
		stdScalarField("hour", kindInt),
		stdScalarField("minute", kindInt),
		stdScalarField("second", kindInt),
		stdScalarField("nanosecond", kindInt),
	},
}, {
	// std/compiler's ON-DISK run input, constructed by
	// `15-app-and-defer/effects/effects_test.nomi` as
	// `compiler.run_file(RunFile{entry_point: "main"})`.
	//
	// Placed after the rows above, for the reason the constants below give: rows
	// here name each other POSITIONALLY, and inserting this beside `Project`,
	// where a reader would look for it, shifts every `stdSpec*` index.
	//
	// NO NEW MACHINERY. It is
	// `Project` minus one field: `entry_point` is the same scalar, `env` is the
	// same `Map<String, String>` projection through stdMapField's nil-gen
	// process-wide intern, and the DEFAULT is the one new piece — `Map.empty()`
	// rather than `None`, which is stdEmptyMapDefault above.
	//
	// It is deliberately NOT the same Go type as `Project`. See rt.RunFile: a
	// Project carries its sources IN it and is ANALYZED, a RunFile names one file
	// ON DISK and is EVALUATED, and one record with both field sets would let the
	// builder construct either shape for either function — a wrong ANSWER rather
	// than an error.
	//
	// transparent, because std declares `pub struct RunFile` without `opaque`.
	// Marked from the DECLARATION rather than from what the corpus does with one,
	// which is the rule the assertions block states: the corpus only ever
	// constructs a RunFile and never reads a field off one.
	origin: "std/compiler",
	nomi:   "RunFile",
	goType: reflect.TypeFor[rt.RunFile](),
	fields: []stdStructField{
		stdScalarField("entry_point", kindString),
		withDefault(stdMapField("env",
			func([]*typeDef) kind { return kindString },
			func([]*typeDef) kind { return kindString }), stdEmptyMapDefault()),
	},
	transparent: true,
}, {
	// std/json's structured PARSE failure, a row whose Nomi name is DOTTED.
	// That is not a new mechanism: `fa.ModuleScope.Lookup` binds
	// `Json.DecodeError` to the declaration with `Origin: "std/json"` and
	// `Name: "Json.DecodeError"`, so the (origin, name) anchor works on the whole
	// spelling exactly as it does on a bare one. The dotted name is what
	// `dottedqual.go`'s namedType lookup needs for `Json.DecodeError.to_string`.
	//
	// Four scalar fields and no default; it relies on the `Json` enum, whose
	// `Result<Json, Json.DecodeError>` is what makes `Json.decode` nameable at
	// the stdlib signature boundary at all.
	//
	// TRANSPARENT: std declares `pub struct Json.DecodeError` without `opaque`,
	// and the corpus really does read `e.line`, `e.col` and `e.message` — through
	// `impl Display for Json.DecodeError`, which is ordinary Nomi in
	// std/json.nomi and lowers through the ordinary impl path. So there is ONE
	// renderer for that text rather than a Go copy that could disagree with it,
	// which is the arrangement rt/dynamic.go's header states for the same pair.
	origin: "std/json",
	nomi:   "Json.DecodeError",
	goType: reflect.TypeFor[rt.JsonDecodeError](),
	fields: []stdStructField{
		stdScalarField("message", kindString),
		stdScalarField("line", kindInt),
		stdScalarField("col", kindInt),
		stdScalarField("offset", kindInt),
	},
	transparent: true,
}, {
	// std/json's SHAPE failure, which is a different type from the one above and
	// std says why: a parse error is `DecodeError` and a shape error describes
	// the PATH inside a well-formed tree where a typed conversion failed. One
	// record with both field sets would let the builder construct either shape
	// for either function — a wrong ANSWER rather than an error, the same
	// argument the `Project`/`RunFile` pair rests on.
	//
	// `Result<Json.ShapeError>` is reached from
	// 11/module_qualified_impl_dispatch, 18/json_container_decode and
	// 18/json_derive.
	//
	// `path: List<String>` needs no new machinery either: `*rt.List[string]`
	// names rt and a Go builtin and nothing else, so stdListField's nil-gen
	// process-wide intern covers it, as for `AssertionFailure.values`.
	origin: "std/json",
	nomi:   "Json.ShapeError",
	goType: reflect.TypeFor[rt.JsonShapeError](),
	fields: []stdStructField{
		stdStringListField("path"),
		stdScalarField("expected", kindString),
		stdScalarField("got", kindString),
	},
	transparent: true,
}, {
	// std/calendar's five FALLIBLE-CONSTRUCTOR CARRIERS, and they are one
	// decision rather than five rows.
	//
	// WHY THEY EXIST AT ALL. A co-located adapter's FFI-shaped signature
	// projects a `Result<_, E>` only for `E` = `String`, so std/calendar's
	// eleven fallible constructors cannot return `Result<T, CalendarError>`
	// across the boundary: the classification a caller matches on
	// (`Nonexistent` is a DST gap, `InvalidFormat` is a bad string) would
	// collapse to a message.
	//
	// The failure therefore crosses as DATA — `kind` is 0 or an error ordinal,
	// `reason` is the payload — and calendar.nomi's `error_of` rebuilds the
	// variant. These rows are what make the Nomi declaration lowerable.
	//
	// ONE PER VALUE TYPE, not one per function: what varies across the eleven
	// is the Ok payload and nothing else. `value` names the corresponding
	// calendar row through stdStructRowField, which is `Response.status: Status`
	// again with a different pair.
	//
	// After the rows above, per the note below the specs: rows name each
	// other POSITIONALLY, and a mid-table insertion shifts every `stdSpec*`
	// index.
	origin: "std/calendar",
	nomi:   "DateAttempt",
	goType: reflect.TypeFor[rt.CalendarDateResult](),
	fields: []stdStructField{
		stdStructRowField("value", stdSpecCalendarDate),
		stdScalarField("kind", kindInt),
		stdScalarField("reason", kindString),
	},
	transparent: true,
}, {
	origin: "std/calendar",
	nomi:   "TimeAttempt",
	goType: reflect.TypeFor[rt.CalendarTimeResult](),
	fields: []stdStructField{
		stdStructRowField("value", stdSpecCalendarTime),
		stdScalarField("kind", kindInt),
		stdScalarField("reason", kindString),
	},
	transparent: true,
}, {
	origin: "std/calendar",
	nomi:   "NaiveAttempt",
	goType: reflect.TypeFor[rt.CalendarNaiveResult](),
	fields: []stdStructField{
		stdStructRowField("value", stdSpecCalendarNaiveDateTime),
		stdScalarField("kind", kindInt),
		stdScalarField("reason", kindString),
	},
	transparent: true,
}, {
	origin: "std/calendar",
	nomi:   "OffsetAttempt",
	goType: reflect.TypeFor[rt.CalendarOffsetResult](),
	fields: []stdStructField{
		stdStructRowField("value", stdSpecCalendarOffsetDateTime),
		stdScalarField("kind", kindInt),
		stdScalarField("reason", kindString),
	},
	transparent: true,
}, {
	origin: "std/calendar",
	nomi:   "ZonedAttempt",
	goType: reflect.TypeFor[rt.CalendarZonedResult](),
	fields: []stdStructField{
		stdStructRowField("value", stdSpecCalendarDateTime),
		stdScalarField("kind", kindInt),
		stdScalarField("reason", kindString),
	},
	transparent: true,
}, {
	origin: "std/startup", nomi: "Startup", goType: reflect.TypeFor[rt.Startup](), transparent: true,
	fields: []stdStructField{
		withDefault(stdMapField("env", func([]*typeDef) kind { return kindString }, func([]*typeDef) kind { return kindString }), stdEmptyMapDefault()),
		withDefault(stdStringListField("args"), stdEmptyListDefault()),
	},
}, {
	// What `io.replay` answers. A row so that its `impl Assertable` is
	// indexed by receiver kind and an `assert` over it selects that impl.
	origin: "std/io",
	nomi:   "Replayed",
	goType: reflect.TypeFor[rt.IOReplayed](),
	fields: []stdStructField{
		stdScalarField("output", kindString),
		stdScalarField("transcript", kindString),
		stdScalarField("expected", kindString),
		stdScalarField("unread", kindInt),
	},
	transparent: true,
}}

// The spec indices a row needs to name another row. Constants rather than
// literals because a row inserted above one of them would otherwise repoint
// every dependency silently; TestStdStructSpecIndicesNameTheirRow pins each
// constant against the row's own Nomi name, so a reorder fails by name.
//
// The guard is right but its DIAGNOSTIC PRECEDENCE is not. A mid-table
// insertion shifts these constants, and `TestStdStructSpecIndicesNameTheirRow`
// names every offence by row ("index 5 is Diagnostic, the constant says
// AssertionPipelineStage"). It is NOT what fails first:
// `TestStdStructSpecsMatchStdSource` and `TestStdStructSpecsAreReachable` fall
// over ahead of it and report missing std/assertions anchors, pointing at a
// file the change never touched. A shifted index breaks the DEPENDENT rows
// before it breaks the constant that describes them. So new rows go at the end
// of the table.
const (
	stdSpecAssertionPipelineStage = 5
	stdSpecAssertionValue         = 6
	stdSpecAssertionBinding       = 7
	stdSpecAssertionDetail        = 8
	stdSpecAssertionFailure       = 9
	// The five calendar value types, named by the carrier rows appended last.
	// Three of them are the first three rows in the table and one is not
	// adjacent to the other two, which is exactly why these are constants.
	stdSpecCalendarNaiveDateTime  = 0
	stdSpecCalendarDateTime       = 1
	stdSpecCalendarOffsetDateTime = 2
	stdSpecCalendarDate           = 13
	stdSpecCalendarTime           = 14
)

// stdStructDefs is the process-wide *typeDef per spec, keyed by spec index.
//
// Built once for the process, exactly like opaqueDefs and stdEnumDefs, and for
// the same pointer-identity reason.
var stdStructDefs = sync.OnceValue(func() []*typeDef {
	// TWO passes, because a row may name another: every shell exists before any
	// field kind is resolved, so `AssertionFailure.values` can be a List over
	// the `AssertionValue` def without either row having to be built first. A
	// row's kindOf reads THIS slice and never stdStructDefs(), so the OnceValue
	// cannot re-enter itself.
	defs := make([]*typeDef, len(stdStructSpecs))
	for i := range stdStructSpecs {
		s := &stdStructSpecs[i]
		defs[i] = &typeDef{nomi: s.nomi, lowerable: true, // rtDeclared, so typeDecl emits nothing for it: the Go type is
			// hand-written in rt, and a second declaration would be a second Go
			// type for one Nomi type.
			rtDeclared: true}
	}
	for i := range stdStructSpecs {
		for _, f := range stdStructSpecs[i].fields {
			// Never boxed: boxing is for a field that can reach its own
			// containing type at a fixed offset, and none here can -- the only
			// self-reference in this family is through a List, already a
			// pointer.
			//
			// `stdDeflt` rather than `deflt`, and the two are different
			// channels on purpose. `deflt` is an ast.Node LOWERED at the
			// construction site under inDeclScope, which is correct only while
			// the declaring and constructing scopes are the same one.
			// `stdDeflt` is a value stated in the builder's own vocabulary and
			// resolves no names, which is what makes it correct across the
			// module boundary a stdlib type always sits behind.
			d := defs[i]
			d.fields = append(d.fields, fieldDef{nomi: f.nomi, k: f.kindOf(defs), stdDeflt: f.deflt})
		}
	}
	return defs
})

// stdStructKindOfGoType is the kind an rt signature's Go type names, or
// kindInvalid.
//
// kindOfGoType's direction. It is what keeps `rt.DateTimeToString(rt.DateTime)
// string` from projecting onto anything but `(DateTime) -> String`.
func stdStructKindOfGoType(t reflect.Type) kind {
	for i := range stdStructSpecs {
		if stdStructSpecs[i].goType == t {
			return named(stdStructDefs()[i])
		}
	}
	return kindInvalid
}

// stdStructValidated is, per spec, whether STD ITSELF declares the type in the
// shape the spec describes — resolved ONCE against std.Load()'s own analysis of
// the declaring module rather than against whatever module is being lowered.
//
// # Why the per-module anchor is not enough
//
// stdStructAnchors resolves a spec through the LOWERED MODULE'S scope, so it
// answers only for a module that has the name in scope. That is the whole
// surface for a type a program writes down: `d: DateTime` needs `DateTime`
// imported. It is NOT the whole surface for a type the CHECKER solved. A file
// whose only mention of std/compiler is `compiler.check(src)` never names
// `Diagnostic` at all, and the `|diagnostic|` in `Iter.any?` over the result
// still has that type (as in
// tests/15-app-and-defer/os_env_boot_only_test.nomi).
//
// It is not enough for a SECOND reason, because fields need not be scalars:
// `AssertionFailure.values` is a
// `List<AssertionValue>`, and a program that imports `AssertionFailure` alone
// does not have `AssertionValue` in scope at all. Resolving the field through
// the importer would make the anchor depend on which names the importer chose
// to import — so the SHAPE CHECK RUNS HERE ONLY, in the declaring module, where
// every sibling row it can name is in scope by construction. The importer then
// asks nothing but identity: does this name resolve to (Origin, Name), and is
// that row validated.
//
// # Why identity is still sound
//
// The declaration is validated HERE, against std's real source, which is
// strictly stronger than validating it through an importer's scope: the shape
// check runs on the declaration the analyzer built the type from, and a std edit
// that renamed or reordered a field produces no anchor for anybody. What the
// lookup then compares is `(Origin, Name)` — the analyzer's own nominal
// identity rule, both halves — so a user module declaring its own
// `Diagnostic` has a different
// Origin and cannot collide.
//
// # A FIXED POINT, and it grows from nothing
//
// A row may name another in a field, so a row is admitted only once every row
// it names is already admitted. Seeded empty and grown, never retracted, so it
// settles in at most one round per spec and two rows that name each other and
// nothing else can never certify one another. Only SAME-ORIGIN rows are offered
// to a field check: a field naming a row from another std module is not
// something this table has, and admitting one would make the check depend on a
// name resolving in a module that does not declare it.
var stdStructValidated = sync.OnceValue(func() []bool {
	ok := make([]bool, len(stdStructSpecs))
	lib := stdAnchorLib()
	decls := make([]*ast.StructDef, len(stdStructSpecs))
	fas := make([]*analysis.FileAnalysis, len(stdStructSpecs))
	for i := range stdStructSpecs {
		s := &stdStructSpecs[i]
		fas[i] = lib.Files[strings.TrimPrefix(s.origin, "std/")]
		decls[i] = stdStructDeclIn(fas[i], s)
	}
	for changed := true; changed; {
		changed = false
		for i := range stdStructSpecs {
			if ok[i] || decls[i] == nil {
				continue
			}
			anchors := stdBaseAnchors(fas[i])
			anchors.structs = map[string]int{}
			for j := range stdStructSpecs {
				if ok[j] && stdStructSpecs[j].origin == stdStructSpecs[i].origin {
					anchors.structs[stdStructSpecs[j].nomi] = j
				}
			}
			if stdStructSpecs[i].matches(decls[i], anchors) {
				ok[i] = true
				changed = true
			}
		}
	}
	return ok
})

// stdStructDeclIn is the declaration a spec's name resolves to in fa, or nil.
//
// Identity only — (Origin, Name) and "it is a struct declaration". The SHAPE is
// stdStructValidated's question and is asked once, in the declaring module; see
// there for why an importer must not ask it.
func stdStructDeclIn(fa *analysis.FileAnalysis, s *stdStructSpec) *ast.StructDef {
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
	st, isStruct := sym.Type.(*analysis.StructType)
	if !isStruct || st.Origin != s.origin || st.Name != s.nomi {
		// The name resolves to something else here, or to a declaration in
		// another file of the same name. Either way there is no anchor and the
		// ordinary paths report.
		return nil
	}
	decl, isDecl := sym.Node.(*ast.StructDef)
	if !isDecl {
		return nil
	}
	return decl
}

// stdStructOfType is the shared def for a nominal type the ANALYZER solved,
// identified by (Origin, Name) with no module scope involved. This is the arm
// inferred.go's projection needs.
func stdStructOfType(origin, name string) (*typeDef, bool) {
	if origin == "" {
		return nil, false
	}
	validated := stdStructValidated()
	for i := range stdStructSpecs {
		if stdStructSpecs[i].origin == origin && stdStructSpecs[i].nomi == name && validated[i] {
			return stdStructDefs()[i], true
		}
	}
	return nil, false
}

// stdStructIndex is d's row in stdStructSpecs when d is that row's shared
// def, by pointer identity as stdEnumDef answers for enums.
func stdStructIndex(d *typeDef) (int, bool) {
	for i, sd := range stdStructDefs() {
		if sd == d {
			return i, true
		}
	}
	return 0, false
}

// --- anchoring -------------------------------------------------------------

// stdStructAnchors resolves the specs against one module's analysis: the
// declaration NODE each spec was reached through, and the spec index under the
// Nomi name.
//
// A free function rather than a gen method for opaqueAnchors' reason: the STDLIB
// SIGNATURE pass (collectStdCandidates, via stdTypeKind) runs before any gen
// exists and has to know whether the `DateTime` in
// `DateTime.with_zone(d: DateTime, zone: String): Result<DateTime, Error>` is a
// representable type. Two implementations of "is this the std type" is the drift
// the identity rules exist to prevent, so there is one.
//
// IDENTITY ONLY. The shape check is stdStructValidated's, asked once against the
// DECLARING module — see there for why an importer must not ask it, and note
// that this is strictly stronger rather than a relaxation: a row that fails the
// shape check anchors for nobody, where before it could in principle have
// anchored in the module that declared it and not in one that imported it.
func stdStructAnchors(fa *analysis.FileAnalysis) (map[*ast.StructDef]int, map[string]int) {
	byDecl := map[*ast.StructDef]int{}
	byName := map[string]int{}
	if fa == nil || fa.ModuleScope == nil {
		return byDecl, byName
	}
	validated := stdStructValidated()
	for i := range stdStructSpecs {
		if !validated[i] {
			continue
		}
		decl := stdStructDeclIn(fa, &stdStructSpecs[i])
		if decl == nil {
			continue
		}
		byDecl[decl] = i
		byName[stdStructSpecs[i].nomi] = i
	}
	return byDecl, byName
}

// matches reports whether decl is the declaration this spec describes.
//
// Everything checked here decides REPRESENTATION, and the FIELD checks are the
// load-bearing half: the builder constructs records and reads fields
// from the spec, so a rename, a reorder or a widened field type must produce NO
// anchor rather than lower against a layout nobody wrote. A permuted field is a
// wrong answer and not an error, which is the one failure the
// identity machinery cannot otherwise detect — the same argument stdEnumSpec's
// variant-order check rests on.
//
// `Opaque` is checked in BOTH DIRECTIONS against the spec's own `transparent`
// flag rather than simply required, because the two cases are different USE-SITE
// surfaces over the same representation and the spec is the place that says
// which one std declares. An opaque struct is built and read only by std's own
// functions; a transparent one is `d.message` in user code. Getting the
// direction wrong is not a compile error either way — a transparent
// `Diagnostic` anchored as opaque would still lower — so it is asserted here,
// where a std edit that added or dropped the keyword produces NO anchor and
// every mention refuses loudly.
//
// `Public` is NOT required. A file-private declaration cannot be the type
// another module names, but that is a reason the anchor is UNREACHABLE from
// elsewhere, not a reason to
// withhold it. Identity is `(Origin, Name)` and a private struct is absent from
// every other module's scope, so no importer can resolve one; the only gen that
// can is the declaring module's own, which is the one that needs it.
//
// std/calendar's boundary carriers need this. The FFI cannot carry
// `Result<_, Error>` for a five-variant `Error`, so each fallible constructor
// crosses through a module-private `DateAttempt`-shaped struct and the public
// API is a Nomi body over it. Without an anchor those signatures would have no
// representation, and `Date.parse`, `Date.from_fragments` and the `Date"…"`
// typed literal would refuse with them.
func (s *stdStructSpec) matches(decl *ast.StructDef, anchors stdAnchors) bool {
	if decl.Name != s.nomi || decl.Opaque == s.transparent {
		return false
	}
	if len(decl.TypeParams) > 0 || len(decl.WhereClauses) > 0 {
		// A generic std struct's identity depends on the instantiation, which
		// is prelude.go's problem and not this file's.
		return false
	}
	if len(decl.Items) > 0 {
		// A type body item is LAYOUT-RELEVANT — it can change what the type is,
		// so redirecting to an rt type would be a claim about a shape nobody
		// wrote.
		//
		// An attached `//!` test is deliberately NOT checked. The prompt is a
		// test case of its own, through tests.go's attachedTestCases, so
		// charging it to the type would double-charge it, and that charge
		// cascades: the type would lose its representation and every std
		// function whose signature names it would refuse `stdlib function
		// outside the scalar subset`. That is the `derive` argument in
		// declModifiers, applied to the other kind of metadata. In a module
		// where most data types carry a doc example, nearly every public
		// function names one of those types.
		return false
	}
	for _, dec := range decl.Decorators {
		// `derive` is the one decorator that is not a gap: it is ALSO
		// synthesized into an ordinary impl block that lowers through the
		// ordinary path, so it is metadata about work already done. Any other
		// decorator is a real refusal and must not be redirected past.
		// std/calendar carries three on NaiveDateTime (Equatable, Hashable,
		// Comparable) and none on DateTime, which hand-writes all three because
		// its identity is the instant alone.
		if dec.Name != "derive" {
			return false
		}
	}
	if len(decl.Fields) != len(s.fields) {
		return false
	}
	defs := stdStructDefs()
	for i, f := range decl.Fields {
		want := s.fields[i]
		// stdTypeKind rather than scalarKind, and it is the SAME resolver every
		// other stdlib signature position uses — so `List<AssertionValue>` in a
		// field is admitted on exactly the terms it is admitted in a parameter,
		// and a widened `List<String>` produces no anchor because the kinds are
		// two interned *compKinds and never equal.
		if f.Name != want.nomi || stdTypeKind(f.TypeAnnotation, anchors) != want.kindOf(defs) {
			return false
		}
		// A field default is an expression of the DECLARING module, evaluated
		// at every construction site. It is representable only where the spec
		// says WHICH expression it is, and the agreement is checked in BOTH
		// directions: a declared default with no spec row for it produces no
		// anchor, and so does a spec default the declaration stopped carrying.
		//
		// Both directions matter and neither is defensiveness. A declared
		// default the spec does not know would otherwise be silently DROPPED,
		// leaving Go's zero value at an omitted field -- and for `Maybe` that
		// is a value with Tag 0, neither Some nor None, which matches no arm of
		// a `case` rather than failing. A spec default the declaration no
		// longer carries would invent a value std never wrote.
		switch {
		case f.Default == nil && want.deflt != nil:
			return false
		case f.Default != nil && want.deflt == nil:
			return false
		case f.Default != nil && !want.deflt.shape(f.Default):
			return false
		}
	}
	return true
}

// loadStdStructs resolves the specs against this gen's module, once.
func (g *gen) loadStdStructs() {
	if g.stdStructsLoaded {
		return
	}
	g.stdStructsLoaded = true
	g.stdStructByDecl, g.stdStructByName = stdStructAnchors(g.fa)
	stdSynthStructAnchors(g.fa, g.stdStructByName)
}

// stdStructDeclared reports the spec index for a struct declaration this module
// carries, or -1. Read by buildTypes: an anchored declaration adopts the shared
// def instead of getting its own shell, which is what keeps std/calendar's own
// gen from declaring a second def for a type rt already owns.
func (g *gen) stdStructDeclared(decl *ast.StructDef) int {
	g.loadStdStructs()
	if i, anchored := g.stdStructByDecl[decl]; anchored {
		return i
	}
	return -1
}

// stdStructNamed resolves a type NAME to the shared def, for a module that
// MENTIONS one of these types without declaring it.
//
// The anchor was established through the module's own scope, which is what the
// checker resolves a bare type name through, so no per-mention re-check is
// needed — opaqueNamed's reasoning exactly. A module declaring its own
// `DateTime` never reaches this: namedType and typeOf consult g.types first, and
// buildTypes put the local declaration there.
func (g *gen) stdStructNamed(name string) (*typeDef, bool) {
	g.loadStdStructs()
	i, anchored := g.stdStructByName[name]
	if !anchored {
		return nil, false
	}
	return stdStructDefs()[i], true
}
