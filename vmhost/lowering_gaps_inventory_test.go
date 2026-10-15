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
		name:   "gapLeavingRegionTypedOperand",
		fix:    "builder: leaving region typed operand. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.typedRegionValue | note | \"an `if` or `case` whose every arm leaves the body, used as an operand\"#1",
		reason: "an `if` or `case` whose every arm leaves the body, used as an operand",
		src: `import std/io

fn id(b: Bool): Bool {
    b
}

fn f(x: Int): Bool {
    x > 0 and id(case x {
        1 -> return True
        _ -> return False
    })
}

fn main() {
    io.inspect(f(1))
}
`,
	},
	{
		name:   "gapListPatternOverEmptyLiteral",
		fix:    "builder: list pattern over empty literal. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.listCaseTest | false | k.comp == nil && (spread ¦¦ len(pat.Heads) != 0)#1",
		reason: "a list case pattern outside flat bindings and wildcards",
		src: `import std/io

fn main() {
  n = case [] {
    [_] -> 1
    _ -> 0
  }
  io.inspect(n)
}
`,
	},
	{
		name:   "gapContextValueUnwitnessable",
		fix:    "builder: context value unwitnessable. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.contextValueCall | note | \"a Context.value witness whose type has no single-name identity\"#1",
		reason: "a Context.value witness whose type has no single-name identity",
		src: `import std/io
import std/duration.Duration

fn main() {
    io.inspect(Context.value(Context.root(), Duration))
}
`,
	},
	{
		name:   "gapStructUpdatePatchBinding",
		fix:    "builder: struct update patch binding. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.structUpdateCall | false | !isLit ¦¦ lit.TypeName != nil ¦¦ lit.Spread != nil#1",
		reason: "qualified call, Type.method: Struct.update",
		noSkip: true,
		src: `import std/io

struct User {
    name: String
    age: Int
}

fn main() {
    u = User{name: "a", age: 1}
    patch = {name: "b"}
    io.inspect(Struct.update(u, patch))
}
`,
	},
	{
		name:   "gapNestedFnDestructuringParam",
		fix:    "builder: nested fn destructuring param. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.nestedFunc/no | note | \"a nested fn: \" + why#1",
		reason: "a nested fn: a parameter without an annotation, or a destructuring one",
		src: `import std/io

fn main() {
    fn sum((a, b): (Int, Int)): Int {
        a + b
    }
    io.print(sum((1, 2)))
}
`,
	},
	{
		name:   "gapPartialOpenNonliteralDefault",
		fix:    "builder: partial open nonliteral default. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.partialApplication | false | p.Default != nil && !irClosedLiteral(p.Default)#1",
		reason: "a partial application over a callee whose parameters the builder cannot see, or with a non-literal default",
		src: `import std/io

fn base(): Int {
    10
}

fn conn(host: String, port: Int = base() + 1): String {
    "${host}:${port}"
}

fn main() {
    c = conn("h", _)
    io.print(c(3))
}
`,
	},
}

func init() {
	knownLoweringGaps = append(knownLoweringGaps, inventoryLoweringGaps...)
}
