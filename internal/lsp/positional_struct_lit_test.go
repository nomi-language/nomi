package lsp

import "testing"

// A struct literal's values written without field names get the fix that
// names them in declaration order, and the fixed program checks clean.
func TestPositionalStructLit_QuickFixNamesTheFields(t *testing.T) {
	s := NewServer()
	decl := "pub struct Student {\n    name: String\n    score: Int\n}\n\n"
	src := decl + "pub fn f(p: (String, Int)) -> Student {\n    Student{p.first, p.second}\n}\n"
	want := decl + "pub fn f(p: (String, Int)) -> Student {\n    Student{name: p.first, score: p.second}\n}\n"
	actions := parseFixActions(t, s, src, "Name the fields")
	if len(actions) != 1 {
		t.Fatalf("got %d fixes, want 1", len(actions))
	}
	got := applyAllEdits(src, actions[0])
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	s.docs.Open(parseFixURI, got)
	snap := s.docs.Snapshot(parseFixURI)
	if diags := buildPublishedDiagnostics(parseFixURI, snap.Content, snap.Nodes, snap.Errors, snap.Analysis.TypeErrors); len(diags) != 0 {
		t.Errorf("fixed program has diagnostics: %v", diags)
	}
}
