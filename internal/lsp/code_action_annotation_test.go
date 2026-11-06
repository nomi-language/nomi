package lsp

import (
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

const annotationURI = "file:///annotation/main.nomi"

const annotationSrc = `interface Reply {
  fn body(r: self) -> String
}

struct Page {
  text: String
}

impl Reply for Page {
  fn body(p: Page) -> String { p.text }
}

typealias Handler (Int) -> Reply

fn page(_n: Int) -> Page { Page{text: "p"} }

fn routes() -> List<#(String, Handler)> { [#("a", page)] }

fn main() {
  mine = [#("b", page)]
  _r = mine + routes()
}
`

// Two typed lists joined with `+` whose types both convert to one are
// refused, and the diagnostic shows the annotation that makes them one as
// its help line; its quick fix writes the annotation, and the program then
// checks.
func TestAnnotationHelpDiagnostic_ShowsTheAnnotationAndFixesIt(t *testing.T) {
	s := NewServer()
	s.docs.Open(annotationURI, annotationSrc)
	snap := s.docs.Snapshot(annotationURI)
	if snap == nil || snap.Analysis == nil {
		t.Fatal("no analysis")
	}
	diags := buildPublishedDiagnostics(annotationURI, snap.Content, snap.Nodes, snap.Errors, snap.Analysis.TypeErrors)
	want := "binary + type mismatch: List<#(String, (Int) -> Page)> vs List<#(String, (Int) -> Reply)>\n" +
		"help: annotate the binding: `mine: List<#(String, Handler)> = ...`"
	if len(diags) != 1 || diags[0].Message != want {
		var got []string
		for _, d := range diags {
			got = append(got, d.Message)
		}
		t.Fatalf("diagnostics:\n%s\nwant:\n%s", strings.Join(got, "\n---\n"), want)
	}
	res, err := s.textDocumentCodeAction(nil, &protocol.CodeActionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(annotationURI)},
		Context: protocol.CodeActionContext{
			Diagnostics: diags,
			Only:        []protocol.CodeActionKind{protocol.CodeActionKindQuickFix},
		},
	})
	if err != nil {
		t.Fatalf("code action: %v", err)
	}
	actions, _ := res.([]protocol.CodeAction)
	var fix *protocol.CodeAction
	for i, a := range actions {
		if a.Title == "Annotate `mine` as `List<#(String, Handler)>`" {
			fix = &actions[i]
		}
	}
	if fix == nil {
		t.Fatalf("no annotation fix among %d actions", len(actions))
	}
	got := applyWorkspaceEdit(t, annotationSrc, *fix, annotationURI)
	if !strings.Contains(got, "  mine: List<#(String, Handler)> = [#(\"b\", page)]\n") {
		t.Fatalf("the fix wrote:\n%s", got)
	}
	s.docs.Open(annotationURI, got)
	if errs := s.docs.Snapshot(annotationURI).Analysis.TypeErrors; len(errs) != 0 {
		t.Fatalf("the fixed program has an error: %v", errs[0])
	}
}
