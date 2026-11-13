package vmhost

import (
	"fmt"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nomi-language/nomi/std"
)

// The type × operation grid. The fuzzers and the feature matrix
// (feature_matrix_test.go) vary syntax: what a value is written as and
// where the expression stands. They rarely vary the value's type, so a
// valid type in a place the builder does not expect (`(ChannelClosed, 1)`,
// an Iter inside a List, a function as a Map value) stays unexercised. This
// test crosses value types with the operations a program does to a value,
// and checks every program that makes.
//
// Inputs:
//
//   - A type (toTypes) is the declarations it needs, any statements that
//     must run before its value can be written (a captured binding, a
//     channel), an expression of the type, the type written out, which
//     interfaces it has (equality, Display), and an observation that turns
//     a value of it into text with the text it must print. The type of a
//     type parameter is one entry: its cell's body runs inside a generic
//     function, at T = Int.
//   - An operation (toOps) is a template over a type: the declarations it
//     needs (a struct with a field of the type, an enum with a payload of
//     it, a generic function), and statements that put the value somewhere
//     and take it back out as `out`. $E is the type's expression, $T the
//     type, $P the type parameters a declaration over $T needs ("<T>" for
//     the type parameter's cell, else nothing). An operation that needs
//     equality or Display says so.
//
// A cell is a type and an operation: one small program whose `main` builds
// the value, runs the operation, and prints the observation of `out`. A
// cell either is rejected by the checker, when the operation needs what
// the type lacks or a toRules entry says why, with the text toRejection
// expects, or lowers and runs,
// printing exactly the type's observation; its built image must print the
// same. Every operation asserts what it can inside the program, so a wrong
// answer fails the run. The test fails on an unexpected rejection, an
// accepted program the checker should have rejected, a decline, a compiler
// panic, an expression the checker leaves untyped, a crash, and a wrong
// transcript.
//
// Known gaps: every failing cell is pinned in knownTypeOpGaps
// (type_ops_gaps_test.go) by the cell names it excuses and a minimal
// reproducer, with its kind: the builder declines or panics, the run
// crashes or prints the wrong thing, or the checker accepts what it should
// reject (or rejects what it should accept). TestKnownTypeOpGaps holds
// each reproducer to its kind, so a fix fails it until the entry goes. The
// interesting cells, the failing ones among them, seed
// FuzzLoweredProgramsRun (typeOpSeeds).
//
// Commands and environment:
//
//	go test ./vmhost -run '^TestTypeOperationGrid$'
//	NOMI_TYPE_OPS=full go test ./vmhost -run '^TestTypeOperationGrid$'
//	NOMI_TYPE_OPS_ONLY='closure × ' go test ./vmhost -run '^TestTypeOperationGrid$'
//	go test ./vmhost -run '^TestKnownTypeOpGaps$'
//
// Plain `go test` checks every core type (toType.core) under every core
// operation (toOp.core), and one in toSample of the other cells: about
// 3500 cells in 8 seconds. NOMI_TYPE_OPS=full checks every cell, about
// 12,600 in 30 seconds; run it before committing a change to the value
// domain or a retained predicate. NOMI_TYPE_OPS_ONLY=<text> checks only the cells whose
// name holds text, in either size. Cells run on every CPU at once and are
// reported in order.
//
// To extend it, add a toType or a toOp. An operation a type cannot take
// for a reason other than equality or Display gets a toRule.

// toType is one value type.
type toType struct {
	name string
	// decls are its top-level declarations.
	decls string
	// imports are the std items it names ("std::channels.ChannelClosed").
	imports []string
	// pre are statements that run before $E is written.
	pre string
	// typ is the type, as an annotation writes it.
	typ string
	// expr is a value of the type, on one line.
	expr string
	// obs turns the value, $v, into a String; empty is Debug.inspect($v).
	obs string
	// want is what io.print prints of obs.
	want string
	// noEq marks a type without equality (no ==, no Set element, no Map
	// key).
	noEq bool
	// shown is the type's Display text; empty means it is not Display.
	shown string
	// inspect is the type's Debug.inspect text; empty means want, which
	// is Debug.inspect's when obs is empty.
	inspect string
	// generic runs the cell's body in `fn run<T>(seed: T)` at T = Int.
	generic bool
	// core puts every cell of the type in the default sample.
	core bool
}

type toNeeds int

const (
	needEq toNeeds = 1 << iota
	needDisplay
)

// toOp is one operation over a value.
type toOp struct {
	name    string
	decls   string
	imports []string
	// body binds `out` to a value of $T taken back from where the
	// operation put $E.
	body  string
	needs toNeeds
	core  bool
}

const (
	toChain = "enum Chain\n    Link(Int, Chain)\n    End\nend"
	toTree  = "struct Tree\n    v: Int\n    kids: List<Tree>\nend"
	toShape = "enum Shape\n    Circle Float\n    Rect {w: Int, h: Int}\nend"
	toColor = "enum Color\n    Red\n    Green\nend"
	toPoint = "struct Point\n    x: Int\n    y: Int\nend"
	toInc   = "fn inc(n: Int) -> Int\n    n + 1\nend"
	toProbe = "fn probe(c: Channel<Int>) -> String\n    _ = Sender.send(c.sender, 5)\n    Debug.inspect(Receiver.receive(c.receiver))\nend"
	toNamed = "interface Named\n    fn name(value: self) -> String\nend\n\nstruct Pt\n    x: Int\nend\n\nimpl Named for Pt\n    fn name(p: Pt) -> String\n        \"pt${p.x}\"\n    end\nend"
	toChans = "std::channels.Channel std::channels.Sender std::channels.Receiver"
)

