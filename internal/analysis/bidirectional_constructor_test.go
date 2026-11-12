package analysis

import "testing"

// Bidirectional inference into generic variant constructors.
//
// The expected (return) type determines a generic constructor's payload type,
// so a dot-leading variant argument should resolve against it — e.g. `.Quit`
// in `Result.Ok(.Quit)` inside `fn (): Result<Input, String>` is `Input.Quit`.

func TestDotVariant_GenericConstructorArg_ReturnPosition_OK(t *testing.T) {
	src := `enum Result<T, E>
    Ok T
    Err E
end
enum Input
    Quit
    Guess Int
end
fn read() -> Result<Input, String>
  Result.Ok(.Quit)
end`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestDotVariant_GenericConstructorArgPayload_ReturnPosition_OK(t *testing.T) {
	src := `enum Result<T, E>
    Ok T
    Err E
end
enum Input
    Quit
    Guess Int
end
fn read(n: Int) -> Result<Input, String>
  Result.Ok(.Guess(n))
end`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// `value |> Result.Ok()` should infer the error type from the return type, not
// leave a free E. Mirrors the `expr? |> Ok()` symptom from the same gap, in
// the pipe path.
func TestPipeOkInfersErrorTypeFromReturn_OK(t *testing.T) {
	src := `enum Result<T, E>
    Ok T
    Err E
end
fn wrap(n: Int) -> Result<Int, String>
  n |> Result.Ok()
end`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// `return` statement: the value is checked against the declared return type.
func TestDotVariant_GenericConstructorArg_ReturnStatement_OK(t *testing.T) {
	src := `enum Result<T, E>
    Ok T
    Err E
end
enum Input
    Quit
    Guess Int
end
fn read(early: Bool) -> Result<Input, String>
  if early then return Result.Ok(.Quit)
  Result.Err("nope")
end`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// Annotated binding: the annotation supplies the expected type at the RHS.
func TestDotVariant_GenericConstructorArg_AnnotatedBinding_OK(t *testing.T) {
	src := `enum Result<T, E>
    Ok T
    Err E
end
enum Input
    Quit
    Guess Int
end
fn main()
  r: Result<Input, String> = Result.Ok(.Quit)
end`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// Argument position: the callee's parameter type supplies the expected type.
func TestDotVariant_GenericConstructorArg_ArgumentPosition_OK(t *testing.T) {
	src := `enum Result<T, E>
    Ok T
    Err E
end
enum Input
    Quit
    Guess Int
end
fn take(_r: Result<Input, String>) -> Int
    0
end
fn demo() -> Int
  take(Result.Ok(.Quit))
end`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// Case-arm body (direct expression and block) — the expected type flows from
// the case's expected type into each arm.
func TestDotVariant_GenericConstructorArg_CaseArm_OK(t *testing.T) {
	src := `enum Result<T, E>
    Ok T
    Err E
end
enum Input
    Quit
    Guess Int
end
fn read(s: String) -> Result<Input, String>
  case s
    "q" -> Result.Ok(.Quit)
    other ->
      n = 0
      Result.Ok(.Guess(n))
  end
end`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// If/else branch tails.
func TestDotVariant_GenericConstructorArg_IfArm_OK(t *testing.T) {
	src := `enum Result<T, E>
    Ok T
    Err E
end
enum Input
    Quit
    Guess Int
end
fn read(early: Bool) -> Result<Input, String>
  if early then Result.Ok(.Quit) else Result.Ok(.Guess(5))
end`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// A genuine mismatch must still error: .Quit is not a variant of Other, even
// though the seed makes the argument's expected type concrete.
func TestDotVariant_GenericConstructorArg_WrongEnum_Errors(t *testing.T) {
	src := `enum Result<T, E>
    Ok T
    Err E
end
enum Input
    Quit
    Guess Int
end
enum Other
    A
    B
end
fn read() -> Result<Other, String>
  Result.Ok(.Quit)
end`
	_, errs := checkSource(src)
	expectError(t, errs, "Quit")
}

// An `if` or `case` argument is its branches' value, so the constructor's
// payload type, pinned from the expected result, reaches a dot variant or an
// anonymous struct literal in each branch.
func TestDotVariant_GenericConstructorArgThroughIfAndCase_OK(t *testing.T) {
	src := `enum Result<T, E>
    Ok T
    Err E
end
enum Color
    Red
    Blue
end
struct Score
    color: Color
  n: Int
end
fn pick(n: Int) -> Result<Color, String>
  Result.Ok(if n == 1 then .Red else .Blue)
end
fn arm(n: Int) -> Result<Color, String>
  Result.Ok(case n
      1 -> .Red
      _ -> .Blue
  end)
end
fn score(n: Int) -> Result<Score, String>
  Result.Ok(case n
      1 -> {color: .Red, n:}
      _ -> {color: .Blue, n:}
  end)
end
fn nested(n: Int) -> Result<Color, String>
  Result.Ok(if n == 1 then .Red else if n == 2 then .Blue else .Red)
end`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// The mirror: with no expected type around the call, nothing determines the
// branches' enum, and the error stays.
func TestDotVariant_GenericConstructorArgThroughIf_NoExpectedType_Rejected(t *testing.T) {
	src := `enum Result<T, E>
    Ok T
    Err E
end
enum Color
    Red
    Blue
end
fn main()
  n = 1
  r = Result.Ok(if n == 1 then .Red else .Blue)
  _ = r
end`
	_, errs := checkSource(src)
	for _, e := range errs {
		if e.Message == ".Red cannot be resolved here: nothing at this position determines its enum" {
			return
		}
	}
	t.Fatalf("expected the unresolved .Red error, got %v", errs)
}

// A list literal with a spread checks its heads against the expected
// element type, as a plain list literal does: `[.Red, ..rest]` against
// `List<Color>`, and inside a generic function against its own
// `List<Crumb<T>>`, where the function's `T` is a determined type.
func TestDotVariant_ListSpreadHeadTakesTheElementType_OK(t *testing.T) {
	src := `enum Color
    Red
    Blue
end
enum Crumb<T>
    Left T
    Right T
end
fn colors(rest: List<Color>) -> List<Color>
  a: List<Color> = [.Red, ..rest]
  [.Blue, ..a]
end
fn push<T>(x: T, rest: List<Crumb<T>>) -> List<Crumb<T>>
  one: List<Crumb<T>> = [.Left(x)]
  [.Right(x), ..one]
end`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// The mirror: a head or a tail of another element type is still refused.
func TestDotVariant_ListSpreadAgainstAnExpectedList_Rejected(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"a head", "  a: List<Color> = [1, ..rest]\n  _ = a", "list element type mismatch: expected Color, got Int"},
		{"a tail", "  a: List<Color> = [.Red, ..[1]]\n  _ = a", "list element type mismatch: expected Color, got Int"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "enum Color\n  Red; Blue\nend\nfn f(rest: List<Color>)\n  _ = rest\n" + tc.body + "\nend"
			_, errs := checkSource(src)
			for _, e := range errs {
				if e.Message == tc.want {
					return
				}
			}
			t.Fatalf("expected %q, got %v", tc.want, errs)
		})
	}
}
