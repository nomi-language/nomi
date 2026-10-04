package analysis_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// TokenAt must report the *start* of the token the cursor lands in, not the
// cursor position — hover highlights the whole token, and anchoring at the
// cursor made the highlight appear to begin mid-token.
func TestTokenAt_AnchorsAtTokenStart(t *testing.T) {
	src := `import std/io
fn main() {
  io.print("hi")
}`
	fa, _ := checkSourceWithStdlib(src)

	var printStart analysis.Pos
	for pos, sym := range fa.References {
		if sym != nil && sym.Name == "print" {
			printStart = pos
		}
	}
	if printStart.Line == 0 {
		t.Fatal("no `print` reference recorded")
	}

	// Cursor two columns into the token (`pr|int`) must still resolve to the
	// token start and full length.
	sym, start, length, ok := fa.TokenAt(analysis.Pos{Line: printStart.Line, Col: printStart.Col + 2})
	if !ok || sym == nil {
		t.Fatalf("TokenAt mid-token returned ok=%v sym=%v", ok, sym)
	}
	if start != printStart {
		t.Errorf("token start: got %v, want %v (the token start, not the cursor)", start, printStart)
	}
	if length != len("print") {
		t.Errorf("token length: got %d, want %d", length, len("print"))
	}
}
