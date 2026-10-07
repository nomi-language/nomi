package analysis_test

import (
	"strings"
	"testing"
)

// Spec §9's `try` boundary rule, at a `fn` boundary with a DECLARED result
// type. Three parts, all checked at the `try` and all naming both sides:
//
//  1. the boundary must be a `Result` or a `Maybe`;
//  2. the flavours must agree;
//  3. when both are `Result`s the ERROR TYPES must be the same type.
//
// (3) is the soundness rule: `try` propagates the operand's `Err` payload
// unchanged, so without it a function's declared error type would stop
// describing what it could return, and the failure would surface far away
// (`Display.to_string: no implementation for type 'json.Json.DecodeError'`)
// naming neither the `try` nor the boundary.

func TestTryBoundary_ErrorTypeMustMatch(t *testing.T) {
	src := `fn r(): Result<Int, Int> {
  Err(7)
}
fn f(): Result<Int, String> {
  v = try r()
  Ok(v)
}`
	errs := analyzeConcurrentRaw(src)
	expectConcurrentError(t, errs, "try error type mismatch: expected String, got Int")
	// The diagnostic must land on the `try`, not on the function or the
	// binding — that is the whole point of reporting at the site.
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "try error type mismatch") {
			found = true
			if e.Line != 5 {
				t.Fatalf("expected the diagnostic on the `try` line (5), got line %d", e.Line)
			}
		}
	}
	if !found {
		t.Fatal("no try error type mismatch diagnostic")
	}
}

func TestTryBoundary_MatchingErrorTypeIsClean(t *testing.T) {
	src := `fn r(): Result<Int, String> {
  Err("boom")
}
fn f(): Result<Int, String> {
  v = try r()
  Ok(v)
}`
	expectNoConcurrentError(t, analyzeConcurrentRaw(src), "try error type mismatch")
}

// The language's answer to a mismatch is conversion AT THE SITE with
// `Result.map_err` (spec §9), not an implicit widening at the boundary.
func TestTryBoundary_MapErrAtTheSiteIsClean(t *testing.T) {
	src := `fn r(): Result<Int, Int> {
  Err(7)
}
fn f(): Result<Int, String> {
  v = try Result.map_err(r(), |code: Int| Int.to_string(code))
  Ok(v)
}`
	expectNoConcurrentError(t, analyzeConcurrentRaw(src), "try error type mismatch")
}

func TestTryBoundary_MaybeCannotPropagateIntoAResult(t *testing.T) {
	src := `fn find(): Maybe<Int> {
  None
}
fn f(): Result<Int, String> {
  v = try find()
  Ok(v)
}`
	expectConcurrentError(t, analyzeConcurrentRaw(src),
		"`try` on a Maybe cannot propagate out of a function returning Result<Int, String>")
}

func TestTryBoundary_ResultCannotPropagateIntoAMaybe(t *testing.T) {
	src := `fn r(): Result<Int, String> {
  Err("boom")
}
fn f(): Maybe<Int> {
  v = try r()
  Some(v)
}`
	expectConcurrentError(t, analyzeConcurrentRaw(src),
		"`try` on a Result cannot propagate out of a function returning Maybe<Int>")
}

func TestTryBoundary_MaybeIntoMaybeIsClean(t *testing.T) {
	src := `fn find(): Maybe<Int> {
  None
}
fn f(): Maybe<Int> {
  v = try find()
  Some(v)
}`
	errs := analyzeConcurrentRaw(src)
	expectNoConcurrentError(t, errs, "cannot propagate")
	expectNoConcurrentError(t, errs, "try error type mismatch")
}

func TestTryBoundary_NonResultBoundaryIsRejected(t *testing.T) {
	src := `fn r(): Result<Int, String> {
  Err("boom")
}
fn f(): Int {
  v = try r()
  v
}`
	expectConcurrentError(t, analyzeConcurrentRaw(src),
		"`try` cannot propagate a Result out of a function returning Int")
}

// A function with no declared return type has a Unit result, so there is
// nothing for the propagated value to inhabit. This is the `fn main()` shape.
func TestTryBoundary_UndeclaredReturnTypeIsRejected(t *testing.T) {
	src := `fn r(): Result<Int, String> {
  Err("boom")
}
fn f() {
  _ = try r()
}`
	expectConcurrentError(t, analyzeConcurrentRaw(src),
		"`try` cannot propagate a Result out of a function returning Unit")
}

