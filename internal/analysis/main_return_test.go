package analysis_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

const mainReturnMessage = "`main` must return `Unit` or `Result<Unit, E>`"
const mainReturnHint = "to show a value, print it with `io.print` and return `Ok(Unit)`"

// mainReturnErrors is the main-return-type diagnostics among errs.
func mainReturnErrors(errs []analysis.TypeError) []analysis.TypeError {
	var out []analysis.TypeError
	for _, e := range errs {
		if e.Message == mainReturnMessage {
			out = append(out, e)
		}
	}
	return out
}

// A main declared to return a value is rejected at its return type: a run
// prints an `Err` and nothing else, so `Ok("hello")` would vanish.
func TestCheckMainReturn_RejectsAValue(t *testing.T) {
	for _, tc := range []struct{ name, ret, body string }{
		{"Ok payload", "Result<String, String>", `Ok("hello")`},
		{"plain value", "String", `"hello"`},
		{"Maybe", "Maybe<Int>", "None"},
		{"Int", "Int", "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib("fn main(): " + tc.ret + " {\n    " + tc.body + "\n}\n")
			got := mainReturnErrors(errs)
			if len(got) != 1 {
				t.Fatalf("got %d main return errors, want 1, among %v", len(got), errs)
			}
			e := got[0]
			if e.Col != 12 || e.EndLine != e.Line || e.EndCol != 12+len(tc.ret) {
				t.Fatalf("the error spans %d:%d-%d:%d, want the annotation `%s` at column 12",
					e.Line, e.Col, e.EndLine, e.EndCol, tc.ret)
			}
			if len(e.Hints) != 1 || e.Hints[0] != mainReturnHint {
				t.Fatalf("hints = %q, want [%q]", e.Hints, mainReturnHint)
			}
		})
	}
}

// Unit, written or omitted, and Result<Unit, E> for any E are what a run
// knows how to finish with.
func TestCheckMainReturn_AcceptsUnitAndResultOfUnit(t *testing.T) {
	for _, src := range []string{
		"fn main() {\n    Unit\n}\n",
		"fn main(): Unit {\n    Unit\n}\n",
		"fn main(): Result<Unit, String> {\n    Ok(Unit)\n}\n",
		"struct Missing {\n    path: String\n}\n\nfn main(): Result<Unit, Missing> {\n    Err(Missing{path: \"app.toml\"})\n}\n",
	} {
		_, errs := checkSourceWithStdlib(src)
		if got := mainReturnErrors(errs); len(got) != 0 {
			t.Fatalf("rejected a main the rule allows:\n%s\n%v", src, got)
		}
		expectNoStdlibErrors(t, errs)
	}
}

// Only a top-level `fn main` is the entry: a function named main inside an
// impl block returns what it likes.
func TestCheckMainReturn_IgnoresAnImplFunctionNamedMain(t *testing.T) {
	_, errs := checkSourceWithStdlib("struct App {\n    name: String\n}\n\nimpl App {\n    fn main(app: App): String {\n        app.name\n    }\n}\n")
	if got := mainReturnErrors(errs); len(got) != 0 {
		t.Fatalf("rejected an impl function named main: %v", got)
	}
}
