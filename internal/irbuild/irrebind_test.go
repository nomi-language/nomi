package irbuild

import (
	"testing"
)

func TestIRRebind_RefinementsAndTypeChanges(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn main() {
  name = "  JANE  "
  name = String.trim(name)
  name = String.to_lower(name)
  io.print(name)
  value = 4
  value = "value ${value}"
  io.print(value)
}
`, "jane\nvalue 4\n")
}

func TestIRRebind_ParameterAndNestedScope(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn mark(n: Int): Int { io.print(n) return n }
fn work(n: Int): Int {
  n = mark(n + 1)
  other = {
    n = mark(n + 1)
    n = mark(n + 1)
    n
  }
  io.print(n)
  n = mark(n + other)
  n
}
fn main() { io.print(work(1)) }
`, "2\n3\n4\n2\n6\n6\n")
}

// TestIRRebind_CapturesKeepTheValueAtCreation pins spec §23: a closure
// captures the value a name holds when the closure is made, and a later
// same-scope rebinding is a new binding the closure never sees. A callable
// rebound after a read is a fresh binding too.
func TestIRRebind_CapturesKeepTheValueAtCreation(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{"x = 1 f = || x x = 2 _ = x io.print(f())", "1\n"},
		{"x = 1 f = || x x = 2 io.print(f() * 10 + x)", "12\n"},
		{"x = 1 x = x + 1 f = || x io.print(f())", "2\n"},
		{"x = 1 f = if True { || x } else { || 0 } x = 2 _ = x io.print(f())", "1\n"},
		{"f = one _ = f f = two io.print(f())", "2\n"},
	} {
		t.Run(tc.body, func(t *testing.T) {
			verifyLambdaProgram(t, "import std/io\nfn one(): Int { 1 }\nfn two(): Int { 2 }\nfn main() { "+tc.body+" }", tc.want)
		})
	}
}

// TestIRRebind_ACapturedParameterRebindRuns: a closure over a parameter keeps
// the argument after the body rebinds the parameter.
func TestIRRebind_ACapturedParameterRebindRuns(t *testing.T) {
	verifyLambdaProgram(t, "import std/io\nfn p(n: Int): Int {\n k = || n\n n = 5\n k() * 10 + n\n}\nfn main() { io.print(p(1)) }", "15\n")
}

// TestIRRebind_ASpawnedTaskReadsTheValueAtItsCreation: a task's body is a
// closure, so it reads what a name held when the closure was made, however
// late it runs. The task blocks on a receive the scope sends to only after
// rebinding x, so a capture that followed the rebinding would answer 22.
func TestIRRebind_ASpawnedTaskReadsTheValueAtItsCreation(t *testing.T) {
	verifyLambdaProgram(t, `import {
  std/channels.{Channel, Receiver, Sender}
  std/io
  std/tasks.Task
}

fn main() {
  r = concurrent {
    ch: Channel<Int> = Channel.unbuffered()
    x = 1
    t = Task.spawn(|| {
      _ = Receiver.receive(ch.receiver)
      x
    })
    x = 2
    _ = Sender.send(ch.sender, 0)
    Task.await(t) * 10 + x
  }
  io.print(r)
}
`, "12\n")
}
