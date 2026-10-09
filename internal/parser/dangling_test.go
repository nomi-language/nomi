package parser

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
)

// ParseFile gives a comment the tree has no slot for to the nearest node:
// after the last node before it inside its innermost container, else before
// the first node after it. Parse leaves it out.
func TestParseFile_AttachesDanglingComments(t *testing.T) {
	src := "fn main() {\n    t = (1, 2 // after two\n    )\n    y = try ( // before g\n        g())\n}\n"
	nodes, _, err := ParseFile(lexer.Lex(src))
	if err != nil {
		t.Fatal(err)
	}
	body := nodes[0].(*ast.FuncDef).Body.Stmts
	two := body[0].(*ast.Binding).Value.(*ast.TupleLit).Items[1].(*ast.IntLit)
	if got := two.GetDanglingAfter(); len(got) != 1 || got[0].Text != "// after two" {
		t.Errorf("2's DanglingAfter = %+v, want the comment after it", got)
	}
	call := body[1].(*ast.Binding).Value.(*ast.TryOp).Expr.(*ast.GroupedExpr).Expr.(*ast.Call)
	if got := call.GetDanglingBefore(); len(got) != 1 || got[0].Text != "// before g" {
		t.Errorf("g()'s DanglingBefore = %+v, want the comment before it", got)
	}

	nodes, err = Parse(lexer.Lex(src))
	if err != nil {
		t.Fatal(err)
	}
	two = nodes[0].(*ast.FuncDef).Body.Stmts[0].(*ast.Binding).Value.(*ast.TupleLit).Items[1].(*ast.IntLit)
	if got := two.GetDanglingAfter(); len(got) != 0 {
		t.Errorf("Parse attached %+v", got)
	}
}

// A comment a Trivia already holds and a `//` with no text are not
// dangling.
func TestParseFile_LeavesClaimedCommentsAlone(t *testing.T) {
	src := "// lead\nfn f() {\n    x = 1 // trailing\n    //\n}\n\n// end\n"
	nodes, _, err := ParseFile(lexer.Lex(src))
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(nodes[0], func(n ast.Node) bool {
		if d, ok := n.(ast.HasDangling); ok && (len(d.GetDanglingBefore()) > 0 || len(d.GetDanglingAfter()) > 0) {
			t.Errorf("%s has dangling comments %+v %+v", n.NodeType(), d.GetDanglingBefore(), d.GetDanglingAfter())
		}
		return true
	})
}
