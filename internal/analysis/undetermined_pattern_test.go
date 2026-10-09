package analysis_test

import (
	"strings"
	"testing"
)

// A name a pattern binds to a type nothing in the file fixes is an error at
// the name, in every form that binds a pattern. `Maybe.to_result(None, "x")`
// is `Result<T, String>` with T free, so `Ok(z)` binds z to no type at all.
func TestUndeterminedPatternBinding_Rejected(t *testing.T) {
	cases := []struct {
		name, body string
		line, col  int
		binding    string
	}{
		{"a binding with else", "Ok(z) = Maybe.to_result(None, \"x\") else { return }\n  dbg z", 2, 6, "z"},
		{"a binding with else arms", "Ok(z) = Maybe.to_result(None, \"x\") else {\n    Err(_) -> return\n  }\n  dbg z", 2, 6, "z"},
		{"a variant of a bare None", "Some(z) = None else { return }\n  dbg z", 2, 8, "z"},
		{"an if pattern", "if Ok(z) = Maybe.to_result(None, \"x\") {\n    dbg z\n  }", 2, 9, "z"},
		{"a case arm", "case Maybe.to_result(None, \"x\") {\n    Ok(z) -> {\n      dbg z\n      Unit\n    }\n    Err(_) -> Unit\n  }", 3, 8, "z"},
		{"a case arm over a piped value", "Maybe.to_result(None, \"x\") |> case {\n    Ok(z) -> {\n      dbg z\n      Unit\n    }\n    Err(_) -> Unit\n  }", 3, 8, "z"},
		{"the variant a value is not", "x = Ok(42)\n  case x {\n    Ok(_) -> Unit\n    Err(e) -> {\n      dbg e\n      Unit\n    }\n  }", 5, 9, "e"},
		{"inside a tuple pattern", "case (Maybe.to_result(None, \"x\"), 1) {\n    (Ok(z), _) -> {\n      dbg z\n      Unit\n    }\n    _ -> Unit\n  }", 3, 9, "z"},
		{"interpolated", "Ok(z) = Maybe.to_result(None, \"x\") else { return }\n  dbg \"${z}\"", 2, 6, "z"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "fn main() {\n  " + tc.body + "\n  Unit\n}\n"
			errs := checkWithStdlib(src)
			got := undeterminedErrors(errs)
			if len(got) != 1 {
				t.Fatalf("want one undetermined error, got:\n  %s", describeErrors(errs))
			}
			want := "the type of '" + tc.binding + "' is not determined: nothing fixes the type the pattern binds it to — " +
				"annotate the matched value (`v: T = …`) or give a call a type argument (`f<T>(…)`)"
			if got[0].Message != want {
				t.Fatalf("message = %q\nwant      %q", got[0].Message, want)
			}
			if got[0].Line != tc.line || got[0].Col != tc.col {
				t.Fatalf("at %d:%d, want %d:%d", got[0].Line, got[0].Col, tc.line, tc.col)
			}
		})
	}
}

// A pattern name whose type something on the binding or a later use fixes,
// or whose type only holds a free variable no value has, stays accepted.
func TestUndeterminedPatternBinding_Accepted(t *testing.T) {
	bodies := []string{
		// The fallback is part of the binding and fixes the payload.
		"Some(z) = None else { 5 }\n  dbg z",
		// A later use fixes it.
		"Ok(z) = Maybe.to_result(None, \"x\") else { return }\n  dbg z + 1",
		"case Maybe.to_result(None, \"x\") {\n    Ok(z) -> {\n      dbg Int.to_string(z)\n      Unit\n    }\n    Err(_) -> Unit\n  }",
		// The matched value is typed.
		"none: Maybe<Int> = None\n  Ok(z) = Maybe.to_result(none, \"x\") else { return }\n  dbg z",
		"Ok(z) = Maybe.to_result<Int, String>(None, \"x\") else { return }\n  dbg z",
		// The name's type holds a free variable, and is a value like `None`.
		"(a, b) = (None, 1)\n  dbg a\n  dbg b",
		"Some(a) = Some(None) else { return }\n  dbg a",
		"Ok(xs) = Ok([]) else { return }\n  dbg xs",
		// Nothing is bound.
		"case Maybe.to_result(None, \"x\") {\n    Ok(_) -> Unit\n    Err(e) -> {\n      dbg e\n      Unit\n    }\n  }",
		// A lambda parameter whose later lines fix what a destructure bound.
		"pairs = Iter.reduce([1, 2], |state = ([], []), item| {\n    (ts, fs) = state\n    ([item, ..ts], fs)\n  })\n  dbg pairs",
	}
	for _, body := range bodies {
		t.Run(body, func(t *testing.T) {
			src := "fn main() {\n  " + body + "\n  Unit\n}\n"
			expectNoErrorsT(t, checkWithStdlib(src))
		})
	}
}

// A matched value whose own call is undetermined is reported once, at the
// pattern's name.
func TestUndeterminedPatternBinding_CallIsNotRepeated(t *testing.T) {
	src := "fn main() {\n  Some(z) = Iter.first(Iter.map([], |x| x)) else { return }\n  dbg z\n  Unit\n}\n"
	errs := checkWithStdlib(src)
	got := undeterminedErrors(errs)
	if len(got) != 1 || !strings.HasPrefix(got[0].Message, "the type of 'z' is not determined") {
		t.Fatalf("want one error at z, got:\n  %s", describeErrors(errs))
	}
}
