package irbuild

import (
	"strings"
	"testing"
)

// Supervisors in retained bodies: the Tour program that spawns into a
// supervisor declared in boot and flushes it before reading the audit
// channel, and focused programs for positional and named short calls to
// std's defaulted `Supervisor.new`, an explicit flush bound, a supervised
// task's await, a constructor boot calls, calendar's defaulted
// Time.new, the prelude `with_default` pair
// and a field read through an app field (`App.settings.label`).
func TestIRSupervisor_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"concurrency.md:L452", `import {
  std/channels.{Channel, Receiver, Sender}
  std/supervisors.{Supervisor}
  std/duration.Duration
  std/timer
}

struct Config {
  context: Context

  audit: Supervisor
  // Where the audit tasks put what they wrote, so ` + "`main`" + ` can look.
  written: Channel<Int>
}

fn boot(): Config {
  Config{context: Context.root(),
    audit: Supervisor.new(max_running: 4),
    written: Channel.buffered<Int>(8),
  }
}

fn record_audit(id: Int): Unit {
  // Stands in for the slow part — a write, an API call.
  timer.sleep(Duration.milliseconds(50))
  case Sender.send(Config.written.sender, id) {
    Ok(_) -> Unit
    Err(_) -> Unit
  }
}

fn handle_order(id: Int): String {
  _ = Supervisor.spawn(Config.audit, || record_audit(id))
  "order ${Display.to_string(id)} accepted"
}

// Both ids are on the channel by the time this runs, but in whichever
// order the two tasks got there — so it sums them rather than reading
// them one at a time and claiming an order.
fn audited_total(): Int {
  first = Maybe.with_default(Receiver.receive(Config.written.receiver), 0)
  second = Maybe.with_default(Receiver.receive(Config.written.receiver), 0)
  first + second
}

fn main() {
  dbg handle_order(1)
  dbg handle_order(2)

  // Only here, at the end, do we wait for the audit trail.
  _ = Supervisor.flush(Config.audit)
  dbg audited_total()
}
`, "dbg line 47: handle_order(1) = \"order 1 accepted\"\ndbg line 48: handle_order(2) = \"order 2 accepted\"\ndbg line 52: audited_total() = 3\n"},
		{"positional and named short calls, a bound and an await", `import {
  std/supervisors.{Restart, Supervisor, Wait}
  std/duration.Duration
  std/tasks.Task
  std/timer
}

struct App {
  context: Context
  work: Supervisor
  retry: Supervisor
  label: String
}

// A constructor boot calls, so the short call reads back to native's Go.
fn retrying(): Supervisor {
  Supervisor.new(max_running: 3, shutdown_timeout: Duration.seconds(2), restart: Restart.Transient)
}

fn boot(): App {
  App{context: Context.root(),
    work: Supervisor.new(2, Duration.seconds(1)),
    retry: retrying(),
    label: "app",
  }
}

fn nap(): Unit {
  timer.sleep(Duration.milliseconds(5))
}

fn main() {
  t = Supervisor.spawn(App.work, || nap())
  Task.await(t)
  dbg "awaited"
  _ = Supervisor.spawn(App.retry, || nap())
  flushed = Supervisor.flush(App.retry, Wait.UpTo(Duration.seconds(5)))
  dbg flushed
  dbg App.label
  Unit
}
`, "dbg line 35: \"awaited\" = \"awaited\"\ndbg line 38: flushed = Flushed\ndbg line 39: App.label = \"app\"\n"},
		{"a constructor boot calls with one named argument", `import {
  std/supervisors.{Supervisor}
  std/duration.Duration
  std/timer
}

struct App {
  context: Context
  jobs: Supervisor
}

fn jobs(): Supervisor {
  Supervisor.new(max_running: 1)
}

fn boot(): App {
  App{context: Context.root(), jobs: jobs()}
}

fn main() {
  _ = Supervisor.spawn(App.jobs, || timer.sleep(Duration.milliseconds(1)))
  dbg Supervisor.flush(App.jobs)
  Unit
}
`, "dbg line 22: Supervisor.flush(App.jobs) = Flushed\n"},
		{"short calls to calendar's defaulted Time.new", `import std/calendar.{Error, Time}

fn noon(): Result<Time, Error> {
  Time.new(12, 0)
}

fn main() {
  dbg noon()
  dbg Time.new(9, 30, 15)
  Unit
}
`, "dbg line 8: noon() = Ok(12:00:00)\ndbg line 9: Time.new(9, 30, 15) = Ok(09:30:15)\n"},
		{"the prelude with_default pair", `fn half(n: Int): Maybe<Int> {
  if n % 2 == 0 { Some(n / 2) } else { None }
}

fn parse(n: Int): Result<Int, String> {
  if n > 0 { Ok(n) } else { Err("negative") }
}

fn main() {
  dbg Maybe.with_default(half(8), 0)
  dbg Maybe.with_default(half(7), -1)
  dbg Result.with_default(parse(5), 0)
  dbg Result.with_default(parse(-5), 0)
  Unit
}
`, "dbg line 10: Maybe.with_default(half(8), 0) = 4\ndbg line 11: Maybe.with_default(half(7), -1) = -1\ndbg line 12: Result.with_default(parse(5), 0) = 5\ndbg line 13: Result.with_default(parse(-5), 0) = 0\n"},
		{"a field read through an app field", `struct Settings {
  label: String
  n: Int
}

struct App {
  context: Context
  settings: Settings
}

fn boot(): App {
  App{context: Context.root(), settings: Settings{label: "x", n: 2}}
}

fn main() {
  dbg App.settings.label
  dbg App.settings.n + 1
  Unit
}
`, "dbg line 16: App.settings.label = \"x\"\ndbg line 17: App.settings.n + 1 = 3\n"},
		{"spawn_all into a supervisor", strings.Replace(irSupervisorBatchHeader, "TASKS", "", 1) + `
fn main() {
  _ = Supervisor.spawn_all(App.audit, [1, 2, 3], record)
  _ = Supervisor.flush(App.audit)
  dbg total(3)
  Unit
}
`, "dbg line 37: total(3) = 6\n"},
		{"await_all over a supervised batch", strings.Replace(irSupervisorBatchHeader, "TASKS", "  std/tasks.Task\n", 1) + `
fn main() {
  batch = Supervisor.spawn_all(App.audit, [1, 2], record)
  _ = Task.await_all(batch)
  dbg total(2)
  Unit
}
`, "dbg line 38: total(2) = 3\n"},
		{"a supervised task's outcome", strings.Replace(irSupervisorBatchHeader, "TASKS", "  std/tasks.{Outcome, Task}\n", 1) + `
fn describe(o: Outcome<Unit>): String {
  case o {
    Outcome.Completed(_) -> "completed"
    Outcome.Cancelled -> "cancelled"
    Outcome.Failed(_) -> "failed"
  }
}

fn main() {
  t = Supervisor.spawn(App.audit, || nap())
  dbg describe(Task.outcome(t))
  u = Supervisor.spawn(App.audit, || nap())
  Task.cancel(u)
  dbg describe(Task.outcome(u))
  cancelled: Outcome<Unit> = .Cancelled
  done: Outcome<Unit> = Outcome.Completed(Unit)
  dbg Task.outcome(u) != done
  dbg Task.outcome(u) == cancelled
  Unit
}
`, "dbg line 45: describe(Task.outcome(t)) = \"completed\"\ndbg line 48: describe(Task.outcome(u)) = \"cancelled\"\ndbg line 51: Task.outcome(u) != done = True\ndbg line 52: Task.outcome(u) == cancelled = True\n"},
		{"named arguments with holes, reordered, and reaching the last slot", `import {
  std/supervisors.{GiveUp, Restart, Supervisor}
  std/duration.Duration
  std/timer
}

struct App {
  context: Context
  a: Supervisor
  b: Supervisor
  c: Supervisor
  d: Supervisor
}

// Skips shutdown_timeout: native fills it with std's _DEFAULT1 accessor.
fn permanent(): Supervisor {
  Supervisor.new(max_running: 1, restart: Restart.Permanent)
}

fn reordered(): Supervisor {
  Supervisor.new(shutdown_timeout: Duration.seconds(1), max_running: 4)
}

// Skips backoff and writes the last parameter, so it calls the full body.
fn exits(): Supervisor {
  Supervisor.new(3, Duration.seconds(2), Restart.Transient, on_give_up: GiveUp.Exit)
}

// Three holes. Each earlier hole's value is forced into a temporary and
// passed to every later accessor's prefix.
fn gives_up(): Supervisor {
  Supervisor.new(max_running: 4, on_give_up: GiveUp.Exit)
}

fn boot(): App {
  App{context: Context.root(), a: permanent(), b: reordered(), c: exits(), d: gives_up()}
}

fn nap(): Unit {
  timer.sleep(Duration.milliseconds(5))
}

fn main() {
  _ = Supervisor.spawn(App.b, || nap())
  dbg Supervisor.flush(App.b)
  dbg Supervisor.flush(App.c)
  dbg Supervisor.flush(App.a)
  dbg Supervisor.flush(App.d)
  Unit
}
`, "dbg line 45: Supervisor.flush(App.b) = Flushed\ndbg line 46: Supervisor.flush(App.c) = Flushed\ndbg line 47: Supervisor.flush(App.a) = Flushed\ndbg line 48: Supervisor.flush(App.d) = Flushed\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}

// irSupervisorBatchHeader is the app for the batch and outcome programs; TASKS
// is replaced by the std/tasks import each program needs.
const irSupervisorBatchHeader = `import {
  std/channels.{Channel, Receiver, Sender}
  std/supervisors.{Supervisor}
TASKS  std/duration.Duration
  std/timer
}

struct App {
  context: Context
  audit: Supervisor
  seen: Channel<Int>
}

fn boot(): App {
  App{context: Context.root(), audit: Supervisor.new(max_running: 2), seen: Channel.buffered<Int>(8)}
}

fn record(id: Int): Unit {
  case Sender.send(App.seen.sender, id) {
    Ok(_) -> Unit
    Err(_) -> Unit
  }
}

fn nap(): Unit {
  timer.sleep(Duration.milliseconds(5))
}

// The ids arrive in whichever order the tasks got there, so this sums them.
fn total(n: Int): Int {
  if n == 0 { 0 } else { Maybe.with_default(Receiver.receive(App.seen.receiver), 0) + total(n - 1) }
}
`

// Supervisor shapes the builder does not lower are declined: the enclosing function
// is not retained.
func TestIRSupervisor_UnsupportedShapesPreserveEmission(t *testing.T) {
	for _, tc := range []struct{ fn, src string }{
		// A supervisor operand that is neither a read nor a call: a
		// conditional's value, which native computes into a slot before the
		// task body's func literal. A call operand retains; see
		// TestIRNestedPattern_SpawnOverACalledAppSupervisor.
		{"spawn_into", `import std/supervisors.{Supervisor}
struct App {
  context: Context
  s: Supervisor
}
fn boot(): App {
  App{context: Context.root(), s: Supervisor.new(max_running: 1)}
}
fn spawn_into(): Unit {
  _ = Supervisor.spawn(if 1 > 0 { App.s } else { App.s }, || Unit)
  Unit
}
fn main() {
  spawn_into()
  _ = Supervisor.flush(App.s)
  Unit
}
`},
	} {
		p, err := AnalyzeSource("main.nomi", tc.src)
		if err != nil {
			t.Fatalf("the front end now rejects this, so the preservation below is untested: %v", err)
		}
		got, _, err := GenerateIR(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, mod := range got.IR {
			for _, f := range mod.Funcs() {
				if f.Name() == tc.fn {
					t.Fatalf("unsupported supervisor shape was retained: %s", tc.fn)
				}
			}
		}
	}
}

// Every omitted default runs once per call. A named call that skips several
// defaulted parameters of a user function prints each default once, in slot
// order, after every written argument; and a std call with three holes calls
// each `_DEFAULT<slot>` accessor once, passing an earlier hole's value, not a
// second call of its accessor, to the later ones. Std's defaults are pure, so
// the std case checks the output and that the caller is retained rather than
// counting calls.
func TestIRDefaults_EachHoleRunsOnce(t *testing.T) {
	t.Run("user function", func(t *testing.T) {
		verifyLambdaProgram(t, `import std/io

fn noisy(label: String, n: Int): Int {
  io.print(label)
  n
}

fn spread(a: Int, b: Int = noisy("b", a + 1), c: Int = noisy("c", b + 1), d: Int = noisy("d", c + 1), e: Int): Int {
  a + b + c + d + e
}

fn main() {
  io.print(spread(noisy("a", 1), e: noisy("e", 10)))
  io.print(spread(1, c: noisy("c!", 7), e: 10))
}
`, "a\ne\nb\nc\nd\n20\nc!\nb\nd\n28\n")
	})
	t.Run("std accessors", func(t *testing.T) {
		src := `import std/supervisors.{GiveUp, Supervisor}

struct App {
  context: Context
  s: Supervisor
}

fn gives_up(): Supervisor {
  Supervisor.new(max_running: 4, on_give_up: GiveUp.Exit)
}

fn boot(): App {
  App{context: Context.root(), s: gives_up()}
}

fn main() {
  dbg Supervisor.flush(App.s)
  Unit
}
`
		verifyLambdaProgram(t, src, "dbg line 17: Supervisor.flush(App.s) = Flushed\n")
		p, err := AnalyzeSource("main.nomi", src)
		if err != nil {
			t.Fatalf("the front end now rejects this, so the count below is untested: %v", err)
		}
		res, _, err := GenerateIR(p)
		if err != nil {
			t.Fatal(err)
		}
		retained := false
		for _, mod := range res.IR {
			for _, f := range mod.Funcs() {
				retained = retained || f.Name() == "gives_up"
			}
		}
		if !retained {
			t.Fatal("gives_up is not retained, so the VM's fill plan is untested")
		}
	})
}

// Debug of a Unit, bare and as a supervised task's Completed payload, renders
// `Unit`.
func TestIRSupervisor_UnitPayloadDebug(t *testing.T) {
	verifyLambdaProgram(t, `import {
  std/supervisors.{Supervisor}
  std/tasks.Task
}
struct App {
  context: Context
  s: Supervisor
}
fn boot(): App {
  App{context: Context.root(), s: Supervisor.new(max_running: 1)}
}
fn ok(): Result<Unit, String> { Ok(Unit) }
fn show(): Unit {
  t = Supervisor.spawn(App.s, || Unit)
  dbg Task.outcome(t)
  dbg ok()
  dbg Unit
  Unit
}
fn main() {
  show()
  _ = Supervisor.flush(App.s)
  Unit
}
`, "dbg line 15: Task.outcome(t) = Completed(Unit)\ndbg line 16: ok() = Ok(Unit)\ndbg line 17: Unit = Unit\n")
}
