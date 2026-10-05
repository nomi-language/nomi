package vmhost_test

// Synctest compatibility of the VM and rt's concurrency.
//
// The concurrency design claims the runtime is compatible with
// `testing/synctest`: standard `context.WithCancel`, plain channels,
// goroutines spawned with `go` from the caller's goroutine, no worker pool, no
// global scheduler thread, no custom timer wheel. These tests are what turns
// that claim into a checked property.
//
// What makes them regression tests rather than demonstrations is the
// wall-clock bound. Each runs work that takes minutes of virtual time and
// asserts it finishes in a couple of seconds of real time. If a worker pool, a
// scheduler goroutine or a timer wheel is introduced, sleeps stop being
// virtualized and the wall clock balloons to the virtual duration, so the
// bound fails or the test times out. If a task blocks on something outside the
// bubble, synctest itself reports the deadlock. The margins are two orders of
// magnitude on either side of the bound.
//
// Loading (front end, IR, the process-wide stdlib lowering) happens outside
// the bubble; only the run is inside it and timed.

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nomi-language/nomi/vmhost"
)

// syncTestBound is the real-time ceiling every test here asserts: far above
// what a virtualized run costs (tens of milliseconds) and far below the
// virtual durations involved (minutes).
const syncTestBound = 3 * time.Second

// lockedBuffer is an output writer safe for the tasks a program spawns.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// runInBubble loads src, runs it inside a synctest bubble, checks every want
// appears in its output, and returns the output and the real time the run took.
func runInBubble(t *testing.T, src string, wants ...string) (string, time.Duration) {
	t.Helper()
	var diag lockedBuffer
	p, err := vmhost.LoadSource("main", src, vmhost.WithErrorOutput(&diag))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var out lockedBuffer
	start := time.Now()
	synctest.Test(t, func(t *testing.T) {
		if err := p.Run(context.Background(), &out, nil, false); err != nil {
			t.Fatalf("run: %v\noutput: %q", err, out.String())
		}
	})
	elapsed := time.Since(start)
	got := out.String()
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in output, got %q", want, got)
		}
	}
	return got, elapsed
}

func assertVirtualized(t *testing.T, elapsed time.Duration, what string) {
	t.Helper()
	t.Logf("%s: %v of real time", what, elapsed)
	if elapsed > syncTestBound {
		t.Errorf("%s took %v of real time (bound %v) — time is no longer being "+
			"virtualized, which means something in the runtime stopped being "+
			"synctest-compatible: a worker pool, a global scheduler goroutine, "+
			"or a custom timer wheel are the usual causes",
			what, elapsed, syncTestBound)
	}
}

// Three ten-second sleeps in parallel inside a `concurrent` block.
func TestSynctestConcurrentBlockVirtualizesSleeps(t *testing.T) {
	src := `
import std/tasks.Task
import std/duration.Duration
import std/io
import std/timer

fn main(): Unit {
  concurrent {
    a = Task.spawn(|| timer.sleep(Duration.seconds(10)))
    b = Task.spawn(|| timer.sleep(Duration.seconds(10)))
    c = Task.spawn(|| timer.sleep(Duration.seconds(10)))
    Task.await(a)
    Task.await(b)
    Task.await(c)
  }
  io.print("slept")
}
`
	_, elapsed := runInBubble(t, src, "slept")
	assertVirtualized(t, elapsed, "3 parallel 10s sleeps")
}

// Five minutes of sleeps that must happen one after another, which no amount
// of parallelism could collapse.
func TestSynctestSequentialSleepsAdvanceVirtualTime(t *testing.T) {
	src := `
import std/duration.Duration
import std/io
import std/timer

fn main(): Unit {
  timer.sleep(Duration.minutes(1))
  timer.sleep(Duration.minutes(2))
  timer.sleep(Duration.minutes(2))
  io.print("five minutes later")
}
`
	_, elapsed := runInBubble(t, src, "five minutes later")
	assertVirtualized(t, elapsed, "5 minutes of sequential sleeps")
}

