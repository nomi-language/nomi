package lsp

import (
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// Hovering a codepoint literal shows the literal and its type, which the
// spelling does not name. The range covers both quotes.
func TestHover_CodepointLiteralShowsCodepoint(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
		end   uint32
	}{
		{"binding", "fn f(): Bool {\n  x = ▮'\\''\n  x == 'a'\n}\n", "```nomi\n'\\'': Codepoint\n```", 10},
		{"pattern", "fn f(cp: Codepoint): Int {\n  case cp {\n    ▮'a' -> 1\n    _ -> 2\n  }\n}\n", "```nomi\n'a': Codepoint\n```", 7},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			input, pos := hoverMarkerPosition(t, c.input)
			uri := "file:///codepoint_hover_" + c.name + ".nomi"
			s := NewServer()
			s.docs.Open(uri, input)
			res, err := s.textDocumentHover(nil, &protocol.HoverParams{
				TextDocumentPositionParams: protocol.TextDocumentPositionParams{
					TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
					Position:     protocol.Position{Line: uint32(pos.Line - 1), Character: uint32(pos.Col - 1)},
				},
			})
			if err != nil {
				t.Fatalf("hover error: %v", err)
			}
			if res == nil {
				t.Fatal("no hover on a codepoint literal")
			}
			mc, ok := res.Contents.(protocol.MarkupContent)
			if !ok {
				t.Fatalf("expected MarkupContent, got %T", res.Contents)
			}
			if mc.Value != c.want {
				t.Errorf("hover = %q, want %q", mc.Value, c.want)
			}
			if res.Range == nil || res.Range.End.Character != c.end {
				t.Errorf("hover range = %+v, want it to end at character %d", res.Range, c.end)
			}
		})
	}
}
