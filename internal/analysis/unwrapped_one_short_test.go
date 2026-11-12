package analysis_test

import "testing"

// A name a pattern bound one level short, a `Maybe<T>` used where a `T` is
// expected, gets help naming the pattern's line and the `Some(...)` to
// write there. A name bound by `name = value`, or a Maybe whose payload is
// not what is expected, keeps the message alone.
func TestUnwrappedOneShort_NamesThePattern(t *testing.T) {
	const decls = "fn read() -> Result<Maybe<String>, String>\n  Ok(Some(\"a b\"))\nend\n\n"
	const msg = "argument 1: expected String, got Maybe<String>"
	for _, tc := range []struct {
		name, body, want string
	}{
		{"a binding with else", "  Ok(input) = read() else\n      return\n  end\n  _ = String.words(input)\n",
			msg + "\nhelp: `input` is a `Maybe<String>`, bound one level short by the pattern on line 6; write `Some(input)` in its place there to bind the `String`"},
		{"a case arm", "  case read()\n    Ok(input) ->\n      _ = String.words(input)\n    Err(_) -> Unit\n  end\n",
			msg + "\nhelp: `input` is a `Maybe<String>`, bound one level short by the pattern on line 7; write `Some(input)` in its place there to bind the `String`"},
		{"a plain binding", "  input = Some(\"a\")\n  _ = String.words(input)\n", msg},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(decls + "fn main()\n" + tc.body + "end\n")
			expectExactDiag(t, errs, tc.want)
		})
	}
	t.Run("a payload that is not what is expected", func(t *testing.T) {
		_, errs := checkSourceWithStdlib(decls + "fn main()\n  Ok(input) = read() else\n      return\n  end\n  _ = Int.abs(input)\nend\n")
		expectExactDiag(t, errs, "argument 1: expected Int, got Maybe<String>")
	})
}
