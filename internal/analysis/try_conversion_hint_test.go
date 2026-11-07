package analysis_test

import "testing"

// A `?` whose error the boundary does not take writes the map_err stage that
// converts it, when one is plain: the boundary enum's one variant holding the
// error, or the error's Display text for a String boundary. Otherwise the
// message stands alone.
func TestTryMismatch_WritesTheConversion(t *testing.T) {
	const inner = "struct Bad {\n  code: Int\n}\n\nimpl Display for Bad {\n  fn to_string(b: Bad) -> String {\n    \"bad ${b.code}\"\n  }\n}\n\nstruct Quiet {\n  code: Int\n}\n\nfn bad() -> Result<Int, Bad> {\n  Err(Bad{code: 1})\n}\n\nfn quiet() -> Result<Int, Quiet> {\n  Err(Quiet{code: 1})\n}\n\n"
	for _, tc := range []struct {
		name, src, want string
	}{
		{"a String boundary over a Display error", "fn f() -> Result<Int, String> {\n  n = bad()?\n  Ok(n)\n}\n",
			"`?` error type mismatch: expected String, got Bad (the enclosing function returns Result<Int, String>); convert the error before the `?`, for example with Result.map_err\nhelp: write the error as a String that says what failed: `|> Result.map_err(&\"...: ${&1}\")?`"},
		{"an enum with one variant holding the error", "enum AppError {\n  Failed(Bad)\n  Other(String)\n}\n\nfn f() -> Result<Int, AppError> {\n  n = bad()?\n  Ok(n)\n}\n",
			"`?` error type mismatch: expected AppError, got Bad (the enclosing function returns Result<Int, AppError>); convert the error before the `?`, for example with Result.map_err\nhelp: wrap the error in `AppError.Failed`: `|> Result.map_err(AppError.Failed)?`"},
		{"an enum with two variants holding the error", "enum AppError {\n  First(Bad)\n  Second(Bad)\n}\n\nfn f() -> Result<Int, AppError> {\n  n = bad()?\n  Ok(n)\n}\n",
			"`?` error type mismatch: expected AppError, got Bad (the enclosing function returns Result<Int, AppError>); convert the error before the `?`, for example with Result.map_err"},
		{"a String boundary over an error with no Display", "fn f() -> Result<Int, String> {\n  n = quiet()?\n  Ok(n)\n}\n",
			"`?` error type mismatch: expected String, got Quiet (the enclosing function returns Result<Int, String>); convert the error before the `?`, for example with Result.map_err"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(inner + tc.src)
			expectExactDiag(t, errs, tc.want)
		})
	}
}
