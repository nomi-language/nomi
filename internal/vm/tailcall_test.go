package vm_test

// Tail calls (tail.go): a call the graph marks as a tail transfer replaces the
// caller's activation, so a tail-recursive loop runs in constant stack and
// never reaches the call-depth limit. Each loop here runs well past
// rt.MaxCallDepth, so a machine that stacked its tail calls would fail with the
// depth fault rather than print. The expected text is the program's answer,
// written down.

import (
	"errors"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/rt"
)

// The spec's own loop: ten million self tail calls, one hundred times the
// depth limit, on a Go stack capped far below what ten million frames need.
func TestTailCall_DeepSelfRecursionRunsInConstantStack(t *testing.T) {
	// 64 MB. A machine that grew one Go frame per Nomi tail call would need
	// several gigabytes for this loop; the cap turns that into a crash rather
	// than a slow pass.
	defer debug.SetMaxStack(debug.SetMaxStack(64 << 20))
	out, err := runDepth(t, `import std/io

fn count(n: Int, acc: Int): Int {
  if n == 0 { acc } else { count(n - 1, acc + 1) }
}

fn main() {
  io.print(count(10000000, 0))
}
`)
	if err != nil {
		t.Fatalf("a tail-recursive loop failed: %v", err)
	}
	if out != "10000000\n" {
		t.Fatalf("output %q, want %q", out, "10000000\n")
	}
}

// A nested `fn` is a recursive closure (ir.NewRecursiveFuncValue), and its
// self call is an indirect tail call through the closure's own capture. It
// runs past the depth limit like the top-level loop above, from main and from
// a `once` initializer's lambda.
func TestTailCall_NestedFnSelfRecursionRunsInConstantStack(t *testing.T) {
	out, err := runDepth(t, `import std/io

once total: Int = {
  fn down(n: Int, acc: Int): Int {
    if n == 0 { acc } else { down(n - 1, acc + 2) }
  }
  down(300000, 0)
}

fn main() {
  fn count(n: Int, acc: Int): Int {
    if n == 0 { acc } else { count(n - 1, acc + 1) }
  }
  io.print(count(300000, 0))
  io.print(total)
}
`)
	if err != nil {
		t.Fatalf("a tail-recursive nested fn failed: %v", err)
	}
	if out != "300000\n600000\n" {
		t.Fatalf("output %q, want %q", out, "300000\n600000\n")
	}
}

// Mutual recursion through `case` arms, a tail call through a function value,
// a tail call through a parameter that holds one, and a tail call in an `if`
// arm that reaches an interface dispatch.
func TestTailCall_MutualIndirectAndDispatchedCallsRunInConstantStack(t *testing.T) {
	out, err := runDepth(t, `import std/io

fn is_even(n: Int): Bool {
  case n {
    0 -> True
    _ -> is_odd(n - 1)
  }
}

fn is_odd(n: Int): Bool {
  case n {
    0 -> False
    _ -> is_even(n - 1)
  }
}

fn apply(f: (Int, Int) -> Int, n: Int, acc: Int): Int {
  f(n, acc)
}

fn bounce(n: Int, acc: Int): Int {
  if n == 0 { acc } else { apply(bounce, n - 1, acc + 1) }
}

fn via(step: (Int) -> Int, n: Int): Int {
  if n == 0 { 0 } else { via(step, step(n)) }
}

interface Stepper {
  fn step(value: self, n: Int): Int
}

struct Down {
  by: Int
}

impl Stepper for Down {
  fn step(value: Down, n: Int): Int {
    if n <= 0 { n } else { Stepper.step(value, n - value.by) }
  }
}

fn main() {
  io.print(is_even(1000001))
  io.print(bounce(1000000, 0))
  io.print(via(|x| x - 1, 1000000))
  io.print(Stepper.step(Down{by: 1}, 1000000))
}
`)
	if err != nil {
		t.Fatalf("a tail-recursive loop failed: %v", err)
	}
	if want := "False\n1000000\n0\n0\n"; out != want {
		t.Fatalf("output %q, want %q", out, want)
	}
}

