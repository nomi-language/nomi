package highlight

import (
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/nomi-language/nomi/std"
)

// The tour's highlighter indexes the snippet as a JavaScript string, so a
// semantic token's column and length count UTF-16 code units. Counted in
// bytes, `unit` after `°` was painted one column right.
func TestSemanticTokens_ColumnsAreUTF16(t *testing.T) {
	line := "  \"${n} °${unit} 👍🏽${n} 日本${unit}\""
	src := "fn label(n: Int, unit: String): String {\n" + line + "\n}\n"
	toks := SemanticTokens(src, std.Load())
	type at struct{ col, length int }
	got := map[at]bool{}
	for _, tok := range toks {
		if tok.Line == 2 {
			got[at{tok.Col, tok.Length}] = true
		}
	}
	for i := 0; i < len(line); i++ {
		if !strings.HasPrefix(line[i:], "${") {
			continue
		}
		name := line[i+2 : i+2+strings.IndexByte(line[i+2:], '}')]
		col := len(utf16.Encode([]rune(line[:i+2]))) + 1
		if !got[at{col, len(name)}] {
			t.Errorf("no token for %q at UTF-16 column %d; tokens: %+v", name, col, toks)
		}
	}
}

// The tour asks for a hover at the UTF-16 column the mouse is over, which
// ByteCol turns into the analyzer's byte column.
func TestByteCol_ConvertsAHoverColumn(t *testing.T) {
	code := "first\n  \"°👍🏽日\" x\n"
	line := strings.Split(code, "\n")[1]
	b := strings.Index(line, "x")
	u := len(utf16.Encode([]rune(line[:b])))
	if got := ByteCol(code, 2, u+1); got != b+1 {
		t.Errorf("ByteCol(line 2, col %d) = %d, want %d", u+1, got, b+1)
	}
	if got := ByteCol(code, 9, 4); got != 4 {
		t.Errorf("ByteCol on a line out of range = %d, want the column unchanged", got)
	}
}
