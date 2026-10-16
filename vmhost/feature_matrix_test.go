package vmhost

import (
	"fmt"
	"math/rand"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/format"
	"github.com/nomi-language/nomi/internal/rotation"
)

// The feature × context matrix. The random generator (lowering_gen_test.go)
// and the fuzzers find a gap by chance, near the inputs they start from.
// This test finds one on purpose: it puts each feature, a small construct
// with a known value, into each context, a place such a value can stand,
// and checks every program that makes. Most of the gaps the decline
// inventory found were one ordinary feature in a context the generator
// rarely builds, so the matrix crosses them all.
//
// A cell is a feature, a chain of wrapping contexts (innermost first), a
// shell and a placement:
//
//   - A feature (mxFeatures) is a typed expression, the declarations it
//     needs, an observation that turns its value into text, and that text.
//   - A wrapping context (mxWraps) takes an expression of the feature's
//     type and answers another of the same type that holds it somewhere:
//     a binding, an argument, a `case` arm, a list element, a lambda body,
//     a `once`, a task body, and so on. Wrapping contexts compose, which
//     makes the triples.
//   - A shell (mxShells) is where the program observes the final value:
//     `fn main` (the default), a test body, an `assert` subject, a
//     `subtests` row, a `defer`'d call, or an application field written by
//     `with`.
//   - A placement (mxPlacements) says which file holds what: one file (the
//     default), the feature's declarations in a second file, or the whole
//     value made by a function of a second file.
//
// Every cell's program either is rejected by the checker with the text the
// matrix expects for it (mxExpectedRejection) or lowers and runs, printing
// exactly the feature's value; a `main` program also runs from the image
// `nomi build` writes. The test fails on an unexpected rejection, an
// accepted program the builder declines, a compiler panic, an expression the
// checker leaves untyped, a crash, and a wrong transcript.
//
// Which cells the checker rejects follows from two columns: a feature whose
// type its position must supply is tagged (tagUntyped, tagExpects,
// tagPartial), and each wrapping context says what it gives that type
// (mxTyping). mxCheckerRules lists the rejections outside that walk, each
// with its reason, and the checker holes the matrix pins as accepted.
//
// A failure a known gap explains is excused: a decline whose reasons a
// knownLoweringGaps entry names, a panic, an untyped expression or a crash
// its list names, and, for a gap whose reason the builder derives from the
// node alone, the cells mxKnown matches by shape. Pin a new finding as a gap
// with a reproducer (lowering_gaps_matrix_test.go), never by widening a
// rule.
//
// The pairs (each feature in each context, shell and placement alone) all
// run under plain `go test`. The triples (a fragile feature in a fragile
// context and one more) are many more: every mxTripleSample-th runs by
// default, NOMI_MATRIX=full runs them all (about a minute), and
// TestFeatureContextMatrixRotating checks 60 more that change every UTC
// day. NOMI_MATRIX_ONLY=<text> checks only the cells whose name contains
// text, such as "unit × with".
//
// To extend it, add one entry to mxFeatures, mxWraps (with its mxTyping),
// mxShells or mxPlacements. A feature the type system cannot put in some
// context gets a tag or a rule that says why.

// mxFeature is one construct with a known value.
type mxFeature struct {
	name string
	// decls are its top-level declarations, if any.
	decls string
	// typ is its value's type, as a binding annotation spells it.
	typ string
	// expr computes the value. It may span lines.
	expr string
	// obs turns the value, written $v, into a String; empty means
	// Debug.inspect($v).
	obs string
	// want is the text obs answers, as io.print prints it.
	want string
	// tags sort the feature for mxExpectedRejection and for the triples.
	tags mxTags
	// reject matches the checker's rejection of a tagUntyped, tagExpects
	// or tagPartial feature where nothing gives it its type.
	reject string
}

// mxUnsolved matches the rejection of a value whose type has a part nothing
// solves, bound where its type must be determined.
const mxUnsolved = "not locally determined|is not determined"

// mxRows matches the rejection of `subtests` rows whose type has an open
// part.
const mxRows = "the type of the rows of `subtests` is not determined"

type mxTags uint32

const (
	// tagUntyped: the expression's type has a part only its position
	// solves (`[]`, `None`, `Tree.Nil`). A generic call solves it
	// by unification, or leaves it unsolved where no value has that part,
	// and a plain binding rejects it.
	tagUntyped mxTags = 1 << iota
	// tagExpects: the expression is checked against the type its position
	// expects, and has none without one (`.Circle(1.0)`, `|n| n + 1`,
	// `Display.to_string`, a list of two implementors of an interface).
	// Unification after the fact does not help it.
	tagExpects
	// tagBareOK: the tagUntyped or tagExpects feature is accepted as
	// Debug.inspect's argument with nothing else around it (the assert
	// subject shell): the part nothing solves is a part no value
	// has, or the observation's call solves it.
	tagBareOK
	// tagPartial: a type argument stays open where no value has it
	// (`Ok(1)` is a Result<Int, ?>), which only `subtests`, whose rows
	// must have a whole type, rejects.
	tagPartial
	// tagWidens: the expression's own type is narrower than typ (an
	// embedded type, an interface implementor), so where nothing expects
	// typ it keeps its own.
	tagWidens
	// tagFn: the value is a function.
	tagFn
	// tagUnit, tagExistential, tagIter: the value is Unit, an interface
	// value, an Iter.
	tagUnit
	tagExistential
	tagIter
	// tagCall: the feature is a call with named arguments, defaults or
	// placeholders, or a call qualified other than by its file.
	tagCall
	// tagDbg: the program prints a `dbg` line, which the transcript
	// comparison leaves out.
	tagDbg
	// tagNested: the feature holds statements nested in an expression.
	tagNested
)

func (t mxTags) has(u mxTags) bool { return t&u != 0 }

// fragile are the features the triples cross: the kinds the decline
// inventory found most of its gaps with.
const mxFragile = tagUntyped | tagExpects | tagWidens | tagFn | tagUnit | tagExistential | tagIter | tagCall | tagNested

const (
	mxPoint = "struct Point {\n    x: Int\n    y: Int = 2\n}"
	mxShape = "struct Spot {\n    n: Int\n}\n\ntype Tag String\n\nenum Shape {\n    embeds Spot\n    embeds Tag\n    Circle Float\n    Rect {w: Int, h: Int}\n    Dot\n}"
	mxNamed = "interface Named {\n    fn name(value: self): String\n    fn shout(value: self): String {\n        name(value) + \"!\"\n    }\n}\n\nstruct Pt {\n    x: Int\n}\n\nimpl Named for Pt {\n    fn name(p: Pt): String {\n        \"pt${p.x}\"\n    }\n}"
	mxAdd3  = "fn add3(a: Int, b: Int, c: Int = 100): Int {\n    a + b + c\n}"
	mxGreet = "fn greet(name: String, punct: String = \"!\", times: Int = 1): String {\n    \"${name}${punct}${times}\"\n}"
	mxBox   = "struct Box<T> {\n    v: T\n}"
	mxTree  = "enum Tree<T> {\n    Leaf T\n    Nil\n}"
	mxCoin  = "enum Coin {\n    Penny\n    Dime\n}"
)

