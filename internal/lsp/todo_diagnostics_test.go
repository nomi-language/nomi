package lsp

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// Every `todo` is published as a warning on its keyword, as every `dbg` is,
// with its reason in the message. A `todo` is not a type error: the document
// checks, and only these warnings remain.
func TestBuildPublishedDiagnosticsReportsTodoWarnings(t *testing.T) {
	content := strings.Join([]string{
		"fn parse(text: String): Int {",
		"    todo \"parse the header\"",
		"}",
		"",
		"fn main() {",
		"    _ = parse(\"x\") + todo",
		"}",
	}, "\n")
	nodes, parseErrs := parser.ParseWithRecovery(lexer.Lex(content))
	if len(parseErrs) != 0 {
		t.Fatalf("unexpected parse errors: %+v", parseErrs)
	}
	diags := buildPublishedDiagnostics("file:///repo/app/main.nomi", content, nodes, nil, nil)
	if len(diags) != 2 {
		t.Fatalf("expected two todo warnings, got %d: %+v", len(diags), diags)
	}
	want := []struct {
		line, start uint32
		message     string
	}{
		{1, 4, `todo "parse the header"`},
		{5, 21, "`todo`: code not written yet; `nomi build` refuses it"},
	}
	for i, d := range diags {
		if d.Severity == nil || *d.Severity != protocol.DiagnosticSeverityWarning {
			t.Errorf("%d: severity %+v, want warning", i, d.Severity)
		}
		if d.Source == nil || *d.Source != "nomi-todo" {
			t.Errorf("%d: source %+v, want nomi-todo", i, d.Source)
		}
		if d.Code == nil || d.Code.Value != "todo-expression" {
			t.Errorf("%d: code %+v, want todo-expression", i, d.Code)
		}
		if d.Message != want[i].message {
			t.Errorf("%d: message %q, want %q", i, d.Message, want[i].message)
		}
		if d.Range.Start.Line != want[i].line || d.Range.Start.Character != want[i].start ||
			d.Range.End.Line != want[i].line || d.Range.End.Character != want[i].start+4 {
			t.Errorf("%d: range %+v, want line %d from %d over the keyword", i, d.Range, want[i].line, want[i].start)
		}
	}
}

// Through the server: a document whose only incomplete code is `todo` has no
// error, only the warnings.
func TestTodoDocumentHasNoErrors(t *testing.T) {
	s := NewServer()
	uri := "file:///todo/main.nomi"
	s.docs.Open(uri, "struct Point {\n    x: Int\n    y: Int\n}\n\nfn make(n: Int): Point {\n    Point{x: 1, y: todo}\n}\n")
	snap := s.docs.Snapshot(uri)
	if len(snap.Errors) > 0 || len(snap.Analysis.TypeErrors) > 0 {
		t.Fatalf("errors: %v %v", snap.Errors, snap.Analysis.TypeErrors)
	}
	diags := buildPublishedDiagnostics(uri, snap.Content, snap.Nodes, snap.Errors, snap.Analysis.TypeErrors)
	if len(diags) != 1 || diags[0].Source == nil || *diags[0].Source != "nomi-todo" {
		t.Fatalf("diagnostics %+v, want the one todo warning", diags)
	}
}
