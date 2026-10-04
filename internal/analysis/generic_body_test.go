package analysis

import "testing"

// Body returns wrong concrete type — should error
func TestCheck_GenericBodyWrongType(t *testing.T) {
	_, errs := checkSource(`
fn identity<T>(_x: T): T { 42 }
`)
	expectError(t, errs, "return type mismatch")
}

// Body uses + on type param — should error
func TestCheck_GenericBodyArithOnParam(t *testing.T) {
	_, errs := checkSource(`
fn double<T>(x: T): T { x + x }
`)
	expectError(t, errs, "cannot use `+` without an Add bound")
}

// Body returns param directly — should work
func TestCheck_GenericBodyPassthrough(t *testing.T) {
	_, errs := checkSource(`
fn identity<T>(x: T): T { x }
`)
	expectNoErrors(t, errs)
}

// Generic body calling another generic — should work
func TestCheck_GenericBodyCallsGeneric(t *testing.T) {
	_, errs := checkSource(`
fn identity<T>(x: T): T { x }
fn wrap<U>(y: U): U { identity(y) }
`)
	expectNoErrors(t, errs)
}

// Generic function wrapping in a list — should work
func TestCheck_GenericBodyWrapList(t *testing.T) {
	_, errs := checkSource(`
fn wrap<T>(x: T): List<T> { [x] }
`)
	expectNoErrors(t, errs)
}

// Generic function with tuple return
func TestCheck_GenericBodyTupleReturn(t *testing.T) {
	_, errs := checkSource(`
fn dup<T>(x: T): (T, T) { (x, x) }
`)
	expectNoErrors(t, errs)
}

// Generic function body returns wrong container — should error
func TestCheck_GenericBodyWrongContainer(t *testing.T) {
	_, errs := checkSource(`
fn wrap<T>(x: T): List<T> { x }
`)
	expectError(t, errs, "return type mismatch")
}

// Generic function with if expression
func TestCheck_GenericBodyIfExpr(t *testing.T) {
	_, errs := checkSource(`
fn choose<T>(cond: Bool, a: T, b: T): T {
  if cond { a } else { b }
}
`)
	expectNoErrors(t, errs)
}

// Generic function with lambda param
func TestCheck_GenericBodyApply(t *testing.T) {
	_, errs := checkSource(`
fn apply<T>(x: T, f: (T) -> T): T { f(x) }
`)
	expectNoErrors(t, errs)
}

// Generic function wrapping a mapped result — occurs check prevents U = Maybe<U>.
// Defines Maybe locally instead of importing stdlib so checkSource (which uses
// BuildFile, no stdlib) can resolve Some/None.
func TestCheck_GenericBodyMapMaybe(t *testing.T) {
	_, errs := checkSource(`
enum Maybe<T> { Some T; None }

fn map<T, U>(maybe: Maybe<T>, f: (T) -> U): Maybe<U> {
  case maybe {
    Some(v) -> Maybe.Some(f(v))
    None -> Maybe.None
  }
}
`)
	expectNoErrors(t, errs)
}