var toTypes = []toType{
	{name: "int", typ: "Int", expr: "42", want: "42", shown: "42", core: true},
	{name: "float", typ: "Float", expr: "1.5", want: "1.5", shown: "1.5"},
	{name: "decimal", typ: "Decimal", expr: "2.25d", want: "2.25d", shown: "2.25"},
	{name: "string", typ: "String", expr: `"hi"`, want: `"hi"`, shown: "hi", core: true},
	{name: "bool", typ: "Bool", expr: "True", want: "True", shown: "True"},
	{name: "unit", typ: "Unit", expr: "Unit", want: "Unit", core: true},
	{name: "bytes", typ: "Bytes", expr: `String.to_bytes("ab")`, want: "<<97, 98>>", shown: "Bytes(2)"},
	{name: "std marker", typ: "ChannelClosed", expr: "ChannelClosed", want: "ChannelClosed",
		imports: []string{"std::channels.ChannelClosed"}, core: true},
	{name: "std marker TimedOut", typ: "TimedOut", expr: "TimedOut", want: "TimedOut",
		imports: []string{"std::channels.TimedOut"}},
	{name: "user marker", decls: "type Done", typ: "Done", expr: "Done", want: "Done", core: true},
	{name: "plain enum", decls: toColor, typ: "Color", expr: "Color.Green", want: "Green"},
	{name: "payload enum", decls: toShape, typ: "Shape", expr: "Shape.Rect{w: 1, h: 2}", want: "Rect{w: 1, h: 2}", core: true},
	{name: "recursive enum", decls: toChain, typ: "Chain", expr: "Chain.Link(1, .Link(2, .End))", want: "Link(1, Link(2, End))"},
	{name: "struct", decls: toPoint, typ: "Point", expr: "Point{x: 1, y: 2}", want: "Point{x: 1, y: 2}"},
	{name: "recursive struct", decls: toTree, typ: "Tree", expr: "Tree{v: 1, kids: [Tree{v: 2, kids: []}]}",
		want: "Tree{v: 1, kids: [Tree{v: 2, kids: []}]}"},
	{name: "anonymous struct", typ: "{a: Int, b: String}", expr: `{a: 1, b: "x"}`, want: `{a: 1, b: "x"}`, core: true, shown: "{a: 1, b: x}"},
	{name: "tuple2", typ: "(Int, String)", expr: `(1, "a")`, want: `(1, "a")`, shown: "(1, a)", core: true},
	{name: "tuple3", typ: "(Int, String, Bool)", expr: `(1, "a", True)`, want: `(1, "a", True)`, shown: "(1, a, True)"},
	{name: "function", inspect: "<function>", decls: toInc, typ: "(Int) -> Int", expr: "inc", obs: "Debug.inspect($v(1))", want: "2", noEq: true, core: true},
	{name: "closure", inspect: "<function>", pre: "k = 10", typ: "(Int) -> Int", expr: "(fn n: Int -> n + k)", obs: "Debug.inspect($v(1))", want: "11", noEq: true, core: true},
	{name: "iter", inspect: "<iter>", typ: "Iter<Int>", expr: "[1, 2] |> Iter.map(&(&1 * 10))", obs: "Debug.inspect(Iter.to_list($v))", want: "[10, 20]", noEq: true, core: true},
	{name: "range", typ: "Range<Int>", expr: "1..4", want: "1..4", shown: "1..4"},
	{name: "maybe int", typ: "Maybe<Int>", expr: "Some(3)", want: "Some(3)", shown: "Some(3)"},
	{name: "maybe struct", decls: toPoint, typ: "Maybe<Point>", expr: "Some(Point{x: 1, y: 2})", want: "Some(Point{x: 1, y: 2})"},
	{name: "maybe closure", inspect: "Some(<function>)", pre: "k = 10", typ: "Maybe<(Int) -> Int>", expr: "Some((fn n: Int -> n + k))", obs: "Debug.inspect(Maybe.map($v, fn f -> f(1)))", want: "Some(11)", noEq: true},
	{name: "result int", typ: "Result<Int, String>", expr: "Ok(3)", want: "Ok(3)", core: true, shown: "Ok(3)"},
	{name: "result err enum", decls: toPoint + "\n\n" + toColor, typ: "Result<Point, Color>", expr: "Err(Color.Red)", want: "Err(Red)"},
	{name: "result unit marker", typ: "Result<Unit, ChannelClosed>", expr: "Err(ChannelClosed)", want: "Err(ChannelClosed)",
		imports: []string{"std::channels.ChannelClosed"}},
	{name: "list int", typ: "List<Int>", expr: "[1, 2]", want: "[1, 2]", shown: "[1, 2]", core: true},
	{name: "list maybe", typ: "List<Maybe<Int>>", expr: "[Some(1), None]", want: "[Some(1), None]", shown: "[Some(1), None]"},
	{name: "list fn", inspect: "[<function>, <function>]", decls: toInc, typ: "List<(Int) -> Int>", expr: "[inc, inc]", obs: "Debug.inspect(Iter.map($v, fn f -> f(1)) |> Iter.to_list())", want: "[2, 2]", noEq: true},
	{name: "list iter", inspect: "[<iter>]", typ: "List<Iter<Int>>", expr: "[[1, 2] |> Iter.map(&(&1 + 1))]", obs: "Debug.inspect(Iter.flatten($v))", want: "[2, 3]", noEq: true},
	{name: "vector string", typ: "Vector<String>", expr: `Vector["a", "b"]`, want: `Vector["a", "b"]`, shown: "Vector[a, b]"},
	{name: "set int", inspect: "Set[1, 2]", typ: "Set<Int>", expr: "Set[1, 2]", obs: "Debug.inspect(Iter.sort(Iter.to_list($v)))", want: "[1, 2]", shown: "Set[1, 2]"},
	{name: "map string int", typ: "Map<String, Int>", expr: `{"a" => 1}`, want: `{"a" => 1}`, core: true, shown: "{a => 1}"},
	{name: "map tuple key", typ: "Map<(Int, Int), String>", expr: `{(1, 2) => "x"}`, want: `{(1, 2) => "x"}`, shown: "{(1, 2) => x}"},
	{name: "map struct value", decls: toPoint, typ: "Map<Int, Point>", expr: "{1 => Point{x: 1, y: 2}}", want: "{1 => Point{x: 1, y: 2}}"},
	{name: "display value", inspect: "7", typ: "Display", expr: "(7 as Display)", obs: "Display.to_string($v)", want: "7", shown: "7", core: true},
	{name: "type parameter", typ: "T", expr: "seed", want: "42", shown: "42", generic: true, core: true},
	{name: "distinct", decls: "type Meters Int", typ: "Meters", expr: "Meters(3)", want: "Meters(3)", core: true},
	{name: "channel", inspect: "Channel{sender: <channel>, receiver: <channel>}", decls: toProbe, imports: strings.Fields(toChans), typ: "Channel<Int>", expr: "Channel.buffered<Int>(2)",
		obs: "probe($v)", want: "Some(5)", noEq: true, core: true},
	{name: "sender", inspect: "<channel>", imports: strings.Fields(toChans), pre: "ch0 = Channel.buffered<Int>(2)", typ: "Sender<Int>", expr: "ch0.sender",
		obs: "Debug.inspect(Sender.send($v, 6))", want: "Ok(Unit)", noEq: true},
	{name: "instant", inspect: "<opaque Instant>", imports: []string{"std::instant.Instant"}, typ: "Instant", expr: "Instant.from_seconds(5)",
		obs: "Debug.inspect(Instant.to_seconds($v))", want: "5"},
	{name: "date", imports: []string{"std::calendar.Date"}, typ: "Date", expr: "Date`2026-01-05`", want: "2026-01-05", shown: "2026-01-05"},
	{name: "duration", imports: []string{"std::duration.Duration"}, typ: "Duration", expr: "Duration.seconds(2)", want: "2s", shown: "2s"},
	{name: "struct with fn", inspect: "Op{f: <function>}", decls: toInc + "\n\nstruct Op\n    f: (Int) -> Int\nend", typ: "Op", expr: "Op{f: inc}",
		obs: "Debug.inspect($v.f(1))", want: "2", noEq: true},
	{name: "byte", typ: "Byte", expr: "(7 as Byte)", want: "7", shown: "7"},
	{name: "dynamic", imports: []string{"std::json.Json", "std::dynamic.Dynamic"}, typ: "Dynamic", expr: "Json.to_dynamic(Json.decode(\"{\\\"a\\\": [1, 2.5]}\")!)", want: `{"a": [1, 2.5]}`},
	{name: "json", imports: []string{"std::json.Json"}, typ: "Json", expr: "Json.decode(\"[1]\")!", want: "[1]", shown: "[1]"},
	{name: "regex", imports: []string{"std::regex.Regex"}, typ: "Regex<Unit>", expr: "Regex`a+b`", want: "Regex`a+b`", shown: "a+b"},
	{name: "generic struct", decls: "struct Bx<A>\n    v: A\nend", typ: "Bx<Int>", expr: "Bx{v: 1}", want: "Bx{v: 1}"},
	{name: "nested anonymous struct", typ: "{p: {q: Int}}", expr: "{p: {q: 1}}", want: "{p: {q: 1}}", shown: "{p: {q: 1}}"},
	{name: "maybe maybe", typ: "Maybe<Maybe<Int>>", expr: "Some(Some(1))", want: "Some(Some(1))", shown: "Some(Some(1))"},
	{name: "list unit", typ: "List<Unit>", expr: "[Unit, Unit]", want: "[Unit, Unit]"},
	{name: "tuple with fn", inspect: "(1, <function>)", decls: toInc, typ: "(Int, (Int) -> Int)", expr: "(1, inc)", obs: "Debug.inspect($v.second(1))", want: "2", noEq: true},
	{name: "user interface value", inspect: "Pt{x: 1}", decls: toNamed, typ: "Named", expr: "(Pt{x: 1} as Named)", obs: "Named.name($v)", want: "pt1"},
	{name: "receiver", inspect: "<channel>", imports: strings.Fields(toChans), pre: "ch0 = Channel.buffered<Int>(2)\n_ = Sender.send(ch0.sender, 4)", typ: "Receiver<Int>", expr: "ch0.receiver",
		obs: "Debug.inspect(Receiver.receive($v))", want: "Some(4)", noEq: true},
	{name: "reader", inspect: "Reader", imports: []string{"std::io.Reader"}, typ: "Reader", expr: `Reader.from_text("x")`,
		obs: "Debug.inspect(Reader.read_all_text($v))", want: `Ok("x")`, noEq: true},
	// Element and payload kinds the builder's narrower predicates once
	// refused: Unit, Iters, functions, interface values and handles inside
	// containers.
	{name: "iter unit", inspect: "<iter>", typ: "Iter<Unit>", expr: "Iter.repeat(Unit) |> Iter.take(2)", obs: "Debug.inspect(Iter.count($v))", want: "2", noEq: true, core: true},
	{name: "vector unit", typ: "Vector<Unit>", expr: "Vector[Unit, Unit]", want: "Vector[Unit, Unit]"},
	{name: "maybe unit", typ: "Maybe<Unit>", expr: "Some(Unit)", want: "Some(Unit)"},
	{name: "tuple with unit", typ: "(Unit, Int)", expr: "(Unit, 1)", want: "(Unit, 1)"},
	{name: "maybe iter", inspect: "Some(<iter>)", typ: "Maybe<Iter<Int>>", expr: "Some(Iter.from(1))", obs: "Debug.inspect(Maybe.map($v, fn it -> it |> Iter.take(2) |> Iter.to_list()))", want: "Some([1, 2])", noEq: true},
	{name: "tuple with iter", inspect: "(<iter>, 1)", typ: "(Iter<Int>, Int)", expr: "(Iter.from(1), 1)", obs: "Debug.inspect($v.first |> Iter.take(2) |> Iter.to_list())", want: "[1, 2]", noEq: true},
	{name: "record with iter", inspect: "{a: <iter>}", typ: "{a: Iter<Int>}", expr: "{a: Iter.from(1)}", obs: "Debug.inspect($v.a |> Iter.take(2) |> Iter.to_list())", want: "[1, 2]", noEq: true},
	{name: "map iter value", inspect: `{"a" => <iter>}`, typ: "Map<String, Iter<Int>>", expr: `{"a" => Iter.from(1)}`, obs: `Debug.inspect(Map.get($v, "a") |> Maybe.map(fn it -> it |> Iter.take(2) |> Iter.to_list()))`, want: "Some([1, 2])", noEq: true},
	{name: "map fn value", inspect: "{1 => <function>}", decls: toInc, typ: "Map<Int, (Int) -> Int>", expr: "{1 => inc}", obs: "Debug.inspect(Map.get($v, 1) |> Maybe.map(fn f -> f(1)))", want: "Some(2)", noEq: true},
	{name: "vector iter", inspect: "Vector[<iter>]", typ: "Vector<Iter<Int>>", expr: "Vector[[1] |> Iter.map(&(&1 + 1))]", obs: "Debug.inspect(Iter.flatten($v))", want: "[2]", noEq: true},
	{name: "iter of iters", inspect: "<iter>", typ: "Iter<Iter<Int>>", expr: "[[1] |> Iter.map(&(&1 + 1))] |> Iter.map(fn it -> it)", obs: "Debug.inspect(Iter.flatten($v))", want: "[2]", noEq: true},
	{name: "set marker", decls: "type Done", typ: "Set<Done>", expr: "Set[Done]", want: "Set[Done]"},
	{name: "set tuple", typ: "Set<(Int, String)>", expr: `Set[(1, "a")]`, want: `Set[(1, "a")]`, shown: "Set[(1, a)]"},
	{name: "generic enum iter", inspect: "Has(<iter>)", decls: "enum Opt<A>\n    Has(A)\n    Nothing\nend", typ: "Opt<Iter<Int>>", expr: "Opt.Has(Iter.from(1))",
		obs: "Debug.inspect(case $v\n        .Has(it) -> it |> Iter.take(1) |> Iter.to_list()\n        .Nothing -> []\n    end)", want: "[1]", noEq: true},
	{name: "box of fn", inspect: "Bx{v: <function>}", decls: toInc + "\n\nstruct Bx<A>\n    v: A\nend", typ: "Bx<(Int) -> Int>", expr: "Bx{v: inc}", obs: "Debug.inspect($v.v(1))", want: "2", noEq: true},
	{name: "string distinct", decls: "type Name String", typ: "Name", expr: `Name("a")`, want: `Name("a")`},
	{name: "list distinct", decls: "type Ids List<Int>", typ: "Ids", expr: "Ids[1]", want: "Ids([1])"},
	{name: "tuple distinct", decls: "type Pair (Int, String)", typ: "Pair", expr: "Pair(1, \"a\")", want: "Pair(1, \"a\")"},
	{name: "map distinct", decls: "type Kvs Map<String, Int>", typ: "Kvs", expr: "Kvs{\"a\" => 1}", want: "Kvs({\"a\" => 1})"},
	{name: "set distinct", decls: "type Tags Set<String>", typ: "Tags", expr: "Tags[\"a\"]", want: "Tags(Set[\"a\"])"},
	{name: "vector distinct", decls: "type Path Vector<Int>", typ: "Path", expr: "Path[1, 2]", want: "Path(Vector[1, 2])"},
	{name: "fn distinct", inspect: "Handler(<function>)", decls: "type Handler (Int) -> Int", typ: "Handler", expr: "Handler(fn n: Int -> n + 1)",
		obs: "Debug.inspect(case $v\n        Handler(f) -> f(1)\n    end)", want: "2", noEq: true},
	{name: "unit fn", inspect: "<function>", typ: "(Unit) -> Unit", expr: "(fn _: Unit -> Unit)", obs: "Debug.inspect($v(Unit))", want: "Unit", noEq: true},
	{name: "fn of fn", inspect: "<function>", typ: "(Int) -> (Int) -> Int", expr: "(fn n: Int -> fn m: Int -> n + m)", obs: "Debug.inspect($v(1)(2))", want: "3", noEq: true},
	{name: "fn to iter", inspect: "<function>", typ: "(Int) -> Iter<Int>", expr: "(fn n: Int -> Iter.from(n))", obs: "Debug.inspect($v(1) |> Iter.take(2) |> Iter.to_list())", want: "[1, 2]", noEq: true},
	{name: "fn to display", inspect: "<function>", typ: "(Int) -> Display", expr: "(fn n: Int -> (n as Display))", obs: "Display.to_string($v(4))", want: "4", noEq: true},
	{name: "list display", inspect: "[1, 2]", typ: "List<Display>", expr: "[(1 as Display), (2 as Display)]", obs: "Debug.inspect(Iter.map($v, fn d -> Display.to_string(d)) |> Iter.to_list())", want: `["1", "2"]`, shown: "[1, 2]"},
	{name: "tuple with display", inspect: "(1, 2)", typ: "(Display, Int)", expr: "((1 as Display), 2)", obs: "Display.to_string($v.first)", want: "1", shown: "(1, 2)"},
	{name: "record with display", inspect: "{a: 1}", typ: "{a: Display}", expr: "{a: (1 as Display)}", obs: "Display.to_string($v.a)", want: "1", shown: "{a: 1}"},
	{name: "map display value", inspect: `{"a" => 1}`, typ: "Map<String, Display>", expr: `{"a" => (1 as Display)}`, obs: `Display.to_string(Map.get($v, "a")!)`, want: "1", shown: "{a => 1}"},
	{name: "codepoint", typ: "Codepoint", expr: "'a'", want: "Codepoint(97)", shown: "a"},
	{name: "ordering", typ: "Ordering", expr: "Ordering.Less", want: "Less", shown: "Less"},
	{name: "type witness", inspect: "Type<Tid>", decls: "type Tid String\n\nfn witness() -> Type<Tid>\n    Tid\nend", typ: "Type<Tid>", expr: "witness()",
		obs: "Debug.inspect(Context.value(Context.with_value(Context.root(), Tid(\"a\")), $v))", want: `Some(Tid("a"))`},
	{name: "context", inspect: "<context>", typ: "Context", expr: "Context.root()", obs: "Debug.inspect(Context.deadline($v))", want: "None", noEq: true},
	{name: "list context", inspect: "[<context>]", typ: "List<Context>", expr: "[Context.root()]", obs: "Debug.inspect(Iter.count($v))", want: "1", noEq: true},
	{name: "derived struct", decls: "struct Dp\n    x: Int\nend\n\nderive Equatable for Dp\n\nderive Hashable for Dp", typ: "Dp", expr: "Dp{x: 1}", want: "Dp{x: 1}"},
	{name: "hand-written equality", decls: "struct Loose\n    id: Int\n    note: String\nend\n\nimpl Equatable for Loose\n    fn equal(a: Loose, b: Loose) -> Bool\n        a.id == b.id\n    end\nend",
		typ: "Loose", expr: `Loose{id: 1, note: "a"}`, want: `Loose{id: 1, note: "a"}`},
	{name: "result unit", typ: "Result<Unit, String>", expr: "Ok(Unit)", want: "Ok(Unit)"},
	{name: "result marker pair", decls: "type Mk", typ: "Result<Mk, ChannelClosed>", expr: "Ok(Mk)", want: "Ok(Mk)", imports: []string{"std::channels.ChannelClosed"}},
}

