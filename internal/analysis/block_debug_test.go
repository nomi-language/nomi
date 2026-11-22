package analysis_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// A type a body declares has the Debug of its shape and no body, but an
// opaque one, whose name-only universal Debug is resolved and checked in
// the declaring block (FileBodies.BlockDebug), so every identifier in it
// has a reference and VerifyElaboration finds every record it holds a
// module-level type's synthesized Debug to.
func TestBlockDebug_IsCheckedInItsBlock(t *testing.T) {
	fa, nodes, errs := checkSourceNodesWithStdlib(`fn show() -> Bool
    enum Shape
        Dot
        Debug
        Circle(Int)
        Boxed(Inner)
    end

    struct Inner
        n: Int
    end

    type Id Int

    opaque struct Hidden
        n: Int
    end

    Hidden{n: 1}.n == 1 and Shape.Boxed(Inner{n: 2}) == Shape.Dot and Id(3) == Id(3)
end
`)
	expectNoStdlibErrors(t, errs)
	if len(fa.BlockDebug) != 1 {
		t.Fatalf("the body declares 1 opaque type and %d types have a universal Debug", len(fa.BlockDebug))
	}
	for decl, impl := range fa.BlockDebug {
		if sd, ok := decl.(*ast.StructDef); !ok || sd.Name != "Hidden" {
			t.Errorf("the universal Debug is written for %v, not the opaque Hidden", decl)
		}
		if !analysis.IsSynthesizedLine(impl.Line) {
			t.Errorf("the Debug of %v is at line %d, outside the synthesized band", decl, impl.Line)
		}
		ast.Inspect(impl, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && fa.References[analysis.Pos{Line: id.Line, Col: id.Col}] == nil {
				t.Errorf("`%s` at %d:%d in the Debug of a block-local type has no reference", id.Name, id.Line, id.Col)
			}
			return true
		})
	}
	for _, v := range analysis.VerifyElaboration(fa, nodes) {
		t.Errorf("%s", v)
	}
}
