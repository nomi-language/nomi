package lsp

import (
	"fmt"
	"path/filepath"

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
	// A keyword: its construct, in the place it stands (hoverdoc.KeywordAt).
	if content, at, n, ok := hoverdoc.KeywordAt(doc.Analysis, doc.Nodes, doc.Content, pos); ok {
		return tokenHover(doc.Content, content, at, n), nil
	}
	// An `embed` pattern: what it matched (embed.go).
	if info, i, ok := embedPatternAt(doc.Nodes, pos); ok {
		return embedPatternHover(doc.Content, info, i), nil
	}
	// Inside a `phrase` or `noun` body: the MessageFormat 2 there
	// (phrase_hover.go).
	if h := phraseBodyHover(doc.Analysis, doc.Nodes, doc.Content, pos); h != nil {
		return h, nil
	}
	// A static typed literal shows its value (literal_eval.go); on its tag,
	// below the handler's own hover.
	literal, literalRange, onTag, isLiteral := s.literalHover(doc, posToOffset(lineOffsets(doc.Content), pos.Line, pos.Col))
	// What a typed literal is and what its slots take, or what the slot
	// under pos takes (hoverdoc.TypedLiteralNote).
	typed, typedSpan, inSlot := hoverdoc.TypedLiteralNote(doc.Analysis, doc.Nodes, pos)
	if isLiteral && !onTag {
		if typed != "" && !inSlot {
			literal += "\n\n" + typed
		}
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
		if typed != "" && !inSlot {
			return &protocol.Hover{
				Contents: protocol.MarkupContent{Kind: protocol.MarkupKindMarkdown, Value: typed},
				Range:    ptrRange(utf16RangeFromContent(doc.Content, spanRange(typedSpan))),
			}, nil
		}
		return nil, nil
	}

	content := hoverdoc.RenderForEditor(sym, doc.Analysis)
	if content == "" {
		return nil, nil
	}
	if where := fieldSource(sym, tokPos, uriToPath(uri)); where != "" {
		content += "\n\n" + where
	}
	// What converting the value costs at run time (hoverdoc.ConversionNote).
	if note := hoverdoc.ConversionNote(doc.Analysis, pos); note != "" {
		content += "\n\n" + note
	}
	if typed != "" {
		content += "\n\n" + typed
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

// fieldSource names the file and line a field read at tokPos is declared
// at when that is another file: "*declared at* `routes.nomi:3`". For an
// anonymous struct's field it is the literal's label or the type's field
// name (analysis anonFieldRef), which nothing else in the hover names. A
// field with no known declaration says nothing.
func fieldSource(sym *analysis.Symbol, tokPos analysis.Pos, path string) string {
	if sym == nil || sym.Kind != analysis.SymbolField || sym.Undeclared || sym.AppFieldOf != nil {
		return ""
	}
	if sym.SourceFile == "" || sym.SourceFile == path || sym.Pos == tokPos {
		return ""
	}
	return fmt.Sprintf("*declared at* `%s:%d`", filepath.Base(sym.SourceFile), sym.Pos.Line)
}
