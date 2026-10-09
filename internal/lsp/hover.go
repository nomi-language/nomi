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
	sym, tokPos, tokLen, ok := doc.Analysis.TokenAt(pos)
	if !ok {
		if content, isTarget := targetStructHover(doc.Analysis, pos); isTarget {
			line := toZeroBased(pos.Line)
			start := byteToUTF16Col(doc.Content, line, toZeroBased(pos.Col))
			return &protocol.Hover{
				Contents: protocol.MarkupContent{Kind: protocol.MarkupKindMarkdown, Value: content},
				Range: &protocol.Range{
					Start: protocol.Position{Line: line, Character: start},
					End:   protocol.Position{Line: line, Character: start + 1},
				},
			}, nil
		}
		return nil, nil
	}

	content := hoverdoc.RenderForEditor(sym, doc.Analysis)
	if content == "" {
		return nil, nil
	}
	if isLiteral {
		content += "\n\n---\n\n" + literal
	}

	// Highlight the hovered token, anchored at its start (not the cursor —
	// anchoring at the cursor makes the highlight appear to begin mid-token).
	// Byte columns are converted to the UTF-16 columns the protocol expects.
	hoverLine := toZeroBased(tokPos.Line)
	startByte := toZeroBased(tokPos.Col)
	startChar := byteToUTF16Col(doc.Content, hoverLine, startByte)
	endChar := byteToUTF16Col(doc.Content, hoverLine, startByte+uint32(tokLen))

	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.MarkupKindMarkdown,
			Value: content,
		},
		Range: &protocol.Range{
			Start: protocol.Position{Line: hoverLine, Character: startChar},
			End:   protocol.Position{Line: hoverLine, Character: endChar},
		},
	}, nil
}

func ptrRange(r protocol.Range) *protocol.Range { return &r }

// targetStructHover is the hover on the opening brace of a target-typed
// struct literal (`a: Address = {street: …}`): the struct the literal builds,
// as hovering `Address` in `Address{…}` shows it. A generic struct leads with
// its instantiation (`Box<Int>`).
func targetStructHover(fa *analysis.FileAnalysis, pos analysis.Pos) (string, bool) {
	for lit, st := range fa.TargetStructs {
		if lit.Line != pos.Line || lit.Col != pos.Col {
			continue
		}
		head := "```nomi\n" + st.String() + "\n```"
		var body string
		if scope := fa.ScopeAt(pos); scope != nil {
			if sym := realSymbol(scope.Lookup(st.Name)); sym != nil {
				if decl, ok := sym.Type.(*analysis.StructType); ok && decl.Name == st.Name && decl.Origin == st.Origin {
					body = hoverdoc.RenderForEditor(sym, fa)
				}
			}
		}
		switch {
		case body == "":
			return head, true
		case len(st.TypeArgs) > 0:
			return head + "\n\n---\n\n" + body, true
		default:
			return body, true
		}
	}
	return "", false
}
