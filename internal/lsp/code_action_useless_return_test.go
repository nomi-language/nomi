package lsp

import (
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

const uselessReturnURI = "file:///uselessreturn/main.nomi"

// uselessReturnActions opens src and returns the "Remove the `return`" fixes
// offered over every diagnostic the client holds.
func uselessReturnActions(t *testing.T, s *Server, src string) []protocol.CodeAction {
	t.Helper()
	s.docs.Open(uselessReturnURI, src)
	snap := s.docs.Snapshot(uselessReturnURI)
	if snap == nil || snap.Analysis == nil {
		t.Fatal("no analysis")
	}
	diags := buildPublishedDiagnostics(uselessReturnURI, snap.Content, snap.Nodes, snap.Errors, snap.Analysis.TypeErrors)
	for i := range diags {
		diags[i].Code = nil
	}
	res, err := s.textDocumentCodeAction(nil, &protocol.CodeActionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uselessReturnURI)},
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
		if a.Title == "Remove the `return`" {
			out = append(out, a)
		}
	}
	return out
}

// Each shape of useless return gets one fix, and the fixed program checks.
func TestUselessReturnFix_RemovesTheReturn(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{
			"own line",
			"fn reset() {\n    return\n}\n",
			"fn reset() {\n}\n",
		},
		{
			"comment kept",
			"import std/io\n\nfn greet() {\n    io.print(\"hi\")\n    return // done\n}\n",
			"import std/io\n\nfn greet() {\n    io.print(\"hi\")\n    // done\n}\n",
		},
		{
			"case arm",
			"import std/io\n\nfn f(x: Int) {\n    case x {\n        0 -> return\n        _ -> io.print(\"x\")\n    }\n}\n",
			"import std/io\n\nfn f(x: Int) {\n    case x {\n        0 -> {}\n        _ -> io.print(\"x\")\n    }\n}\n",
		},
		{
			"one-line branch",
			"import std/io\n\nfn f(x: Int) {\n    if x > 0 { return } else { io.print(\"x\") }\n}\n",
			"import std/io\n\nfn f(x: Int) {\n    if x > 0 { } else { io.print(\"x\") }\n}\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewServer()
			actions := uselessReturnActions(t, s, tc.src)
			if len(actions) != 1 {
				t.Fatalf("got %d fixes, want 1", len(actions))
			}
			if a := actions[0]; a.IsPreferred == nil || !*a.IsPreferred || len(a.Diagnostics) != 1 {
				t.Errorf("fix is not preferred or names %d diagnostics", len(a.Diagnostics))
			}
			got := applyWorkspaceEdit(t, tc.src, actions[0], uselessReturnURI)
			if got != tc.want {
				t.Fatalf("got\n%s\nwant\n%s", got, tc.want)
			}
			s.docs.Open(uselessReturnURI, got)
			if errs := s.docs.Snapshot(uselessReturnURI).Analysis.TypeErrors; len(errs) > 0 {
				t.Fatalf("the fixed program has errors: %v", errs)
			}
		})
	}
}

// The `if` that does nothing has no fix: what to keep of its condition is
// the author's call.
func TestUselessReturnFix_NotOfferedForAnIf(t *testing.T) {
	src := "import std/io\n\nfn f(done: Bool) {\n    io.print(\"x\")\n    if done { return }\n}\n"
	s := NewServer()
	if actions := uselessReturnActions(t, s, src); len(actions) != 0 {
		var titles []string
		for _, a := range actions {
			titles = append(titles, a.Title)
		}
		t.Fatalf("got fixes %s, want none", strings.Join(titles, ", "))
	}
}
