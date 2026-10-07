package irbuild

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/vm"
)

func TestIRConcurrency_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"concurrency.md:L31", `import std/tasks.Task

fn double(n: Int): Int {
  n * 2
}

fn main() {
  total = concurrent {
    a = Task.spawn(|| double(10))
    b = Task.spawn(|| double(20))
    c = Task.spawn(|| double(30))
    Task.await(a) + Task.await(b) + Task.await(c)
  }
  dbg total
}
`, "dbg line 14: total = 120\n"},
		{"concurrency.md:L79", `import {
  std/channels.{Channel, Receiver, Sender}
  std/io
  std/tasks.Task
}

fn produce(out: Sender<Int>, n: Int): Unit {
  case Sender.send(out, n) {
    Ok(_) -> Unit
    Err(_) -> io.print("send failed")
  }
}

fn consume(inbox: Receiver<Int>): Int {
  Iter.loop(|sum = 0|
    case Receiver.receive(inbox) {
      Some(v) -> sum + v
      None -> break sum
    }
  )
}

fn main() {
  ch = Channel.buffered<Int>(4)

  total = concurrent {
    p1 = Task.spawn(|| produce(ch.sender, 10))
    p2 = Task.spawn(|| produce(ch.sender, 20))
    p3 = Task.spawn(|| produce(ch.sender, 30))
    cons = Task.spawn(|| consume(ch.receiver))

    // Drain the producers, close so the consumer's loop ends on None.
    Task.await(p1)
    Task.await(p2)
    Task.await(p3)
    Sender.close(ch.sender)
    Task.await(cons)
  }

  dbg total
}
`, "dbg line 40: total = 60\n"},
		{"concurrency.md:L159", `import {
  std/tasks.Task
  std/duration.Duration
  std/timer
}

fn short_fail(): Result<Int, String> {
  timer.sleep(Duration.milliseconds(10))
  Err("boom")
}

fn long_sleep(): Result<Int, String> {
  timer.sleep(Duration.seconds(5))
  Ok(0)
}

fn main() {
  outcome = concurrent {
    short = Task.spawn(|| short_fail())
    long = Task.spawn(|| long_sleep())
    n = try Task.await(short)
    _ = try Task.await(long)
    Ok(n)
  }

  _ = dbg outcome
}
`, "dbg line 26: outcome = Err(\"boom\")\n"},
		{"concurrency.md:L243", `import {
  std/tasks.Task
}

fn active?(id: Int): Bool {
  id != 2
}

fn fetch_user(id: Int): String {
  "user-${Display.to_string(id)}"
}

fn main() {
  results = concurrent {
    [3, 2, 1]
    |> Iter.filter(active?)
    |> Task.spawn_all(fetch_user, max_running: 8)
    |> Task.await_all()
  }
  dbg results
}
`, "dbg line 20: results = [\"user-3\", \"user-1\"]\n"},
		{"concurrency.md:L650", `import {
  std/tasks.Task
  std/duration.Duration
  std/timer
}

fn serve_conn(): Int {
  // Would take half a minute if left alone.
  timer.sleep(Duration.seconds(30))
  1
}

fn main() {
  outcome = concurrent {
    conn = Task.spawn(|| serve_conn())
    Task.cancel(conn)
    // ` + "`outcome`, not `await`" + ` — inheriting a cancellation you asked for
    // makes no sense.
    Task.outcome(conn)
  }
  dbg outcome
}
`, "dbg line 21: outcome = Cancelled\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}

// Focused programs for the mechanisms the Tour programs use: captures into a
// block and its tasks, a branching block tail, nested blocks, a completed
// outcome, a Maybe try, a lambda batch body and the scalar Display spellings.
func TestIRConcurrency_Mechanisms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"captures, String tasks and a conditional tail", `import std/tasks.Task

fn greet(name: String, n: Int): String {
  "${name}-${Display.to_string(n)}"
}

fn main() {
  base = 40
  who = "ada"
  label = concurrent {
    a = Task.spawn(|| greet(who, base + 1))
    b = Task.spawn(|| base * 2)
    total = Task.await(b)
    if total > 50 { Task.await(a) } else { "small" }
  }
  dbg label
  Unit
}
`, "dbg line 16: label = \"ada-41\"\n"},
		{"nested blocks and a Unit task", `import {
  std/io
  std/tasks.Task
}

fn inner(n: Int): Int {
  concurrent {
    t = Task.spawn(|| n + 1)
    Task.await(t)
  }
}

fn main() {
  outer = concurrent {
    a = Task.spawn(|| inner(1))
    b = Task.spawn(|| inner(2))
    Task.await(a) * Task.await(b)
  }
  _ = concurrent {
    t = Task.spawn(|| io.print("from a task"))
    Task.await(t)
  }
  dbg outer
  Unit
}
`, "from a task\ndbg line 23: outer = 6\n"},
		{"a completed outcome and a Maybe try", `import std/tasks.Task

fn half(n: Int): Maybe<Int> {
  if n % 2 == 0 { Some(n / 2) } else { None }
}

fn main() {
  outcome = concurrent {
    t = Task.spawn(|| 21 * 2)
    Task.outcome(t)
  }
  dbg outcome
  found = concurrent {
    a = Task.spawn(|| half(10))
    b = Task.spawn(|| half(7))
    x = try Task.await(a)
    y = try Task.await(b)
    Some(x + y)
  }
  dbg found
  Unit
}
`, "dbg line 12: outcome = Completed(42)\ndbg line 20: found = None\n"},
		{"a lambda batch body over a mapped sequence", `import std/tasks.Task

fn main() {
  offset = 100
  results = concurrent {
    [1, 2, 3, 4]
    |> Iter.map(|n| n * 10)
    |> Task.spawn_all(|n| n + offset, max_running: 2)
    |> Task.await_all()
  }
  dbg results
  Unit
}
`, "dbg line 11: results = [110, 120, 130, 140]\n"},
		{"an unbuffered channel, a send after close and a drained receive", `import {
  std/channels.{Channel, ChannelClosed, Receiver, Sender}
  std/io
  std/tasks.Task
}

fn report(r: Result<Unit, ChannelClosed>): String {
  case r {
    Ok(_) -> "sent"
    Err(_) -> "closed"
  }
}

fn main() {
  ch = Channel.unbuffered<String>()
  got = concurrent {
    t = Task.spawn(|| Receiver.receive(ch.receiver))
    first = report(Sender.send(ch.sender, "hi"))
    Sender.close(ch.sender)
    io.print(first)
    Task.await(t)
  }
  dbg got
  io.print(report(Sender.send(ch.sender, "late")))
  dbg Receiver.receive(ch.receiver)
  Unit
}
`, "sent\ndbg line 23: got = Some(\"hi\")\nclosed\ndbg line 25: Receiver.receive(ch.receiver) = None\n"},
		{"loop case tails with guards and a wildcard break", `fn pick(limit: Int): Int {
  Iter.loop(|n = 0| {
    if n > limit { break n * 10 }
    case n {
      0 -> n + 1
      _ -> n + 2
    }
  })
}

fn find(): Int {
  Iter.loop(|n = 1|
    case n {
      1 -> 2
      2 -> 5
      _ -> break n
    }
  )
}

fn classify(_xs: Int): String {
  Iter.loop(|s = ""| {
    if String.length(s) > 8 { break "long" }
    case String.length(s) {
      3 -> break s
      _ -> s + "ab"
    }
  })
}

fn main() {
  dbg pick(5)
  dbg classify(0)
  dbg find()
  Unit
}
`, "dbg line 32: pick(5) = 70\ndbg line 33: classify(0) = \"long\"\ndbg line 34: find() = 5\n"},
		{"scalar Display calls", `import std/io

fn show(n: Int, x: Float, b: Bool, s: String): String {
  Display.to_string(n) + " " + Display.to_string(x) + " " + Display.to_string(b) + " " + Display.to_string(s)
}

fn main() {
  io.print(show(7, 1.5, True, "s"))
  io.print("n=${Display.to_string(3)}")
}
`, "7 1.5 True s\nn=3\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}

// Concurrent shapes the builder does not lower are declined: the enclosing function
// is not retained.
func TestIRConcurrency_UnsupportedShapesPreserveEmission(t *testing.T) {
	// A try inside a task body leaves the task's lambda, whose boundary it
	// is.
	verifyLambdaProgram(t, "import {\n std/io\n std/tasks.Task\n}\nfn run(): Int {\nr = concurrent {\n t = Task.spawn(|| {\n x = try Some(2)\n Some(x)\n })\n Task.await(t)\n }\n Maybe.with_default(r, 0)\n}\n"+
		"fn missing(): Maybe<Int> { None }\nfn none(): Int {\nr = concurrent {\n t = Task.spawn(|| {\n x = try missing()\n Some(x)\n })\n Task.await(t)\n }\n Maybe.with_default(r, 0)\n}\nfn main() {\n io.print(run())\n io.print(none())\n}\n", "2\n0\n")
	for _, body := range []string{
		// An Iter.loop inside the block.
		"concurrent {\n t = Task.spawn(|| 1)\n n0 = Task.await(t)\n Iter.loop(|n = n0| {\n if n > 3 { break n }\n n + 1\n })\n }",
	} {
		p, err := AnalyzeSource("main.nomi", "import std/tasks.Task\nfn double(n: Int): Int { n * 2 }\nfn run(): Int {\n"+body+"\n}\nfn main() { _ = run() }")
		if err != nil {
			t.Fatal(err)
		}
		got, _, err := GenerateIR(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, mod := range got.IR {
			for _, f := range mod.Funcs() {
				if f.Name() == "run" {
					t.Fatalf("unsupported concurrent shape was retained:\n%s", body)
				}
			}
		}
	}
}

// A task whose body reaches a function the producer did not retain fails the
// VM's run rather than settling as a Failed outcome the program can observe:
// `slow_bounded` calls `Context.with_value` over a generic Maybe, whose
// type has no single-name identity to key the store, so the body is not
// retained, and `main` inspects the task's outcome.
func TestIRConcurrency_AMachineLimitInATaskFailsTheRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.nomi")
	src := `import {
  std/tasks.Task
  std/duration.Duration
  std/timer
}

fn slow_bounded(): Int {
  _ = Context.with_value(Run.context, Some(1))
  timer.sleep(Duration.minutes(5))
  1
}

fn main() {
  outcome = concurrent {
    t = Task.spawn(|| slow_bounded())
    Task.outcome(t)
  }
  _ = case outcome {
    .Completed(_) -> "done"
    .Cancelled -> "timed out"
    .Failed(_) -> "broke"
  }
}
struct Run {
  context: Context
}

fn boot(): Run {
  Run{context: Context.root()}
}
`
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatal(err)
	}
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range res.IR {
		for _, f := range m.Funcs() {
			if f.Name() != "main" {
				continue
			}
			var out bytes.Buffer
			_, err := vm.NewProgram(m, res.IRModules(), &out).Run("main")
			if err == nil || !strings.Contains(err.Error(), "did not retain") {
				t.Fatalf("VM run answered %v; want the unretained callee reported", err)
			}
			return
		}
	}
	t.Fatal("main was not retained, so the task never ran on the VM")
}

