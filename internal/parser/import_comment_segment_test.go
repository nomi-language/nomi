package parser

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/lexer"
)

// A comment is no import path segment, though its text may read as a name:
// `import///A` used to import `A`, and `nomi fmt` wrote `import A`, dropping
// the comment. Found by FuzzFormatKeepsMeaning. An `import` that a comment
// ends opens an import block.
func TestImportPathSegmentIsNoComment(t *testing.T) {
	for src, want := range map[string]string{"import///A\n": "expected import path", "import //A\n": "to close the import block"} {
		_, err := Parse(lexer.Lex(src))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Parse(%q) error = %v, want the import block a comment-ended `import` opens", src, err)
		}
	}
}
