package lsp

import (
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// A type parameter that can be an interface value is a hint, not an
// error, over its name, and its quick fix writes the interface value: the
// fixed program checks clean and has no hint.
func TestInterfaceValueHint_HintAndQuickFix(t *testing.T) {
	s := NewServer()
	decls := "pub interface Named\n    fn name(value: self) -> String\nend\n\n"
	src := decls + "pub fn label<T>(value: T) -> String where T: Named\n    \"a ${T.name(value)}\"\nend\n"
	want := decls + "pub fn label(value: Named) -> String\n    \"a ${Named.name(value)}\"\nend\n"
	s.docs.Open(parseFixURI, src)
	snap := s.docs.Snapshot(parseFixURI)
	if snap == nil || snap.Analysis == nil {
		t.Fatal("no analysis")
	}
	if errs := snap.TypeErrors(); len(errs) > 0 {
		t.Fatalf("the source does not check: %v", errs)
	}
	diags := interfaceValueDiagnostics(parseFixURI, snapInterfaceValueHints(snap, nil))
	if len(diags) != 1 {
		t.Fatalf("want one hint, got %+v", diags)
	}
	d := diags[0]
	if *d.Severity != protocol.DiagnosticSeverityHint {
		t.Errorf("severity %v, want a hint", *d.Severity)
	}
	if d.Code == nil || d.Code.Value != "interface-value-parameter" {
		t.Errorf("code %+v, want interface-value-parameter", d.Code)
	}
	wantMsg := "`T` is used only as the type of `value`, under one bound: write `value: Named`\n" +
		"help: a parameter of type Named takes a value of any implementor, as `T` does; keep `T` when the caller's type matters past the call"
	if d.Message != wantMsg {
		t.Errorf("message:\n got %q\nwant %q", d.Message, wantMsg)
	}
	if d.Range.Start.Line != 4 || d.Range.Start.Character != 13 || d.Range.End.Character != 14 {
		t.Errorf("range %+v, want line 4, characters 13 to 14", d.Range)
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
		if a.Title == "Write `value: Named`" {
			fix = &actions[i]
		}
	}
	if fix == nil {
		t.Fatalf("no fix titled \"Write `value: Named`\" among %+v", actions)
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
	if hints := snapInterfaceValueHints(snap, nil); len(hints) > 0 {
		t.Errorf("the fixed program still has hints: %v", hints)
	}
}
