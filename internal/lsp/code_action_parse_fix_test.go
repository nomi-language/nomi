package lsp

import (
	"testing"

	"github.com/nomi-language/nomi/internal/format"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

const parseFixURI = "file:///parsefix/main.nomi"

// parseFixActions opens src and returns the fixes titled title offered over
// every diagnostic the client holds.
func parseFixActions(t *testing.T, s *Server, src, title string) []protocol.CodeAction {
	t.Helper()
	s.docs.Open(parseFixURI, src)
	snap := s.docs.Snapshot(parseFixURI)
	if snap == nil || snap.Analysis == nil {
		t.Fatal("no analysis")
	}
	diags := buildPublishedDiagnostics(parseFixURI, snap.Content, snap.Nodes, snap.Analysis, snap.Errors, snap.Analysis.TypeErrors)
	res, err := s.textDocumentCodeAction(nil, &protocol.CodeActionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(parseFixURI)},
		Context: protocol.CodeActionContext{
			Diagnostics: diags,
			Only:        []protocol.CodeActionKind{protocol.CodeActionKindQuickFix},
		},
	})
	if err != nil {
		t.Fatalf("code action: %v", err)
	}
	actions, _ := res.([]protocol.CodeAction)
	var out []protocol.CodeAction
	for _, a := range actions {
		if a.Title == title {
			out = append(out, a)
		}
	}
	return out
}

// applyAllEdits applies every edit of action, last first.
func applyAllEdits(content string, action protocol.CodeAction) string {
	edits := action.Edit.Changes[protocol.DocumentUri(parseFixURI)]
	for i := len(edits) - 1; i >= 0; i-- {
		content = applyTextEdit(content, edits[i])
	}
	return content
}

// Each fix gives the program the user meant: it checks with no
// diagnostics, and `nomi fmt` leaves it as it is.
func TestParseFix_RepairsTheProgram(t *testing.T) {
	for _, tc := range []struct{ name, title, src, want string }{
		{
			"doc comment run", "Use `//` comments",
			`pub fn f(text: String) -> Int
    /// if text == "" { 0 }
    /// else
    String.length(text)
end
`,
			`pub fn f(text: String) -> Int
    // if text == "" { 0 }
    // else
    String.length(text)
end
`,
		},
		{
			"prompt before a statement", "Use `//` comments",
			`pub fn f(x: Int) -> Int
    //! assert f(1) == 1
    x
end
`,
			`pub fn f(x: Int) -> Int
    // assert f(1) == 1
    x
end
`,
		},
		{
			"another language's binding", "Remove `let`",
			"pub fn f() -> Int\n    let x = 1\n    x\nend\n",
			"pub fn f() -> Int\n    x = 1\n    x\nend\n",
		},
		{
			"a method call", "Call `Iter.count`",
			"pub fn f(xs: List<Int>) -> Int\n    xs.len()\nend\n",
			"pub fn f(xs: List<Int>) -> Int\n    Iter.count(xs)\nend\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewServer()
			actions := parseFixActions(t, s, tc.src, tc.title)
			if len(actions) != 1 {
				t.Fatalf("got %d fixes, want 1", len(actions))
			}
			a := actions[0]
			if a.Kind == nil || *a.Kind != protocol.CodeActionKindQuickFix || a.IsPreferred == nil || !*a.IsPreferred || len(a.Diagnostics) != 1 {
				t.Errorf("fix is not a preferred quick fix naming its diagnostic")
			}
			got := applyAllEdits(tc.src, a)
			if got != tc.want {
				t.Fatalf("got\n%s\nwant\n%s", got, tc.want)
			}
			s.docs.Open(parseFixURI, got)
			snap := s.docs.Snapshot(parseFixURI)
			if diags := buildPublishedDiagnostics(parseFixURI, snap.Content, snap.Nodes, snap.Analysis, snap.Errors, snap.Analysis.TypeErrors); len(diags) != 0 {
				t.Errorf("fixed program has diagnostics: %v", diags)
			}
			formatted, err := format.Format(got)
			if err != nil {
				t.Fatalf("format: %v", err)
			}
			if formatted != got {
				t.Errorf("format changes the fixed program:\n%s", formatted)
			}
		})
	}
}

// A parse error with no fix gets none.
func TestParseFix_NoneForOtherErrors(t *testing.T) {
	s := NewServer()
	src := "pub fn f() -> Int\n    1 +\nend\n"
	for _, title := range []string{"Use `//` comments", "Remove `let`"} {
		if got := parseFixActions(t, s, src, title); len(got) != 0 {
			t.Errorf("offered %q for an unrelated error", title)
		}
	}
}