var mxFeatures = []mxFeature{
	// Literals.
	{name: "int", typ: "Int", expr: "42", want: "42"},
	{name: "float", typ: "Float", expr: "1.5", want: "1.5"},
	{name: "decimal", typ: "Decimal", expr: "2.25d", want: "2.25d"},
	{name: "string", typ: "String", expr: `"hi"`, want: `"hi"`},
	{name: "interpolation", typ: "String", expr: `"n=${40 + 2}"`, want: `"n=42"`},
	{name: "bool", typ: "Bool", expr: "True", want: "True"},
	{name: "empty list", typ: "List<Int>", expr: "[]", want: "[]", tags: tagUntyped | tagBareOK, reject: mxUnsolved},
	{name: "list", typ: "List<Int>", expr: "[1, 2]", want: "[1, 2]"},
	{name: "list spread", typ: "List<Int>", expr: "[0, ..[1, 2]]", want: "[0, 1, 2]"},
	{name: "empty vector", typ: "Vector<Int>", expr: "#[]", want: "#[]", tags: tagUntyped | tagBareOK, reject: mxUnsolved},
	{name: "vector", typ: "Vector<Int>", expr: "#[1, 2]", want: "#[1, 2]"},
	{name: "empty set", typ: "Set<Int>", expr: "#{}", want: "#{}", tags: tagUntyped | tagBareOK, reject: mxUnsolved},
	{name: "set", typ: "Set<Int>", expr: "#{3, 4}", want: "#{3, 4}"},
	{name: "map", typ: "Map<String, Int>", expr: `{"a" => 1}`, want: `{"a" => 1}`},
	{name: "map spread", typ: "Map<String, Int>", expr: `{..{"a" => 1}, "b" => 2}`, want: `{"a" => 1, "b" => 2}`},
	{name: "empty map", typ: "Map<String, Int>", expr: "Map.empty()", want: "{=>}", tags: tagUntyped | tagBareOK, reject: mxUnsolved},
	{name: "tuple", typ: "(Int, String)", expr: `(1, "a")`, want: `(1, "a")`},
	{name: "record", typ: "{a: Int, b: String}", expr: `{a: 1, b: "x"}`, want: `{a: 1, b: "x"}`},
	{name: "range", typ: "Range<Int>", expr: "1..4", want: "1..4"},

	// Declared types.
	{name: "struct", decls: mxPoint, typ: "Point", expr: "Point{x: 1}", want: "Point{x: 1, y: 2}"},
	{name: "enum bare variant", decls: mxShape, typ: "Shape", expr: "Shape.Dot", want: "Dot"},
	{name: "enum payload variant", decls: mxShape, typ: "Shape", expr: "Shape.Circle(1.5)", want: "Circle(1.5)"},
	{name: "enum struct variant", decls: mxShape, typ: "Shape", expr: "Shape.Rect{w: 1, h: 2}", want: "Rect{w: 1, h: 2}"},
	{name: "embedded struct", decls: mxShape, typ: "Shape", expr: "Spot{n: 3}", want: "Spot{n: 3}", tags: tagWidens},
	{name: "embedded distinct", decls: mxShape, typ: "Shape", expr: `Tag("t")`, want: `Tag("t")`, tags: tagWidens},
	{name: "dot variant", decls: mxShape, typ: "Shape", expr: ".Circle(2.0)", want: "Circle(2.0)", tags: tagExpects,
		reject: "requires a determinable enum type|resolves only to enum variants|cannot be resolved here|is not determined"},
	{name: "distinct", decls: "type Meters Int", typ: "Meters", expr: "Meters(3)", want: "Meters(3)"},
	{name: "unit", typ: "Unit", expr: "Unit", want: "Unit", tags: tagUnit},
	{name: "generic struct", decls: mxBox, typ: "Box<Int>", expr: "Box{v: 1}", want: "Box{v: 1}"},
	{name: "generic enum", decls: mxTree, typ: "Tree<String>", expr: `Tree.Leaf("a")`, want: `Leaf("a")`},
	{name: "generic bare variant", decls: mxTree, typ: "Tree<Int>", expr: "Tree.Nil", want: "Nil", tags: tagUntyped | tagBareOK, reject: mxUnsolved},

	// Function values.
	{name: "named function", decls: "fn twice(n: Int): Int {\n    n * 2\n}", typ: "(Int) -> Int", expr: "twice",
		obs: "Debug.inspect($v(2))", want: "4", tags: tagFn},
	{name: "lambda", typ: "(Int) -> Int", expr: "|n: Int| n + 1", obs: "Debug.inspect($v(2))", want: "3", tags: tagFn},
	{name: "unannotated lambda", typ: "(Int) -> Int", expr: "|n| n + 1", obs: "Debug.inspect($v(2))", want: "3",
		tags: tagFn | tagExpects, reject: "cannot infer type for parameter"},
	{name: "captured lambda", typ: "(Int) -> Int", expr: "{\n    k = 5\n    |n: Int| n + k\n}",
		obs: "Debug.inspect($v(2))", want: "7", tags: tagFn | tagNested},
	{name: "partial", decls: mxAdd3, typ: "(Int) -> Int", expr: "add3(_, 10)", obs: "Debug.inspect($v(2))", want: "112",
		tags: tagFn | tagCall},
	{name: "named partial", decls: mxAdd3, typ: "(Int) -> Int", expr: "add3(1, b: _)", obs: "Debug.inspect($v(2))",
		want: "103", tags: tagFn | tagCall},
	{name: "constructor value", decls: mxShape, typ: "(Float) -> Shape", expr: "Shape.Circle",
		obs: "Debug.inspect($v(2.0))", want: "Circle(2.0)", tags: tagFn},
	{name: "owner function value", typ: "(String) -> Int", expr: "String.length", obs: `Debug.inspect($v("abc"))`,
		want: "3", tags: tagFn | tagCall},
	{name: "interface function value", typ: "(Int) -> String", expr: "Display.to_string",
		obs: "Debug.inspect($v(12))", want: `"12"`, tags: tagFn | tagCall | tagExpects | tagBareOK,
		reject: "cannot infer type parameter"},
	{name: "unit function", decls: "fn noop(_n: Int) {}", typ: "(Int) -> Unit", expr: "noop",
		obs: "Debug.inspect($v(1))", want: "Unit", tags: tagFn | tagUnit},
	{name: "intrinsic partial", typ: "(String) -> String", expr: "String.pad_start(_, width: 3, fill: \"0\")",
		obs: `Debug.inspect($v("7"))`, want: `"007"`, tags: tagFn | tagCall},

	// Interface values.
	{name: "existential", decls: mxNamed + "\n\nfn named_of(n: Int): Named {\n    Pt{x: n}\n}", typ: "Named",
		expr: "named_of(4)", obs: "Named.shout($v)", want: "pt4!", tags: tagExistential},
	{name: "existential widening", decls: mxNamed, typ: "Named", expr: "Pt{x: 5}", obs: "Named.shout($v)",
		want: "pt5!", tags: tagExistential | tagWidens},
	{name: "existential list", typ: "List<Display>", expr: `[1, "a"]`,
		obs: `$v |> Iter.map(|d| Display.to_string(d)) |> String.join(",")`, want: "1,a",
		tags: tagExistential | tagExpects, reject: "list element type mismatch"},

	// Maybe and Result.
	{name: "some", typ: "Maybe<Int>", expr: "Some(3)", want: "Some(3)"},
	{name: "none", typ: "Maybe<Int>", expr: "None", want: "None", tags: tagUntyped | tagBareOK, reject: mxUnsolved},
	{name: "ok", typ: "Result<Int, String>", expr: "Ok(1)", want: "Ok(1)", tags: tagPartial, reject: mxRows},
	{name: "err", typ: "Result<Int, String>", expr: `Err("bad")`, want: `Err("bad")`, tags: tagPartial, reject: mxRows},
	{name: "maybe map", typ: "Maybe<Int>", expr: "Maybe.map(Some(2), |n| n * 3)", want: "Some(6)"},

	// Iter chains.
	{name: "iter chain", typ: "Iter<Int>", expr: "[1, 2, 3] |> Iter.map(|n| n * 2) |> Iter.filter(|n| n > 2)",
		obs: "Debug.inspect(Iter.to_list($v))", want: "[4, 6]", tags: tagIter},
	{name: "range iter", typ: "Iter<Int>", expr: "1..=3 |> Iter.map(|n| n * n)",
		obs: "Debug.inspect(Iter.to_list($v))", want: "[1, 4, 9]", tags: tagIter},
	{name: "iter reduce", typ: "Int", expr: "[1, 2, 3] |> Iter.reduce(|acc = 0, n| acc + n)", want: "6"},

	// Expressions.
	{name: "case", typ: "String", expr: "case 2 {\n    1 -> \"one\"\n    _ -> \"many\"\n}", want: `"many"`, tags: tagNested},
	{name: "if", typ: "String", expr: `if 2 > 1 { "yes" } else { "no" }`, want: `"yes"`},
	{name: "block", typ: "Int", expr: "{\n    a = 2\n    a * 3\n}", want: "6", tags: tagNested},
	{name: "try", decls: "fn half(n: Int): Result<Int, String> {\n    Ok(n / 2)\n}", typ: "Result<Int, String>",
		expr: "{\n    f = || {\n        h = try half(8)\n        Ok(h + 1)\n    }\n    f()\n}", want: "Ok(5)", tags: tagNested},
	{name: "try maybe", typ: "Maybe<Int>",
		expr: "{\n    f = || {\n        h = try Some(8)\n        Some(h + 1)\n    }\n    f()\n}", want: "Some(9)", tags: tagNested},
	{name: "dbg", typ: "Int", expr: "dbg 7", want: "7", tags: tagDbg},
	{name: "and or", typ: "Bool", expr: "1 > 2 or (3 > 2 and True)", want: "True"},
	{name: "pipe", typ: "Int", expr: `"abc" |> String.length()`, want: "3"},
	{name: "then stage", typ: "Int", expr: "4 |> then |n| n * n", want: "16"},
	{name: "field access", decls: mxPoint, typ: "Int", expr: "Point{x: 5}.x", want: "5"},
	{name: "struct update", decls: mxPoint, typ: "Point", expr: "Struct.update(Point{x: 1}, {y: 9})",
		want: "Point{x: 1, y: 9}"},
	{name: "struct spread", decls: mxPoint + "\n\nfn origin(): Point {\n    Point{x: 0, y: 0}\n}", typ: "Point",
		expr: "{..origin(), x: 7}", want: "Point{x: 7, y: 0}"},
	{name: "nested spread", decls: mxPoint + "\n\nstruct Line {\n    a: Point\n    b: Point\n}\n\nfn line0(): Line {\n    Line{a: Point{x: 0}, b: Point{x: 1}}\n}",
		typ: "Line", expr: "{..line0(), a: {.., x: 5}}", want: "Line{a: Point{x: 5, y: 2}, b: Point{x: 1, y: 2}}"},

	// Patterns.
	{name: "tuple pattern", typ: "String", expr: "case (1, \"a\") {\n    (n, s) -> \"${n}${s}\"\n}", want: `"1a"`, tags: tagNested},
	{name: "list pattern", typ: "Int", expr: "case [1, 2, 3] {\n    [h, ..t] -> h + Iter.count(t)\n    [] -> 0\n}", want: "3",
		tags: tagNested},
	{name: "struct pattern", decls: mxPoint, typ: "Int", expr: "case Point{x: 1, y: 5} {\n    Point{x, y} -> x + y\n}",
		want: "6", tags: tagNested},
	{name: "variant guard pattern", decls: mxShape, typ: "String",
		expr: "case Shape.Circle(3.0) {\n    .Circle(r) when r > 2.0 -> \"big\"\n    _ -> \"other\"\n}", want: `"big"`, tags: tagNested},
	{name: "map pattern", typ: "Int", expr: "case {\"k\" => 4} {\n    {\"k\" => w} -> w\n    _ -> 0\n}", want: "4",
		tags: tagNested},
	{name: "prefix pattern", typ: "String", expr: "case \"/u/7\" {\n    \"/u/\" + id -> id\n    _ -> \"\"\n}", want: `"7"`,
		tags: tagNested},
	{name: "as pattern", typ: "String",
		expr: "case Some(2) {\n    Some(n) as whole -> \"${n}${Debug.inspect(whole)}\"\n    None -> \"\"\n}", want: `"2Some(2)"`,
		tags: tagNested},
	{name: "or pattern", decls: mxShape, typ: "String",
		expr: "case Shape.Rect{w: 2, h: 3} {\n    .Circle(_) or .Dot -> \"round\"\n    .Rect{w: n} or .Rect{h: n} when n > 5 -> \"wide\"\n    .Rect{w: 1 or 2 as n} -> \"${n}\"\n    _ -> \"other\"\n}",
		want: `"2"`, tags: tagNested},
	{name: "or parameter pattern", typ: "Int", expr: "{\n    f = |(0, n) or (n, _): (Int, Int)| n * 2\n    f((0, 4)) + f((3, 1))\n}",
		want: "14", tags: tagNested},
	{name: "distinct pattern", decls: "type Meters Int", typ: "Int", expr: "case Meters(4) {\n    Meters(n) -> n\n}",
		want: "4", tags: tagNested},
	{name: "literal pattern", typ: "String", expr: "case 3 {\n    1 -> \"a\"\n    3 -> \"c\"\n    _ -> \"z\"\n}", want: `"c"`,
		tags: tagNested},
	{name: "binding else", typ: "Int", expr: "{\n    Some(n) = Some(9) else { 0 }\n    n\n}", want: "9", tags: tagNested},
	{name: "pattern if", typ: "Int", expr: "if Some(n) = Some(4) { n } else { 0 }", want: "4"},
	{name: "parameter pattern", typ: "Int", expr: "{\n    f = |(a, b): (Int, Int)| a * b\n    f((3, 4))\n}", want: "12",
		tags: tagNested},

	// Calls.
	{name: "named arguments", decls: mxGreet, typ: "String", expr: `greet("a", times: 2)`, want: `"a!2"`, tags: tagCall},
	{name: "reordered named arguments", decls: mxGreet, typ: "String", expr: `greet(times: 3, name: "b")`, want: `"b!3"`,
		tags: tagCall},
	{name: "default reads a parameter", decls: "fn span(a: Int, b: Int = a * 2): Int {\n    a + b\n}", typ: "Int",
		expr: "span(3)", want: "9", tags: tagCall},
	{name: "intrinsic named arguments", typ: "String", expr: `String.pad_start("7", fill: "0", width: 3)`,
		want: `"007"`, tags: tagCall},
	{name: "captured partial named call", decls: mxAdd3, typ: "Int", expr: "{\n    g = add3(_, b: _)\n    g(b: 2, 1)\n}",
		want: "103", tags: tagCall | tagNested},
	{name: "owner qualified call", typ: "Int", expr: "Maybe.with_default(Some(5), 0)", want: "5", tags: tagCall},
	{name: "interface qualified call", typ: "String", expr: "Display.to_string(12)", want: `"12"`, tags: tagCall},
	{name: "type parameter qualified call",
		decls: "fn shown<T>(x: T): String where T: Display {\n    T.to_string(x)\n}", typ: "String", expr: "shown(5)",
		want: `"5"`, tags: tagCall},

	// Derived impls.
	{name: "derive Equatable", decls: "struct Cpt {\n    a: Int\n}\n\nderive Equatable for Cpt", typ: "Cpt",
		expr: "Cpt{a: 1}", obs: "Debug.inspect($v == Cpt{a: 1})", want: "True"},
	{name: "derive Hashable", decls: "struct Hp {\n    a: Int\n}\n\nderive Equatable for Hp\nderive Hashable for Hp", typ: "Hp",
		expr: "Hp{a: 1}", obs: "Debug.inspect(Hashable.hash($v) == Hashable.hash(Hp{a: 1}))", want: "True"},
	{name: "derive Comparable", decls: mxCoin + "\n\nderive Equatable for Coin\nderive Comparable for Coin", typ: "Coin",
		expr: "Coin.Penny", obs: "Debug.inspect($v < Coin.Dime)", want: "True"},
	{name: "derive Display", decls: "struct Shown {\n    name: String\n}\n\nderive Display for Shown", typ: "Shown",
		expr: `Shown{name: "bob"}`, obs: "Display.to_string($v)", want: "Shown{name: bob}"},
	{name: "derive Debug", decls: "struct Dbg {\n    s: String\n}\n\nderive Debug for Dbg", typ: "Dbg",
		expr: `Dbg{s: "q"}`, want: `Dbg{s: "q"}`},
	{name: "derive ToJson", decls: "struct Jp {\n    a: Int\n    s: String\n}\n\nderive ToJson for Jp", typ: "Jp",
		expr: `Jp{a: 1, s: "x"}`, obs: "Json.encode(ToJson.to_json($v))", want: `{"a":1,"s":"x"}`},
	{name: "derive FromJson",
		decls: "struct Jq {\n    a: Int\n}\n\nderive ToJson for Jq\nderive FromJson for Jq", typ: "Jq",
		expr: "Jq{a: 2}", obs: "Debug.inspect(FromJson.from_json<Jq>(ToJson.to_json($v)))", want: "Ok(Jq{a: 2})"},
	{name: "derive Variants", decls: mxCoin + "\n\nderive Variants for Coin", typ: "List<Coin>",
		expr: "Coin.variants()", want: "[Penny, Dime]"},
}

