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
		src: `test "assert pattern over an annotated pipe binding" {
    xs: List<Int> = [1, 2] |> Iter.map(fn x -> x + 1) |> Iter.to_list()
    assert [2, _] = xs
}
`,
	},
	{
		name: "gapAssertTupleCompositeDistinct",
		src: `type Pair #(Int, String)

test "assert a tuple pattern holding a tuple distinct" {
    t = #(#Pair(1, "a"), 2)
    assert #(#Pair(x, _), 2) = t
    assert x == 1
}
`,
	},
	{
		name: "gapBindingElseTupleCompositeDistinct",
		src: `import std/io

type Pair #(Int, String)

fn f(t: #(Pair, Int)) -> Int {
    #(#Pair(x, _), 2) = t else { return 0 }
    x
}

fn main() {
    io.inspect(f(#(#Pair(1, "a"), 2)))
}
`,
	},
	{
		name: "gapBindingTupleCompositeDistinct",
		src: `import std/io

type Pair #(Int, String)

fn f(t: #(Pair, Int)) -> Int {
    #(#Pair(x, _), y) = t
    x + y
}

fn main() {
    io.inspect(f(#(#Pair(1, "a"), 2)))
}
`,
	},
	{
		name: "gapCChunksFnList",
		src: `import std/io

fn main() {
    fs = [fn x: Int -> x + 1, fn x: Int -> x * 2]

    io.inspect(
        Iter.chunks(fs, 1)
        |> Iter.count()
    )
}
`,
	},
	{
		name: "gapCConcatFnList",
		src: `import std/io

fn main() {
    fs = [fn x: Int -> x + 1]

    io.inspect(
        Iter.concat(fs, fs)
        |> Iter.count()
    )
}
`,
	},
	{
		name: "gapCFlatMapFnList",
		src: `import std/io

fn main() {
    fs = [1, 2] |> Iter.flat_map(fn x -> [fn y: Int -> y + x]) |> Iter.to_list()
    io.inspect(Iter.count(fs))
}
`,
	},
	{
		name: "gapCFlattenFnLists",
		src: `import std/io

fn main() {
    fs = [[fn y: Int -> y + 1], [fn y: Int -> y]]
    io.inspect(Iter.count(Iter.flatten(fs)))
}
`,
	},
	{
		name: "gapCGroupByFnList",
		src: `import std/io

fn main() {
    fs = [fn x: Int -> x + 1, fn x: Int -> x * 2]
    g = Iter.group_by(fs, fn f -> f(1))
    io.inspect(Map.keys(g))
}
`,
	},
	{
		name: "gapCIterParamEmptySet",
		src: `import std/io

fn total(xs: Iter<Int>) -> Int {
    Iter.count(xs)
}

fn main() {
    io.inspect(total(%[]))
}
`,
	},
	{
		name: "gapCIterParamUserSourceKnownCount",
		src: `import std/io

struct Pair {
    a: Int
    b: Int
}

impl Iter for Pair {
    fn each_while(p: Pair, yield: (Int) -> Bool) -> Bool {
        if yield(p.a) { yield(p.b) } else { False }
    }

    fn known_count(_p: Pair) -> Maybe<Int> {
        Some(2)
    }
}

fn total(xs: Iter<Int>) -> Int {
    Iter.count(xs)
}

fn main() {
    io.inspect(total(#Pair{a: 1, b: 2}))
}
`,
	},
	{
		name: "gapCLoopFinalBreakEmptyList",
		src: `import std/io

fn main() {
    xs = Iter.loop(fn acc: List<Int> = [1] -> {
        if Iter.count(acc) > 2 { break acc }
        break []
    })

    io.inspect(xs)
}
`,
	},
	{
		name: "gapCLoopFnState",
		src: `import std/io

fn main() {
    f = Iter.loop(fn g = fn x: Int -> x -> {
        if g(0) > 3 { break g }
        fn x: Int -> g(x) + 1
    })

    io.inspect(f(1))
}
`,
	},
	{
		name: "gapCLoopGuardBreakEmptyList",
		src: `import std/io

fn main() {
    xs = Iter.loop(fn acc: List<Int> = [1] -> {
        if Iter.count(acc) > 2 { break [] }
        [1, ..acc]
    })

    io.inspect(xs)
}
`,
	},
	{
		name: "gapCLoopGuardReturnEmptyList",
		src: `import std/io

fn main() {
    xs = Iter.loop(fn acc: List<Int> = [1] -> {
        if Iter.count(acc) > 2 { break acc }
        if Iter.count(acc) == 0 { return [] }
        [1, ..acc]
    })

    io.inspect(xs)
}
`,
	},
	{
		name: "gapCLoopIfElseBreakEmptyList",
		src: `import std/io

fn main() {
    xs = Iter.loop(fn acc: List<Int> = [1] -> {
        if Iter.count(acc) > 2 {
            break []
        } else {
            [1, ..acc]
        }
    })

    io.inspect(xs)
}
`,
	},
	{
		name: "gapCLoopIfLetTail",
		src: `import std/io

fn main() {
    m: Maybe<Int> = Some(3)

    n = Iter.loop(fn n = 0 -> {
        if n > 3 { break n }
        if Some(v) = m { n + v } else { n + 1 }
    })

    io.inspect(n)
}
`,
	},
	{
		name: "gapCLoopTailEmptyList",
		src: `import std/io

fn main() {
    xs = Iter.loop(fn acc: List<Int> = [1] -> {
        if Iter.count(acc) > 2 { break acc }
        []
    })

    io.inspect(xs)
}
`,
	},
	{
		name: "gapCToMapFnValues",
		src: `import std/io

fn main() {
    m = Iter.to_map([#(1, fn x: Int -> x)])
    io.inspect(Map.keys(m))
}
`,
	},
	{
		name: "gapCUserSourceKnownCount",
		src: `import std/io

struct Pair {
    a: Int
    b: Int
}

impl Iter for Pair {
    fn each_while(p: Pair, yield: (Int) -> Bool) -> Bool {
        if yield(p.a) { yield(p.b) } else { False }
    }

    fn known_count(_p: Pair) -> Maybe<Int> {
        Some(2)
    }
}

fn main() {
    io.inspect(
        #Pair{a: 1, b: 2}
        |> Iter.map(fn x -> x + 1)
        |> Iter.to_list()
    )
}
`,
	},
	{
		name: "gapCZipFnListRight",
		src: `import std/io

fn main() {
    fs = [fn x: Int -> x + 1]

    io.inspect(
        Iter.zip([1], fs)
        |> Iter.count()
    )
}
`,
	},
	{
		name: "gapCZipUserDiscreteRange",
		src: `import std/io

type Id Int

derive Equatable for Id
derive Comparable for Id

impl Discrete for Id {
    fn next(Id(n)) -> Maybe<Id> {
        Some(Id(n + 1))
    }

    fn steps_between(Id(a), Id(b)) -> Maybe<Int> {
        Some(b - a)
    }
}

fn main() {
    io.inspect(
        Iter.zip([1, 2], Id(1)..Id(4))
        |> Iter.count()
    )
}
`,
	},
	{
		name: "gapCapturedDefaultLambdaCall",
		src: `import std/io

fn main() {
    f = fn x: Int, y: Int = 2 -> x + y
    g = fn -> f(1)
    io.print(Int.to_string(g()))
}
`,
	},
	{
		name: "gapCapturedLambdaNamedArgs",
		src: `import std/io

fn main() {
    f = fn x: Int, y: Int -> x - y
    g = fn -> f(y: 1, x: 5)
    io.print(Int.to_string(g()))
}
`,
	},
	{
		name: "gapContextValueQualifiedWitness",
		src: `// FILE: main.nomi
import {
    std/io
    domain
}

fn main() {
    c = Context.with_value(Context.root(), domain.Token(7))
    io.inspect(Context.value(c, domain.Token))
}
// FILE: domain.nomi
pub type Token Int
`,
	},
	{
		name: "gapDistinctTupleCallformIterWiden",
		src: `import std/io

struct Circle {
    r: Int
}

enum Shape {
    embeds Circle
    Dot
}

type P #(Iter<Shape>, Int)

fn main() {
    c = #Circle{r: 1}
    q = P(#([c], 2))
    #P(it, n) = q
    io.print(Int.to_string(Iter.count(it) + n))
}
`,
	},
	{
		name: "gapElseFallbackTupleCompositeDistinct",
		src: `import std/io

type Pair #(Int, String)

fn f(m: Maybe<#(Pair, Int)>) -> Int {
    Some(#(#Pair(x, _), y)) = m else {
        #(#Pair(0, ""), 0)
    }
    x + y
}

fn main() {
    io.inspect(f(None))
}
`,
	},
	{
		name: "gapEmbeddedStructFieldCompositeDistinct",
		src: `import std/io

type Pair #(Int, String)

struct Circle {
    p: Pair
}

enum E {
    embeds Circle
    Other
}

fn f(e: E) -> Int {
    case e {
        #.Circle{p: #Pair(a, _)} -> a
        _ -> 0
    }
}

fn main() {
    io.inspect(f(#Circle{p: #Pair(1, "a")}))
}
`,
	},
	{
		name: "gapFieldAccessorRecordExistential",
		src: `import std/io

fn main() {
    d: Display = 1
    rs = [#{x: 1, d}]

    io.inspect(
        rs
        |> Iter.map(.x)
        |> Iter.to_list()
    )
}
`,
	},
	{
		name: "gapFieldDefaultIterWiden",
		src: `import std/io

struct Circle {
    r: Int
}

enum Shape {
    embeds Circle
    Dot
}

struct H {
    it: Iter<Shape> = [#Circle{r: 1}]
}

fn main() {
    h = #H{}
    io.print(Int.to_string(Iter.count(h.it)))
}
`,
	},
	{
		name: "gapForeignFnIter",
		src: `// FILE: main.nomi
import {
    std/io
    nums
}

fn main() {
    io.print("${nums.sum_of([1, 2])}")
}

test "record param" {
    assert nums.px(#{x: 1, y: 2}) == 1
}
// FILE: nums.nomi
pub fn evens() -> Iter<Int> {
    [2, 4, 6]
}

pub fn sum_of(xs: Iter<Int>) -> Int {
    Iter.reduce(xs, fn acc = 0, x -> acc + x)
}

pub fn px(p: #{x: Int, y: Int}) -> Int {
    p.x
}
`,
	},
	{
		name: "gapGenericBoundImplOtherFile",
		src: `// FILE: main.nomi
import {
    std/io
    lib
}

impl Display for lib.Dog {
    fn to_string(d: lib.Dog) -> String {
        "dog " + d.name
    }
}

fn show<T>(x: T) -> String where T: Display {
    T.to_string(x)
}

fn main() {
    io.print(show(#lib.Dog{name: "d"}))
}
// FILE: lib.nomi
pub struct Dog {
    name: String
}
`,
	},
	{
		name: "gapGenericVariantUnsolvedParam",
		src: `import std/io

enum Either2<A, B> {
    L {a: A}
    R {b: B}
}

fn main() {
    io.inspect(#Either2.L{a: 1})
}
`,
	},
	{
		name: "gapGoFnDefaultedParam",
		src: `import std/io

gopkg "strings" as strs

fn upper(s: String = "abc") -> String go strs.ToUpper

fn main() {
    io.print(upper())
}
`,
	},
	{
		name: "gapInterfaceInFnBody",
		src: `import std/io

fn c(x: Int) -> Int {
    interface Foo {
        fn foo(v: self) -> Int
    }

    x
}

fn main() {
    io.inspect(c(1))
}
`,
	},
	{
		name: "gapLambdaReturnEmptyList",
		src: `import std/io

fn main() {
    a = fn x: Int -> {
        if x > 0 { return [] }
        [1]
    }

    io.inspect(a(1))
}
`,
	},
	{
		name: "gapLambdaReturnEmptyListFirst",
		src: `import std/io

fn main() {
    g = fn x: Int -> {
        if x > 0 { return [] }
        if x < 0 { return [x] }
        [x, x]
    }

    io.inspect(g(1))
}
`,
	},
	{
		name: "gapListDistinctUntypedListPattern",
		src: `import std/io

type Stack List<Int>

fn top(s: Stack) -> Int {
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
		name: "gapLoopArmReturnEmpty",
		src: `import std/io

fn main() {
    r = Iter.loop(fn xs = [1, 2, 3] -> {
        case xs {
            [] -> break [0]
            [_] -> []
            [_, ..rest] -> rest
        }
    })

    io.inspect(r)
}
`,
	},
	{
		name: "gapLoopElseFallbackIf",
		src: `import {
    std/io
    std/iter.Iter.loop
}

fn find(n: Int) -> Maybe<Int> {
    if n > 3 { None } else { Some(n) }
}

fn main() {
    r = loop(fn n = 0 -> {
        Some(v) = find(n) else {
            if n > 5 { 10 } else { 20 }
        }
        if v > 2 { break v }
        n + 1
    })

    io.inspect(r)
}
`,
	},
	{
		name: "gapLoopElseIfBothBreak",
		src: `import {
    std/io
    std/iter.Iter.loop
}

fn find(n: Int) -> Maybe<Maybe<Int>> {
    if n > 3 { None } else { Some(Some(n)) }
}

fn main() {
    r = loop(fn n = 0 -> {
        Some(Some(v)) = find(n) else {
            if n > 10 { break 1 } else { break 2 }
        }
        v + n + 1
    })

    io.inspect(r)
}
`,
	},
	{
		name: "gapLoopNestedGuardReturnEmpty",
		src: `import std/io

fn main() {
    r = Iter.loop(fn xs = [1, 2, 3] -> {
        case xs {
            [] ->
                break [0]

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
		name: "gapMapBreakEmptyListFirst",
		src: `import std/io

fn main() {
    ys =
        [1, 2, 3]
        |> Iter.map(fn x -> {
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
		name: "gapMapOfFunctions",
		src: `import std/io

fn main() {
    ops = %{"inc" => fn x: Int -> x + 1}

    case Map.get(ops, "inc") {
        Some(f) -> io.inspect(f(1))
        None -> io.inspect(0)
    }
}
`,
	},
	{
		name: "gapMapValueLiteralAttachDistinct",
		src: `import std/io

type Kvs Map<String, Int>

fn f(m: Map<String, Kvs>) -> Int {
    case m {
        %{"k" => %Kvs{"a" => v}} -> v
        _ -> 0
    }
}

fn main() {
    io.inspect(f(%{"k" => %Kvs{"a" => 1}}))
}
`,
	},
	{
		name: "gapNamedGapGenericParam",
		src: `import std/io

struct Box<T> {
    v: T
}

impl Box<T> {
    fn pick<T>(_b: Box<T>, other: Maybe<T> = None, n: Int = 0) -> Int {
        case other {
            Some(_) -> n + 1
            None -> n
        }
    }
}

fn main() {
    io.inspect(Box.pick(#Box{v: 1}, n: 2))
}
`,
	},
	{
		name: "gapNestedGenericBoundCallTuple",
		src: `import std/io

fn main() {
    fn txt<T>(x: T) -> String where T: Display {
        T.to_string(x)
    }

    io.print(txt(#(1, "a")))
}
`,
	},
	{
		name: "gapPartialCapturedLambda",
		src: `import std/io

fn main() {
    f = fn a: Int, b: Int -> a * b
    g = fn -> &f(2, &1)
    io.inspect(g()(3))
}
`,
	},
	{
		name: "gapPartialOmittedNonliteralDefault",
		src: `import std/io

fn base() -> Int {
    10
}

fn conn(host: String, port: Int = base() + 1) -> String {
    "${host}:${port}"
}

fn main() {
    c = &conn(&1)
    io.print(c("h"))
}
`,
	},
	{
		name: "gapRecordFieldLiteralAttachDistinct",
		src: `import std/io

type Stack List<Int>

fn main() {
    c = #{r: 1, q: Stack([1])}

    n = case c {
        #{q: Stack[a, .._]} -> a
        _ -> 0
    }

    io.inspect(n)
}
`,
	},
	{
		name: "gapRecursiveNestedFnEmptyListTail",
		src: `import std/io

fn main() {
    fn r(n: Int) -> List<Int> {
        if n > 0 { io.inspect(r(n - 1)) }
        []
    }

    io.inspect(r(2))
}
`,
	},
	{
		name: "gapSetupLeavingBinding",
		src: `fn id(n: Int) -> Int {
    n
}

group "a setup binding whose every arm returns" {
    setup {
        v = case id(1) {
            1 -> return 5
            _ -> return 6
        }

        v
    }

    test "v", v {
        assert v == 5
    }
}
`,
	},
	{
		name: "gapShadowedStdInterfaceThroughItsModule",
		src: `import {
    std/calendar
    std/calendar.Date
    std/io
}

struct DateParts {
    x: Int
}

fn main() {
    d = Date` + "`2024-03-05`" + `
    io.print(Int.to_string(calendar.DateParts.year(d)))
    io.print(Int.to_string(#DateParts{x: 1}.x))
}
`,
	},
	{
		name: "gapSiblingDefaultGenericHole",
		src: `// FILE: main.nomi
import {
    std/io
    lib
}

fn main() {
    io.print(lib.show(1))
}
// FILE: lib.nomi
fn is_some<T>(m: Maybe<T>) -> Bool {
    case m {
        Some(_) -> True
        None -> False
    }
}

pub fn show(x: Int, flag: Bool = is_some(None)) -> String {
    "${x} ${flag}"
}
`,
	},
	{
		name: "gapSiblingFnReturnsStructWithRecordField",
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
    at: #{x: Int, y: Int}
}

pub fn make() -> Box {
    #Box{at: #{x: 1, y: 2}}
}

pub fn width(b: Box) -> Int {
    b.at.x
}
`,
	},
	{
		name: "gapSiblingGenericEmptyList",
		src: `// FILE: main.nomi
import {
    std/io
    lib.cnt
}

fn main() {
    io.inspect(cnt([]))
}
// FILE: lib.nomi
pub fn cnt<T>(xs: List<T>) -> Int {
    Iter.count(xs)
}
`,
	},
	{
		name: "gapStructSpreadIterFieldWiden",
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
    c = #Circle{r: 1}
    h = #H{it: [Shape.Dot], n: 1}
    h2 = #{..h, it: [c]}
    io.inspect(Iter.count(h2.it))
}
`,
	},
	{
		name: "gapStructUpdateNamedIterFieldWiden",
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
    c = #Circle{r: 1}
    h = #H{it: [Shape.Dot], n: 1}
    h2 = Struct.update(updates: #{it: [c]}, original: h)
    io.inspect(Iter.count(h2.it))
}
`,
	},
	{
		name: "gapStructVariantFieldCompositeDistinct",
		src: `import std/io

type Pair #(Int, String)

enum E {
    Rec {p: Pair}
    Other
}

fn f(e: E) -> Int {
    case e {
        #.Rec{p: #Pair(a, _)} -> a
        _ -> 0
    }
}

fn main() {
    io.inspect(f(#E.Rec{p: #Pair(1, "a")}))
}
`,
	},
	{
		name: "gapStructVariantPatternAliasQualifier",
		src: `import std/io

enum Shape {
    Rec {
        w: Int,
        h: Int,
    }
    Dot
}

typealias S Shape

fn f(s: Shape) -> Int {
    case s {
        #S.Rec{w} -> w
        _ -> 0
    }
}

fn main() {
    io.inspect(f(#Shape.Rec{w: 2, h: 3}))
}
`,
	},
	{
		name: "gapStructVariantRecordPayloadPattern",
		src: `import std/io

enum Shape {
    Rec {
        w: Int,
        h: Int,
    }
    Dot
}

fn f(s: Shape) -> Int {
    case s {
        .Rec(#{w, h}) -> w + h
        _ -> 0
    }
}

fn main() {
    io.inspect(f(#Shape.Rec{w: 2, h: 3}))
}
`,
	},
	{
		name: "gapSupervisorSpawnAllNamedArg",
		src: `import {
    std/io
    std/supervisors.Supervisor
}

struct App {
    context: Context
    work: Supervisor
}

fn boot(_startup: Startup) -> Result<App, String> {
    Ok(#App{context: Context.root(), work: Supervisor.new(2)})
}

fn main() {
    _ = Supervisor.spawn_all(App.work, [1, 2], f: fn n: Int -> io.inspect(n))
}
`,
	},
	{
		name: "gapSupervisorSpawnNamedArg",
		src: `import {
    std/io
    std/supervisors.Supervisor
}

struct App {
    context: Context
    work: Supervisor
}

fn boot(_startup: Startup) -> Result<App, String> {
    Ok(#App{context: Context.root(), work: Supervisor.new(2)})
}

fn main() {
    _ = Supervisor.spawn(App.work, body: fn -> io.print("job"))
}
`,
	},
	{
		name: "gapTaskAwaitNamedArg",
		src: `import {
    std/io
    std/tasks.Task
}

fn main() {
    n = concurrent {
        t = Task.spawn(fn -> 3)
        Task.await(task: t)
    }

    io.inspect(n)
}
`,
	},
	{
		name: "gapTaskSpawnAllVector",
		src: `import {
    std/io
    std/tasks.Task
}

fn double(n: Int) -> Int {
    n * 2
}

fn main() {
    xs = concurrent {
        ts = Task.spawn_all(
            [1, 2, 3]
            |> Iter.to_vector(),
            double,
            max_running: 2,
        )

        Task.await_all(ts)
    }

    io.inspect(xs)
}
`,
	},
	{
		name: "gapTestAssertionBindingRebind",
		src: `fn double(n: Int) -> Int {
    n * 2
}

test "an assertion binding that rebinds a name" {
    v = 1
    v = assert double(v) == 2
    assert v
}
`,
	},
	{
		name: "gapTestPipeBindingNestedPipe",
		src: `fn double(n: Int) -> Int {
    n * 2
}

test "a pipe binding whose stage holds a pipe" {
    ok = [1, 2] |> Iter.any(fn x: Int -> x |> double() > 2)
    assert ok
}
`,
	},
	{
		name: "gapTestingCheckNestedPipe",
		src: `import std/testing

fn double(n: Int) -> Int {
    n * 2
}

test "testing.check over a pipe whose stage holds a pipe" {
    assert testing.check(
        [1, 2]
        |> Iter.any(fn x: Int -> x |> double() > 2)
    )
}
`,
	},
	{
		name: "gapTupleDestructureRebindsFunction",
		src: `import std/io

fn main() {
    f = fn x: Int -> x
    io.inspect(f(1))
    #(f, n) = #(fn x: Int -> x + 1, 1)
    io.inspect(f(n))
}
`,
	},
	{
		name: "gapTupleEmptyListEquality",
		src: `import std/io

fn main() {
    io.inspect(#(1, []) == #(1, [2]))
}
`,
	},
	{
		name: "gapTuplePatternCompositeDistinct",
		src: `import std/io

type Pair #(Int, String)

fn f(t: #(Pair, Int)) -> Int {
    case t {
        #(#Pair(1, _), 2) -> 1
        _ -> 0
    }
}

fn main() {
    io.inspect(f(#(#Pair(1, "a"), 2)))
}
`,
	},
	{
		name: "gapTurbofishTwoTypeArgs",
		src: `import std/io

fn main() {
    io.inspect(Maybe.map<Int, String>(Some(1), fn x -> "${x}"))
}
`,
	},
	{
		name: "gapUpdateBaseDeclines",
		src: `import std/io

struct P {
    x: Int
}

fn pick(b: Bool) -> Bool {
    b
}

fn main() {
    p = #P{x: 1}
    q = Struct.update(if pick(True) { p } else { p }, #{x: 2})
    io.inspect(q.x)
    r = Struct.update(updates: #{x: 3}, original: if pick(False) { p } else { p })
    io.inspect(r.x)
}
`,
	},
	{
		name: "gapVariantPatternAliasQualifier",
		src: `import std/io

enum Shape {
    Num Int
    Dot
}

typealias S Shape

fn f(s: Shape) -> Int {
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
		name: "gapVariantPayloadAsCompositeDistinct",
		src: `import std/io

type Pair #(Int, String)

enum E {
    Dist Pair
    Other
}

fn f(e: E) -> Int {
    case e {
        .Dist(#Pair(a, _) as _whole) -> a
        _ -> 0
    }
}

fn main() {
    io.inspect(f(E.Dist(#Pair(1, "a"))))
}
`,
	},
	{
		name: "gapVariantPayloadTupleCompositeDistinct",
		src: `import std/io

type Pair #(Int, String)

enum E {
    TP #(Pair, Int)
    Other
}

fn f(e: E) -> Int {
    case e {
        .TP(#(#Pair(a, _), _)) -> a
        _ -> 0
    }
}

fn main() {
    io.inspect(f(E.TP(#(#Pair(1, "a"), 2))))
}
`,
	},
	{
		name: "gapVectorCapturedElemWiden",
		src: `import {
    std/io
    std/io.Captured
}

struct Circle {
    r: Int
}

enum Shape {
    embeds Circle
    Dot
}

fn main() {
    c = #Circle{r: 1}
    v: Vector<Captured<Shape>> = #[#Captured{value: c, output: "", errors: "", transcript: ""}]
    io.inspect(Vector.length(v))
}
`,
	},
	{
		name: "gapVectorItemsJoinCaptured",
		src: `import {
    std/io
    std/io.Captured
}

struct Circle {
    r: Int
}

enum Shape {
    embeds Circle
    Dot
}

fn main() {
    a = #Captured{value: #Circle{r: 1}, output: "", errors: "", transcript: ""}
    b = #Captured{value: Shape.Dot, output: "", errors: "", transcript: ""}
    v = #[a, b]
    io.inspect(Vector.length(v))
}
`,
	},
	{
		name: "gapVectorOfFunctions",
		src: `import std/io

fn main() {
    fs = #[fn x: Int -> x + 1, fn x: Int -> x * 2]

    case Vector.get(fs, 1) {
        Some(f) -> io.inspect(f(5))
        None -> io.inspect(0)
    }
}
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
