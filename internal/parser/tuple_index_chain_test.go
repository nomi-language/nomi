package parser

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
)

// `t.1.0` reads as `(t.1).0`, although the lexer reads `1.0` as a Float:
// each index is its own field access, positioned at its own digits and dot.
func TestTupleIndexChain_FloatTokenSplits(t *testing.T) {
	expr := parseExpr(t, "t.1.0.x")
	outer, ok := expr.(*ast.FieldAccess)
	if !ok || outer.Field.Name != "x" {
		t.Fatalf("want .x outermost, got %#v", expr)
	}
	second, ok := outer.Object.(*ast.FieldAccess)
	if !ok || second.Field.Name != "0" || second.Col != 4 || second.Field.Col != 5 {
		t.Fatalf("want .0 at col 4 (index at 5), got %#v", outer.Object)
	}
	first, ok := second.Object.(*ast.FieldAccess)
	if !ok || first.Field.Name != "1" || first.Col != 2 || first.Field.Col != 3 {
		t.Fatalf("want .1 at col 2 (index at 3), got %#v", second.Object)
	}
	if id, ok := first.Object.(*ast.Ident); !ok || id.Name != "t" {
		t.Fatalf("want t innermost, got %#v", first.Object)
	}
}

// A Float with an exponent, a separator or a suffix is not two indices.
func TestTupleIndexChain_OtherFloatsAreStillErrors(t *testing.T) {
	for _, src := range []string{"t.1e3", "t.1_0.0", "t.1.5d"} {
		if _, err := Parse(lexer.Lex(src)); err == nil {
			t.Errorf("Parse(%q) succeeded; want the field-name error", src)
		}
	}
}