// A test body declares NO result — a propagating `try` there ends the case
// with `test returned early` rather than returning a value anywhere — so the
// boundary rule has nothing to say about it and must stay silent.
func TestTryBoundary_TestBodyIsExempt(t *testing.T) {
	src := `fn r(): Result<Int, Int> {
  Err(7)
}
test "propagates" {
  v = try r()
  assert v == 1
}`
	errs := analyzeConcurrentRaw(src)
	expectNoConcurrentError(t, errs, "try error type mismatch")
	expectNoConcurrentError(t, errs, "cannot propagate")
}

// An INTERFACE-typed declared error side accepts a concrete implementer, the
// same relaxation an interface-typed parameter gets. This is the one place the
// rule is inhabitation rather than literal equality, and it is deliberate: the
// propagated value's type must inhabit the boundary's declared type.
func TestTryBoundary_InterfaceTypedErrorSideAcceptsAnImplementer(t *testing.T) {
	src := `struct Boom {
  why: String
}
impl Display for Boom {
  fn to_string(value: Boom): String {
    value.why
  }
}
fn r(): Result<Int, Boom> {
  Err(Boom{why: "boom"})
}
fn f(): Result<Int, Display> {
  v = try r()
  Ok(v)
}`
	expectNoConcurrentError(t, analyzeConcurrentRaw(src), "try error type mismatch")
}

// A generic boundary is not silently satisfied by a concrete error type: the
// function's own type parameter is PROTECTED, so unify does not bind it away.
func TestTryBoundary_GenericBoundaryIsNotSatisfiedByAConcreteError(t *testing.T) {
	src := `fn r(): Result<Int, String> {
  Err("boom")
}
fn f<E>(): Result<Int, E> {
  v = try r()
  Ok(v)
}`
	expectConcurrentError(t, analyzeConcurrentRaw(src), "try error type mismatch: expected E, got String")
}

// A generic boundary propagating its OWN error type is clean.
func TestTryBoundary_GenericBoundaryMatchingItsOwnErrorTypeIsClean(t *testing.T) {
	src := `fn f<E>(r: Result<Int, E>): Result<Int, E> {
  v = try r
  Ok(v)
}`
	expectNoConcurrentError(t, analyzeConcurrentRaw(src), "try error type mismatch")
}

// The pipe spelling is the same shape — the parser desugars `x |> try f()`
// into a bare `try` stage over the piped value — so it must be checked too,
// otherwise the rule has a hole the moment anyone writes a pipeline.
func TestTryBoundary_PipeStageIsChecked(t *testing.T) {
	src := `fn f(): Result<Int, String> {
  n =
    Some(1)
    |> try
  Ok(n)
}`
	expectConcurrentError(t, analyzeConcurrentRaw(src),
		"`try` on a Maybe cannot propagate out of a function returning Result<Int, String>")
}

// An INFERRING boundary keeps its own regime: resolveBoundaryErr binds or
// validates the block's error side from the accumulated `try` sites, and
// checkTryBoundary must not also report against the enclosing fn's
// declaration. Regression guard for the gate.
func TestTryBoundary_ConcurrentBlockKeepsItsOwnRegime(t *testing.T) {
	src := `fn short_fail(): Result<Int, Int> {
  Err(7)
}
fn demo(): String {
  outcome = concurrent {
    x = Task.spawn(|| short_fail())
    Ok(try Task.await(x))
  }
  case outcome {
    Ok(n) -> "ok"
    Err(e) -> Int.to_string(e)
  }
}`
	errs := analyzeConcurrent(src)
	expectNoConcurrentError(t, errs, "try error type mismatch")
	expectNoConcurrentError(t, errs, "cannot propagate")
}

// THE INFERRING HALF. A lambda declares no result, so there is nothing to
// launder — its error type IS the `try` site's. What it can still get wrong is
// clause 1: the lambda's result is not a Result or a Maybe at all, so the
// propagated `Err`/`None` is handed to a caller expecting the success type.
//
// Closing the fn half alone would leave the hole reachable through a lambda.
func TestTryBoundary_LambdaResultCannotHoldAPropagatedResult(t *testing.T) {
	src := `fn parse(n: Int): Result<Int, String> {
  Ok(n)
}
fn main() {
  _ = |x: Int| {
    v = try parse(x)
    v
  }
}`
	expectConcurrentError(t, analyzeConcurrentRaw(src),
		"`try` cannot propagate a Result out of a lambda whose result is Int")
}

