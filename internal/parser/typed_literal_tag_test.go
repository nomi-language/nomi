package parser

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/lexer"
)

// A lowercase name written right before a string, `date"2026"`, is a typed
// literal whose tag is not a type name. It used to read as two statements,
// "unexpected a string right after the end of a statement".
func TestTypedLiteralTag_ALowercaseTagSaysATagIsATypeName(t *testing.T) {
	for _, src := range []string{
		"fn main()\n  d = date\"2026\"\nend\n",
		"fn main()\n  d = date\"\"\"\n    2026\n    \"\"\"\nend\n",
	} {
		_, err := Parse(lexer.Lex(src))
		want := "a typed literal starts with a type name, and `date` is not one: a type name starts with a capital letter, as in `Date\"...\"`"
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Parse = %v; want %q\n%s", err, want, src)
		}
	}
}
