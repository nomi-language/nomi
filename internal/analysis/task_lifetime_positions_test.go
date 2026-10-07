package analysis_test

import (
	"strings"
	"testing"
)

// escapingBlock is a concurrent block whose tail carries its task out.
const escapingBlock = "concurrent { Task.spawn(|| 42) }"

// The task-lifetime rules apply to a concurrent block wherever it sits:
// every expression position, every kind of body. Each fixture puts
// escapingBlock (spelled E) in one position.
func TestTaskLifetime_EscapeIsFoundInEveryPosition(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"function tail", `fn f(): Task<Int> {
  E
}`},
		{"case scrutinee", `fn f(): Int {
  case E {
    _ -> 0
  }
}`},
		{"case arm", `fn f(n: Int): Task<Int> {
  case n {
    _ -> E
  }
}`},
		{"case guard", `fn f(n: Int): Int {
  case n {
    _ when Task.await(E) == 1 -> 0
    _ -> 1
  }
}`},
		{"pipe", `fn f(): Int {
  E |> Task.await
}`},
		{"interpolation", `fn f(): String {
  "${Task.await(E)}"
}`},
		{"named argument", `fn g(n: Int): Int {
  n
}
fn f(): Int {
  g(n: Task.await(E))
}`},
		{"range bound", `fn f(): Int {
  r = 0..Task.await(E)
  0
}`},
		{"list spread", `fn f(): List<Int> {
  [1, ..[Task.await(E)]]
}`},
		{"if pattern", `fn f(): Int {
  if 42 = Task.await(E) {
    1
  } else {
    0
  }
}`},
		{"impl function", `struct Box {
  n: Int
}
impl Box {
  fn spawn(): Task<Int> {
    E
  }
}`},
		{"test body", `test "escape" {
  _ = Task.await(E)
  assert True
}`},
		{"assertion", `test "escape" {
  assert Task.await(E) == 42
}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := strings.ReplaceAll(tc.src, "E", escapingBlock)
			errs := analyzeConcurrent(src)
			expectConcurrentError(t, errs, "Task<T> cannot escape its enclosing concurrent block")
		})
	}
}

// A task awaited in any expression position inside its block counts as
// awaited: the reference sweep reaches every position the escape walk does.
// Each fixture's control, the same block without the use, is rejected, so
// the block is known to be checked at all.
func TestTaskLifetime_AwaitIsFoundInEveryPosition(t *testing.T) {
	for _, tc := range []struct{ name, use string }{
		{"named argument", "_ = g(n: Task.await(t))"},
		{"range bound", "_ = 0..Task.await(t)"},
		{"list spread", "_ = [1, ..[Task.await(t)]]"},
		{"assertion", "assert Task.await(t) == 42"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := `fn g(n: Int): Int {
  n
}
test "await" {
  concurrent {
    t = Task.spawn(|| 42)
    USE
  }
  assert True
}`
			errs := analyzeConcurrent(strings.ReplaceAll(src, "USE", tc.use))
			expectNoConcurrentError(t, errs, "is never awaited")
			errs = analyzeConcurrent(strings.ReplaceAll(src, "USE", "_ = 0"))
			expectConcurrentError(t, errs, "Task bound to 't' is never awaited")
		})
	}
}
