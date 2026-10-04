package irbuild

import (
	"strings"
	"testing"
)

// Grouped cases take the routes a named function's body takes: comparisons
// beyond scalars, prelude constructors, channel and task calls inside an
// assertion subject, annotated bindings, `Iter.any?`/`Iter.all?`, concurrent
// blocks and `Instant.now`. Each fixture runs on the VM through
// irTestGroupVM against its golden record.

const irGroupGapsChannelSource = `import {
  std/channels.{Channel, Receiver, Sender}
  std/duration.Duration
}

pub struct App {
  context: Context
  done: Channel<Int>
}

pub fn boot(): App {
  App{context: Context.root(), done: Channel.buffered<Int>(4)}
}

fn main() {}

tests "channel equality" {
  boot boot()

  test "a received value equals Some" {
    _ = Sender.send(App.done.sender, 1)
    assert Receiver.receive(App.done.receiver) == Some(1)
  }

  test "a failing receive shows the call's row and both operands" {
    _ = Sender.send(App.done.sender, 2)
    assert Receiver.receive(App.done.receiver) == Some(1)
  }

  test "a piped receive records no call row" {
    _ = Sender.send(App.done.sender, 3)
    assert App.done.receiver |> Receiver.receive() == Some(1)
  }

  test "a piped qualified call records no call row" {
    s = "abc"
    assert s |> String.length() == 4
  }

  test "std equality shows its operands" {
    d = Duration.seconds(2)
    assert d == Duration.seconds(1)
  }

  test "a closed channel answers None" {
    Sender.close(App.done.sender)
    none: Maybe<Int> = None
    assert Receiver.receive(App.done.receiver) == none
  }
}
`

// deadline_floor_test.nomi's grouped case, plus failing variants whose reports
// carry the rows a channel call, a pipe and a std equality record.
func TestIRGroupGaps_ChannelAndMaybeEqualityInASubject(t *testing.T) {
	run := irTestGroupVM(t, irGroupGapsChannelSource, 6)
	for _, want := range []string{"Some(2)", "Duration.seconds(1)", "2 passed"} {
		if !strings.Contains(run.vm.stdout, want) {
			t.Errorf("the report lacks %q:\n%s", want, run.vm.stdout)
		}
	}
}

const irGroupGapsTaskSource = `import {
  std/channels.{Channel, Receiver, Sender}
  std/duration.Duration
  std/supervisors.Supervisor
  std/tasks.{Outcome, Task}
  std/testing.Clock
  std/timer
}

pub struct App {
  context: Context
  work: Supervisor
  alerts: Channel<String>
}

fn report(message: String): Unit {
  case Sender.send(App.alerts.sender, message) {
    Ok(_) -> Unit
    Err(_) -> Unit
  }
}

fn slow(): Unit {
  timer.sleep(Duration.seconds(30))
  report("SHOULD NOT ARRIVE")
}

fn drain(): List<String> {
  Sender.close(App.alerts.sender)
  Iter.loop(|seen = []|
    case Receiver.receive(App.alerts.receiver) {
      Some(message) -> [message, ..seen]
      None -> break seen
    }
  )
}

pub fn boot(): App {
  App{
    context: Context.root(),
    work: Supervisor.new(max_running: 2),
    alerts: Channel.buffered<String>(8),
  }
}

fn main() {}

tests "tasks" {
  clock Clock.Virtual

  boot boot()

  test "a cancelled task reports Cancelled" {
    doomed = Supervisor.spawn(App.work, || slow())
    Task.cancel(doomed)
    cancelled: Outcome<Unit> = .Cancelled
    assert Task.outcome(doomed) == cancelled
  }

  test "a failing outcome comparison shows its rows" {
    done = Supervisor.spawn(App.work, || report("quick"))
    _ = Supervisor.flush(App.work)
    cancelled: Outcome<Unit> = .Cancelled
    assert Task.outcome(done) == cancelled
  }

  test "any and all over a drained list" {
    _ = Supervisor.spawn(App.work, || report("audited 7"))
    _ = Supervisor.flush(App.work)
    seen = drain()
    assert Iter.any?(seen, |m| m == "audited 7")
    assert Iter.all?(seen, |m| String.contains?(m, "audited"))
    refute Iter.any?(seen, |m| m == "missing")
  }

  test "a failing any shows its operand" {
    _ = Supervisor.spawn(App.work, || report("x"))
    _ = Supervisor.flush(App.work)
    assert drain() |> Iter.all?(|m| m == "y")
  }
}
`

