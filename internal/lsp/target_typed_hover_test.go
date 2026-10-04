package lsp

import (
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// Hover on a target-typed brace literal (`a: Address = {…}`) names the struct
// it builds: on the opening brace, the struct; on a field label, that
// struct's field.
func TestHover_TargetTypedStructLiteral(t *testing.T) {
	const decls = `struct Address {
  street: String
  city: String
}

struct Box<T> {
  item: T
}

`
	cases := []struct {
		name string
		body string
		want []string
		not  string
	}{
		{"brace at an annotated binding", "fn f(): Address {\n  a: Address = ▮{street: \"s\", city: \"c\"}\n  a\n}\n", []string{"struct Address"}, ""},
		{"brace at a return", "fn f(): Address {\n  ▮{street: \"s\", city: \"c\"}\n}\n", []string{"struct Address"}, ""},
		{"field label", "fn f(): Address {\n  {▮street: \"s\", city: \"c\"}\n}\n", []string{"street: String"}, ""},
		{"generic brace", "fn f(): Box<Int> {\n  ▮{item: 3}\n}\n", []string{"Box<Int>", "struct Box"}, ""},
		{"anonymous brace has no struct hover", "fn f(): Int {\n  r = ▮{street: \"s\", city: \"c\"}\n  String.length(r.street)\n}\n", nil, "struct Address"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input, pos := hoverMarkerPosition(t, decls+tc.body)
			uri := "file:///target_typed_hover.nomi"
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
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Fatalf("hover = %q, want it to contain %q", got, w)
				}
			}
			if tc.not != "" && strings.Contains(got, tc.not) {
				t.Fatalf("hover = %q, want no %q", got, tc.not)
			}
		})
	}
}