const (
	toHolder = "struct Holder$P\n    f: $T\n    n: Int\nend"
	toWrap   = "enum Wrap$P\n    Has $T\n    Empty\nend"
	toRec    = "enum Rec$P\n    Has {f: $T, n: Int}\n    Empty\nend"
	toDuo    = "enum Duo$P\n    Both ($T, Int)\n    Neither\nend"
)

var toOps = []toOp{
	{name: "identity", body: "out = $E", core: true},
	{name: "tuple", body: "t = ($E, 1)\nassert t.second == 1\n(out, _) = t", core: true},
	{name: "annotated tuple", body: "t: (Int, $T) = (1, $E)\nout = t.second"},
	{name: "tuple3 middle", body: "t = (1, $E, \"z\")\n(_, out, _) = t"},
	{name: "anonymous struct", body: "s = {f: $E, n: 1}\nout = s.f", core: true},
	{name: "anonymous struct pattern", body: "s: {f: $T, n: Int} = {f: $E, n: 1}\n{f:, n:} = s\nassert n == 1\nout = f"},
	{name: "named struct field", decls: toHolder, body: "h = Holder{f: $E, n: 1}\nout = h.f", core: true},
	{name: "named struct pattern", decls: toHolder, body: "Holder{f:, n:} = Holder{f: $E, n: 1}\nassert n == 1\nout = f"},
	{name: "struct update", decls: toHolder, body: "h = Holder{f: $E, n: 1}\nh2 = {..h, n: .. + 1}\nh3 = {..h2, f: $E}\nassert h3.n == 2\nout = h3.f", core: true},
	{name: "generic struct", decls: "struct Box<A>\n    v: A\nend", body: "b = Box{v: $E}\nout = b.v"},
	{name: "list", body: "xs = [$E, $E]\nassert Iter.count(xs) == 2\nout = List.head(xs)!", core: true},
	{name: "list pattern", body: "out = case [$E, $E]\n    [x, _] -> x\n    _ -> $E\nend"},
	{name: "vector", body: "xs: Vector<$T> = [$E, $E]\nout = Vector.get(xs, 1)!", core: true},
	{name: "set", body: "s: Set<$T> = [$E]\nassert Set.size(s) == 1\nout = List.head(Iter.to_list(s))!", needs: needEq, core: true},
	{name: "named set", body: "s = Set[$E]\nout = List.head(Iter.to_list(s))!", needs: needEq},
	{name: "map key", body: "m = {$E => 1}\nassert Map.get(m, $E) == Some(1)\nout = List.head(Map.keys(m))!", needs: needEq, core: true},
	{name: "map value", body: "m = {\"k\" => $E}\nout = Map.get(m, \"k\")!", core: true},
	{name: "enum payload case", decls: toWrap, body: "w: Wrap$P = .Has($E)\nout = case w\n    .Has(x) -> x\n    .Empty -> $E\nend", core: true},
	{name: "enum payload else", decls: toWrap, body: "w = Wrap.Has($E)\nWrap.Has(out) = w else return Ok(Unit)"},
	{name: "maybe case", body: "m = Some($E)\nout = case m\n    Some(x) -> x\n    None -> $E\nend"},
	{name: "maybe else", body: "Some(out) = Some($E) else return Ok(Unit)"},
	{name: "equality", body: "a = $E\nb = $E\nassert a == b\nassert not (a != b)\nout = a", needs: needEq, core: true},
	{name: "equality in tuple", body: "assert ($E, 1) == ($E, 1)\nout = $E", needs: needEq},
	{name: "equality in list", body: "assert [$E] == [$E]\nout = $E", needs: needEq},
	{name: "equality in maybe", body: "assert Some($E) == Some($E)\nout = $E", needs: needEq},
	{name: "hash", body: "assert Hashable.hash($E) == Hashable.hash($E)\nout = $E", needs: needEq},
	{name: "interpolation", body: "s = \"<${$E}>\"\nassert s == \"<$S>\"\nout = $E", needs: needDisplay, core: true},
	{name: "inspect", body: "assert Debug.inspect($E) == \"$I\"\nassert Debug.inspect([$E]) == \"[$I]\"\nout = $E", core: true},
	{name: "display to_string", body: "assert Display.to_string($E) == \"$S\"\nout = $E", needs: needDisplay},
	{name: "generic identity", decls: "fn ident<A>(x: A) -> A\n    x\nend", body: "out = ident($E)", core: true},
	{name: "generic list", decls: "fn first_of<A>(xs: List<A>) -> A\n    List.head(xs)!\nend", body: "out = first_of([$E])"},
	{name: "generic equatable", decls: "fn same<A>(a: A, b: A) -> Bool where A: Equatable\n    a == b\nend", body: "assert same($E, $E)\nout = $E", needs: needEq},
	{name: "generic display", decls: "fn show<A>(a: A) -> String where A: Display\n    \"${a}\"\nend", body: "assert show($E) == \"$S\"\nout = $E", needs: needDisplay},
	// A generic call expected to be an interface takes its type argument
	// from its arguments, and its result converts (spec §5 *Type arguments
	// from the arguments*): the lambda sees $T, not Display.
	{name: "args-first loop into Display", body: "d: Display = Iter.loop($E, fn s -> if True then break s else s)\nassert \"${d}\" == \"$S\"\nout = $E", needs: needDisplay},
	{name: "args-first filter into Iter<Display>", body: "ds: Iter<Display> = [$E] |> Iter.filter(fn x -> x == x)\nassert (Iter.map(ds, Display.to_string) |> Iter.to_list()) == [\"$S\"]\nout = $E", needs: needEq | needDisplay},
	{name: "closure capture", body: "v = $E\ng = fn -> v\nout = g()", core: true},
	{name: "lambda in map", body: "v = $E\nout = [1] |> Iter.map(fn _ -> v) |> Iter.to_list() |> List.head()!", core: true},
	{name: "Iter.map", body: "out = [$E, $E] |> Iter.map(fn x -> x) |> Iter.to_list() |> List.head()!", core: true},
	{name: "Iter.filter", body: "ys = [$E, $E] |> Iter.filter(fn _ -> True) |> Iter.to_list()\nassert Iter.count(ys) == 2\nout = List.head(ys)!"},
	{name: "Iter.with_index", body: "ys = [$E, $E] |> Iter.with_index() |> Iter.to_list()\n(i, out) = List.head(ys)!\nassert i == 0", core: true},
	{name: "Iter.zip", body: "ys = Iter.zip([$E], [1]) |> Iter.to_list()\n(out, n) = List.head(ys)!\nassert n == 1"},
	{name: "Iter.chunks", body: "ys = [$E, $E, $E] |> Iter.chunks(2) |> Iter.to_list()\nassert Iter.count(ys) == 2\nout = List.head(List.head(ys)!)!"},
	{name: "Iter.flatten", body: "ys = [[$E], [$E]] |> Iter.flatten()\nassert Iter.count(ys) == 2\nout = List.head(ys)!", core: true},
	{name: "Iter.to_set", body: "s = [$E, $E] |> Iter.to_set()\nassert Set.size(s) == 1\nout = List.head(Iter.to_list(s))!", needs: needEq},
	{name: "Iter.to_list of vector", body: "ys = Vector[$E] |> Iter.to_list()\nout = List.head(ys)!"},
	{name: "Iter.reduce", body: "out = Iter.reduce([$E, $E], $E, fn _, x -> x)"},
	{name: "channel", imports: strings.Fields(toChans), body: "ch = Channel.buffered<$T>(1)\nassert Ok(_) = Sender.send(ch.sender, $E)\nout = Receiver.receive(ch.receiver)!", core: true},
	{name: "channel across tasks", imports: append(strings.Fields(toChans), "std::tasks.Task"),
		body: "ch = Channel.buffered<$T>(1)\nout = concurrent\n    t = Task.spawn(fn -> Sender.send(ch.sender, $E))\n    got = Receiver.receive(ch.receiver)!\n    _ = Task.await(t)\n    got\nend"},
	{name: "task result", imports: []string{"std::tasks.Task"}, body: "out = concurrent\n    t = Task.spawn(fn -> $E)\n    Task.await(t)\nend", core: true},
	{name: "ok", body: "r: Result<$T, String> = Ok($E)\nassert Ok(out) = r", core: true},
	{name: "err", body: "r: Result<Int, $T> = Err($E)\nassert Err(out) = r", core: true},
	{name: "some", body: "m = Some($E)\nassert Some(out) = m"},
	{name: "unannotated ok", body: "r = Ok($E)\nassert Ok(out) = r"},
	{name: "some of tuple", body: "m = Some(($E, 1))\nassert Some((out, 1)) = m"},
	{name: "parameter default", decls: "fn pick$P(x: $T, n: Int = 1) -> $T\n    if n > 0 then x else x\nend", body: "out = pick($E)"},
	{name: "named argument", decls: "fn pick$P(x: $T, n: Int = 1) -> $T\n    if n > 0 then x else x\nend", body: "out = pick(n: 2, x: $E)"},
	{name: "if branch", body: "out = if Iter.count([1]) == 1 then $E else $E"},
	{name: "block", body: "out = begin\n    x = $E\n    x\nend"},
	{name: "pipe", decls: "fn ident<A>(x: A) -> A\n    x\nend", body: "out = $E |> ident()"},
	{name: "Iter.find", body: "out = [$E] |> Iter.find(fn _ -> True) |> Maybe.with_default($E)"},
	{name: "Maybe.map", body: "out = Maybe.map(Some(1), fn _ -> $E)!"},
	{name: "Map.put", body: "m: Map<String, $T> = {}\nm2 = Map.put(m, \"k\", $E)\nout = Map.get(m2, \"k\")!"},
	{name: "list of tuples", body: "(out, n) = List.head([($E, 1)])!\nassert n == 1"},
	{name: "Iter.map to tuple", body: "ys = [1] |> Iter.map(fn n -> ($E, n)) |> Iter.to_list()\n(out, _) = List.head(ys)!"},
	// Patterns and branches over a value of any kind.
	{name: "case name", body: "out = case $E\n    x -> x\nend", core: true},
	{name: "case guard", body: "out = case $E\n    x when Iter.count([1]) == 1 -> x\n    _ -> $E\nend"},
	{name: "list else", body: "[out, ..] = [$E] else return Ok(Unit)"},
	{name: "if else None", body: "m = if Iter.count([1]) == 1 then Some($E) else None\nout = m!"},
	{name: "vector pattern", body: "xs: Vector<$T> = [$E]\nout = case xs\n    [x] -> x\n    _ -> $E\nend"},
	{name: "maybe of maybe", body: "assert Some(Some(out)) = Some(Some($E))"},
	{name: "result of tuple", body: "r: Result<($T, Int), String> = Ok(($E, 1))\nassert Ok((out, 1)) = r"},
	{name: "lambda tuple parameter", body: "f = fn (a, _): ($T, Int) -> a\nout = f(($E, 1))"},
	{name: "destructuring parameter", decls: "fn first_of_pair<A>((a, _): (A, Int)) -> A\n    a\nend", body: "out = first_of_pair(($E, 1))"},
	// Patterns that compare or nest around the value, at each place a
	// pattern stands.
	{name: "struct variant pattern", decls: toRec, body: "r: Rec$P = .Has{f: $E, n: 1}\nout = case r\n    .Has{f:, n: 1} -> f\n    .Has{f:, n: _} -> f\n    .Empty -> $E\nend"},
	{name: "struct variant else pattern", decls: toRec, body: "r: Rec$P = .Has{f: $E, n: 1}\nRec.Has{f: out, n: 1} = r else return Ok(Unit)"},
	{name: "positional payload literal pattern", decls: toDuo, body: "d: Duo$P = .Both($E, 1)\nout = case d\n    .Both(x, 1) -> x\n    .Both(x, _) -> x\n    .Neither -> $E\nend"},
	{name: "nested payload pattern", decls: toWrap, body: "w: Wrap$P = .Has($E)\nout = case Some(w)\n    Some(.Has(x)) -> x\n    _ -> $E\nend"},
	{name: "tuple literal pattern", body: "out = case ($E, 1)\n    (x, 1) -> x\n    (x, _) -> x\nend"},
	{name: "nested tuple pattern", body: "out = case (($E, 1), \"s\")\n    ((x, 1), \"s\") -> x\n    ((x, _), _) -> x\nend"},
	{name: "assert tuple pattern", body: "assert (out, 1) = ($E, 1)"},
	{name: "assert or pattern", body: "assert (out, 1) or (out, 2) = ($E, 2)"},
	{name: "else tuple payload pattern", body: "Some((out, 1)) = Some(($E, 1)) else return Ok(Unit)"},
	{name: "else struct pattern", decls: toHolder, body: "Holder{f: out, n: 1} = Holder{f: $E, n: 1} else return Ok(Unit)"},
	{name: "else list pattern", body: "[_, out] = [$E, $E] else return Ok(Unit)"},
	{name: "map pattern", body: "out = case {\"k\" => $E}\n    {\"k\" => x} -> x\n    _ -> $E\nend"},
	{name: "map tuple value pattern", body: "{\"k\" => (out, 1)} = {\"k\" => ($E, 1)} else return Ok(Unit)"},
	{name: "list rest pattern", body: "out = case [$E, $E, $E]\n    [x, .._rest] -> x\n    [] -> $E\nend"},
	{name: "list middle pattern", body: "out = case [$E, $E, $E]\n    [_, ..mid, _] -> List.head(mid)!\n    _ -> $E\nend"},
	{name: "list of tuples pattern", body: "out = case [($E, 1)]\n    [(x, 1)] -> x\n    _ -> $E\nend"},
	{name: "vector rest pattern", body: "xs: Vector<$T> = [$E, $E]\nout = case xs\n    [x, ..] -> x\n    _ -> $E\nend"},
	{name: "pattern if", body: "out = if Some(x) = Some($E) then x else $E"},
	{name: "is pattern", body: "m = Some(($E, 1))\nassert m is Some((_, 1))\nout = $E"},
	{name: "as pattern", body: "out = case Some($E)\n    Some(_) as m -> m!\n    None -> $E\nend"},
	{name: "lambda nested pattern", body: "f = fn ((a, _), _): (($T, Int), Int) -> a\nout = f((($E, 1), 2))"},
	{name: "destructuring parameter before default pattern", decls: "fn pick_first$P((a, _): ($T, Int), n: Int = 1) -> $T\n    if n > 0 then a else a\nend", body: "out = pick_first(($E, 1))"},
	{name: "Iter.loop nested pattern", body: "((out, _), _) = Iter.loop((($E, 1), 0), fn ((w, k), i) -> if i > 2 then break ((w, k), i) else ((w, k), i + 1))"},
	{name: "concurrent tail", imports: []string{"std::tasks.Task"}, body: "out = concurrent\n    t = Task.spawn(fn -> 1)\n    _ = Task.await(t)\n    $E\nend"},
	{name: "nested list", body: "ll = [[$E], [$E, $E]]\nout = List.head(List.head(ll)!)!"},
	{name: "list of maybe", body: "out = List.head([Some($E), None])!!"},
	{name: "capture", body: "f: ($T) -> ($T, Int) = &(&1, 1)\n(out, _) = f($E)"},
	{name: "accessor", body: "out = [($E, 1)] |> Iter.map(.first) |> Iter.to_list() |> List.head()!"},
	// Iter stages and terminals carrying the value.
	{name: "Iter.concat", body: "ys = Iter.concat([$E], [$E]) |> Iter.to_list()\nassert Iter.count(ys) == 2\nout = List.head(ys)!", core: true},
	{name: "Iter.reverse", body: "out = List.head(Iter.reverse([$E, $E]))!"},
	{name: "Iter.windows", body: "ys = [$E, $E, $E] |> Iter.windows(2) |> Iter.to_list()\nout = List.head(List.head(ys)!)!"},
	{name: "Iter.transpose", body: "ys = Iter.transpose([[$E], [$E]])\nout = List.head(List.head(ys)!)!"},
	{name: "Iter.partition", body: "(yes, _) = Iter.partition([$E], fn _ -> True)\nout = List.head(yes)!"},
	{name: "Iter.group_by value", body: "g = Iter.group_by([$E], fn _ -> 1)\nout = List.head(Map.get(g, 1)!)!"},
	{name: "Iter.to_map value", body: "m = [(1, $E)] |> Iter.to_map()\nout = Map.get(m, 1)!"},
	{name: "Iter.scan", body: "out = Iter.scan([1], $E, fn acc, _ -> acc) |> Iter.to_list() |> List.head()!"},
	{name: "Iter.loop", body: "(out, _) = Iter.loop(($E, 0), fn (w, i) -> if i > 2 then break (w, i) else (w, i + 1))"},
	{name: "Iter.repeat", body: "out = Iter.repeat($E) |> Iter.take(1) |> Iter.to_list() |> List.head()!"},
	{name: "Iter.iterate", body: "out = Iter.iterate($E, fn x -> x) |> Iter.take(2) |> Iter.to_list() |> List.head()!"},
	{name: "Iter.intersperse", body: "out = Iter.intersperse([$E, $E], $E) |> Iter.to_list() |> List.head()!"},
	{name: "Iter.cycle", body: "out = Iter.cycle([$E]) |> Iter.take(3) |> Iter.to_list() |> List.head()!"},
	{name: "Iter.permutations", body: "out = Iter.permutations([$E]) |> Iter.to_list() |> List.head() |> Maybe.with_default([]) |> List.head()!"},
	{name: "Iter.chunk_by", body: "ys = [$E, $E] |> Iter.chunk_by(fn _ -> 1) |> Iter.to_list()\nout = List.head(List.head(ys)!)!"},
	{name: "Iter.flat_map", body: "out = [1] |> Iter.flat_map(fn _ -> [$E]) |> Iter.to_list() |> List.head()!"},
	{name: "Iter.last", body: "out = Iter.last([$E, $E])!"},
	{name: "Iter.get", body: "out = Iter.get([$E, $E], 1)!"},
	{name: "Iter.sort_with", body: "out = List.head(Iter.sort_with([$E, $E], fn _, _ -> Ordering.Equal))!"},
	{name: "Iter.min_by", body: "out = Iter.min_by([$E, $E], fn _ -> 1)!"},
	{name: "Iter.map_reduce", body: "(_, ys) = Iter.map_reduce([1], 0, fn acc, _ -> (acc, $E))\nout = List.head(ys)!"},
	{name: "Iter.each", body: "Iter.each([$E], fn _ -> Unit)\nout = $E"},
	{name: "map value iterated", body: "out = {1 => $E} |> Iter.map(fn p -> p.second) |> Iter.to_list() |> List.head()!"},
	{name: "map key iterated", body: "out = {$E => 1} |> Iter.map(fn p -> p.first) |> Iter.to_list() |> List.head()!", needs: needEq},
	{name: "set iterated", body: "out = Set[$E] |> Iter.map(fn x -> x) |> Iter.to_list() |> List.head()!", needs: needEq},
	{name: "Iter.zip with a map", body: "ys = Iter.zip([1], {$E => 1}) |> Iter.to_list()\n(_, (out, _)) = List.head(ys)!", needs: needEq},
	{name: "Iter.contains", body: "assert Iter.contains([$E], $E)\nout = $E", needs: needEq},
	{name: "Iter.frequencies", body: "f = Iter.frequencies([$E, $E])\nassert Iter.count(f) == 1\nout = $E", needs: needEq},
	{name: "inspect in tuple", body: "assert Debug.inspect(($E, 1)) == \"($I, 1)\"\nout = $E", core: true},
	{name: "inspect bound in tuple", body: "v: $T = $E\nassert Debug.inspect((v, 1)) == \"($I, 1)\"\nout = v"},
	{name: "inspect in record", body: "assert Debug.inspect({a: $E}) == \"{a: $I}\"\nout = $E"},
	{name: "spawn_all", imports: []string{"std::tasks.Task"}, body: "out = concurrent\n    ts = Task.spawn_all([1], fn _ -> $E, 1)\n    List.head(Task.await_all(ts))!\nend"},
}

