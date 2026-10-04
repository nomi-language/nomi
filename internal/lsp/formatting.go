package lsp

import (
	"strings"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/nomi-language/nomi/internal/format"
)

// textDocumentFormatting handles textDocument/formatting requests.
// Returns a single TextEdit that replaces the whole document with the
// formatted source. If the document has parse errors, returns no edits
// (rather than an error) so the user's in-progress edits aren't lost.
func (s *Server) textDocumentFormatting(_ *glsp.Context, params *protocol.DocumentFormattingParams) ([]protocol.TextEdit, error) {
	uri := string(params.TextDocument.URI)
	doc := s.docs.Snapshot(uri)
	if doc == nil {
		return nil, nil
	}

	// The latest text, not the analyzed one: the edit replaces what the
	// client holds.
	formatted, err := format.Format(doc.Text)
	if err != nil {
		return nil, nil
	}
	if formatted == doc.Text {
		return nil, nil
	}

	return []protocol.TextEdit{{
		Range:   fullDocumentRange(doc.Text),
		NewText: formatted,
	}}, nil
}

func fullDocumentRange(content string) protocol.Range {
	lines := strings.Split(content, "\n")
	endLine := uint32(len(lines) - 1)
	endChar := uint32(len(lines[len(lines)-1]))
	return protocol.Range{
		Start: protocol.Position{Line: 0, Character: 0},
		End:   protocol.Position{Line: endLine, Character: endChar},
	}
}
