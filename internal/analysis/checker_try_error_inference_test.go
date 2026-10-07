package analysis_test

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"strings"
	"testing"
)

// expectClean fails if any diagnostic was produced.
func expectClean(t *testing.T, errs []analysis.TypeError) {
	t.Helper()
	if len(errs) == 0 {
		return
	}
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Error()
	}
	t.Fatalf("expected no diagnostics, got %d:\n  %s", len(errs), strings.Join(msgs, "\n  "))
}

// A `concurrent` block whose tail is `Ok(try Task.await(x))` infers the error
// side of its Result from the `try` site (here `String`), so a downstream
// String use of the Err binding type-checks without an annotation.
func TestTryErrInference_ConcurrentBlockInfersResultErr(t *testing.T) {
	src := `fn short_fail(): Result<Int, String> {
  Err("boom")
}
fn demo(): String {
  outcome = concurrent {
    x = Task.spawn(|| short_fail())
    Ok(try Task.await(x))
  }
  case outcome {
    Ok(n) -> "ok"
    Err(e) -> e + "!"
  }
}`
	expectClean(t, analyzeConcurrent(src))
}

// An unannotated lambda whose body is `Ok(try parse())` infers
// `() -> Result<Int, String>` from the `try` site.
func TestTryErrInference_UnannotatedLambdaInfersResultErr(t *testing.T) {
	src := `fn parse(): Result<Int, String> {
  Ok(1)
}
fn demo(): String {
  f = || Ok(try parse())
  case f() {
    Ok(n) -> "ok"
    Err(e) -> e + "!"
  }
}`
	expectClean(t, analyzeConcurrent(src))
}

// Two `try` sites in one boundary with incompatible error types
// (`String` and `Int`) are rejected — the boundary's error type would
// be ambiguous.
func TestTryErrInference_ConflictingTryErrTypes_Rejected(t *testing.T) {
	src := `fn a(): Result<Int, String> {
  Ok(1)
}
fn b(): Result<Int, Int> {
  Ok(2)
}
fn demo(): Int {
  outcome = concurrent {
    x = Task.spawn(|| a())
    y = Task.spawn(|| b())
    r1 = try Task.await(x)
    r2 = try Task.await(y)
    Ok(r1 + r2)
  }
  case outcome {
    Ok(n) -> n
    Err(_) -> 0
  }
}`
	expectConcurrentError(t, analyzeConcurrent(src), "inconsistent error types in try expressions")
}

// A `Maybe`-flavored boundary is unaffected: `try` on a `Maybe` carries no
// error type, so nothing is accumulated and the block infers `Maybe<T>`.
func TestTryErrInference_MaybeBoundaryUnaffected(t *testing.T) {
	src := `fn find(): Maybe<Int> {
  Some(1)
}
fn demo(): Int {
  outcome = concurrent {
    x = Task.spawn(|| find())
    Some(try Task.await(x))
  }
  case outcome {
    Some(n) -> n
    None -> 0
  }
}`
	expectClean(t, analyzeConcurrent(src))
}

// A normal function with `try` is unchanged — its declared return type
// pins the error side; the accumulator must not disturb it.
func TestTryErrInference_FunctionUnchanged(t *testing.T) {
	src := `fn h(): Result<Int, String> {
  Ok(1)
}
fn g(): Result<Int, String> {
  x = try h()
  Ok(x)
}
fn demo(): Int {
  case g() {
    Ok(n) -> n
    Err(_) -> 0
  }
}`
	expectClean(t, analyzeConcurrent(src))
}
