package lsp

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// `"Hello, $name"` with a `name` in scope is a warning over the `$name`,
// and its quick fix writes `${name}`: the fixed program checks clean and
// has no warning.
func TestDollarWarning_WarningAndQuickFix(t *testing.T) {
	s := NewServer()
	src := "pub fn greet(name: String) -> String\n    \"Hello, $name\" + name\nend\n"
	want := "pub fn greet(name: String) -> String\n    \"Hello, ${name}\" + name\nend\n"
	s.docs.Open(parseFixURI, src)
	snap := s.docs.Snapshot(parseFixURI)
	if snap == nil || snap.Analysis == nil {
		t.Fatal("no analysis")
	}
	if errs := snap.TypeErrors(); len(errs) > 0 {
		t.Fatalf("the source does not check: %v", errs)
	}
	var diags []protocol.Diagnostic
	for _, d := range buildPublishedDiagnostics(parseFixURI, src, snap.Nodes, snap.Analysis, snap.Errors, snap.TypeErrors()) {
		if d.Code != nil && d.Code.Value == analysis.DollarInterpolationCode {
			diags = append(diags, d)
		}
	}
	if len(diags) != 1 {
		t.Fatalf("want one warning among the published diagnostics, got %+v", diags)
	}
	d := diags[0]
	if *d.Severity != protocol.DiagnosticSeverityWarning {
		t.Errorf("severity %v, want a warning", *d.Severity)
	}
	if wantMsg := "`$name` is text here, not the value of `name`: Nomi interpolates with `${...}`, not `$`\nhelp: to interpolate, write `\"Hello, ${name}\"`"; d.Message != wantMsg {
		t.Errorf("message:\n got %q\nwant %q", d.Message, wantMsg)
	}
	if d.Range.Start.Line != 1 || d.Range.Start.Character != 12 || d.Range.End.Character != 17 {
		t.Errorf("range %+v, want line 1, characters 12 to 17", d.Range)
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
		if a.Title == "Write `${name}`" {
			fix = &actions[i]
		}
	}
	if fix == nil {
		t.Fatalf("no fix titled \"Write `${name}`\" among %+v", actions)
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
	if warns := dollarWarningDiagnostics(parseFixURI, snap.Analysis, snap.Nodes); len(warns) > 0 {
		t.Errorf("the fixed program still has warnings: %v", warns)
	}
}