// A goroutine blocked on a channel receive has to count as durably blocked
// for the clock to advance: the producers sleep before sending, so the
// consumer is parked on a receive while virtual time moves.
func TestSynctestChannelsWorkInABubble(t *testing.T) {
	src := `
import std/channels.{Channel, Receiver, Sender}
import std/tasks.Task
import std/duration.Duration
import std/io
import std/timer

fn produce(ch: Channel<Int>, n: Int): Unit {
  timer.sleep(Duration.seconds(30))
  case Sender.send(ch.sender, n) {
    Ok(u) -> u
    Err(_) -> Unit
  }
}

fn consume(ch: Channel<Int>): Int {
  Iter.loop(|sum = 0|
    case Receiver.receive(ch.receiver) {
      Some(v) -> sum + v
      None -> break sum
    }
  )
}

fn main(): Unit {
  ch = Channel.buffered<Int>(1)
  total = concurrent {
    p1 = Task.spawn(|| produce(ch, 10))
    p2 = Task.spawn(|| produce(ch, 20))
    cons = Task.spawn(|| consume(ch))
    Task.await(p1)
    Task.await(p2)
    _ = Sender.close(ch.sender)
    Task.await(cons)
  }
  io.print(Display.to_string(total))
}
`
	_, elapsed := runInBubble(t, src, "30")
	assertVirtualized(t, elapsed, "channel round-trip behind 30s sleeps")
}

// A failing sibling cancels the others through the same ctx-aware sleeps.
func TestSynctestCancellationCascadeIsInstant(t *testing.T) {
	src := `
import std/tasks.Task
import std/duration.Duration
import std/io
import std/timer

fn slow_task(): Result<Int, String> {
  timer.sleep(Duration.minutes(5))
  Ok(0)
}

fn fast_fail(): Result<Int, String> {
  Err("boom")
}

fn main(): Unit {
  outcome: Result<Int, String> = concurrent {
    fast = Task.spawn(|| fast_fail())
    slow1 = Task.spawn(|| slow_task())
    slow2 = Task.spawn(|| slow_task())
    _slow1_ref = slow1
    _slow2_ref = slow2
    Ok(try Task.await(fast))
  }
  case outcome {
    Ok(v) -> io.print("ok:" + Display.to_string(v))
    Err(e) -> io.print("err:" + e)
  }
}
`
	_, elapsed := runInBubble(t, src, "err:boom")
	assertVirtualized(t, elapsed, "cancellation cascade past 5m sleeps")
}

// supervisorProgram is a program whose boot creates one Supervisor named
// notify with the given arguments.
func supervisorProgram(groupArgs, rest string) string {
	return `
import {
  std/supervisors.Supervisor
  std/duration.Duration
  std/io
  std/timer
}

struct Config {
  context: Context
  notify: Supervisor
}

fn boot(): Config {
  Config{context: Context.root(), notify: Supervisor.new(` + groupArgs + `)}
}
` + rest
}

// A task that takes thirty virtual seconds under a five-minute drain budget.
// The drain waits for it, and the whole thing is free.
func TestSynctestGroupDrainVirtualizesItsBudget(t *testing.T) {
	src := supervisorProgram("shutdown_timeout: Duration.minutes(5), max_running: 4", `
fn note(): Unit {
  timer.sleep(Duration.seconds(30))
  io.print("note sent")
}

fn main(): Unit {
  _ = Supervisor.spawn(Config.notify, || note())
  io.print("main returned")
}
`)
	_, elapsed := runInBubble(t, src, "note sent")
	assertVirtualized(t, elapsed, "30s of grouped work under a 5m budget")
}

