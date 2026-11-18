package irbuild

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// Two parses of one std module declare each function twice, with one std
// identity wherever it sits in each, and sameDecl takes them for one
// declaration; any other pair of nodes is two, so verifyCallee still fails a
// route that lowers another function.
func TestSameDecl_IsOneDeclarationAcrossTwoParses(t *testing.T) {
	writerPrint := analysis.StdFunc{Module: "std/io", Owner: "Writer", Name: "print"}
	shared := &ast.FuncDef{Name: "print", Line: 741, Col: 5}
	edited := &ast.FuncDef{Name: "print", Line: 741, Col: 5}
	for _, c := range []struct {
		name       string
		a, b       ast.Node
		aStd, bStd analysis.StdFunc
		want       bool
	}{
		{"one node", shared, shared, analysis.StdFunc{}, analysis.StdFunc{}, true},
		{"two parses of one std function", edited, shared, writerPrint, writerPrint, true},
		{"a program's two declarations at one position", edited, shared, analysis.StdFunc{}, analysis.StdFunc{}, false},
		{"another std function at the same position", edited, shared, writerPrint, analysis.StdFunc{Module: "std/io", Name: "print"}, false},
		{"one receiver's impls of an interface at two type arguments", edited, shared,
			analysis.StdFunc{Module: "std/strings", Owner: "String", Impl: "Contains<String>", Name: "contains"},
			analysis.StdFunc{Module: "std/strings", Owner: "String", Impl: "Contains<Codepoint>", Name: "contains"}, false},
		{"the same std function moved in an edited parse", edited, &ast.FuncDef{Name: "print", Line: 745, Col: 5}, writerPrint, writerPrint, true},
		{"no declaration", nil, shared, writerPrint, writerPrint, false},
	} {
		if got := sameDecl(c.a, c.b, c.aStd, c.bStd); got != c.want {
			t.Errorf("%s: sameDecl = %v, want %v", c.name, got, c.want)
		}
	}
}
