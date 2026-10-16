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
