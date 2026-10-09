package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// The enclosing function's return type is an expected type only for the
// function's result: its tail, a `return`, and the arms of a tail `case` or
// `if`. A generic constructor anywhere else (`Err("bad")` in an arm of a
// bound `case`) gets the type its own arguments and position give it, not
// the function's.

func bindingType(t *testing.T, fa *analysis.FileAnalysis, name string) string {
	t.Helper()
	for _, sym := range fa.Definitions {
		if sym.Name == name && sym.Kind == analysis.SymbolBinding {
			if sym.Type == nil {
				t.Fatalf("binding %s has no type", name)
			}
			return sym.Type.String()
		}
	}
	t.Fatalf("no binding %s", name)
	return ""
}

func expectBindingType(t *testing.T, src, name, want string) {
	t.Helper()
	fa, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
	if got := bindingType(t, fa, name); got != want {
		t.Errorf("type of %s: got %s, want %s", name, got, want)
	}
}

const returnScopeF = `fn f(): Result<Int, String> { Ok(1) }
fn mi(): Maybe<Int> { Some(1) }
`

func TestReturnTypeScope_ErrInBoundCaseArm(t *testing.T) {
	expectBindingType(t, returnScopeF+`
fn g(s: String): Result<Unit, String> {
  r = case s {
    "a" -> f()
    _ -> Err("bad")
  }
  Ok(Unit)
}`, "r", "Result<Int, String>")
}

func TestReturnTypeScope_ErrInBoundCaseArmBlock(t *testing.T) {
	expectBindingType(t, returnScopeF+`
fn h(s: String): Result<Unit, String> {
  r = case s {
    "a" -> f()
    _ -> { Err("bad") }
  }
  Ok(Unit)
}`, "r", "Result<Int, String>")
}

func TestReturnTypeScope_ErrInBoundIfElse(t *testing.T) {
	expectBindingType(t, returnScopeF+`
fn k(s: String): Result<Unit, String> {
  r = if s == "a" { f() } else { Err("bad") }
  Ok(Unit)
}`, "r", "Result<Int, String>")
}

func TestReturnTypeScope_ErrInNonResultFunction(t *testing.T) {
	expectBindingType(t, returnScopeF+`
fn m(s: String): Int {
  r = case s {
    "a" -> f()
    _ -> Err("bad")
  }
  1
}`, "r", "Result<Int, String>")
}

// `Ok(2)` fixes T but not E; E comes from the other arm, not from the
// function's `Result<Int, Int>`.
func TestReturnTypeScope_OkInBoundCaseArm(t *testing.T) {
	expectBindingType(t, returnScopeF+`
fn g(s: String): Result<Int, Int> {
  r = case s {
    "a" -> f()
    _ -> Ok(2)
  }
  Ok(1)
}`, "r", "Result<Int, String>")
}

func TestReturnTypeScope_OkInBoundIfElse(t *testing.T) {
	expectBindingType(t, returnScopeF+`
fn g(s: String): Result<Int, Int> {
  r = if s == "a" { f() } else { Ok(2) }
  Ok(1)
}`, "r", "Result<Int, String>")
}

func TestReturnTypeScope_SomeAndNoneInBoundArms(t *testing.T) {
	expectBindingType(t, returnScopeF+`
fn g(s: String): Maybe<String> {
  r = case s {
    "a" -> mi()
    _ -> Some(2)
  }
  Some("x")
}`, "r", "Maybe<Int>")
	expectBindingType(t, returnScopeF+`
fn g(s: String): Maybe<String> {
  r = if s == "a" { mi() } else { None }
  Some("x")
}`, "r", "Maybe<Int>")
}

// A bound `Err` whose T nothing fixes stays open; a later annotated use
// fixes it, where the function's `Result<Unit, String>` used to make it
// `Result<Unit, String>` and reject the annotation.
func TestReturnTypeScope_BoundErrStaysOpen(t *testing.T) {
	src := `fn g(): Result<Unit, String> {
  e = Err("bad")
  r: Result<Int, String> = e
  Ok(Unit)
}`
	fa, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
	if got := bindingType(t, fa, "r"); got != "Result<Int, String>" {
		t.Errorf("type of r: got %s, want Result<Int, String>", got)
	}
	if got := bindingType(t, fa, "e"); strings.Contains(got, "Unit") {
		t.Errorf("type of e: got %s, which took the function's return type", got)
	}
}

// Inside a lambda the lambda's own result applies, never the outer
// function's.
func TestReturnTypeScope_Lambda(t *testing.T) {
	expectBindingType(t, returnScopeF+`
fn g(): Result<Unit, Int> {
  h = |s: String| case s {
    "a" -> f()
    _ -> Err("bad")
  }
  r = h("a")
  Ok(Unit)
}`, "r", "Result<Int, String>")
}

// Positions whose value is the function's result still take its type.
func TestReturnTypeScope_ResultPositionsStillPropagate(t *testing.T) {
	src := returnScopeF + `
enum Color {
  Red
  Blue
}
fn ret(s: String): Result<Int, String> {
  if s == "" {
    return Err("empty")
  }
  case s {
    "a" -> Ok(1)
    _ -> Err("no")
  }
}
fn tail_if(s: String): Result<Int, String> {
  if s == "a" { Ok(1) } else { Err("no") }
}
fn tail_err(): Result<Int, String> {
  e = Err("bad")
  e
}
fn tries(s: String): Result<Int, String> {
  x = try f()
  y = try ret(s)
  Ok(x + y)
}
fn fallback(m: Maybe<Int>): Result<Int, String> {
  Some(n) = m else { return Err("none") }
  Ok(n)
}
fn wrapped(): Maybe<Result<Int, String>> {
  Some(Err("boom"))
}
fn tail_color(s: String): Color {
  case s {
    "a" -> .Red
    _ -> .Blue
  }
}`
	_, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
}

// A `.Variant` in an arm of an unannotated binding has no expected enum,
// as before: the arm's result has no expected type.
func TestReturnTypeScope_DotVariantInBoundArmStillNeedsAType(t *testing.T) {
	src := `enum Color {
  Red
  Blue
}
fn g(s: String): Maybe<Color> {
  c = case s {
    "a" -> Color.Red
    _ -> .Blue
  }
  Some(c)
}`
	_, errs := checkSourceWithStdlib(src)
	expectErrorContaining(t, errs, "requires a determinable enum type")
}