// toCell is one type under one operation.
type toCell struct {
	t  *toType
	op *toOp
}

func (c toCell) name() string { return c.t.name + " × " + c.op.name }

// toFill writes t into an operation's template.
func toFill(s string, t *toType) string {
	p := ""
	if t.generic {
		p = "<T>"
	}
	inspect := t.inspect
	if inspect == "" {
		inspect = t.want
	}
	// $I is the inspect text as the body of a String literal.
	lit := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(inspect)
	return strings.NewReplacer("$E", t.expr, "$T", t.typ, "$P", p, "$S", t.shown, "$I", lit).Replace(s)
}

func toIndent(s, by string) string {
	return by + strings.ReplaceAll(s, "\n", "\n"+by)
}

// source is the cell's program, and the transcript its run must print.
func (c toCell) source() (src, transcript string) {
	t, op := c.t, c.op
	var decls []string
	seen := map[string]bool{}
	for _, d := range []string{t.decls, toFill(op.decls, t)} {
		// Two declarations of one name (a type's Point and an operation's)
		// are one.
		for _, part := range strings.Split(d, "\n\n") {
			if part != "" && !seen[part] {
				seen[part] = true
				decls = append(decls, part)
			}
		}
	}
	obs := t.obs
	if obs == "" {
		obs = "Debug.inspect($v)"
	}
	body := toFill(op.body, t)
	if t.pre != "" {
		body = t.pre + "\n" + body
	}
	var fn string
	if t.generic {
		fn = "fn run<T>(seed: T) -> Result<Unit, AssertionFailure> where T: Equatable and Hashable and Display\n" + toIndent(body, "    ") +
			"\n    io.print(Debug.inspect(out))\n    Ok(Unit)\nend\n\nfn main() -> Result<Unit, AssertionFailure>\n    run(42)\nend"
	} else {
		fn = "fn main() -> Result<Unit, AssertionFailure>\n" + toIndent(body, "    ") + "\n    io.print(" + strings.ReplaceAll(obs, "$v", "out") + ")\n    Ok(Unit)\nend"
	}
	main := strings.Join(append(decls, fn), "\n\n") + "\n"
	return toImports(main, append(append([]string{"std::io", "std::assertions.AssertionFailure"}, t.imports...), op.imports...)) + main,
		t.want + "\n\n--- ok: <nil>"
}

