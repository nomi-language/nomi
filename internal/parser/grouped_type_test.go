package parser

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// Nomi has no 1-tuple, so `(T)` in a type is grouping: however many pairs
// surround it, the type is T itself, in every type position, and it keeps
// its own position and span. Read as a one-element FuncType, `(Int)` was a
// 1-tuple type to the checker, and `Completed((T))` kept one pair after the
// variant's own collapse, which nomi fmt wrote as `Completed (T)`.
func TestParse_GroupedTypeIsTheType(t *testing.T) {
	for _, src := range []string{"(Int)", "((Int))", "(((Int)))"} {
		nodes := parse(t, "fn f(x: "+src+") -> "+src+" {\n    x\n}\n")
		fn := nodes[0].(*ast.FuncDef)
		for what, te := range map[string]ast.TypeExpr{"parameter": fn.Params[0].TypeAnnotation, "return": fn.ReturnTypeExpr} {
			st, ok := te.(*ast.SimpleType)
			if !ok || st.Name != "Int" {
				t.Fatalf("%s: the %s type is %#v, want SimpleType Int", src, what, te)
			}
		}
		st := fn.Params[0].TypeAnnotation.(*ast.SimpleType)
		if want := 9 + (len(src)-3)/2; st.Col != want || st.Span.StartCol != want || st.Span.EndCol != want+3 {
			t.Fatalf("%s: Int at col %d spanning %v, want col %d spanning only the name", src, st.Col, st.Span, want)
		}
	}

	for _, src := range []string{"Completed(T)", "Completed((T))", "Completed(((T)))", "Completed ((T))", "Completed T"} {
		def := parse(t, "enum S<T> {\n    "+src+"\n}\n")[0].(*ast.EnumDef)
		if st, ok := def.Variants[0].DataTypeExpr.(*ast.SimpleType); !ok || st.Name != "T" {
			t.Fatalf("%s: payload is %#v, want SimpleType T", src, def.Variants[0].DataTypeExpr)
		}
	}

	// A tuple and a function type keep their own parentheses inside a
	// grouping pair.
	def := parse(t, "type P (#(Int, String))\n")[0].(*ast.TypeDef)
	if ft, ok := def.InnerTypeExpr.(*ast.FuncType); !ok || ft.Return != nil || len(ft.Params) != 2 {
		t.Fatalf("got %#v, want the tuple (Int, String)", def.InnerTypeExpr)
	}
	def = parse(t, "type F (((Int)) -> ((Int)))\n")[0].(*ast.TypeDef)
	ft, ok := def.InnerTypeExpr.(*ast.FuncType)
	if !ok || ft.Return == nil || len(ft.Params) != 1 || ft.TypeString() != "(Int) -> Int" {
		t.Fatalf("got %#v, want the function type (Int) -> Int", def.InnerTypeExpr)
	}
}

// `#(T,)` is not a 1-tuple type; the parser rejects it.
func TestParse_OneElementTupleTypeIsRejected(t *testing.T) {
	err := parseError(t, "fn f(x: #(Int,)) -> Int {\n    x\n}\n")
	if want := "a tuple type has at least two elements"; !strings.Contains(err.Error(), want) {
		t.Fatalf("got %q, want it to say %q", err, want)
	}
}
