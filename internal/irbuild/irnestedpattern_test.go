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

const irNestedPatternSource = `import std::io

enum Shape
  Circle Int
  Square Int
  Dot
  Named String
  Box {w: Int, h: Int}
end

fn classify(m: Maybe<Shape>) -> Int
  case m
    Some(Shape.Circle(r)) -> r * 3
    Some(Shape.Square(4)) -> 100
    Some(Shape.Square(s)) -> s * s
    Some(.Named("x")) -> 7
    Some(Shape.Box{w:, h:}) -> w * h
    Some(Shape.Dot) -> 0
    Some(_) -> 1
    None -> -1
  end
end

fn outcome(r: Result<Shape, String>) -> String
  case r
    Ok(Shape.Circle(_)) -> "circle"
    Ok(_other) -> "other"
    Err(e) -> e
  end
end

fn main()
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
end
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

const irChannelFieldSource = `import
  std::channels.{Channel, Receiver, Sender}
  std::io
end

enum Request
  Increment Int
  Get {reply: Channel<Int>}
  Explode
end

struct Counter
  inbox: Channel<Request>
end

fn increment(counter: Counter, n: Int) -> Unit
  case Sender.send(counter.inbox.sender, Request.Increment(n))
    Ok(_) -> Unit
    Err(_) -> Unit
  end
end

fn ask(counter: Counter, reply: Channel<Int>) -> Unit
  _ = Sender.send(counter.inbox.sender, Request.Get{reply:})
  Unit
end

fn handle(counter: Counter, count: Int) -> Int
  case Receiver.receive(counter.inbox.receiver)
    Some(Request.Increment(n)) -> count + n
    Some(Request.Get{reply:}) ->
      _ = Sender.send(reply.sender, count)
      count
    Some(Request.Explode) -> count / 0
    None -> -1
  end
end

fn main()
  counter = Counter{inbox: Channel.buffered<Request>(8)}
  reply = Channel.buffered<Int>(1)
  increment(counter, 5)
  increment(counter, 7)
  ask(counter, reply)
  a = handle(counter, 0)
  b = handle(counter, a)
  c = handle(counter, b)
  io.print(c)
  case Receiver.receive(reply.receiver)
    Some(v) -> io.print(v)
    None -> io.print("none")
  end
end
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

const irSpawnCallSupervisorSource = `import
  std::channels.{Channel, Receiver, Sender}
  std::duration.Duration
  std::supervisors.{Restart, Supervisor}
  std::testing.Clock
end

pub struct App
  context: Context
  done: Channel<Int>
end

fn group(timeout: Duration) -> Supervisor
  Supervisor.new(shutdown_timeout: timeout, max_running: 2, restart: Restart.Temporary)
end

fn start(done: Channel<Int>) -> Unit
  _ = Supervisor.spawn(group(Duration.seconds(1)), fn -> report(done, 1))
  _ = group(Duration.seconds(2)) |> Supervisor.spawn(fn -> report(done, 2))
  Unit
end

fn report(done: Channel<Int>, n: Int) -> Unit
  _ = Sender.send(done.sender, n)
  Unit
end

pub fn boot() -> Result<App, String>
  done = Channel.buffered<Int>(4)
  start(done)
  Ok(App{context: Context.root(), done:})
end

fn main() end

group "spawn over a called supervisor"
  clock Clock.Virtual

  boot boot()

  test "both tasks report"
    a = Receiver.receive(App.done.receiver)
    b = Receiver.receive(App.done.receiver)
    assert a != b
  end
end
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

// An enum whose channel field carries the enum itself, directly or inside a
// composite, is in the value domain (irHandleOnWalk decides the handle's
// element co-inductively): a function taking a channel of it is retained,
// and its nested patterns over a received value run.
func TestIRNestedPattern_SelfCarryingChannelsRun(t *testing.T) {
	for _, tc := range []struct{ name, elem string }{
		{"self-carrying channel", "Request"},
		{"self-carrying channel of a composite", "List<Request>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := `import
  std::channels.{Channel, Receiver, Sender}
  std::io
end

enum Request
  Ask {reply: Channel<` + tc.elem + `>}
  Stop
end

