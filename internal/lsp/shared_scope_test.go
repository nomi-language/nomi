package lsp

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// Every edit rebuilds the document's analysis with its file scope parented
// under the stdlib prelude, which the server keeps for its whole life.
// NewScope used to append each such scope to the prelude's Children, so
// every analysis the server ever built stayed reachable: a Neovim session
// grew nomi-lsp by about 2 MB per keystroke, to several GB in minutes. The
// stdlib's scopes are shared, and an edit must leave them as they were.
func TestEdits_LeaveTheSharedStdlibScopesAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.nomi")
	src := "import std/calendar.Date\n\nfn main() {\n    d = Date.parse(\"2026-05-04\")\n}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	uri := pathToURI(path)
	s := NewServer()
	ctx := &glsp.Context{Notify: func(string, any) {}}
	if err := s.textDocumentDidOpen(ctx, &protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{URI: protocol.DocumentUri(uri), LanguageID: "nomi", Version: 1, Text: src}}); err != nil {
		t.Fatal(err)
	}
	children := func() int {
		n := len(s.std.Primitives.Children)
		for _, scope := range s.std.Modules {
			n += len(scope.Children)
		}
		return n
	}
	before := children()
	for v := 2; v <= 21; v++ {
		text := src + fmt.Sprintf("// edit %d\n", v)
		if err := s.textDocumentDidChange(ctx, &protocol.DidChangeTextDocumentParams{
			TextDocument:   protocol.VersionedTextDocumentIdentifier{TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)}, Version: protocol.Integer(v)},
			ContentChanges: []any{protocol.TextDocumentContentChangeEventWhole{Text: text}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if after := children(); after != before {
		t.Fatalf("20 edits added %d child scopes to the shared stdlib scopes", after-before)
	}
}
