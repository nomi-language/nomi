package lsp

import (
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

const pipeToStringDecls = "type Fahrenheit Decimal\n\nimpl Display for Fahrenheit {\n    fn to_string(Fahrenheit(n)) -> String {\n        \"${n} F\"\n    }\n}\n\n" +
	"fn label(width: Int, text: String) -> String {\n    String.pad_start(text, width)\n}\n\n"

// pipeToStringActions opens src and returns its published diagnostics and
// the "Add `|> Display.to_string()`" fixes offered over them.
func pipeToStringActions(t *testing.T, s *Server, uri, src string) ([]protocol.Diagnostic, []protocol.CodeAction) {
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
		if strings.HasPrefix(a.Title, "Add `|> Display.to_string()`") {
			out = append(out, a)
		}
	}
	return diags, out
}

const pipeToStringURI = "file:///pipetostring/main.nomi"

// The diagnostic for a Display value piped into a String stage spans the
// stage's callee and relates the value's source, and its fix inserts the
// `Display.to_string()` stage, which leaves a program that checks.
func TestPipeToStringFix_InsertsTheStage(t *testing.T) {
	src := pipeToStringDecls + "fn main() {\n    f = Fahrenheit(1d)\n    _ = f |> String.pad_start(6)\n}\n"
	s := NewServer()
	diags, actions := pipeToStringActions(t, s, pipeToStringURI, src)
	var d *protocol.Diagnostic
	for i := range diags {
		if strings.HasPrefix(diags[i].Message, "`String.pad_start` takes a String") {
			d = &diags[i]
		}
	}
	if d == nil {
		t.Fatalf("no pipe mismatch among %+v", diags)
	}
	line := protocol.UInteger(strings.Count(src[:strings.Index(src, "_ = f |>")], "\n"))
	wantRange := protocol.Range{
		Start: protocol.Position{Line: line, Character: 13},
		End:   protocol.Position{Line: line, Character: 29},
	}
	if d.Range != wantRange {
		t.Fatalf("range = %+v, want %+v (`String.pad_start`)", d.Range, wantRange)
	}
	if len(d.RelatedInformation) != 1 {
		t.Fatalf("related = %+v, want the piped value", d.RelatedInformation)
	}
	r := d.RelatedInformation[0]
	if r.Location.URI != pipeToStringURI || r.Location.Range.Start != (protocol.Position{Line: line, Character: 8}) || r.Location.Range.End != (protocol.Position{Line: line, Character: 9}) {
		t.Fatalf("related location = %+v, want `f` on line %d", r.Location, line)
	}
	if len(actions) != 1 {
		t.Fatalf("got %d fixes, want 1", len(actions))
	}
	edited := applyWorkspaceEdit(t, src, actions[0], pipeToStringURI)
	want := pipeToStringDecls + "fn main() {\n    f = Fahrenheit(1d)\n    _ = f |> Display.to_string() |> String.pad_start(6)\n}\n"
	if edited != want {
		t.Fatalf("got\n%s\nwant\n%s", edited, want)
	}
	s.docs.Open(pipeToStringURI, edited)
	if errs := s.docs.Snapshot(pipeToStringURI).Analysis.TypeErrors; len(errs) > 0 {
		t.Fatalf("the fixed program has errors: %v", errs)
	}
}

// A `&1` slot is not preceded by `|>`, so the diagnostic there keeps its help
// and gets no fix.
func TestPipeToStringFix_NotOfferedForAPlaceholder(t *testing.T) {
	src := pipeToStringDecls + "fn main() {\n    f = Fahrenheit(1d)\n    _ = f |> then &label(4, &1)\n}\n"
	s := NewServer()
	diags, actions := pipeToStringActions(t, s, pipeToStringURI, src)
	found := false
	for _, d := range diags {
		if strings.HasPrefix(d.Message, "`label` takes a String as its second argument") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no pipe mismatch among %+v", diags)
	}
	if len(actions) != 0 {
		t.Fatalf("got fixes %+v, want none", actions)
	}
}
