package vmhost_test

import (
	"context"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// A task's body runs in the dynamic extent of the `concurrent` block that
// spawned it, so a spawn inside it, directly or through a helper at any
// depth, joins that block (spec §16 *Static guarantees*, rule 1): the block
// waits for it, and its failure and cancellation are a block task's.

// A helper called from a task body spawns, and the task awaits what it spawned.
func TestNestedSpawn_AwaitedByTheTaskThatSpawnedIt(t *testing.T) {
	src := `
import std::tasks.Task
import std::io

fn square(n: Int) -> Int
  n * n
end

fn sum_of_squares(a: Int, b: Int) -> Int
  x = Task.spawn(fn -> square(a))
  y = Task.spawn(fn -> square(b))
  Task.await(x) + Task.await(y)
end

fn deeper(n: Int) -> Int
  t = Task.spawn(fn -> sum_of_squares(n, n + 1))
  Task.await(t)
end

fn main() -> Unit
  total = concurrent
    a = Task.spawn(fn -> sum_of_squares(3, 4))
    b = Task.spawn(fn -> deeper(1))
    Task.await(a) + Task.await(b)
  end
  io.print("total ${total}")
end
`
	runInBubble(t, src, "total 30")
}

// A task spawned inside a task and never awaited still belongs to the block:
// the block cancels it on exit and waits for it, so nothing outlives the
// block and the run does not hang on its five-minute sleep.
func TestNestedSpawn_UnawaitedIsCancelledAndWaitedForAtBlockExit(t *testing.T) {
	src := `
import std::tasks.Task
import std::duration.Duration
import std::io
import std::timer

fn background() -> Int
  timer.sleep(Duration.minutes(5))
  io.print("background finished")
  0
end

fn start() -> Int
  t = Task.spawn(fn -> background())
  _t = t
  1
end

fn main() -> Unit
  n = concurrent
    parent = Task.spawn(fn -> start())
    Task.await(parent)
  end
  io.print("after block ${n}")
end
`
	out, elapsed := runInBubble(t, src, "after block 1")
	if strings.Contains(out, "background finished") {
		t.Fatalf("the unawaited nested task ran to the end: %q", out)
	}
	assertVirtualized(t, elapsed, "an unawaited nested task cancelled at block exit")
}

// A nested task's failure fails the task that awaits it, which fails the
// block, and the block's other tasks are cancelled.
func TestNestedSpawn_FailurePropagatesToTheBlock(t *testing.T) {
	src := `
import std::tasks.Task
import std::duration.Duration
import std::io
import std::timer

fn fast_fail() -> Result<Int, String>
  Err("boom")
end

fn slow() -> Result<Int, String>
  timer.sleep(Duration.minutes(5))
  Ok(0)
end

fn outer() -> Result<Int, String>
  inner = Task.spawn(fn -> fast_fail())
  Ok(Task.await(inner)?)
end

fn main() -> Unit
  outcome: Result<Int, String> = concurrent
    parent = Task.spawn(fn -> outer())
    sibling = Task.spawn(fn -> slow())
    _sibling = sibling
    Ok(Task.await(parent)?)
  end
  case outcome
    Ok(v) -> io.print("ok:" + Display.to_string(v))
    Err(e) -> io.print("err:" + e)
  end
end
`
	_, elapsed := runInBubble(t, src, "err:boom")
	assertVirtualized(t, elapsed, "a nested failure past a 5m sibling")
}

// A trap in a nested task is the awaiting task's failure, and awaiting that
// task fails the program with the trap's text.
func TestNestedSpawn_TrapFailsTheAwaiter(t *testing.T) {
	src := `
import std::tasks.Task

fn divide(n: Int) -> Int
  100 / n
end

fn outer() -> Int
  inner = Task.spawn(fn -> divide(0))
  Task.await(inner)
end

fn main() -> Unit
  _n = concurrent
    parent = Task.spawn(fn -> outer())
    Task.await(parent)
  end
end
`
	p, err := vmhost.LoadSource("main", src)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var out lockedBuffer
	err = p.Run(context.Background(), &out, nil, false)
	if err == nil || !strings.Contains(err.Error(), "division by zero") {
		t.Fatalf("run = %v, want the nested task's division by zero", err)
	}
}

// Cancelling a task that is waiting on a task it spawned cancels it alone:
// its outcome is Cancelled, and the task it spawned, which belongs to the
// block, is cancelled and waited for when the block exits.
func TestNestedSpawn_CancellingTheParent(t *testing.T) {
	src := `
import std::tasks.Task
import std::duration.Duration
import std::io
import std::timer

fn slow() -> Int
  timer.sleep(Duration.minutes(5))
  io.print("slow finished")
  0
end

fn outer() -> Int
  inner = Task.spawn(fn -> slow())
  Task.await(inner)
end

fn main() -> Unit
  cancelled = concurrent
    parent = Task.spawn(fn -> outer())
    timer.sleep(Duration.seconds(1))
    Task.cancel(parent)
    case Task.outcome(parent)
      .Cancelled -> True
      _ -> False
    end
  end
  io.print("cancelled ${cancelled}")
end
`
	out, elapsed := runInBubble(t, src, "cancelled True")
	if strings.Contains(out, "slow finished") {
		t.Fatalf("the nested task outlived its block: %q", out)
	}
	assertVirtualized(t, elapsed, "cancelling a parent waiting on a nested task")
}
