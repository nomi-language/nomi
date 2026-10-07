package analysis_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// recordedAt returns the type a map records for the node of the given kind at
// line:col, and whether one is recorded.
func recordedAt(m map[ast.Node]analysis.Type, kind string, line, col int) (analysis.Type, bool) {
	for n, ty := range m {
		if n.NodeType() != kind || n.LineNum() != line {
			continue
		}
		switch v := n.(type) {
		case *ast.Ident:
			if v.Col == col {
				return ty, true
			}
		case *ast.DotVariant:
			if v.Col == col {
				return ty, true
			}
		case *ast.Call:
			if v.Col == col {
				return ty, true
			}
		}
	}
	return nil, false
}

func TestExprTypes_RecordValuesAndExpectations(t *testing.T) {
	src := `enum Color {
  Red
  Green
}
fn paint(_c: Color, times: Int): Int { times }
fn demo(): Int {
  xs = [1, 2]
  total = xs |> Iter.count()
  paint(.Red, total)
}`
	fa, errs := checkSourceWithStdlib(src)
	expectNoErrorsT(t, errs)

	// `xs` on the pipe's left, line 8 column 11.
	ty, ok := recordedAt(fa.ExprTypes, "Ident", 8, 11)
	if !ok || analysis.ResolveTypeVar(ty).String() != "List<Int>" {
		t.Fatalf("type of `xs` in the pipe = %v (recorded %v), want List<Int>", ty, ok)
	}
	// `.Red` is checked against paint's first parameter.
	exp, ok := recordedAt(fa.ExpectedTypes, "DotVariant", 9, 9)
	if !ok || exp.String() != "Color" {
		t.Fatalf("expected type at `.Red` = %v (recorded %v), want Color", exp, ok)
	}
	// `total` is checked against the second.
	exp, ok = recordedAt(fa.ExpectedTypes, "Ident", 9, 15)
	if !ok || exp.String() != "Int" {
		t.Fatalf("expected type at `total` = %v (recorded %v), want Int", exp, ok)
	}
}

func TestImplementsInterface(t *testing.T) {
	fa, errs := checkSourceWithStdlib("fn demo(): Int { 1 }")
	expectNoErrorsT(t, errs)
	list := &analysis.ListType{Elem: analysis.TypeInt}
	cases := []struct {
		ty    analysis.Type
		iface string
		want  bool
	}{
		{list, "Iter", true},
		{analysis.TypeInt, "Display", true},
		{analysis.TypeInt, "Iter", false},
		{analysis.TypeString, "Debug", true},
		{&analysis.FuncType{Return: analysis.TypeInt}, "Display", false},
	}
	for _, c := range cases {
		if got := analysis.ImplementsInterface(fa, c.ty, c.iface); got != c.want {
			t.Errorf("ImplementsInterface(%s, %s) = %v, want %v", c.ty, c.iface, got, c.want)
		}
	}
}