// The `Maybe` flavour is the reason the accumulator carries a FLAVOUR and not
// only an error type: a `try` on a `Maybe` contributes no error type, so an
// error-type-only accumulator is empty here and cannot see the violation.
func TestTryBoundary_LambdaResultCannotHoldAPropagatedMaybe(t *testing.T) {
	src := `fn find(n: Int): Maybe<Int> {
  Some(n)
}
fn main() {
  _ = |x: Int| {
    v = try find(x)
    v
  }
}`
	expectConcurrentError(t, analyzeConcurrentRaw(src),
		"`try` cannot propagate a Maybe out of a lambda whose result is Int")
}

func TestTryBoundary_LambdaWithAResultTailIsClean(t *testing.T) {
	src := `fn parse(n: Int): Result<Int, String> {
  Ok(n)
}
fn main() {
  _ = |x: Int| {
    v = try parse(x)
    Ok(v)
  }
}`
	expectNoConcurrentError(t, analyzeConcurrentRaw(src), "cannot propagate")
}

// A lambda mixing flavours: the `Maybe` site cannot propagate out of a
// `Result`-tailed lambda.
func TestTryBoundary_LambdaFlavourDisagreement(t *testing.T) {
	src := `fn parse(n: Int): Result<Int, String> {
  Ok(n)
}
fn find(n: Int): Maybe<Int> {
  Some(n)
}
fn main() {
  _ = |x: Int| {
    a = try parse(x)
    b = try find(x)
    Ok(a + b)
  }
}`
	expectConcurrentError(t, analyzeConcurrentRaw(src),
		"`try` on a Maybe cannot propagate out of a lambda whose result is Result")
}

// A `concurrent` block whose tail is not a Result/Maybe is the same violation
// through the other inferring boundary, and the diagnostic says so.
func TestTryBoundary_ConcurrentBlockResultCannotHoldIt(t *testing.T) {
	src := `fn short_fail(): Result<Int, String> {
  Err("boom")
}
fn demo(): Int {
  outcome = concurrent {
    x = Task.spawn(|| short_fail())
    try Task.await(x)
  }
  outcome
}`
	expectConcurrentError(t, analyzeConcurrent(src),
		"`try` cannot propagate a Result out of a concurrent block whose result is Int")
}

// The divergent body: a lambda whose body only ever `break`s has result type
// Infallible, which is the bottom type and has no result to disagree with.
// This is the shape TypesEqual gets WRONG — it treats Infallible as compatible
// with everything, so a TypesEqual-based guard here silenced every boundary,
// including the conflicting-error-types one. Pointer identity is the test.
func TestTryBoundary_DivergentLambdaBodyIsExempt(t *testing.T) {
	src := `fn parse(n: Int): Result<Int, String> {
  Ok(n)
}
fn demo(): Int {
  Iter.loop(|acc = 0| {
    v = try parse(acc)
    break v
  })
}`
	errs := analyzeConcurrentRaw(src)
	expectNoConcurrentError(t, errs, "cannot propagate")
}

// An `assert` inside a `concurrent` block exits the block as
// Err(AssertionFailure), like a `try`, so a block whose value is not a
// Result has nowhere to send the failure and is an error at the assertion.
// The diagnostic names the assertion, not a `try` the user did not write.
func TestTryBoundary_AssertionInAConcurrentBlockNeedsAResult(t *testing.T) {
	src := `fn demo(): Int {
  v = concurrent {
    assert 1 == 1
    2
  }
  v
}`
	errs := analyzeConcurrentRaw(src)
	expectConcurrentError(t, errs, "an assertion inside a concurrent block exits the block with Err(AssertionFailure), but the block's value is Int")
	expectNoConcurrentError(t, errs, "cannot propagate")
	expectNoConcurrentError(t, errs, "try error type mismatch")
}

// A block producing Result<_, AssertionFailure> holds its assertions; one
// producing another error type is the try boundary's own mismatch.
func TestTryBoundary_AssertionInAResultConcurrentBlock(t *testing.T) {
	ok := `import std/assertions.{AssertionFailure}

fn demo(): Result<Int, AssertionFailure> {
  concurrent {
    assert 1 == 1
    Ok(2)
  }
}`
	errs := analyzeConcurrentRaw(ok)
	expectNoConcurrentError(t, errs, "assertion")
	expectNoConcurrentError(t, errs, "inconsistent")
	bad := `fn demo(): Result<Int, String> {
  concurrent {
    assert 1 == 1
    Ok(2)
  }
}`
	expectConcurrentError(t, analyzeConcurrentRaw(bad), "AssertionFailure")
}
