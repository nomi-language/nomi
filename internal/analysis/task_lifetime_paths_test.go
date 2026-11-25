package analysis_test

import (
	"strings"
	"testing"
)

// pathsFixture binds a handle `t` in a concurrent block that may leave by
// `?`, and runs BODY, which ends with the block's value. With the import
// analyzeConcurrent adds, t is bound at line 8 and BODY starts at line 9.
const pathsFixture = `fn job(n: Int) -> Result<Int, String>
  Ok(n)
end
fn demo(cached: Bool, n: Int) -> Result<Int, String>
  concurrent
    t = Task.spawn(fn -> 42)
    BODY
  end
end`

// A handle settled on every path that reaches the block's end is settled,
// whatever the other paths do. The control, the same block with no settle,
// reports t, so the block is known to be checked.
func TestTaskLifetime_SettledOnEveryPath_OK(t *testing.T) {
	control := notSettled(analyzeConcurrent(strings.ReplaceAll(pathsFixture, "BODY", "Ok(0)")))
	if _, ok := control["t"]; !ok {
		t.Fatalf("the control does not report t: %v", control)
	}
	for _, tc := range []struct{ name, body string }{
		{"settled in both branches",
			"if cached then\n      _ = Task.await(t)\n    else\n      _ = Task.outcome(t)\n    end\n    Ok(0)"},
		{"cancel in one branch and await in the other",
			"if cached then\n      Task.cancel(t)\n    else\n      _ = Task.await(t)\n    end\n    Ok(0)"},
		{"settled after a branch",
			"if cached then\n      _ = Task.inspect(t)\n    end\n    _ = Task.await(t)\n    Ok(0)"},
		{"settled after a `?`",
			"x = job(n)?\n    _ = Task.await(t)\n    Ok(x)"},
		{"a branch that leaves by `?` only",
			"x =\n      if cached then\n        job(n)?\n      else\n        Task.await(t)\n      end\n    _ = Task.await(t)\n    Ok(x)"},
		{"a branch that returns",
			"if cached then\n      return Err(\"cached\")\n    end\n    _ = Task.await(t)\n    Ok(0)"},
		{"the other branch returns",
			"if cached then\n      return Err(\"cached\")\n    else\n      _ = Task.await(t)\n    end\n    Ok(0)"},
		{"a branch that traps",
			"if cached then\n      unreachable \"no cache\"\n    end\n    _ = Task.await(t)\n    Ok(0)"},
		{"a case with every arm settling",
			"x =\n      case n\n        0 -> Task.await(t)\n        _ -> Task.await(t) + 1\n      end\n    Ok(x)"},
		{"a case arm that returns",
			"x =\n      case n\n        0 -> return Err(\"zero\")\n        _ -> Task.await(t)\n      end\n    Ok(x)"},
		{"an else-if chain settling in every branch",
			"if cached then\n      _ = Task.await(t)\n    else if n > 0 then\n      Task.cancel(t)\n    else\n      _ = Task.outcome(t)\n    end\n    Ok(0)"},
		{"a handle bound and settled in one branch",
			"_ = Task.await(t)\n    if cached then\n      u = Task.spawn(fn -> 1)\n      _ = Task.await(u)\n    end\n    Ok(0)"},
		{"every path leaves",
			"if cached then\n      return Err(\"a\")\n    else\n      return Err(\"b\")\n    end"},
		{"a lambda passed directly settles its parameter on every path",
			"Iter.each([t], fn h -> if cached then Task.cancel(h) else Task.cancel(h))\n    Ok(0)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := strings.ReplaceAll(pathsFixture, "BODY", tc.body)
			errs := analyzeConcurrent(src)
			if got := notSettled(errs); len(got) != 0 {
				t.Fatalf("settled handles reported: %v", got)
			}
		})
	}
}

// A handle some path to the block's end leaves unsettled is the
// task-not-settled error, at its binding, naming that path or where the
// settle stands that may not run.
func TestTaskLifetime_UnsettledOnSomePath_Errors(t *testing.T) {
	for _, tc := range []struct{ name, body, says string }{
		{"one branch only",
			"if cached then\n      _ = Task.await(t)\n    else\n      _ = 0\n    end\n    Ok(0)",
			"Task bound to 't' is not awaited or cancelled on every path: the `else` of the `if` at line 9 leaves it neither awaited nor cancelled"},
		{"the other branch only",
			"if cached then\n      _ = 0\n    else\n      Task.cancel(t)\n    end\n    Ok(0)",
			"the `then` branch of the `if` at line 9 leaves it neither awaited nor cancelled"},
		{"a missing else",
			"if cached then\n      _ = Task.await(t)\n    end\n    Ok(0)",
			"the missing `else` of the `if` at line 9 leaves it neither awaited nor cancelled"},
		{"a case arm that does not settle",
			"x =\n      case n\n        0 -> Task.await(t)\n        _ -> 1\n      end\n    Ok(x)",
			"the arm at line 12 of the `case` at line 10 leaves it neither awaited nor cancelled"},
		{"only inside a loop",
			"Iter.each([1, 2], fn _ -> Task.cancel(t))\n    Ok(0)",
			"Task bound to 't' is never awaited or cancelled: line 9 awaits or cancels it only inside a lambda, which may never run"},
		{"only inside a capture",
			"Iter.each([1, 2], &Task.cancel(t))\n    Ok(0)",
			"line 9 awaits or cancels it only inside a capture, which may never run"},
		{"a captured lambda bound to a name",
			"_f = fn -> Task.outcome(t)\n    Ok(0)",
			"line 9 awaits or cancels it only inside a lambda, which may never run"},
		{"only on the right of `and`",
			"_ok = cached and Task.await(t) > 0\n    Ok(0)",
			"line 9 awaits or cancels it only where it may not run"},
		{"inspected in one branch, cancelled in the other",
			"if cached then\n      _ = Task.inspect(t)\n    else\n      Task.cancel(t)\n    end\n    Ok(0)",
			"the `then` branch of the `if` at line 9"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := strings.ReplaceAll(pathsFixture, "BODY", tc.body)
			errs := analyzeConcurrent(src)
			got := notSettled(errs)
			e, ok := got["t"]
			if !ok {
				t.Fatalf("t is not reported, so the block is admitted; errors: %v", errs)
			}
			if len(got) != 1 {
				t.Errorf("want only t reported, got %v", got)
			}
			if e.Line != 8 {
				t.Errorf("the error is at line %d, want the binding's line 8", e.Line)
			}
			if !strings.Contains(e.Message, tc.says) {
				t.Errorf("message %q does not contain %q", e.Message, tc.says)
			}
		})
	}
}

// A lambda that captures a handle and is the block's value settles
// nothing: the block ends with the task unsettled.
func TestTaskLifetime_LambdaReturnedFromTheBlock_Errors(t *testing.T) {
	src := `fn demo() -> Int
  f =
    concurrent
      t = Task.spawn(fn -> 42)
      fn -> Task.await(t)
    end
  f()
end`
	errs := analyzeConcurrent(src)
	e, ok := notSettled(errs)["t"]
	if !ok {
		t.Fatalf("t is not reported, so the block is admitted; errors: %v", errs)
	}
	if want := "line 7 awaits or cancels it only inside a lambda, which may never run"; !strings.Contains(e.Message, want) {
		t.Errorf("message %q does not contain %q", e.Message, want)
	}
}
