package vmhost

import (
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"regexp"
	"runtime"
	rdebug "runtime/debug"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/format"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/irbuild"
	"github.com/nomi-language/nomi/internal/rotation"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/std"
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
//     makes the triples. Some exist because the builder reaches the value
//     by a route of its own: a field of a struct whose impls are derived
//     (the derived Debug, equality, hash and JSON bodies run at the
//     feature's type), a generic owner function's argument or body
//     (`impl Own<A> { fn get<A>(...) }`, instantiated per call under the
//     block's frame), a generic function of std/presence, which is lowered
//     with each program, and an interface default or a parameter default
//     declared in another file than the one that uses it.
//   - A shell (mxShells) is where the program observes the final value:
//     `fn main` (the default), a test body, an `assert` subject, a
//     `subtests` row or row name, a `defer`'d call, an application field
//     written by `with`, or a std module's `//!` prompt.
//   - A placement (mxPlacements) says which file holds what: one file (the
//     default), the feature's declarations in a second file, the whole
//     value made by a function of a second file, the feature's impls in
//     another file than its types, or a std module (mxStdPlacements): the
//     whole cell appended to std/presence, which is lowered with each
//     program, or to std/hex, lowered once for the process, and run by
//     the module's own tests as `nomi test std/<module>.nomi` runs an
//     edited module. There the main shell is a test body, and the `//!`
//     prompt shell joins the test shells. std/hex holds no cell that
//     declares a type, which a module lowered once cannot.
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
// rule. A gap only a std module's tests reach names the module in its
// `std` field, and its reproducer is the text appended to it.
//
// Plain `go test` runs in about 30 seconds, a per-change check. The pairs
// (each feature in each context, shell and placement alone) all run, but
// for a std placement's: its cell checks and lowers a whole std module,
// about five times a program cell's cost, so only every mxStdSample-th
// runs. The triples (a fragile feature in a fragile context and one more)
// are many more: every mxTripleSample-th runs, and of a std placement's
// every mxStdSample-th of those (mxSample). NOMI_MATRIX=full runs every
// cell (several minutes), and TestFeatureContextMatrixRotating checks 60
// more triples that change every UTC day. NOMI_MATRIX_ONLY=<text> checks
// only the cells whose name contains text, such as "unit × with" or "std
// module", in either size. Cells are checked on every CPU at once and
// reported in order.
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
	// expects, and has none without one (`.Circle(1.0)`, `fn n -> n + 1`,
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
	// tagCall: the feature is a call with named arguments or defaults, a
	// capture, or a call qualified other than by its file.
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
	mxPoint = "struct Point\n    x: Int\n    y: Int = 2\nend"
	mxShape = "struct Spot\n    n: Int\nend\n\ntype Tag String\n\nenum Shape\n    embeds Spot\n    embeds Tag\n    Circle Float\n    Rect {w: Int, h: Int}\n    Dot\nend"
	mxNamed = "interface Named\n    fn name(value: self) -> String\n    fn shout(value: self) -> String\n        name(value) + \"!\"\n    end\nend\n\nstruct Pt\n    x: Int\nend\n\nimpl Named for Pt\n    fn name(p: Pt) -> String\n        \"pt${p.x}\"\n    end\nend"
	// A distinct type with `|||` and `~~~` of its own.
	mxFlags = "type Flags Int\n\nimpl BitwiseOr<Flags, Flags> for Flags\n    fn bitwise_or(lhs: Flags, rhs: Flags) -> Flags\n        Flags(x) = lhs\n        Flags(y) = rhs\n        Flags(x ||| y)\n    end\nend\n\n" +
		"impl BitwiseNot<Flags> for Flags\n    fn bitwise_not(value: Flags) -> Flags\n        Flags(x) = value\n        Flags(~~~x)\n    end\nend"
	mxAdd3  = "fn add3(a: Int, b: Int, c: Int = 100) -> Int\n    a + b + c\nend"
	mxGreet = "fn greet(name: String, punct: String = \"!\", times: Int = 1) -> String\n    \"${name}${punct}${times}\"\nend"
	mxBox   = "struct Box<T>\n    v: T\nend"
	mxTree  = "enum Tree<T>\n    Leaf T\n    Nil\nend"
	mxCoin  = "enum Coin\n    Penny\n    Dime\nend"
	mxVTree = "enum VTree\n    Leaf Int\n    Node Vector<VTree>\nend\n\nderive Equatable for VTree"
	mxVDir  = "struct VDir\n    name: String\n    kids: Vector<VDir>\nend"
	// A function whose Result a `!` call unwraps.
	mxHalf  = "fn half(n: Int) -> Result<Int, String>\n    if n % 2 == 0 then\n        Ok(n / 2)\n    else\n        Err(\"odd\")\n    end\nend"
	mxHolds = "fn has<C, T>(c: C, x: T) -> Bool where C: Contains<T>\n    C.contains(c, x)\nend"
	// An interface with a default, implemented for a generic struct.
	mxLabeled = "interface Labeled\n    fn label(value: self) -> String\n    fn loud(value: self) -> String\n        Labeled.label(value) + \"!\"\n    end\nend\n\nstruct Bx<T>\n    v: T\nend\n\nimpl Labeled for Bx<T>\n    fn label(b: Bx<T>) -> String\n        \"bx${Debug.inspect(b.v)}\"\n    end\nend"
	mxGets    = "interface Gets<T>\n    fn got(value: self) -> T\nend\n\nstruct Bag\n    s: String\nend\n\nimpl Gets<String> for Bag\n    fn got(b: Bag) -> String\n        b.s\n    end\nend"
	// Two generic interfaces, each implemented for one type at two type
	// arguments: Takes chosen by an argument, Gives by the result.
	mxTwo = "interface Takes<T>\n    fn takes(value: self, x: T) -> String\nend\n\ninterface Gives<T>\n    fn gives(value: self) -> T\nend\n\nstruct Two\n    n: Int\nend\n\n" +
		"impl Takes<Int> for Two\n    fn takes(t: Two, x: Int) -> String\n        \"int${t.n + x}\"\n    end\nend\n\n" +
		"impl Takes<String> for Two\n    fn takes(_t: Two, x: String) -> String\n        \"str${x}\"\n    end\nend\n\n" +
		"impl Gives<Int> for Two\n    fn gives(t: Two) -> Int\n        t.n\n    end\nend\n\n" +
		"impl Gives<String> for Two\n    fn gives(_t: Two) -> String\n        \"two\"\n    end\nend\n\n" +
		"fn took<C, T>(c: C, x: T) -> String where C: Takes<T>\n    C.takes(c, x)\nend\n\n" +
		"fn gives_text(g: Gives<String>) -> String\n    Gives.gives(g)\nend"
	// A program interface implemented for std containers: generically over
	// List and Maybe, at a concrete Map, and receiverless for List.
	mxShow = "interface Show\n    fn show(value: self) -> String\n    fn loud(value: self) -> String\n        Show.show(value) + \"!\"\n    end\nend\n\n" +
		"interface Zero\n    fn zero(n: Int) -> self\nend\n\n" +
		"impl Show for Int\n    fn show(n: Int) -> String\n        \"i${n}\"\n    end\nend\n\n" +
		"impl Zero for Int\n    fn zero(n: Int) -> Int\n        n\n    end\nend\n\n" +
		"impl Show for List<T> where T: Show\n    fn show(xs: List<T>) -> String\n        \"[\" + (xs |> Iter.map(&T.show(&1)) |> String.join(\" \")) + \"]\"\n    end\nend\n\n" +
		"impl Show for Maybe<T> where T: Show\n    fn show(m: Maybe<T>) -> String\n        case m\n            Some(x) -> \"some \" + T.show(x)\n            None -> \"none\"\n        end\n    end\nend\n\n" +
		"impl Show for Map<String, Int>\n    fn show(m: Map<String, Int>) -> String\n        \"map${Map.size(m)}\"\n    end\nend\n\n" +
		"impl Zero for List<T> where T: Zero\n    fn zero(n: Int) -> List<T>\n        Iter.repeat(T.zero(n)) |> Iter.take(2) |> Iter.to_list()\n    end\nend\n\n" +
		"fn shown<T>(x: T) -> String where T: Show\n    T.loud(x)\nend"
	// A program's generic interface implemented for a std container.
	mxHas = "interface Has<T>\n    fn count_of(value: self, x: T) -> String\nend\n\n" +
		"impl Has<Int> for List<Int>\n    fn count_of(xs: List<Int>, x: Int) -> String\n" +
		"        if (xs |> Iter.filter(&(&1 == x)) |> Iter.count()) == 2 then\n            \"two\"\n        else\n            \"other\"\n        end\n    end\nend"
	// std interfaces implemented for std receivers at a type argument the
	// program declares, which the orphan rule admits through it: an
	// operator on Int and a Contains on String.
	mxPct = "type Pct Int\n\nstruct Sep\n    s: String\nend\n\n" +
		"impl Multiply<Pct, Int> for Int\n    fn multiply(lhs: Int, rhs: Pct) -> Int\n        Pct(p) = rhs\n        lhs * p / 100\n    end\nend\n\n" +
		"impl Contains<Sep> for String\n    fn contains(c: String, x: Sep) -> Bool\n        String.contains(c, x.s)\n    end\nend\n\n" +
		"fn scaled<T>(x: T, p: Pct) -> T where T: Multiply<Pct, T>\n    x * p\nend"
	// An interface implemented for an enum with an `embeds` variant and for
	// the struct it embeds.
	mxSays = mxShape + "\n\ninterface Says\n    fn says(value: self) -> String\nend\n\n" +
		"impl Says for Shape\n    fn says(s: Shape) -> String\n        case s\n            .Spot{n:} -> \"shape spot ${n}\"\n            _ -> \"shape other\"\n        end\n    end\nend\n\n" +
		"impl Says for Spot\n    fn says(s: Spot) -> String\n        \"spot ${s.n}\"\n    end\nend\n\n" +
		"impl Says for Int\n    fn says(n: Int) -> String\n        \"int ${n}\"\n    end\nend\n\n" +
		"fn spot_shape() -> Shape\n    Spot{n: 2}\nend"
	// A generic enum whose impl block holds a generic owner function over a
	// nested instance, as std/presence's Presence.flatten is.
	mxOpt = "enum Opt<T>\n    Full T\n    Empty\nend\n\n" +
		"impl Opt<T>\n    pub fn flat<T>(o: Opt<Maybe<T>>) -> Maybe<T>\n        case o\n            .Full(m) -> m\n            .Empty -> None\n        end\n    end\n" +
		"    pub fn map<T, U>(o: Opt<T>, f: (T) -> U) -> Opt<U>\n        case o\n            .Full(v) -> Opt.Full(f(v))\n            .Empty -> Opt.Empty\n        end\n    end\nend"
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
	{name: "empty vector", typ: "Vector<Int>", expr: "Vector[]", want: "Vector[]", tags: tagUntyped | tagBareOK, reject: mxUnsolved},
	{name: "vector", typ: "Vector<Int>", expr: "Vector[1, 2]", want: "Vector[1, 2]"},
	{name: "empty set", typ: "Set<Int>", expr: "Set[]", want: "Set[]", tags: tagUntyped | tagBareOK, reject: mxUnsolved},
	{name: "set", typ: "Set<Int>", expr: "Set[3, 4]", want: "Set[3, 4]"},
	{name: "map", typ: "Map<String, Int>", expr: `{"a" => 1}`, want: `{"a" => 1}`},
	{name: "map spread", typ: "Map<String, Int>", expr: `{..{"a" => 1}, "b" => 2}`, want: `{"a" => 1, "b" => 2}`},
	{name: "empty map", typ: "Map<String, Int>", expr: "{}", want: "{}", tags: tagUntyped | tagBareOK, reject: mxUnsolved},
	{name: "tuple", typ: "(Int, String)", expr: `(1, "a")`, want: `(1, "a")`},
	{name: "record", typ: "{a: Int, b: String}", expr: `{a: 1, b: "x"}`, want: `{a: 1, b: "x"}`},
	{name: "range", typ: "Range<Int>", expr: "1..4", want: "1..4"},
	{name: "not", typ: "Bool", expr: `not 1 == 2 and not ([1] |> Iter.is_empty())`, want: "True"},
	{name: "not stage", typ: "Bool", expr: `"" |> not String.contains("x")`, want: "True"},
	{name: "is test", typ: "Bool", expr: `Some(2) is Some(1 or 2) and not [1] is []`, want: "True"},
	{name: "in test", typ: "Bool", expr: `2 in [1, 2] and 3 in 1..5 and not "z" in {"a" => 1}`, want: "True"},
	{name: "bitwise operators", typ: "Int", expr: "(12 &&& 10 ||| 1 <<< 4) ^^^ ~~~0 >>> 1", want: "-25"},
	{name: "byte bitwise operators", typ: "Byte", expr: "~~~Int.to_byte(15)! &&& 0xFF", want: "240"},
	{name: "byte literal", typ: "Byte", expr: "0x81", want: "129", tags: tagWidens},

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
	{name: "bitwise operator impl", decls: mxFlags, typ: "Flags", expr: "~~~(Flags(1) ||| Flags(4))", want: "Flags(-6)"},
	{name: "unit", typ: "Unit", expr: "Unit", want: "Unit", tags: tagUnit},
	{name: "phrase", decls: "phrase greet(name: String) `Hello, {$name}!`", typ: "Phrase", expr: `greet("Ann")`,
		obs: "Phrase.format($v, Locale`pl`)", want: "Hello, Ann!"},
	{name: "generic struct", decls: mxBox, typ: "Box<Int>", expr: "Box{v: 1}", want: "Box{v: 1}"},
	{name: "generic enum", decls: mxTree, typ: "Tree<String>", expr: `Tree.Leaf("a")`, want: `Leaf("a")`},
	{name: "generic bare variant", decls: mxTree, typ: "Tree<Int>", expr: "Tree.Nil", want: "Nil", tags: tagUntyped | tagBareOK, reject: mxUnsolved},
	{name: "recursive vector enum", decls: mxVTree, typ: "VTree", expr: "VTree.Node(Vector[VTree.Leaf(1), VTree.Node(Vector[])])",
		obs: `"${Debug.inspect($v)} ${$v == $v}"`, want: "Node(Vector[Leaf(1), Node(Vector[])]) True"},
	{name: "recursive vector struct", decls: mxVDir, typ: "VDir", expr: `VDir{name: "r", kids: Vector[VDir{name: "a", kids: Vector[]}]}`,
		want: `VDir{name: "r", kids: Vector[VDir{name: "a", kids: Vector[]}]}`},

	// Function values.
	{name: "named function", decls: "fn twice(n: Int) -> Int\n    n * 2\nend", typ: "(Int) -> Int", expr: "twice",
		obs: "Debug.inspect($v(2))", want: "4", tags: tagFn},
	{name: "lambda", typ: "(Int) -> Int", expr: "fn n: Int -> n + 1", obs: "Debug.inspect($v(2))", want: "3", tags: tagFn},
	{name: "unannotated lambda", typ: "(Int) -> Int", expr: "fn n -> n + 1", obs: "Debug.inspect($v(2))", want: "3",
		tags: tagFn | tagExpects, reject: "cannot infer type for parameter"},
	{name: "captured lambda", typ: "(Int) -> Int", expr: "begin\n    k = 5\n    fn n: Int -> n + k\nend",
		obs: "Debug.inspect($v(2))", want: "7", tags: tagFn | tagNested},
	{name: "partial", decls: mxAdd3, typ: "(Int) -> Int", expr: "&add3(&1, 10)", obs: "Debug.inspect($v(2))", want: "112",
		tags: tagFn | tagCall},
	{name: "named partial", decls: mxAdd3, typ: "(Int) -> Int", expr: "&add3(1, b: &1)", obs: "Debug.inspect($v(2))",
		want: "103", tags: tagFn | tagCall},
	{name: "operator function partial", typ: "(Int) -> Int", expr: "&Int.add(7, &1)", obs: "Debug.inspect($v(2))",
		want: "9", tags: tagFn | tagCall},
	{name: "operator capture", typ: "(Int) -> Bool", expr: "&(&1 > 2)", obs: "Debug.inspect($v(3))", want: "True",
		tags: tagFn | tagExpects | tagCall, reject: "cannot infer type for `&1`"},
	{name: "struct capture", decls: mxPoint, typ: "(Int) -> Point", expr: "&Point{x: &1, y: 5}",
		obs: "Debug.inspect($v(1))", want: "Point{x: 1, y: 5}", tags: tagFn | tagExpects | tagCall,
		reject: "cannot infer type for `&1`"},
	{name: "numbered capture parameters", typ: "(Int, Int) -> Int", expr: "&(&2 * &2 - &1)", obs: "Debug.inspect($v(1, 10))",
		want: "99", tags: tagFn | tagExpects | tagCall, reject: "cannot infer type for `&2`"},
	{name: "capture ignoring a parameter", typ: "(Int, Int) -> Int", expr: "&(&2)", obs: "Debug.inspect($v(1, 10))",
		want: "10", tags: tagFn | tagExpects | tagCall, reject: "cannot infer type for `&2`"},
	{name: "reordering capture", decls: mxAdd3, typ: "(Int, Int) -> Int", expr: "&add3(&2, 0, c: &1)",
		obs: "Debug.inspect($v(1, 10))", want: "11", tags: tagFn | tagExpects | tagCall, reject: "cannot infer type for `&2`"},
	{name: "list capture", typ: "(Int) -> List<Int>", expr: "&[&1, 0]", obs: "Debug.inspect($v(4))",
		want: "[4, 0]", tags: tagFn | tagExpects | tagCall, reject: "cannot infer type for `&1`"},
	{name: "constructor value", decls: mxShape, typ: "(Float) -> Shape", expr: "Shape.Circle",
		obs: "Debug.inspect($v(2.0))", want: "Circle(2.0)", tags: tagFn},
	{name: "owner function value", typ: "(String) -> Int", expr: "String.length", obs: `Debug.inspect($v("abc"))`,
		want: "3", tags: tagFn | tagCall},
	{name: "interface function value", typ: "(Int) -> String", expr: "Display.to_string",
		obs: "Debug.inspect($v(12))", want: `"12"`, tags: tagFn | tagCall | tagExpects | tagBareOK,
		reject: "cannot infer type parameter"},
	{name: "unit function", decls: "fn noop(_n: Int) end", typ: "(Int) -> Unit", expr: "noop",
		obs: "Debug.inspect($v(1))", want: "Unit", tags: tagFn | tagUnit},
	{name: "intrinsic partial", typ: "(String) -> String", expr: "&String.pad_start(&1, width: 3, fill: \"0\")",
		obs: `Debug.inspect($v("7"))`, want: `"007"`, tags: tagFn | tagCall},

	// Interface values.
	{name: "existential", decls: mxNamed + "\n\nfn named_of(n: Int) -> Named\n    Pt{x: n}\nend", typ: "Named",
		expr: "named_of(4)", obs: "Named.shout($v)", want: "pt4!", tags: tagExistential},
	{name: "existential widening", decls: mxNamed, typ: "Named", expr: "Pt{x: 5}", obs: "Named.shout($v)",
		want: "pt5!", tags: tagExistential | tagWidens},
	{name: "generic existential", decls: mxLabeled, typ: "Labeled", expr: "Bx{v: 3}", obs: "Labeled.loud($v)",
		want: "bx3!", tags: tagExistential | tagWidens},
	{name: "generic interface call", decls: mxGets, typ: "String", expr: `Gets.got(Bag{s: "x"})`, want: `"x"`,
		tags: tagCall},
	{name: "two-instantiation call", decls: mxTwo, typ: "String",
		expr: `Takes.takes(Two{n: 1}, 2) + Two.takes(Two{n: 1}, "a")`, want: `"int3stra"`, tags: tagCall},
	{name: "two-instantiation bound", decls: mxTwo, typ: "String", expr: `took(Two{n: 2}, "b") + took(Two{n: 2}, 5)`,
		want: `"strbint7"`, tags: tagCall},
	{name: "two-instantiation existential", decls: mxTwo, typ: "Gives<String>", expr: "Two{n: 4}",
		obs: "gives_text($v)", want: "two", tags: tagExistential | tagWidens},
	{name: "two-instantiation result", decls: mxTwo, typ: "Int", expr: "Gives.gives(Two{n: 6})", want: "6",
		tags: tagExpects, reject: "only the result's type tells them apart"},
	{name: "local type argument operator", decls: mxPct, typ: "Int",
		expr: "300 * Pct(50) + Int.multiply(10, Pct(30)) + Multiply.multiply(100, Pct(1)) + scaled(40, Pct(50)) + 2 * 3",
		want: "180"},
	{name: "local type argument contains", decls: mxPct, typ: "Bool",
		expr: `String.contains("a-b", Sep{s: "-"}) and Contains.contains("ab", Sep{s: "b"}) and String.contains("ab", "a")`,
		want: "True"},
	{name: "container impl call", decls: mxShow, typ: "String",
		expr: `Show.loud([[1], [2]]) + List.show([3]) + Show.show({"a" => 1})`, want: `"[[i1] [i2]]![i3]map1"`, tags: tagCall},
	{name: "container impl bound", decls: mxShow, typ: "String", expr: "shown(Some([4]))", want: `"some [i4]!"`,
		tags: tagCall},
	{name: "container receiverless impl", decls: mxShow, typ: "List<List<Int>>", expr: "Zero.zero<List<List<Int>>>(5)",
		want: "[[5, 5], [5, 5]]", tags: tagCall},
	{name: "container existential", decls: mxShow, typ: "Show", expr: "[1, 2]", obs: "Show.loud($v)", want: "[i1 i2]!",
		tags: tagExistential | tagWidens},
	// Two instances of one container in one list of existentials: each
	// element's box names its own instance, whose body differs.
	{name: "container existential instances", decls: mxShow, typ: "List<Show>", expr: "[[1], [[2]], Some(3)]",
		obs: `$v |> Iter.map(&Show.show(&1)) |> String.join(",")`, want: "[i1],[[i2]],some i3",
		tags: tagExistential | tagExpects, reject: "list element type mismatch"},
	{name: "container generic existential", decls: mxHas, typ: "Has<Int>", expr: "[1, 2, 2]", obs: "Has.count_of($v, 2)",
		want: "two", tags: tagExistential | tagWidens},
	// An `embeds` enum's widened struct and its own variant held as the
	// interface beside the embedded struct, which implements it too.
	{name: "embeds existential", decls: mxSays, typ: "Says", expr: "spot_shape()", obs: "Says.says($v)",
		want: "shape spot 2", tags: tagExistential | tagWidens},
	{name: "embeds existential list", decls: mxSays, typ: "List<Says>", expr: "[spot_shape(), 5, Shape.Dot, Spot{n: 4}]",
		obs: `$v |> Iter.map(&Says.says(&1)) |> String.join(",")`, want: "shape spot 2,int 5,shape other,spot 4",
		tags: tagExistential | tagExpects, reject: "list element type mismatch"},
	// Interface values compared, and as a Map's keys and a Set's elements:
	// by the concrete value's type, then its value.
	{name: "existential equality", decls: mxNamed + "\n\nfn self_equal(n: Named) -> String\n    \"${n == n} ${n != n}\"\nend",
		typ: "Named", expr: "Pt{x: 5}", obs: "self_equal($v)", want: "True False", tags: tagExistential | tagWidens},
	{name: "existential map key", decls: mxShow, typ: "Map<Show, Int>", expr: "{[1] => 7, 1 => 8}",
		obs: "Debug.inspect(Map.get($v, [1]))", want: "Some(7)", tags: tagExistential | tagExpects, reject: "map key type mismatch"},
	{name: "container existential set", decls: mxShow, typ: "Set<Show>", expr: "Set[[1], 1, [1]]",
		obs: "Debug.inspect(Set.size($v))", want: "2", tags: tagExistential | tagExpects, reject: "set element type mismatch"},
	// Containers and an `embeds` enum's value held as std's Display: boxed
	// with the type each had there, so a List and a Vector are two values
	// and the enum renders through its own impl.
	{name: "std container existential set", typ: "Set<Display>", expr: "Set[[1], Iter.to_vector([1]), [1]]",
		obs: "Debug.inspect(Set.size($v))", want: "2", tags: tagExistential | tagExpects, reject: "set element type mismatch"},
	{name: "embeds display existential", decls: mxSays + "\n\nimpl Display for Shape\n    fn to_string(_s: Shape) -> String\n        \"a shape\"\n    end\nend\n\n" +
		"impl Display for Spot\n    fn to_string(_s: Spot) -> String\n        \"a spot\"\n    end\nend",
		typ: "Display", expr: "spot_shape()", obs: "Display.to_string($v)", want: "a shape", tags: tagExistential | tagWidens},
	// Interface values through an Equatable and a Hashable bound: each
	// instance compares and hashes them as `==` and a Set do.
	{name: "existential equality bounds", decls: mxShow + "\n\nfn kinds<T>(xs: List<T>) -> String where T: Equatable and Hashable\n" +
		"    \"${Set.size(Iter.to_set(xs))} ${xs |> Iter.filter(&List.contains(xs, &1)) |> Iter.count()}\"\nend",
		typ: "List<Show>", expr: "[[1], 1, [1]]", obs: "kinds($v)", want: "2 3", tags: tagExistential | tagExpects, reject: "list element type mismatch"},
	{name: "existential list", typ: "List<Display>", expr: `[1, "a"]`,
		obs: `$v |> Iter.map(fn d -> Display.to_string(d)) |> String.join(",")`, want: "1,a",
		tags: tagExistential | tagExpects, reject: "list element type mismatch"},

	// Maybe and Result.
	{name: "some", typ: "Maybe<Int>", expr: "Some(3)", want: "Some(3)"},
	{name: "none", typ: "Maybe<Int>", expr: "None", want: "None", tags: tagUntyped | tagBareOK, reject: mxUnsolved},
	{name: "ok", typ: "Result<Int, String>", expr: "Ok(1)", want: "Ok(1)", tags: tagPartial, reject: mxRows},
	{name: "err", typ: "Result<Int, String>", expr: `Err("bad")`, want: `Err("bad")`, tags: tagPartial, reject: mxRows},
	{name: "maybe map", typ: "Maybe<Int>", expr: "Maybe.map(Some(2), fn n -> n * 3)", want: "Some(6)"},
	{name: "maybe grid", typ: "Maybe<Vector<Vector<Int>>>", expr: "Vector.get(Vector[Vector[Vector[1]], Vector[Vector[3]]], 1)",
		want: "Some(Vector[Vector[3]])"},
	{name: "vector get!", typ: "String", expr: `Vector.get(Vector["a", "b"], 1)!`, want: `"b"`, tags: tagCall},
	{name: "result bang", decls: mxHalf, typ: "Int", expr: `8 |> half()! |> half()!`, want: "2", tags: tagCall},
	{name: "list contains", typ: "Bool", expr: `List.contains(["a", "b"], "b")`, want: "True", tags: tagCall},
	{name: "contains bound", decls: mxHolds, typ: "Bool", expr: `has({"k" => 1}, "k") and not has(Vector[1, 2], 3)`,
		want: "True", tags: tagCall},

	// Iter chains.
	{name: "iter chain", typ: "Iter<Int>", expr: "[1, 2, 3] |> Iter.map(fn n -> n * 2) |> Iter.filter(fn n -> n > 2)",
		obs: "Debug.inspect(Iter.to_list($v))", want: "[4, 6]", tags: tagIter},
	{name: "range iter", typ: "Iter<Int>", expr: "1..=3 |> Iter.map(fn n -> n * n)",
		obs: "Debug.inspect(Iter.to_list($v))", want: "[1, 4, 9]", tags: tagIter},
	{name: "iter reduce", typ: "Int", expr: "[1, 2, 3] |> Iter.reduce(0, fn acc, n -> acc + n)", want: "6"},
	{name: "iter scan", typ: "Iter<Int>", expr: "[3, 1, 4] |> Iter.scan(0, fn total, n -> total + n)",
		obs: "Debug.inspect(Iter.to_list($v))", want: "[3, 4, 8]", tags: tagIter},
	{name: "iter map_reduce", typ: "(Int, List<String>)", expr: `["a", "bb"] |> Iter.map_reduce(0, fn at, s -> (at + 1, "${at}${s}"))`,
		want: `(2, ["0a", "1bb"])`},
	// A seed bound by a pattern, which `continue` keeps whole.
	{name: "iter reduce pattern seed", typ: "(Int, List<Int>)",
		expr: "[1, 2, 3] |> Iter.reduce((0, []), fn (n, xs), x ->\n    if x == 2 then continue\n    (n + x, xs + [x])\nend)",
		want: "(4, [1, 3])", tags: tagNested},
	// A fold's starting value given by name, with a named function.
	{name: "iter reduce from", typ: "Int", expr: "[1, 2, 3] |> Iter.reduce(10, Int.add)", want: "16"},
	// An iter-sensitive helper is the callback itself: its `continue` skips
	// the element and its `break v` answers v and stops, as the callback's
	// own would. A fold and a loop take their starting value as `from:`.
	{name: "iter-sensitive callback", decls: "fn skip_two(n: Int) -> Int\n    if n == 2 then continue\n    n * 10\nend",
		typ: "List<Int>", expr: "[1, 2, 3] |> Iter.map(skip_two) |> Iter.to_list()", want: "[10, 30]", tags: tagNested},
	{name: "iter-sensitive fold break", decls: "fn stop_big(acc: Int, n: Int) -> Int\n    if n > 2 then break acc * 10\n    acc + n\nend",
		typ: "Int", expr: "[1, 2, 3, 4] |> Iter.reduce(0, stop_big)", want: "30", tags: tagNested},
	{name: "iter-sensitive loop step", decls: "fn count_up(n: Int) -> Int\n    if n == 1 then return 3\n    if n > 4 then break n * 10\n    n + 1\nend",
		typ: "Int", expr: "Iter.loop(0, count_up)", want: "50", tags: tagNested},
	// Called anywhere, the whole body of a callback lambda included, its
	// `continue` would cross the caller's boundary too: rejected in every
	// context.
	{name: "iter-sensitive call", decls: "fn skip_two(n: Int) -> Int\n    if n == 2 then continue\n    n * 10\nend",
		typ: "List<Int>", expr: "[1, 2, 3] |> Iter.map(fn x ->\n    y = skip_two(x)\n    y + 1\nend) |> Iter.to_list()",
		want: "[11, 31]", tags: tagNested},
	{name: "iter-sensitive lambda body", decls: "fn skip_two(n: Int) -> Int\n    if n == 2 then continue\n    n * 10\nend",
		typ: "List<Int>", expr: "[1, 2, 3] |> Iter.map(fn x -> skip_two(x)) |> Iter.to_list()",
		want: "[10, 30]", tags: tagNested},
	// `Summable.empty_sum()` makes a T with no element, so T must be
	// determined: by the position over `[]`, or by the inner lists' List.
	{name: "sum of an empty list", typ: "Int", expr: "Iter.sum([])", want: "0", tags: tagUntyped, reject: mxUnsolved},
	{name: "sum of empty lists", typ: "List<Int>", expr: "Iter.sum([[]])", want: "[]", tags: tagUntyped | tagBareOK, reject: mxUnsolved},
	{name: "sum turbofish", typ: "Int", expr: "Iter.sum<Int>([])", want: "0", tags: tagCall},

	// Expressions.
	{name: "case", typ: "String", expr: "case 2\n    1 -> \"one\"\n    _ -> \"many\"\nend", want: `"many"`, tags: tagNested},
	{name: "if", typ: "String", expr: `if 2 > 1 then "yes" else "no"`, want: `"yes"`},
	{name: "block", typ: "Int", expr: "begin\n    a = 2\n    a * 3\nend", want: "6", tags: tagNested},
	{name: "try", decls: "fn half(n: Int) -> Result<Int, String>\n    Ok(n / 2)\nend", typ: "Result<Int, String>",
		expr: "begin\n    f = fn ->\n        h = half(8)?\n        Ok(h + 1)\n    end\n    f()\nend", want: "Ok(5)", tags: tagNested},
	{name: "maybe?", typ: "Maybe<Int>",
		expr: "begin\n    f = fn ->\n        h = Some(8)?\n        Some(h + 1)\n    end\n    f()\nend", want: "Some(9)", tags: tagNested},
	{name: "dbg", typ: "Int", expr: "dbg 7", want: "7", tags: tagDbg},
	{name: "and or", typ: "Bool", expr: "1 > 2 or (3 > 2 and True)", want: "True"},
	{name: "user operator", decls: "struct Money\n    n: Int\nend\n\nimpl Add<Int, Int> for Money\n    fn add(lhs: Money, rhs: Int) -> Int\n        lhs.n + rhs\n    end\nend",
		typ: "Int", expr: "Money{n: 1} + 4", want: "5"},
	{name: "pipe", typ: "Int", expr: `"abc" |> String.length()`, want: "3"},
	{name: "then stage", typ: "Int", expr: "4 |> then fn n -> n * n", want: "16"},
	{name: "field access", decls: mxPoint, typ: "Int", expr: "Point{x: 5}.x", want: "5"},
	{name: "struct update", decls: mxPoint, typ: "Point", expr: "Struct.update(Point{x: 1}, {y: 9})",
		want: "Point{x: 1, y: 9}"},
	{name: "struct spread", decls: mxPoint + "\n\nfn origin() -> Point\n    Point{x: 0, y: 0}\nend", typ: "Point",
		expr: "{..origin(), x: 7}", want: "Point{x: 7, y: 0}"},
	{name: "nested spread", decls: mxPoint + "\n\nstruct Line\n    a: Point\n    b: Point\nend\n\nfn line0() -> Line\n    Line{a: Point{x: 0}, b: Point{x: 1}}\nend",
		typ: "Line", expr: "{..line0(), a: {.., x: 5}}", want: "Line{a: Point{x: 5, y: 2}, b: Point{x: 1, y: 2}}"},

	// Patterns.
	{name: "tuple pattern", typ: "String", expr: "case (1, \"a\")\n    (n, s) -> \"${n}${s}\"\nend", want: `"1a"`, tags: tagNested},
	{name: "list pattern", typ: "Int", expr: "case [1, 2, 3]\n    [h, ..t] -> h + Iter.count(t)\n    [] -> 0\nend", want: "3",
		tags: tagNested},
	{name: "struct pattern", decls: mxPoint, typ: "Int", expr: "case (Point{x: 1, y: 5})\n    Point{x:, y:} -> x + y\nend",
		want: "6", tags: tagNested},
	{name: "variant guard pattern", decls: mxShape, typ: "String",
		expr: "case Shape.Circle(3.0)\n    .Circle(r) when r > 2.0 -> \"big\"\n    _ -> \"other\"\nend", want: `"big"`, tags: tagNested},
	{name: "map pattern", typ: "Int", expr: "case {\"k\" => 4}\n    {\"k\" => w} -> w\n    _ -> 0\nend", want: "4",
		tags: tagNested},
	{name: "vector pattern", typ: "Int", expr: "case Vector[1, 2, 3]\n    [h, ..mid, z] -> h + z + Vector.length(mid)\n    _ -> 0\nend",
		want: "5", tags: tagNested},
	{name: "vector end pattern", typ: "Int", expr: "case Vector[4, 5]\n    [] -> 0\n    [.., last] -> last\nend", want: "5",
		tags: tagNested},
	{name: "set pattern", typ: "Int", expr: "case Set[\"a\", \"b\"]\n    [\"a\", ..rest] -> Set.size(rest)\n    _ -> 0\nend",
		want: "1", tags: tagNested},
	{name: "exact set pattern", typ: "String", expr: "case Set[2, 1]\n    [1, 2] -> \"both\"\n    _ -> \"no\"\nend",
		want: `"both"`, tags: tagNested},

	{name: "prefix pattern", typ: "String", expr: "case \"/u/7\"\n    \"/u/\" + id -> id\n    _ -> \"\"\nend", want: `"7"`,
		tags: tagNested},
	{name: "as pattern", typ: "String",
		expr: "case Some(2)\n    Some(n) as whole -> \"${n}${Debug.inspect(whole)}\"\n    None -> \"\"\nend", want: `"2Some(2)"`,
		tags: tagNested},
	{name: "or pattern", decls: mxShape, typ: "String",
		expr: "case (Shape.Rect{w: 2, h: 3})\n    .Circle(_) or .Dot -> \"round\"\n    .Rect{w: n} or .Rect{h: n} when n > 5 -> \"wide\"\n    .Rect{w: 1 or 2 as n} -> \"${n}\"\n    _ -> \"other\"\nend",
		want: `"2"`, tags: tagNested},
	{name: "or parameter pattern", typ: "Int", expr: "begin\n    f = fn (0, n) or (n, _): (Int, Int) -> n * 2\n    f((0, 4)) + f((3, 1))\nend",
		want: "14", tags: tagNested},
	{name: "distinct pattern", decls: "type Meters Int", typ: "Int", expr: "case Meters(4)\n    Meters(n) -> n\nend",
		want: "4", tags: tagNested},
	{name: "literal pattern", typ: "String", expr: "case 3\n    1 -> \"a\"\n    3 -> \"c\"\n    _ -> \"z\"\nend", want: `"c"`,
		tags: tagNested},
	{name: "binding else", typ: "Int", expr: "begin\n    Some(n) = Some(9) else 0\n    n\nend", want: "9", tags: tagNested},
	{name: "pattern if", typ: "Int", expr: "if Some(n) = Some(4) then n else 0", want: "4"},
	{name: "parameter pattern", typ: "Int", expr: "begin\n    f = fn (a, b): (Int, Int) -> a * b\n    f((3, 4))\nend", want: "12",
		tags: tagNested},

	// Calls.
	{name: "named arguments", decls: mxGreet, typ: "String", expr: `greet("a", times: 2)`, want: `"a!2"`, tags: tagCall},
	{name: "reordered named arguments", decls: mxGreet, typ: "String", expr: `greet(times: 3, name: "b")`, want: `"b!3"`,
		tags: tagCall},
	{name: "default reads a parameter", decls: "fn span(a: Int, b: Int = a * 2) -> Int\n    a + b\nend", typ: "Int",
		expr: "span(3)", want: "9", tags: tagCall},
	{name: "intrinsic named arguments", typ: "String", expr: `String.pad_start("7", fill: "0", width: 3)`,
		want: `"007"`, tags: tagCall},
	{name: "captured partial named call", decls: mxAdd3, typ: "Int", expr: "begin\n    g = &add3(&1, b: &2)\n    g(b: 2, 1)\nend",
		want: "103", tags: tagCall | tagNested},
	{name: "owner qualified call", typ: "Int", expr: "Maybe.with_default(Some(5), 0)", want: "5", tags: tagCall},
	{name: "interface qualified call", typ: "String", expr: "Display.to_string(12)", want: `"12"`, tags: tagCall},
	{name: "type parameter qualified call",
		decls: "fn shown<T>(x: T) -> String where T: Display\n    T.to_string(x)\nend", typ: "String", expr: "shown(5)",
		want: `"5"`, tags: tagCall},
	// Generic owner functions, instantiated per call from the impl block's
	// frame (irbuild's methodinst.go) rather than as a generic fn is.
	{name: "owner generic function", decls: mxOpt, typ: "Maybe<Int>", expr: "Opt.flat(Opt.Full(Some(3)))", want: "Some(3)",
		tags: tagCall},
	{name: "owner generic function callback", decls: mxOpt, typ: "Opt<String>", expr: `Opt.map(Opt.Full(2), fn n -> "${n * 3}")`,
		want: `Full("6")`, tags: tagCall},
	// The owner function's own T is open: nothing determines it, and no
	// value of it is made.
	{name: "owner generic function open type argument", decls: mxOpt, typ: "Maybe<Int>", expr: "Opt.flat(Opt.Full(None))",
		want: "None", tags: tagCall | tagUntyped | tagBareOK, reject: mxUnsolved},

	// Derived impls.
	{name: "derive Equatable", decls: "struct Cpt\n    a: Int\nend\n\nderive Equatable for Cpt", typ: "Cpt",
		expr: "Cpt{a: 1}", obs: "Debug.inspect($v == Cpt{a: 1})", want: "True"},
	{name: "derive Hashable", decls: "struct Hp\n    a: Int\nend\n\nderive Equatable for Hp\nderive Hashable for Hp", typ: "Hp",
		expr: "Hp{a: 1}", obs: "Debug.inspect(Hashable.hash($v) == Hashable.hash(Hp{a: 1}))", want: "True"},
	{name: "derive Comparable", decls: mxCoin + "\n\nderive Equatable for Coin\nderive Comparable for Coin", typ: "Coin",
		expr: "Coin.Penny", obs: "Debug.inspect($v < Coin.Dime)", want: "True"},
	{name: "derive Display", decls: "struct Shown\n    name: String\nend\n\nderive Display for Shown", typ: "Shown",
		expr: `Shown{name: "bob"}`, obs: "Display.to_string($v)", want: "Shown{name: bob}"},
	{name: "derive Debug", decls: "struct Dbg\n    s: String\nend\n\nderive Debug for Dbg", typ: "Dbg",
		expr: `Dbg{s: "q"}`, want: `Dbg{s: "q"}`},
	{name: "derive ToJson", decls: "struct Jp\n    a: Int\n    s: String\nend\n\nderive ToJson for Jp", typ: "Jp",
		expr: `Jp{a: 1, s: "x"}`, obs: "Json.encode(ToJson.to_json($v))", want: `{"a":1,"s":"x"}`},
	{name: "derive FromJson",
		decls: "struct Jq\n    a: Int\nend\n\nderive ToJson for Jq\nderive FromJson for Jq", typ: "Jq",
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
	return "begin\n" + mxIndent(strings.Join(lines, "\n")) + "\nend"
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
	// std, on a placement, names the std module the whole cell is
	// appended to, whose own tests run it (mxStdPlacements).
	std string
}

// mxTyping is what a wrapping context gives the type of the expression it
// holds.
type mxTyping int

const (
	// mxFlows: nothing; the expression's type flows out of the context as
	// it is, solved or not, to be bound or fixed further out.
	mxFlows mxTyping = iota
	// mxGives: the type it declares, as the expected type: an annotation,
	// a parameter, a pipe stage's parameter (its head is the call's first
	// argument), a result, a field, a payload.
	mxGives
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
		b.decls = append(b.decls, fmt.Sprintf("fn take%d(a: %s) -> %s\n    a\nend", n, b.typ, b.typ))
		b.expr = fmt.Sprintf("take%d(%s)", n, b.expr)
	}),
	wrapCtx("named argument", false, mxGives, func(b *mxBuild, n int) {
		b.decls = append(b.decls, fmt.Sprintf("fn named%d(k: Int, a: %s) -> %s\n    _ = k\n    a\nend", n, b.typ, b.typ))
		b.expr = fmt.Sprintf("named%d(a: %s, k: 0)", n, b.expr)
	}),
	wrapCtx("function result", false, mxGives, func(b *mxBuild, n int) {
		b.decls = append(b.decls, fmt.Sprintf("fn make%d() -> %s\n%s\nend", n, b.typ, mxIndent(b.expr)))
		b.expr = fmt.Sprintf("make%d()", n)
	}),
	wrapCtx("parameter default", false, mxGives, func(b *mxBuild, n int) {
		b.decls = append(b.decls, fmt.Sprintf("fn dflt%d(a: %s = %s) -> %s\n    a\nend", n, b.typ, b.expr, b.typ))
		b.expr = fmt.Sprintf("dflt%d()", n)
	}),
	wrapCtx("pipe stage", false, mxGives, func(b *mxBuild, n int) {
		b.decls = append(b.decls, fmt.Sprintf("fn pass%d(a: %s) -> %s\n    a\nend", n, b.typ, b.typ))
		b.expr = fmt.Sprintf("(%s |> pass%d())", mxParen(b.expr), n)
	}),
	wrapCtx("then stage", false, mxFlows, func(b *mxBuild, n int) {
		b.expr = fmt.Sprintf("(%s |> then fn t%d -> t%d)", mxParen(b.expr), n, n)
	}),
	wrapCtx("case arm", false, mxPasses, func(b *mxBuild, n int) {
		b.expr = fmt.Sprintf("case %d\n    1 -> %s\n    _ -> %s\nend", n, mxIndentTail(b.expr), mxIndentTail(b.expr))
	}),
	wrapCtx("if arm", false, mxPasses, func(b *mxBuild, n int) {
		b.expr = fmt.Sprintf("if %d > 0 then\n%s\nelse\n%s\nend", n, mxIndent(b.expr), mxIndent(b.expr))
	}),
	wrapCtx("lambda body", false, mxFlows, func(b *mxBuild, n int) {
		f := fmt.Sprintf("f%d", n)
		body := b.expr
		if strings.Contains(body, "\n") {
			body = mxBlock(body)
		}
		b.expr = mxBlock(f+" = fn -> "+body, f+"()")
	}),
	wrapCtx("nested function", false, mxGives, func(b *mxBuild, n int) {
		b.expr = mxBlock(fmt.Sprintf("fn inner%d() -> %s\n%s\nend", n, b.typ, mxIndent(b.expr)), fmt.Sprintf("inner%d()", n))
	}),
	wrapCtx("generic type argument", true, mxFlows, func(b *mxBuild, n int) {
		b.decls = append(b.decls, fmt.Sprintf("fn ident%d<A>(a: A) -> A\n    a\nend", n))
		b.expr = fmt.Sprintf("ident%d(%s)", n, b.expr)
	}),
	wrapCtx("generic function body", true, mxGives, func(b *mxBuild, n int) {
		b.decls = append(b.decls, fmt.Sprintf("fn gen%d<A>(a: A) -> (A, %s)\n    (a, %s)\nend", n, b.typ, mxIndentTail(b.expr)))
		b.expr = fmt.Sprintf("gen%d(0).second", n)
	}),
	wrapCtx("interface default", false, mxGives, func(b *mxBuild, n int) {
		b.decls = append(b.decls, fmt.Sprintf("interface Make%d\n    fn tag%d(x: self) -> Int\n\n    fn make%d(_x: self) -> %s\n%s\n    end\nend", n, n, n, b.typ, mxIndent(mxIndent(b.expr))),
			fmt.Sprintf("type Maker%d Int", n),
			fmt.Sprintf("impl Make%d for Maker%d\n    fn tag%d(_x: Maker%d) -> Int\n        0\n    end\nend", n, n, n, n))
		b.expr = fmt.Sprintf("Make%d.make%d(Maker%d(0))", n, n, n)
	}),
	wrapCtx("once", true, mxGives, func(b *mxBuild, n int) {
		b.decls = append(b.decls, fmt.Sprintf("once once%d: %s = %s", n, b.typ, b.expr))
		b.expr = fmt.Sprintf("once%d", n)
	}),
	wrapCtx("struct field", true, mxGives, func(b *mxBuild, n int) {
		b.decls = append(b.decls, fmt.Sprintf("struct Hold%d\n    v: %s\nend", n, b.typ))
		h := fmt.Sprintf("h%d", n)
		b.expr = mxBlock(fmt.Sprintf("%s = Hold%d{v: %s}", h, n, b.expr), h+".v")
	}),
	wrapCtx("field default", true, mxGives, func(b *mxBuild, n int) {
		b.decls = append(b.decls, fmt.Sprintf("struct Dflt%d\n    v: %s = %s\nend", n, b.typ, mxIndentTail(b.expr)))
		b.expr = fmt.Sprintf("Dflt%d{}.v", n)
	}),
	wrapCtx("record field", true, mxFlows, func(b *mxBuild, n int) {
		r := fmt.Sprintf("r%d", n)
		b.expr = mxBlock(fmt.Sprintf("%s = {f: %s, g: 0}", r, b.expr), r+".f")
	}),
	wrapCtx("enum payload", true, mxGives, func(b *mxBuild, n int) {
		b.decls = append(b.decls, fmt.Sprintf("enum Pay%d\n    Has %s\n    Gone\nend", n, mxPayload(b.typ)))
		b.expr = fmt.Sprintf("case Pay%d.Has(%s)\n    Pay%d.Has(p%d) -> p%d\n    Pay%d.Gone -> todo\nend",
			n, b.expr, n, n, n, n)
	}),
	wrapCtx("variant parameter pattern", false, mxGives, func(b *mxBuild, n int) {
		b.decls = append(b.decls, fmt.Sprintf("enum Only%d\n    One %s\nend", n, mxPayload(b.typ)),
			fmt.Sprintf("fn open%d(Only%d.One(p)) -> %s\n    p\nend", n, n, b.typ))
		b.expr = fmt.Sprintf("open%d(Only%d.One(%s))", n, n, b.expr)
	}),
	wrapCtx("variant binding pattern", false, mxGives, func(b *mxBuild, n int) {
		b.decls = append(b.decls, fmt.Sprintf("enum Lone%d\n    One %s\nend", n, mxPayload(b.typ)))
		b.expr = mxBlock(fmt.Sprintf("Lone%d.One(p%d) = Lone%d.One(%s)", n, n, n, b.expr), fmt.Sprintf("p%d", n))
	}),
	wrapCtx("generic struct field", true, mxFlows, func(b *mxBuild, n int) {
		b.decls = append(b.decls, fmt.Sprintf("struct Gbox%d<A>\n    v: A\nend", n))
		g := fmt.Sprintf("g%d", n)
		b.expr = mxBlock(fmt.Sprintf("%s = Gbox%d{v: %s}", g, n, b.expr), g+".v")
	}),
	wrapCtx("generic enum payload", true, mxFlows, func(b *mxBuild, n int) {
		b.decls = append(b.decls, fmt.Sprintf("enum Gopt%d<A>\n    Full A\n    Nothing\nend", n))
		b.expr = fmt.Sprintf("case Gopt%d.Full(%s)\n    Gopt%d.Full(p%d) -> p%d\n    Gopt%d.Nothing -> todo\nend",
			n, b.expr, n, n, n, n)
	}),
	wrapCtx("list element", true, mxFlows, func(b *mxBuild, n int) {
		b.expr = fmt.Sprintf("case [%s]\n    [p%d] -> p%d\n    _ -> todo\nend", b.expr, n, n)
	}),
	wrapCtx("map value", true, mxFlows, func(b *mxBuild, n int) {
		b.expr = fmt.Sprintf("case Map.get({\"k\" => %s}, \"k\")\n    Some(p%d) -> p%d\n    None -> todo\nend",
			b.expr, n, n)
	}),
	wrapCtx("tuple element", true, mxFlows, func(b *mxBuild, n int) {
		b.expr = fmt.Sprintf("(%d, %s).second", n, b.expr)
	}),
	wrapCtx("maybe payload", true, mxFlows, func(b *mxBuild, n int) {
		b.expr = fmt.Sprintf("case Some(%s)\n    Some(p%d) -> p%d\n    None -> todo\nend", b.expr, n, n)
	}),
	wrapCtx("result payload", true, mxFlows, func(b *mxBuild, n int) {
		b.expr = fmt.Sprintf("case Ok(%s)\n    Ok(p%d) -> p%d\n    Err(_) -> todo\nend", b.expr, n, n)
	}),
	wrapCtx("Iter.map callback", false, mxFlows, func(b *mxBuild, n int) {
		xs := fmt.Sprintf("xs%d", n)
		b.expr = mxBlock(fmt.Sprintf("%s = [0] |> Iter.map(fn _z%d -> %s) |> Iter.to_list()", xs, n, mxBraced(b.expr)),
			fmt.Sprintf("case %s\n    [p%d] -> p%d\n    _ -> todo\nend", xs, n, n))
	}),
	wrapCtx("Iter.loop callback", false, mxGives, func(b *mxBuild, n int) {
		m, s := fmt.Sprintf("m%d", n), fmt.Sprintf("s%d", n)
		b.expr = mxBlock(
			fmt.Sprintf("%s = Iter.loop(None, fn %s: Maybe<%s> ->\n    case %s\n        Some(_) -> break %s\n        None -> Some(%s)\n    end\nend)",
				m, s, b.typ, s, s, mxIndentTail(mxIndentTail(b.expr))),
			fmt.Sprintf("case %s\n    Some(p%d) -> p%d\n    None -> todo\nend", m, n, n))
	}),
	wrapCtx("concurrent task", false, mxFlows, func(b *mxBuild, n int) {
		t := fmt.Sprintf("task%d", n)
		b.expr = fmt.Sprintf("concurrent\n    %s = Task.spawn(fn -> %s)\n    Task.await(%s)\nend", t, mxIndentTail(mxBraced(b.expr)), t)
	}),
	wrapCtx("generic function of another file", true, mxFlows, func(b *mxBuild, n int) {
		b.lib = append(b.lib, fmt.Sprintf("fn carry%d<A>(a: A) -> A\n    a\nend", n))
		b.expr = fmt.Sprintf("carry%d(%s)", n, b.expr)
	}),
	// The value in a field of a struct whose impls are derived, so the
	// derived bodies run at the feature's type: Debug, `==` and a hash.
	wrapCtx("derived Debug and equality", true, mxGives, func(b *mxBuild, n int) {
		s, d := fmt.Sprintf("Drv%d", n), fmt.Sprintf("d%d", n)
		b.decls = append(b.decls, fmt.Sprintf("struct %s\n    v: %s\nend\n\nderive Debug for %s\nderive Equatable for %s\nderive Hashable for %s", s, b.typ, s, s, s))
		b.expr = mxBlock(fmt.Sprintf("%s = %s{v: %s}", d, s, mxIndentTail(b.expr)),
			fmt.Sprintf("if %s == %s and Hashable.hash(%s) == Hashable.hash(%s) and Debug.inspect(%s) == \"%s{v: ${Debug.inspect(%s.v)}}\" then\n    %s.v\nelse\n    todo\nend",
				d, d, d, d, d, s, d, d))
	}),
	// The same with ToJson and FromJson derived: the value round-trips
	// through the derived bodies.
	wrapCtx("derived JSON", true, mxGives, func(b *mxBuild, n int) {
		s, d := fmt.Sprintf("Jsn%d", n), fmt.Sprintf("j%d", n)
		b.decls = append(b.decls, fmt.Sprintf("struct %s\n    v: %s\nend\n\nderive Equatable for %s\nderive ToJson for %s\nderive FromJson for %s", s, b.typ, s, s, s))
		b.expr = mxBlock(fmt.Sprintf("%s = %s{v: %s}", d, s, mxIndentTail(b.expr)),
			fmt.Sprintf("if FromJson.from_json<%s>(ToJson.to_json(%s)) == Ok(%s) then\n    %s.v\nelse\n    todo\nend", s, d, d, d))
	}),
	// A generic owner function (`impl Own<A> { fn get<A>(...) }`) takes the
	// value, or makes it in its body: instantiated per call under the
	// block's frame (methodinst.go), not as a generic fn is.
	wrapCtx("owner generic function argument", true, mxFlows, func(b *mxBuild, n int) {
		b.decls = append(b.decls, fmt.Sprintf("struct Own%d<A>\n    v: A\nend\n\nimpl Own%d<A>\n    pub fn get<A>(o: Own%d<A>) -> A\n        o.v\n    end\nend", n, n, n))
		b.expr = fmt.Sprintf("Own%d.get(Own%d{v: %s})", n, n, mxIndentTail(b.expr))
	}),
	wrapCtx("owner generic function body", true, mxGives, func(b *mxBuild, n int) {
		b.decls = append(b.decls, fmt.Sprintf("struct Mk%d<A>\n    v: A\nend\n\nimpl Mk%d<A>\n    pub fn make<A>(_o: Mk%d<A>) -> %s\n%s\n    end\nend",
			n, n, n, b.typ, mxIndent(mxIndent(b.expr))))
		b.expr = fmt.Sprintf("Mk%d.make(Mk%d{v: 0})", n, n)
	}),
	// A generic function of a std module lowered with each program
	// (std/presence), instantiated at the feature's type.
	wrapCtx("program std generic function", true, mxFlows, func(b *mxBuild, n int) {
		b.expr = fmt.Sprintf("case Presence.to_maybe(Presence.Present(%s))\n    Some(p%d) -> p%d\n    None -> todo\nend", mxIndentTail(b.expr), n, n)
	}),
	// An interface default declared in another file than the impl that
	// inherits it: the impl's function forwards to the declaring file's
	// instance (ifacedefault.go).
	wrapCtx("interface default of another file", true, mxGives, func(b *mxBuild, n int) {
		b.lib = append(b.lib, fmt.Sprintf("interface Make%d\n    fn tag%d(x: self) -> Int\n\n    fn make%d(_x: self) -> %s\n%s\n    end\nend", n, n, n, b.typ, mxIndent(mxIndent(b.expr))))
		b.decls = append(b.decls, fmt.Sprintf("type Maker%d Int", n),
			fmt.Sprintf("impl Make%d for Maker%d\n    fn tag%d(_x: Maker%d) -> Int\n        0\n    end\nend", n, n, n, n))
		b.expr = fmt.Sprintf("Make%d.make%d(Maker%d(0))", n, n, n)
	}),
	// A parameter default of another file's function, left out at the
	// call: filled at the call or through the declaring file's accessor
	// (irbuild package doc, "Defaulted parameters across files").
	wrapCtx("parameter default of another file", true, mxGives, func(b *mxBuild, n int) {
		b.lib = append(b.lib, fmt.Sprintf("fn far%d(k: Int, a: %s = %s) -> %s\n    _ = k\n    a\nend", n, b.typ, mxIndentTail(b.expr), b.typ))
		b.expr = fmt.Sprintf("far%d(0)", n)
	}),
}

