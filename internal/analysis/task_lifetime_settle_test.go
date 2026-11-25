package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// settleFixture binds a single handle `t` and a list of handles `hs` in one
// concurrent block, settles `t` unless USE does, and runs USE.
const settleFixture = `struct Box
  n: Int
end
impl Box
  fn cancel<T>(task: Task<T>) -> Unit
    Task.cancel(task)
  end
end
fn job(n: Int) -> Result<Int, String>
  Ok(n)
end
fn wait_for(task: Task<Int>) -> Int
  Task.await(task)
end
test "settle"
  concurrent
    t = Task.spawn(fn -> 42)
    hs = Task.spawn_all([1, 2], job, max_running: 2)
    USE
  end
  assert True
end`

// notSettled answers the Rule 2 errors in errs, by the binding they name.
func notSettled(errs []analysis.TypeError) map[string]analysis.TypeError {
	out := map[string]analysis.TypeError{}
	for _, e := range errs {
		if e.Code == analysis.TaskNotSettledCode && len(e.Args) == 1 {
			out[e.Args[0]] = e
		}
	}
	return out
}

// Each settling position settles the handles it names. The control, the
// same block with a use that settles nothing, reports both, so the block
// is known to be checked.
func TestTaskLifetime_SettlingPositions_OK(t *testing.T) {
	for _, tc := range []struct{ name, use string }{
		{"await and await_all", "_ = Task.await(t)\n    _ = Task.await_all(hs)"},
		{"outcome and try_await_all", "_ = Task.outcome(t)\n    _ = Task.try_await_all(hs)"},
		{"cancel, and a settling function as an argument", "Task.cancel(t)\n    Iter.each(hs, Task.cancel)"},
		{"a handle inside an argument", "_ = Task.await_all([t])\n    _ = Task.await_all(Iter.to_list(hs))"},
		{"a lambda that settles its parameter", "Task.cancel(t)\n    Iter.each(hs, fn h -> Task.cancel(h))"},
		{"a lambda over a destructured parameter", "Task.cancel(t)\n    Iter.each(Iter.with_index(hs), fn (_, h) -> Task.cancel(h))"},
		{"a capture", "Task.cancel(t)\n    Iter.each(hs, &Task.cancel(&1))"},
		{"a pipe into a settling call", "_ = t |> Task.await()\n    _ = hs |> Task.await_all()"},
		{"a pipe into a call with a settling argument", "Task.cancel(t)\n    hs |> Iter.each(Task.cancel)"},
		{"a pipe chain", "Task.cancel(t)\n    hs |> Iter.filter(fn _ -> True) |> Iter.each(Task.cancel)"},
		{"a nested concurrent block", "concurrent\n      _ = Task.await(t)\n      _ = Task.await_all(hs)\n    end"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := analyzeConcurrent(strings.ReplaceAll(settleFixture, "USE", tc.use))
			if got := notSettled(errs); len(got) != 0 {
				t.Fatalf("settled handles reported: %v", got)
			}
			control := notSettled(analyzeConcurrent(strings.ReplaceAll(settleFixture, "USE", "_ = 0")))
			if _, ok := control["t"]; !ok {
				t.Fatalf("the control does not report t: %v", control)
			}
			if _, ok := control["hs"]; !ok {
				t.Fatalf("the control does not report hs: %v", control)
			}
		})
	}
}

// No other use settles a handle, and the error says what the block did
// with it.
func TestTaskLifetime_OtherUses_Errors(t *testing.T) {
	for _, tc := range []struct{ name, use, handle, says string }{
		{"Task.inspect", "_ = Task.inspect(t)\n    Iter.each(hs, Task.cancel)", "t",
			"Task bound to 't' is never awaited or cancelled: line 21 passes it to `Task.inspect`"},
		{"a pipe into Task.inspect", "_ = t |> Task.inspect()\n    Iter.each(hs, Task.cancel)", "t",
			"line 21 passes it to `Task.inspect`"},
		{"counting a list", "Task.cancel(t)\n    _ = Iter.count(hs)", "hs",
			"Tasks bound to 'hs' are never awaited or cancelled: line 22 passes them to `Iter.count`"},
		{"copying into a list", "_u = [t]\n    Iter.each(hs, Task.cancel)", "t",
			"line 21 copies it to `_u`"},
		{"copying to a name", "_x = t\n    Iter.each(hs, Task.cancel)", "t",
			"line 21 copies it to `_x`"},
		{"discarding", "_ = t\n    Iter.each(hs, Task.cancel)", "t",
			"line 21 discards it with `_ =`"},
		{"a function the program declares", "_ = wait_for(t)\n    Iter.each(hs, Task.cancel)", "t",
			"line 21 passes it to `wait_for`"},
		{"a program's own function named cancel", "Box.cancel(t)\n    Iter.each(hs, Task.cancel)", "t",
			"line 21 passes it to `Box.cancel`"},
		{"a lambda that settles something other than its parameter", "Task.cancel(t)\n    Iter.each(hs, fn _ -> Task.cancel(t))", "hs",
			"line 22 passes them to `Iter.each`"},
		{"never used", "Task.cancel(t)", "hs",
			"Tasks bound to 'hs' are never awaited or cancelled; pass them to `Task.await`, `Task.await_all`, `Task.outcome` or `Task.cancel`"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := analyzeConcurrent(strings.ReplaceAll(settleFixture, "USE", tc.use))
			got := notSettled(errs)
			e, ok := got[tc.handle]
			if !ok {
				t.Fatalf("%s is not reported; errors: %v", tc.handle, errs)
			}
			if len(got) != 1 {
				t.Errorf("want only %s reported, got %v", tc.handle, got)
			}
			if !strings.Contains(e.Message, tc.says) {
				t.Errorf("message %q does not contain %q", e.Message, tc.says)
			}
			if !strings.Contains(e.Message, "`Task.await`, `Task.await_all`, `Task.outcome` or `Task.cancel`") {
				t.Errorf("message %q does not name the settling functions", e.Message)
			}
		})
	}
}

// A task a supervisor owns is not held to Rule 2: its handle needs no use.
func TestTaskLifetime_SupervisedTaskNeedsNoSettling_OK(t *testing.T) {
	src := `import
  std::supervisors.Supervisor
  std::tasks.Task
end

fn start(sup: Supervisor) -> Int
  concurrent
    s = Supervisor.spawn(sup, fn -> Unit)
    _ = Task.inspect(s)
    t = Task.spawn(fn -> 42)
    0
  end
end`
	got := notSettled(analyzeConcurrentRaw(src))
	if _, ok := got["s"]; ok {
		t.Errorf("a supervised task is reported: %v", got)
	}
	if _, ok := got["t"]; !ok {
		t.Errorf("the block's own task is not reported: %v", got)
	}
}
