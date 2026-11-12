package parser

import (
	"github.com/nomi-language/nomi/internal/ast"
	"testing"
)

// End-position recording on the four scope-anchoring nodes that lacked it
// (Lambda, CaseBranch, ImplBlock, InterfaceDef), so analysis.ScopeAt can
// compute a real [Start, End) span for each.

func TestLambdaSingleExprEndPos(t *testing.T) {
	// Single-expression body: the synthesized Block has EndLine == 0, so the
	// Lambda must carry its own end (one past the last body token, `1`).
	src := "fn x -> x + 1"
	expr := parseExpr(t, src)
	lam, ok := expr.(*ast.Lambda)
	if !ok {
		t.Fatalf("expected *ast.Lambda, got %T", expr)
	}
	if lam.EndLine != 1 {
		t.Errorf("EndLine: expected 1, got %d", lam.EndLine)
	}
	// tokenEnd is one past the last token, i.e. len(src)+1 for a full-tail body.
	if lam.EndCol != len(src)+1 {
		t.Errorf("EndCol: expected %d, got %d", len(src)+1, lam.EndCol)
	}
}

// A body below the `->` ends one past its `end`, and so does the lambda.
func TestLambdaBlockBodyEndPos(t *testing.T) {
	src := "fn x ->\n    x + 1\nend"
	expr := parseExpr(t, src)
	lam, ok := expr.(*ast.Lambda)
	if !ok {
		t.Fatalf("expected *ast.Lambda, got %T", expr)
	}
	if lam.EndLine != 3 || lam.EndCol != 4 {
		t.Errorf("lambda end %d:%d, want 3:4", lam.EndLine, lam.EndCol)
	}
	if lam.Body.EndLine != 3 || lam.Body.EndCol != 1 {
		t.Errorf("body end %d:%d, want 3:1, the `end`", lam.Body.EndLine, lam.Body.EndCol)
	}
}

func TestCaseBranchEndPos(t *testing.T) {
	src := "case x\n  1 -> 10\nend"
	expr := parseExpr(t, src)
	c, ok := expr.(*ast.Case)
	if !ok {
		t.Fatalf("expected *ast.Case, got %T", expr)
	}
	if len(c.Branches) != 1 {
		t.Fatalf("expected 1 branch, got %d", len(c.Branches))
	}
	b := c.Branches[0]
	// Body `10` is on line 2: "  1 -> 10" — one past the last token is col 10.
	if b.EndLine != 2 {
		t.Errorf("EndLine: expected 2, got %d", b.EndLine)
	}
	if b.EndCol != 10 {
		t.Errorf("EndCol: expected 10, got %d", b.EndCol)
	}
}

func TestImplBlockEndPos(t *testing.T) {
	// End is the `end` token's position, mirroring ast.Block's EndCol
	// convention.
	src := "impl Speech for Bar\n    fn speak(_b: self) -> String\n        \"x\"\n    end\nend"
	nodes := parse(t, src)
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	blk, ok := nodes[0].(*ast.ImplBlock)
	if !ok {
		t.Fatalf("expected *ast.ImplBlock, got %T", nodes[0])
	}
	if blk.EndLine != 5 || blk.EndCol != 1 {
		t.Errorf("end %d:%d, want 5:1, the `end`", blk.EndLine, blk.EndCol)
	}
}

func TestInterfaceDefEndPos(t *testing.T) {
	src := "interface Foo\n    fn bar() -> Int\nend"
	nodes := parse(t, src)
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	iface, ok := nodes[0].(*ast.InterfaceDef)
	if !ok {
		t.Fatalf("expected *ast.InterfaceDef, got %T", nodes[0])
	}
	if iface.EndLine != 3 || iface.EndCol != 1 {
		t.Errorf("end %d:%d, want 3:1, the `end`", iface.EndLine, iface.EndCol)
	}
}