// mxBuild is a cell's program while its contexts wrap the feature.
type mxBuild struct {
	typ  string
	expr string
	// decls are top-level declarations of the entry file; lib are those of
	// lib.nomi.
	decls []string
	lib   []string
}

// mxBlock is a block expression of the statements and tail, one per line.
func mxBlock(lines ...string) string {
	return "{\n" + mxIndent(strings.Join(lines, "\n")) + "\n}"
}

func mxIndent(s string) string {
	return "    " + strings.ReplaceAll(s, "\n", "\n    ")
}

// mxParen parenthesizes e unless it is a plain name or literal.
func mxParen(e string) string {
	if regexp.MustCompile(`^[A-Za-z0-9_."]+$`).MatchString(e) {
		return e
	}
	return "(" + e + ")"
}

// mxContext is a wrapping context, a shell or a placement. Exactly one of
// wrap, shell and place is set.
type mxContext struct {
	name string
	// wrap rewrites b.expr into an expression of the same type that holds
	// it; n names its declarations apart from another context's.
	wrap func(b *mxBuild, n int)
	// shell writes the entry's observing code for the final expression e
	// and answers it, any declarations it needs, and the transcript the
	// run must print.
	shell func(b *mxBuild, e, obs, want string) (body string, decls []string, transcript string)
	// place moves declarations between the files: feature declarations
	// arrive in b.decls ahead of the contexts'.
	place func(b *mxBuild, featureDecls []string)
	// fragile marks the contexts the triples cross with a second context:
	// containers, type arguments and file boundaries.
	fragile bool
	// typing says what a wrapping context does for a value whose type its
	// position must supply (tagUntyped, tagExpects).
	typing mxTyping
}

