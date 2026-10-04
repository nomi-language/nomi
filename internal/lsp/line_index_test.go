package lsp

import (
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// TestLineIndex_AgreesWithNthLine converts every column of every line,
// and lines past the end, through a lineIndex and through the scanning
// functions, over texts with multi-byte and astral characters, empty
// lines, and with and without a final newline.
func TestLineIndex_AgreesWithNthLine(t *testing.T) {
	for _, content := range []string{
		"",
		"\n",
		"a",
		"fn main() {\n    x = \"é—😀\"\n\n    x\n}",
		"fn main() {\n    x = \"é—😀\"\n\n    x\n}\n",
		"😀😀\nab\n\n",
	} {
		li := newLineIndex(content)
		lines := uint32(strings.Count(content, "\n") + 3)
		for line := range lines {
			text, ok := nthLine(content, line)
			if got, gotOK := li.line(line); got != text || gotOK != ok {
				t.Errorf("%q line %d: index gives %q %v, nthLine %q %v", content, line, got, gotOK, text, ok)
			}
			for col := range uint32(len(text) + 3) {
				if got, want := li.utf16Col(line, col), byteToUTF16Col(content, line, col); got != want {
					t.Errorf("%q %d:%d: utf16Col %d, byteToUTF16Col %d", content, line, col, got, want)
				}
				if got, want := li.byteCol(line, col), utf16ToByteCol(content, line, col); got != want {
					t.Errorf("%q %d:%d: byteCol %d, utf16ToByteCol %d", content, line, col, got, want)
				}
			}
		}
	}
}

// TestOutlineRequests_ConvertThroughOneLineIndex counts the work of the
// requests that convert a range per declaration or hint: over five rounds
// of documentSymbol, codeLens, documentLink and a whole-file inlayHint on
// a 2,705-line file, the server builds one line index and never scans the
// text from its top.
func TestOutlineRequests_ConvertThroughOneLineIndex(t *testing.T) {
	src := generatedFile(300)
	s, uri := openProject(t, map[string]string{"main.nomi": src}, "main.nomi")
	doc := protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)}
	inlay := &InlayHintParams{}
	inlay.TextDocument.URI = uri
	inlay.Range.End.Line = uint32(strings.Count(src, "\n") + 1)
	scans := nthLineScans.Load()
	for range 5 {
		syms, err := s.textDocumentDocumentSymbol(nil, &protocol.DocumentSymbolParams{TextDocument: doc})
		if err != nil {
			t.Fatal(err)
		}
		if n := len(syms.([]protocol.DocumentSymbol)); n != 301 {
			t.Fatalf("%d symbols, want 301", n)
		}
		lenses, err := s.textDocumentCodeLens(nil, &protocol.CodeLensParams{TextDocument: doc})
		if err != nil {
			t.Fatal(err)
		}
		if len(lenses) != 300 {
			t.Fatalf("%d lenses, want 300", len(lenses))
		}
		if _, err := s.textDocumentDocumentLink(nil, &protocol.DocumentLinkParams{TextDocument: doc}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.textDocumentInlayHint(nil, inlay); err != nil {
			t.Fatal(err)
		}
	}
	if d := nthLineScans.Load() - scans; d != 0 {
		t.Errorf("the requests scanned the text from the top %d times, want 0", d)
	}
	if s.lines.builds != 1 {
		t.Errorf("built the line index %d times, want once", s.lines.builds)
	}
}