// mxPayload is typ as a variant's payload type: an anonymous struct type is
// parenthesized, since `Has{a: Int}` would declare a struct variant.
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
	return "fn main()\n    got = " + mxIndentTail(e) + "\n    io.print(" + mxObs(obs, "got") + ")\nend", nil,
		want + "\n\n--- ok: <nil>"
}}

const mxCase = "matrix"

var mxShells = []*mxContext{
	mxShellMain,
	{name: "test body", shell: func(b *mxBuild, e, obs, want string) (string, []string, string) {
		return "test \"" + mxCase + "\"\n    got = " + mxIndentTail(e) + "\n    assert " + mxObs(obs, "got") + " == " + mxString(want) + "\nend",
			nil, "\n--- " + mxCase + ": ok: <nil>\n"
	}},
	{name: "assert subject", shell: func(b *mxBuild, e, obs, want string) (string, []string, string) {
		return "test \"" + mxCase + "\"\n    assert " + mxIndentTail(mxObs(obs, mxParen(e))) + " == " + mxString(want) + "\nend",
			nil, "\n--- " + mxCase + ": ok: <nil>\n"
	}},
	{name: "subtests row", shell: func(b *mxBuild, e, obs, want string) (string, []string, string) {
		return "test \"" + mxCase + "\"\n    subtests [" + mxIndentTail(e) + "], got\n        assert " + mxObs(obs, "got") + " == " +
			mxString(want) + "\n    end\nend", nil, "\n--- " + mxCase + ": ok: <nil>\n"
	}},
	{name: "subtests name", shell: func(b *mxBuild, e, obs, want string) (string, []string, string) {
		// The row's name observes the value: it divides by zero, which
		// fails the row, unless the observation is the one expected.
		return "test \"" + mxCase + "\"\n    subtests [" + mxIndentTail(e) + "], got, Int.to_string(100 / (if " + mxObs(obs, "got") + " == " +
			mxString(want) + " then 1 else 0))\n        assert True\n    end\nend", nil, "\n--- " + mxCase + ": ok: <nil>\n"
	}},
	{name: "defer", shell: func(b *mxBuild, e, obs, want string) (string, []string, string) {
		// One deferred call reads the local, widening it to the parameter's
		// type; the other's operand is a call, evaluated where the `defer`
		// stands.
		show := "fn show(v: " + b.typ + ")\n    io.print(" + mxObs(obs, "v") + ")\nend"
		return "fn main()\n    got = " + mxIndentTail(e) + "\n    defer show(got)\n    defer io.print(" + mxObs(obs, "got") +
				")\n    io.print(\"body\")\nend", []string{show},
			"body\n" + want + "\n" + want + "\n\n--- ok: <nil>"
	}},
	{name: "with", shell: func(b *mxBuild, e, obs, want string) (string, []string, string) {
		decls := []string{"struct App\n    v: " + b.typ + "\nend", "fn boot() -> Result<App, String>\n    Ok(App{v: " + mxIndentTail(mxIndentTail(e)) + "})\nend"}
		return "fn main()\n    with App.v = " + mxIndentTail(e) + "\n    got = App.v\n    io.print(" + mxObs(obs, "got") + ")\nend",
			decls, want + "\n\n--- ok: <nil>"
	}},
	mxShellPrompt,
}

