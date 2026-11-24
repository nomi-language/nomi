package analysis_test

import (
	"testing"
)

// Rule 2 (positive case): a Task binding is consumed by `await` inside
// the block. Accepted.
func TestTaskLifetime_BindingAwaited_OK(t *testing.T) {
	src := `fn demo() -> Int
  concurrent
    t = Task.spawn(fn -> 42)
    Task.await(t)
  end
end`
	errs := analyzeConcurrent(src)
	expectNoConcurrentError(t, errs, "is never awaited")
}

// Rule 2 (negative case): a Task binding whose name is never
// referenced after the binding. Rejected.
func TestTaskLifetime_BindingNeverAwaited_Errors(t *testing.T) {
	src := `fn demo() -> Int
  concurrent
    t = Task.spawn(fn -> 42)
    0
  end
end`
	errs := analyzeConcurrent(src)
	expectConcurrentError(t, errs, "Task bound to 't' is never awaited")
}

// Rule 2: a Task binding awaited in the block's tail expression is
// settled.
func TestTaskLifetime_BindingReferencedAsTail_OK(t *testing.T) {
	src := `fn demo() -> Int
  concurrent
    t = Task.spawn(fn -> 42)
    Task.await(t)
  end
end`
	errs := analyzeConcurrent(src)
	expectNoConcurrentError(t, errs, "is never awaited")
}

// Rule 2: a Task binding declared in an outer `concurrent` block and
// awaited inside a nested one is settled: the settle walk enters nested
// blocks. The nested block's own Rule 2 and Rule 3 checks still run
// independently.
func TestTaskLifetime_BindingReferencedInNestedConcurrent_OK(t *testing.T) {
	src := `fn demo() -> Int
  concurrent
    t = Task.spawn(fn -> 42)
    concurrent
      Task.await(t)
    end
    0
  end
end`
	errs := analyzeConcurrent(src)
	expectNoConcurrentError(t, errs, "is never awaited")
}

// Rule 3 (positive case): the block's tail expression has a non-Task
// type. Accepted.
func TestTaskLifetime_BlockTailNonTask_OK(t *testing.T) {
	src := `fn demo() -> Int
  concurrent
    t = Task.spawn(fn -> 42)
    Task.await(t)
  end
end`
	errs := analyzeConcurrent(src)
	expectNoConcurrentError(t, errs, "cannot escape")
}

// Rule 3 (negative case): the block's tail expression IS the
// `Task.spawn(...)` call directly — Task value escapes via the block's
// return value into the enclosing function's return.
func TestTaskLifetime_BlockTailIsSpawn_Errors(t *testing.T) {
	src := `fn spawn_one() -> Task<Int>
  concurrent
    Task.spawn(fn -> 42)
  end
end
fn demo() -> Int
  spawn_one()
  0
end`
	errs := analyzeConcurrent(src)
	expectConcurrentError(t, errs, "cannot escape")
}

// Rule 3 (negative case via reference): the block's tail expression is
// a reference to a binding whose value is a Task. Same escape.
func TestTaskLifetime_BlockTailRefersToTaskBinding_Errors(t *testing.T) {
	src := `fn spawn_one() -> Task<Int>
  concurrent
    t = Task.spawn(fn -> 42)
    t
  end
end
fn demo() -> Int
  spawn_one()
  0
end`
	errs := analyzeConcurrent(src)
	expectConcurrentError(t, errs, "cannot escape")
}

// Rule 3 (negative case via tuple): the block's tail expression is a
// tuple containing a Task value. The tuple value escapes when the
// block returns it.
func TestTaskLifetime_BlockTailTupleWithTask_Errors(t *testing.T) {
	src := `fn spawn_pair() -> (Int, Task<Int>)
  concurrent
    t = Task.spawn(fn -> 42)
    (1, t)
  end
end
fn demo() -> Int
  spawn_pair()
  0
end`
	errs := analyzeConcurrent(src)
	expectConcurrentError(t, errs, "cannot escape")
}

// Rule 2: discarding the handle, `_ = t`, does not settle it, because it
// skips the wait (spec §16). The error is at the binding and says what the
// block did with the handle; `_ = Task.await(t)` is accepted.
func TestTaskLifetime_DiscardingTheHandle_Errors(t *testing.T) {
	src := `fn demo() -> Int
  concurrent
    t = Task.spawn(fn -> 42)
    _ = t
    0
  end
end`
	errs := analyzeConcurrent(src)
	expectConcurrentError(t, errs, "Task bound to 't' is never awaited or cancelled: line 6 discards it with `_ =`")

	awaited := `fn demo() -> Int
  concurrent
    t = Task.spawn(fn -> 42)
    _ = Task.await(t)
    0
  end
end`
	errs = analyzeConcurrent(awaited)
	expectNoConcurrentError(t, errs, "is never awaited")
}