// mxTyping is what a wrapping context gives the type of the expression it
// holds.
type mxTyping int

const (
	// mxFlows: nothing; the expression's type flows out of the context as
	// it is, solved or not, to be bound or fixed further out.
	mxFlows mxTyping = iota
	// mxGives: the type it declares, as the expected type: an annotation,
	// a parameter, a result, a field, a payload.
	mxGives
	// mxUnifies: the type it declares, by unification after the
	// expression is checked (a pipe's head against its stage's
	// parameter), which solves a tagUntyped part but gives a tagExpects
	// expression nothing.
	mxUnifies
	// mxPasses: the type its own position expects (a `case` or `if` arm,
	// a block's tail).
	mxPasses
	// mxBinds: an unannotated binding, which must be locally determined.
	mxBinds
)

func wrapCtx(name string, fragile bool, typing mxTyping, wrap func(b *mxBuild, n int)) *mxContext {
	return &mxContext{name: name, wrap: wrap, fragile: fragile, typing: typing}
}

var mxWraps = []*mxContext{
	wrapCtx("binding", false, mxBinds, func(b *mxBuild, n int) {
		v := fmt.Sprintf("b%d", n)
		b.expr = mxBlock(v+" = "+b.expr, v)
	}),
	wrapCtx("annotated binding", false, mxGives, func(b *mxBuild, n int) {
		v := fmt.Sprintf("b%d", n)
		b.expr = mxBlock(v+": "+b.typ+" = "+b.expr, v)
	}),
	wrapCtx("block tail", false, mxPasses, func(b *mxBuild, n int) {
		k := fmt.Sprintf("k%d", n)
		b.expr = mxBlock(k+" = 1", "_ = "+k, b.expr)
	}),
	wrapCtx("function argument", false, mxGives, func(b *mxBuild, n int) {
		b.decls = append(b.decls, fmt.Sprintf("fn take%d(a: %s): %s {\n    a\n}", n, b.typ, b.typ))
		b.expr = fmt.Sprintf("take%d(%s)", n, b.expr)
	}),
	wrapCtx("named argument", false, mxGives, func(b *mxBuild, n int) {
		b.decls = append(b.decls, fmt.Sprintf("fn named%d(k: Int, a: %s): %s {\n    _ = k\n    a\n}", n, b.typ, b.typ))
		b.expr = fmt.Sprintf("named%d(a: %s, k: 0)", n, b.expr)
	}),
	wrapCtx("function result", false, mxGives, func(b *mxBuild, n int) {
		b.decls = append(b.decls, fmt.Sprintf("fn make%d(): %s {\n%s\n}", n, b.typ, mxIndent(b.expr)))
		b.expr = fmt.Sprintf("make%d()", n)
	}),
	wrapCtx("parameter default", false, mxGives, func(b *mxBuild, n int) {
		b.decls = append(b.decls, fmt.Sprintf("fn dflt%d(a: %s = %s): %s {\n    a\n}", n, b.typ, b.expr, b.typ))
		b.expr = fmt.Sprintf("dflt%d()", n)
	}),
	wrapCtx("pipe stage", false, mxUnifies, func(b *mxBuild, n int) {
		b.decls = append(b.decls, fmt.Sprintf("fn pass%d(a: %s): %s {\n    a\n}", n, b.typ, b.typ))
		b.expr = fmt.Sprintf("(%s |> pass%d())", mxParen(b.expr), n)
	}),
	wrapCtx("then stage", false, mxFlows, func(b *mxBuild, n int) {
		b.expr = fmt.Sprintf("(%s |> then |t%d| t%d)", mxParen(b.expr), n, n)
	}),
	wrapCtx("case arm", false, mxPasses, func(b *mxBuild, n int) {
		b.expr = fmt.Sprintf("case %d {\n    1 -> %s\n    _ -> %s\n}", n, mxIndentTail(b.expr), mxIndentTail(b.expr))
	}),
	wrapCtx("if arm", false, mxPasses, func(b *mxBuild, n int) {
		b.expr = fmt.Sprintf("if %d > 0 {\n%s\n} else {\n%s\n}", n, mxIndent(b.expr), mxIndent(b.expr))
	}),
	wrapCtx("lambda body", false, mxFlows, func(b *mxBuild, n int) {
		f := fmt.Sprintf("f%d", n)
		body := b.expr
		if strings.Contains(body, "\n") {
			body = mxBlock(body)
		}
		b.expr = mxBlock(f+" = || "+body, f+"()")
	}),
	wrapCtx("nested function", false, mxGives, func(b *mxBuild, n int) {
		b.expr = mxBlock(fmt.Sprintf("fn inner%d(): %s {\n%s\n}", n, b.typ, mxIndent(b.expr)), fmt.Sprintf("inner%d()", n))
	}),
	wrapCtx("generic type argument", true, mxFlows, func(b *mxBuild, n int) {
		b.decls = append(b.decls, fmt.Sprintf("fn ident%d<A>(a: A): A {\n    a\n}", n))
		b.expr = fmt.Sprintf("ident%d(%s)", n, b.expr)
	}),
	wrapCtx("generic function body", true, mxGives, func(b *mxBuild, n int) {
		b.decls = append(b.decls, fmt.Sprintf("fn gen%d<A>(a: A): (A, %s) {\n    (a, %s)\n}", n, b.typ, mxIndentTail(b.expr)))
		b.expr = fmt.Sprintf("gen%d(0).1", n)
	}),
	wrapCtx("interface default", false, mxGives, func(b *mxBuild, n int) {
		b.decls = append(b.decls, fmt.Sprintf("interface Make%d {\n    fn tag%d(x: self): Int\n\n    fn make%d(_x: self): %s {\n%s\n    }\n}", n, n, n, b.typ, mxIndent(mxIndent(b.expr))),
			fmt.Sprintf("type Maker%d Int", n),
			fmt.Sprintf("impl Make%d for Maker%d {\n    fn tag%d(_x: Maker%d): Int {\n        0\n    }\n}", n, n, n, n))
		b.expr = fmt.Sprintf("Make%d.make%d(Maker%d(0))", n, n, n)
	}),
	wrapCtx("once", true, mxGives, func(b *mxBuild, n int) {
		b.decls = append(b.decls, fmt.Sprintf("once once%d: %s = %s", n, b.typ, b.expr))
		b.expr = fmt.Sprintf("once%d", n)
	}),
	wrapCtx("struct field", true, mxGives, func(b *mxBuild, n int) {
		b.decls = append(b.decls, fmt.Sprintf("struct Hold%d {\n    v: %s\n}", n, b.typ))
		h := fmt.Sprintf("h%d", n)
		b.expr = mxBlock(fmt.Sprintf("%s = Hold%d{v: %s}", h, n, b.expr), h+".v")
	}),
	wrapCtx("field default", true, mxGives, func(b *mxBuild, n int) {
		b.decls = append(b.decls, fmt.Sprintf("struct Dflt%d {\n    v: %s = %s\n}", n, b.typ, mxIndentTail(b.expr)))
		b.expr = fmt.Sprintf("Dflt%d{}.v", n)
	}),
	wrapCtx("record field", true, mxFlows, func(b *mxBuild, n int) {
		r := fmt.Sprintf("r%d", n)
		b.expr = mxBlock(fmt.Sprintf("%s = {f: %s, g: 0}", r, b.expr), r+".f")
	}),
	wrapCtx("enum payload", true, mxGives, func(b *mxBuild, n int) {
		b.decls = append(b.decls, fmt.Sprintf("enum Pay%d {\n    Has %s\n    Gone\n}", n, mxPayload(b.typ)))
		b.expr = fmt.Sprintf("case Pay%d.Has(%s) {\n    Pay%d.Has(p%d) -> p%d\n    Pay%d.Gone -> todo\n}",
			n, b.expr, n, n, n, n)
	}),
	wrapCtx("variant parameter pattern", false, mxGives, func(b *mxBuild, n int) {
		b.decls = append(b.decls, fmt.Sprintf("enum Only%d {\n    One %s\n}", n, mxPayload(b.typ)),
			fmt.Sprintf("fn open%d(Only%d.One(p)): %s {\n    p\n}", n, n, b.typ))
		b.expr = fmt.Sprintf("open%d(Only%d.One(%s))", n, n, b.expr)
	}),
	wrapCtx("variant binding pattern", false, mxGives, func(b *mxBuild, n int) {
		b.decls = append(b.decls, fmt.Sprintf("enum Lone%d {\n    One %s\n}", n, mxPayload(b.typ)))
		b.expr = mxBlock(fmt.Sprintf("Lone%d.One(p%d) = Lone%d.One(%s)", n, n, n, b.expr), fmt.Sprintf("p%d", n))
	}),
	wrapCtx("generic struct field", true, mxFlows, func(b *mxBuild, n int) {
		b.decls = append(b.decls, fmt.Sprintf("struct Gbox%d<A> {\n    v: A\n}", n))
		g := fmt.Sprintf("g%d", n)
		b.expr = mxBlock(fmt.Sprintf("%s = Gbox%d{v: %s}", g, n, b.expr), g+".v")
	}),
	wrapCtx("generic enum payload", true, mxFlows, func(b *mxBuild, n int) {
		b.decls = append(b.decls, fmt.Sprintf("enum Gopt%d<A> {\n    Full A\n    Nothing\n}", n))
		b.expr = fmt.Sprintf("case Gopt%d.Full(%s) {\n    Gopt%d.Full(p%d) -> p%d\n    Gopt%d.Nothing -> todo\n}",
			n, b.expr, n, n, n, n)
	}),
	wrapCtx("list element", true, mxFlows, func(b *mxBuild, n int) {
		b.expr = fmt.Sprintf("case [%s] {\n    [p%d] -> p%d\n    _ -> todo\n}", b.expr, n, n)
	}),
	wrapCtx("map value", true, mxFlows, func(b *mxBuild, n int) {
		b.expr = fmt.Sprintf("case Map.get({\"k\" => %s}, \"k\") {\n    Some(p%d) -> p%d\n    None -> todo\n}",
			b.expr, n, n)
	}),
	wrapCtx("tuple element", true, mxFlows, func(b *mxBuild, n int) {
		b.expr = fmt.Sprintf("(%d, %s).1", n, b.expr)
	}),
	wrapCtx("maybe payload", true, mxFlows, func(b *mxBuild, n int) {
		b.expr = fmt.Sprintf("case Some(%s) {\n    Some(p%d) -> p%d\n    None -> todo\n}", b.expr, n, n)
	}),
	wrapCtx("result payload", true, mxFlows, func(b *mxBuild, n int) {
		b.expr = fmt.Sprintf("case Ok(%s) {\n    Ok(p%d) -> p%d\n    Err(_) -> todo\n}", b.expr, n, n)
	}),
	wrapCtx("Iter.map callback", false, mxFlows, func(b *mxBuild, n int) {
		xs := fmt.Sprintf("xs%d", n)
		b.expr = mxBlock(fmt.Sprintf("%s = [0] |> Iter.map(|_z%d| %s) |> Iter.to_list()", xs, n, mxBraced(b.expr)),
			fmt.Sprintf("case %s {\n    [p%d] -> p%d\n    _ -> todo\n}", xs, n, n))
	}),
	wrapCtx("Iter.loop callback", false, mxGives, func(b *mxBuild, n int) {
		m, s := fmt.Sprintf("m%d", n), fmt.Sprintf("s%d", n)
		b.expr = mxBlock(
			fmt.Sprintf("%s = Iter.loop(|%s: Maybe<%s> = None| {\n    case %s {\n        Some(_) -> break %s\n        None -> Some(%s)\n    }\n})",
				m, s, b.typ, s, s, mxIndentTail(mxIndentTail(b.expr))),
			fmt.Sprintf("case %s {\n    Some(p%d) -> p%d\n    None -> todo\n}", m, n, n))
	}),
	wrapCtx("concurrent task", false, mxFlows, func(b *mxBuild, n int) {
		t := fmt.Sprintf("task%d", n)
		b.expr = fmt.Sprintf("concurrent {\n    %s = Task.spawn(|| %s)\n    Task.await(%s)\n}", t, mxIndentTail(mxBraced(b.expr)), t)
	}),
	wrapCtx("generic function of another file", true, mxFlows, func(b *mxBuild, n int) {
		b.lib = append(b.lib, fmt.Sprintf("fn carry%d<A>(a: A): A {\n    a\n}", n))
		b.expr = fmt.Sprintf("carry%d(%s)", n, b.expr)
	}),
}

