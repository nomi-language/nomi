package parser

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/lexer"
)

const strayDocMessage = "a `///` doc comment must be followed by a declaration it documents"

// A `///` comment above a top-level item that takes no doc comment is an
// error: accepted, nothing would read it and `nomi fmt` would delete the line.
func TestStrayDocComment_IsAnError(t *testing.T) {
	cases := map[string]string{
		"import":                              "/// Explains the imports.\nimport std/io\n",
		"test block":                          "/// Explains the test.\ntest \"t\" {\n  assert True\n}\n",
		"after a blank line before an import": "fn f(): Int {\n  1\n}\n\n/// Stray.\nimport std/io\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(lexer.Lex(src))
			if err == nil {
				t.Fatalf("Parse accepted a `///` comment with no declaration to document:\n%s", src)
			}
			if !strings.Contains(err.Error(), strayDocMessage) {
				t.Fatalf("Parse error = %q, want it to say %q", err, strayDocMessage)
			}
			docLine := 0
			for i, line := range strings.Split(src, "\n") {
				if strings.HasPrefix(line, "///") {
					docLine = i + 1
				}
			}
			if pe, ok := err.(ParseError); !ok || pe.Line != docLine || pe.Col != 1 {
				t.Fatalf("Parse error = %#v, want it at %d:1, the `///` line", err, docLine)
			}
		})
	}
}

// The resilient parse the LSP uses reports the same error and keeps the item.
func TestStrayDocComment_ResilientParseReportsIt(t *testing.T) {
	nodes, errs, _ := ParseResilient(lexer.Lex("/// Stray.\nimport std/io\n"))
	if len(errs) != 1 || !strings.Contains(errs[0].Message, strayDocMessage) {
		t.Fatalf("ParseResilient errors = %v, want one stray-doc-comment error", errs)
	}
	if len(nodes) != 1 {
		t.Fatalf("ParseResilient kept %d nodes, want the import", len(nodes))
	}
}

// A doc comment on a declaration still attaches.
func TestStrayDocComment_DeclarationsStillTakeDocs(t *testing.T) {
	for _, src := range []string{
		"/// Doubles n.\nfn double(n: Int): Int {\n  n * 2\n}\n",
		"/// A point.\nstruct Point {\n  x: Int\n}\n",
		"/// Cached.\nonce answer = 42\n",
	} {
		if _, err := Parse(lexer.Lex(src)); err != nil {
			t.Fatalf("Parse rejected a documented declaration: %v\n%s", err, src)
		}
	}
}
