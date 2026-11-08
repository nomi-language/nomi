package parser

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
)

// A distinct type takes type parameters after its name, which its inner type
// may name.
func TestParseGenericDistinct(t *testing.T) {
	nodes, err := Parse(lexer.Lex("type Tagged<T, U> (T, U)\n"))
	if err != nil {
		t.Fatal(err)
	}
	td, ok := nodes[0].(*ast.TypeDef)
	if !ok {
		t.Fatalf("got %T, want *ast.TypeDef", nodes[0])
	}
	if len(td.TypeParams) != 2 || td.TypeParams[0].Name != "T" || td.TypeParams[1].Name != "U" {
		t.Fatalf("type params = %+v, want T, U", td.TypeParams)
	}
	if td.InnerTypeExpr == nil {
		t.Fatal("no inner type")
	}
}

// A zero-sized type has no value for a type parameter to describe.
func TestParseZeroSizedTypeTakesNoTypeParameters(t *testing.T) {
	_, err := Parse(lexer.Lex("type Marker<T>\n"))
	if err == nil {
		t.Fatal("`type Marker<T>` parses")
	}
	if !strings.Contains(err.Error(), "zero-sized `type Marker` takes no type parameters") {
		t.Fatalf("error = %v", err)
	}
}
