package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// A typed literal without slots whose handler returns `Result<T, E>` is
// checked at compile time, so its type is T in every quote style; a literal
// with a slot keeps the handler's Result.
func TestTypedLiteral_LiteralWithoutSlotsHasTheHandlersOkType(t *testing.T) {
	src := "import std::regex.Regex\n\n" +
		"fn f(w: String) -> List<Regex<Unit>>\n" +
		"  a: Regex<Unit> = Regex`\\d+`\n" +
		"  b: Regex<Unit> = Regex\"\\\\d+\"\n" +
		"  c: Regex<Unit> = Regex\"\"\"\n    \\\\d+\n    \"\"\"\n" +
		"  d: Result<Regex<List<Maybe<String>>>, String> = Regex\"${w}\\\\d\"\n" +
		"  case d\n" +
		"    Ok(_) -> [a, b, c]\n" +
		"    _ -> [a]\n" +
		"  end\n" +
		"end\n"
	if errs := checkTypedLiteralSource(src); len(errs) != 0 {
		t.Fatalf("the literals' types are rejected: %v", errs)
	}
	for _, mistyped := range []string{
		"  a: Result<Regex<Unit>, String> = Regex`\\d+`\n",
		"  a: Result<Regex<Unit>, String> = Regex\"\\\\d+\"\n",
		"  w = \"x\"\n  a: Regex<List<Maybe<String>>> = Regex\"${w}\"\n",
	} {
		src := "import std::regex.Regex\n\nfn f() -> Unit\n" + mistyped + "  _ = a\nend\n"
		if errs := checkTypedLiteralSource(src); len(errs) == 0 {
			t.Errorf("%q is admitted", strings.TrimSpace(mistyped))
		}
	}
}

// A `?` on a literal without slots, in any quote style, is the ordinary
// "`?` requires a Result or Maybe" error, at the `?`, with a hint that the
// literal is already the handler's Ok type. A `?` on a literal with a slot
// stands.
func TestTypedLiteral_TryOnLiteralWithoutSlotsIsRejectedWithHint(t *testing.T) {
	for _, line := range []string{
		"  digits = Regex`\\d+`?\n",
		"  digits = Regex\"\\\\d+\"?\n",
	} {
		src := "import std::regex.Regex\n\nfn f() -> Result<Regex<Unit>, String>\n" + line + "  Ok(digits)\nend\n"
		errs := checkTypedLiteralSource(src)
		var found *analysis.TypeError
		for i := range errs {
			if errs[i].Message == "`?` requires a Result or Maybe, got Regex<Unit>" {
				found = &errs[i]
			}
		}
		if found == nil {
			t.Fatalf("%q: no error, got %v", strings.TrimSpace(line), errs)
		}
		want := "a typed literal without slots is checked at compile time, so this one is already a Regex<Unit>: remove the `?`"
		if len(found.Hints) != 1 || found.Hints[0] != want {
			t.Errorf("%q: hints = %q, want %q", strings.TrimSpace(line), found.Hints, want)
		}
		if found.Line != 4 || found.Col < 12 {
			t.Errorf("%q: the error is at %d:%d, want line 4 at the `try`", strings.TrimSpace(line), found.Line, found.Col)
		}
	}
	src := "import std::regex.Regex\n\nfn f(w: String) -> Result<Regex<List<Maybe<String>>>, String>\n  digits = Regex\"${w}\\\\d+\"?\n  Ok(digits)\nend\n"
	if errs := checkTypedLiteralSource(src); len(errs) != 0 {
		t.Fatalf("try on a literal with a slot is rejected: %v", errs)
	}
}