// mxShellTest is the test body shell, which a std placement uses for main.
var mxShellTest = mxShells[1]

// mxShellPrompt observes the value in a std module's `//!` prompt, which
// `nomi test std/<module>.nomi` runs as a case built in the module's scope.
// Only a std placement has one.
var mxShellPrompt = &mxContext{name: "//! prompt", shell: func(b *mxBuild, e, obs, want string) (string, []string, string) {
	lines := strings.Split("got = "+e+"\nassert "+mxObs(obs, "got")+" == "+mxString(want), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight("//! "+l, " ")
	}
	return "/// The matrix's probe.\n" + strings.Join(lines, "\n") + "\n//\npub fn matrix_probe() end", nil, ""
}}

// mxStdPlacements append the whole cell, its declarations and its test, to
// a std module's own source, and run the test as `nomi test
// std/<module>.nomi` runs an edited module (LoadStdlib): std/presence is
// lowered with each program (irbuild's programstd.go), std/hex once for
// the process (stdedited.go). A std file has no prelude, so the cell's
// std names are imported (mxStdFileImports).
var mxStdPlacements = []*mxContext{
	{name: "std module lowered with each program", fragile: true, std: "presence", place: func(b *mxBuild, featureDecls []string) {}},
	{name: "std module lowered once", fragile: true, std: "hex", place: func(b *mxBuild, featureDecls []string) {}},
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
		b.lib = append(b.lib, "fn produce() -> "+b.typ+"\n"+mxIndent(b.expr)+"\nend")
		b.decls = nil
		b.expr = "produce()"
	}},
	mxPlaceImpls,
	mxStdPlacements[0],
	mxStdPlacements[1],
}

