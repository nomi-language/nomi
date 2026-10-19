package analysis

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// The record call form's literal is the variant's payload and is typed as
// one: the record of an inline variant's fields, or the embedded struct. It
// had no type recorded, though the call had the enum's.
func TestVariantCtor_RecordCallFormTypesItsLiteral(t *testing.T) {
	src := `struct Circle {
    radius: Float
}

enum Pay {
    Has {a: Int, b: String}
    embeds Circle
    Gone
}

fn has() -> Pay {
    Pay.Has(#{a: 1, b: "x"})
}

fn circle() -> Pay {
    Pay.Circle(#{radius: 1.0})
}
`
	fa, nodes := checkNodes(t, src)
	for _, u := range UnresolvedExprs(fa, nodes) {
		t.Errorf("%d:%d: %s %s has %s", u.Line, u.Col, u.Node.NodeType(), u.Where, u.Missing)
	}
	want := map[int]string{12: "#{a: Int, b: String}", 16: "Circle"}
	for _, node := range nodes {
		ast.Inspect(node, func(n ast.Node) bool {
			lit, ok := n.(*ast.StructLit)
			if !ok || lit.TypeName != nil {
				return true
			}
			w, ok := want[lit.Line]
			if !ok {
				return true
			}
			delete(want, lit.Line)
			if got := fa.ExprTypes[lit]; got == nil || got.String() != w {
				t.Errorf("line %d: the literal is typed %v, want %s", lit.Line, got, w)
			}
			return true
		})
	}
	for line := range want {
		t.Errorf("no anonymous struct literal found on line %d", line)
	}
}
