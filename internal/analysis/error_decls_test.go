package analysis

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// An error in a function, an impl function or a test names that
// declaration; one in a type, at an impl block's header or in another file
// names none, and the file has no declarations to leave out.
func TestErrorDeclarations(t *testing.T) {
	const src = `struct Box
    n: Int
end

fn f(x: Int) -> Int
    x + 1
end

impl Box
    fn g(b: Box) -> Int
        b.n
    end
end

test "t"
    assert 1 == 1
end
`
	nodes, err := parser.Parse(lexer.Lex(src))
	if err != nil {
		t.Fatal(err)
	}
	var f, g, test ast.Node
	for _, n := range nodes {
		switch d := n.(type) {
		case *ast.FuncDef:
			f = d
		case *ast.ImplBlock:
			g = d.Items[0]
		case *ast.TestDecl:
			test = d
		}
	}
	decls, ok := ErrorDeclarations(nodes, []TypeError{{Line: 6, Col: 5}, {Line: 11, Col: 9}, {Line: 16, Col: 12}})
	if !ok || len(decls) != 3 || !decls[f] || !decls[g] || !decls[test] {
		t.Errorf("errors in f, g and the test name %v (ok %v); want those three", decls, ok)
	}
	for _, e := range []TypeError{{Line: 2, Col: 8}, {Line: 9, Col: 1}, {Line: 6, Col: 5, File: "other.nomi"}} {
		if decls, ok := ErrorDeclarations(nodes, []TypeError{{Line: 6, Col: 5}, e}); ok || decls != nil {
			t.Errorf("an error at %d:%d in %q names %v (ok %v); want none", e.Line, e.Col, e.File, decls, ok)
		}
	}
}