// mxPlaceImpls keeps the feature's impl blocks in the entry file
// and moves its types and interfaces to another, so each impl is in a file
// apart from what it implements and for what.
var mxPlaceImpls = &mxContext{name: "impls apart from their types", fragile: true, place: func(b *mxBuild, featureDecls []string) {
	var impls []string
	for _, d := range featureDecls {
		for _, part := range strings.Split(d, "\n\n") {
			if mxImplDecl(part) {
				impls = append(impls, part)
			} else {
				b.lib = append(b.lib, part)
			}
		}
	}
	b.decls = append(impls, b.decls[len(featureDecls):]...)
}}

// mxHasImpl reports whether a feature's declarations hold an impl block.
func mxHasImpl(decls string) bool {
	if decls == "" {
		return false
	}
	for _, part := range strings.Split(decls, "\n\n") {
		if mxImplDecl(part) {
			return true
		}
	}
	return false
}

// mxImplDecl reports whether a top-level declaration is an impl block of an
// interface. A derive and an inherent impl (`impl Opt<T>`) stay
// with their type, which the checker requires.
func mxImplDecl(decl string) bool {
	head, _, _ := strings.Cut(decl, "\n")
	return strings.HasPrefix(decl, "impl ") && strings.Contains(head, " for ")
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
	{"io", "std::io", ""},
	{"Task", "std::tasks", "Task"},
	{"Json", "std::json", "Json"},
	{"ToJson", "std::json", "ToJson"},
	{"FromJson", "std::json", "FromJson"},
	{"Presence", "std::presence", "Presence"},
}

