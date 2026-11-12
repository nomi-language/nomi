package irbuild

import (
	"testing"
)

func TestIRRebind_RefinementsAndTypeChanges(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn main()
  name = "  JANE  "
  name = String.trim(name)
  name = String.lowercase(name)
  io.print(name)
  value = 4
  value = "value ${value}"
  io.print(value)
end
`, "jane\nvalue 4\n")
}

func TestIRRebind_ParameterAndNestedScope(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn mark(n: Int) -> Int
    io.print(n)
    return n
end
fn work(n: Int) -> Int
  n = mark(n + 1)
  other = begin
    n = mark(n + 1)
    n = mark(n + 1)
    n
  end
  io.print(n)
  n = mark(n + other)
  n
end
fn main()
    io.print(work(1))
end
`, "2\n3\n4\n2\n6\n6\n")
}

// TestIRRebind_CapturesKeepTheValueAtCreation pins spec §7 *Closures*: a closure
// captures the value a name holds when the closure is made, and a later
// same-scope rebinding is a new binding the closure never sees. A callable
// rebound after a read is a fresh binding too.
func TestIRRebind_CapturesKeepTheValueAtCreation(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{"x = 1\n  f = fn -> x\n  x = 2\n  _ = x\n  io.print(f())", "1\n"},
		{"x = 1\n  f = fn -> x\n  x = 2\n  io.print(f() * 10 + x)", "12\n"},
		{"x = 1\n  x = x + 1\n  f = fn -> x\n  io.print(f())", "2\n"},
		{"x = 1\n  f = (if True then (fn -> x) else fn -> 0)\n  x = 2\n  _ = x\n  io.print(f())", "1\n"},
		{"f = one\n  _ = f\n  f = two\n  io.print(f())", "2\n"},
	} {
		t.Run(tc.body, func(t *testing.T) {
			verifyLambdaProgram(t, "import std::io\nfn one() -> Int\n  1\nend\nfn two() -> Int\n  2\nend\nfn main()\n  "+tc.body+"\nend\n", tc.want)
		})
	}
}

// TestIRRebind_ACapturedParameterRebindRuns: a closure over a parameter keeps
// the argument after the body rebinds the parameter.
func TestIRRebind_ACapturedParameterRebindRuns(t *testing.T) {
	verifyLambdaProgram(t, "import std::io\nfn p(n: Int) -> Int\n k = fn -> n\n n = 5\n k() * 10 + n\nend\nfn main()\n    io.print(p(1))\nend", "15\n")
}

// TestIRRebind_ASpawnedTaskReadsTheValueAtItsCreation: a task's body is a
// closure, so it reads what a name held when the closure was made, however
// late it runs. The task blocks on a receive the scope sends to only after
// rebinding x, so a capture that followed the rebinding would answer 22.
func TestIRRebind_ASpawnedTaskReadsTheValueAtItsCreation(t *testing.T) {
	verifyLambdaProgram(t, `import
  std::channels.{Channel, Receiver, Sender}
  std::io
  std::tasks.Task
end

fn main()
  r = concurrent
    ch: Channel<Int> = Channel.unbuffered()
    x = 1
    t = Task.spawn(fn ->
      _ = Receiver.receive(ch.receiver)
      x
    end)
    x = 2
    _ = Sender.send(ch.sender, 0)
    Task.await(t) * 10 + x
  end
  io.print(r)
end
`, "12\n")
}
