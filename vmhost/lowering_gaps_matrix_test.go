package vmhost

import "regexp"

// matrixLoweringGaps are the gaps the feature × context matrix found
// (feature_matrix_test.go): programs the front end accepts and the builder
// declines or panics on, each pinned by a minimal reproducer. They join
// knownLoweringGaps, so TestKnownLoweringGaps holds each reproducer to its
// decline. A gap whose reason the builder derives from the node alone
// (noSkip) excuses no matrix cell by that reason; mxKnown names the cells
// it fails by their shape.
var matrixLoweringGaps = []loweringGap{}

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