var (
	mxIdent    = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
	mxDeclName = regexp.MustCompile(`(?m)^(?:struct|enum|type|interface|fn|once|typealias|phrase) ([A-Za-z_][A-Za-z0-9_]*)`)
	mxPubDecl  = regexp.MustCompile(`(?m)^(struct|enum|type|interface|fn|once|typealias|phrase) `)
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
	if c.place.std != "" {
		src, _ = c.stdSource()
		return src, ""
	}
	b := c.build()
	body, shellDecls, transcript := c.shell.shell(b, b.expr, c.f.obs, c.f.want)
	mainDecls := strings.Join(append(append([]string{}, b.decls...), shellDecls...), "\n\n")
	if len(b.lib) == 0 {
		main := strings.TrimLeft(mainDecls+"\n\n"+body, "\n") + "\n"
		return mxImports(main, "", nil) + main, transcript
	}
	lib := strings.Join(b.lib, "\n\n") + "\n"
	lib = mxPubDecl.ReplaceAllString(lib, "pub $1 ")
	var libNames []string
	for _, m := range mxDeclName.FindAllStringSubmatch(strings.ReplaceAll(lib, "pub ", ""), -1) {
		libNames = append(libNames, m[1])
	}
	// lib.nomi may name the entry's declarations (a wrap's default body
	// over the feature's types): it imports them back, as imports may be
	// cyclic.
	var fromMain []string
	libUses := mxUses(lib)
	for _, m := range mxDeclName.FindAllStringSubmatch(mainDecls, -1) {
		// A name lib.nomi declares a function of itself (an interface's
		// `fn show`) is its own.
		if libUses[m[1]] && !strings.Contains(lib, "fn "+m[1]+"(") {
			fromMain = append(fromMain, m[1])
		}
	}
	if len(fromMain) > 0 {
		mainDecls = mxPubDecl.ReplaceAllString(mainDecls, "pub $1 ")
	}
	main := strings.TrimLeft(mainDecls+"\n\n"+body, "\n") + "\n"
	return "// FILE: main.nomi\n" + mxImports(main, "lib", libNames) + main +
		"// FILE: lib.nomi\n" + mxImports(lib, "main", fromMain) + lib, transcript
}

