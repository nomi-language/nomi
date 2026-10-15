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