// mxPayload is typ as a variant's payload type: an anonymous struct type is
// parenthesized, since `Has {a: Int}` would declare a struct variant.
func mxPayload(typ string) string {
	if strings.HasPrefix(typ, "{") {
		return "(" + typ + ")"
	}
	return typ
}

// mxIndentTail indents every line of e but its first, for an expression
// that starts after text on its line.
func mxIndentTail(e string) string {
	return strings.ReplaceAll(e, "\n", "\n    ")
}

// mxBraced is e as a lambda body: braced when it spans lines, since an
// unbraced body may not continue on the next line.
func mxBraced(e string) string {
	if strings.Contains(e, "\n") {
		return mxBlock(e)
	}
	return e
}

// mxString is s as a Nomi string literal.
func mxString(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "${", `\${`)
	return `"` + r.Replace(s) + `"`
}

func mxObs(obs, v string) string {
	if obs == "" {
		obs = "Debug.inspect($v)"
	}
	return strings.ReplaceAll(obs, "$v", v)
}

var mxShellMain = &mxContext{name: "main", shell: func(b *mxBuild, e, obs, want string) (string, []string, string) {
	return "fn main() {\n    got = " + mxIndentTail(e) + "\n    io.print(" + mxObs(obs, "got") + ")\n}", nil,
		want + "\n\n--- ok: <nil>"
}}

