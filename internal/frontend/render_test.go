package frontend

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// renderOf renders the diagnostics src's errors become in main.nomi.
func renderOf(t *testing.T, src string, errs []analysis.TypeError) string {
	t.Helper()
	var b bytes.Buffer
	typeDiagnostics("main.nomi", src, errs).Render(&b)
	return b.String()
}

const renderSrc = "fn main() {\n  count = 1\n  total: Int = name\n\tx = \"héllo\" + cuont\n  y = [\n    1,\n  ]\n}\n"

func TestRender_SourceLineSpanHintAndNote(t *testing.T) {
	t.Setenv("NOMI_COLOR", "never")
	t.Setenv(DiagnosticsFormatEnv, "")
	got := renderOf(t, renderSrc, []analysis.TypeError{{
		Line: 3, Col: 16, EndLine: 3, EndCol: 20,
		Message: "type mismatch: expected Int, got String",
		Hints:   []string{"did you mean 'count'?"},
		Related: []analysis.RelatedInfo{{Line: 2, Col: 3, EndLine: 2, EndCol: 8, Message: "'count' is declared here"}},
	}})
	want := "error: type mismatch: expected Int, got String\n" +
		" --> main.nomi:3:16\n" +
		"  |\n" +
		"3 |   total: Int = name\n" +
		"  |                ^^^^\n" +
		"  = help: did you mean 'count'?\n" +
		"  = note: 'count' is declared here: main.nomi:2:3\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// A tab before the span stays a tab under it, and a multi-byte character
// takes one caret.
func TestRender_TabsAndWideCharacters(t *testing.T) {
	t.Setenv("NOMI_COLOR", "never")
	t.Setenv(DiagnosticsFormatEnv, "")
	got := renderOf(t, renderSrc, []analysis.TypeError{{Line: 4, Col: 6, EndLine: 4, EndCol: 14, Message: "m"}})
	if want := "4 | \tx = \"héllo\" + cuont\n  | \t    ^^^^^^^\n"; !strings.Contains(got, want) {
		t.Errorf("got:\n%s\nwant it to contain:\n%s", got, want)
	}
}

// A span that runs onto later lines is underlined to the end of its first
// line and names the line it ends on. An error with only a point gets the
// token there.
func TestRender_MultilineSpanAndPoint(t *testing.T) {
	t.Setenv("NOMI_COLOR", "never")
	t.Setenv(DiagnosticsFormatEnv, "")
	got := renderOf(t, renderSrc, []analysis.TypeError{
		{Line: 5, Col: 7, EndLine: 7, EndCol: 4, Message: "multi"},
		{Line: 4, Col: 17, Message: "point"},
	})
	for _, want := range []string{
		"5 |   y = [\n  |       ^ (through line 7)\n",
		"4 | \tx = \"héllo\" + cuont\n  | \t              ^^^^^\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("got:\n%s\nwant it to contain:\n%s", got, want)
		}
	}
}

func TestRender_Colour(t *testing.T) {
	t.Setenv("NOMI_COLOR", "always")
	t.Setenv(DiagnosticsFormatEnv, "")
	got := renderOf(t, renderSrc, []analysis.TypeError{{Line: 3, Col: 16, EndLine: 3, EndCol: 20, Message: "bad", Hints: []string{"h"}}})
	for _, want := range []string{
		"\x1b[1m\x1b[31merror:\x1b[0m \x1b[1mbad\x1b[0m\n",
		"\x1b[36m-->\x1b[0m main.nomi:3:16\n",
		"\x1b[1m\x1b[31m^^^^\x1b[0m\n",
		"\x1b[1mhelp:\x1b[0m h\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("coloured output lacks %q:\n%q", want, got)
		}
	}
	t.Setenv("NOMI_COLOR", "")
	t.Setenv("NO_COLOR", "1")
	if got := renderOf(t, renderSrc, []analysis.TypeError{{Line: 3, Col: 16, Message: "bad"}}); strings.Contains(got, "\x1b[") {
		t.Errorf("NO_COLOR output has escapes:\n%q", got)
	}
}

// The short form is one `path:line:col:` line for the message and one for
// each hint and related location.
func TestRender_ShortForm(t *testing.T) {
	t.Setenv("NOMI_COLOR", "always")
	t.Setenv(DiagnosticsFormatEnv, "short")
	got := renderOf(t, renderSrc, []analysis.TypeError{{
		Line: 4, Col: 17, Message: "undefined variable 'cuont'",
		Hints:   []string{"did you mean 'count'?"},
		Related: []analysis.RelatedInfo{{File: "/abs/other.nomi", Line: 2, Col: 3, Message: "declared here"}},
	}})
	want := "main.nomi:4:17: undefined variable 'cuont'\n" +
		"main.nomi:4:17: help: did you mean 'count'?\n" +
		"/abs/other.nomi:2:3: note: declared here\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// The data carries the span CompleteSpans gives a point, and the line's text.
func TestTypeDiagnostics_CarryTheSpanAndSource(t *testing.T) {
	ds := typeDiagnostics("main.nomi", renderSrc, []analysis.TypeError{{Line: 4, Col: 17, Message: "m"}})
	d := ds[0]
	if d.EndLine != 4 || d.EndCol != 22 || d.SourceLine != "\tx = \"héllo\" + cuont" {
		t.Errorf("got %+v", d)
	}
}