// toImports is the import block for items, each "std::mod" or
// "std::mod.Name", keeping only the names main uses.
func toImports(main string, items []string) string {
	byMod := map[string][]string{}
	var mods []string
	for _, it := range items {
		mod, name, _ := strings.Cut(it, ".")
		if _, ok := byMod[mod]; !ok {
			mods = append(mods, mod)
			byMod[mod] = nil
		}
		if name != "" && regexp.MustCompile(`\b`+name+`\b`).MatchString(main) && !toContains(byMod[mod], name) {
			byMod[mod] = append(byMod[mod], name)
		}
	}
	var lines []string
	for _, mod := range mods {
		names := byMod[mod]
		switch {
		case mod == "std::io":
			// The module itself (io.print), and any type it declares.
			lines = append(lines, mod)
			for _, n := range names {
				lines = append(lines, mod+"."+n)
			}
		case len(names) == 1:
			lines = append(lines, mod+"."+names[0])
		case len(names) > 1:
			lines = append(lines, mod+".{"+strings.Join(names, ", ")+"}")
		}
	}
	return "import\n" + toIndent(strings.Join(lines, "\n"), "    ") + "\nend\n\n"
}

func toContains(xs []string, x string) bool {
	for _, y := range xs {
		if x == y {
			return true
		}
	}
	return false
}

