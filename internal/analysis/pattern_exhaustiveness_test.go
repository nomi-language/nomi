package analysis_test

import "testing"

// Pattern exhaustiveness on enum scrutinees.
//
// `case` on an enum scrutinee must cover every variant. Wildcards / bare
// bindings cover everything; variant patterns with payload constraints
// (literals, nested variants, guards) do NOT — those need a `_` fallback.
//
// These tests live in the external test package so they can use the real
// stdlib via `checkSourceWithStdlib` — Maybe / Result / Bool aren't
// built-in language features, they're stdlib enums.

func TestCaseExhaustiveMaybeAllVariants(t *testing.T) {
	src := `

fn m(x: Maybe<Int>): Int {
  case x {
    Some(n) -> n
    None -> 0
  }
}`
	_, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
}

func TestCaseExhaustiveWildcard(t *testing.T) {
	src := `

fn m(x: Maybe<Int>): Int {
  case x {
    Some(n) -> n
    _ -> 0
  }
}`
	_, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
}

func TestCaseExhaustiveBareBinding(t *testing.T) {
	src := `

fn m(x: Maybe<Int>): Int {
  case x {
    Some(n) -> n
    other -> 0
  }
}`
	_, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
}

func TestCaseNonExhaustiveMaybeMissingNone(t *testing.T) {
	src := `

fn m(x: Maybe<Int>): Int {
  case x {
    Some(n) -> n
  }
}`
	_, errs := checkSourceWithStdlib(src)
	expectStdlibError(t, errs, "non-exhaustive")
}

func TestCaseNonExhaustiveMaybeMissingSome(t *testing.T) {
	src := `

fn m(x: Maybe<Int>): Int {
  case x {
    None -> 0
  }
}`
	_, errs := checkSourceWithStdlib(src)
	expectStdlibError(t, errs, "non-exhaustive")
}

func TestCaseNonExhaustiveResult(t *testing.T) {
	src := `

fn m(x: Result<Int, String>): Int {
  case x {
    Ok(n) -> n
  }
}`
	_, errs := checkSourceWithStdlib(src)
	expectStdlibError(t, errs, "non-exhaustive")
}

func TestCaseExhaustiveResult(t *testing.T) {
	src := `

fn m(x: Result<Int, String>): Int {
  case x {
    Ok(n) -> n
    Err(_) -> 0
  }
}`
	_, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
}

func TestCaseNonExhaustiveBoolMissingFalse(t *testing.T) {
	src := `fn m(x: Bool): Int {
  case x {
    True -> 1
  }
}`
	_, errs := checkSourceWithStdlib(src)
	expectStdlibError(t, errs, "non-exhaustive")
}

func TestCaseExhaustiveBool(t *testing.T) {
	src := `fn m(x: Bool): Int {
  case x {
    True -> 1
    False -> 0
  }
}`
	_, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
}

func TestCaseNonExhaustiveUserEnum(t *testing.T) {
	src := `enum Shape { Circle
  Square
  Triangle }
fn m(x: Shape): Int {
  case x {
    Circle -> 1
    Square -> 2
  }
}`
	_, errs := checkSourceWithStdlib(src)
	expectStdlibError(t, errs, "non-exhaustive")
}

func TestCaseLiteralPayloadNotExhaustive(t *testing.T) {
	// Some(42) constrains the payload — does NOT cover the `Some` variant.
	// Per spec line 880: "Any pattern with literal constraints or guards
	// always requires a `_` fallback."
	src := `

fn m(x: Maybe<Int>): Int {
  case x {
    Some(42) -> 1
    None -> 0
  }
}`
	_, errs := checkSourceWithStdlib(src)
	expectStdlibError(t, errs, "non-exhaustive")
}

func TestCaseGuardNotExhaustive(t *testing.T) {
	// Guard on the only `Some` branch — doesn't cover `Some` cleanly.
	src := `

fn m(x: Maybe<Int>): Int {
  case x {
    Some(n) when n > 0 -> 1
    None -> 0
  }
}`
	_, errs := checkSourceWithStdlib(src)
	expectStdlibError(t, errs, "non-exhaustive")
}

func TestCaseExhaustivenessSkipsAdHoc(t *testing.T) {
	// Ad-hoc case (no scrutinee) — exhaustiveness doesn't apply.
	src := `fn m(x: Int): Int {
  case {
    x > 0 -> 1
    _ -> 0
  }
}`
	_, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
}

func TestCaseExhaustivenessSkipsNonEnum(t *testing.T) {
	// Scrutinee is an Int (not an enum) — exhaustiveness for primitives
	// is not in scope; the `case` body is allowed to be incomplete.
	src := `fn m(x: Int): Int {
  case x {
    0 -> 1
    _ -> 0
  }
}`
	_, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
}

func TestCaseExhaustiveNestedVariantInsideVariant(t *testing.T) {
	// Some(Ok(_)) | Some(Err(_)) | None covers Maybe<Result<T, E>> fully:
	// Some's sub-patterns themselves exhaust Result, so the outer Some is
	// covered without an explicit Some(_) fallback.
	src := `


fn m(x: Maybe<Result<Int, String>>): Int {
  case x {
    Some(Ok(n)) -> n
    Some(Err(_)) -> -1
    None -> 0
  }
}`
	_, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
}
