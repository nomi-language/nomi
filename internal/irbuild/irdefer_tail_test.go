package irbuild

import "testing"

// A `defer` that is a block's last statement registers its call, which runs
// right after it as the block ends, and the block answers Unit.
func TestIRDefer_ADeferEndingABlockRunsAsTheBlockEnds(t *testing.T) {
	verifyDeferProgram(t, `import std::io

fn note(label: String) -> Unit
  io.print("cleanup ${label}")
end

fn body_ends(n: Int)
  io.print("body ${n}")
  defer note("body")
end

fn main()
  if True then
    io.print("branch")
    defer note("branch")
  end
  case 2
    2 ->
      io.print("arm")
      defer note("arm")
    _ -> io.print("other")
  end
  body_ends(1)
  io.print("end")
end
`, "branch\ncleanup branch\narm\ncleanup arm\nbody 1\ncleanup body\nend\n",
		"body_ends", "main")
}

// A `defer` written directly in a `concurrent { }` body runs when the body
// ends, after the tasks it awaited, and on a trap out of the body.
func TestIRDefer_AConcurrentBodyRunsItsDeferredCalls(t *testing.T) {
	verifyDeferProgram(t, `import std::io
import std::tasks.Task

fn note(label: String) -> Unit
  io.print("cleanup ${label}")
end

fn main()
  total = concurrent
    defer note("first")
    t = Task.spawn(fn -> 20 + 1)
    defer note("second")
    io.print("body")
    Task.await(t) * 2
  end
  io.print(total)
  concurrent
    io.print("tail")
    defer note("tail")
  end
  io.print("end")
end
`, "body\ncleanup second\ncleanup first\n42\ntail\ncleanup tail\nend\n", "main")
}
