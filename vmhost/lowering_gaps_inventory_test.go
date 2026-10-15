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
		name:   "gapLambdaReturnEmptyListFirst",
		fix:    "builder: lambda return empty list first. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.explicitReturn | false | !ok ¦¦ k != bl.returnKind#1",
		reason: "a value of kind List<Int> where List<_> is expected",
		src: `import std/io

fn main() {
    g = |x: Int| {
        if x > 0 { return [] }
        if x < 0 { return [x] }
        [x, x]
    }
    io.inspect(g(1))
}
`,
	},
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
		name:   "gapLoopArmReturnEmpty",
		fix:    "builder: loop arm return empty. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.armInto | note | \"an Iter.loop arm's `return` without a value of the state type\"#1",
		reason: "an Iter.loop arm's `return` without a value of the state type",
		src: `import std/io

fn main() {
    r = Iter.loop(|xs = [1, 2, 3]| {
        case xs {
            [] -> break [0]
            [_] -> return []
            [_, ..rest] -> rest
        }
    })
    io.inspect(r)
}
`,
	},
	{
		name:   "gapLoopNestedGuardReturnEmpty",
		fix:    "builder: loop nested guard return empty. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.leading | note | \"an Iter.loop guard outside `if c { break v ¦ return v ¦ continue }`\"#1",
		reason: "an Iter.loop guard outside `if c { break v | return v | continue }`",
		src: `import std/io

fn main() {
    r = Iter.loop(|xs = [1, 2, 3]| {
        case xs {
            [] -> break [0]
            [_, ..rest] -> {
                if rest == [1] { return [] }
                rest
            }
        }
    })
    io.inspect(r)
}
`,
	},
	{
		name:   "gapMapBreakEmptyListFirst",
		fix:    "builder: map break empty list first. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.ctlReturn | false | !ok ¦¦ k != bl.returnKind#1",
		reason: "a value of kind List<Int> where List<_> is expected",
		src: `import std/io

fn main() {
    ys = [1, 2, 3]
        |> Iter.map(|x| {
            if x > 2 { break [] }
            if x > 1 { break [x] }
            [x, x]
        })
        |> Iter.to_list()
    io.inspect(ys)
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
		name:   "gapTurbofishNested",
		fix:    "builder: turbofish nested. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.call | false | !direct ¦¦ bl.g.irMonoTemplate(bl.g.funcs[id.Name]) == nil#1",
		reason: "a turbofish on a call",
		noSkip: true,
		src: `import std/io

fn main() {
    fn ident<T>(x: T): T {
        x
    }
    io.inspect(ident<Int>(3))
}
`,
	},
	{
		name:   "gapCIterParamEmptySet",
		fix:    "builder: c iter param empty set. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.seqView | false | sk == kindEmptySet ¦¦ !irRetainedSetKind(sk) ¦¦ member != elem#1",
		reason: "a direct call's operand: total",
		noSkip: true,
		src: `import std/io

fn total(xs: Iter<Int>): Int {
    Iter.count(xs)
}

fn main() {
    io.inspect(total(#{}))
}
`,
	},
	{
		name:   "gapCLoopFinalBreakEmptyList",
		fix:    "builder: c loop final break empty list. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.loopBody | note | \"an Iter.loop final `break` without a value of the state type\"#1",
		reason: "an Iter.loop final `break` without a value of the state type",
		src: `import std/io

fn main() {
    xs = Iter.loop(|acc: List<Int> = [1]| {
        if Iter.count(acc) > 2 { break acc }
        break []
    })
    io.inspect(xs)
}
`,
	},
	{
		name:   "gapCLoopFnState",
		fix:    "builder: c loop fn state. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.iterLoop | note | \"an Iter.loop state outside the retained value domain: \" + sk.nomi()#1",
		reason: "an Iter.loop state outside the retained value domain: (Int) -> Int",
		src: `import std/io

fn main() {
    f = Iter.loop(|g = |x: Int| x| {
        if g(0) > 3 { break g }
        |x: Int| g(x) + 1
    })
    io.inspect(f(1))
}
`,
	},
	{
		name:   "gapCLoopGuardBreakEmptyList",
		fix:    "builder: c loop guard break empty list. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.loopBody | note | \"an Iter.loop guard outside `if c { break v ¦ return v ¦ continue }`\"#1",
		reason: "an Iter.loop guard outside `if c { break v | return v | continue }`",
		src: `import std/io

fn main() {
    xs = Iter.loop(|acc: List<Int> = [1]| {
        if Iter.count(acc) > 2 { break [] }
        [1, ..acc]
    })
    io.inspect(xs)
}
`,
	},
	{
		name:   "gapCLoopGuardReturnEmptyList",
		fix:    "builder: c loop guard return empty list. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.loopValue | false | !ok ¦¦ vk != k#1",
		reason: "an Iter.loop guard outside `if c { break v | return v | continue }`",
		src: `import std/io

fn main() {
    xs = Iter.loop(|acc: List<Int> = [1]| {
        if Iter.count(acc) > 2 { break acc }
        if Iter.count(acc) == 0 { return [] }
        [1, ..acc]
    })
    io.inspect(xs)
}
`,
	},
	{
		name:   "gapCLoopIfElseBreakEmptyList",
		fix:    "builder: c loop if else break empty list. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.loopBody | note | \"an Iter.loop guard outside `if c { break v ¦ return v ¦ continue }`\"#2",
		reason: "an Iter.loop guard outside `if c { break v | return v | continue }`",
		src: `import std/io

fn main() {
    xs = Iter.loop(|acc: List<Int> = [1]| {
        if Iter.count(acc) > 2 { break [] } else { [1, ..acc] }
    })
    io.inspect(xs)
}
`,
	},
	{
		name:   "gapCLoopIfLetTail",
		fix:    "builder: c loop if let tail. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.loopTail | note | \"an Iter.loop body without a straight tail expression\"#1",
		reason: "an Iter.loop body without a straight tail expression",
		src: `import std/io

fn main() {
    m: Maybe<Int> = Some(3)
    n = Iter.loop(|n = 0| {
        if n > 3 { break n }
        if Some(v) = m { n + v } else { n + 1 }
    })
    io.inspect(n)
}
`,
	},
	{
		name:   "gapCLoopNamedCallback",
		fix:    "builder: c loop named callback. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.iterLoop | note | \"an Iter.loop callback that is not a lambda literal of at most one parameter\"#1",
		reason: "an Iter.loop callback that is not a lambda literal of at most one parameter",
		src: `import std/io

fn step(n: Int): Int {
    if n > 3 { break n }
    n + 1
}

fn main() {
    io.inspect(Iter.loop(step))
}
`,
	},
	{
		name:   "gapCLoopTailEmptyList",
		fix:    "builder: c loop tail empty list. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.loopTail | note | \"an Iter.loop tail that is not the state type\"#1",
		reason: "an Iter.loop tail that is not the state type",
		src: `import std/io

fn main() {
    xs = Iter.loop(|acc: List<Int> = [1]| {
        if Iter.count(acc) > 2 { break acc }
        []
    })
    io.inspect(xs)
}
`,
	},
	{
		name:   "gapCLoopUnseeded",
		fix:    "builder: c loop unseeded. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.iterLoop | note | \"an Iter.loop callback without a seeded state\"#1",
		reason: "an Iter.loop callback without a seeded state",
		src: `import std/io

fn main() {
    n = Iter.loop(|n: Int| {
        if n > 3 { break n }
        n + 1
    })
    io.inspect(n)
}
`,
	},
	{
		name:   "gapAssertPatternStringPrefix",
		fix:    "builder: assert pattern string prefix. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.patternAssert | note | \"an `assert pattern = value` pattern outside the retained case tests: \" + sk.nomi()#1",
		reason: "an `assert pattern = value` pattern outside the retained case tests: String",
		src: `test "assert string prefix pattern" {
  s = "abc"
  assert "a" + rest = s
  assert rest == "bc"
}
`,
	},
	{
		name:   "gapAssertTupleCompositeDistinct",
		fix:    "builder: assert tuple composite distinct. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.patternAssert | note | \"an `assert pattern = value` tuple pattern outside the retained case tests\"#1",
		reason: "a `case` tuple pattern outside flat Int/String tests with bindings and wildcards",
		src: `type Pair (Int, String)

test "assert a tuple pattern holding a tuple distinct" {
  t = (Pair(1, "a"), 2)
  assert (Pair(x, _), 2) = t
  assert x == 1
}
`,
	},
	{
		name:   "gapBindingElseTupleCompositeDistinct",
		fix:    "builder: binding else tuple composite distinct. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.patternBindingStmt | note | \"a pattern binding outside the retained case tests: \" + sk.nomi()#2",
		reason: "a `case` tuple pattern outside flat Int/String tests with bindings and wildcards",
		src: `import std/io

type Pair (Int, String)

fn f(t: (Pair, Int)): Int {
  (Pair(x, _), 2) = t else { return 0 }
  x
}

fn main() {
  io.inspect(f((Pair(1, "a"), 2)))
}
`,
	},
	{
		name:   "gapBindingTupleCompositeDistinct",
		fix:    "builder: binding tuple composite distinct. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.patternBindingStmt | note | \"a pattern binding outside the retained case tests: \" + sk.nomi()#1",
		reason: "a `case` tuple pattern outside flat Int/String tests with bindings and wildcards",
		src: `import std/io

type Pair (Int, String)

fn f(t: (Pair, Int)): Int {
  (Pair(x, _), y) = t
  x + y
}

fn main() {
  io.inspect(f((Pair(1, "a"), 2)))
}
`,
	},
	{
		name:   "gapDistinctDestructureFileQualified",
		fix:    "builder: distinct destructure file qualified. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.distinctBinding | false | !ok ¦¦ k.tag != tagNamed ¦¦ !irDistinctOverValue(k.def) ¦¦ t.TypeName != k.def.nomi#1; irScalarBuilder.distinctBinding | note | \"a distinct destructure outside the retained wrapping domain\"#1",
		reason: "a distinct destructure outside the retained wrapping domain",
		src: `import std/io
import std/calendar
import std/calendar.Days

fn main() {
  calendar.Days(n) = Days(4)
  io.inspect(n)
}
`,
	},
	{
		name:   "gapDistinctDestructureRebind",
		fix:    "builder: distinct destructure rebind. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.distinctBinding | note | \"a rebind: \" + name#1",
		reason: "a rebind: s",
		src: `import std/io

type Id String

fn name_length(s: String): Int {
  Id(s) = Id(s + "!")
  String.length(s)
}

fn main() {
  io.inspect(name_length("ab"))
}
`,
	},
	{
		name:   "gapElseFallbackTupleCompositeDistinct",
		fix:    "builder: else fallback tuple composite distinct. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.fallbackBinding | note | \"a binding else payload pattern outside the retained case tests: \" + pk.nomi()#1",
		reason: "a `case` tuple pattern outside flat Int/String tests with bindings and wildcards",
		src: `import std/io

type Pair (Int, String)

fn f(m: Maybe<(Pair, Int)>): Int {
  Some((Pair(x, _), y)) = m else { (Pair(0, ""), 0) }
  x + y
}

fn main() {
  io.inspect(f(None))
}
`,
	},
	{
		name:   "gapEmbeddedStructFieldCompositeDistinct",
		fix:    "builder: embedded struct field composite distinct. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.embeddedStructCaseTest | false | fields[i] == nil ¦¦ !irFieldSubPattern(f.Pattern, fields[i].k)#1",
		reason: "a `case` enum pattern outside retained bare/positional payload tests",
		src: `import std/io

type Pair (Int, String)

struct Circle {
  p: Pair
}

enum E {
  embeds Circle
  Other
}

fn f(e: E): Int {
  case e {
    .Circle{p: Pair(a, _)} -> a
    _ -> 0
  }
}

fn main() {
  io.inspect(f(Circle{p: Pair(1, "a")}))
}
`,
	},
	{
		name:   "gapListDistinctUntypedListPattern",
		fix:    "builder: list distinct untyped list pattern. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.casePatternTest | note | \"a struct or distinct case pattern outside retained field and payload patterns\"#1; irScalarBuilder.nominalCaseTest | false | !irCompositeDistinct(k.def) ¦¦ k.def.inner.tag != tagList ¦¦ p.TypeName == nil ¦¦ typeText…#1",
		reason: "a struct or distinct case pattern outside retained field and payload patterns",
		src: `import std/io

type Stack List<Int>

fn top(s: Stack): Int {
  case s {
    [a, .._] -> a
    _ -> 0
  }
}

fn main() {
  io.inspect(top(Stack([1, 2])))
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
		name:   "gapMapValueLiteralAttachDistinct",
		fix:    "builder: map value literal attach distinct. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.casePatternTest | note | \"a map case pattern outside retained keys and value patterns\"#1; irScalarBuilder.patternValueTest | note | \"a nested pattern outside the retained tests: \" + irNodeWord(pat) + \" over \" + k.nomi()#1",
		reason: "a nested pattern outside the retained tests: a MapPattern over Kvs",
		src: `import std/io

type Kvs Map<String, Int>

fn f(m: Map<String, Kvs>): Int {
  case m {
    {"k" => Kvs{"a" => v}} -> v
    _ -> 0
  }
}

fn main() {
  io.inspect(f({"k" => Kvs{"a" => 1}}))
}
`,
	},
	{
		name:   "gapRecordFieldLiteralAttachDistinct",
		fix:    "builder: record field literal attach distinct. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.nominalCaseTest | false | p.TypeName != nil ¦¦ !bl.recordCaseFields(p, subj, k, next)#1",
		reason: "a nested pattern outside the retained tests: a ListPattern over Stack",
		src: `import std/io

type Stack List<Int>

fn main() {
  c = {r: 1, q: Stack([1])}
  n = case c {
    {q: Stack[a, .._]} -> a
    _ -> 0
  }
  io.inspect(n)
}
`,
	},
	{
		name:   "gapStructVariantFieldCompositeDistinct",
		fix:    "builder: struct variant field composite distinct. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.casePatternTest | note | \"a `case` enum pattern outside retained bare/positional payload tests\"#1; irScalarBuilder.structVariantCaseTest | false | idx[i] < 0 ¦¦ !irFieldSubPattern(f.Pattern, v.payloads[idx[i]].k)#1",
		reason: "a `case` enum pattern outside retained bare/positional payload tests",
		src: `import std/io

type Pair (Int, String)

enum E {
  Rec {p: Pair}
  Other
}

fn f(e: E): Int {
  case e {
    .Rec{p: Pair(a, _)} -> a
    _ -> 0
  }
}

fn main() {
  io.inspect(f(E.Rec{p: Pair(1, "a")}))
}
`,
	},
	{
		name:   "gapStructVariantPatternAliasQualifier",
		fix:    "builder: struct variant pattern alias qualifier. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.structVariantCaseTest | false | !ok ¦¦ (owner != \"\" && !bl.g.declaredAs(owner, k.def) && !bl.g.preludeOwns(owner, k.def) &…#1",
		reason: "a `case` enum pattern outside retained bare/positional payload tests",
		src: `import std/io

enum Shape {
  Rec {w: Int, h: Int}
  Dot
}

typealias S Shape

fn f(s: Shape): Int {
  case s {
    S.Rec{w} -> w
    _ -> 0
  }
}

fn main() {
  io.inspect(f(Shape.Rec{w: 2, h: 3}))
}
`,
	},
	{
		name:   "gapStructVariantRecordPayloadPattern",
		fix:    "builder: struct variant record payload pattern. Found unreached by the decline inventory (testdata/lowering-declines.txt) at gen.retainedEnumPattern | false | v.kind == \"struct\"#1",
		reason: "a `case` enum pattern outside retained bare/positional payload tests",
		src: `import std/io

enum Shape {
  Rec {w: Int, h: Int}
  Dot
}

fn f(s: Shape): Int {
  case s {
    .Rec({w, h}) -> w + h
    _ -> 0
  }
}

fn main() {
  io.inspect(f(Shape.Rec{w: 2, h: 3}))
}
`,
	},
	{
		name:   "gapTupleDestructureRebindsFunction",
		fix:    "builder: tuple destructure rebinds function. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.destructureBind | note | \"a rebind: \" + name#1",
		reason: "a rebind: f",
		src: `import std/io

fn main() {
  f = |x: Int| x
  io.inspect(f(1))
  (f, n) = (|x: Int| x + 1, 1)
  io.inspect(f(n))
}
`,
	},
	{
		name:   "gapTuplePatternCompositeDistinct",
		fix:    "builder: tuple pattern composite distinct. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.tupleCaseTest | note | \"a `case` tuple pattern outside flat Int/String tests with bindings and wildcards\"#1",
		reason: "a `case` tuple pattern outside flat Int/String tests with bindings and wildcards",
		src: `import std/io

type Pair (Int, String)

fn f(t: (Pair, Int)): Int {
  case t {
    (Pair(1, _), 2) -> 1
    _ -> 0
  }
}

fn main() {
  io.inspect(f((Pair(1, "a"), 2)))
}
`,
	},
	{
		name:   "gapVariantPatternAliasQualifier",
		fix:    "builder: variant pattern alias qualifier. Found unreached by the decline inventory (testdata/lowering-declines.txt) at gen.retainedEnumPattern | false | !ok ¦¦ (owner != \"\" && !g.declaredAs(owner, k.def) && !g.preludeOwns(owner, k.def) && !g.s…#1",
		reason: "a `case` enum pattern outside retained bare/positional payload tests",
		src: `import std/io

enum Shape {
  Num Int
  Dot
}

typealias S Shape

fn f(s: Shape): Int {
  case s {
    S.Num(n) -> n
    S.Dot -> 0
  }
}

fn main() {
  io.inspect(f(Shape.Num(2)))
}
`,
	},
	{
		name:   "gapVariantPayloadAsCompositeDistinct",
		fix:    "builder: variant payload as composite distinct. Found unreached by the decline inventory (testdata/lowering-declines.txt) at gen.retainedEnumPattern | false | pat.Binding != \"\" ¦¦ !irFieldSubPattern(pat.Payload, pk)#1; irScalarBuilder.enumCaseTest | note | \"a `case` enum pattern outside retained bare/positional payload tests\"#1",
		reason: "a `case` enum pattern outside retained bare/positional payload tests",
		src: `import std/io

type Pair (Int, String)

enum E {
  Dist Pair
  Other
}

fn f(e: E): Int {
  case e {
    .Dist(Pair(a, _) as _whole) -> a
    _ -> 0
  }
}

fn main() {
  io.inspect(f(E.Dist(Pair(1, "a"))))
}
`,
	},
	{
		name:   "gapVariantPayloadTupleCompositeDistinct",
		fix:    "builder: variant payload tuple composite distinct. Found unreached by the decline inventory (testdata/lowering-declines.txt) at gen.retainedEnumPattern | false | pat.Binding != \"\" ¦¦ !ok#1",
		reason: "a `case` enum pattern outside retained bare/positional payload tests",
		src: `import std/io

type Pair (Int, String)

enum E {
  TP (Pair, Int)
  Other
}

fn f(e: E): Int {
  case e {
    .TP((Pair(a, _), _)) -> a
    _ -> 0
  }
}

fn main() {
  io.inspect(f(E.TP((Pair(1, "a"), 2))))
}
`,
	},
	{
		name:   "gapChannelNamedArg",
		fix:    "builder: channel named arg. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.channelCall | false | bl.rowsUnrecorded(t) ¦¦ namedArgNode(t.Args) != nil#1",
		reason: "a named argument",
		noSkip: true,
		src: `import std/io
import std/channels.{Channel, Receiver, Sender}

fn main() {
    ch = Channel.buffered<Int>(2)
    _ = Sender.send(sender: ch.sender, value: 1)
    io.inspect(Receiver.receive(ch.receiver))
}
`,
	},
	{
		name:   "gapContextValueNamedArg",
		fix:    "builder: context value named arg. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.contextValueCall | false | named#1",
		reason: "a type name in value position: TraceId",
		noSkip: true,
		src: `import std/io

type TraceId String

fn main() {
    c = Context.with_value(Context.root(), TraceId("t"))
    io.inspect(Context.value(c, value_type: TraceId))
}
`,
	},
	{
		name:   "gapContextValueQualifiedWitness",
		fix:    "builder: context value qualified witness. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.contextValueCall | note | \"a Context.value witness that is not a type name\"#1",
		reason: "a Context.value witness that is not a type name",
		src: `// FILE: main.nomi
import std/io
import domain

fn main() {
    c = Context.with_value(Context.root(), domain.Token(7))
    io.inspect(Context.value(c, domain.Token))
}
// FILE: domain.nomi
pub type Token Int
`,
	},
	{
		name:   "gapContextValueTurbofish",
		fix:    "builder: context value turbofish. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.contextValueCall | false | len(t.Args) != 2 ¦¦ len(t.TypeArgs) != 0#1",
		reason: "a turbofish on a call",
		noSkip: true,
		src: `import std/io

type TraceId String

fn main() {
    c = Context.with_value(Context.root(), TraceId("t"))
    io.inspect(Context.value<TraceId>(c, TraceId))
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
		name:   "gapSupervisorSpawnAllIterSource",
		fix:    "builder: supervisor spawn all iter source. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.supervisorSpawnAll | false | src.tag != tagList ¦¦ src.comp == nil#1",
		reason: "qualified call, Type.method: Supervisor.spawn_all",
		noSkip: true,
		src: `import std/io
import std/supervisors.Supervisor

struct App {
    context: Context
    work: Supervisor
}

fn boot(): App {
    App{context: Context.root(), work: Supervisor.new(2)}
}

fn main() {
    _ = Supervisor.spawn_all(App.work, [1, 2] |> Iter.map(|x: Int| x + 1), |n: Int| io.inspect(n))
}
`,
	},
	{
		name:   "gapSupervisorSpawnAllNamedArg",
		fix:    "builder: supervisor spawn all named arg. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.supervisorSpawnAll | false | bl.rowsUnrecorded(t) ¦¦ namedArgNode(t.Args) != nil ¦¦ len(t.Args) != 3#1",
		reason: "a named argument",
		noSkip: true,
		src: `import std/io
import std/supervisors.Supervisor

struct App {
    context: Context
    work: Supervisor
}

fn boot(): App {
    App{context: Context.root(), work: Supervisor.new(2)}
}

fn main() {
    _ = Supervisor.spawn_all(App.work, [1, 2], f: |n: Int| io.inspect(n))
}
`,
	},
	{
		name:   "gapSupervisorSpawnFieldOperand",
		fix:    "builder: supervisor spawn field operand. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.supervisorSpawn | false | !app#1",
		reason: "qualified call, Type.method: Supervisor.spawn",
		noSkip: true,
		src: `import std/io
import std/supervisors.Supervisor

struct App {
    context: Context
    work: Supervisor
}

struct Holder {
    sup: Supervisor
}

fn boot(): App {
    App{context: Context.root(), work: Supervisor.new(2)}
}

fn main() {
    h = Holder{sup: App.work}
    _ = Supervisor.spawn(h.sup, || io.print("a"))
}
`,
	},
	{
		name:   "gapSupervisorSpawnNamedArg",
		fix:    "builder: supervisor spawn named arg. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.supervisorSpawn | false | bl.rowsUnrecorded(t) ¦¦ namedArgNode(t.Args) != nil ¦¦ len(t.Args) != 2#1",
		reason: "a named argument",
		noSkip: true,
		src: `import std/io
import std/supervisors.Supervisor

struct App {
    context: Context
    work: Supervisor
}

fn boot(): App {
    App{context: Context.root(), work: Supervisor.new(2)}
}

fn main() {
    _ = Supervisor.spawn(App.work, body: || io.print("job"))
}
`,
	},
	{
		name:   "gapSupervisorSpawnNamedFn",
		fix:    "builder: supervisor spawn named fn. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.supervisorSpawn | false | !isLambda ¦¦ len(lam.Params) != 0#1",
		reason: "qualified call, Type.method: Supervisor.spawn",
		noSkip: true,
		src: `import std/io
import std/supervisors.Supervisor

struct App {
    context: Context
    work: Supervisor
}

fn boot(): App {
    App{context: Context.root(), work: Supervisor.new(2)}
}

fn job(): Unit {
    io.print("job")
}

fn main() {
    _ = Supervisor.spawn(App.work, job)
}
`,
	},
	{
		name:   "gapTaskAwaitNamedArg",
		fix:    "builder: task await named arg. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.taskCall | false | namedArgNode(t.Args) != nil ¦¦ len(t.Args) != 1#1",
		reason: "a named argument",
		noSkip: true,
		src: `import std/io
import std/tasks.Task

fn main() {
    n = concurrent {
        t = Task.spawn(|| 3)
        Task.await(task: t)
    }
    io.inspect(n)
}
`,
	},
	{
		name:   "gapTaskSpawnAllVector",
		fix:    "builder: task spawn all vector. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.taskSpawnAll | false | src.tag != tagSeq ¦¦ src.comp == nil ¦¦ kinds[2] != kindInt#1",
		reason: "a named argument",
		noSkip: true,
		src: `import std/io
import std/tasks.Task

fn double(n: Int): Int {
    n * 2
}

fn main() {
    xs = concurrent {
        ts = Task.spawn_all([1, 2, 3] |> Iter.to_vector(), double, max_running: 2)
        Task.await_all(ts)
    }
    io.inspect(xs)
}
`,
	},
	{
		name:   "gapTaskSpawnNamedFn",
		fix:    "builder: task spawn named fn. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.taskCall | false | !isLambda ¦¦ len(lam.Params) != 0#1",
		reason: "qualified call, Type.method: Task.spawn",
		noSkip: true,
		src: `import std/io
import std/tasks.Task

fn work(): Int {
    7
}

fn main() {
    n = concurrent {
        t = Task.spawn(work)
        Task.await(t)
    }
    io.inspect(n)
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
		name:   "gapTypeParamQualifiedWitness",
		fix:    "builder: type param qualified witness. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.namesType | false | unguarded#1; irScalarBuilder.typeWitnessValue | false | !irWitnessableKind(x) ¦¦ !bl.namesType(at, x)#1",
		reason: "an ident bound nowhere this builder can see",
		noSkip: true,
		src: `// FILE: main.nomi
import std/io
import domain

fn stored(c: Context, t: Type<domain.Token>): Maybe<domain.Token> {
    Context.value(c, t)
}

fn main() {
    c = Context.with_value(Context.root(), domain.Token(7))
    io.inspect(stored(c, domain.Token))
}
// FILE: domain.nomi
pub type Token Int
`,
	},
	{
		name:   "gapAndLeftDeclines",
		fix:    "builder: and left declines. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.shortCircuit | false | !ok ¦¦ lk != kindBool#1",
		reason: "the comparison operator `==`",
		noSkip: true,
		src: `import std/io
import std/channels.Channel

fn main() {
    ch: Channel<Int> = Channel.unbuffered()
    io.print(ch.sender == ch.sender and True)
}
`,
	},
	{
		name:   "gapCapturedEmptyList",
		fix:    "builder: captured empty list. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.lowerWant | note | \"a value of kind \" + have.nomi() + \" where \" + want.nomi() + \" is expected\"#1",
		reason: "a value of kind Captured<List<_>> where Captured<List<Int>> is expected",
		src: `import std/io
import std/io.Captured

fn main() {
    c: Captured<List<Int>> = Captured{value: [], output: "", transcript: ""}
    io.inspect(c.value)
}
`,
	},
	{
		name:   "gapDecimalDoubledUnderscore",
		fix:    "builder: decimal doubled underscore. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.decimalLiteral | false | err != nil#1",
		reason: "a DecimalLit",
		noSkip: true,
		src: `import std/io

fn main() {
    io.inspect(1__0.5d)
}
`,
	},
	{
		name:   "gapDefaultGenericLambda",
		fix:    "builder: default generic lambda. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.callDefaults | false | !ok ¦¦ k != sig.params[i] ¦¦ !irCallOperandKind(k)#1",
		reason: "a lambda parameter outside the domain: an unsupported type",
		src: `import std/io

fn apply<T>(x: T, f: (T) -> T = |y| y): T {
    f(x)
}

fn main() {
    io.print(apply(3))
}
`,
	},
	{
		name:   "gapDistinctTupleCallformIterWiden",
		fix:    "builder: distinct tuple callform iter widen. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.compositeDistinctMake | false | !ok ¦¦ k != d.inner#1",
		reason: "a value of kind List<Circle> where Iter<Shape> is expected",
		src: `import std/io

struct Circle {
    r: Int
}

enum Shape {
    embeds Circle
    Dot
}

type P (Iter<Shape>, Int)

fn main() {
    c = Circle{r: 1}
    q = P(([c], 2))
    P(it, n) = q
    io.print(Iter.count(it) + n)
}
`,
	},
	{
		name:   "gapDistinctTupleIterWiden",
		fix:    "builder: distinct tuple iter widen. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.compositeDistinctMake | false | !vok ¦¦ vk != d.inner.comp.parts[i]#1",
		reason: "a value of kind List<Circle> where Iter<Shape> is expected",
		src: `import std/io

struct Circle {
    r: Int
}

enum Shape {
    embeds Circle
    Dot
}

type P (Iter<Shape>, Int)

fn main() {
    c = Circle{r: 1}
    p = P([c], 1)
    P(it, n) = p
    io.print(Iter.count(it) + n)
}
`,
	},
	{
		name:   "gapDottedGenericStructLiteral",
		fix:    "builder: dotted generic struct literal. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.structMake | false | tn.Member == nil ¦¦ bl.g.genericTemplates[tn.Module+\".\"+tn.Member.TypeString()] != nil#1",
		reason: "a struct literal: Tree.Node",
		noSkip: true,
		src: `import std/io

struct Tree.Node<T> {
    v: T
}

fn main() {
    n = Tree.Node{v: 1}
    io.print(n.v)
}
`,
	},
	{
		name:   "gapEnumFieldReadDisagree",
		fix:    "builder: enum field read disagree. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.enumFieldRead | note | \"an enum field read whose variants disagree on its type: .\" + name#1",
		reason: "an enum field read whose variants disagree on its type: .w",
		src: `import std/io

enum Shape {
    Rect {w: Int}
    Circle {w: Float}
}

fn main() {
    s = Shape.Rect{w: 1}
    io.print(s.w)
}
`,
	},
	{
		name:   "gapEnumFieldReadFunction",
		fix:    "builder: enum field read function. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.enumFieldRead | false | !found ¦¦ !irRetainedValueKind(fk)#1",
		reason: "a field access on an unbound name o: .f",
		noSkip: true,
		src: `import std/io

enum Op {
    Unary {f: (Int) -> Int}
    Nop
}

fn main() {
    o = Op.Unary{f: |x| x + 1}
    g = o.f
    io.print(g(1))
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
	{
		name:   "gapFieldDefaultIterWiden",
		fix:    "builder: field default iter widen. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.declDefault | false | !ok ¦¦ k != want#1",
		reason: "a struct literal: H",
		noSkip: true,
		src: `import std/io

struct Circle {
    r: Int
}

enum Shape {
    embeds Circle
    Dot
}

struct H {
    it: Iter<Shape> = [Circle{r: 1}]
}

fn main() {
    h = H{}
    io.print(Iter.count(h.it))
}
`,
	},
	{
		name:   "gapIfaceGapDefaultCrossFile",
		fix:    "builder: iface gap default cross file. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.ifaceGapDefaults | false | unguarded#1",
		reason: "a named argument",
		noSkip: true,
		src: `// FILE: main.nomi
import std/io
import shapes.{Shape, Sq}

fn main() {
    io.print(Shape.area(Sq{side: 2}, extra: 1))
}
// FILE: shapes.nomi
pub interface Shape {
    open fn area(_s: self, scale: Int = 1, extra: Int = 0): Int {
        scale + extra
    }
}

pub struct Sq {
    side: Int
}

impl Shape for Sq {
    fn area(s: Sq, scale: Int = 1, extra: Int = 0): Int {
        s.side * scale + extra
    }
}
`,
	},
	{
		name:   "gapIfaceGapDefaultImplElsewhere",
		fix:    "builder: iface gap default impl elsewhere. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.ifaceGapDefaults | false | impl == nil ¦¦ !impl.lowerable#1",
		reason: "a named argument",
		noSkip: true,
		src: `// FILE: main.nomi
import std/io
import sq.Sq

pub interface Shape {
    open fn area(_s: self, scale: Int = 1, extra: Int = 0): Int {
        scale + extra
    }
}

fn main() {
    io.print(Shape.area(Sq{side: 2}, extra: 1))
}
// FILE: sq.nomi
import main.Shape

pub struct Sq {
    side: Int
}

impl Shape for Sq {
    fn area(s: Sq, scale: Int = 1, extra: Int = 0): Int {
        s.side * scale + extra
    }
}
`,
	},
	{
		name:   "gapIterOfEmbeddingEnumFromList",
		fix:    "builder: iter of embedding enum from list. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.coerceEmpty | false | want.tag == tagSeq && want.comp != nil#1",
		reason: "a value of kind List<Circle> where Iter<Shape> is expected",
		src: `import std/io

struct Circle {
    r: Int
}

enum Shape {
    embeds Circle
    Dot
}

fn main() {
    c = Circle{r: 2}
    it: Iter<Shape> = [c]
    io.print(Iter.count(it))
}
`,
	},
	{
		name:   "gapMaybeEqualEmbeddedWiden",
		fix:    "builder: maybe equal embedded widen. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.containerEqualCall | false | at == 1 && bare[1] == kindInvalid#1",
		reason: "qualified call, Type.method: Maybe.equal?",
		noSkip: true,
		src: `import std/io

struct Circle {
    r: Int
}

enum Shape {
    embeds Circle
    Dot
}

derive Equatable for Circle
derive Equatable for Shape

fn main() {
    c = Circle{r: 1}
    s: Shape = Shape.Dot
    io.print(Maybe.equal?(Some(c), Some(s)))
}
`,
	},
	{
		name:   "gapMaybeEqualStructPayload",
		fix:    "builder: maybe equal struct payload. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.containerEqualCall | false | !bl.g.irPreludeEqualityKind(rk) ¦¦ lk != rk#1",
		reason: "qualified call, Type.method: Maybe.equal?",
		noSkip: true,
		src: `import std/io

struct Inner {
    a: Int
}

derive Equatable for Inner

fn main() {
    io.print(Maybe.equal?(Some(Inner{a: 1}), None))
}
`,
	},
	{
		name:   "gapNamedArgDestructuredParam",
		fix:    "builder: named arg destructured param. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.namedQualCall | false | p.Destructure != nil#1",
		reason: "a named argument",
		noSkip: true,
		src: `import std/io

struct Point {
    x: Int
    y: Int
}

impl Point {
    fn scale(Point{x, y}, by: Int): Int {
        x * by + y
    }
}

fn main() {
    io.print(Point.scale(Point{x: 1, y: 2}, by: 3))
}
`,
	},
	{
		name:   "gapNamedArgValueDeclines",
		fix:    "builder: named arg value declines. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.namedQualCall | false | !ok ¦¦ !irCallOperandKind(k)#1",
		reason: "the comparison operator `==`",
		noSkip: true,
		src: `import std/io
import std/channels.Channel

struct P {
    x: Int
}

impl P {
    fn show(p: P, flag: Bool): String {
        "${p.x} ${flag}"
    }
}

fn main() {
    ch: Channel<Int> = Channel.unbuffered()
    io.print(P.show(P{x: 1}, flag: ch.sender == ch.sender))
}
`,
	},
	{
		name:   "gapNamedGapGenericParam",
		fix:    "builder: named gap generic param. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.namedQualCall | false | !irCallOperandKind(kinds[i])#1",
		reason: "a named argument",
		noSkip: true,
		src: `import std/io

struct Box<T> {
    v: T
}

impl Box<T> {
    fn pick<T>(_b: Box<T>, other: Maybe<T> = None, n: Int = 0): Int {
        case other {
            Some(_) -> n + 1
            None -> n
        }
    }
}

fn main() {
    io.print(Box.pick(Box{v: 1}, n: 2))
}
`,
	},
	{
		name:   "gapSenderEquality",
		fix:    "builder: sender equality. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.compareOperands | false | (!irScalarLeafKind(lk) ¦¦ lk.def != nil) && !prelude && !decimal && !list && !structural#1",
		reason: "the comparison operator `==`",
		noSkip: true,
		src: `import std/io
import std/channels.Channel

fn main() {
    ch: Channel<Int> = Channel.unbuffered()
    io.print(ch.sender == ch.sender)
}
`,
	},
	{
		name:   "gapSpreadBaseDeclines",
		fix:    "builder: spread base declines. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.structSpread | false | !ok ¦¦ !irRetainedUpdateKind(k)#1",
		reason: "the comparison operator `==`",
		noSkip: true,
		src: `import std/io
import std/channels.Channel

struct P {
    x: Int
}

fn main() {
    ch: Channel<Int> = Channel.unbuffered()
    p = P{x: 1}
    q = {..(if ch.sender == ch.sender { p } else { p }), x: 2}
    io.print(q.x)
}
`,
	},
	{
		name:   "gapStructSpreadIterFieldWiden",
		fix:    "builder: struct spread iter field widen. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.structMergeVals | false | !ok ¦¦ vk != fk#1",
		reason: "a struct literal: <none>",
		noSkip: true,
		src: `import std/io

struct Circle {
    r: Int
}

enum Shape {
    embeds Circle
    Dot
}

struct H {
    it: Iter<Shape>
    n: Int
}

fn main() {
    c = Circle{r: 1}
    h = H{it: [Shape.Dot], n: 1}
    h2 = {..h, it: [c]}
    io.print(Iter.count(h2.it))
}
`,
	},
	{
		name:   "gapStructUpdateNamedIterFieldWiden",
		fix:    "builder: struct update named iter field widen. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.recordMerge | false | !ok ¦¦ vk != fk#1",
		reason: "a named argument",
		noSkip: true,
		src: `import std/io

struct Circle {
    r: Int
}

enum Shape {
    embeds Circle
    Dot
}

struct H {
    it: Iter<Shape>
    n: Int
}

fn main() {
    c = Circle{r: 1}
    h = H{it: [Shape.Dot], n: 1}
    h2 = Struct.update(updates: {it: [c]}, original: h)
    io.print(Iter.count(h2.it))
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
		name:   "gapTupleEmptyListEquality",
		fix:    "builder: tuple empty list equality. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.compareOperands | false | !ok ¦¦ lk != rk#1",
		reason: "the comparison operator `==`",
		noSkip: true,
		src: `import std/io

fn main() {
    io.print((1, []) == (1, [2]))
}
`,
	},
	{
		name:   "gapTupleHashUnitSlot",
		fix:    "checker or builder: the tuple now lowers, and tupleHashCall's synthesized `Hashable.hash` of the Unit part declines; the checker rejects `Hashable.hash(Unit)` itself (\"no impl of `Hashable` for `Unit`\") yet accepts a tuple holding Unit as Hashable, so either the checker should reject this tuple or Unit should hash. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.tupleHashCall | false | !sok ¦¦ !irRetainedTupleKind(sk)#1",
		reason: "qualified call, Type.method: Hashable.hash",
		noSkip: true,
		src: `import std/io

fn main() {
    io.print(Hashable.hash(('a', Unit)))
}
`,
	},
	{
		name:   "gapTypeParamNamedStruct",
		fix:    "builder: type param named struct. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.structUpdateCall | false | bound#1",
		reason: "qualified call, Type.method: Struct.update",
		noSkip: true,
		src: `import std/io

interface Up {
    fn update(a: self, b: {x: Int}): self
}

struct P {
    x: Int
}

impl Up for P {
    fn update(_a: P, b: {x: Int}): P {
        P{x: b.x}
    }
}

fn bump<Struct>(p: Struct): Struct where Struct: Up {
    Struct.update(p, {x: 2})
}

fn main() {
    io.print(bump(P{x: 1}).x)
}
`,
	},
	{
		name:   "gapUpdateBaseDeclines",
		fix:    "builder: update base declines. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.structUpdateCall | false | !ok ¦¦ !irRetainedUpdateKind(k)#2",
		reason: "the comparison operator `==`",
		noSkip: true,
		src: `import std/io
import std/channels.Channel

struct P {
    x: Int
}

fn main() {
    ch: Channel<Int> = Channel.unbuffered()
    p = P{x: 1}
    q = Struct.update(if ch.sender == ch.sender { p } else { p }, {x: 2})
    io.print(q.x)
}
`,
	},
	{
		name:   "gapUpdateNamedBaseDeclines",
		fix:    "builder: update named base declines. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.structUpdateCall | false | !ok ¦¦ !irRetainedUpdateKind(k)#1",
		reason: "the comparison operator `==`",
		noSkip: true,
		src: `import std/io
import std/channels.Channel

struct P {
    x: Int
}

fn main() {
    ch: Channel<Int> = Channel.unbuffered()
    p = P{x: 1}
    q = Struct.update(updates: {x: 2}, original: if ch.sender == ch.sender { p } else { p })
    io.print(q.x)
}
`,
	},
	{
		name:   "gapVectorCapturedElemWiden",
		fix:    "builder: vector captured elem widen. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.lowerSameKindItems | false | !ok ¦¦ k != want#1",
		reason: "a value of kind Captured<Circle> where Captured<Shape> is expected",
		src: `import std/io
import std/io.Captured

struct Circle {
    r: Int
}

enum Shape {
    embeds Circle
    Dot
}

fn main() {
    c = Circle{r: 1}
    v: Vector<Captured<Shape>> = #[Captured{value: c, output: "", transcript: ""}]
    io.print(Vector.length(v))
}
`,
	},
	{
		name:   "gapVectorItemsJoinCaptured",
		fix:    "builder: vector items join captured. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.literalItemsCoerced | false | !ok ¦¦ k != want#1",
		reason: "a VectorLit",
		noSkip: true,
		src: `import std/io
import std/io.Captured

struct Circle {
    r: Int
}

enum Shape {
    embeds Circle
    Dot
}

fn main() {
    a = Captured{value: Circle{r: 1}, output: "", transcript: ""}
    b = Captured{value: Shape.Dot, output: "", transcript: ""}
    v = #[a, b]
    io.print(Vector.length(v))
}
`,
	},
	{
		name:   "gapCapturedDefaultLambdaCall",
		fix:    "builder: captured default lambda call. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.indirectCall | false | defaulted && metadata == nil#1",
		reason: "a direct callee outside this module's fn table: f",
		noSkip: true,
		src: `import std/io

fn main() {
    f = |x: Int, y: Int = 2| x + y
    g = || f(1)
    io.print(g())
}
`,
	},
	{
		name:   "gapCapturedLambdaNamedArgs",
		fix:    "builder: captured lambda named args. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.indirectCall | false | !planned#1",
		reason: "a named argument",
		noSkip: true,
		src: `import std/io

fn main() {
    f = |x: Int, y: Int| x - y
    g = || f(y: 1, x: 5)
    io.print(g())
}
`,
	},
	{
		name:   "gapGenericBoundCallAtTuple",
		fix:    "builder: generic bound call at tuple. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.qualCallLowered | false | bound#1",
		reason: "qualified call, Type.method: T.to_string",
		noSkip: true,
		src: `import std/io

fn txt<T>(x: T): String where T: Display {
    T.to_string(x)
}

fn main() {
    io.print(txt((1, "a")))
}
`,
	},
	{
		name:   "gapGenericBoundImplOtherFile",
		fix:    "builder: generic bound impl other file. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.kindImplCall | false | plan == nil#1",
		reason: "qualified call, Type.method: T.to_string",
		noSkip: true,
		src: `// FILE: main.nomi
import {
    std/io
    lib
}

impl Display for lib.Dog {
    fn to_string(d: lib.Dog): String {
        "dog " + d.name
    }
}

fn show<T>(x: T): String where T: Display {
    T.to_string(x)
}

fn main() {
    io.print(show(lib.Dog{name: "d"}))
}
// FILE: lib.nomi
pub struct Dog {
    name: String
}
`,
	},
	{
		name:   "gapGenericCallBareNone",
		fix:    "builder: generic call bare none. Found unreached by the decline inventory (testdata/lowering-declines.txt) at gen.checkedMonoTypeArgsFilled | false | unguarded#1",
		reason: "a direct callee that is not lowerable: is_some",
		noSkip: true,
		src: `import std/io

fn is_some<T>(m: Maybe<T>): Bool {
    case m {
        Some(_) -> True
        None -> False
    }
}

fn main() {
    io.print(is_some(None))
}
`,
	},
	{
		name:   "gapGenericVariantUnsolvedParam",
		fix:    "builder: generic variant unsolved param. Found unreached by the decline inventory (testdata/lowering-declines.txt) at gen.checkedFieldTypeArgs | false | !ok#1",
		reason: "a struct literal: Either2.L",
		noSkip: true,
		src: `import std/io

enum Either2<A, B> {
    L {
        a: A
    }
    R {
        b: B
    }
}

fn main() {
    io.inspect(Either2.L{a: 1})
}
`,
	},
	{
		name:   "gapIterSensitiveFnValue",
		fix:    "builder: iter sensitive fn value. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.funcRef | note | \"an iter-sensitive function passed where its break or continue cannot be caught: \" + t.Nam…#1",
		reason: "an iter-sensitive function passed where its break or continue cannot be caught: stop",
		src: `import std/io

fn stop(x: Int): Int {
    if x > 2 {
        break
    }
    x
}

fn main() {
    xs = [1, 2, 3] |> Iter.map(stop) |> Iter.to_list()
    io.inspect(xs)
    f = stop
    ys = [1, 2, 3] |> Iter.map(f) |> Iter.to_list()
    io.inspect(ys)
}
`,
	},
	{
		name:   "gapLambdaReturnEmptyList",
		fix:    "builder: lambda return empty list. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.lambdaFunction | note | \"a lambda whose returns disagree\"#1",
		reason: "a lambda whose returns disagree",
		src: `import std/io

fn main() {
    a = |x: Int| {
        if x > 0 {
            return []
        }
        [1]
    }
    io.inspect(a(1))
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
		name:   "gapNestedFnEmptyListResult",
		fix:    "builder: nested fn empty list result. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.nestedInstance/no | note | \"a nested fn: \" + why#1",
		reason: "a nested fn: a result that is not the declared return type: List<_> vs List<Int>",
		src: `import std/io

fn main() {
    fn none_list(_x: Int): List<Int> {
        []
    }
    io.inspect(none_list(1))
}
`,
	},
	{
		name:   "gapNestedGenericBoundCallTuple",
		fix:    "builder: nested generic bound call tuple. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.stdKindQualCall | false | base == \"\"#1",
		reason: "qualified call, Type.method: T.to_string",
		noSkip: true,
		src: `import std/io

fn main() {
    fn txt<T>(x: T): String where T: Display {
        T.to_string(x)
    }
    io.print(txt((1, "a")))
}
`,
	},
	{
		name:   "gapPartialCapturedLambda",
		fix:    "builder: partial captured lambda. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.partialApplication | false | !ok ¦¦ len(params) != len(kinds)#1; irScalarBuilder.partialCallee | false | plan == nil ¦¦ plan.source == nil ¦¦ len(plan.names) != len(plan.source.Params)#1",
		reason: "a partial application over a callee whose parameters the builder cannot see, or with a non-literal default",
		src: `import std/io

fn main() {
    f = |a: Int, b: Int| a * b
    g = || f(2, _)
    io.print(g()(3))
}
`,
	},
	{
		name:   "gapPartialOmittedNonliteralDefault",
		fix:    "builder: partial omitted nonliteral default. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.partialApplication | false | unguarded#1; irScalarBuilder.partialApplication/no | note | \"a partial application over a callee whose parameters the builder cannot see, or with a no…#1",
		reason: "a partial application over a callee whose parameters the builder cannot see, or with a non-literal default",
		src: `import std/io

fn base(): Int {
    10
}

fn conn(host: String, port: Int = base() + 1): String {
    "${host}:${port}"
}

fn main() {
    c = conn(_)
    io.print(c("h"))
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
	{
		name:   "gapRecursiveNestedFnEmptyListTail",
		fix:    "builder: recursive nested fn empty list tail. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.recursiveNestedFunc | note | \"a recursive nested fn: its body is outside the lambda shape: \" + fd.Name#1",
		reason: "a recursive nested fn: its body is outside the lambda shape: r",
		src: `import std/io

fn main() {
    fn r(n: Int): List<Int> {
        if n > 0 {
            io.inspect(r(n - 1))
        }
        []
    }
    io.inspect(r(2))
}
`,
	},
	{
		name:   "gapSiblingDefaultFnValue",
		fix:    "builder: sibling default fn value. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.siblingFuncRef | false | !f.lowerable() ¦¦ f.defaults#1",
		reason: "a field access on an unbound name lib: .greet",
		noSkip: true,
		src: `// FILE: main.nomi
import {
    std/io
    lib
}

fn main() {
    f = lib.greet
    io.print(f("a", "?"))
}
// FILE: lib.nomi
pub fn greet(name: String, punct: String = "!"): String {
    name + punct
}
`,
	},
	{
		name:   "gapSiblingDefaultGenericHole",
		fix:    "builder: sibling default generic hole. Found unreached by the decline inventory (testdata/lowering-declines.txt) at gen.irFileSlotRetain | note | \"a default that does not lower\"#1",
		reason: "a direct callee that is not lowerable: is_some",
		noSkip: true,
		src: `// FILE: main.nomi
import {
    std/io
    lib
}

fn main() {
    io.print(lib.show(1))
}
// FILE: lib.nomi
fn is_some<T>(m: Maybe<T>): Bool {
    case m {
        Some(_) -> True
        None -> False
    }
}

pub fn show(x: Int, flag: Bool = is_some(None)): String {
    "${x} ${flag}"
}
`,
	},
	{
		name:   "gapSiblingGenericEmptyList",
		fix:    "builder: sibling generic empty list. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.siblingCall | false | !ok ¦¦ len(params) != len(f.decl.Params) ¦¦ (!irCallableValueKind(result) && result != kin…#1",
		reason: "a direct callee outside this module's fn table: cnt",
		noSkip: true,
		src: `// FILE: main.nomi
import {
    std/io
    lib.{cnt}
}

fn main() {
    io.print(cnt([]))
}
// FILE: lib.nomi
pub fn cnt<T>(xs: List<T>): Int {
    Iter.count(xs)
}
`,
	},
	{
		name:   "gapStdGenericHostFnValue",
		fix:    "builder: std generic host fn value. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.stdFileFuncRef | false | generic == nil && (g.std == nil ¦¦ g.std.byFile[module+\".\"+name] == nil)#1",
		reason: "a field access on an unbound name testing: .check",
		noSkip: true,
		src: `import {
    std/io
    std/testing
}

fn main() {
    c: (Bool) -> Result<Bool, testing.AssertionFailure> = testing.check
    io.inspect(c(True))
}
`,
	},
	{
		name:   "gapTurbofishFileQualified",
		fix:    "builder: turbofish file qualified. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.turbofishDispatch | false | !isType ¦¦ bl.g.fa == nil#1",
		reason: "a turbofish on a call",
		noSkip: true,
		src: `import std/io

fn main() {
    c = io.capture<Int>("", || 1)
    io.inspect(c.value)
}
`,
	},
	{
		name:   "gapTurbofishStdOwner",
		fix:    "builder: turbofish std owner. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.turbofishDispatch | false | !found ¦¦ sym.Kind != analysis.SymbolInterface#1",
		reason: "a turbofish on a call",
		noSkip: true,
		src: `import std/io

fn main() {
    io.inspect(Maybe.collect<Int>([None, None]))
}
`,
	},
	{
		name:   "gapTurbofishTwoTypeArgs",
		fix:    "builder: turbofish two type args. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.turbofishDispatch | false | len(t.TypeArgs) != 1#1",
		reason: "a turbofish on a call",
		noSkip: true,
		src: `import std/io

fn main() {
    io.inspect(Maybe.map<Int, String>(Some(1), |x| "${x}"))
}
`,
	},
	{
		name:   "gapUnitParamGenericFnValue",
		fix:    "builder: unit param generic fn value. Found unreached by the decline inventory (testdata/lowering-declines.txt) at gen.funcTypeKinds | false | !irCallableValueKind(params[i])#1",
		reason: "an ident bound nowhere this builder can see",
		noSkip: true,
		src: `import std/io

fn ident<T>(x: T): T {
    x
}

fn u() {
}

fn main() {
    f: (Unit) -> Unit = ident
    f(u())
    io.print("done")
}
`,
	},
	{
		name:   "gapCrossFileTypeCycle",
		fix:    "builder: cross file type cycle. Found unreached by the decline inventory (testdata/lowering-declines.txt) at typeRegistry.ensureTypes | false | g.typesBusy#1",
		reason: "a struct literal: A",
		noSkip: true,
		src: `// FILE: main.nomi
import {
    std/io
    a
}

fn main() {
    io.print(a.make())
}
// FILE: a.nomi
import b.B

pub struct A {
    name: String
    bs: List<B>
}

pub fn make(): String {
    x = A{name: "a", bs: []}
    x.name
}
// FILE: b.nomi
import a.A

pub struct B {
    n: Int
    parent: Maybe<A>
}
`,
	},
	{
		name:   "gapForeignFnIter",
		fix:    "builder: foreign fn iter. Found unreached by the decline inventory (testdata/lowering-declines.txt) at gen.importKind | false | unguarded#2",
		reason: "qualified call, file.fn: nums.",
		noSkip: true,
		src: `// FILE: main.nomi
import {
    std/io
    nums
}

fn main() {
    io.print("${nums.sum_of([1, 2])}")
}

test "record param" {
    assert nums.px({x: 1, y: 2}) == 1
}
// FILE: nums.nomi
pub fn evens(): Iter<Int> {
    [2, 4, 6]
}

pub fn sum_of(xs: Iter<Int>): Int {
    Iter.reduce(xs, |acc = 0, x| acc + x)
}

pub fn px(p: {x: Int, y: Int}): Int {
    p.x
}
`,
	},
	{
		name:   "gapGoFnDefaultedParam",
		fix:    "builder: go fn defaulted param. Found unreached by the decline inventory (testdata/lowering-declines.txt) at gen.hostFnReject | note | reason#1",
		reason: "a direct callee that is not lowerable: upper",
		noSkip: true,
		src: `import std/io

gopkg "strings" as strs

fn upper(s: String = "abc"): String go strs.ToUpper

fn main() {
    io.print(upper())
}
`,
	},
	{
		name:   "gapImportedVariantUnsolved",
		fix:    "builder: imported variant unsolved. Found unreached by the decline inventory (testdata/lowering-declines.txt) at gen.importedVariantAt | false | !isEnum ¦¦ irUnsolvedType(et)#1",
		reason: "a type name in value position: Empty",
		noSkip: true,
		src: `// FILE: main.nomi
import {
    std/io
    shapes.Holder.{Empty}
}

fn main() {
    io.inspect(Empty)
}
// FILE: shapes.nomi
pub enum Holder<T> {
    Of T
    Empty
}
`,
	},
	{
		name:   "gapSiblingFnReturnsStructWithRecordField",
		fix:    "builder: sibling fn returns struct with record field. Found unreached by the decline inventory (testdata/lowering-declines.txt) at gen.importNamed | false | !d.lowerable#1; gen.importNamed | false | !d.lowerable#2",
		reason: "qualified call, file.fn: shapes.make",
		noSkip: true,
		src: `// FILE: main.nomi
import {
    std/io
    shapes
}

fn main() {
    io.print("${shapes.width(shapes.make())}")
}
// FILE: shapes.nomi
pub struct Box {
    at: {x: Int, y: Int}
}

pub fn make(): Box {
    Box{at: {x: 1, y: 2}}
}

pub fn width(b: Box): Int {
    b.at.x
}
`,
	},
	{
		name:   "gapTypedLiteralFnResult",
		fix:    "builder: typed literal fn result. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.taggedLiteral | false | len(h.params) != 1 ¦¦ !irListTransportKind(h.params[0]) ¦¦ !irRetainedValueKind(h.result)#1",
		reason: "a typed literal: a handler signature outside the retained domain",
		src: `import {
    std/io
    std/literals.{Fragment, Literal}
}

type Adder

impl Literal for Adder {
    fn from_fragments(fragments: List<Fragment<Int>>): (Int) -> Int {
        n = Iter.count(fragments)
        |x| x + n
    }
}

fn main() {
    f = Adder"add"
    io.print("${f(1)}")
}
`,
	},
}

func init() {
	knownLoweringGaps = append(knownLoweringGaps, inventoryLoweringGaps...)
}
