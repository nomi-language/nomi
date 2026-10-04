package lsp

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// privateUserFile is generatedFile(300) with User private, so each of the
// 300 public functions reports "exposes private type User".
func privateUserFile() string {
	return strings.Replace(generatedFile(300), "pub struct User", "struct User", 1)
}

// noScans fails when run calls nthLine, a scan of a text from its top: a
// request converting many ranges must convert them through a lineIndex.
func noScans(t *testing.T, what string, run func()) {
	t.Helper()
	before := nthLineScans.Load()
	run()
	if d := nthLineScans.Load() - before; d != 0 {
		t.Errorf("%s scanned a text from its top %d times, want 0", what, d)
	}
}

// TestDiagnosticsAndCodeActions_ConvertThroughALineIndex publishes the 300
// diagnostics of a 2,705-line file and asks for code actions over all of
// them, counting nthLine calls.
func TestDiagnosticsAndCodeActions_ConvertThroughALineIndex(t *testing.T) {
	src := privateUserFile()
	s, uri := openProject(t, map[string]string{"main.nomi": src}, "main.nomi")
	var diags []protocol.Diagnostic
	notify := glsp.NotifyFunc(func(method string, params any) {
		if p, ok := params.(*protocol.PublishDiagnosticsParams); ok && string(p.URI) == uri {
			diags = p.Diagnostics
		}
	})
	noScans(t, "publishing diagnostics", func() { s.publish(notify, uri) })
	if len(diags) < 300 {
		t.Fatalf("published %d diagnostics, want at least 300", len(diags))
	}
	noScans(t, "code actions over 300 diagnostics", func() {
		if _, err := s.textDocumentCodeAction(nil, &protocol.CodeActionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
			Context:      protocol.CodeActionContext{Diagnostics: diags},
		}); err != nil {
			t.Fatal(err)
		}
	})
}

// TestRenameAndWorkspaceSymbol_ConvertThroughALineIndex renames a struct
// named 600 times and lists the file's 300 functions as workspace
// symbols, counting nthLine calls.
func TestRenameAndWorkspaceSymbol_ConvertThroughALineIndex(t *testing.T) {
	src := generatedFile(300)
	s, uri := openProject(t, map[string]string{"main.nomi": src}, "main.nomi")
	noScans(t, "rename", func() {
		edit, err := s.textDocumentRename(nil, &protocol.RenameParams{
			TextDocumentPositionParams: protocol.TextDocumentPositionParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
				Position:     offsetPosition(src, strings.Index(src, "User")),
			},
			NewName: "Person",
		})
		if err != nil {
			t.Fatal(err)
		}
		if n := len(edit.Changes[protocol.DocumentUri(uri)]); n < 600 {
			t.Fatalf("rename edits %d places, want at least 600", n)
		}
	})
	noScans(t, "workspace symbols", func() {
		syms, err := s.workspaceSymbol(nil, &protocol.WorkspaceSymbolParams{Query: "f"})
		if err != nil {
			t.Fatal(err)
		}
		if len(syms) < 300 {
			t.Fatalf("%d workspace symbols, want at least 300", len(syms))
		}
	})
}

