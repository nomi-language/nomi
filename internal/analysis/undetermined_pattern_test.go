package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
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
// or a case arm's name whose type only holds a free variable no value has,
// stays accepted.
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
		// A destructured name whose wholly unresolved type a later use fixes.
		"(a, b) = (None, 1)\n  dbg a == Some(2)\n  dbg b",
		// A case arm's name whose type holds a free variable, a value like `None`.
		"case Some(None) {\n    Some(a) -> {\n      dbg a\n      Unit\n    }\n    None -> Unit\n  }",
		// Nothing is bound.
		"case Maybe.to_result(None, \"x\") {\n    Ok(_) -> Unit\n    Err(e) -> {\n      dbg e\n      Unit\n    }\n  }",
		// A lambda parameter whose later lines fix what a destructure bound.
		"pairs = Iter.reduce([1, 2], ([], []), fn state, item -> {\n    (ts, fs) = state\n    ([item, ..ts], [Int.to_string(item), ..fs])\n  })\n  dbg pairs",
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
	src := "fn main() {\n  Some(z) = Iter.first(Iter.map([], fn x -> x)) else { return }\n  dbg z\n  Unit\n}\n"
	errs := checkWithStdlib(src)
	got := undeterminedErrors(errs)
	if len(got) != 1 || !strings.HasPrefix(got[0].Message, "the type of 'z' is not determined") {
		t.Fatalf("want one error at z, got:\n  %s", describeErrors(errs))
	}
}

// A destructuring binding is held to the rule `name = value` is: a name it
// binds to a wholly unresolved type is refused, as `box = read()` is, where
// a case arm's name of that type is accepted. `(box, _) = read()` binds box
// to a `Box<?>` when only read's result names its T; the builder could not
// lower the call that takes it.
func TestUndeterminedDestructure_Rejected(t *testing.T) {
	decls := `struct Box<T> {
  value: Maybe<T>
}

type Wrap<T> Box<T>

fn read<T>() -> (Box<T>, Bool) {
  (Box{value: None}, True)
}

fn pair<T>() -> .{box: Box<T>, n: Int} {
  .{box: Box{value: None}, n: 1}
}

fn wrapped<T>() -> Wrap<T> {
  Wrap(Box{value: None})
}

fn show<T>(handler: (Box<T>) -> String) -> String {
`
	cases := []struct {
		name, body string
		line, col  int
		binding    string
	}{
		{"a tuple destructure of a generic result", "  (box, _) = read()\n  handler(box)", 20, 4, "box"},
		{"a struct destructure of a generic result", "  .{box, n} = pair()\n  dbg n\n  handler(box)", 20, 5, "box"},
		{"a distinct destructure of a generic result", "  Wrap(box) = wrapped()\n  handler(box)", 20, 8, "box"},
		{"a tuple of None", "  (a, b) = (None, 1)\n  dbg a\n  dbg b\n  handler(Box{value: None})", 20, 4, "a"},
		{"a binding with else", "  Some(a) = Some(None) else { return \"\" }\n  dbg a\n  handler(Box{value: None})", 20, 8, "a"},
		{"an empty list", "  Ok(xs) = Ok([]) else { return \"\" }\n  dbg xs\n  handler(Box{value: None})", 20, 6, "xs"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := decls + tc.body + "\n}\n"
			errs := checkWithStdlib(src)
			var got []analysis.TypeError
			for _, e := range errs {
				if strings.Contains(e.Message, "determined") {
					got = append(got, e)
				}
			}
			if len(got) != 1 {
				t.Fatalf("the checker admits this destructure, or reports it more than once; want one error at %s:\n%s\n  %s",
					tc.binding, src, describeErrors(errs))
			}
			want := "binding '" + tc.binding + "' type is not locally determined: it has unsolved type parameter(s) — " +
				"give the call a type argument (`f<T>(…)`) or annotate the matched value (`v: T = …`)"
			if got[0].Message != want {
				t.Fatalf("message = %q\nwant      %q", got[0].Message, want)
			}
			if got[0].Line != tc.line || got[0].Col != tc.col {
				t.Fatalf("at %d:%d, want %d:%d", got[0].Line, got[0].Col, tc.line, tc.col)
			}
		})
	}
}

// Naming the type argument, as the error says, determines the binding.
func TestUndeterminedDestructure_TypeArgumentDetermines(t *testing.T) {
	src := `struct Box<T> {
  value: Maybe<T>
}

fn read<T>() -> (Box<T>, Bool) {
  (Box{value: None}, True)
}

fn show<T>(handler: (Box<T>) -> String) -> String {
  (box, _) = read<T>()
  handler(box)
}
`
	expectNoErrorsT(t, checkWithStdlib(src))
}