// build wraps the feature in c's contexts and places its declarations.
func (c mxCell) build() *mxBuild {
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
	return b
}

// stdSource is the std module c.place.std's source with the cell appended,
// its imports after the module's own, and the line the appended text
// starts at. The main shell is a test body there.
func (c mxCell) stdSource() (src string, from int) {
	b := c.build()
	shell := c.shell
	if shell == mxShellMain {
		shell = mxShellTest
	}
	body, shellDecls, _ := shell.shell(b, b.expr, c.f.obs, c.f.want)
	cell := strings.Join(append(append(append([]string{}, b.decls...), shellDecls...), body), "\n\n") + "\n"
	return stdEdited(c.place.std, cell)
}

// stdEdited is std module `module`'s source with cell appended and the
// imports cell needs after the module's own, and the line cell starts at.
func stdEdited(module, cell string) (src string, from int) {
	host, ok := std.ReadFile(module)
	if !ok {
		panic("no std/" + module)
	}
	text := string(host)
	// The module's import block ends at the first `end` alone on a line.
	end := strings.Index("\n"+text, "\nimport\n")
	if end < 0 {
		panic("std/" + module + " has no import block")
	}
	end += strings.Index(text[end:], "\nend\n") + 5
	imports := mxStdFileImports(cell, text[:end], text)
	head := text[:end] + imports + text[end:]
	head = strings.TrimRight(head, "\n") + "\n\n"
	return head + cell, strings.Count(head, "\n") + 1
}

// mxStdFileImport is where a std file imports a name the prelude gives a
// program: `group.item`, grouped as `group.{item, ...}`.
type mxStdFileImport struct{ group, item string }

