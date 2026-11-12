package lsp

import (
	"sort"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

const releaseURI = "file:///release/main.nomi"

// releaseActions opens src and returns the fixes titled title offered over
// every diagnostic the client holds.
func releaseActions(t *testing.T, s *Server, src, title string) []protocol.CodeAction {
	t.Helper()
	s.docs.Open(releaseURI, src)
	snap := s.docs.Snapshot(releaseURI)
	if snap == nil || snap.Analysis == nil {
		t.Fatal("no analysis")
	}
	diags := buildPublishedDiagnostics(releaseURI, snap.Content, snap.Nodes, snap.Analysis, snap.Errors, snap.Analysis.TypeErrors)
	res, err := s.textDocumentCodeAction(nil, &protocol.CodeActionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(releaseURI)},
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
		if a.Title == title {
			out = append(out, a)
		}
	}
	return out
}

// "Release `w`" defers the release after the binding and, for a writer,
// closes it before the success return; "Release the `Reader`" binds a
// dropped acquisition first; "Commit `w`" adds the close a path misses.
// Each fixed program checks.
func TestReleaseFix_DefersTheReleaseAndCommits(t *testing.T) {
	for _, tc := range []struct{ name, title, src, want string }{
		{
			"reader",
			"Release `r`",
			"import std::fs\nimport std::io.{Error, Reader}\n\nfn all(path: String) -> Result<String, Error>\n    r = fs.open(path)?\n    Reader.read_all_text(r)\nend\n\nfn main()\n    _ = all(\"a\")\nend\n",
			"import std::fs\nimport std::io.{Error, Reader}\n\nfn all(path: String) -> Result<String, Error>\n    r = fs.open(path)?\n    defer Reader.close_quietly(r)\n    Reader.read_all_text(r)\nend\n\nfn main()\n    _ = all(\"a\")\nend\n",
		},
		{
			"writer",
			"Release `w`",
			"import std::fs\nimport std::io.{Error, Writer}\n\nfn save(path: String) -> Result<Unit, Error>\n    w = fs.create(path)?\n    Writer.write_text(w, \"x\")?\n    Ok(Unit)\nend\n\nfn main()\n    _ = save(\"a\")\nend\n",
			"import std::fs\nimport std::io.{Error, Writer}\n\nfn save(path: String) -> Result<Unit, Error>\n    w = fs.create(path)?\n    defer Writer.discard(w)\n    Writer.write_text(w, \"x\")?\n    Writer.close(w)?\n    Ok(Unit)\nend\n\nfn main()\n    _ = save(\"a\")\nend\n",
		},
		{
			"dropped reader",
			"Release the `Reader`",
			"import std::fs\nimport std::io.{Error, Reader}\n\nfn touch(path: String) -> Result<Unit, Error>\n    _ = fs.open(path)?\n    Ok(Unit)\nend\n\nfn main()\n    _ = touch(\"a\")\nend\n",
			"import std::fs\nimport std::io.{Error, Reader}\n\nfn touch(path: String) -> Result<Unit, Error>\n    reader = fs.open(path)?\n    defer Reader.close_quietly(reader)\n    Ok(Unit)\nend\n\nfn main()\n    _ = touch(\"a\")\nend\n",
		},
		{
			"missing close",
			"Commit `w`",
			"import std::fs\nimport std::io.{Error, Writer}\n\nfn save(path: String) -> Result<Unit, Error>\n    w = fs.create(path)?\n    defer Writer.discard(w)\n    Writer.write_text(w, \"x\")?\n    Ok(Unit)\nend\n\nfn main()\n    _ = save(\"a\")\nend\n",
			"import std::fs\nimport std::io.{Error, Writer}\n\nfn save(path: String) -> Result<Unit, Error>\n    w = fs.create(path)?\n    defer Writer.discard(w)\n    Writer.write_text(w, \"x\")?\n    Writer.close(w)?\n    Ok(Unit)\nend\n\nfn main()\n    _ = save(\"a\")\nend\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewServer()
			actions := releaseActions(t, s, tc.src, tc.title)
			if len(actions) != 1 {
				t.Fatalf("got %d fixes titled %s, want 1", len(actions), tc.title)
			}
			got := applyTextEditsLastFirst(tc.src, actions[0].Edit.Changes[protocol.DocumentUri(releaseURI)])
			if got != tc.want {
				t.Fatalf("got\n%s\nwant\n%s", got, tc.want)
			}
			s.docs.Open(releaseURI, got)
			if errs := s.docs.Snapshot(releaseURI).Analysis.TypeErrors; len(errs) > 0 {
				t.Fatalf("the fixed program has errors: %v", errs)
			}
		})
	}
}

// applyTextEditsLastFirst applies edits, which do not overlap, from the
// last to the first.
func applyTextEditsLastFirst(content string, edits []protocol.TextEdit) string {
	sorted := append([]protocol.TextEdit(nil), edits...)
	sort.Slice(sorted, func(i, j int) bool {
		a, b := sorted[i].Range.Start, sorted[j].Range.Start
		return a.Line > b.Line || (a.Line == b.Line && a.Character > b.Character)
	})
	for _, e := range sorted {
		content = applyTextEdit(content, e)
	}
	return content
}