const mxCase = "matrix"

var mxShells = []*mxContext{
	mxShellMain,
	{name: "test body", shell: func(b *mxBuild, e, obs, want string) (string, []string, string) {
		return "test \"" + mxCase + "\" {\n    got = " + mxIndentTail(e) + "\n    assert " + mxObs(obs, "got") + " == " + mxString(want) + "\n}",
			nil, "\n--- " + mxCase + ": ok: <nil>\n"
	}},
	{name: "assert subject", shell: func(b *mxBuild, e, obs, want string) (string, []string, string) {
		return "test \"" + mxCase + "\" {\n    assert " + mxIndentTail(mxObs(obs, mxParen(e))) + " == " + mxString(want) + "\n}",
			nil, "\n--- " + mxCase + ": ok: <nil>\n"
	}},
	{name: "subtests row", shell: func(b *mxBuild, e, obs, want string) (string, []string, string) {
		return "test \"" + mxCase + "\" {\n    subtests [" + mxIndentTail(e) + "], got {\n        assert " + mxObs(obs, "got") + " == " +
			mxString(want) + "\n    }\n}", nil, "\n--- " + mxCase + ": ok: <nil>\n"
	}},
	{name: "defer", shell: func(b *mxBuild, e, obs, want string) (string, []string, string) {
		// One deferred call reads the local, widening it to the parameter's
		// type; the other's operand is a call, evaluated where the `defer`
		// stands.
		show := "fn show(v: " + b.typ + ") {\n    io.print(" + mxObs(obs, "v") + ")\n}"
		return "fn main() {\n    got = " + mxIndentTail(e) + "\n    defer show(got)\n    defer io.print(" + mxObs(obs, "got") +
				")\n    io.print(\"body\")\n}", []string{show},
			"body\n" + want + "\n" + want + "\n\n--- ok: <nil>"
	}},
	{name: "with", shell: func(b *mxBuild, e, obs, want string) (string, []string, string) {
		decls := []string{"struct App {\n    v: " + b.typ + "\n}", "fn boot(): App {\n    App{v: " + mxIndentTail(mxIndentTail(e)) + "}\n}"}
		return "fn main() {\n    with App.v = " + mxIndentTail(e) + "\n    got = App.v\n    io.print(" + mxObs(obs, "got") + ")\n}",
			decls, want + "\n\n--- ok: <nil>"
	}},
}

var mxPlaceSame = &mxContext{name: "one file", place: func(b *mxBuild, featureDecls []string) {}}

var mxPlacements = []*mxContext{
	mxPlaceSame,
	{name: "declarations in another file", fragile: true, place: func(b *mxBuild, featureDecls []string) {
		b.lib = append(b.lib, featureDecls...)
		b.decls = b.decls[len(featureDecls):]
	}},
	{name: "value from another file", fragile: true, place: func(b *mxBuild, featureDecls []string) {
		b.lib = append(b.lib, b.decls...)
		b.lib = append(b.lib, "fn produce(): "+b.typ+" {\n"+mxIndent(b.expr)+"\n}")
		b.decls = nil
		b.expr = "produce()"
	}},
}

// mxCell is one program of the matrix.
type mxCell struct {
	f     *mxFeature
	wraps []*mxContext
	shell *mxContext
	place *mxContext
}

func (c mxCell) name() string {
	parts := []string{c.f.name}
	for _, w := range c.wraps {
		parts = append(parts, w.name)
	}
	if c.shell != mxShellMain {
		parts = append(parts, c.shell.name)
	}
	if c.place != mxPlaceSame {
		parts = append(parts, c.place.name)
	}
	return strings.Join(parts, " × ")
}

// mxStdImports are the std names a program may use, with what imports them.
var mxStdImports = []struct{ name, module, item string }{
	{"io", "std/io", ""},
	{"Task", "std/tasks", "Task"},
	{"Json", "std/json", "Json"},
	{"ToJson", "std/json", "ToJson"},
	{"FromJson", "std/json", "FromJson"},
}

var (
	mxIdent    = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
	mxDeclName = regexp.MustCompile(`(?m)^(?:struct|enum|type|interface|fn|once|typealias) ([A-Za-z_][A-Za-z0-9_]*)`)
	mxPubDecl  = regexp.MustCompile(`(?m)^(struct|enum|type|interface|fn|once|typealias) `)
	mxStrLit   = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)
)

// mxUses reports the names text uses as a plain identifier, one not after a
// `.` (a field, a variant or an owner's member) and not inside a string
// literal.
func mxUses(text string) map[string]bool {
	text = mxStrLit.ReplaceAllString(text, `""`)
	used := map[string]bool{}
	for _, loc := range mxIdent.FindAllStringIndex(text, -1) {
		if loc[0] > 0 && text[loc[0]-1] == '.' && (loc[0] < 2 || text[loc[0]-2] != '.') {
			continue
		}
		used[text[loc[0]:loc[1]]] = true
	}
	return used
}

