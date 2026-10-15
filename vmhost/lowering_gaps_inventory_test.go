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
		name:   "gapCChunksFnList",
		fix:    "builder: c chunks fn list. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.iterStage | false | !irRetainedListKind(chunk) && !irListTransportKind(chunk)#1",
		reason: "qualified call, Type.method: Iter.chunks",
		noSkip: true,
		src: `import std/io

fn main() {
    fs = [|x: Int| x + 1, |x: Int| x * 2]
    io.inspect(Iter.chunks(fs, 1) |> Iter.count())
}
`,
	},
	{
		name:   "gapCConcatFnList",
		fix:    "builder: c concat fn list. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.iterCall | false | !ok ¦¦ relem != elem#1",
		reason: "qualified call, Type.method: Iter.concat",
		noSkip: true,
		src: `import std/io

fn main() {
    fs = [|x: Int| x + 1]
    io.inspect(Iter.concat(fs, fs) |> Iter.count())
}
`,
	},
	{
		name:   "gapCFlatMapFnList",
		fix:    "builder: c flat map fn list. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.iterStage | false | unguarded#1",
		reason: "qualified call, Type.method: Iter.flat_map",
		noSkip: true,
		src: `import std/io

fn main() {
    fs = [1, 2] |> Iter.flat_map(|x| [|y: Int| y + x]) |> Iter.to_list()
    io.inspect(Iter.count(fs))
}
`,
	},
	{
		name:   "gapCFlatMapString",
		fix:    "builder: c flat map string. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.iterStage | false | !ok ¦¦ r.comp == nil ¦¦ len(r.comp.parts) != 1#1",
		reason: "qualified call, Type.method: Iter.flat_map",
		noSkip: true,
		src: `import std/io

fn main() {
    io.inspect(["ab", "cd"] |> Iter.flat_map(|s| s) |> Iter.to_list())
}
`,
	},
	{
		name:   "gapCFlattenFnLists",
		fix:    "builder: c flatten fn lists. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.iterStage | false | !irRetainedListKind(k) && !irListTransportKind(k)#1",
		reason: "qualified call, Type.method: Iter.flatten",
		noSkip: true,
		src: `import std/io

fn main() {
    fs = [[|y: Int| y + 1], [|y: Int| y]]
    io.inspect(Iter.count(Iter.flatten(fs)))
}
`,
	},
	{
		name:   "gapCFlattenStrings",
		fix:    "builder: c flatten strings. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.iterStage | false | elem.comp == nil ¦¦ len(elem.comp.parts) != 1#1",
		reason: "qualified call, Type.method: Iter.flatten",
		noSkip: true,
		src: `import std/io

fn main() {
    io.inspect(Iter.flatten(["ab", "cd"]))
}
`,
	},
	{
		name:   "gapCGroupByFnList",
		fix:    "builder: c group by fn list. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.iterGroupBy | false | result.tag != tagMap ¦¦ !irRetainedMapKind(result)#1",
		reason: "qualified call, Type.method: Iter.group_by",
		noSkip: true,
		src: `import std/io

fn main() {
    fs = [|x: Int| x + 1, |x: Int| x * 2]
    g = Iter.group_by(fs, |f| f(1))
    io.inspect(Map.keys(g))
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
		name:   "gapCIterParamUserSourceKnownCount",
		fix:    "builder: c iter param user source known count. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.seqView | false | !(it != nil && userElem == elem)#1",
		reason: "a direct call's operand: total",
		noSkip: true,
		src: `import std/io

struct Pair {
    a: Int
    b: Int
}

impl Iter for Pair {
    fn each_while(p: Pair, yield: (Int) -> Bool): Bool {
        if yield(p.a) { yield(p.b) } else { False }
    }

    fn known_count(_p: Pair): Maybe<Int> {
        Some(2)
    }
}

fn total(xs: Iter<Int>): Int {
    Iter.count(xs)
}

fn main() {
    io.inspect(total(Pair{a: 1, b: 2}))
}
`,
	},
	{
		name:   "gapCIterateFn",
		fix:    "builder: c iterate fn. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.iterConstructor | false | len(args.temps) != 2 ¦¦ !irRetainedValueKind(args.kinds[0])#1",
		reason: "qualified call, Type.method: Iter.iterate",
		noSkip: true,
		src: `import std/io

fn main() {
    fs = Iter.iterate(|x: Int| x, |f| |x: Int| f(x) + 1) |> Iter.take(2) |> Iter.to_list()
    io.inspect(Iter.count(fs))
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
		name:   "gapCRangeBool",
		fix:    "builder: c range bool. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.rangeMake | false | !irRetainedRangeKind(k)#1",
		reason: "a RangeLit",
		noSkip: true,
		src: `import std/io

fn main() {
    io.inspect(Range.contains?(False..True, True))
}
`,
	},
	{
		name:   "gapCRangeToString",
		fix:    "builder: c range to string. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.rangeCall | false | unguarded#1",
		reason: "qualified call, Type.method: Range.to_string",
		noSkip: true,
		src: `import std/io

fn main() {
    io.print(Range.to_string(1..4))
}
`,
	},
	{
		name:   "gapCRangeUserDiscrete",
		fix:    "builder: c range user discrete. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.rangeView | false | cmp == nil ¦¦ cmp.irBody == nil ¦¦ !shared#1",
		reason: "qualified call, Type.method: Iter.map",
		noSkip: true,
		src: `import std/io

type Id Int

derive Equatable for Id
derive Comparable for Id

impl Discrete for Id {
    fn next(Id(n)): Maybe<Id> {
        Some(Id(n + 1))
    }

    fn steps_between(Id(a), Id(b)): Maybe<Int> {
        Some(b - a)
    }
}

fn val(Id(n)): Int {
    n
}

fn main() {
    io.inspect(Id(1)..Id(4) |> Iter.map(|i| val(i)) |> Iter.to_list())
}
`,
	},
	{
		name:   "gapCRepeatFn",
		fix:    "builder: c repeat fn. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.iterConstructor | false | len(args.temps) != 1 ¦¦ !irRetainedValueKind(args.kinds[0])#1",
		reason: "qualified call, Type.method: Iter.repeat",
		noSkip: true,
		src: `import std/io

fn main() {
    fs = Iter.repeat(|x: Int| x + 1) |> Iter.take(2) |> Iter.to_list()
    io.inspect(Iter.count(fs))
}
`,
	},
	{
		name:   "gapCToMapFnValues",
		fix:    "builder: c to map fn values. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.iterStage | false | !irRetainedMapKind(k)#1",
		reason: "qualified call, Type.method: Iter.to_map",
		noSkip: true,
		src: `import std/io

fn main() {
    m = Iter.to_map([(1, |x: Int| x)])
    io.inspect(Map.keys(m))
}
`,
	},
	{
		name:   "gapCToSetRanges",
		fix:    "builder: c to set ranges. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.iterStage | false | !irRetainedSetKind(k)#1",
		reason: "qualified call, Type.method: Iter.to_set",
		noSkip: true,
		src: `import std/io

fn main() {
    io.inspect(Iter.to_set([1..2, 3..4]))
}
`,
	},
	{
		name:   "gapCToVectorFn",
		fix:    "builder: c to vector fn. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.iterStage | false | !irRetainedVectorKind(k) && !bl.g.irVectorValueKind(k)#1",
		reason: "qualified call, Type.method: Iter.to_vector",
		noSkip: true,
		src: `import std/io

fn main() {
    fs = [|x: Int| x + 1]
    v = Iter.to_vector(fs)
    io.inspect(Vector.length(v))
}
`,
	},
	{
		name:   "gapCUserSourceKnownCount",
		fix:    "builder: c user source known count. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.iterCall | false | sk.tag != tagList && sk.tag != tagSeq && sk != kindString && sk != irBytesKind() && !isRan…#1",
		reason: "qualified call, Type.method: Iter.map",
		noSkip: true,
		src: `import std/io

struct Pair {
    a: Int
    b: Int
}

impl Iter for Pair {
    fn each_while(p: Pair, yield: (Int) -> Bool): Bool {
        if yield(p.a) { yield(p.b) } else { False }
    }

    fn known_count(_p: Pair): Maybe<Int> {
        Some(2)
    }
}

fn main() {
    io.inspect(Pair{a: 1, b: 2} |> Iter.map(|x| x + 1) |> Iter.to_list())
}
`,
	},
	{
		name:   "gapCZipFnListRight",
		fix:    "builder: c zip fn list right. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.iterPlainView | false | unguarded#1",
		reason: "qualified call, Type.method: Iter.zip",
		noSkip: true,
		src: `import std/io

fn main() {
    fs = [|x: Int| x + 1]
    io.inspect(Iter.zip([1], fs) |> Iter.count())
}
`,
	},
	{
		name:   "gapCZipUserDiscreteRange",
		fix:    "builder: c zip user discrete range. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.iterPlainView | false | sourced#1",
		reason: "qualified call, Type.method: Iter.zip",
		noSkip: true,
		src: `import std/io

type Id Int

derive Equatable for Id
derive Comparable for Id

impl Discrete for Id {
    fn next(Id(n)): Maybe<Id> {
        Some(Id(n + 1))
    }

    fn steps_between(Id(a), Id(b)): Maybe<Int> {
        Some(b - a)
    }
}

fn main() {
    io.inspect(Iter.zip([1, 2], Id(1)..Id(4)) |> Iter.count())
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
		name:   "gapListSpreadFunctionTail",
		fix:    "builder: list spread function tail. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.listMakeOf | false | !ok ¦¦ !(irRetainedListKind(k) ¦¦ irListTransportKind(k))#1",
		reason: "a ListSpreadLit",
		noSkip: true,
		src: `import std/io

fn main() {
    fs = [|x: Int| x + 1]
    gs = [|x: Int| x * 2, ..fs]
    io.print(Iter.count(gs))
}
`,
	},
	{
		name:   "gapMapOfFunctions",
		fix:    "builder: map of functions. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.mapEntriesOver | false | !joined ¦¦ (j == 0 && !irMapKeyKind(k) && !bl.g.irStructEqualityKind(k) && !bl.g.irStructu…#1",
		reason: "a MapLit",
		noSkip: true,
		src: `import std/io

fn main() {
    ops = {"inc" => |x: Int| x + 1}
    case Map.get(ops, "inc") {
        Some(f) -> io.print(f(1))
        None -> io.print(0)
    }
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
		name:   "gapVectorOfFunctions",
		fix:    "builder: vector of functions. Found unreached by the decline inventory (testdata/lowering-declines.txt) at irScalarBuilder.lowerSameKindItems | false | !joined ¦¦ !accept(elem) ¦¦ !bl.widenEmbedded(typedItems, typedValues, typedKinds, elem)#1",
		reason: "a VectorLit",
		noSkip: true,
		src: `import std/io

fn main() {
    fs = #[|x: Int| x + 1, |x: Int| x * 2]
    case Vector.at(fs, 1) {
        Some(f) -> io.print(f(5))
        None -> io.print(0)
    }
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
