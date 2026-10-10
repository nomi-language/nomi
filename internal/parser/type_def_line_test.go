package parser

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// A distinct type's inner type is on the line its name ends on, which may
// be a line after `type`. Measuring from `type` read `type` then `Meters
// Int` as the zero-sized `Meters` and a statement `Int`, and the formatter
// wrote that as two lines that do not mean what the source meant.
func TestParse_TypeDefInnerTypeOnTheNamesLine(t *testing.T) {
	nodes := parse(t, "type\n    Meters Int\n")
	if len(nodes) != 1 {
		t.Fatalf("got %d declarations, want 1", len(nodes))
	}
	def, ok := nodes[0].(*ast.TypeDef)
	if !ok || def.InnerTypeExpr == nil || def.InnerTypeExpr.TypeString() != "Int" {
		t.Fatalf("got %#v, want `type Meters Int`", nodes[0])
	}

	// An inner type on a later line than the name is not one.
	nodes = parse(t, "type Meters\nfn f() {}\n")
	if def, ok := nodes[0].(*ast.TypeDef); !ok || def.InnerTypeExpr != nil {
		t.Fatalf("got %#v, want the zero-sized `type Meters`", nodes[0])
	}
}
