package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// A backtick typed literal whose handler returns `Result<T, E>` is checked
// at compile time, so its type is T; a double-quoted one keeps the handler's
// Result, interpolated or not.
func TestTypedLiteral_BacktickLiteralHasTheHandlersOkType(t *testing.T) {
	src := "import std/regex.Regex\n\n" +
		"fn f(w: String): List<Regex> {\n" +
		"  a: Regex = Regex`\\d+`\n" +
		"  b: Result<Regex, String> = Regex\"\\\\d+\"\n" +
		"  c: Result<Regex, String> = Regex\"${w}\\\\d\"\n" +
		"  case (b, c) {\n" +
		"    (Ok(x), Ok(y)) -> [a, x, y]\n" +
		"    _ -> [a]\n" +
		"  }\n" +
		"}\n"
	if errs := checkTypedLiteralSource(src); len(errs) != 0 {
		t.Fatalf("the literals' types are rejected: %v", errs)
	}
	for _, mistyped := range []string{
		"  a: Result<Regex, String> = Regex`\\d+`\n",
		"  a: Regex = Regex\"\\\\d+\"\n",
	} {
		src := "import std/regex.Regex\n\nfn f(): Unit {\n" + mistyped + "  _ = a\n}\n"
		if errs := checkTypedLiteralSource(src); len(errs) == 0 {
			t.Errorf("%q is admitted", strings.TrimSpace(mistyped))
		}
	}
}

// `try` on a backtick literal checked at compile time is the ordinary
// "try requires a Result or Maybe" error, at the `try`, with a hint that the
// literal is already the handler's Ok type. Both spellings of `try`.
func TestTypedLiteral_TryOnBacktickLiteralIsRejectedWithHint(t *testing.T) {
	for _, line := range []string{
		"  digits = try Regex`\\d+`\n",
		"  digits = Regex`\\d+` |> try\n",
	} {
		src := "import std/regex.Regex\n\nfn f(): Result<Regex, String> {\n" + line + "  Ok(digits)\n}\n"
		errs := checkTypedLiteralSource(src)
		var found *analysis.TypeError
		for i := range errs {
			if errs[i].Message == "try requires a Result or Maybe, got Regex" {
				found = &errs[i]
			}
		}
		if found == nil {
			t.Fatalf("%q: no try error, got %v", strings.TrimSpace(line), errs)
		}
		want := "a backtick typed literal is checked at compile time, so this one is already a Regex: remove the `try`"
		if len(found.Hints) != 1 || found.Hints[0] != want {
			t.Errorf("%q: hints = %q, want %q", strings.TrimSpace(line), found.Hints, want)
		}
		if found.Line != 4 || found.Col < 12 {
			t.Errorf("%q: the error is at %d:%d, want line 4 at the `try`", strings.TrimSpace(line), found.Line, found.Col)
		}
	}
	// A double-quoted literal is still a Result, so `try` on it stands.
	src := "import std/regex.Regex\n\nfn f(): Result<Regex, String> {\n  digits = try Regex\"\\\\d+\"\n  Ok(digits)\n}\n"
	if errs := checkTypedLiteralSource(src); len(errs) != 0 {
		t.Fatalf("try on a double-quoted literal is rejected: %v", errs)
	}
}
