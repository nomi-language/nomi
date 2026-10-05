package lsp

import (
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// Format-on-save runs `nomi fmt`'s formatter, so it removes a tail `return`
// and keeps an early one.
func TestFormatting_StripsTailReturn(t *testing.T) {
	uri := "file:///tail_return.nomi"
	src := "fn f(x: Int): Int {\n    if x > 0 {\n        return 1\n    }\n    return x\n}\n"
	want := "fn f(x: Int): Int {\n    if x > 0 { return 1 }\n    x\n}\n"

	s := NewServer()
	s.docs.Open(uri, src)
	edits, err := s.textDocumentFormatting(nil, &protocol.DocumentFormattingParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(edits) != 1 {
		t.Fatalf("got %d edits, want 1", len(edits))
	}
	if edits[0].NewText != want {
		t.Errorf("got:\n%s\nwant:\n%s", edits[0].NewText, want)
	}
}