// TestPublish_OneWalkAndTheCachedTokens counts the passes a publish makes
// over an open document whose 303 diagnostics need their spans read from
// the tokens: one WalkNodes per top-level node (todo and dbg sites
// together), and the tokens lexed once into the server's cache, which a
// second publish and an inlay hint request reuse.
func TestPublish_OneWalkAndTheCachedTokens(t *testing.T) {
	src := privateUserFile() + "\nfn later(): Int {\n    dbg(todo)\n}\n"
	s, uri := openProject(t, map[string]string{"main.nomi": src}, "main.nomi")
	snap := s.docs.Snapshot(uri)
	var diags []protocol.Diagnostic
	notify := glsp.NotifyFunc(func(method string, params any) {
		if p, ok := params.(*protocol.PublishDiagnosticsParams); ok {
			diags = p.Diagnostics
		}
	})
	walks := analysis.WalkNodesCalls()
	s.publish(notify, uri)
	if d := analysis.WalkNodesCalls() - walks; d != int64(len(snap.Nodes)) {
		t.Errorf("a publish walked %d trees, want one per top-level node (%d)", d, len(snap.Nodes))
	}
	var sources []string
	for _, d := range diags {
		if d.Source != nil && (*d.Source == "nomi-dbg" || *d.Source == "nomi-todo") {
			sources = append(sources, *d.Source)
		}
	}
	if strings.Join(sources, " ") != "nomi-dbg nomi-todo" {
		t.Errorf("dbg and todo warnings %v, want one of each", sources)
	}
	if s.pipeTokens.lexes != 1 {
		t.Fatalf("a publish lexed %d times into the cache, want once", s.pipeTokens.lexes)
	}
	s.publish(notify, uri)
	inlay := &InlayHintParams{}
	inlay.TextDocument.URI = uri
	inlay.Range.End.Line = uint32(strings.Count(src, "\n") + 1)
	if _, err := s.textDocumentInlayHint(nil, inlay); err != nil {
		t.Fatal(err)
	}
	if s.pipeTokens.lexes != 1 {
		t.Errorf("a second publish and an inlay hint request lexed %d times in all, want once", s.pipeTokens.lexes)
	}
}

// BenchmarkManyRanges measures the requests that convert hundreds of
// ranges: publishing the 303 diagnostics of the 2,705-line file with a
// private User, and renaming User (601 edits).
//
//	go test ./internal/lsp -run '^$' -bench BenchmarkManyRanges -benchmem
func BenchmarkManyRanges(b *testing.B) {
	s, uri := openProject(b, map[string]string{"main.nomi": privateUserFile()}, "main.nomi")
	b.Run("publishDiagnostics", func(b *testing.B) {
		b.ReportAllocs()
		notify := glsp.NotifyFunc(func(string, any) {})
		for b.Loop() {
			s.publish(notify, uri)
		}
	})
	src := generatedFile(300)
	s, uri = openProject(b, map[string]string{"main.nomi": src}, "main.nomi")
	params := &protocol.RenameParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
			Position:     offsetPosition(src, strings.Index(src, "User")),
		},
		NewName: "Person",
	}
	b.Run("rename", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := s.textDocumentRename(nil, params); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// TestCallHierarchy_ConvertsThroughALineIndex asks for the 300 incoming
// calls of a function and the 300 outgoing calls of another, counting
// nthLine calls.
func TestCallHierarchy_ConvertsThroughALineIndex(t *testing.T) {
	var b strings.Builder
	b.WriteString("fn callee(n: Int): Int {\n    n\n}\n")
	var calls []string
	for i := range 300 {
		fmt.Fprintf(&b, "\nfn c%d(): Int {\n    callee(%d)\n}\n", i, i)
		calls = append(calls, fmt.Sprintf("c%d()", i))
	}
	fmt.Fprintf(&b, "\nfn all(): Int {\n    %s\n}\n", strings.Join(calls, " + "))
	src := b.String()
	s, uri := openProject(t, map[string]string{"main.nomi": src}, "main.nomi")
	noScans(t, "incoming calls", func() {
		in := s.incomingCalls(t.Context(), callItemData{URI: uri, Line: 1, Col: 4, Name: "callee"})
		if len(in) != 300 {
			t.Fatalf("%d incoming calls, want 300", len(in))
		}
	})
	allLine := strings.Count(src[:strings.Index(src, "fn all")], "\n") + 1
	noScans(t, "outgoing calls", func() {
		out, err := s.callHierarchyOutgoingCalls(nil, &protocol.CallHierarchyOutgoingCallsParams{
			Item: protocol.CallHierarchyItem{Data: callItemData{URI: uri, Line: allLine, Col: 4, Name: "all"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(out) != 300 {
			t.Fatalf("%d outgoing calls, want 300", len(out))
		}
	})
}