fn pick(inbox: Channel<Request>) -> Int
  case Receiver.receive(inbox.receiver)
    Some(Request.Stop) -> 1
    Some(_) -> 2
    None -> 0
  end
end

fn main()
  inbox = Channel.buffered<Request>(2)
  reply = Channel.buffered<` + tc.elem + `>(1)
  _ = Sender.send(inbox.sender, Request.Ask{reply:})
  _ = Sender.send(inbox.sender, Request.Stop)
  io.print(pick(inbox))
  io.print(pick(inbox))
end
`
			// irRetainedNames fails the test if the front end rejects the
			// source, so a rejection cannot pass for a run.
			verifyDeferProgram(t, src, "2\n1\n", "pick", "main")
		})
	}
}

// A Float field sub-pattern inside a nested struct-shaped variant is a literal
// test on the projected field, as an Int one is: rt.Equal compares it.
func TestIRNestedPattern_FloatFieldSubPatternRuns(t *testing.T) {
	verifyDeferProgram(t, `import std::io

enum Shape
  Box {w: Float, h: Int}
  Dot
end

fn pick(m: Maybe<Shape>) -> Int
  case m
    Some(Shape.Box{w: 2.0, h:}) -> h
    Some(_) -> 0
    None -> -1
  end
end

fn main()
  io.print(pick(Some(Shape.Box{w: 2.0, h: 7})))
  io.print(pick(Some(Shape.Box{w: 2.5, h: 7})))
  io.print(pick(Some(Shape.Dot)))
  io.print(pick(None))
end
`, "7\n0\n0\n-1\n", "pick", "main")
}

// A nested variant pattern in `assert pattern = value` shares the case test:
// the outer tag, the payload, then the inner tag, each failure reaching the
// mismatch report.
func TestIRNestedPattern_PatternAssertion(t *testing.T) {
	out := irPatternAssertVM(t, `enum Shape
  Circle Int
  Dot
end

fn pick(n: Int) -> Maybe<Shape>
  if n > 0 then Some(Shape.Circle(n)) else Some(Shape.Dot)
end

test "a nested pattern binds the inner payload"
  assert Some(Shape.Circle(r)) = pick(3)
  assert r == 3
end

test "an inner tag mismatch fails the assertion"
  assert Some(Shape.Circle(r)) = pick(0)
  assert r == 0
end
`, 2)
	if !strings.Contains(out, "1 passed, 1 failed") {
		t.Errorf("the report lacks one pass and one failure:\n%s", out)
	}
}

// A supervisor operand that is a call to a function reading an app field retains, with
// native's Go: the call is spelled inline in the spawn, after the task body's
// func literal.
func TestIRNestedPattern_SpawnOverACalledAppSupervisor(t *testing.T) {
	src := `import
  std::io
  std::supervisors.{Supervisor}
end
pub struct App
  context: Context
  s: Supervisor
end
fn boot() -> Result<App, String>
  Ok(App{context: Context.root(), s: Supervisor.new(max_running: 1)})
end
fn pick() -> Supervisor
  App.s
end
fn spawn_into() -> Unit
  _ = Supervisor.spawn(pick(), fn -> io.print("ran"))
  Unit
end
fn main()
  spawn_into()
  _ = Supervisor.flush(App.s)
  Unit
end
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
	src := `import
  std::duration.Duration
  std::io
  std::supervisors.{Backoff, Restart, Supervisor}
end
pub struct App
  context: Context
  s: Supervisor
end
fn boot() -> Result<App, String>
  start()
  Ok(App{context: Context.root(), s: Supervisor.new(max_running: 1)})
end
fn group(timeout: Duration) -> Supervisor
  Supervisor.new(shutdown_timeout: timeout, max_running: 1)
end
fn start() -> Unit
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
    |> Supervisor.spawn(fn -> idle(1))
  _ = Supervisor.spawn(
    group(Duration.seconds(1)),
    fn -> idle(2),
  )
  Unit
end
fn idle(n: Int) -> Unit
  _ = n
  Unit
end
fn main()
  _ = Supervisor.flush(App.s)
  io.print("done")
end
`
	if names := irRetainedFuncNames(t, src); !names["start"] {
		t.Fatal("start was not retained")
	}
	verifyLambdaProgram(t, src, "done\n")
}
