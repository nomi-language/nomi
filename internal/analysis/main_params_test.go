package analysis_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

const mainParamsMessage = "`main` takes no parameters"
const mainParamsHint = "a program reads its arguments in `fn boot(startup: Startup)` as `startup.args`, and keeps what `main` needs in the application struct"

func mainParamsErrors(errs []analysis.TypeError) []analysis.TypeError {
	var out []analysis.TypeError
	for _, e := range errs {
		if e.Message == mainParamsMessage {
			out = append(out, e)
		}
	}
	return out
}

// Nothing calls main with arguments, so a main that declares parameters is
// rejected over its parameter list, with the hint naming where arguments
// arrive.
func TestCheckMainParams_RejectsParameters(t *testing.T) {
	for _, tc := range []struct{ name, params string }{
		{"one", "args: List<String>"},
		{"two with a default", "name: String, count: Int = 3"},
		{"discard", "_: Int"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib("fn main(" + tc.params + ") {\n    Unit\n}\n")
			got := mainParamsErrors(errs)
			if len(got) != 1 {
				t.Fatalf("got %d main parameter errors, want 1, among %v", len(got), errs)
			}
			e := got[0]
			// `fn main(` is 8 columns, so the list starts at column 9.
			if e.Col != 9 || e.EndLine != e.Line || e.EndCol != 9+len(tc.params) {
				t.Fatalf("the error spans %d:%d-%d:%d, want `%s` from column 9",
					e.Line, e.Col, e.EndLine, e.EndCol, tc.params)
			}
			if len(e.Hints) != 1 || e.Hints[0] != mainParamsHint {
				t.Fatalf("hints = %q, want [%q]", e.Hints, mainParamsHint)
			}
		})
	}
}

// `fn main()` is accepted, and so are the other parameterized entry
// callbacks: a boot taking Startup, an impl function named main, and a test.
func TestCheckMainParams_AcceptsTheEntryForms(t *testing.T) {
	for name, src := range map[string]string{
		"no parameters": "fn main() {\n    Unit\n}\n",
		"boot with startup": "struct App {\n    name: String\n}\n\nfn boot(startup: Startup): App {\n    App{name: List.head(startup.args) |> Maybe.with_default(\"x\")}\n}\n\n" +
			"fn main() {\n    _ = App.name\n}\n",
		"impl function named main": "struct Box {\n    n: Int\n}\n\nimpl Box {\n    fn main(b: Box): Int {\n        b.n\n    }\n}\n\nfn main() {\n    _ = Box.main(Box{n: 1})\n}\n",
		"test":                     "fn main() {\n    Unit\n}\n\ntest \"runs\" {\n    assert 1 == 1\n}\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(src)
			if got := mainParamsErrors(errs); len(got) != 0 {
				t.Fatalf("rejected an accepted form:\n%s\n%v", src, got)
			}
			if name == "boot with startup" {
				// A single file outside a project build has no entry boot
				// registered, so its `App.name` read is reported; the boot
				// program runs whole in vmhost.TestSpecPrograms (spec §26).
				return
			}
			expectNoStdlibErrors(t, errs)
		})
	}
}
