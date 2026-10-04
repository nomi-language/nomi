package analysis

import "testing"

// Bidirectional inference into generic variant constructors.
//
// The expected (return) type determines a generic constructor's payload type,
// so a dot-leading variant argument should resolve against it — e.g. `.Quit`
// in `Result.Ok(.Quit)` inside `fn (): Result<Input, String>` is `Input.Quit`.

func TestDotVariant_GenericConstructorArg_ReturnPosition_OK(t *testing.T) {
	src := `enum Result<T, E> { Ok T; Err E }
enum Input { Quit; Guess Int }
fn read(): Result<Input, String> {
  Result.Ok(.Quit)
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestDotVariant_GenericConstructorArgPayload_ReturnPosition_OK(t *testing.T) {
	src := `enum Result<T, E> { Ok T; Err E }
enum Input { Quit; Guess Int }
fn read(n: Int): Result<Input, String> {
  Result.Ok(.Guess(n))
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// `value |> Result.Ok()` should infer the error type from the return type, not
// leave a free E. Mirrors the `try expr |> Ok()` symptom from the same gap, in
// the pipe path.
func TestPipeOkInfersErrorTypeFromReturn_OK(t *testing.T) {
	src := `enum Result<T, E> { Ok T; Err E }
fn wrap(n: Int): Result<Int, String> {
  n |> Result.Ok()
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// `return` statement: the value is checked against the declared return type.
func TestDotVariant_GenericConstructorArg_ReturnStatement_OK(t *testing.T) {
	src := `enum Result<T, E> { Ok T; Err E }
enum Input { Quit; Guess Int }
fn read(early: Bool): Result<Input, String> {
  if early { return Result.Ok(.Quit) }
  Result.Err("nope")
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// Annotated binding: the annotation supplies the expected type at the RHS.
func TestDotVariant_GenericConstructorArg_AnnotatedBinding_OK(t *testing.T) {
	src := `enum Result<T, E> { Ok T; Err E }
enum Input { Quit; Guess Int }
fn main() {
  r: Result<Input, String> = Result.Ok(.Quit)
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// Argument position: the callee's parameter type supplies the expected type.
func TestDotVariant_GenericConstructorArg_ArgumentPosition_OK(t *testing.T) {
	src := `enum Result<T, E> { Ok T; Err E }
enum Input { Quit; Guess Int }
fn take(_r: Result<Input, String>): Int { 0 }
fn main(): Int {
  take(Result.Ok(.Quit))
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// Case-arm body (direct expression and block) — the expected type flows from
// the case's expected type into each arm.
func TestDotVariant_GenericConstructorArg_CaseArm_OK(t *testing.T) {
	src := `enum Result<T, E> { Ok T; Err E }
enum Input { Quit; Guess Int }
fn read(s: String): Result<Input, String> {
  case s {
    "q" -> Result.Ok(.Quit)
    other -> {
      n = 0
      Result.Ok(.Guess(n))
    }
  }
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// If/else branch tails.
func TestDotVariant_GenericConstructorArg_IfArm_OK(t *testing.T) {
	src := `enum Result<T, E> { Ok T; Err E }
enum Input { Quit; Guess Int }
fn read(early: Bool): Result<Input, String> {
  if early { Result.Ok(.Quit) } else { Result.Ok(.Guess(5)) }
}`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

// A genuine mismatch must still error: .Quit is not a variant of Other, even
// though the seed makes the argument's expected type concrete.
func TestDotVariant_GenericConstructorArg_WrongEnum_Errors(t *testing.T) {
	src := `enum Result<T, E> { Ok T; Err E }
enum Input { Quit; Guess Int }
enum Other { A; B }
fn read(): Result<Other, String> {
  Result.Ok(.Quit)
}`
	_, errs := checkSource(src)
	expectError(t, errs, "Quit")
}
