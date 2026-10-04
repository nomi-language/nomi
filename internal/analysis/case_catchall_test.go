package analysis_test

import "testing"

const catchAllMsg = "non-exhaustive case: add a `_` arm"

func catchAllErrors(src string) int {
	_, errs := checkSourceWithStdlib(src)
	n := 0
	for _, e := range errs {
		if e.Message == catchAllMsg {
			n++
		}
	}
	return n
}

// A subject-less case, and a case over Int or String literal patterns, cannot
// be proven exhaustive, so each needs a `_` arm; a guarded arm does not count.
func TestCaseCatchAll_RequiredWhereCasesCannotBeEnumerated(t *testing.T) {
	for name, src := range map[string]string{
		"subject-less":    "fn f(x: Int): String {\n  case {\n    x > 0 -> \"pos\"\n    x == 0 -> \"zero\"\n  }\n}\n",
		"Int literals":    "fn f(n: Int): String {\n  case n {\n    1 -> \"one\"\n    2 -> \"two\"\n  }\n}\n",
		"String literals": "fn f(s: String): Int {\n  case s {\n    \"a\" -> 1\n    \"b\" + rest -> String.length(rest)\n  }\n}\n",
		"guarded binding": "fn f(n: Int): Int {\n  case n {\n    m when m > 0 -> m\n  }\n}\n",
		"piped Int":       "fn f(n: Int): Int {\n  n |> case {\n    1 -> 1\n  }\n}\n",
	} {
		if got := catchAllErrors(src); got != 1 {
			t.Errorf("%s: got %d %q errors, want 1", name, got, catchAllMsg)
		}
	}
}

func TestCaseCatchAll_WildcardOrBindingIsEnough(t *testing.T) {
	for name, src := range map[string]string{
		"subject-less _": "fn f(x: Int): String {\n  case {\n    x > 0 -> \"pos\"\n    _ -> \"other\"\n  }\n}\n",
		"Int _":          "fn f(n: Int): String {\n  case n {\n    1 -> \"one\"\n    _ -> \"many\"\n  }\n}\n",
		"String binding": "fn f(s: String): String {\n  case s {\n    \"a\" -> \"A\"\n    other -> other\n  }\n}\n",
		"piped Int _":    "fn f(n: Int): Int {\n  n |> case {\n    1 -> 1\n    _ -> 0\n  }\n}\n",
	} {
		if got := catchAllErrors(src); got != 0 {
			t.Errorf("%s: got %d %q errors, want 0", name, got, catchAllMsg)
		}
	}
}
