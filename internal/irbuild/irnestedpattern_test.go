package irbuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// irRetainedFuncNames lowers src as main.nomi and answers the names of the
// functions its module retained.
func irRetainedFuncNames(t *testing.T, src string) map[string]bool {
	t.Helper()
	path := filepath.Join(t.TempDir(), "main.nomi")
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatalf("the front end rejects the fixture, so nothing below is tested: %v", err)
	}
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, m := range res.IR {
		if m.Name() != path {
			continue
		}
		for _, f := range m.Funcs() {
			names[f.Name()] = true
		}
	}
	return names
}

const irNestedPatternSource = `import std/io

enum Shape {
  Circle Int
  Square Int
  Dot
  Named String
  Box {w: Int, h: Int}
}

fn classify(m: Maybe<Shape>): Int {
  case m {
    Some(Shape.Circle(r)) -> r * 3
    Some(Shape.Square(4)) -> 100
    Some(Shape.Square(s)) -> s * s
    Some(.Named("x")) -> 7
    Some(Shape.Box{w, h}) -> w * h
    Some(Shape.Dot) -> 0
    Some(_) -> 1
    None -> -1
  }
}

fn outcome(r: Result<Shape, String>): String {
  case r {
    Ok(Shape.Circle(_)) -> "circle"
    Ok(other) -> "other"
    Err(e) -> e
  }
}

fn main() {
  io.print(classify(Some(Shape.Circle(2))))
  io.print(classify(Some(Shape.Square(4))))
  io.print(classify(Some(Shape.Square(5))))
  io.print(classify(Some(Shape.Named("x"))))
  io.print(classify(Some(Shape.Named("y"))))
  io.print(classify(Some(Shape.Box{w: 2, h: 9})))
  io.print(classify(Some(Shape.Dot)))
  io.print(classify(None))
  io.print(outcome(Ok(Shape.Circle(1))))
  io.print(outcome(Ok(Shape.Dot)))
  io.print(outcome(Err("bad")))
}
`

// A variant pattern nested in a prelude constructor's payload tests the outer
// tag, projects the payload and tests the inner tag in the success arm, as
// native's matchArm recursion does. A failed inner test proceeds to the next
// arm.
func TestIRNestedPattern_VariantInsidePreludePayload(t *testing.T) {
	names := irRetainedFuncNames(t, irNestedPatternSource)
	for _, fn := range []string{"classify", "outcome", "main"} {
		if !names[fn] {
			t.Errorf("%s was not retained", fn)
		}
	}
	verifyLambdaProgram(t, irNestedPatternSource,
		"6\n100\n25\n7\n1\n18\n0\n-1\ncircle\nother\nbad\n")
}

const irChannelFieldSource = `import {
  std/channels.{Channel, Receiver, Sender}
  std/io
}

enum Request {
  Increment Int
  Get {reply: Channel<Int>}
  Explode
}

struct Counter {
  inbox: Channel<Request>
}

fn increment(counter: Counter, n: Int): Unit {
  case Sender.send(counter.inbox.sender, Request.Increment(n)) {
    Ok(_) -> Unit
    Err(_) -> Unit
  }
}

fn ask(counter: Counter, reply: Channel<Int>): Unit {
  _ = Sender.send(counter.inbox.sender, Request.Get{reply})
  Unit
}

fn handle(counter: Counter, count: Int): Int {
  case Receiver.receive(counter.inbox.receiver) {
    Some(Request.Increment(n)) -> count + n
    Some(Request.Get{reply}) -> {
      _ = Sender.send(reply.sender, count)
      count
    }
    Some(Request.Explode) -> count / 0
    None -> -1
  }
}

fn main() {
  counter = Counter{inbox: Channel.buffered<Request>(8)}
  reply = Channel.buffered<Int>(1)
  increment(counter, 5)
  increment(counter, 7)
  ask(counter, reply)
  a = handle(counter, 0)
  b = handle(counter, a)
  c = handle(counter, b)
  io.print(c)
  case Receiver.receive(reply.receiver) {
    Some(v) -> io.print(v)
    None -> io.print("none")
  }
}
`

// A struct field holding a Channel of an enum whose struct-shaped variant
// holds a Channel of Int: construction, field reads through the halves, and
// the nested patterns receiving them.
func TestIRNestedPattern_ChannelFieldsInStructsAndVariants(t *testing.T) {
	names := irRetainedFuncNames(t, irChannelFieldSource)
	for _, fn := range []string{"increment", "ask", "handle", "main"} {
		if !names[fn] {
			t.Errorf("%s was not retained", fn)
		}
	}
	verifyLambdaProgram(t, irChannelFieldSource, "12\n12\n")
}

