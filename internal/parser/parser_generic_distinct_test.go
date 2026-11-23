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

// A generic type's `where` bounds its parameters, after its inner type and
// any refinement parents and before a `when` rule.
func TestParseGenericDistinctWhere(t *testing.T) {
	nodes, err := Parse(lexer.Lex("refine Sorted<T> List<T> where T: Comparable when fn xs -> True\n"))
	if err != nil {
		t.Fatal(err)
	}
	td := nodes[0].(*ast.TypeDef)
	if len(td.WhereClauses) != 1 || td.WhereClauses[0].Name != "T" || len(td.WhereClauses[0].Bounds) != 1 {
		t.Fatalf("where = %+v, want T: Comparable", td.WhereClauses)
	}
	if td.Rule == nil {
		t.Fatal("no rule after the where clause")
	}
	nodes, err = Parse(lexer.Lex("refine Run<T> Many<T> and Sorted<T> where T: Comparable and Hashable when fn xs -> True\n"))
	if err != nil {
		t.Fatal(err)
	}
	td = nodes[0].(*ast.TypeDef)
	if len(td.MoreParents) != 1 || len(td.WhereClauses) != 1 || len(td.WhereClauses[0].Bounds) != 2 {
		t.Fatalf("parents %d, where %+v; want one more parent and two bounds", len(td.MoreParents), td.WhereClauses)
	}
}

// A `where` on a type with no type parameters bounds nothing.
func TestParseWhereWithoutTypeParameters(t *testing.T) {
	_, err := Parse(lexer.Lex("type Ints List<Int> where T: Comparable\n"))
	if err == nil || !strings.Contains(err.Error(), "`where` bounds a type parameter, and `type Ints` declares none") {
		t.Fatalf("error = %v", err)
	}
}
