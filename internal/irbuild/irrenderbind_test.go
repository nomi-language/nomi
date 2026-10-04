package irbuild

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/std"
)

// TestIRRender_EveryRenderingSiteHasAPosition measures the premise `ir.At`
// panics on, over the nodes the three disciplines take a position from.
//
// A rendering takes its position from a node the rendering code does not
// otherwise need, and the corpus holds `*ast.If` nodes whose condition carries
// no position, so an absence of crashes is not evidence here.
//
// A SUPERSET of the sites the builder reaches, for the reason
// TestIRConstRef_EveryRoutedSiteHasAPosition gives: the claim is about the
// trees, and a claim about the trees stays true when more of them lowers.
func TestIRRender_EveryRenderingSiteHasAPosition(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	counts := map[string]int{}
	var unpositioned []string

	note := func(where, what string, n ast.Node) {
		if isNilNode(n) {
			return
		}
		counts[what]++
		if line, _ := nodePos(n); line < 1 {
			unpositioned = append(unpositioned,
				fmt.Sprintf("%s: %s has no line", where, what))
		}
	}

	seen := map[ast.Node]bool{}
	var walk func(where string, n ast.Node)
	walk = func(where string, n ast.Node) {
		if isNilNode(n) || seen[n] {
			return
		}
		seen[n] = true
		switch v := n.(type) {
		case *ast.Assertion:
			// The Row discipline's largest caller: the subject and each
			// recorded operand.
			note(where, "an assertion subject", n)
			if b, isBinary := v.Expr.(*ast.Binary); isBinary {
				note(where, "a comparison operand", b.Left)
				note(where, "a comparison operand", b.Right)
			}
		case *ast.StringInterp:
			// The Display discipline: one rendering per interpolated hole.
			for _, p := range v.Parts {
				if e, isExpr := p.(ast.StringExpr); isExpr {
					note(where, "an interpolation hole", e.Expr)
				}
			}
		case *ast.Dbg:
			note(where, "a `dbg`", n)
		case *ast.TryOp:
			// The Row discipline in a test body's `try` guard.
			note(where, "a `try`", n)
		case *ast.Call:
			// `Debug.inspect(x)` and `Display.to_string(x)`, whose renderings
			// take the CALL's position for a refusal and the argument's for
			// the rendering.
			if len(v.Args) == 1 {
				if fa, isField := v.Func.(*ast.FieldAccess); isField {
					// A TYPE qualifier, which is an `*ast.TypeIdent` and not
					// an `*ast.Ident`; reading `*ast.Ident` here counts zero,
					// which the planted floor below catches.
					if ti, isType := fa.Object.(*ast.TypeIdent); isType {
						switch ti.Name {
						case "Debug", "Display", "Int", "Float", "Bool", "String":
							note(where, "a qualified rendering call", n)
						}
					}
				}
			}
		}
		for _, c := range childNodes(n) {
			walk(where, c)
		}
	}

	_, files := corpusAnalysis(t)
	for _, f := range files {
		if f.Prog == nil {
			continue
		}
		for i := range f.Prog.Modules {
			for _, n := range f.Prog.Modules[i].Nodes {
				walk(f.Rel, n)
			}
		}
	}
	lib := std.Load()
	for module, nodes := range lib.Nodes {
		for _, n := range nodes {
			walk("std/"+module, n)
		}
	}

	// PLANT THE POSITIVES, one per discipline's dominant shape, so a walk
	// that found nothing fails rather than reporting an empty answer.
	for what, floor := range map[string]int{
		"an assertion subject":       100,
		"an interpolation hole":      20,
		"a qualified rendering call": 5,
	} {
		if counts[what] < floor {
			t.Errorf("only %d of %q in either tree, which cannot be true — the walk is "+
				"broken and the zero below would be a false one", counts[what], what)
		}
	}
	if len(unpositioned) > 0 {
		t.Errorf("%d rendering position(s) carry no 1-based line, so `ir.At` would panic "+
			"on them:\n%s", len(unpositioned), strings.Join(unpositioned, "\n"))
	}
	total := 0
	for _, n := range counts {
		total += n
	}
	t.Logf("%d rendering positions over %d shapes, 0 without a line: %v",
		total, len(counts), counts)
}

// TestIRBind_TheNodePositionsComeFromTheirNodes reads the positions `ir.Bind`
// and `ir.Copy` carry back off the nodes the production builders make, which
// is the only thing that catches a builder attributing them to the wrong line.
func TestIRBind_TheNodePositionsComeFromTheirNodes(t *testing.T) {
	const file = "probe.nomi"
	g := &gen{nomiPath: file}
	fn := ir.NewFunc(ir.At(file, 1, 1), "probe")
	sh := &irFuncShell{fn: fn, frame: newIRFuncFrame(fn), entry: fn.NewBlock(ir.At(file, 1, 1), "entry")}
	g.irPro = sh
	src := g.irNewTemp(kindInt)

	bindAt := &ast.Binding{Name: "n", Line: 12, Col: 5}
	copyAt := &ast.Break{Line: 19, Col: 7}

	for _, tc := range []struct {
		what string
		got  ir.Pos
		line int
		col  int
	}{
		{"the Bind", g.irBindNode(bindAt, "n", src, kindInt).Pos(), 12, 5},
		{"the Copy", g.irCopyNode(copyAt, kindInt, src).Pos(), 19, 7},
	} {
		if tc.got.Line() != tc.line || tc.got.Col() != tc.col {
			t.Errorf("%s is at %s, want %s:%d:%d", tc.what, tc.got, file, tc.line, tc.col)
		}
	}
}
