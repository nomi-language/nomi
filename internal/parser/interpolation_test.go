package parser

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

func TestInterpolationNestedCodeAndAppFieldRead(t *testing.T) {
	nodes := parse(t, `fn main() { _ = "outer ${{label: "inner ${MyApp.port}"}.label}" }`)
	fn := nodes[0].(*ast.FuncDef)
	binding := fn.Body.Stmts[0].(*ast.Binding)
	outer := binding.Value.(*ast.StringInterp)
	access := outer.Parts[1].(ast.StringExpr).Expr.(*ast.FieldAccess)
	record := access.Object.(*ast.StructLit)
	inner := record.Fields[0].Value.(*ast.StringInterp)
	field := inner.Parts[1].(ast.StringExpr).Expr.(*ast.FieldAccess)
	if field.Field.Name != "port" || field.Object.(*ast.TypeIdent).Name != "MyApp" {
		t.Fatalf("app field read: %#v", field)
	}
}

func TestInterpolationContainsSetLiteral(t *testing.T) {
	expression := parseExpr(t, `"set ${#{1, 2}}"`)
	interp := expression.(*ast.StringInterp)
	if _, ok := interp.Parts[1].(ast.StringExpr).Expr.(*ast.SetLit); !ok {
		t.Fatalf("interpolation expression: %#v", interp.Parts[1])
	}
}

// `#{` in a string is text.
func TestInterpolationHashBraceIsText(t *testing.T) {
	expression := parseExpr(t, `"set #{1, 2}"`)
	if lit, ok := expression.(*ast.StringLit); !ok || lit.Value != "set #{1, 2}" {
		t.Fatalf("expected the plain text, got %#v", expression)
	}
}