// A task cancelled while it sleeps unwinds through rt, and the VM runs the
// deferred calls of the activations it leaves. The cancellation can land
// while `Duration.seconds(30)` is being evaluated as the sleep's argument,
// which must not surface as a failed task ("timer.sleep: argument must be a
// Duration").
func TestIRConcurrency_ACancelledTaskRunsItsDeferredCalls(t *testing.T) {
	const want = "cleanup\ndbg line 24: outcome = Cancelled\n"
	path := filepath.Join(t.TempDir(), "main.nomi")
	src := `import {
  std/channels.{Channel, Receiver, Sender}
  std/io
  std/tasks.Task
  std/duration.Duration
  std/timer
}

fn work(started: Sender<Int>): Int {
  defer io.print("cleanup")
  _ = Sender.send(started, 1)
  timer.sleep(Duration.seconds(30))
  1
}

fn main() {
  ch = Channel.buffered<Int>(1)
  outcome = concurrent {
    t = Task.spawn(|| work(ch.sender))
    _ = Receiver.receive(ch.receiver)
    Task.cancel(t)
    Task.outcome(t)
  }
  dbg outcome
  Unit
}
`
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatal(err)
	}
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	ran := false
	for _, m := range res.IR {
		for _, f := range m.Funcs() {
			if f.Name() != "main" {
				continue
			}
			var out bytes.Buffer
			if _, err := vm.NewProgram(m, res.IRModules(), &out).Run("main"); err != nil {
				t.Fatal(err)
			}
			if out.String() != want {
				t.Fatalf("VM output %q; want %q", out.String(), want)
			}
			ran = true
		}
	}
	if !ran {
		t.Fatal("main was not retained")
	}
	if got := vmReference(path); got.exit != 0 || got.stdout != want || got.stderr != "" {
		t.Fatalf("vm command: %s; want %q", got, want)
	}
}