// mxStdFileNames is each name a cell may use, with its import in a std
// file: the prelude's re-exports (std/prelude.nomi), and the names
// mxStdImports imports in a program.
var mxStdFileNames = sync.OnceValue(func() map[string]mxStdFileImport {
	names := map[string]mxStdFileImport{
		"io":       {"io", "self"},
		"Task":     {"tasks", "Task"},
		"Json":     {"json", "Json"},
		"ToJson":   {"json", "ToJson"},
		"FromJson": {"json", "FromJson"},
		"Presence": {"presence", "Presence"},
	}
	prelude, _ := std.ReadFile("prelude")
	in := false
	for _, line := range strings.Split(string(prelude), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "pub import":
			in = true
			continue
		case !in || line == "" || strings.HasPrefix(line, "//"):
			continue
		case line == "end":
			in = false
			continue
		}
		for _, imp := range mxStdImportItems(line) {
			names[imp.name] = imp.mxStdFileImport
		}
	}
	return names
})

type mxStdImportItem struct {
	name string
	mxStdFileImport
}

// mxStdImportItems reads one line of an import block (`maybe.Maybe`,
// `bool.Bool.{self, False, True}`) as the names it binds.
func mxStdImportItems(line string) []mxStdImportItem {
	split := func(path string) (string, string) {
		i := strings.LastIndex(path, ".")
		if i < 0 {
			return "", path
		}
		return path[:i], path[i+1:]
	}
	prefix, items, braced := strings.Cut(line, ".{")
	if !braced {
		g, n := split(line)
		return []mxStdImportItem{{n, mxStdFileImport{g, n}}}
	}
	var out []mxStdImportItem
	for _, item := range strings.Split(strings.TrimSuffix(items, "}"), ",") {
		item = strings.TrimSpace(item)
		if item == "self" {
			g, n := split(prefix)
			if g == "" {
				out = append(out, mxStdImportItem{n, mxStdFileImport{n, "self"}})
			} else {
				out = append(out, mxStdImportItem{n, mxStdFileImport{g, n}})
			}
			continue
		}
		out = append(out, mxStdImportItem{item, mxStdFileImport{prefix, item}})
	}
	return out
}

var (
	mxHostDecl      = regexp.MustCompile(`(?m)^(?:pub )?(?:opaque )?(?:struct|enum|type|interface|fn|once) ([A-Za-z_][A-Za-z0-9_]*)`)
	mxInterpolation = regexp.MustCompile(`\$\{([^}]*)\}`)
	mxInOperator    = regexp.MustCompile(`[\w)\]] in [\w(\[%#"]`)
)

// mxStdFileImports is the import block a std module whose source up to the
// end of its imports is hostImports, and whose whole source is host, needs
// for cell: each std name cell uses that the module neither imports nor
// declares.
func mxStdFileImports(cell, hostImports, host string) string {
	have := map[string]bool{}
	block := hostImports[strings.LastIndex(hostImports, "import\n"):]
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "import" || line == "end" || strings.HasPrefix(line, "//") {
			continue
		}
		for _, imp := range mxStdImportItems(line) {
			have[imp.name] = true
		}
	}
	declared := map[string]bool{}
	for _, m := range mxDeclName.FindAllStringSubmatch(cell, -1) {
		declared[m[1]] = true
	}
	for _, m := range mxHostDecl.FindAllStringSubmatch(host, -1) {
		declared[m[1]] = true
	}
	// A name an interpolation uses is used, and so is the interface an
	// operator needs in scope: `in` needs Contains.
	uses := mxUses(cell)
	for _, lit := range mxStrLit.FindAllString(cell, -1) {
		for _, m := range mxInterpolation.FindAllStringSubmatch(lit, -1) {
			for n := range mxUses(m[1]) {
				uses[n] = true
			}
		}
	}
	if mxInOperator.MatchString(mxStrLit.ReplaceAllString(cell, `""`)) {
		uses["Contains"] = true
	}
	groups := map[string][]string{}
	for name := range uses {
		imp, ok := mxStdFileNames()[name]
		if !ok || have[name] || declared[name] {
			continue
		}
		groups[imp.group] = append(groups[imp.group], imp.item)
	}
	if len(groups) == 0 {
		return ""
	}
	var lines []string
	for g, items := range groups {
		sort.Strings(items)
		if len(items) == 1 && items[0] != "self" {
			lines = append(lines, "    "+g+"."+items[0])
		} else {
			lines = append(lines, "    "+g+".{"+strings.Join(items, ", ")+"}")
		}
	}
	sort.Strings(lines)
	return "\nimport\n" + strings.Join(lines, "\n") + "\nend\n"
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
	if c.place == mxPlaceImpls && !mxHasImpl(c.f.decls) {
		return "", "", "the " + c.f.name + " declares no impl to place apart"
	}
	if skip := mxStdSkip(c); skip != "" {
		return "", "", skip
	}
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
		case w.typing == mxGives:
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
		// its parameter, §6 *Bindings*) and sometimes not, which the matrix does not
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
	case c.f.tags.has(tagPartial) && c.shell.name != "subtests row" && c.shell.name != "subtests name":
		return "", "", ""
	case c.f.tags.has(tagBareOK) && c.shell.name == "assert subject":
		// Debug.inspect's argument: a part of a type that no value has may
		// stay unsolved, and the observation's call solves a generic
		// function value.
		return "", "", ""
	}
	return c.f.reject, "the " + c.shell.name + " shell gives the " + c.f.name + " no type", ""
}

// mxStdSkip is why the matrix does not generate c, for a reason of a std
// placement or of the prompt shell: "" when it does.
func mxStdSkip(c mxCell) string {
	if c.place.std == "" {
		if c.shell == mxShellPrompt {
			return "a `//!` prompt is a std module's"
		}
		return ""
	}
	if c.shell.name == "defer" || c.shell.name == "with" {
		return "a std module's tests have no `fn main`"
	}
	if c.shell == mxShellTest {
		return "the main shell is a test body in a std module"
	}
	b := c.build()
	if len(b.lib) > 0 {
		return "a std module is one file"
	}
	if c.place.std == "presence" {
		return ""
	}
	// A module lowered once for the process names only types with a
	// process-wide representation (irbuild's programstd.go), and imports no
	// module lowered with each program.
	for _, w := range c.wraps {
		if w.name == "program std generic function" {
			return "a std module lowered once imports no module lowered with each program"
		}
	}
	if mxDeclaresType.MatchString(strings.Join(b.decls, "\n\n")) {
		return "a std module lowered once declares no type: it would need an rt type and row"
	}
	return ""
}

var mxDeclaresType = regexp.MustCompile(`(?m)^(struct|enum|type|interface|phrase|derive) `)