// toRule is a rejection outside the equality and Display columns.
type toRule struct {
	match  func(c toCell) bool
	reject string
	why    string
}

var toRules = []toRule{
	{
		match: func(c toCell) bool {
			partial := strings.HasPrefix(c.t.expr, "Ok(") || strings.HasPrefix(c.t.expr, "Err(")
			return partial && (c.op.name == "generic equatable" || c.op.name == "generic display" || c.op.name == "Iter.contains")
		},
		reject: "nothing names the type its `_` stands for",
		why:    "a bound on a type argument needs the whole type, and `Ok(3)` leaves its error type open",
	},
	{
		match:  func(c toCell) bool { return strings.HasPrefix(c.t.expr, "(fn ") && c.op.name == "parameter default" },
		reject: "missing argument for parameter 'x'",
		why:    "a lambda routes on sight to an empty last parameter, past defaults (spec §7 *Lambdas at Call Sites*)",
	},
	{
		match: func(c toCell) bool {
			partial := strings.HasPrefix(c.t.expr, "Ok(") || strings.HasPrefix(c.t.expr, "Err(")
			return strings.HasPrefix(c.op.name, "args-first") && (c.t.shown == "" || partial)
		},
		reject: "(type mismatch|argument [0-9]+|element type mismatch): expected (Iter<)?Display|cannot compare",
		why:    "the call is typed by its arguments, and its result is no Display, or `Ok(3)` leaves its error type open (spec §5 *Type arguments from the arguments*)",
	},
}

