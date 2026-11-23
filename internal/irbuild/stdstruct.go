package irbuild

import (
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/nomi-language/nomi/rt"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// A stdlib record struct that rt also declares as a Go struct:
// `calendar.DateTime`, `compiler.Diagnostic` and the other rows of
// stdStructSpecs.
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
// opaqueSpec.matches rejects it: a field set is not an `inner` kind. So this
// is the same MECHANISM (an (Origin, Name) anchor against a validated
// declaration shape, a process-wide *typeDef marked `rtDeclared`, which every
// gen that names the type holds by the same pointer) over a different
// declaration kind.
//
// # Sharing the def, and why the argument carries over unchanged
//
// foreign.go rejects sharing a *typeDef across gens because "a shared def's
// field kinds are interned in the OWNER's g.comps". A row here is admissible
// exactly when every field kind is package-neutral. `DateTime`'s fields are
// Int and String, which are interned nowhere; see stdStructField.kindOf for
// the non-scalar fields.
//
// It has to be shared rather than merely may be, for opaque.go's reason: a
// stdFunc's parameter and result kinds are built once in stdCandidateFor and
// compared by POINTER against a call site's argument kinds in a different gen,
// so a per-gen def would make `DateTime.with_zone(d, z)` mismatch against
// itself.
//
// # What the shape check is FOR
//
// Every field's NAME, ORDER and written TYPE is checked against the spec, and
// the declaration must be `pub opaque struct` with no type parameters and no
// decorator but `derive`. That is not defensiveness: the builder
// constructs records and reads fields from the SPEC, so a std edit
// that renamed `zone`, reordered the fields, or changed `instant_nanos: Int` to
// `Float` would otherwise lower against a layout nobody wrote — and unlike a
// type error, a permuted field is a wrong ANSWER rather than an error. A declaration that fails the check produces no anchor, and every
// mention then refuses loudly through the ordinary paths.
//
// A row is held to std three ways, each by its own check:
//
//   - the SPELLING: each field's `decl` against the declaration's annotation as
//     written (typeText), in mismatch below. Syntax only, so it resolves no
//     type and needs no other row admitted first;
//   - the KIND: each field's kindOf against the checker's type for that
//     annotation, projected (TestStdSpecSpellingsAreTheirKinds);
//   - the GO TYPE: each field's kindOf against rt's field at the same index
//     (TestStdStructGoWidthMatchesTheDeclaredField).
//
// TestStdStructSpecsMatchStdSource is what keeps the check from being vacuous:
// without it a spec could drift from std and every guard here would pass
// against a declaration nothing anchors.
//
// stdStructField is one field of a stdlib record struct: the Nomi name, the
// type std writes for it, the kind it holds and its default.
type stdStructField struct {
	// nomi is the field name as std declares it, and what a field selector in
	// std's own source resolves through.
	nomi string
	// decl is the field's type as std's declaration writes it, in typeText's
	// form: `Int64`, `List<AssertionValue>`, `Map<String, String>`. A refinement
	// is written as itself (`month: Int64`), though its kind is its base's.
	decl string
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
	// `List<AssertionValue>` and `Option<String>` are built over scalars and
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
func stdScalarField(nomi, decl string, k kind) stdStructField {
	return stdStructField{nomi: nomi, decl: decl, kindOf: func([]*typeDef) kind { return k }}
}

// stdListField is a field holding `List<the spec at index i>`.
func stdListField(nomi, decl string, i int) stdStructField {
	return stdStructField{nomi: nomi, decl: decl, kindOf: func(defs []*typeDef) kind {
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
func stdStringListField(nomi, decl string) stdStructField {
	return stdStructField{nomi: nomi, decl: decl, kindOf: func([]*typeDef) kind {
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
func stdStructRowField(nomi, decl string, i int) stdStructField {
	return stdStructField{nomi: nomi, decl: decl, kindOf: func(defs []*typeDef) kind {
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
func stdListOfEnumField(nomi, decl string, i int) stdStructField {
	return stdStructField{nomi: nomi, decl: decl, kindOf: func([]*typeDef) kind {
		return listKindIn(nil, named(stdEnumShells()[i]))
	}}
}

// stdOptionField is a field holding `Option<k>`.
//
// Routed through sharedPreludeInstance rather than buildPreludeDef, so the def
// a field carries is the SAME pointer a signature position or a call site gets
// for the same instantiation — the identity rule stdprelude.go exists to state.
func stdOptionField(nomi, decl string, of func(defs []*typeDef) kind) stdStructField {
	return stdStructField{nomi: nomi, decl: decl, kindOf: func(defs []*typeDef) kind {
		k, shared := sharedPreludeInstance(preludeSpecFor("std/option", "Option"), []kind{of(defs)})
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
// the table being built, exactly as stdOptionField's `of` does. Both must be
// package-neutral: a non-neutral component with a nil gen panics in internComp
// rather than yielding a kind interned nowhere, which is the loud version of
// the failure that guard exists for.
func stdMapField(nomi, decl string, key, val func(defs []*typeDef) kind) stdStructField {
	return stdStructField{nomi: nomi, decl: decl, kindOf: func(defs []*typeDef) kind {
		return mapKindIn(nil, key(defs), val(defs))
	}}
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
// By NAME rather than by index: a positional reference from a field row
// would be a second encoding of stdHostSpecs' order,
// and that table's own comments say rows are APPENDED precisely so nothing
// shifts.
//
// kindInvalid when no row matches, which propagates as a refusal rather than a
// field whose kind is a lie.
func stdHostOriginKind(origin, nomi string) kind {
	if i, ok := stdHostOriginRows()[[2]string{origin, nomi}]; ok {
		return stdHostKind(i)
	}
	return kindInvalid
}

// stdHostOriginRows indexes stdHostSpecs by (origin, name), the first row
// winning as a scan would. The table is fixed for the process.
var stdHostOriginRows = sync.OnceValue(func() map[[2]string]int {
	rows := map[[2]string]int{}
	for i := range stdHostSpecs {
		key := [2]string{stdHostSpecs[i].origin, stdHostSpecs[i].nomi}
		if _, dup := rows[key]; !dup {
			rows[key] = i
		}
	}
	return rows
})

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
// # Why an empty List or Map default states no element kind
//
// The builder makes the empty collection at the field's kind (emptyOf,
// irstdfielddefault.go). The default states no kind of its own, so it cannot
// disagree with the field it fills.
type stdFieldDefault struct {
	// shape reports whether std's declared default is the expression `lit`
	// states. Structural rather than textual: this package has no
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

// stdNoneDefault is `= None` on an Option field, the spelling std writes it in.
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

// stdEmptyMapDefault is `= {}` on a Map field.
//
// The builder makes the empty map at the field's key and value kinds
// (emptyOf, irstdfielddefault.go). The default states no kinds of its own,
// so it cannot disagree with the field it fills.
func stdEmptyMapDefault() *stdFieldDefault {
	return &stdFieldDefault{shape: func(n ast.Node) bool {
		m, isMap := n.(*ast.MapLit)
		return isMap && len(m.Entries) == 0 && len(m.Spreads) == 0 && m.TypeName == nil
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
	// It is a declared expectation rather than a relaxation, and mismatch()
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
// name is scaffolding. TestStdEnumSpecsAreReachable asserts the same over
// the corpus for the enum family.
var stdStructSpecs = []stdStructSpec{{
	origin: "std/calendar",
	nomi:   "NaiveDateTime",
	goType: reflect.TypeFor[rt.NaiveDateTime](),
	fields: []stdStructField{
		stdScalarField("year", "Int", kindInt),
		stdScalarField("month", "Int64", kindInt),
		stdScalarField("day", "Int64", kindInt),
		stdScalarField("hour", "Int64", kindInt),
		stdScalarField("minute", "Int64", kindInt),
		stdScalarField("second", "Int64", kindInt),
		stdScalarField("nanosecond", "Int64", kindInt),
	},
}, {
	origin: "std/calendar",
	nomi:   "DateTime",
	goType: reflect.TypeFor[rt.DateTime](),
	fields: []stdStructField{
		stdScalarField("instant_nanos", "Int", kindInt),
		stdScalarField("zone", "String", kindString),
	},
}, {
	origin: "std/calendar",
	nomi:   "OffsetDateTime",
	goType: reflect.TypeFor[rt.OffsetDateTime](),
	fields: []stdStructField{
		stdScalarField("instant_nanos", "Int", kindInt),
		stdScalarField("offset_seconds", "Int64", kindInt),
	},
}, {
	// std/compiler's diagnostic record: the whole payload of `compiler.check`,
	// and the first row here that std declares TRANSPARENT.
	//
	// Its Go type is in rt, like every other row's, even though the function
	// that produces it is in internal/stdcompiler and reaches the analyzer.
	origin: "std/compiler",
	nomi:   "Diagnostic",
	goType: reflect.TypeFor[rt.Diagnostic](),
	fields: []stdStructField{
		stdScalarField("line", "Int", kindInt),
		stdScalarField("col", "Int", kindInt),
		stdScalarField("message", "String", kindString),
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
	// names and their order are held to std's declaration as everywhere else
	// here.
	origin: "std/compiler",
	nomi:   "Hover",
	goType: reflect.TypeFor[rt.Hover](),
	fields: []stdStructField{
		stdScalarField("signature", "String", kindString),
		stdScalarField("markdown", "String", kindString),
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
		stdScalarField("expression", "String", kindString),
		stdScalarField("value", "String", kindString),
	},
	transparent: true,
}, {
	origin: "std/assertions",
	nomi:   "AssertionValue",
	goType: reflect.TypeFor[rt.NomiAssertionValue](),
	fields: []stdStructField{
		stdScalarField("expression", "String", kindString),
		stdScalarField("value", "String", kindString),
		stdListField("pipeline", "List<AssertionPipelineStage>", stdSpecAssertionPipelineStage),
	},
	transparent: true,
}, {
	origin: "std/assertions",
	nomi:   "AssertionBinding",
	goType: reflect.TypeFor[rt.NomiAssertionBinding](),
	fields: []stdStructField{
		stdScalarField("name", "String", kindString),
		stdScalarField("expression", "String", kindString),
		stdScalarField("value", "String", kindString),
		stdListField("pipeline", "List<AssertionPipelineStage>", stdSpecAssertionPipelineStage),
	},
	transparent: true,
}, {
	origin: "std/assertions",
	nomi:   "AssertionDetail",
	goType: reflect.TypeFor[rt.NomiAssertionDetail](),
	fields: []stdStructField{
		stdScalarField("label", "String", kindString),
		stdScalarField("value", "String", kindString),
	},
	transparent: true,
}, {
	origin: "std/assertions",
	nomi:   "AssertionFailure",
	goType: reflect.TypeFor[rt.NomiAssertionFailure](),
	fields: []stdStructField{
		stdScalarField("line", "Int64", kindInt),
		stdScalarField("keyword", "String", kindString),
		stdScalarField("expression", "String", kindString),
		stdScalarField("reason", "String", kindString),
		stdOptionField("actual", "Option<String>", func([]*typeDef) kind { return kindString }),
		stdOptionField("binding", "Option<AssertionBinding>", func(defs []*typeDef) kind {
			return named(defs[stdSpecAssertionBinding])
		}),
		stdListField("values", "List<AssertionValue>", stdSpecAssertionValue),
		stdListField("details", "List<AssertionDetail>", stdSpecAssertionDetail),
		// A `subtests` statement's failing rows. The only field that
		// names a row placed after this one, and the only cycle in the
		// table: a SubtestFailure holds an AssertionFailure. Both are
		// pointers at run time, through the List, and stdStructDefs builds
		// every shell before any field.
		withDefault(stdListField("subtests", "List<SubtestFailure>", stdSpecSubtestFailure), stdEmptyListDefault()),
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
		withDefault(stdScalarField("reason", "String", kindString),
			stdStringDefault("assertion failed")),
		withDefault(stdOptionField("actual", "Option<String>", func([]*typeDef) kind { return kindString }),
			stdNoneDefault()),
		withDefault(stdOptionField("expected", "Option<String>", func([]*typeDef) kind { return kindString }),
			stdNoneDefault()),
		withDefault(stdListField("details", "List<AssertionDetail>", stdSpecAssertionDetail),
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
	nomi:   "Error",
	goType: reflect.TypeFor[rt.DynamicDecodeError](),
	fields: []stdStructField{
		stdListOfEnumField("path", "List<PathSegment>", stdEnumPathSegment),
		stdScalarField("expected", "String", kindString),
		stdScalarField("got", "String", kindString),
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
	//   - `manifest` is an `Option<String>` with a default of `None`, which a
	//     literal that omits the field takes.
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
		stdScalarField("entry_point", "String", kindString),
		stdMapField("files", "Map<String, String>",
			func([]*typeDef) kind { return kindString },
			func([]*typeDef) kind { return kindString }),
		withDefault(stdOptionField("manifest", "Option<String>", func([]*typeDef) kind {
			return kindString
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
	// these rows: `literal.go` resolves the handler,
	// and a signature has to be able to NAME the result type.
	origin: "std/calendar",
	nomi:   "Date",
	goType: reflect.TypeFor[rt.Date](),
	fields: []stdStructField{
		stdScalarField("year", "Int", kindInt),
		stdScalarField("month", "Int64", kindInt),
		stdScalarField("day", "Int64", kindInt),
	},
}, {
	// A SEPARATE row from Date rather than a shared shape, and the reason is
	// semantic rather than tidiness: std gives each its own `derive
	// Equatable`/`Hashable`/`Comparable`, which generate over the fields that
	// EXIST. One record with both field sets would make `Date"2026-05-04"`
	// structurally equal to `NaiveDateTime"2026-05-04T00:00:00"` — a wrong
	// ANSWER, not a wasted field.
	origin: "std/calendar",
	nomi:   "Time",
	goType: reflect.TypeFor[rt.Time](),
	fields: []stdStructField{
		stdScalarField("hour", "Int64", kindInt),
		stdScalarField("minute", "Int64", kindInt),
		stdScalarField("second", "Int64", kindInt),
		stdScalarField("nanosecond", "Int64", kindInt),
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
	// process-wide intern, and the DEFAULT is the one new piece — `{}`
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
		stdScalarField("entry_point", "String", kindString),
		withDefault(stdMapField("env", "Map<String, String>",
			func([]*typeDef) kind { return kindString },
			func([]*typeDef) kind { return kindString }), stdEmptyMapDefault()),
	},
	transparent: true,
}, {
	// std/json's structured PARSE failure, a row whose Nomi name is DOTTED.
	// That is not a new mechanism: `fa.ModuleScope.Lookup` binds
	// `Json.DecodeError` to the declaration with `Origin: "std/json"` and
	// `Name: "Json.DecodeError"`, so the (origin, name) anchor works on the whole
	// spelling exactly as it does on a bare one.
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
		stdScalarField("message", "String", kindString),
		stdScalarField("line", "Int", kindInt),
		stdScalarField("col", "Int", kindInt),
		stdScalarField("offset", "Int", kindInt),
	},
	transparent: true,
}, {
	// std/json's SHAPE failure, which is a different type from the one above and
	// std says why: a parse error is `Json.DecodeError` and a shape error describes
	// the PATH inside a well-formed tree where a typed conversion failed. One
	// record with both field sets would let the builder construct either shape
	// for either function — a wrong ANSWER rather than an error, the same
	// argument the `Project`/`RunFile` pair rests on.
	//
	// `Result<Json.ShapeError>` is reached from
	// 11/module_qualified_impl_dispatch, 18/json_container_decode and
	// 18/json_derive.
	//
	// `path: List<String>` needs no new machinery either: its element is a
	// scalar, so stdStringListField's nil-gen process-wide intern covers it,
	// as stdListField's does for `AssertionFailure.values`.
	origin: "std/json",
	nomi:   "Json.ShapeError",
	goType: reflect.TypeFor[rt.JsonShapeError](),
	fields: []stdStructField{
		stdStringListField("path", "List<String>"),
		stdScalarField("expected", "String", kindString),
		stdScalarField("got", "String", kindString),
	},
	transparent: true,
}, {
	// std/calendar's five FALLIBLE-CONSTRUCTOR CARRIERS, and they are one
	// decision rather than five rows.
	//
	// WHY THEY EXIST AT ALL. A generated adapter projects a Go function's
	// error result as a `Result<_, E>` only for `E` = `String`
	// (internal/hostgen, "Projection rows"), so std/calendar's
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
		stdStructRowField("value", "Date", stdSpecCalendarDate),
		stdScalarField("kind", "Int", kindInt),
		stdScalarField("reason", "String", kindString),
	},
	transparent: true,
}, {
	origin: "std/calendar",
	nomi:   "TimeAttempt",
	goType: reflect.TypeFor[rt.CalendarTimeResult](),
	fields: []stdStructField{
		stdStructRowField("value", "Time", stdSpecCalendarTime),
		stdScalarField("kind", "Int", kindInt),
		stdScalarField("reason", "String", kindString),
	},
	transparent: true,
}, {
	origin: "std/calendar",
	nomi:   "NaiveAttempt",
	goType: reflect.TypeFor[rt.CalendarNaiveResult](),
	fields: []stdStructField{
		stdStructRowField("value", "NaiveDateTime", stdSpecCalendarNaiveDateTime),
		stdScalarField("kind", "Int", kindInt),
		stdScalarField("reason", "String", kindString),
	},
	transparent: true,
}, {
	origin: "std/calendar",
	nomi:   "OffsetAttempt",
	goType: reflect.TypeFor[rt.CalendarOffsetResult](),
	fields: []stdStructField{
		stdStructRowField("value", "OffsetDateTime", stdSpecCalendarOffsetDateTime),
		stdScalarField("kind", "Int", kindInt),
		stdScalarField("reason", "String", kindString),
	},
	transparent: true,
}, {
	origin: "std/calendar",
	nomi:   "ZonedAttempt",
	goType: reflect.TypeFor[rt.CalendarZonedResult](),
	fields: []stdStructField{
		stdStructRowField("value", "DateTime", stdSpecCalendarDateTime),
		stdScalarField("kind", "Int", kindInt),
		stdScalarField("reason", "String", kindString),
	},
	transparent: true,
}, {
	origin: "std/startup", nomi: "Startup", goType: reflect.TypeFor[rt.Startup](), transparent: true,
	fields: []stdStructField{
		withDefault(stdMapField("env", "Map<String, String>", func([]*typeDef) kind { return kindString }, func([]*typeDef) kind { return kindString }), stdEmptyMapDefault()),
		withDefault(stdStringListField("args", "List<String>"), stdEmptyListDefault()),
	},
}, {
	// What `io.replay` answers. A row so that its `impl Assertable` is
	// indexed by receiver kind and an `assert` over it selects that impl.
	origin: "std/io",
	nomi:   "Replayed",
	goType: reflect.TypeFor[rt.IOReplayed](),
	fields: []stdStructField{
		stdScalarField("output", "String", kindString),
		stdScalarField("transcript", "String", kindString),
		stdScalarField("expected", "String", kindString),
		stdScalarField("unread", "Int", kindInt),
	},
	transparent: true,
}, {
	// One failing row of a `subtests` statement, `AssertionFailure.subtests`'
	// element. Last, for the reason the constants below give.
	origin: "std/assertions",
	nomi:   "SubtestFailure",
	goType: reflect.TypeFor[rt.NomiSubtestFailure](),
	fields: []stdStructField{
		stdScalarField("row", "String", kindString),
		stdStructRowField("failure", "AssertionFailure", stdSpecAssertionFailure),
	},
	transparent: true,
}, {
	// std/locale's fallible-constructor carrier, the calendar carriers'
	// arrangement: `Locale.parse` and `Locale.from_posix` cross as data and
	// locale.nomi's `Locale.lift` rebuilds the `Error` variant. The
	// value is the canonical tag's String, so every field is a scalar.
	origin: "std/locale",
	nomi:   "LocaleAttempt",
	goType: reflect.TypeFor[rt.LocaleResult](),
	fields: []stdStructField{
		stdScalarField("value", "String", kindString),
		stdScalarField("kind", "Int", kindInt),
		stdScalarField("detail", "String", kindString),
	},
	transparent: true,
}, {
	// std/literals' LiteralError, what a CheckedLiteral's check answers when
	// it refuses a literal's text. std/literals is lowered once for the
	// process, so its struct needs this row; Display and Equatable are
	// ordinary Nomi in std/literals.nomi.
	origin: "std/literals",
	nomi:   "LiteralError",
	goType: reflect.TypeFor[rt.LiteralError](),
	fields: []stdStructField{
		stdScalarField("fragment", "Int", kindInt),
		stdScalarField("offset", "Int", kindInt),
		stdScalarField("message", "String", kindString),
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
// `TestStdStructSpecsMatchStdSource` falls
// over ahead of it and reports missing std/assertions anchors, pointing at a
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
	// The last row, named by AssertionFailure.subtests.
	stdSpecSubtestFailure = 25
	// std/json's two errors, the payloads of the `Json.Error` enum row.
	stdSpecJsonDecodeError = 16
	stdSpecJsonShapeError  = 17
)

// stdStructDefs is the process-wide *typeDef per spec, keyed by spec index.
//
// Built once for the process, exactly like opaqueDefs and stdEnumDefs, and for
// the same pointer-identity reason.
var stdStructDefs = sync.OnceValue(func() []*typeDef {
	defer changingTypes()()
	// TWO passes, because a row may name another: every shell exists before any
	// field kind is resolved, so `AssertionFailure.values` can be a List over
	// the `AssertionValue` def without either row having to be built first. A
	// row's kindOf reads THIS slice and never stdStructDefs(), so the OnceValue
	// cannot re-enter itself.
	defs := stdStructShells()
	for i := range stdStructSpecs {
		for _, f := range stdStructSpecs[i].fields {
			// Never boxed: boxing is for a field that can reach its own
			// containing type at a fixed offset, and none here can -- the only
			// self-reference in this family is through a List, already a
			// pointer.
			//
			// `stdDeflt` rather than `deflt`, and the two are different
			// channels on purpose. `deflt` is an ast.Node LOWERED at the
			// construction site, which is correct only while
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

// stdStructShells is the first pass of stdStructDefs on its own: one def
// per spec with its identity and no fields. stdEnumDefs names a row's
// shell for an enum payload (`Json.Error.Decode`'s `Json.DecodeError`)
// while stdStructDefs may name an enum's def for a field (`Error.path`'s
// `PathSegment`), so each table's second pass reads the other's shells and
// never the other's OnceValue, and neither can re-enter the other.
// stdStructDefs fills these same pointers.
var stdStructShells = sync.OnceValue(func() []*typeDef {
	defs := make([]*typeDef, len(stdStructSpecs))
	for i := range stdStructSpecs {
		s := &stdStructSpecs[i]
		defs[i] = &typeDef{nomi: s.nomi, lowerable: true, // rtDeclared: rt declares the type, so the def is process-wide and
			// package-neutral, and typeDecl replays no refusal for it.
			rtDeclared: true}
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
// `Diagnostic` at all, and the `fn diagnostic ->` in `Iter.any_match` over the result
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
// # Each row on its own
//
// A row may name another in a field, and two name each other:
// `AssertionFailure.subtests` is a `List<SubtestFailure>` and a
// `SubtestFailure` holds an `AssertionFailure`. The check compares each
// field's written spelling and resolves no type, so a row is checked without
// any other row admitted first, and the answer is one pass over the table.
var stdStructValidated = perStd(func() []bool {
	ok := make([]bool, len(stdStructSpecs))
	lib := stdAnchorLib()
	for i := range stdStructSpecs {
		s := &stdStructSpecs[i]
		decl := stdStructDeclIn(lib.Files[strings.TrimPrefix(s.origin, "std/")], s)
		ok[i] = decl != nil && s.mismatch(decl) == ""
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
// IDENTITY ONLY. The shape check is stdStructValidated's, asked once against the
// DECLARING module — see there for why an importer must not ask it, and note
// that this is strictly stronger rather than a relaxation: a row that fails the
// shape check anchors for nobody, where before it could in principle have
// anchored in the module that declared it and not in one that imported it.
func stdStructAnchors(fa *analysis.FileAnalysis) map[*ast.StructDef]int {
	byDecl := map[*ast.StructDef]int{}
	if fa == nil || fa.ModuleScope == nil {
		return byDecl
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
	}
	return byDecl
}

// mismatch is how decl differs from the declaration this spec describes, and
// "" when it does not: a sentence naming the row, and for a field the field
// and both spellings, which TestStdStructSpecsMatchStdSource reports.
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
// std/calendar's boundary carriers need this. A generated adapter cannot
// carry `Result<_, Error>` for a five-variant `Error`, so each fallible constructor
// crosses through a module-private `DateAttempt`-shaped struct and the public
// API is a Nomi body over it. Without an anchor those signatures would have no
// representation, and `Date.parse`, `Date.from_fragments` and the `Date"…"`
// typed literal would refuse with them.
func (s *stdStructSpec) mismatch(decl *ast.StructDef) string {
	row := s.origin + "." + s.nomi
	if decl.Name != s.nomi {
		return row + ": std declares " + decl.Name
	}
	if decl.Opaque == s.transparent {
		return row + ": std's declaration and the row disagree about `opaque`"
	}
	if len(decl.TypeParams) > 0 || len(decl.WhereClauses) > 0 {
		// A generic std struct's identity depends on the instantiation, which
		// is prelude.go's problem and not this file's.
		return row + ": std declares it generic"
	}
	// An attached `//!` test is not checked. The prompt is a test case of its
	// own, through tests.go's attachedTestCases, so charging it to the type
	// would charge it twice, and that charge cascades: the type would lose
	// its representation and every std function whose signature names it
	// would refuse `stdlib function outside the scalar subset`. That is the
	// `derive` argument in declModifiers, applied to the other kind of
	// metadata. In a module where most data types carry a doc example, nearly
	// every public function names one of those types.
	for _, dec := range decl.Decorators {
		// `derive` is the one decorator that is not a gap: it is ALSO
		// synthesized into an ordinary impl block that lowers through the
		// ordinary path, so it is metadata about work already done. Any other
		// decorator is a real refusal and must not be redirected past.
		// std/calendar carries three on NaiveDateTime (Equatable, Hashable,
		// Comparable) and none on DateTime, which hand-writes all three because
		// its identity is the instant alone.
		if dec.Name != "derive" {
			return row + ": std's declaration carries the decorator " + dec.Name
		}
	}
	if len(decl.Fields) != len(s.fields) {
		return fmt.Sprintf("%s: std declares %d fields, the row %d", row, len(decl.Fields), len(s.fields))
	}
	for i, f := range decl.Fields {
		want := s.fields[i]
		if f.Name != want.nomi {
			return fmt.Sprintf("%s: field %d is %s in std, %s in the row", row, i, f.Name, want.nomi)
		}
		// The written spelling, compared by typeText as every std spec
		// compares a written type. A widened `List<String>` or a refinement
		// swapped for its base is a different spelling.
		if got := typeText(f.TypeAnnotation); got != want.decl {
			return fmt.Sprintf("%s.%s: std writes `%s`, the row says `%s`", row, f.Name, got, want.decl)
		}
		// A field default is an expression of the DECLARING module, evaluated
		// at every construction site. It is representable only where the spec
		// says WHICH expression it is, and the agreement is checked in BOTH
		// directions: a declared default with no spec row for it produces no
		// anchor, and so does a spec default the declaration stopped carrying.
		//
		// Both directions matter. A declared default the spec does not know
		// would be dropped, so a literal that omits the field would not get
		// the value std declares. A spec default the declaration does not
		// carry would invent a value std never wrote.
		switch {
		case f.Default == nil && want.deflt != nil:
			return row + "." + f.Name + ": the row has a default std does not declare"
		case f.Default != nil && want.deflt == nil:
			return row + "." + f.Name + ": std declares a default the row does not have"
		case f.Default != nil && !want.deflt.shape(f.Default):
			return row + "." + f.Name + ": std's default is not the row's"
		}
	}
	return ""
}

// loadStdStructs resolves the specs against this gen's module, once.
func (g *gen) loadStdStructs() {
	if g.stdStructsLoaded {
		return
	}
	g.stdStructsLoaded = true
	g.stdStructByDecl = stdStructAnchors(g.fa)
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