// The corpus's message_loop runs its four grouped cases through the VM: a
// struct holding a Channel of an enum whose struct-shaped variant holds a
// Channel, nested variant patterns in an `Iter.loop` case, and
// `Supervisor.new(..., restart:, backoff:)` piped into `Supervisor.spawn`.
func TestIRNestedPattern_MessageLoopCorpusProgram(t *testing.T) {
	dir := filepath.Join("..", "..", "tests", "16-concurrency", "message_loop")
	src, err := os.ReadFile(filepath.Join(dir, "message_loop_test.nomi"))
	if err != nil {
		t.Fatal(err)
	}
	// The entry file whose boot the group's `boot` line calls.
	app, err := os.ReadFile(filepath.Join(dir, "counter_app.nomi"))
	if err != nil {
		t.Fatal(err)
	}
	run := irTestGroupVMFiles(t, string(src), map[string]string{"counter_app.nomi": string(app)}, 4)
	// The server's functions and its boot are counter_app.nomi's.
	retained := map[string]bool{}
	for _, m := range run.all {
		for _, f := range m.Funcs() {
			retained[f.Name()] = true
		}
	}
	for _, fn := range []string{"start", "serve", "increment", "get", "explode", "boot"} {
		if !retained[fn] {
			t.Errorf("%s was not retained", fn)
		}
	}
	if !strings.Contains(run.vm.stdout, "4 passed") {
		t.Errorf("the VM report lacks 4 passed:\n%s", run.vm.stdout)
	}
	// irTestGroupVM holds the grouped cases to the walk; the retained
	// functions' read-back is held to it here.
}

const irSpawnCallSupervisorSource = `import {
  std/channels.{Channel, Receiver, Sender}
  std/duration.Duration
  std/supervisors.{Restart, Supervisor}
  std/testing.Clock
}

pub struct App {
  context: Context
  done: Channel<Int>
}

fn group(timeout: Duration): Supervisor {
  Supervisor.new(shutdown_timeout: timeout, max_running: 2, restart: Restart.Temporary)
}

fn start(done: Channel<Int>): Unit {
  _ = Supervisor.spawn(group(Duration.seconds(1)), || report(done, 1))
  _ = group(Duration.seconds(2)) |> Supervisor.spawn(|| report(done, 2))
  Unit
}

fn report(done: Channel<Int>, n: Int): Unit {
  _ = Sender.send(done.sender, n)
  Unit
}

pub fn boot(): App {
  done = Channel.buffered<Int>(4)
  start(done)
  App{context: Context.root(), done}
}

fn main() {}

tests "spawn over a called supervisor" {
  clock Clock.Virtual

  boot boot()

  test "both tasks report" {
    a = Receiver.receive(App.done.receiver)
    b = Receiver.receive(App.done.receiver)
    assert a != b
  }
}
`

// A supervisor argument that is a call, written directly or piped, is
// evaluated after the task body's func literal, as native writes it inline.
func TestIRNestedPattern_SpawnOverACalledSupervisor(t *testing.T) {
	run := irTestGroupVM(t, irSpawnCallSupervisorSource, 1)
	retained := map[string]bool{}
	for _, f := range run.module.Funcs() {
		retained[f.Name()] = true
	}
	if !retained["start"] {
		t.Error("start was not retained")
	}
}

