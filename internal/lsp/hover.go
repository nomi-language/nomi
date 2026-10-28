package lsp

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/hoverdoc"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// textDocumentHover is the LSP hover handler. The rendering (signature + doc)
// lives in the glsp-free internal/hoverdoc package, so the same content is
// reusable by non-LSP callers (the tour wasm, the reference generator).
func (s *Server) textDocumentHover(ctx *glsp.Context, params *protocol.HoverParams) (*protocol.Hover, error) {
	uri := string(params.TextDocument.URI)
	if isInputURI(uri) {
		if d := s.catalogAt(uri); d != nil {
			return s.catalogHover(d, params.Position), nil
		}
		return nil, nil
	}
	doc := s.docs.Snapshot(uri)
	if doc == nil {
		return nil, nil
	}

	pos := analysis.Pos{
		Line: int(params.Position.Line) + 1,
		Col:  utf16ToByteCol(doc.Content, params.Position.Line, params.Position.Character),
	}

	if doc.Analysis == nil {
		return nil, nil
	}
	// A static typed literal shows its value (literal_eval.go); on its tag,
	// below the handler's own hover.
	literal, literalRange, onTag, isLiteral := s.literalHover(doc, posToOffset(lineOffsets(doc.Content), pos.Line, pos.Col))
	if isLiteral && !onTag {
		return &protocol.Hover{
			Contents: protocol.MarkupContent{Kind: protocol.MarkupKindMarkdown, Value: literal},
			Range:    ptrRange(utf16RangeFromContent(doc.Content, literalRange)),
		}, nil
	}
	// The punctuation of a shape literal or pattern, a variant's `.` and
	// `(`, a pattern's `..rest`, a pipe's `|>`, an operator and a discard
	// `_` (hoverdoc.SyntaxAt).
	if content, at, n, ok := hoverdoc.SyntaxAt(doc.Analysis, doc.Nodes, pos); ok {
		return tokenHover(doc.Content, content, at, n), nil
	}
	sym, tokPos, tokLen, ok := doc.Analysis.TokenAt(pos)
	if !ok {
		return nil, nil
	}

	content := hoverdoc.RenderForEditor(sym, doc.Analysis)
	if content == "" {
		return nil, nil
	}
	if isLiteral {
		content += "\n\n---\n\n" + literal
	}

	return tokenHover(doc.Content, content, tokPos, tokLen), nil
}

// tokenHover is a hover showing content over the token of length n at
// start. The highlight is anchored at the token's start, not the cursor:
// anchoring at the cursor makes it appear to begin mid-token. Byte columns
// are converted to the UTF-16 columns the protocol expects.
func tokenHover(content, markdown string, start analysis.Pos, n int) *protocol.Hover {
	hoverLine := toZeroBased(start.Line)
	startByte := toZeroBased(start.Col)
	startChar := byteToUTF16Col(content, hoverLine, startByte)
	endChar := byteToUTF16Col(content, hoverLine, startByte+uint32(n))
	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.MarkupKindMarkdown,
			Value: markdown,
		},
		Range: &protocol.Range{
			Start: protocol.Position{Line: hoverLine, Character: startChar},
			End:   protocol.Position{Line: hoverLine, Character: endChar},
		},
	}
}

func ptrRange(r protocol.Range) *protocol.Range { return &r }