// The drain budget is two virtual minutes and the task sleeps an hour, so the
// drain expires and abandons it. The abandoned goroutine must not stall the
// bubble, and the two-minute budget must cost no real time. It works because
// an abandoned task that is cancellation-aware still unwinds when the group's
// context fires.
func TestSynctestDrainExpiryAbandonsWithoutStallingTheBubble(t *testing.T) {
	src := supervisorProgram("shutdown_timeout: Duration.minutes(2), max_running: 4", `
fn forever(): Unit {
  timer.sleep(Duration.hours(1))
  io.print("SHOULD NOT PRINT")
}

fn main(): Unit {
  _ = Supervisor.spawn(Config.notify, || forever())
  io.print("main returned")
}
`)
	out, elapsed := runInBubble(t, src, "main returned")
	if strings.Contains(out, "SHOULD NOT PRINT") {
		t.Fatalf("work past its budget was waited for: %q", out)
	}
	assertVirtualized(t, elapsed, "a 2m drain budget expiring on a 1h task")
}

// Six thirty-second items through a group of two is three virtual rounds, and
// the queued workers park on a buffered channel rather than being handed to a
// pool, which would be a scheduler synctest cannot see into.
func TestSynctestMaxRunningQueuesWithoutAPool(t *testing.T) {
	src := supervisorProgram("shutdown_timeout: Duration.minutes(10), max_running: 2", `
fn work(n: Int): Unit {
  timer.sleep(Duration.seconds(30))
  io.print("done " + Display.to_string(n))
}

fn main(): Unit {
  _ = Supervisor.spawn_all(Config.notify, [1, 2, 3, 4, 5, 6], work)
  io.print("spawned")
}
`)
	_, elapsed := runInBubble(t, src, "done 1", "done 2", "done 3", "done 4", "done 5", "done 6")
	assertVirtualized(t, elapsed, "6 x 30s items through a group of 2")
}

// Three restarts on the built-in backoff curve, which starts at a second and
// doubles. The delays are not configurable, so asserting the built-in curve
// without paying for it in wall clock requires a bubble. The delays carry
// jitter, so virtual time makes the clock reproducible, not the schedule.
func TestSynctestRestartBackoffVirtualizesItsDelays(t *testing.T) {
	src := supervisorProgram("shutdown_timeout: Duration.minutes(10), max_running: 1, "+
		"restart: Restart.Transient, backoff: Backoff.Exponential{max_restarts: 3, max_elapsed: Duration.minutes(5)}", `
fn breaks(): Unit {
  io.print("run")
  _ = 1 / 0
  Unit
}

fn main(): Unit {
  _ = Supervisor.spawn(Config.notify, || breaks())
}
`)
	src = strings.Replace(src, "  std/supervisors.Supervisor\n", "  std/supervisors.{Backoff, Restart, Supervisor}\n", 1)
	src = strings.Replace(src, "  std/timer\n", "", 1)
	out, elapsed := runInBubble(t, src)
	if n := strings.Count(out, "run\n"); n != 4 {
		t.Fatalf("expected 4 runs (1 + 3 restarts), got %d: %q", n, out)
	}
	assertVirtualized(t, elapsed, "3 restarts on the built-in curve")
}

// `Supervisor.flush` is the call that makes background work testable, so it is
// the one most likely to run inside a bubble.
func TestSynctestGroupWaitIsInstant(t *testing.T) {
	src := supervisorProgram("shutdown_timeout: Duration.minutes(5), max_running: 4", `
fn note(n: Int): Unit {
  timer.sleep(Duration.minutes(1))
  io.print("note " + Display.to_string(n))
}

fn main(): Unit {
  _ = Supervisor.spawn(Config.notify, || note(1))
  _ = Supervisor.spawn(Config.notify, || note(2))
  _ = Supervisor.flush(Config.notify)
  io.print("flushed")
}
`)
	out, elapsed := runInBubble(t, src, "flushed")
	flushed := strings.Index(out, "flushed")
	for _, want := range []string{"note 1", "note 2"} {
		at := strings.Index(out, want)
		if at < 0 || at > flushed {
			t.Errorf("wait returned before %q: %q", want, out)
		}
	}
	assertVirtualized(t, elapsed, "waiting out two 1m tasks")
}
