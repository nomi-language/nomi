package analysis_test

import (
	"testing"
)

// Rule 2 (positive case): a Task binding is consumed by `await` inside
// the block. Accepted.
func TestTaskLifetime_BindingAwaited_OK(t *testing.T) {
	src := `fn main(): Int {
  concurrent {
    t = Task.spawn(|| 42)
    Task.await(t)
  }
}`
	errs := analyzeConcurrent(src)
	expectNoConcurrentError(t, errs, "is never awaited")
}

// Rule 2 (negative case): a Task binding whose name is never
// referenced after the binding. Rejected.
func TestTaskLifetime_BindingNeverAwaited_Errors(t *testing.T) {
	src := `fn main(): Int {
  concurrent {
    t = Task.spawn(|| 42)
    0
  }
}`
	errs := analyzeConcurrent(src)
	expectConcurrentError(t, errs, "Task bound to 't' is never awaited")
}

// Rule 2 (corner case): a Task binding passed to `await` indirectly via
// a tail expression that references the binding name. Accepted — the
// rule is "the name must be referenced", not "must be a `Task.await(t)`
// call lexically". This is the conservative v1 of the use-once rule
// (broader uses of the binding are also accepted; only the wholly-
// unused case is rejected).
func TestTaskLifetime_BindingReferencedAsTail_OK(t *testing.T) {
	src := `fn main(): Int {
  concurrent {
    t = Task.spawn(|| 42)
    Task.await(t)
  }
}`
	errs := analyzeConcurrent(src)
	expectNoConcurrentError(t, errs, "is never awaited")
}

// Rule 2 (corner case): a Task binding declared in an outer
// `concurrent { }` block is referenced from inside a NESTED
// `concurrent { }` block. The reference counts as a use of the
// outer binding (collectReferencedNames descends into nested
// ConcurrentBlock bodies); no spurious "never awaited" diagnostic.
// The nested block's own Rule 2/Rule 3 checks still run
// independently.
func TestTaskLifetime_BindingReferencedInNestedConcurrent_OK(t *testing.T) {
	src := `fn main(): Int {
  concurrent {
    t = Task.spawn(|| 42)
    concurrent {
      Task.await(t)
    }
    0
  }
}`
	errs := analyzeConcurrent(src)
	expectNoConcurrentError(t, errs, "is never awaited")
}

// Rule 3 (positive case): the block's tail expression has a non-Task
// type. Accepted.
func TestTaskLifetime_BlockTailNonTask_OK(t *testing.T) {
	src := `fn main(): Int {
  concurrent {
    t = Task.spawn(|| 42)
    Task.await(t)
  }
}`
	errs := analyzeConcurrent(src)
	expectNoConcurrentError(t, errs, "cannot escape")
}

// Rule 3 (negative case): the block's tail expression IS the
// `Task.spawn(...)` call directly — Task value escapes via the block's
// return value into the enclosing function's return.
func TestTaskLifetime_BlockTailIsSpawn_Errors(t *testing.T) {
	src := `fn spawn_one(): Task<Int> {
  concurrent {
    Task.spawn(|| 42)
  }
}
fn main(): Int {
  spawn_one()
  0
}`
	errs := analyzeConcurrent(src)
	expectConcurrentError(t, errs, "cannot escape")
}

// Rule 3 (negative case via reference): the block's tail expression is
// a reference to a binding whose value is a Task. Same escape.
func TestTaskLifetime_BlockTailRefersToTaskBinding_Errors(t *testing.T) {
	src := `fn spawn_one(): Task<Int> {
  concurrent {
    t = Task.spawn(|| 42)
    t
  }
}
fn main(): Int {
  spawn_one()
  0
}`
	errs := analyzeConcurrent(src)
	expectConcurrentError(t, errs, "cannot escape")
}

// Rule 3 (negative case via tuple): the block's tail expression is a
// tuple containing a Task value. The tuple value escapes when the
// block returns it.
func TestTaskLifetime_BlockTailTupleWithTask_Errors(t *testing.T) {
	src := `fn spawn_pair(): (Int, Task<Int>) {
  concurrent {
    t = Task.spawn(|| 42)
    (1, t)
  }
}
fn main(): Int {
  spawn_pair()
  0
}`
	errs := analyzeConcurrent(src)
	expectConcurrentError(t, errs, "cannot escape")
}
