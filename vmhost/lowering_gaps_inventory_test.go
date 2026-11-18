package vmhost

// inventoryLoweringGaps are the gaps the decline inventory found
// (testdata/lowering-declines.txt): decline sites no suite reached, each
// with a program the front end accepts and the builder declines at that
// site. Each fix names the site keys its reproducer reaches. They join
// knownLoweringGaps, so TestKnownLoweringGaps holds each reproducer to its
// decline. The program generator (lowering_gen_test.go) builds none of these
// shapes, so it needs no avoidance for them.
var inventoryLoweringGaps = []loweringGap{
	{
		name: "gapBareVariantWithBraces",
		fix: "the checker, which rejects a field on a bare variant and admits the empty braces, or the builder: variantStructLitOf builds only a struct-shaped variant " +
			"(irScalarBuilder.variantStructLitOf | false | v == nil ¦¦ !irStructShapedVariant(v)#1)",
		reason: "a struct literal: Shape.Dot",
		noSkip: true,
		src: `import std::io

enum Shape
    Circle {radius: Int}
    Dot
end

derive Debug for Shape

fn main()
    b = Shape.Dot{}
    io.inspect(b)
end
`,
	},
}

func init() {
	knownLoweringGaps = append(knownLoweringGaps, inventoryLoweringGaps...)
}