// mxLaterGives reports whether a context of c from the i-th wrap out gives
// a type: a wrap, the placement or the shell.
func mxLaterGives(c mxCell, i int) bool {
	for _, w := range c.wraps[i:] {
		if w.typing == mxGives {
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
		if r.before && (w.typing == mxGives) {
			return false
		}
	}
	return c.shell.name == r.context || c.place.name == r.context
}

var mxCheckerRules = []mxCheckerRule{
	{match: mxBindsWhollyOpen, want: mxUnsolved,
		why: "`Iter.sum([])`'s whole type is the T nothing has fixed yet, and the context binds a generic " +
			"type whose type argument is that T before anything fixes it"},
	{match: func(c mxCell) bool {
		return c.f.name == "iter-sensitive call" || c.f.name == "iter-sensitive lambda body"
	},
		want: mxIterSensitiveUse,
		why:  "an iter-sensitive function may only be passed as an iteration callback itself, and this callback calls it"},
	{match: mxByteLiteralFixedAsInt, want: "Byte.*, (got|but the pipe passes) .*Int|is Int, expected Byte",
		why: "an Int literal is a Byte only where a Byte is expected of it; a context that types it first makes it an Int, " +
			"and an Int never becomes a Byte where one is later required"},
	{match: func(c mxCell) bool {
		return (mxHasWrap(c, "derived Debug and equality") || mxHasWrap(c, "derived JSON")) && c.f.tags.has(tagFn|tagIter)
	},
		want: "no impl of `Equatable`|does not require Equatable",
		why:  "a derived Equatable needs every field Equatable, and a function or an Iter is not"},
	{match: func(c mxCell) bool {
		return mxHasWrap(c, "derived JSON") && !mxJsonable(c.f.typ) && !strings.Contains(c.f.decls, "derive FromJson for "+c.f.typ)
	},
		want: "no impl of `(ToJson|FromJson|Equatable)`|does not require (ToJson|FromJson|Equatable)|has no member '(from|to)_json'",
		why:  "a derived ToJson and FromJson need every field to implement them"},
}

// mxHasWrap reports whether c has the wrapping context named name.
func mxHasWrap(c mxCell, name string) bool {
	for _, w := range c.wraps {
		if w.name == name {
			return true
		}
	}
	return false
}

// mxJsonable reports whether the checker admits a field of type typ in a
// struct deriving ToJson and FromJson: std's scalars and containers of
// them, a Map keyed by String, and an anonymous struct of them. A tuple
// implements neither (spec §12 *Tuples, anonymous structs and functions*),
// and std's impls are for Map<String, V> alone.
func mxJsonable(typ string) bool {
	if strings.Contains(typ, "->") || strings.Contains(typ, "(") {
		return false
	}
	t := regexp.MustCompile(`\w+:`).ReplaceAllString(typ, "")
	for _, key := range regexp.MustCompile(`Map<(\w+)`).FindAllStringSubmatch(t, -1) {
		if key[1] != "String" {
			return false
		}
	}
	t = strings.ReplaceAll(t, "Map<String, ", "Map<")
	for _, id := range mxIdent.FindAllString(t, -1) {
		switch id {
		case "Int", "Float", "Decimal", "String", "Bool", "List", "Vector", "Set", "Map", "Maybe":
		default:
			return false
		}
	}
	return true
}

// mxByteLiteralFixedAsInt matches the byte literal inside a context that
// types it as an Int before any context expects a Byte of it (a binding, a
// case subject, a lambda), when a context further out requires a Byte.
// Walking out from the literal: a context that gives Byte makes it one, a
// pipe stage's parameter included; a block tail, an arm, and a generic
// call whose result is expected pass the expected type in; anything else
// fixes Int. The
// `defer` shell binds the value and then passes it to a Byte parameter.
func mxByteLiteralFixedAsInt(c mxCell) bool {
	if c.f.name != "byte literal" {
		return false
	}
	deferShell := c.shell.name == "defer" && c.place.name != "value from another file"
	for i, w := range c.wraps {
		switch {
		case w.typing == mxGives:
			return false
		case w.typing == mxPasses, w.name == "generic type argument", w.name == "generic function of another file":
			continue
		}
		return mxLaterGives(c, i+1) || deferShell
	}
	return deferShell
}

// mxIterSensitiveUse matches the rejection of a call to an iter-sensitive
// function.
const mxIterSensitiveUse = "uses `continue`, so it cannot be called: pass it as an iteration callback itself"

// mxBindsWhollyOpen matches the sum of an empty list inside a context
// whose own unannotated binding has a generic type whose type argument is
// the unsolved T (`Gbox<T>`, `List<T>`, `Task<T>`), before a context
// outside it gives T. An empty list in the same place binds `List<List<?>>`,
// whose open part is nested, which a binding accepts.
func mxBindsWhollyOpen(c mxCell) bool {
	if c.f.name != "sum of an empty list" {
		return false
	}
	for _, w := range c.wraps {
		switch w.name {
		case "Iter.map callback", "concurrent task", "generic struct field":
			return true
		}
		if w.typing == mxGives {
			return false
		}
	}
	return false
}

// mxOutcome is what checking and running one cell found.
type mxOutcome struct {
	// class: "rejected", "ok", or a failure: "unexpected rejection",
	// "accepted, expected a rejection", "declines", "declines without a
	// cause", "panics", "untyped", "crash", "wrong output".
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
	if c.place.std != "" {
		return checkStdCell(c)
	}
	src, transcript := c.source()
	var p *Program
	lo := checkLowersKeeping(dir, src, &p)
	wantReject, why, _ := mxExpectedRejection(c)
	if o, done := mxCheckOutcome(lo, wantReject, why, c.place.std); done {
		return o
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

// checkStdCell checks a cell of a std placement: the edited module is
// checked and lowered as `nomi test std/<module>.nomi` does it (LoadStdlib),
// and its matrix case runs as that command runs it.
func checkStdCell(c mxCell) (out mxOutcome) {
	src, from := c.stdSource()
	wantReject, why, _ := mxExpectedRejection(c)
	lo, k := checkStdLowers(c.place.std, src, from)
	if o, done := mxCheckOutcome(lo, wantReject, why, c.place.std); done {
		return o
	}
	switch {
	case k.Name == "":
		return mxOutcome{class: "crash", detail: k.Output}
	case k.Err != nil:
		if cp, internal := vm.AsCompilePanic(k.Err); internal {
			return mxOutcome{class: "crash", detail: fmt.Sprintf("%s: %s\n%s", crashCompile, cp.What(), cp.Stack)}
		}
		return mxOutcome{class: "wrong output", detail: fmt.Sprintf("the case fails: %v\n%s", k.Err, k.Output)}
	}
	return mxOutcome{class: "ok"}
}

// checkStdLowers checks src, the source of std module `module` edited from
// line from on, as `nomi test std/<module>.nomi` checks an edited module
// (LoadStdlib), and runs its cases whose name starts with "matrix", which
// must be one. A case the builder blocked is the outcome's decline; a case
// that ran is answered, or a CaseResult with no name and the reason in its
// Output when none did.
func checkStdLowers(module, src string, from int) (lo lowerOutcome, k CaseResult) {
	var p *Program
	func() {
		defer func() {
			if r := recover(); r != nil {
				lo = lowerOutcome{accepted: true, internal: fmt.Sprintf("%v\n%s", r, rdebug.Stack())}
				p = nil
			}
		}()
		nodes, fa, err := frontend.New(frontend.Config{}).CheckStdlibSource(module, src, frontend.Mode{Tests: true})
		if err != nil {
			lo.rejection = err.Error()
			return
		}
		lo.accepted = true
		for _, u := range analysis.UnresolvedExprs(fa, nodes) {
			if u.Line >= from {
				lo.unresolved = append(lo.unresolved, fmt.Sprintf("std/%s.nomi:%s", module, u))
			}
		}
		lp, err := loadStdlib(module, nodes, fa, irbuild.GenerateEditedStdlibTestIR)
		if err != nil {
			var ie *InternalError
			if errors.As(err, &ie) {
				lo.internal = fmt.Sprintf("%v\n%s", ie.Panic, ie.Stack)
			} else {
				lo.internal = "the lowering fails: " + err.Error()
			}
			return
		}
		p = lp
	}()
	if p == nil {
		return lo, k
	}
	var cases []CaseResult
	func() {
		defer func() {
			if r := recover(); r != nil {
				cases = []CaseResult{{Output: fmt.Sprintf("%s: %v\n%s", crashPanic, r, rdebug.Stack())}}
			}
		}()
		cases = p.Cases(io.Discard, TestOptions{Run: regexp.MustCompile(`^matrix`)})
	}()
	if len(cases) != 1 {
		return lo, CaseResult{Output: fmt.Sprintf("want the matrix's one case, got %d: %+v", len(cases), cases)}
	}
	k = cases[0]
	lo.declines, lo.reasons = k.Blocked, k.Reasons
	if len(lo.declines) > 0 && len(lo.reasons) == 0 {
		lo.unplaced = lo.declines
	}
	return lo, k
}

// mxCheckOutcome sorts what checking a cell's program found, and reports
// done unless the program is accepted, lowers whole and must run. module is
// the std module a std placement's cell is appended to, whose own gaps then
// excuse a decline, or "".
func mxCheckOutcome(lo lowerOutcome, wantReject, why, module string) (mxOutcome, bool) {
	switch {
	case !lo.accepted && wantReject != "" && regexp.MustCompile(wantReject).MatchString(lo.rejection):
		return mxOutcome{class: "rejected"}, true
	case !lo.accepted && wantReject != "":
		return mxOutcome{class: "unexpected rejection", detail: fmt.Sprintf("the matrix expects a rejection holding %q (%s), and the checker says:\n%s", wantReject, why, lo.rejection)}, true
	case !lo.accepted && why != "":
		return mxOutcome{class: "unexpected rejection", detail: "the matrix pins a checker hole here (" + why + "); if the checker now rejects it, remove the rule:\n" + lo.rejection}, true
	case !lo.accepted:
		return mxOutcome{class: "unexpected rejection", detail: lo.rejection}, true
	case lo.internal != "":
		o := mxOutcome{class: "panics", detail: lo.internal}
		if gap := knownPanicFor(lo.internal); gap != nil {
			o.gap = gap.name
		}
		return o, true
	case len(lo.unplaced) > 0:
		// A decline that lost its cause is a builder bug wherever it is,
		// and no gap excuses it, by its reason or by the cell's shape.
		return mxOutcome{class: "declines without a cause", detail: strings.Join(lo.unplaced, "\n")}, true
	case len(lo.declines) > 0:
		o := mxOutcome{class: "declines", detail: strings.Join(lo.declines, "\n")}
		if gap := knownGapIn(module, lo.reasons); gap != nil {
			o.gap = gap.name
		}
		return o, true
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
		return o, true
	case wantReject != "":
		return mxOutcome{class: "accepted, expected a rejection", detail: fmt.Sprintf("the matrix expects a rejection holding %q (%s)", wantReject, why)}, true
	}
	return mxOutcome{}, false
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
		for _, p := range mxStdPlacements {
			for _, s := range mxShells[1:] {
				cells = append(cells, mxCell{f: f, shell: s, place: p})
			}
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
const mxTripleSample = 120

// mxStdSample is how much harder plain `go test` samples the cells of a std
// placement, each of which checks and lowers a whole std module: every
// mxStdSample-th of the pairs, and of the triples it would otherwise check.
const mxStdSample = 20

// mxSample answers every every-th of cells, and every (every ×
// mxStdSample)-th of those of a std placement, each counted from a fixed
// start, or all of them when full.
func mxSample(cells []mxCell, every int, full bool) []mxCell {
	if full {
		return cells
	}
	var out []mxCell
	plain, std := 0, 0
	for _, c := range cells {
		if c.place.std != "" {
			if std%(every*mxStdSample) == 0 {
				out = append(out, c)
			}
			std++
			continue
		}
		if plain%every == 0 {
			out = append(out, c)
		}
		plain++
	}
	return out
}

// checkCells checks cells, reporting each failure with its source, and
// logs the tally.
func checkCells(t *testing.T, cells []mxCell, failure func(i int) string) {
	t.Helper()
	only := os.Getenv("NOMI_MATRIX_ONLY")
	tally := map[string]int{}
	start := time.Now()
	var picked []int
	for i, c := range cells {
		if only == "" || strings.Contains(c.name(), only) {
			picked = append(picked, i)
		}
	}
	checked := len(picked)
	// Cells are independent programs, checked on every CPU at once and
	// reported in order. A cell's lowering checks std bodies (StdLib.Bodies)
	// in the shared analyses that another cell's check reads, a std
	// placement's edited module most of all, so every body is checked
	// before any cell starts, as the language server does.
	if err := std.Shared().AllBodies(); err != nil {
		t.Fatalf("the stdlib's bodies do not check: %v", err)
	}
	outcomes := make([]mxOutcome, len(picked))
	dirs := make([]string, len(picked))
	for j := range picked {
		dirs[j] = t.TempDir()
	}
	next := make(chan int)
	var wg sync.WaitGroup
	for range runtime.GOMAXPROCS(0) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range next {
				outcomes[j] = checkCell(dirs[j], cells[picked[j]])
			}
		}()
	}
	for j := range picked {
		next <- j
	}
	close(next)
	wg.Wait()
	for j, i := range picked {
		c := cells[i]
		name := c.name()
		o := outcomes[j]
		key := o.class
		if o.gap != "" {
			key += " (known: " + o.gap + ")"
		}
		tally[key]++
		if !o.failed() {
			continue
		}
		pre := ""
		if failure != nil {
			pre = failure(i)
		}
		t.Errorf("%s: %s%s:\n%s\n%s", name, pre, o.class, mxShown(c), o.detail)
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

// mxShown is c's program as a failure shows it, formatted and numbered:
// for a std placement, the module's lines from the cell's imports on.
func mxShown(c mxCell) string {
	if c.place.std == "" {
		src, _ := c.source()
		if formatted, err := mxFormat(src); err == nil {
			src = formatted
		}
		return numbered(src)
	}
	src, from := c.stdSource()
	host, _ := std.ReadFile(c.place.std)
	lines := strings.Split(src, "\n")
	var b strings.Builder
	fmt.Fprintf(&b, "std/%s.nomi with the cell's imports after its own and the cell appended at line %d:\n", c.place.std, from)
	for i := range lines {
		// The cell's import block, then the cell.
		if i >= from-1 || (lines[i] != "" && !strings.Contains(string(host), lines[i])) {
			fmt.Fprintf(&b, "%4d | %s\n", i+1, lines[i])
		}
	}
	return b.String()
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

// TestFeatureContextMatrix (about 30 seconds) checks every pair and every
// mxTripleSample-th triple, from a fixed start, and of the cells of a std
// placement every mxStdSample-th of those (mxSample). Each cell's program
// must be rejected with the text the matrix expects, or lower and print the
// feature's value. NOMI_MATRIX=full checks every cell, and
// NOMI_MATRIX_ONLY=<text> the cells whose name holds the text. It also
// fails for an mxKnown entry whose gap no knownLoweringGaps or
// knownCheckerGaps entry pins. Findings are pinned in
// lowering_gaps_matrix_test.go.
func TestFeatureContextMatrix(t *testing.T) {
	t.Setenv("NOMI_DEBUG_LOWERING", "1")
	for _, k := range mxKnown {
		if !mxPinned(k.gap) {
			t.Errorf("mxKnown names %s, which no knownLoweringGaps or knownCheckerGaps entry pins", k.gap)
		}
	}
	pairs, triples := mxPairs(), mxTriples()
	full := os.Getenv("NOMI_MATRIX") == "full"
	t.Logf("matrix: %d features, %d wrapping contexts, %d more shells, %d more placements; %d pairs, %d triples, checking %s",
		len(mxFeatures), len(mxWraps), len(mxShells)-1, len(mxPlacements)-1, len(pairs), len(triples),
		map[bool]string{true: "all", false: fmt.Sprintf("every pair and every %dth triple, and every %dth of those in a std module",
			mxTripleSample, mxStdSample)}[full])
	t.Log("pairs:")
	checkCells(t, mxSample(pairs, 1, full), nil)
	t.Log("triples:")
	checkCells(t, mxSample(triples, mxTripleSample, full), nil)
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
