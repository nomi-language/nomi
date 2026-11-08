package lsp

import (
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// Hovering an untagged string literal that its position reads as a checked
// literal shows the type the position gave it, which the spelling does not
// say; as an argument, the parameter's hover holds the spot and says it. A
// literal with no such position is a String and shows no type.
func TestHover_TargetTypedStringLiteralShowsItsType(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"binding", "import std::url.Url\n\nfn f() -> Url {\n  u: Url = ▮\"https://example.com/\"\n  u\n}\n", "```nomi\n\"https://example.com/\": Url\n```"},
		{"argument", "import std::url.Url\n\nfn g(u: Url) -> Url {\n  u\n}\n\nfn f() -> Url {\n  g(\"https://ex▮ample.com/\")\n}\n", "```nomi\nu: Url\n```"},
		{"no position", "fn f() -> Int {\n  s = ▮\"https://example.com/\"\n  String.length(s)\n}\n", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			input, pos := hoverMarkerPosition(t, c.input)
			uri := "file:///target_literal_hover_" + strings.ReplaceAll(c.name, " ", "_") + ".nomi"
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
			got := ""
			if res != nil {
				mc, ok := res.Contents.(protocol.MarkupContent)
				if !ok {
					t.Fatalf("expected MarkupContent, got %T", res.Contents)
				}
				got = mc.Value
			}
			if got != c.want {
				t.Errorf("hover = %q, want %q", got, c.want)
			}
		})
	}
}
