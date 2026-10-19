package vmhost

// matrixLoweringGaps are the gaps the feature × context matrix found
// (feature_matrix_test.go): programs the front end accepts and the builder
// declines or panics on, each pinned by a minimal reproducer. They join
// knownLoweringGaps, so TestKnownLoweringGaps holds each reproducer to its
// decline. A gap whose reason the builder derives from the node alone
// (noSkip) excuses no matrix cell by that reason; mxKnown names the cells
// it fails by their shape.
var matrixLoweringGaps = []loweringGap{
	{
		name: "std container held as a program interface",
		fix: "builder and VM: a VM existential is the concrete value, and a dispatch selects the impl by the type name " +
			"the value carries (vm.runtimeTypeName); a List, Vector, Map, Set, Maybe or Result value carries none that " +
			"names its type arguments, so `s: Show = [1, 2]` needs the value boxed with its type at the erasure",
		reason: "a value of kind List<Int> held as the existential Show",
		src: `interface Show {
    fn show(value: self) -> String
}

impl Show for List<T> {
    fn show(xs: List<T>) -> String {
        "n${Iter.count(xs)}"
    }
}

fn main() {
    s: Show = [1, 2]
    _ = Show.show(s)
}
`,
	},
	{
		name: "enum with an embeds variant held as a program interface",
		fix: "builder and VM: a dispatch selects the impl by the type the value carries, and a value widened into an " +
			"`embeds` variant is the embedded struct itself, so `s: Show = Shape.Dot` (irDispatchEnum) needs the " +
			"enum's identity carried at the erasure",
		reason: "a value of kind Shape held as the existential Show",
		src: `interface Show {
    fn show(value: self) -> String
}

struct Circle {
    r: Int
}

enum Shape {
    embeds Circle
    Dot
}

impl Show for Shape {
    fn show(s: Shape) -> String {
        case s {
            .Dot -> "dot"
            _ -> "circle"
        }
    }
}

fn main() {
    s: Show = Shape.Dot
    _ = Show.show(s)
}
`,
	},
}

// matrixCheckerGaps are the expressions the matrix found the checker
// leaving without a type. They join knownCheckerGaps.
var matrixCheckerGaps = []checkerGap{}

func init() {
	knownLoweringGaps = append(knownLoweringGaps, matrixLoweringGaps...)
	knownCheckerGaps = append(knownCheckerGaps, matrixCheckerGaps...)
}