// mxImports are the import lines text needs: the std names it uses, and
// the given file's names it uses.
func mxImports(text, file string, names []string) string {
	used := mxUses(text)
	byModule := map[string][]string{}
	var modules []string
	for _, s := range mxStdImports {
		if !used[s.name] {
			continue
		}
		if _, ok := byModule[s.module]; !ok {
			modules = append(modules, s.module)
		}
		if s.item != "" {
			byModule[s.module] = append(byModule[s.module], s.item)
		}
	}
	var lines []string
	for _, m := range modules {
		items := byModule[m]
		switch len(items) {
		case 0:
			lines = append(lines, "import "+m)
		case 1:
			lines = append(lines, "import "+m+"."+items[0])
		default:
			lines = append(lines, "import "+m+".{"+strings.Join(items, ", ")+"}")
		}
	}
	var mine []string
	for _, n := range names {
		if used[n] {
			mine = append(mine, n)
		}
	}
	if len(mine) == 1 {
		lines = append(lines, "import "+file+"."+mine[0])
	} else if len(mine) > 1 {
		lines = append(lines, "import "+file+".{"+strings.Join(mine, ", ")+"}")
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n\n"
}

// source is the cell's program, and the transcript its run must print.
func (c mxCell) source() (src, transcript string) {
	b := &mxBuild{typ: c.f.typ, expr: c.f.expr}
	var featureDecls []string
	if c.f.decls != "" {
		featureDecls = []string{c.f.decls}
	}
	b.decls = append(b.decls, featureDecls...)
	for i, w := range c.wraps {
		w.wrap(b, i+1)
	}
	c.place.place(b, featureDecls)
	body, shellDecls, transcript := c.shell.shell(b, b.expr, c.f.obs, c.f.want)
	main := strings.Join(append(append(append([]string{}, b.decls...), shellDecls...), body), "\n\n") + "\n"
	if len(b.lib) == 0 {
		return mxImports(main, "", nil) + main, transcript
	}
	lib := strings.Join(b.lib, "\n\n") + "\n"
	lib = mxPubDecl.ReplaceAllString(lib, "pub $1 ")
	var libNames []string
	for _, m := range mxDeclName.FindAllStringSubmatch(strings.ReplaceAll(lib, "pub ", ""), -1) {
		libNames = append(libNames, m[1])
	}
	return "// FILE: main.nomi\n" + mxImports(main, "lib", libNames) + main +
		"// FILE: lib.nomi\n" + mxImports(lib, "", nil) + lib, transcript
}

// mxDbgLine matches the line a `dbg` prints.
var mxDbgLine = regexp.MustCompile(`(?m)^dbg line \d+: .*\n`)

// mxExpectedRejection is the text the checker's rejection of c must hold
// (a regular expression) and why, or "" when c must be accepted. skip is
// set, with its reason, for a cell the matrix does not generate.
//
// The rules: a feature whose type its position supplies (tagUntyped,
// tagExpects, tagPartial) is walked out from the innermost context. A
// context that gives the type ends the walk, accepted; one that binds it
// unannotated, or a shell with nothing to give, rejects it. Beyond that,
// mxCheckerRules lists the pairs the checker rejects for reasons of its
// own.
func mxExpectedRejection(c mxCell) (want, why, skip string) {
	for _, r := range mxCheckerRules {
		if r.applies(c) {
			return r.want, r.why, ""
		}
	}
	if c.f.reject == "" {
		return "", "", ""
	}
	expects := c.f.tags.has(tagExpects)
	for i, w := range c.wraps {
		switch {
		case w.typing == mxGives, w.typing == mxUnifies && !expects:
			return "", "", ""
		case w.typing == mxPasses, w.typing == mxFlows && !expects:
			continue
		case w.typing == mxBinds && !c.f.tags.has(tagPartial):
			return c.f.reject, "an unannotated binding must be locally determined, and `" + w.name + "` binds the " +
				c.f.name + " before anything gives it a type", ""
		case w.typing == mxBinds:
			continue
		}
		// A tagExpects feature where its position expects nothing. A
		// generic call or a constructor that a later position gives a type
		// sometimes hands it on (a generic call's own expected type fixes
		// its parameter, §2) and sometimes not, which the matrix does not
		// pin: the pairs check each side.
		if mxLaterGives(c, i+1) {
			return "", "", "a " + c.f.name + " in `" + w.name + "` inside a context that gives its type"
		}
		return c.f.reject, "`" + w.name + "` gives the " + c.f.name + " no expected type, and it needs one", ""
	}
	switch {
	case c.place.name == "value from another file", c.shell.name == "with":
		// The other file's function returns the type, and an application
		// field has it.
		return "", "", ""
	case c.f.tags.has(tagPartial) && c.shell.name != "subtests row":
		return "", "", ""
	case c.f.tags.has(tagBareOK) && c.shell.name == "assert subject":
		// Debug.inspect's argument: a part of a type that no value has may
		// stay unsolved, and the observation's call solves a generic
		// function value.
		return "", "", ""
	}
	return c.f.reject, "the " + c.shell.name + " shell gives the " + c.f.name + " no type", ""
}

// mxLaterGives reports whether a context of c from the i-th wrap out gives
// a type: a wrap, the placement or the shell.
func mxLaterGives(c mxCell, i int) bool {
	for _, w := range c.wraps[i:] {
		if w.typing == mxGives || w.typing == mxUnifies {
			return true
		}
	}
	return c.place.name == "value from another file" || c.shell.name == "with"
}

// mxCheckerRule is a rejection the type system makes of a feature in a
// context for a reason of its own, outside the typing walk.
type mxCheckerRule struct {
	feature, context string
	// before: the rule holds only while no context inside context gives
	// the feature its type.
	before bool
	// match, when set, decides instead of feature, context and before.
	match func(c mxCell) bool
	// hole marks a program the checker accepts though the spec rejects
	// it: the cell must be accepted and run, and a fix that rejects it
	// removes the rule.
	hole bool
	want string
	why  string
}

func (r mxCheckerRule) applies(c mxCell) bool {
	if r.match != nil {
		return r.match(c)
	}
	if c.f.name != r.feature {
		return false
	}
	for _, w := range c.wraps {
		if w.name == r.context {
			return true
		}
		if r.before && (w.typing == mxGives || w.typing == mxUnifies) {
			return false
		}
	}
	return c.shell.name == r.context || c.place.name == r.context
}

var mxCheckerRules = []mxCheckerRule{}

// mxOutcome is what checking and running one cell found.
type mxOutcome struct {
	// class: "rejected", "ok", or a failure: "unexpected rejection",
	// "accepted, expected a rejection", "declines", "panics", "untyped",
	// "crash", "wrong output".
	class  string
	detail string
	// gap names the known gap that excuses a failure.
	gap string
}

func (o mxOutcome) failed() bool {
	return o.class != "rejected" && o.class != "ok" && o.gap == ""
}

func checkCell(dir string, c mxCell) mxOutcome {
	o := checkCellAlone(dir, c)
	if o.gap == "" && (o.class == "declines" || o.class == "untyped" || o.class == "panics") {
		o.gap = mxKnownFor(c)
	}
	return o
}

// mxKnown excuses the cells a pinned gap fails, by the cell's shape, where
// the gap's own pattern cannot recognize them: the builder's reason is one
// it derives from the node alone ("a ListLit", "a struct literal: Hold",
// noSkip), or the checker's report names a position. Each names a
// knownLoweringGaps or knownCheckerGaps entry, whose reproducer pins it.
var mxKnown = []struct {
	gap   string
	match func(c mxCell) bool
}{}

// mxHas reports whether one of c's wrapping contexts is named name.
func mxHas(c mxCell, name string) bool {
	for _, w := range c.wraps {
		if w.name == name {
			return true
		}
	}
	return false
}

func mxKnownFor(c mxCell) string {
	for _, k := range mxKnown {
		if k.match(c) {
			return k.gap
		}
	}
	return ""
}

// checkCellAlone is checkCell before mxKnown excuses a decline by its shape.
func checkCellAlone(dir string, c mxCell) mxOutcome {
	src, transcript := c.source()
	var p *Program
	lo := checkLowersKeeping(dir, src, &p)
	wantReject, why, _ := mxExpectedRejection(c)
	switch {
	case !lo.accepted && wantReject != "" && regexp.MustCompile(wantReject).MatchString(lo.rejection):
		return mxOutcome{class: "rejected"}
	case !lo.accepted && wantReject != "":
		return mxOutcome{class: "unexpected rejection", detail: fmt.Sprintf("the matrix expects a rejection holding %q (%s), and the checker says:\n%s", wantReject, why, lo.rejection)}
	case !lo.accepted && why != "":
		return mxOutcome{class: "unexpected rejection", detail: "the matrix pins a checker hole here (" + why + "); if the checker now rejects it, remove the rule:\n" + lo.rejection}
	case !lo.accepted:
		return mxOutcome{class: "unexpected rejection", detail: lo.rejection}
	case lo.internal != "":
		o := mxOutcome{class: "panics", detail: lo.internal}
		if gap := knownPanicFor(lo.internal); gap != nil {
			o.gap = gap.name
		}
		return o
	case len(lo.unplaced) > 0 || len(lo.declines) > 0:
		o := mxOutcome{class: "declines", detail: strings.Join(lo.declines, "\n")}
		if gap := knownGapFor(lo.reasons); gap != nil && len(lo.unplaced) == 0 {
			o.gap = gap.name
		}
		return o
	case len(lo.unresolved) > 0:
		o := mxOutcome{class: "untyped", detail: strings.Join(lo.unresolved, "\n")}
		all := true
		for _, u := range lo.unresolved {
			if gap := knownCheckerGapFor(u); gap != nil {
				o.gap = gap.name
			} else {
				all = false
			}
		}
		if !all {
			o.gap = ""
		}
		return o
	case wantReject != "":
		return mxOutcome{class: "accepted, expected a rejection", detail: fmt.Sprintf("the matrix expects a rejection holding %q (%s)", wantReject, why)}
	}
	plan := mxPlan(p)
	if plan.main == nil && len(plan.cases) == 0 {
		return mxOutcome{class: "crash", detail: "nothing ran: no main and no test case is retained"}
	}
	run := runOnce(p, plan, dir, nil)
	if o, done := mxRunFailure(run); done {
		return o
	}
	got := mxDbgLine.ReplaceAllString(run.transcript, "")
	if got != transcript {
		return mxOutcome{class: "wrong output", detail: fmt.Sprintf("want transcript:\n%s\ngot:\n%s", transcript, run.transcript)}
	}
	if c.f.tags.has(tagDbg) && got == run.transcript {
		return mxOutcome{class: "wrong output", detail: "no dbg line in:\n" + run.transcript}
	}
	if plan.main != nil {
		if img := builtImage(p); img != nil {
			third := runOnce(p, plan, dir, img)
			if o, done := mxRunFailure(third); done {
				return o
			}
			if third.transcript != run.transcript {
				return mxOutcome{class: "crash", detail: crashImage + ": the built image prints:\n" + third.transcript + "\nthe source prints:\n" + run.transcript}
			}
		}
	}
	return mxOutcome{class: "ok"}
}

// mxPlan is what a cell runs: main, or its test cases. Unlike runPlanFor it
// does not sort effects, since every matrix program stays inside the
// machine and is run once.
func mxPlan(p *Program) runPlan {
	var plan runPlan
	switch {
	case p.entry == nil:
	case p.HasMain():
		plan.main = p.entryFunc("main")
	case p.prog.HasTests:
		plan.cases = p.entry.Tests()
	}
	return plan
}

func mxRunFailure(run runOutcome) (mxOutcome, bool) {
	switch run.class {
	case runCrash:
		o := mxOutcome{class: "crash", detail: run.crash}
		if gap := knownRunGapFor(run.crash); gap != nil {
			o.gap = gap.name
		}
		return o, true
	case runOK:
		return mxOutcome{}, false
	}
	return mxOutcome{class: "wrong output", detail: fmt.Sprintf("the run ends %s (%s):\n%s", run.class, run.skip, run.transcript)}, true
}

// mxPairs is every feature in every context, shell and placement, the
// others at their defaults.
func mxPairs() []mxCell {
	var cells []mxCell
	for i := range mxFeatures {
		f := &mxFeatures[i]
		cells = append(cells, mxCell{f: f, shell: mxShellMain, place: mxPlaceSame})
		for _, w := range mxWraps {
			cells = append(cells, mxCell{f: f, wraps: []*mxContext{w}, shell: mxShellMain, place: mxPlaceSame})
		}
		for _, s := range mxShells[1:] {
			cells = append(cells, mxCell{f: f, shell: s, place: mxPlaceSame})
		}
		for _, p := range mxPlacements[1:] {
			cells = append(cells, mxCell{f: f, shell: mxShellMain, place: p})
		}
	}
	return mxDedupe(cells)
}

// mxTriples is every fragile feature in a fragile context and one more
// context of any kind, on either side of it.
func mxTriples() []mxCell {
	var cells []mxCell
	for i := range mxFeatures {
		f := &mxFeatures[i]
		if !f.tags.has(mxFragile) {
			continue
		}
		for _, a := range mxWraps {
			if !a.fragile {
				continue
			}
			for _, b := range mxWraps {
				cells = append(cells, mxCell{f: f, wraps: []*mxContext{a, b}, shell: mxShellMain, place: mxPlaceSame})
				if b != a {
					cells = append(cells, mxCell{f: f, wraps: []*mxContext{b, a}, shell: mxShellMain, place: mxPlaceSame})
				}
			}
			for _, s := range mxShells[1:] {
				cells = append(cells, mxCell{f: f, wraps: []*mxContext{a}, shell: s, place: mxPlaceSame})
			}
			for _, p := range mxPlacements[1:] {
				cells = append(cells, mxCell{f: f, wraps: []*mxContext{a}, shell: mxShellMain, place: p})
			}
		}
		for _, p := range mxPlacements[1:] {
			for _, b := range mxWraps {
				if !b.fragile {
					cells = append(cells, mxCell{f: f, wraps: []*mxContext{b}, shell: mxShellMain, place: p})
				}
			}
			for _, s := range mxShells[1:] {
				cells = append(cells, mxCell{f: f, shell: s, place: p})
			}
		}
	}
	return mxDedupe(cells)
}

func mxDedupe(cells []mxCell) []mxCell {
	seen := map[string]bool{}
	var out []mxCell
	for _, c := range cells {
		if _, _, skip := mxExpectedRejection(c); skip != "" {
			continue
		}
		if n := c.name(); !seen[n] {
			seen[n] = true
			out = append(out, c)
		}
	}
	return out
}

// mxTripleSample is the share of the triples plain `go test` checks: every
// mxTripleSample-th, from a fixed start.
const mxTripleSample = 12

// checkCells checks cells, reporting each failure with its source, and
// logs the tally.
func checkCells(t *testing.T, cells []mxCell, failure func(i int) string) {
	t.Helper()
	only := os.Getenv("NOMI_MATRIX_ONLY")
	tally := map[string]int{}
	start := time.Now()
	checked := 0
	for i, c := range cells {
		name := c.name()
		if only != "" && !strings.Contains(name, only) {
			continue
		}
		checked++
		o := checkCell(t.TempDir(), c)
		key := o.class
		if o.gap != "" {
			key += " (known: " + o.gap + ")"
		}
		tally[key]++
		if !o.failed() {
			continue
		}
		src, _ := c.source()
		if formatted, err := mxFormat(src); err == nil {
			src = formatted
		}
		pre := ""
		if failure != nil {
			pre = failure(i)
		}
		t.Errorf("%s: %s%s:\n%s\n%s", name, pre, o.class, numbered(src), o.detail)
	}
	var keys []string
	for k := range tally {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "\n  %s: %d", k, tally[k])
	}
	t.Logf("%d cells in %s:%s", checked, time.Since(start).Round(time.Millisecond), b.String())
}

