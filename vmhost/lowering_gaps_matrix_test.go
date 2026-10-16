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
	{
		name: "gapNestedFnInFieldDefault",
		fix: "a `fn` declared inside a struct field's default leaves its body's tail and the call " +
			"of it without a type when the tail is Unit, a function value, a `case`, a call with " +
			"named arguments and more (30 matrix features; an Int literal is typed), and the " +
			"builder declines what they hold. The same block as a function body, a binding or " +
			"a `once` initializer is checked",
		match: regexp.MustCompile(`^main\.nomi:(6:13: TypeIdent in ExprStmt has no type recorded: .Unit.|8:9: Call in ExprStmt has no type recorded: .inner\(\).)$`),
		src: `import std/io

struct D {
    v: Unit = {
        fn inner(): Unit {
            Unit
        }
        inner()
    }
}

fn main() {
    io.inspect(D{}.v)
}
`,
	},
}

func init() {
	knownLoweringGaps = append(knownLoweringGaps, matrixLoweringGaps...)
	knownCheckerGaps = append(knownCheckerGaps, matrixCheckerGaps...)
}
