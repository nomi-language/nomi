package vmhost_test

// Safe points: a cancelled task, or one past its deadline, stops while it
// computes, not only when it waits. Every call, backward branch and element
// an iteration driven in Go passes along reads the frame's stop flag
// (rt.SafePoint), and stopping there unwinds as a wait does: deferred calls
// run and the task settles Cancelled, or the main line reports its deadline.
//
// Each busy body runs a bound it can reach (tens of millions of steps), so a
// regression fails by its outcome (`Completed(...)`, the `finished` line)
// rather than by hanging the test binary. Nothing here times the run.

import (
	"context"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/rt"
	"github.com/nomi-language/nomi/vmhost"
)

// safePointTask is a program whose task signals that it is running, then
// computes body, with a deferred call that runs a function of its own. Main
// cancels it once it is running and prints its outcome.
func safePointTask(decls, body string) string {
	return `import {
    std/channels.Channel
    std/channels.Receiver
    std/channels.Sender
    std/io
    std/tasks.Task
}

fn note(text: String) {
    io.print(text)
}

` + decls + `

fn main() {
    started = Channel.buffered<Int>(1)
    outcome = concurrent {
        t = Task.spawn(fn -> {
            defer note("cleanup ran")
            _ = Sender.send(started.sender, 1)
            n = ` + body + `
            io.print("finished")
            n
        })
        _ = Receiver.receive(started.receiver)
        Task.cancel(t)
        Task.outcome(t)
    }
    dbg outcome
}
`
}

// runSafePoint loads and runs src and answers its output and run error.
func runSafePoint(t *testing.T, src string) (string, error) {
	t.Helper()
	var diag lockedBuffer
	p, err := vmhost.LoadSource("main.nomi", src, vmhost.WithErrorOutput(&diag))
	if err != nil {
		t.Fatalf("load: %v\n%s", err, diag.String())
	}
	var out lockedBuffer
	err = p.Run(context.Background(), &out, nil, false)
	return out.String(), err
}

func assertCancelledMidWork(t *testing.T, src string) {
	t.Helper()
	out, err := runSafePoint(t, src)
	if err != nil {
		t.Fatalf("run: %v\noutput: %q", err, out)
	}
	if strings.Contains(out, "finished") || !strings.Contains(out, "outcome = Cancelled") {
		t.Fatalf("the cancelled task ran its work to the end instead of stopping at a safe point; output: %q", out)
	}
	if !strings.Contains(out, "cleanup ran") {
		t.Fatalf("the task stopped without running its deferred call; output: %q", out)
	}
}

func TestSafePoint_ACancelledLoopStops(t *testing.T) {
	assertCancelledMidWork(t, safePointTask(`fn spin(limit: Int) -> Int {
    Iter.loop(
        fn i -> {
            if i >= limit {
                break i
            }
            i + 1
        },
        from: 0,
    )
}`, "spin(30_000_000)"))
}

func TestSafePoint_ACancelledRecursionStops(t *testing.T) {
	// A tail call into itself and a plain call per step: the transfer and the
	// call are both safe points.
	assertCancelledMidWork(t, safePointTask(`fn step(acc: Int) -> Int {
    acc + 1
}

fn spin(n: Int, acc: Int) -> Int {
    if n == 0 {
        return acc
    }
    spin(n - 1, step(acc))
}`, "spin(30_000_000, 0)"))
}

func TestSafePoint_ACancelledReduceOverARangeStops(t *testing.T) {
	assertCancelledMidWork(t, safePointTask("", "Iter.reduce(0..30_000_000, fn acc = 0, x -> acc + x % 7)"))
}

func TestSafePoint_ACancelledIterationDrivenInGoStops(t *testing.T) {
	// No Nomi callback per element: only the element safe point the
	// machine wraps around the source can stop it.
	assertCancelledMidWork(t, safePointTask("", "Iter.from(0) |> Iter.take(30_000_000) |> Iter.last() |> Maybe.with_default(0)"))
}

// A deadline on the main line over a loop that never waits: the program
// reports the deadline, the rest of main is skipped, and main's deferred call
// runs.
func TestSafePoint_ADeadlineStopsABusyLoopOnTheMainLine(t *testing.T) {
	out, err := runSafePoint(t, `import {
    std/duration.Duration
    std/io
}

struct App {
    context: Context
}

fn boot(_startup: Startup) -> Result<App, String> {
    Ok(#App{context: Context.root()})
}

fn note(text: String) {
    io.print(text)
}

fn main() {
    defer note("cleanup ran")
    with App.context = Context.with_timeout(App.context, Duration.milliseconds(5))
    n = Iter.loop(
        fn i -> {
            if i >= 30_000_000 {
                break i
            }
            i + 1
        },
        from: 0,
    )
    io.print("finished ${n}")
}
`)
	if err == nil || !strings.Contains(err.Error(), rt.MainDeadlineFault) {
		t.Fatalf("want the deadline fault, got %v; output: %q", err, out)
	}
	if strings.Contains(out, "finished") {
		t.Fatalf("the loop ran to its end past the deadline; output: %q", out)
	}
	if !strings.Contains(out, "cleanup ran") {
		t.Fatalf("main stopped without running its deferred call; output: %q", out)
	}
}

// A deadline inside a task over a loop that never waits settles the task
// Cancelled, as a deadline reached in a wait does.
func TestSafePoint_ADeadlineStopsABusyLoopInATask(t *testing.T) {
	out, err := runSafePoint(t, `import {
    std/duration.Duration
    std/io
    std/tasks.Task
}

struct App {
    context: Context
}

fn boot(_startup: Startup) -> Result<App, String> {
    Ok(#App{context: Context.root()})
}

fn note(text: String) {
    io.print(text)
}

fn bounded() -> Int {
    defer note("cleanup ran")
    with App.context = Context.with_timeout(App.context, Duration.milliseconds(5))
    n = Iter.loop(
        fn i -> {
            if i >= 30_000_000 {
                break i
            }
            i + 1
        },
        from: 0,
    )
    io.print("finished")
    n
}

fn main() {
    outcome = concurrent {
        t = Task.spawn(fn -> bounded())
        Task.outcome(t)
    }
    dbg outcome
}
`)
	if err != nil {
		t.Fatalf("run: %v\noutput: %q", err, out)
	}
	if strings.Contains(out, "finished") || !strings.Contains(out, "outcome = Cancelled") {
		t.Fatalf("the task ran its loop to the end past its deadline; output: %q", out)
	}
	if !strings.Contains(out, "cleanup ran") {
		t.Fatalf("the task stopped without running its deferred call; output: %q", out)
	}
}
