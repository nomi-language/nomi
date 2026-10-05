package lsp

import (
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// pipeStageActions opens src and returns the "Call `f()`" fixes offered over
// every diagnostic the client holds.
func pipeStageActions(t *testing.T, s *Server, uri, src string) []protocol.CodeAction {
	t.Helper()
	s.docs.Open(uri, src)
	snap := s.docs.Snapshot(uri)
	if snap == nil || snap.Analysis == nil {
		t.Fatal("no analysis")
	}
	diags := buildPublishedDiagnostics(uri, snap.Content, snap.Nodes, snap.Errors, snap.Analysis.TypeErrors)
	for i := range diags {
		diags[i].Code = nil
	}
	res, err := s.textDocumentCodeAction(nil, &protocol.CodeActionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
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
		if strings.HasPrefix(a.Title, "Call `") {
			out = append(out, a)
		}
	}
	return out
}

const pipeStageURI = "file:///pipestage/main.nomi"

// Each bare stage gets its own fix, and applying them all leaves a program
// that checks.
func TestBarePipeStageFix_AppendsParentheses(t *testing.T) {
	src := "import std/io\n\nfn double(n: Int): Int {\n    n * 2\n}\n\nfn main() {\n    3 |> double |> io.print\n}\n"
	s := NewServer()
	actions := pipeStageActions(t, s, pipeStageURI, src)
	var titles []string
	for _, a := range actions {
		titles = append(titles, a.Title)
		if a.IsPreferred == nil || !*a.IsPreferred {
			t.Errorf("%s: not preferred", a.Title)
		}
		if len(a.Diagnostics) != 1 {
			t.Errorf("%s: names %d diagnostics, want 1", a.Title, len(a.Diagnostics))
		}
	}
	if got, want := strings.Join(titles, " | "), "Call `double()` | Call `io.print()`"; got != want {
		t.Fatalf("titles = %q, want %q", got, want)
	}
	edited := applyWorkspaceEdit(t, src, actions[0], pipeStageURI)
	edited = applyWorkspaceEdit(t, edited, pipeStageActions(t, s, pipeStageURI, edited)[0], pipeStageURI)
	want := "import std/io\n\nfn double(n: Int): Int {\n    n * 2\n}\n\nfn main() {\n    3 |> double() |> io.print()\n}\n"
	if edited != want {
		t.Fatalf("got\n%s\nwant\n%s", edited, want)
	}
	s.docs.Open(pipeStageURI, edited)
	if errs := s.docs.Snapshot(pipeStageURI).Analysis.TypeErrors; len(errs) > 0 {
		t.Fatalf("the fixed program has errors: %v", errs)
	}
}

// A `.Variant` stage needs its enum named, which appending `()` does not
// do, so it gets no fix.
func TestBarePipeStageFix_NotOfferedForADotVariant(t *testing.T) {
	src := "enum Dir {\n    North\n}\n\nfn main() {\n    d: Dir = 1 |> .North\n    _ = d\n}\n"
	s := NewServer()
	s.docs.Open(pipeStageURI, src)
	found := false
	for _, e := range s.docs.Snapshot(pipeStageURI).Analysis.TypeErrors {
		found = found || strings.HasPrefix(e.Message, "a pipe stage is a call")
	}
	if !found {
		t.Fatal("the checker no longer rejects a `.Variant` pipe stage; this test is moot")
	}
	if actions := pipeStageActions(t, s, pipeStageURI, src); len(actions) != 0 {
		t.Fatalf("offered %d fixes for a `.Variant` stage", len(actions))
	}
}

// thenActions returns the "Write `then`" fixes offered over every diagnostic.
func thenActions(t *testing.T, s *Server, uri, src string) []protocol.CodeAction {
	t.Helper()
	s.docs.Open(uri, src)
	snap := s.docs.Snapshot(uri)
	if snap == nil || snap.Analysis == nil {
		t.Fatal("no analysis")
	}
	diags := buildPublishedDiagnostics(uri, snap.Content, snap.Nodes, snap.Errors, snap.Analysis.TypeErrors)
	res, err := s.textDocumentCodeAction(nil, &protocol.CodeActionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
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
		if a.Title == "Write `then` before the lambda" {
			out = append(out, a)
		}
	}
	return out
}

// A lambda stage gets the fix that inserts `then `, and the fixed program
// checks.
func TestLambdaPipeStageFix_InsertsThen(t *testing.T) {
	src := "fn main(): String {\n    3\n    |> |n| n + 1\n    |> Int.to_string()\n}\n"
	s := NewServer()
	actions := thenActions(t, s, pipeStageURI, src)
	if len(actions) != 1 {
		t.Fatalf("got %d fixes, want 1", len(actions))
	}
	edited := applyWorkspaceEdit(t, src, actions[0], pipeStageURI)
	want := "fn main(): String {\n    3\n    |> then |n| n + 1\n    |> Int.to_string()\n}\n"
	if edited != want {
		t.Fatalf("got\n%s\nwant\n%s", edited, want)
	}
	s.docs.Open(pipeStageURI, edited)
	if errs := s.docs.Snapshot(pipeStageURI).Analysis.TypeErrors; len(errs) > 0 {
		t.Fatalf("the fixed program has errors: %v", errs)
	}
}

// A lambda in parentheses would need them removed too, so it gets no fix.
func TestLambdaPipeStageFix_NotOfferedInParentheses(t *testing.T) {
	src := "fn main(): Int {\n    3 |> (|n| n + 1)\n}\n"
	s := NewServer()
	s.docs.Open(pipeStageURI, src)
	found := false
	for _, e := range s.docs.Snapshot(pipeStageURI).Analysis.TypeErrors {
		found = found || strings.HasPrefix(e.Message, "a lambda is not a pipe stage")
	}
	if !found {
		t.Fatal("the checker no longer rejects a parenthesized lambda stage; this test is moot")
	}
	if actions := thenActions(t, s, pipeStageURI, src); len(actions) != 0 {
		t.Fatalf("offered %d fixes for a parenthesized lambda", len(actions))
	}
}
