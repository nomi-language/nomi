package vmhost

import "regexp"

// matrixLoweringGaps are the gaps the feature × context matrix found
// (feature_matrix_test.go): programs the front end accepts and the builder
// declines or panics on, each pinned by a minimal reproducer. They join
// knownLoweringGaps, so TestKnownLoweringGaps holds each reproducer to its
// decline. A gap whose reason the builder derives from the node alone
// (noSkip) excuses no matrix cell by that reason; mxKnown names the cells
// it fails by their shape.
var matrixLoweringGaps = []loweringGap{
	{
		name: "gapDeferredCallOperand",
		fix: "builder: a `defer`'d call whose operand is anything but a constant or a local read: " +
			"another call (`defer io.print(Debug.inspect(x))`), a `case` or `if`, or a local read " +
			"that widens to the parameter's type (an embedded struct passed as its enum). The spec " +
			"evaluates the operands where the `defer` stands, so the builder can bind each to a " +
			"temporary there and defer the call over the temporaries",
		reason:   "a deferred call whose operands are not constants or local reads",
		alsoSkip: []string{"a deferred call that does not lower to one call"},
		src: `import std/io

fn main() {
    x = 1
    defer io.print(Debug.inspect(x))
    io.print("body")
}
`,
	},
	{
		name: "gapNestedFnWidenedResult",
		fix: "builder: a nested `fn` whose body's result is not exactly its declared return type: an " +
			"embedded struct or distinct type returned as its enum (`fn inner(): Shape { Spot{n: 3} }`), " +
			"or an empty literal whose element type only the return type fixes (`#[]`, `#{}`, " +
			"`Map.empty()` returned as a Vector<Int>, Set<Int>, Map<String, Int>). A top-level `fn` " +
			"with the same body lowers; the nested one compares the body's kind with the declared " +
			"one instead of widening it as a return does",
		reason: "a nested fn: a result that is not the declared return type",
		src: `import std/io

struct Spot {
    n: Int
}

enum Shape {
    embeds Spot
    Dot
}

fn main() {
    fn inner(): Shape {
        Spot{n: 3}
    }

    io.inspect(inner())
}
`,
	},
	{
		name: "gapCaseOverListOfIters",
		fix: "builder: a `case` whose scrutinee is a list of values a list pattern cannot compare, " +
			"an Iter, a Range or a Map whose type a later use solves (`case [it] { [p] -> ... }` " +
			"with `it` an Iter<Int>). The arms bind the elements and compare nothing, so the " +
			"scrutinee's element kind need not be one of the literal-pattern kinds",
		reason: "a tail `case` scrutinee outside retained literal-pattern kinds",
		src: `import std/io

fn main() {
    it = [1, 2] |> Iter.map(|n| n * 2)
    got = case [it] {
        [p] -> p
        _ -> it
    }
    io.inspect(Iter.to_list(got))
}
`,
	},
	{
		name: "gapImportedFnPartial",
		fix: "builder: a partial application of a function another file declares, " +
			"`add3(_, 10)` after `import lib.add3`. The same partial of a function of the entry " +
			"file lowers; the builder does not see the imported callee's parameters and defaults",
		reason: "a partial application over a callee whose parameters the builder cannot see",
		src: `// FILE: main.nomi
import std/io
import lib.add3

fn main() {
    f = add3(_, 10)
    io.inspect(f(2))
}
// FILE: lib.nomi
pub fn add3(a: Int, b: Int, c: Int = 100): Int {
    a + b + c
}
`,
	},
	{
		name: "gapUnitAppField",
		fix: "builder: an application field of type Unit. Boot builds the app and `with App.v = Unit` " +
			"writes the field, but the builder retains no slot for a Unit field, so the write declines",
		reason: "an app-field write of an unretained field",
		src: `import std/io

struct App {
    v: Unit
}

fn boot(): App {
    App{v: Unit}
}

fn main() {
    with App.v = Unit
    io.inspect(App.v)
}
`,
	},
	{
		name: "gapTodoArmBesideUnit",
		fix: "builder: a `case` whose arms are a Unit value and a `todo`, used as an argument. " +
			"`todo` is Infallible and fits any arm type, as it does beside an Int arm, but the " +
			"builder joins the arms' kinds and finds Infallible and Unit disagree",
		reason: "two `case` arms disagree: Infallible vs",
		src: `import std/io

fn main() {
    io.inspect(case [Unit] {
        [p] -> p
        _ -> todo
    })
}
`,
	},
	{
		name: "gapNestedFnExistentialLint",
		fix: "builder: a nested `fn` that returns an interface value from a `case` arm binding an " +
			"implementor taken from a list, `fn inner(): Named { case [Pt{x: 5}] { [p] -> p ... } }`. " +
			"The lowered graph copies the Pt into a temporary typed as the existential, which " +
			"ir.Lint rejects; the arm needs the boxing a top-level function's return gets",
		reason: "a lambda graph that does not lint",
		noSkip: true,
		src: `import std/io

interface Named {
    fn name(value: self): String
}

struct Pt {
    x: Int
}

impl Named for Pt {
    fn name(p: Pt): String {
        "pt${p.x}"
    }
}

fn main() {
    fn inner(): Named {
        case [Pt{x: 5}] {
            [p] -> p
            _ -> todo
        }
    }

    io.print(Named.name(inner()))
}
`,
	},
	{
		name: "gapRangeStructField",
		fix: "builder: a struct literal with a field of type Range<Int> (`Hold{v: 1..4}`), and the " +
			"same Range as a generic struct's field or a field default. A Range in a binding, a " +
			"tuple or a list lowers",
		reason: "a struct literal: Hold",
		noSkip: true,
		src: `import std/io

struct Hold {
    v: Range<Int>
}

fn main() {
    h = Hold{v: 1..4}
    io.inspect(h.v)
}
`,
	},
	{
		name: "gapEmptyLiteralSolvedLater",
		fix: "builder: an empty literal whose element type a later position solves, inside a list, " +
			"a map or a struct literal: `[#{}]` as a case scrutinee in a function returning a " +
			"Set<Int>, `{\"k\" => #{}}`, `Hold{v: {f: #{}}.f}`. The checker records the solved " +
			"type on the outer literal, and the builder reads the inner `#{}` as an unsolved Set<_>",
		reason: "a ListLit",
		noSkip: true,
		src: `fn make(): Set<Int> {
    case [#{}] {
        [p] -> p
        _ -> todo
    }
}

fn main() {
    _ = make()
}
`,
	},
	{
		name: "gapUnitVariantParameterPattern",
		fix: "builder: a parameter that takes apart a single-variant enum whose payload is Unit, " +
			"`fn open(Only.One(p)): Unit`. The builder binds `p` to a temporary it never " +
			"defines, and ir.Lint panics; a payload of any other type lowers",
		reason: "temp defined before use",
		panics: true,
		noSkip: true,
		src: `import std/io

enum Only {
    One Unit
}

fn open(Only.One(p)): Unit {
    p
}

fn main() {
    io.inspect(open(Only.One(Unit)))
}
`,
	},
	{
		name: "gapUnsolvedGenericLiteral",
		fix: "builder: a generic struct literal, a `Map.get` over a map literal or a list of empty " +
			"vectors or sets, whose type argument stays unsolved because no value has it: " +
			"`io.inspect(Gbox{v: []})`, `io.inspect([#{}])`, where " +
			"the element type of `[]` is never fixed. The checker accepts it, as it accepts " +
			"`io.inspect([])` (§2, an unsolved type argument no value has is not an error), " +
			"and the builder lowers `io.inspect([])` but not the literal around it",
		reason: "a struct literal: Gbox",
		noSkip: true,
		src: `import std/io

struct Gbox<A> {
    v: A
}

fn main() {
    io.inspect(Gbox{v: []})
}
`,
	},
	{
		name: "gapExistentialListInNestedFn",
		fix: "builder: a list literal of two implementors of an interface, typed as a list of the " +
			"interface by a nested `fn`'s return type (`fn inner(): List<Display> { [1, \"a\"] }`) " +
			"or by the expected type of a block whose tail it is. The same literal returned by a " +
			"top-level `fn` or bound with an annotation lowers",
		reason: "a ListLit",
		noSkip: true,
		src: `import std/io

fn main() {
    fn inner(): List<Display> {
        [1, "a"]
    }

    io.print(inner() |> Iter.map(|d| Display.to_string(d)) |> String.join(","))
}
`,
	},
}

// matrixCheckerGaps are the expressions the matrix found the checker
// leaving without a type. They join knownCheckerGaps.
var matrixCheckerGaps = []checkerGap{
	{
		// The enum payload context once wrote `Has {a: Int, b: String}`, a
		// struct variant, for an anonymous struct payload, and found this.
		name: "gapStructVariantCallForm",
		fix: "the record call form of a struct variant, `Pay.Has({a: 1, b: \"x\"})`, leaves its " +
			"anonymous struct argument without a type. The program runs",
		match: regexp.MustCompile(`^main\.nomi:9:\d+: StructLit in Call has no type recorded: .got = case Pay\.Has\(\{a: 1, b: "x"\}\) \{.$`),
		src: `import std/io

enum Pay {
    Has {a: Int, b: String}
    Gone
}

fn main() {
    got = case Pay.Has({a: 1, b: "x"}) {
        Pay.Has(p) -> p
        Pay.Gone -> todo
    }
    io.inspect(got)
}
`,
	},
}

func init() {
	knownLoweringGaps = append(knownLoweringGaps, matrixLoweringGaps...)
	knownCheckerGaps = append(knownCheckerGaps, matrixCheckerGaps...)
}
