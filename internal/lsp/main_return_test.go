package lsp

import (
	"strings"
	"testing"
)

// A main declared to return a value is an analyzer error, so the editor
// shows it over the return type, with the help line, as it is typed.
func TestDiagnostics_MainReturningAValue(t *testing.T) {
	const uri = "file:///tmp/main_return/main.nomi"
	src := "fn main(): Result<String, String> {\n    Ok(\"hello\")\n}\n"
	s := NewServer()
	s.docs.Open(uri, src)
	snap := s.docs.Snapshot(uri)
	if snap == nil || snap.Analysis == nil {
		t.Fatal("no analysis")
	}
	diags := buildPublishedDiagnostics(uri, snap.Content, snap.Nodes, snap.Errors, snap.Analysis.TypeErrors)
	for _, d := range diags {
		if !strings.HasPrefix(d.Message, "`main` must return `Unit` or `Result<Unit, E>`") {
			continue
		}
		if !strings.Contains(d.Message, "help: to show a value, print it with `io.print` and return `Ok(Unit)`") {
			t.Fatalf("the diagnostic has no help line: %q", d.Message)
		}
		// `Result<String, String>` on line 0, columns 11 to 33.
		if d.Range.Start.Line != 0 || d.Range.Start.Character != 11 || d.Range.End.Line != 0 || d.Range.End.Character != 33 {
			t.Fatalf("the diagnostic spans %+v, want the return type", d.Range)
		}
		return
	}
	t.Fatalf("no main return type diagnostic among %+v", diags)
}
