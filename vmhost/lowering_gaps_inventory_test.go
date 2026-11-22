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
	{
		name: "gapPartialOpenSlotOfAnUndispatchableFunction",
		fix: "the builder: an impl function builds accessors for its defaults only when its interface function can be dispatched on " +
			"(slotDispatchMethod), so a partial on a Ring that leaves `by` open finds none for Ring's default " +
			"(irScalarBuilder.fillFromAccessors | note | a defaulted parameter with no accessor)",
		reason: "a defaulted parameter with no accessor",
		src: `import std::io

pub interface Grow
    open fn grow(value: self, by: Int = 1) -> List<self>
        if by > 0 then [value] else []
    end
end

pub struct Ring
    r: Int
end

impl Grow for Ring
    fn grow(g: Ring, by: Int = 2) -> List<Ring>
        [Ring{r: g.r + by}]
    end
end

fn main()
    ring = Ring{r: 2}
    h = &Grow.grow(&1, &2)
    io.print("${List.length(h(ring))}")
end
`,
	},
}

func init() {
	knownLoweringGaps = append(knownLoweringGaps, inventoryLoweringGaps...)
}