// mxFormat formats each file of src.
func mxFormat(src string) (string, error) {
	if !strings.HasPrefix(src, "// FILE: ") {
		return format.Format(src)
	}
	parts := regexp.MustCompile(`(?m)^// FILE: .*\n`).Split(src, -1)
	heads := regexp.MustCompile(`(?m)^// FILE: .*\n`).FindAllString(src, -1)
	var b strings.Builder
	for i, h := range heads {
		f, err := format.Format(parts[i+1])
		if err != nil {
			return "", err
		}
		b.WriteString(h + f)
	}
	return b.String(), nil
}

// TestFeatureContextMatrix checks every pair and a fixed sample of the
// triples; NOMI_MATRIX=full checks every triple.
func TestFeatureContextMatrix(t *testing.T) {
	t.Setenv("NOMI_DEBUG_LOWERING", "1")
	for _, k := range mxKnown {
		if !mxPinned(k.gap) {
			t.Errorf("mxKnown names %s, which no knownLoweringGaps or knownCheckerGaps entry pins", k.gap)
		}
	}
	pairs, triples := mxPairs(), mxTriples()
	full := os.Getenv("NOMI_MATRIX") == "full"
	var sample []mxCell
	for i, c := range triples {
		if full || i%mxTripleSample == 0 {
			sample = append(sample, c)
		}
	}
	t.Logf("matrix: %d features, %d wrapping contexts, %d more shells, %d more placements; %d pairs, %d triples, checking %s",
		len(mxFeatures), len(mxWraps), len(mxShells)-1, len(mxPlacements)-1, len(pairs), len(triples),
		map[bool]string{true: "all", false: fmt.Sprintf("every %dth triple", mxTripleSample)}[full])
	t.Log("pairs:")
	checkCells(t, pairs, nil)
	t.Log("triples:")
	checkCells(t, sample, nil)
}

// mxPinned reports whether a known gap list holds the gap named name.
func mxPinned(name string) bool {
	for _, g := range knownLoweringGaps {
		if g.name == name {
			return true
		}
	}
	for _, g := range knownCheckerGaps {
		if g.name == name {
			return true
		}
	}
	return false
}

// TestFeatureContextMatrixRotating checks a sample of the triples that
// changes every UTC day (internal/rotation).
func TestFeatureContextMatrixRotating(t *testing.T) {
	t.Setenv("NOMI_DEBUG_LOWERING", "1")
	triples := mxTriples()
	set := rotation.For(t, "./vmhost", 60)
	var cells []mxCell
	var seeds []int64
	for _, seed := range set.Seeds() {
		cells = append(cells, triples[rand.New(rand.NewSource(seed)).Intn(len(triples))])
		seeds = append(seeds, seed)
	}
	checkCells(t, cells, func(i int) string { return set.Failure(seeds[i]) })
}
