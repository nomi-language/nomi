package vm_test

// The call-depth limit (depth.go): unbounded recursion on the VM is an ordinary
// Nomi fault at the call that went one level too deep, not a Go stack
// overflow that kills the process. Every test here would kill the test binary
// without the limit, which is the failure it guards against.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/rt"
	"github.com/nomi-language/nomi/vmhost"
)

func depthProgram(t *testing.T, src string) *vmhost.Program {
	t.Helper()
	p, err := vmhost.LoadSource("main", src)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	return p
}

// runDepth runs main and answers its output and its failure, requiring that
// the VM ran it rather than refusing it.
func runDepth(t *testing.T, src string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := depthProgram(t, src).Run(context.Background(), &out, nil, false)
	if _, blocked := vmhost.IsBlocked(err); blocked {
		t.Fatalf("the VM refused the program, so the limit is untested: %v", err)
	}
	return out.String(), err
}

// requireDepthFault checks err is the depth fault at line, marked a Fault.
func requireDepthFault(t *testing.T, err error, line int) {
	t.Helper()
	if err == nil {
		t.Fatal("unbounded recursion returned without a failure")
	}
	if want := rt.CallDepthError(line).Error(); err.Error() != want {
		t.Fatalf("failure %q, want %q", err.Error(), want)
	}
	var fault *vm.Fault
	if !errors.As(err, &fault) {
		t.Fatalf("the depth failure is %T, not a *vm.Fault, so a caller would read it "+
			"as this machine's limit rather than the program's fault", err)
	}
}

// The recursive call is on line 5, and that line is the fault's.
func TestCallDepth_InfiniteRecursionFaultsAtTheCall(t *testing.T) {
	out, err := runDepth(t, `import std/io

fn down(n: Int): Int {
  io.print("x")
  down(n + 1) + 1
}

fn main() {
  io.print("start")
  down(0) |> io.print()
}
`)
	requireDepthFault(t, err, 5)
	// main plus MaxCallDepth-1 activations of down, each printing before
	// its call: the fault stops the program where the limit is, not earlier.
	if got := strings.Count(out, "x\n"); got != rt.MaxCallDepth-1 {
		t.Fatalf("down printed %d times before the fault, want %d", got, rt.MaxCallDepth-1)
	}
	if !strings.HasPrefix(out, "start\n") {
		t.Fatalf("output before the fault is missing: %q", out[:min(len(out), 40)])
	}
}

// The limit is exact: main is depth 1, sum(n) is depth 2, and sum(0) is depth
// n+2. So n = MaxCallDepth-2 runs to the limit and returns, and one more
// faults.
func TestCallDepth_RecursionUpToTheLimitRuns(t *testing.T) {
	src := func(n int) string {
		return fmt.Sprintf(`import std/io

fn sum(n: Int): Int {
  if n == 0 {
    0
  } else {
    n + sum(n - 1)
  }
}

fn main() {
  sum(%d) |> io.print()
}
`, n)
	}
	n := rt.MaxCallDepth - 2
	out, err := runDepth(t, src(n))
	if err != nil {
		t.Fatalf("recursion to exactly the limit failed: %v", err)
	}
	if want := fmt.Sprintf("%d\n", int64(n)*int64(n+1)/2); out != want {
		t.Fatalf("output %q, want %q", out, want)
	}

	_, err = runDepth(t, src(n+1))
	requireDepthFault(t, err, 7)
}

// A callback an iteration driver runs is counted from the activation that ran
// the iteration, so recursion through `Iter.map` faults too. Without that the
// driver would restart the count at every level and the process would die.
func TestCallDepth_RecursionThroughACallbackFaults(t *testing.T) {
	_, err := runDepth(t, `import std/io

fn walk(n: Int): Int {
  [n] |> Iter.map(|x| walk(x + 1) + 1) |> Iter.reduce(|acc = 0, x| acc + x)
}

fn main() {
  walk(0) |> io.print()
}
`)
	requireDepthFault(t, err, 4)
}

// A fault in a spawned task is that task's failure: the spawner reads it with
// Task.outcome and carries on. The task counts from zero, on its own stack.
func TestCallDepth_AFaultInATaskIsItsFailure(t *testing.T) {
	out, err := runDepth(t, `import std/io
import std/tasks.Task

fn down(n: Int): Int {
  down(n + 1) + 1
}

fn main() {
  concurrent {
    worker = Task.spawn(|| down(0))
    case Task.outcome(worker) {
      .Completed(v) -> io.print("done: ${v}")
      .Cancelled -> io.print("cancelled")
      .Failed(e) -> io.inspect(e)
    }
  }
  io.print("after")
}
`)
	if err != nil {
		t.Fatalf("the spawner failed; the task's fault must stay the task's: %v", err)
	}
	want := fmt.Sprintf("Errored(%q)\nafter\n", rt.CallDepthError(5).Error())
	if out != want {
		t.Fatalf("output %q, want %q", out, want)
	}
}

// A test case that recurses without bound fails with the fault, and the next
// case still runs.
func TestCallDepth_ATestCaseFailsAndTheNextRuns(t *testing.T) {
	p := depthProgram(t, `fn down(n: Int): Int {
  down(n + 1) + 1
}

test "recurses" {
  assert down(0) == 0
}

test "adds" {
  assert 1 + 2 == 3
}
`)
	cases := p.Cases(&bytes.Buffer{}, vmhost.TestOptions{})
	if len(cases) != 2 {
		t.Fatalf("%d cases, want 2: %+v", len(cases), cases)
	}
	if cases[0].Blocked != nil || cases[0].Err == nil ||
		!strings.Contains(cases[0].Err.Error(), rt.CallDepthError(2).Error()) {
		t.Fatalf("case recurses = %+v, want a failure carrying %q", cases[0], rt.CallDepthError(2))
	}
	if cases[1].Blocked != nil || cases[1].Err != nil {
		t.Fatalf("case adds = %+v, want a pass", cases[1])
	}
}
