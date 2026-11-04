package semtokens_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/highlight"
	"github.com/nomi-language/nomi/internal/semtokens"
	"github.com/nomi-language/nomi/std"
)

// Each segment of a full path is a module token of its own length, as a
// file qualifier is.
func TestCollect_FullPathSegmentsAreNamespaces(t *testing.T) {
	src := "import std::io\n\nfn main() {\n    std::io.print(\"x\")\n}\n"
	fa := highlight.Analyze(src, std.Load())
	if fa == nil {
		t.Fatal("Analyze returned nil")
	}
	want := map[int]int{5: len("std"), 10: len("io")}
	for _, tok := range semtokens.Collect(fa) {
		if tok.Line != 4 {
			continue
		}
		if n, ok := want[tok.Col]; ok {
			if tok.Type != "module" || tok.Length != n {
				t.Errorf("token at 4:%d is %+v, want a module token of length %d", tok.Col, tok, n)
			}
			delete(want, tok.Col)
		}
	}
	if len(want) != 0 {
		t.Errorf("no token at line 4, columns %v; tokens: %+v", want, semtokens.Collect(fa))
	}
}