// A call that is not in tail position keeps its caller's frame, so the same
// loop written `1 + f(...)` still reaches the limit at that call's line.
func TestTailCall_ANonTailCallStillReachesTheDepthLimit(t *testing.T) {
	_, err := runDepth(t, `import std/io

fn count(n: Int): Int {
  if n == 0 { 0 } else { 1 + count(n - 1) }
}

fn main() {
  io.print(count(1000000))
}
`)
	requireDepthFault(t, err, 4)
}

// A fault a million tail calls deep is one fault at its own line: there is one
// frame to report it from.
func TestTailCall_AFaultInADeepLoopReportsItsLine(t *testing.T) {
	out, err := runDepth(t, `import std/io

fn spin(n: Int): Int {
  if n == 0 {
    10 / n
  } else {
    spin(n - 1)
  }
}

fn main() {
  io.print("start")
  io.print(spin(1000000))
}
`)
	if err == nil {
		t.Fatalf("the division by zero did not fault; output %q", out)
	}
	if want := "line 5: division by zero"; err.Error() != want {
		t.Fatalf("failure %q, want %q", err.Error(), want)
	}
	var fault *vm.Fault
	if !errors.As(err, &fault) {
		t.Fatalf("the failure is %T, not a *vm.Fault", err)
	}
	if out != "start\n" {
		t.Fatalf("output %q, want %q", out, "start\n")
	}
}

// A tail-calling function's deferred calls run after the call's operands and
// before the callee: the transfer happens after every block of the caller has
// exited.
func TestTailCall_DeferredCallsRunBeforeTheCallee(t *testing.T) {
	out, err := runDepth(t, `import std/io

fn note(label: String, n: Int) {
  io.print("${label} ${n}")
}

fn arg(n: Int): Int {
  io.print("operand ${n}")
  n
}

fn down(n: Int): Int {
  defer note("leave", n)
  io.print("enter ${n}")
  step(arg(n))
}

fn step(n: Int): Int {
  if n == 0 { 0 } else { down(n - 1) }
}

fn main() {
  io.print(down(2))
}
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := strings.Join([]string{
		"enter 2", "operand 2", "leave 2",
		"enter 1", "operand 1", "leave 1",
		"enter 0", "operand 0", "leave 0",
		"0", "",
	}, "\n")
	if out != want {
		t.Fatalf("output:\n%s\nwant:\n%s", out, want)
	}
}

// A task body's tail loop runs in constant stack on the task's own goroutine
// and its value reaches the awaiting caller.
func TestTailCall_ATaskBodyLoopsInConstantStack(t *testing.T) {
	out, err := runDepth(t, `import std/io
import std/tasks.Task

fn count(n: Int, acc: Int): Int {
  if n == 0 { acc } else { count(n - 1, acc + 1) }
}

fn main() {
  concurrent {
    worker = Task.spawn(|| count(500000, 0))
    case Task.outcome(worker) {
      .Completed(v) -> io.print("done: ${v}")
      .Cancelled -> io.print("cancelled")
      .Failed(e) -> io.inspect(e)
    }
  }
}
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if out != "done: 500000\n" {
		t.Fatalf("output %q, want %q", out, "done: 500000\n")
	}
}

// The loop the limit counts is the one tail calls do not add to: one more
// than MaxCallDepth tail calls runs, where the same number of ordinary calls
// faults (TestCallDepth_InfiniteRecursionFaultsAtTheCall).
func TestTailCall_TailCallsDoNotCountTowardTheLimit(t *testing.T) {
	src := strings.ReplaceAll(`import std/io

fn count(n: Int): Int {
  if n == 0 { 0 } else { count(n - 1) }
}

fn main() {
  io.print(count(LIMIT))
}
`, "LIMIT", strconv.Itoa(rt.MaxCallDepth+1))
	out, err := runDepth(t, src)
	if err != nil || out != "0\n" {
		t.Fatalf("%d tail calls: output %q, err %v", rt.MaxCallDepth+1, out, err)
	}
}
