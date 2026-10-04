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
	src := "|x| x + 1"
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

func TestLambdaBlockBodyEndPos(t *testing.T) {
	src := "|x| { x }"
	expr := parseExpr(t, src)
	lam, ok := expr.(*ast.Lambda)
	if !ok {
		t.Fatalf("expected *ast.Lambda, got %T", expr)
	}
	if lam.EndLine != 1 {
		t.Errorf("EndLine: expected 1, got %d", lam.EndLine)
	}
	if lam.EndCol != len(src)+1 {
		t.Errorf("EndCol: expected %d, got %d", len(src)+1, lam.EndCol)
	}
}

func TestCaseBranchEndPos(t *testing.T) {
	src := "case x {\n  1 -> 10\n}"
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
	// End is the closing-brace column itself (exclusive), mirroring ast.Block's
	// EndCol convention. The final `}` is the last char.
	src := "impl Speech for Bar { fn speak(_b: self): String { \"x\" } }"
	nodes := parse(t, src)
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	blk, ok := nodes[0].(*ast.ImplBlock)
	if !ok {
		t.Fatalf("expected *ast.ImplBlock, got %T", nodes[0])
	}
	if blk.EndLine != 1 {
		t.Errorf("EndLine: expected 1, got %d", blk.EndLine)
	}
	if blk.EndCol != len(src) {
		t.Errorf("EndCol: expected %d (col of closing brace), got %d", len(src), blk.EndCol)
	}
}

func TestInterfaceDefEndPos(t *testing.T) {
	src := "interface Foo { fn bar(): Int }"
	nodes := parse(t, src)
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	iface, ok := nodes[0].(*ast.InterfaceDef)
	if !ok {
		t.Fatalf("expected *ast.InterfaceDef, got %T", nodes[0])
	}
	if iface.EndLine != 1 {
		t.Errorf("EndLine: expected 1, got %d", iface.EndLine)
	}
	if iface.EndCol != len(src) {
		t.Errorf("EndCol: expected %d (col of closing brace), got %d", len(src), iface.EndCol)
	}
}
