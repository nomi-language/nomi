package vmhost

import (
	"strings"
	"testing"
)

// declineReproducers are the reproducers of lowering gaps that are fixed and
// whose decline sites testdata/lowering-declines.txt still names, written in
// today's syntax. Each lowers now without reaching its site; holding them to
// it keeps the fixes, and a builder change that sends one back to its site
// shows in the decline inventory, whose vmhost suite runs
// TestDeclineReproducersLower (internal/declinesites).
var declineReproducers = []struct{ name, src string }{
	{
		name: "gapAssertPatternAnnotatedPipe",
		src: `test "assert pattern over an annotated pipe binding"
    xs: List<Int> = [1, 2] |> Iter.map(fn x -> x + 1) |> Iter.to_list()
    assert [2, _] = xs
end
`,
	},
	{
		name: "gapAssertTupleCompositeDistinct",
		src: `type Pair (Int, String)

test "assert a tuple pattern holding a tuple distinct"
    t = (Pair(1, "a"), 2)
    assert (Pair(x, _), 2) = t
    assert x == 1
end
`,
	},
	{
		name: "gapBindingElseTupleCompositeDistinct",
		src: `import std::io

type Pair (Int, String)

fn f(t: (Pair, Int)) -> Int
    (Pair(x, _), 2) = t else
        return 0
    end
    x
end

fn main()
    io.inspect(f((Pair(1, "a"), 2)))
end
`,
	},
	{
		name: "gapBindingTupleCompositeDistinct",
		src: `import std::io

type Pair (Int, String)

fn f(t: (Pair, Int)) -> Int
    (Pair(x, _), y) = t
    x + y
end

fn main()
    io.inspect(f((Pair(1, "a"), 2)))
end
`,
	},
	{
		name: "gapCChunksFnList",
		src: `import std::io

fn main()
    fs = [fn x: Int -> x + 1, fn x: Int -> x * 2]

    io.inspect(
        Iter.chunks(fs, 1)
        |> Iter.count()
    )
end
`,
	},
	{
		name: "gapCConcatFnList",
		src: `import std::io

fn main()
    fs = [fn x: Int -> x + 1]

    io.inspect(
        Iter.concat(fs, fs)
        |> Iter.count()
    )
end
`,
	},
	{
		name: "gapCFlatMapFnList",
		src: `import std::io

fn main()
    fs = [1, 2] |> Iter.flat_map(fn x -> [fn y: Int -> y + x]) |> Iter.to_list()
    io.inspect(Iter.count(fs))
end
`,
	},
	{
		name: "gapCFlattenFnLists",
		src: `import std::io

fn main()
    fs = [[fn y: Int -> y + 1], [fn y: Int -> y]]
    io.inspect(Iter.count(Iter.flatten(fs)))
end
`,
	},
	{
		name: "gapCGroupByFnList",
		src: `import std::io

fn main()
    fs = [fn x: Int -> x + 1, fn x: Int -> x * 2]
    g = Iter.group_by(fs, fn f -> f(1))
    io.inspect(Map.keys(g))
end
`,
	},
	{
		name: "gapCIterParamEmptySet",
		src: `import std::io

fn total(xs: Iter<Int>) -> Int
    Iter.count(xs)
end

fn main()
    io.inspect(total(Set[]))
end
`,
	},
	{
		name: "gapCIterParamUserSourceKnownCount",
		src: `import std::io

struct Pair
    a: Int
    b: Int
end

impl Iter for Pair
    fn each_while(p: Pair, yield: (Int) -> Bool) -> Bool
        if yield(p.a) then yield(p.b) else False
    end

    fn known_count(_p: Pair) -> Maybe<Int>
        Some(2)
    end
end

fn total(xs: Iter<Int>) -> Int
    Iter.count(xs)
end

fn main()
    io.inspect(total(Pair{a: 1, b: 2}))
end
`,
	},
	{
		name: "gapCLoopFinalBreakEmptyList",
		src: `import std::io

fn main()
    xs = Iter.loop([1], fn acc: List<Int> ->
        if Iter.count(acc) > 2 then break acc
        break []
    end)

    io.inspect(xs)
end
`,
	},
	{
		name: "gapCLoopFnState",
		src: `import std::io

fn main()
    f = Iter.loop(fn x: Int -> x, fn g ->
        if g(0) > 3 then break g
        fn x: Int -> g(x) + 1
    end)

    io.inspect(f(1))
end
`,
	},
	{
		name: "gapCLoopGuardBreakEmptyList",
		src: `import std::io

fn main()
    xs = Iter.loop([1], fn acc: List<Int> ->
        if Iter.count(acc) > 2 then break []
        [1, ..acc]
    end)

    io.inspect(xs)
end
`,
	},
	{
		name: "gapCLoopGuardReturnEmptyList",
		src: `import std::io

fn main()
    xs = Iter.loop([1], fn acc: List<Int> ->
        if Iter.count(acc) > 2 then break acc
        if Iter.count(acc) == 0 then return []
        [1, ..acc]
    end)

    io.inspect(xs)
end
`,
	},
	{
		name: "gapCLoopIfElseBreakEmptyList",
		src: `import std::io

fn main()
    xs = Iter.loop([1], fn acc: List<Int> ->
        if Iter.count(acc) > 2 then
            break []
        else
            [1, ..acc]
        end
    end)

    io.inspect(xs)
end
`,
	},
	{
		name: "gapCLoopIfLetTail",
		src: `import std::io

fn main()
    m: Maybe<Int> = Some(3)

    n = Iter.loop(0, fn n ->
        if n > 3 then break n
        if Some(v) = m then n + v else n + 1
    end)

    io.inspect(n)
end
`,
	},
	{
		name: "gapCLoopTailEmptyList",
		src: `import std::io

fn main()
    xs = Iter.loop([1], fn acc: List<Int> ->
        if Iter.count(acc) > 2 then break acc
        []
    end)

    io.inspect(xs)
end
`,
	},
	{
		name: "gapCToMapFnValues",
		src: `import std::io

fn main()
    m = Iter.to_map([(1, fn x: Int -> x)])
    io.inspect(Map.keys(m))
end
`,
	},
	{
		name: "gapCUserSourceKnownCount",
		src: `import std::io

struct Pair
    a: Int
    b: Int
end

impl Iter for Pair
    fn each_while(p: Pair, yield: (Int) -> Bool) -> Bool
        if yield(p.a) then yield(p.b) else False
    end

    fn known_count(_p: Pair) -> Maybe<Int>
        Some(2)
    end
end

fn main()
    io.inspect(
        Pair{a: 1, b: 2}
        |> Iter.map(fn x -> x + 1)
        |> Iter.to_list()
    )
end
`,
	},
	{
		name: "gapCZipFnListRight",
		src: `import std::io

fn main()
    fs = [fn x: Int -> x + 1]

    io.inspect(
        Iter.zip([1], fs)
        |> Iter.count()
    )
end
`,
	},
	{
		name: "gapCZipUserDiscreteRange",
		src: `import std::io

type Id Int

derive Equatable for Id
derive Comparable for Id

impl Discrete for Id
    fn next(Id(n)) -> Maybe<Id>
        Some(Id(n + 1))
    end

    fn steps_between(Id(a), Id(b)) -> Maybe<Int>
        Some(b - a)
    end
end

fn main()
    io.inspect(
        Iter.zip([1, 2], Id(1)..Id(4))
        |> Iter.count()
    )
end
`,
	},
	{
		name: "gapCapturedDefaultLambdaCall",
		src: `import std::io

fn main()
    f = fn x: Int, y: Int = 2 -> x + y
    g = fn -> f(1)
    io.print(Int.to_string(g()))
end
`,
	},
	{
		name: "gapCapturedLambdaNamedArgs",
		src: `import std::io

fn main()
    f = fn x: Int, y: Int -> x - y
    g = fn -> f(y: 1, x: 5)
    io.print(Int.to_string(g()))
end
`,
	},
	{
		name: "gapContextValueQualifiedWitness",
		src: `// FILE: main.nomi
import
    std::io
    domain
end

fn main()
    c = Context.with_value(Context.root(), domain.Token(7))
    io.inspect(Context.value(c, domain.Token))
end
// FILE: domain.nomi
pub type Token Int
`,
	},
	{
		name: "gapDistinctTupleCallformIterWiden",
		src: `import std::io

struct Circle
    r: Int
end

enum Shape
    embeds Circle
    Dot
end

type P (Iter<Shape>, Int)

fn main()
    c = Circle{r: 1}
    q = P(([c], 2))
    P(it, n) = q
    io.print(Int.to_string(Iter.count(it) + n))
end
`,
	},
	{
		name: "gapElseFallbackTupleCompositeDistinct",
		src: `import std::io

type Pair (Int, String)

fn f(m: Maybe<(Pair, Int)>) -> Int
    Some((Pair(x, _), y)) = m else
        (Pair(0, ""), 0)
    end
    x + y
end

fn main()
    io.inspect(f(None))
end
`,
	},
	{
		name: "gapEmbeddedStructFieldCompositeDistinct",
		src: `import std::io

type Pair (Int, String)

struct Circle
    p: Pair
end

enum E
    embeds Circle
    Other
end

fn f(e: E) -> Int
    case e
        .Circle{p: Pair(a, _)} -> a
        _ -> 0
    end
end

fn main()
    io.inspect(f(Circle{p: Pair(1, "a")}))
end
`,
	},
	{
		name: "gapFieldAccessorRecordExistential",
		src: `import std::io

fn main()
    d: Display = 1
    rs = [{x: 1, d:}]

    io.inspect(
        rs
        |> Iter.map(.x)
        |> Iter.to_list()
    )
end
`,
	},
	{
		name: "gapFieldDefaultIterWiden",
		src: `import std::io

struct Circle
    r: Int
end

enum Shape
    embeds Circle
    Dot
end

struct H
    it: Iter<Shape> = [Circle{r: 1}]
end

fn main()
    h = H{}
    io.print(Int.to_string(Iter.count(h.it)))
end
`,
	},
	{
		name: "gapForeignFnIter",
		src: `// FILE: main.nomi
import
    std::io
    nums
end

fn main()
    io.print("${nums.sum_of([1, 2])}")
end

test "record param"
    assert nums.px({x: 1, y: 2}) == 1
end
// FILE: nums.nomi
pub fn evens() -> Iter<Int>
    [2, 4, 6]
end

pub fn sum_of(xs: Iter<Int>) -> Int
    Iter.reduce(xs, 0, fn acc, x -> acc + x)
end

pub fn px(p: {x: Int, y: Int}) -> Int
    p.x
end
`,
	},
	{
		name: "gapGenericBoundImplOtherFile",
		src: `// FILE: main.nomi
import
    std::io
    lib
end

impl Display for lib.Dog
    fn to_string(d: lib.Dog) -> String
        "dog " + d.name
    end
end

fn show<T>(x: T) -> String where T: Display
    T.to_string(x)
end

fn main()
    io.print(show(lib.Dog{name: "d"}))
end
// FILE: lib.nomi
pub struct Dog
    name: String
end
`,
	},
	{
		name: "gapGenericVariantUnsolvedParam",
		src: `import std::io

enum Either2<A, B>
    L {a: A}
    R {b: B}
end

fn main()
    io.inspect(Either2.L{a: 1})
end
`,
	},
	{
		name: "gapGoFnDefaultedParam",
		src: `import std::io

gopkg "strings" as strs

fn upper(s: String = "abc") -> String go strs.ToUpper

fn main()
    io.print(upper())
end
`,
	},
	{
		name: "gapInterfaceInFnBody",
		src: `import std::io

fn c(x: Int) -> Int
    interface Foo
        fn foo(v: self) -> Int
    end

    x
end

fn main()
    io.inspect(c(1))
end
`,
	},
	{
		name: "gapLambdaReturnEmptyList",
		src: `import std::io

fn main()
    a = fn x: Int ->
        if x > 0 then return []
        [1]
    end

    io.inspect(a(1))
end
`,
	},
	{
		name: "gapLambdaReturnEmptyListFirst",
		src: `import std::io

fn main()
    g = fn x: Int ->
        if x > 0 then return []
        if x < 0 then return [x]
        [x, x]
    end

    io.inspect(g(1))
end
`,
	},
	{
		name: "gapListDistinctUntypedListPattern",
		src: `import std::io

type Stack List<Int>

fn top(s: Stack) -> Int
    case s
        [a, ..] -> a
        _ -> 0
    end
end

fn main()
    io.inspect(top(Stack([1, 2])))
end
`,
	},
	{
		name: "gapLoopArmReturnEmpty",
		src: `import std::io

fn main()
    r = Iter.loop([1, 2, 3], fn xs ->
        case xs
            [] -> break [0]
            [_] -> []
            [_, ..rest] -> rest
        end
    end)

    io.inspect(r)
end
`,
	},
	{
		name: "gapLoopElseFallbackIf",
		src: `import
    std::io
    std::iter.Iter.loop
end

fn find(n: Int) -> Maybe<Int>
    if n > 3 then None else Some(n)
end

fn main()
    r = loop(0, fn n ->
        Some(v) = find(n) else
            if n > 5 then 10 else 20
        end
        if v > 2 then break v
        n + 1
    end)

    io.inspect(r)
end
`,
	},
	{
		name: "gapLoopElseIfBothBreak",
		src: `import
    std::io
    std::iter.Iter.loop
end

fn find(n: Int) -> Maybe<Maybe<Int>>
    if n > 3 then None else Some(Some(n))
end

fn main()
    r = loop(0, fn n ->
        Some(Some(v)) = find(n) else
            if n > 10 then break 1 else break 2
        end
        v + n + 1
    end)

    io.inspect(r)
end
`,
	},
	{
		name: "gapLoopNestedGuardReturnEmpty",
		src: `import std::io

fn main()
    r = Iter.loop([1, 2, 3], fn xs ->
        case xs
            [] ->
                break [0]

            [_, ..rest] ->
                if rest == [1] then return []
                rest
        end
    end)

    io.inspect(r)
end
`,
	},
	{
		name: "gapMapBreakEmptyListFirst",
		src: `import std::io

fn main()
    ys =
        [1, 2, 3]
        |> Iter.map(fn x ->
            if x > 2 then break []
            if x > 1 then break [x]
            [x, x]
        end)
        |> Iter.to_list()

    io.inspect(ys)
end
`,
	},
	{
		name: "gapMapOfFunctions",
		src: `import std::io

fn main()
    ops = {"inc" => fn x: Int -> x + 1}

    case Map.get(ops, "inc")
        Some(f) -> io.inspect(f(1))
        None -> io.inspect(0)
    end
end
`,
	},
	{
		name: "gapMapValueLiteralAttachDistinct",
		src: `import std::io

type Kvs Map<String, Int>

fn f(m: Map<String, Kvs>) -> Int
    case m
        {"k" => Kvs{"a" => v}} -> v
        _ -> 0
    end
end

fn main()
    io.inspect(f({"k" => Kvs{"a" => 1}}))
end
`,
	},
	{
		name: "gapNamedGapGenericParam",
		src: `import std::io

struct Box<T>
    v: T
end

impl Box<T>
    fn pick<T>(_b: Box<T>, other: Maybe<T> = None, n: Int = 0) -> Int
        case other
            Some(_) -> n + 1
            None -> n
        end
    end
end

fn main()
    io.inspect(Box.pick(Box{v: 1}, n: 2))
end
`,
	},
	{
		name: "gapNestedGenericBoundCallTuple",
		src: `import std::io

fn main()
    fn txt<T>(x: T) -> String where T: Display
        T.to_string(x)
    end

    io.print(txt((1, "a")))
end
`,
	},
	{
		name: "gapPartialCapturedLambda",
		src: `import std::io

fn main()
    f = fn a: Int, b: Int -> a * b
    g = fn -> &f(2, &1)
    io.inspect(g()(3))
end
`,
	},
	{
		name: "gapPartialOmittedNonliteralDefault",
		src: `import std::io

fn base() -> Int
    10
end

fn conn(host: String, port: Int = base() + 1) -> String
    "${host}:${port}"
end

fn main()
    c = &conn(&1)
    io.print(c("h"))
end
`,
	},
	{
		name: "gapRecordFieldLiteralAttachDistinct",
		src: `import std::io

type Stack List<Int>

fn main()
    c = {r: 1, q: Stack([1])}

    n = case c
        {q: Stack[a, ..]} -> a
        _ -> 0
    end

    io.inspect(n)
end
`,
	},
	{
		name: "gapRecursiveNestedFnEmptyListTail",
		src: `import std::io

fn main()
    fn r(n: Int) -> List<Int>
        if n > 0 then io.inspect(r(n - 1))
        []
    end

    io.inspect(r(2))
end
`,
	},
	{
		name: "gapSetupLeavingBinding",
		src: `fn id(n: Int) -> Int
    n
end

group "a setup binding whose every arm returns"
    setup begin
        v = case id(1)
            1 -> return 5
            _ -> return 6
        end

        v
    end

    test "v", v
        assert v == 5
    end
end
`,
	},
	{
		name: "gapShadowedStdInterfaceThroughItsModule",
		src: `import
    std::calendar
    std::calendar.Date
    std::io
end

struct DateParts
    x: Int
end

fn main()
    d = Date"2024-03-05"
    io.print(Int.to_string(calendar.DateParts.year(d)))
    io.print(Int.to_string(DateParts{x: 1}.x))
end
`,
	},
	{
		name: "gapSiblingDefaultGenericHole",
		src: `// FILE: main.nomi
import
    std::io
    lib
end

fn main()
    io.print(lib.show(1))
end
// FILE: lib.nomi
fn is_some<T>(m: Maybe<T>) -> Bool
    case m
        Some(_) -> True
        None -> False
    end
end

pub fn show(x: Int, flag: Bool = is_some(None)) -> String
    "${x} ${flag}"
end
`,
	},
	{
		name: "gapSiblingFnReturnsStructWithRecordField",
		src: `// FILE: main.nomi
import
    std::io
    shapes
end

fn main()
    io.print("${shapes.width(shapes.make())}")
end
// FILE: shapes.nomi
pub struct Box
    at: {x: Int, y: Int}
end

pub fn make() -> Box
    Box{at: {x: 1, y: 2}}
end

pub fn width(b: Box) -> Int
    b.at.x
end
`,
	},
	{
		name: "gapSiblingGenericEmptyList",
		src: `// FILE: main.nomi
import
    std::io
    lib.cnt
end

fn main()
    io.inspect(cnt([]))
end
// FILE: lib.nomi
pub fn cnt<T>(xs: List<T>) -> Int
    Iter.count(xs)
end
`,
	},
	{
		name: "gapStructSpreadIterFieldWiden",
		src: `import std::io

struct Circle
    r: Int
end

enum Shape
    embeds Circle
    Dot
end

struct H
    it: Iter<Shape>
    n: Int
end

fn main()
    c = Circle{r: 1}
    h = H{it: [Shape.Dot], n: 1}
    h2 = {..h, it: [c]}
    io.inspect(Iter.count(h2.it))
end
`,
	},
	{
		name: "gapStructUpdateNamedIterFieldWiden",
		src: `import std::io

struct Circle
    r: Int
end

enum Shape
    embeds Circle
    Dot
end

struct H
    it: Iter<Shape>
    n: Int
end

fn main()
    c = Circle{r: 1}
    h = H{it: [Shape.Dot], n: 1}
    h2 = Struct.update(updates: {it: [c]}, original: h)
    io.inspect(Iter.count(h2.it))
end
`,
	},
	{
		name: "gapStructVariantFieldCompositeDistinct",
		src: `import std::io

type Pair (Int, String)

enum E
    Rec {p: Pair}
    Other
end

fn f(e: E) -> Int
    case e
        .Rec{p: Pair(a, _)} -> a
        _ -> 0
    end
end

fn main()
    io.inspect(f(E.Rec{p: Pair(1, "a")}))
end
`,
	},
	{
		name: "gapStructVariantPatternAliasQualifier",
		src: `import std::io

enum Shape
    Rec {
        w: Int,
        h: Int,
    }
    Dot
end

typealias S Shape

fn f(s: Shape) -> Int
    case s
        S.Rec{w:} -> w
        _ -> 0
    end
end

fn main()
    io.inspect(f(Shape.Rec{w: 2, h: 3}))
end
`,
	},
	{
		name: "gapStructVariantRecordPayloadPattern",
		src: `import std::io

enum Shape
    Rec {
        w: Int,
        h: Int,
    }
    Dot
end

fn f(s: Shape) -> Int
    case s
        .Rec({w:, h:}) -> w + h
        _ -> 0
    end
end

fn main()
    io.inspect(f(Shape.Rec{w: 2, h: 3}))
end
`,
	},
	{
		name: "gapSupervisorSpawnAllNamedArg",
		src: `import
    std::io
    std::supervisors.Supervisor
end

struct App
    context: Context
    work: Supervisor
end

fn boot(_startup: Startup) -> Result<App, String>
    Ok(App{context: Context.root(), work: Supervisor.new(2)})
end

fn main()
    _ = Supervisor.spawn_all(App.work, [1, 2], f: fn n: Int -> io.inspect(n))
end
`,
	},
	{
		name: "gapSupervisorSpawnNamedArg",
		src: `import
    std::io
    std::supervisors.Supervisor
end

struct App
    context: Context
    work: Supervisor
end

fn boot(_startup: Startup) -> Result<App, String>
    Ok(App{context: Context.root(), work: Supervisor.new(2)})
end

fn main()
    _ = Supervisor.spawn(App.work, body: fn -> io.print("job"))
end
`,
	},
	{
		name: "gapTaskAwaitNamedArg",
		src: `import
    std::io
    std::tasks.Task
end

fn main()
    n = concurrent
        t = Task.spawn(fn -> 3)
        Task.await(task: t)
    end

    io.inspect(n)
end
`,
	},
	{
		name: "gapTaskSpawnAllVector",
		src: `import
    std::io
    std::tasks.Task
end

fn double(n: Int) -> Int
    n * 2
end

fn main()
    xs = concurrent
        ts = Task.spawn_all(
            [1, 2, 3]
            |> Iter.to_vector(),
            double,
            max_running: 2,
        )

        Task.await_all(ts)
    end

    io.inspect(xs)
end
`,
	},
	{
		name: "gapTestAssertionBindingRebind",
		src: `fn double(n: Int) -> Int
    n * 2
end

test "an assertion binding that rebinds a name"
    v = 1
    v = assert double(v) == 2
    assert v
end
`,
	},
	{
		name: "gapTestPipeBindingNestedPipe",
		src: `fn double(n: Int) -> Int
    n * 2
end

test "a pipe binding whose stage holds a pipe"
    ok = [1, 2] |> Iter.any_match(fn x: Int -> x |> double() > 2)
    assert ok
end
`,
	},
	{
		name: "gapTestingCheckNestedPipe",
		src: `import std::testing

fn double(n: Int) -> Int
    n * 2
end

test "testing.check over a pipe whose stage holds a pipe"
    assert testing.check(
        [1, 2]
        |> Iter.any_match(fn x: Int -> x |> double() > 2)
    )
end
`,
	},
	{
		name: "gapTupleDestructureRebindsFunction",
		src: `import std::io

fn main()
    f = fn x: Int -> x
    io.inspect(f(1))
    (f, n) = (fn x: Int -> x + 1, 1)
    io.inspect(f(n))
end
`,
	},
	{
		name: "gapTupleEmptyListEquality",
		src: `import std::io

fn main()
    io.inspect((1, []) == (1, [2]))
end
`,
	},
	{
		name: "gapTuplePatternCompositeDistinct",
		src: `import std::io

type Pair (Int, String)

fn f(t: (Pair, Int)) -> Int
    case t
        (Pair(1, _), 2) -> 1
        _ -> 0
    end
end

fn main()
    io.inspect(f((Pair(1, "a"), 2)))
end
`,
	},
	{
		name: "gapTurbofishTwoTypeArgs",
		src: `import std::io

fn main()
    io.inspect(Maybe.map<Int, String>(Some(1), fn x -> "${x}"))
end
`,
	},
	{
		name: "gapUpdateBaseDeclines",
		src: `import std::io

struct P
    x: Int
end

fn pick(b: Bool) -> Bool
    b
end

fn main()
    p = P{x: 1}
    q = Struct.update(if pick(True) then p else p, {x: 2})
    io.inspect(q.x)
    r = Struct.update(updates: {x: 3}, original: if pick(False) then p else p)
    io.inspect(r.x)
end
`,
	},
	{
		name: "gapVariantPatternAliasQualifier",
		src: `import std::io

enum Shape
    Num Int
    Dot
end

typealias S Shape

fn f(s: Shape) -> Int
    case s
        S.Num(n) -> n
        S.Dot -> 0
    end
end

fn main()
    io.inspect(f(Shape.Num(2)))
end
`,
	},
	{
		name: "gapVariantPayloadAsCompositeDistinct",
		src: `import std::io

type Pair (Int, String)

enum E
    Dist Pair
    Other
end

fn f(e: E) -> Int
    case e
        .Dist(Pair(a, _) as _whole) -> a
        _ -> 0
    end
end

fn main()
    io.inspect(f(E.Dist(Pair(1, "a"))))
end
`,
	},
	{
		name: "gapVariantPayloadTupleCompositeDistinct",
		src: `import std::io

type Pair (Int, String)

enum E
    TP (Pair, Int)
    Other
end

fn f(e: E) -> Int
    case e
        .TP((Pair(a, _), _)) -> a
        _ -> 0
    end
end

fn main()
    io.inspect(f(E.TP((Pair(1, "a"), 2))))
end
`,
	},
	{
		name: "gapVectorCapturedElemWiden",
		src: `import
    std::io
    std::io.Captured
end

struct Circle
    r: Int
end

enum Shape
    embeds Circle
    Dot
end

fn main()
    c = Circle{r: 1}
    v: Vector<Captured<Shape>> = Vector[Captured{value: c, output: "", errors: "", transcript: ""}]
    io.inspect(Vector.length(v))
end
`,
	},
	{
		name: "gapVectorItemsJoinCaptured",
		src: `import
    std::io
    std::io.Captured
end

struct Circle
    r: Int
end

enum Shape
    embeds Circle
    Dot
end

fn main()
    a = Captured{value: Circle{r: 1}, output: "", errors: "", transcript: ""}
    b = Captured{value: Shape.Dot, output: "", errors: "", transcript: ""}
    v = Vector[a, b]
    io.inspect(Vector.length(v))
end
`,
	},
	{
		name: "gapVectorOfFunctions",
		src: `import std::io

fn main()
    fs = Vector[fn x: Int -> x + 1, fn x: Int -> x * 2]

    case Vector.get(fs, 1)
        Some(f) -> io.inspect(f(5))
        None -> io.inspect(0)
    end
end
`,
	},
	{
		name: "gapTupleOfMarker",
		src: `import std::channels.ChannelClosed

test "tuple"
    t = (ChannelClosed, 1)
    assert t.second == 1
end
`,
	},
	{
		name: "gapRecordOfMarker",
		src: `import std::channels.ChannelClosed

test "record"
    r = {e: ChannelClosed, n: 1}
    assert r.n == 1
end
`,
	},
	{
		name: "gapWithIndexOfMarkers",
		src: `import std::channels.ChannelClosed

test "with_index"
    assert Iter.with_index([ChannelClosed]) |> Iter.count() == 1
end
`,
	},
	{
		name: "gapZipOfMarkers",
		src: `import std::channels.ChannelClosed

test "zip"
    assert Iter.zip([ChannelClosed], [1]) |> Iter.count() == 1
end
`,
	},
	{
		name: "gapToSetOfMarkers",
		src: `import std::channels.ChannelClosed

test "to_set"
    assert Iter.to_set([ChannelClosed]) |> Iter.count() == 1
end
`,
	},
	{
		name: "gapMarkerPayloadPattern",
		src: `import std::channels.{Channel, ChannelClosed, Sender}

test "marker payload"
    ch = Channel.buffered<Int>(1)
    Sender.close(ch.sender)
    n = case Sender.send(ch.sender, 1)
        Err(ChannelClosed) -> 1
        Ok(_) -> 0
    end
    assert n == 1
end
`,
	},
	{
		name: "gapChunksOfIters",
		src: `test "chunks"
    seqs = [Iter.map([1, 2], fn n -> n), Iter.map([3], fn n -> n)]
    assert Iter.chunks(seqs, 1) |> Iter.count() == 2
end
`,
	},
	{
		name: "gapFlattenListsOfIters",
		src: `test "flatten"
    seqs = [Iter.map([1, 2], fn n -> n), Iter.map([3], fn n -> n)]
    assert Iter.flatten([seqs]) |> Iter.count() == 2
end
`,
	},
	{
		name: "gapBlockImportRebindsModuleAlias",
		src: `import std::strings as text

test "block alias"
    import std::io as text
    assert text.print("hi") == Unit
end
`,
	},
	{
		name: "gapBlockAliasQualifiedType",
		src: `import std::csv as fmt

test "block alias type"
    import std::xml as fmt

    o = fmt.WriteOptions{}
    assert o == o
end
`,
	},
	{
		name: "gapBareCallToOwnFunctionWithItsOwnTypeParams",
		src: `enum Tree<K>
    Leaf
    Node (Tree<K>, K, Tree<K>)
end

impl Tree<K>
    fn has<K>(tree: Tree<K>, key: K) -> Bool where K: Comparable
        case tree
            .Leaf -> False
            .Node(left, k, right) ->
                case Comparable.compare(key, k)
                    .Less -> has(left, key)
                    .Greater -> has(right, key)
                    .Equal -> True
                end
        end
    end
end

test "a bare self-call in a function that redeclares the block's parameter"
    assert Tree.has(Tree.Node(.Leaf, 2, .Leaf), 2)
end
`,
	},
	{
		name: "gapRecordFieldListIntoIter",
		src: `import
    std::io
end

fn main()
    r: {b: Iter<String>} = {b: ["x"]}
    nested: {a: {b: Iter<Int>}} = {a: {b: [1, 2]}}
    io.print("${r.b |> Iter.count()} ${nested.a.b |> Iter.count()}")
end
`,
	},
	{
		name: "gapAsPatternOverAStructVariant",
		src: `enum Shape
    Dot
    Box{w: Int, h: Int}
end

fn describe(s: Shape) -> Int
    case s
        .Box({w: 1, h:} as r) -> h + r.w
        .Box(({w: 2, h: _} or {w: 3, h: _}) as r) -> r.h
        .Box(r) -> r.w
        .Dot -> 0
    end
end

test "an as pattern over a struct-shaped variant"
    assert describe(.Box{w: 1, h: 5}) == 6
    assert describe(.Box{w: 3, h: 7}) == 7
    assert describe(.Box{w: 9, h: 7}) == 9
end
`,
	},
	{
		name: "gapListPatternOverAPayloadListOfIters",
		src: `enum E
    Its(List<Iter<Int>>)
    Other
end

fn f(e: E) -> Int
    case e
        .Its([it, ..]) -> Iter.count(it)
        _ -> 0
    end
end

test "a list pattern over a payload List<Iter<Int>>"
    assert f(.Its([[1, 2]])) == 2
    assert f(.Other) == 0
end
`,
	},
}

// TestDeclineReproducersLower holds each of declineReproducers to the rule
// that a program the front end accepts lowers.
func TestDeclineReproducersLower(t *testing.T) {
	t.Setenv("NOMI_DEBUG_LOWERING", "1")
	for _, r := range declineReproducers {
		t.Run(strings.TrimPrefix(r.name, "gap"), func(t *testing.T) {
			o := checkLowers(t.TempDir(), r.src)
			if !o.accepted {
				t.Fatalf("the front end rejects this reproducer now:\n%s\n%s", r.src, o.rejection)
			}
			if o.failed() {
				t.Fatal(o.describe(r.src))
			}
		})
	}
}