// toRejection is what the checker must say about c, or "" when c must run.
func toRejection(c toCell) (want, why string) {
	for _, r := range toRules {
		if r.match(c) {
			return r.reject, r.why
		}
	}
	if c.op.needs&needEq != 0 && c.t.noEq {
		return toNoEquality, "the operation needs equality, which " + c.t.typ + " lacks"
	}
	if c.op.needs&needDisplay != 0 && c.t.shown == "" {
		return toNoDisplay, "the operation needs Display, which " + c.t.typ + " lacks"
	}
	return "", ""
}

// The checker's texts for a value with no equality (`==` cannot compare
// it, a Set element or Map key cannot be one, an Equatable or Hashable
// bound or impl it does not meet) and for one with no Display.
const (
	toNoEquality = "cannot compare|cannot be a (Set element|Map key)|(implement|impl of|require) `?(Equatable|Hashable)"
	toNoDisplay  = "(implement|impl of|require) `?Display"
)

// The default run checks every cell of a core type under a core operation,
// and one in toSample of the rest.
const toSample = 3

func toCells(full bool) []toCell {
	var cells []toCell
	n := 0
	for i := range toTypes {
		for j := range toOps {
			c := toCell{&toTypes[i], &toOps[j]}
			if full || (c.t.core && c.op.core) || n%toSample == 0 {
				cells = append(cells, c)
			}
			if !c.t.core || !c.op.core {
				n++
			}
		}
	}
	return cells
}

