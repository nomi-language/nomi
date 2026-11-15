package lsp

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// `List.first(names)!` with `names` a NonEmptyList is a hint over the
// callee, and its quick fix calls `NonEmptyList.first` and drops the `!`:
// the fixed program checks clean and has no hint.
func TestPlainCounterpartHint_HintAndQuickFix(t *testing.T) {
	s := NewServer()
	src := "pub fn lead(names: NonEmptyList<String>) -> String\n    List.first(names)!\nend\n"
	want := "pub fn lead(names: NonEmptyList<String>) -> String\n    NonEmptyList.first(names)\nend\n"
	s.docs.Open(parseFixURI, src)
	snap := s.docs.Snapshot(parseFixURI)
	if snap == nil || snap.Analysis == nil {
		t.Fatal("no analysis")
	}
	if errs := snap.TypeErrors(); len(errs) > 0 {
		t.Fatalf("the source does not check: %v", errs)
	}
	diags := fileHintDiagnostics(snap.Analysis, snap.Nodes)
	if len(diags) != 1 {
		t.Fatalf("want one hint, got %+v", diags)
	}
	d := diags[0]
	if *d.Severity != protocol.DiagnosticSeverityHint {
		t.Errorf("severity %v, want a hint", *d.Severity)
	}
	if d.Code == nil || d.Code.Value != analysis.HintPlainCounterpart {
		t.Errorf("code %+v, want %s", d.Code, analysis.HintPlainCounterpart)
	}
	if wantMsg := "`names` is a NonEmptyList: `NonEmptyList.first(names)` is a String, not a Maybe"; d.Message != wantMsg {
		t.Errorf("message:\n got %q\nwant %q", d.Message, wantMsg)
	}
	if d.Range.Start.Line != 1 || d.Range.Start.Character != 4 || d.Range.End.Character != 14 {
		t.Errorf("range %+v, want line 1, characters 4 to 14", d.Range)
	}
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
	var fix *protocol.CodeAction
	for i, a := range actions {
		if a.Title == "Call `NonEmptyList.first` and drop the `!`" {
			fix = &actions[i]
		}
	}
	if fix == nil {
		t.Fatalf("no fix titled \"Call `NonEmptyList.first` and drop the `!`\" among %+v", actions)
	}
	got := applyAllEdits(src, *fix)
	if got != want {
		t.Fatalf("fixed:\n%s\nwant:\n%s", got, want)
	}
	s.docs.Open(parseFixURI, got)
	snap = s.docs.Snapshot(parseFixURI)
	if errs := snap.TypeErrors(); len(errs) > 0 {
		t.Errorf("the fixed program does not check: %v", errs)
	}
	if hints := fileHintDiagnostics(snap.Analysis, snap.Nodes); len(hints) > 0 {
		t.Errorf("the fixed program still has hints: %v", hints)
	}
}
