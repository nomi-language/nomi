package parser

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/lexer"
)

// A comment is no import path segment, though its text may read as a name:
// `import///A` used to import `A`, and `nomi fmt` wrote `import A`, dropping
// the comment. Found by FuzzFormatKeepsMeaning.
func TestImportPathSegmentIsNoComment(t *testing.T) {
	for _, src := range []string{"import///A\n", "import //A\n"} {
		_, err := Parse(lexer.Lex(src))
		if err == nil || !strings.Contains(err.Error(), "expected import path") {
			t.Errorf("Parse(%q) error = %v, want the missing import path", src, err)
		}
	}
}