// checkTypeOpCell checks and runs one cell, as checkCellAlone does a
// feature matrix cell.
func checkTypeOpCell(dir string, c toCell) mxOutcome {
	src, transcript := c.source()
	var p *Program
	lo := checkLowersKeeping(dir, src, &p)
	wantReject, why := toRejection(c)
	if o, done := mxCheckOutcome(lo, wantReject, why, ""); done {
		return o
	}
	plan := mxPlan(p)
	if plan.main == nil {
		return mxOutcome{class: "crash", detail: "nothing ran: main is not retained"}
	}
	run := runOnce(p, plan, dir, nil)
	if o, done := mxRunFailure(run); done {
		return o
	}
	if run.transcript != transcript {
		return mxOutcome{class: "wrong output", detail: fmt.Sprintf("want transcript:\n%s\ngot:\n%s", transcript, run.transcript)}
	}
	if img := builtImage(p); img != nil {
		third := runOnce(p, plan, dir, img)
		if o, done := mxRunFailure(third); done {
			return o
		}
		if third.transcript != run.transcript {
			return mxOutcome{class: "crash", detail: crashImage + ": the built image prints:\n" + third.transcript + "\nthe source prints:\n" + run.transcript}
		}
	}
	return mxOutcome{class: "ok"}
}

// TestTypeOperationGrid checks every core cell and a sample of the rest, or
// every cell under NOMI_TYPE_OPS=full; see the comment at the top of this
// file.
func TestTypeOperationGrid(t *testing.T) {
	t.Setenv("NOMI_DEBUG_LOWERING", "1")
	full := os.Getenv("NOMI_TYPE_OPS") == "full"
	cells := toCells(full)
	only := os.Getenv("NOMI_TYPE_OPS_ONLY")
	var picked []toCell
	for _, c := range cells {
		if only == "" || strings.Contains(c.name(), only) {
			picked = append(picked, c)
		}
	}
	t.Logf("type × operation grid: %d types, %d operations, %d cells, checking %d", len(toTypes), len(toOps), len(toTypes)*len(toOps), len(picked))
	if err := std.Shared().AllBodies(); err != nil {
		t.Fatalf("the stdlib's bodies do not check: %v", err)
	}
	start := time.Now()
	outcomes := make([]mxOutcome, len(picked))
	dirs := make([]string, len(picked))
	for i := range picked {
		dirs[i] = t.TempDir()
	}
	next := make(chan int)
	var wg sync.WaitGroup
	for range runtime.GOMAXPROCS(0) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				o := checkTypeOpCell(dirs[i], picked[i])
				if o.class != "rejected" && o.class != "ok" {
					if gap := knownTypeOpGapFor(picked[i], o); gap != nil {
						o.gap = gap.name
					}
				}
				outcomes[i] = o
			}
		}()
	}
	for i := range picked {
		next <- i
	}
	close(next)
	wg.Wait()
	tally := map[string]int{}
	for i, c := range picked {
		o := outcomes[i]
		key := o.class
		if o.gap != "" {
			key += " (known: " + o.gap + ")"
		}
		tally[key]++
		if !o.failed() {
			continue
		}
		src, _ := c.source()
		t.Errorf("%s: %s:\n%s\n%s", c.name(), o.class, numbered(src), o.detail)
	}
	var keys []string
	for k := range tally {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "\n  %s: %d", k, tally[k])
	}
	t.Logf("%d cells in %s:%s", len(picked), time.Since(start).Round(time.Millisecond), b.String())
}

// typeOpSeeds are the grid's cells FuzzLoweredProgramsRun starts from when
// fuzzing: every cell of a type and an operation that a known gap names
// (the failing cells, and their neighbours that run), and every core type
// under every core operation that must run.
func typeOpSeeds() []loweringSeed {
	var seeds []loweringSeed
	for _, c := range toCells(true) {
		pinned := false
		for _, g := range knownTypeOpGaps {
			if g.cells.MatchString(c.name()) {
				pinned = true
			}
		}
		if want, _ := toRejection(c); !pinned && (want != "" || !c.t.core || !c.op.core) {
			continue
		}
		src, _ := c.source()
		seeds = append(seeds, loweringSeed{name: "typeop/" + c.name(), src: src})
	}
	return seeds
}
