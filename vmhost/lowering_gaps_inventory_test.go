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
		name:   "gapBareImportedMaybeMethod",
		fix:    "builder: bare imported maybe method. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.call/lower | false | !(p != nil)#1",
		reason: "a direct callee outside this module's fn table: with_default",
		noSkip: true,
		src: `import {
    std/io
    std/maybe.Maybe.{with_default}
}

fn main() {
    io.inspect(with_default(Some(1), 2))
}
`,
	},
	{
		name:   "gapBareStdMethodLocalType",
		fix:    "builder: bare std method local type. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.call | false | localType ¦¦ localIface#1",
		reason: "a direct callee outside this module's fn table: parse",
		noSkip: true,
		src: `import {
    std/io
    std/calendar.Date.parse
}

struct Date {
    day: Int
}

fn main() {
    io.inspect(Result.ok?(parse("2024-01-02")))
    io.inspect(Date{day: 1})
}
`,
	},
	{
		name:   "gapPipeDbgGrouped",
		fix:    "builder: pipe dbg grouped. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.pipe | false | r.Expr != nil#2",
		reason: "the pipe operator `|>`",
		noSkip: true,
		src: `import std/io

fn main() {
    x = 5 |> (dbg 1)
    io.inspect(x)
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
		name:   "gapDeferLambda",
		fix:    "builder: defer lambda. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.deferCall | note | \"a defer inside a lambda\"#1",
		reason: "a defer inside a lambda",
		src: `import std/io

fn close(name: String): Unit {
    io.print("close " + name)
}

fn main() {
    f = |name: String| {
        defer close(name)
        io.print("body")
    }
    f("a")
}
`,
	},
	{
		name:   "gapDeferLoop",
		fix:    "builder: defer loop. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.deferCall | note | \"a defer outside a named function's body or scoped block\"#1",
		reason: "a defer outside a named function's body or scoped block",
		src: `import std/io

fn close(name: String): Unit {
    io.print("close " + name)
}

fn main() {
    _ = Iter.loop(|n = 0| {
        defer close("loop")
        if n == 2 { break n }
        n + 1
    })
}
`,
	},
	{
		name:   "gapTestImportRebind",
		fix:    "builder: test import rebind. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.testImport/no | note | \"an `import` in a block that rebinds a name the module scope binds differently\"#1",
		reason: "an `import` in a block that rebinds a name the module scope binds differently",
		src: `import std/io

struct Duration {
    ms: Int
}

fn main() {
    io.inspect(Duration{ms: 1}.ms)
}

test "a block import that rebinds a module-level name" {
    import std/duration.Duration
    assert Duration.seconds(1) == Duration.milliseconds(1000)
}
`,
	},
	{
		name:   "gapEqualImplSelfRecursion",
		fix:    "builder: equal impl self recursion. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.equatableImplCall | false | bl.f.Sym() == sym#1",
		reason: "the comparison operator `==`",
		noSkip: true,
		src: `import std/io

struct P {
    x: Int
}

impl Equatable for P {
    fn equal?(a: P, b: P): Bool {
        a.x == b.x or a == b
    }
}

fn main() {
    io.print(P{x: 1} == P{x: 2})
}
`,
	},
}

func init() {
	knownLoweringGaps = append(knownLoweringGaps, inventoryLoweringGaps...)
}
