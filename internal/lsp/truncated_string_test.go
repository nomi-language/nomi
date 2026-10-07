package lsp

import (
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// A document that ends inside a string, as one does while it is typed, is
// published with the lexer's diagnosis and formats to no edit; the server
// does not panic on it.
func TestTruncatedStringIsADiagnostic(t *testing.T) {
	cases := []struct{ src, want string }{
		{`"\$`, `invalid escape \$`},
		{"fn main() {\n  x = \"abc", `unterminated string; close it with "`},
		{"fn main() {\n  x = \"a\\", `unterminated string; close it with "`},
		{"fn main() {\n  x = \"a${", `unterminated ${...} in a string; close it with }`},
		{"fn main() {\n  x = `abc", "unterminated raw string; close it with `"},
		{"fn main() {\n  x = \"\"\"\n  a ${b", `unterminated ${...} in a triple-quoted string; close it with }`},
		{"fn main() {\n  x = \"\"\"\n  abc", `unterminated triple-quoted string; close it with """`},
	}
	for _, c := range cases {
		t.Run(c.want, func(t *testing.T) {
			s, uri, _, published := openLoweringDoc(t, c.src)
			awaitPublish(t, published, "with "+c.want, func(diags []protocol.Diagnostic) bool {
				for _, d := range diags {
					if strings.Contains(d.Message, c.want) {
						return true
					}
				}
				return false
			})
			edits, err := s.textDocumentFormatting(nil, &protocol.DocumentFormattingParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
			})
			if err != nil || len(edits) != 0 {
				t.Fatalf("formatting a truncated string = %v, %v; want no edit", edits, err)
			}
		})
	}
}
