package semtokens_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/internal/semtokens"
	"github.com/nomi-language/nomi/std"
)

// A capture's `&N` is colored as a parameter, whether the capture is a
// partial application or not.
func TestCollect_CaptureParametersAreParameters(t *testing.T) {
	src := `fn add(a: Int, b: Int) -> Int {
  a + b
}

fn f() -> (Int) -> Bool {
  &(&1 > 2)
}

fn g() -> (Int) -> Int {
  &add(1, &1)
}
`
	nodes, err := parser.Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	lib := std.Load()
	fa := analysis.BuildFileWithStdlib(nodes, lib.Primitives, lib.Modules, "", nil)
	analysis.AttachStdlibProjectImpls(fa, lib.Files)
	analysis.BuildTypes(fa, nodes)
	analysis.CheckTypes(fa, nodes)
	want := map[[2]int]bool{{6, 5}: false, {10, 11}: false}
	for _, tok := range semtokens.Collect(fa) {
		key := [2]int{tok.Line, tok.Col}
		if _, ok := want[key]; ok && tok.Type == "parameter" && tok.Length == 2 {
			want[key] = true
		}
	}
	for at, found := range want {
		if !found {
			t.Errorf("no parameter token of length 2 at %d:%d", at[0], at[1])
		}
	}
}