// Shapes left native: an enum whose channel field carries the enum itself,
// which the channel-field rule declines rather than recursing into. It keeps
// the walk's Sources.
func TestIRNestedPattern_BoundariesPreserveFallback(t *testing.T) {
	for _, tc := range []struct{ name, fn, why, src string }{
		{"self-carrying channel", "pick", "an ident kind outside the domain: Channel<Request>", `import std/channels.{Channel, Receiver}

enum Request {
  Ask {reply: Channel<Request>}
  Stop
}

fn pick(inbox: Channel<Request>): Int {
  case Receiver.receive(inbox.receiver) {
    Some(Request.Stop) -> 1
    Some(_) -> 2
    None -> 0
  }
}

fn main(): Int { pick(Channel.buffered<Request>(1)) }
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := AnalyzeSource("main.nomi", tc.src)
			if err != nil {
				t.Fatalf("the front end rejects the fixture, so nothing below is tested: %v", err)
			}
			var why []string
			prev := IRDeclineObserved
			IRDeclineObserved = func(fn, reason string) {
				if fn == tc.fn {
					why = append(why, reason)
				}
			}
			got, _, err := GenerateIR(p)
			IRDeclineObserved = prev
			if err != nil {
				t.Fatal(err)
			}
			if len(why) == 0 || why[0] != tc.why {
				t.Errorf("%s declined for %q; want %q", tc.fn, why, tc.why)
			}
			for _, mod := range got.IR {
				for _, f := range mod.Funcs() {
					if f.Name() == tc.fn {
						t.Fatalf("%s was retained", tc.fn)
					}
				}
			}
		})
	}
}

// A Float field sub-pattern inside a nested struct-shaped variant is a literal
// test on the projected field, as an Int one is: rt.Equal compares it.
func TestIRNestedPattern_FloatFieldSubPatternRuns(t *testing.T) {
	verifyDeferProgram(t, `import std/io

enum Shape {
  Box {w: Float, h: Int}
  Dot
}

fn pick(m: Maybe<Shape>): Int {
  case m {
    Some(Shape.Box{w: 2.0, h}) -> h
    Some(_) -> 0
    None -> -1
  }
}

fn main() {
  io.print(pick(Some(Shape.Box{w: 2.0, h: 7})))
  io.print(pick(Some(Shape.Box{w: 2.5, h: 7})))
  io.print(pick(Some(Shape.Dot)))
  io.print(pick(None))
}
`, "7\n0\n0\n-1\n", "pick", "main")
}

// A nested variant pattern in `assert pattern = value` shares the case test:
// the outer tag, the payload, then the inner tag, each failure reaching the
// mismatch report.
func TestIRNestedPattern_PatternAssertion(t *testing.T) {
	out := irPatternAssertVM(t, `enum Shape {
  Circle Int
  Dot
}

fn pick(n: Int): Maybe<Shape> {
  if n > 0 { Some(Shape.Circle(n)) } else { Some(Shape.Dot) }
}

test "a nested pattern binds the inner payload" {
  assert Some(Shape.Circle(r)) = pick(3)
  assert r == 3
}

test "an inner tag mismatch fails the assertion" {
  assert Some(Shape.Circle(r)) = pick(0)
  assert r == 0
}
`, 2)
	if !strings.Contains(out, "1 passed, 1 failed") {
		t.Errorf("the report lacks one pass and one failure:\n%s", out)
	}
}

// A supervisor operand that is a call to a function reading an app field retains, with
// native's Go: the call is spelled inline in the spawn, after the task body's
// func literal.
func TestIRNestedPattern_SpawnOverACalledAppSupervisor(t *testing.T) {
	src := `import {
  std/io
  std/supervisors.{Supervisor}
}
pub struct App {
  context: Context
  s: Supervisor
}
fn boot(): App {
  App{context: Context.root(), s: Supervisor.new(max_running: 1)}
}
fn pick(): Supervisor {
  App.s
}
fn spawn_into(): Unit {
  _ = Supervisor.spawn(pick(), || io.print("ran"))
  Unit
}
fn main() {
  spawn_into()
  _ = Supervisor.flush(App.s)
  Unit
}
`
	if names := irRetainedFuncNames(t, src); !names["spawn_into"] {
		t.Fatal("spawn_into was not retained")
	}
	verifyLambdaProgram(t, src, "ran\n")
}

// A called supervisor operand spread over several lines keeps native's line
// map: the task body's func literal is written where the supervisor call's
// operands left the cursor, whether the call is piped or an argument. The
// tasks' own order is the scheduler's, so they print nothing.
func TestIRNestedPattern_SpawnOverAMultilineCalledSupervisor(t *testing.T) {
	src := `import {
  std/duration.Duration
  std/io
  std/supervisors.{Backoff, Restart, Supervisor}
}
pub struct App {
  context: Context
  s: Supervisor
}
fn boot(): App {
  start()
  App{context: Context.root(), s: Supervisor.new(max_running: 1)}
}
fn group(timeout: Duration): Supervisor {
  Supervisor.new(shutdown_timeout: timeout, max_running: 1)
}
fn start(): Unit {
  _ =
    Supervisor.new(
      shutdown_timeout: Duration.seconds(5),
      max_running: 1,
      restart: Restart.Temporary,
      backoff: Backoff.Exponential{
        max_restarts: 3,
        max_elapsed: Duration.minutes(5),
      },
    )
    |> Supervisor.spawn(|| idle(1))
  _ = Supervisor.spawn(
    group(Duration.seconds(1)),
    || idle(2),
  )
  Unit
}
fn idle(n: Int): Unit {
  _ = n
  Unit
}
fn main() {
  _ = Supervisor.flush(App.s)
  io.print("done")
}
`
	if names := irRetainedFuncNames(t, src); !names["start"] {
		t.Fatal("start was not retained")
	}
	verifyLambdaProgram(t, src, "done\n")
}