// supervisors_test.nomi's shapes: an annotated binding, Outcome equality over
// `Task.outcome` in a subject, `Iter.any?`/`Iter.all?`, and the empty-seeded
// branching loop in `drain`.
func TestIRGroupGaps_OutcomeAnyAllAndAnEmptySeededLoop(t *testing.T) {
	run := irTestGroupVM(t, irGroupGapsTaskSource, 4)
	drain := false
	for _, f := range run.module.Funcs() {
		drain = drain || f.Name() == "drain"
	}
	if !drain {
		t.Error("drain's empty-seeded loop was not retained")
	}
	for _, want := range []string{"Completed(Unit)", "Cancelled", "2 passed"} {
		if !strings.Contains(run.vm.stdout, want) {
			t.Errorf("the report lacks %q:\n%s", want, run.vm.stdout)
		}
	}
}

const irGroupGapsClockSource = `import {
  std/instant.Instant
  std/duration.Duration
  std/tasks.Task
  std/testing.Clock
  std/timer
}

fn long_sleep(): Unit {
  timer.sleep(Duration.seconds(5))
}

fn short_fail(): Result<Int, String> {
  Err("boom")
}

tests "virtual clock" {
  clock Clock.Virtual

  test "a short-circuit cancels its siblings" {
    before = Instant.now()

    outcome = concurrent {
      short = Task.spawn(|| short_fail())
      long = Task.spawn(|| long_sleep())
      _l = long
      Ok(try Task.await(short))
    }

    elapsed = Instant.to_seconds(Instant.now()) - Instant.to_seconds(before)
    failed: Result<Int, String> = Err("boom")

    assert outcome == failed
    assert elapsed == 0
  }

  test "time passes in the bubble" {
    before = Instant.now()
    timer.sleep(Duration.seconds(7))
    assert Instant.to_seconds(Instant.now()) - Instant.to_seconds(before) == 7
  }
}
`

// concurrent_runtime_test.nomi's virtual-clock group, less its pattern
// assertion: Instant.now reads the bubble's clock and a concurrent block runs
// in the case.
func TestIRGroupGaps_InstantNowAndConcurrentBlocks(t *testing.T) {
	run := irTestGroupVM(t, irGroupGapsClockSource, 2)
	if run.vm.exit != 0 {
		t.Errorf("want both cases to pass:\n%s", run.vm.stdout)
	}
}

const irGroupGapsLoopProgram = `import std/io

fn collect(n: Int): List<Int> {
  Iter.loop(|seen = []|
    case Iter.count(seen) >= n {
      True -> break seen
      False -> [Iter.count(seen), ..seen]
    }
  )
}

fn has_big(xs: List<Int>): Bool {
  Iter.any?(xs, |n| n > 2)
}

fn all_small(xs: List<Int>): Bool {
  Iter.all?(xs, |n| n < 3)
}

fn main() {
  io.inspect(collect(3))
  io.print(has_big(collect(3)))
  io.print(all_small([1, 2]))
  io.print(all_small([1, 5]))
}
`

// An empty seed takes the checker's parameter type in a named function, and
// `Iter.any?`/`Iter.all?` read back as native writes them.
func TestIRGroupGaps_EmptySeededLoopAndAnyAllAgree(t *testing.T) {
	verifyLambdaProgram(t, irGroupGapsLoopProgram, "[2, 1, 0]\nFalse\nTrue\nFalse\n")
}
