package analysis_test

import "testing"

// A `?` after a function's name propagates the function value, so
// `is_empty?(xs)` is the ordinary `?` error about a function type.
func TestCheckTryOpAfterAPredicateName(t *testing.T) {
	src := `fn is_empty(xs: List<Int>) -> Bool {
  xs == []
}
fn demo() -> Bool {
  is_empty?([1])
}`
	_, errs := checkSourceWithStdlib(src)
	expectErrorT(t, errs, "`?` requires a Result or Maybe, got (List<Int>) -> Bool")
}

// `handler?(x)` propagates a Maybe of a function and calls its payload.
func TestCheckTryOpThenCall(t *testing.T) {
	src := `fn apply(handler: Maybe<(Int) -> Int>, x: Int) -> Maybe<Int> {
  Some(handler?(x))
}`
	_, errs := checkSourceWithStdlib(src)
	expectNoErrorsT(t, errs)
}

// `try` is an ordinary name.
func TestCheckTryIsAName(t *testing.T) {
	src := `fn try(x: Int) -> Int {
  x * 2
}
fn demo() -> Int {
  try = try(3)
  try + 1
}`
	_, errs := checkSourceWithStdlib(src)
	expectNoErrorsT(t, errs)
}
