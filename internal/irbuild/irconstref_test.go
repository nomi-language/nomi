package irbuild

// Tests for the constant and reference instructions lowered through
// `internal/ir`.
//
// The property held here: every routed site has a position, measured over
// the corpus and the stdlib rather than inferred from the absence of a panic.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/std"
)

// TestIRConstRef_EveryRoutedSiteHasAPosition measures the premise `ir.At`
// panics on, over the node types these two classes read a position from.
//
// Same instrument as TestIRArith_EveryArithSiteHasAPosition and
// TestIRLogic_EveryShortCircuitSiteHasAPosition, and here for the same reason:
// `irNodePos` holds the IR's contract rather than falling back, because
// falling back would silently refuse a construct that otherwise lowers.
// `ir.At` panics on a line below 1, so the claim is that no node these classes
// reach carries one.
//
// A SUPERSET of the sites the builder reaches, for the reason arithNodesUnder
// and logicNodesUnder are: the claim is about the trees, and a claim about the
// trees stays true when more of them starts lowering. It does not cover nodes
// synthesized during lowering, which `arith` and `logic` do not cover either.
//
// Over BOTH trees, because they are lowered by different gens: the corpus
// through `Analyze`, the stdlib through `std.Load`.
func TestIRConstRef_EveryRoutedSiteHasAPosition(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	counts := map[string]int{}
	synthesized := 0
	var unpositioned []string

	check := func(where string, root ast.Node) {
		for _, n := range constRefNodesUnder(root) {
			what := fmt.Sprintf("%T", n)
			counts[what]++
			line, col := nodePos(n)
			if line < 1 {
				unpositioned = append(unpositioned,
					fmt.Sprintf("%s: %s at line %d col %d has no line", where, what, line, col))
			}
			if !emittableLine(line) {
				synthesized++
			}
		}
	}

	_, files := corpusAnalysis(t)
	for _, f := range files {
		if f.Prog == nil {
			continue
		}
		for i := range f.Prog.Modules {
			for _, n := range f.Prog.Modules[i].Nodes {
				check(f.Rel, n)
			}
		}
	}
	lib := std.Load()
	for module, nodes := range lib.Nodes {
		for _, n := range nodes {
			check("std/"+module, n)
		}
	}

	total := 0
	for _, n := range counts {
		total += n
	}
	// PLANT A POSITIVE on each node type the two classes actually read a
	// position from. A walk that found none of them would report zero
	// unpositioned sites, and so would a correct one.
	for _, want := range []string{
		"*ast.IntLit", "*ast.StringLit", "*ast.TypeIdent", "*ast.Ident", "*ast.ListLit",
	} {
		if counts[want] < 5 {
			t.Fatalf("only %d %s nodes found across the corpus and the stdlib, which cannot "+
				"be true — the walk is broken and the zero below would be a false one",
				counts[want], want)
		}
	}
	if len(unpositioned) > 0 {
		shown := unpositioned
		if len(shown) > 20 {
			shown = shown[:20]
		}
		t.Errorf("%d routed site(s) carry no 1-based line, so `ir.At` would panic on "+
			"them:\n%s", len(unpositioned), strings.Join(shown, "\n"))
	}
	t.Logf("%d const/ref position sources across the corpus and the stdlib, %d at a "+
		"SYNTHESIZED position, 0 without a line; by type: %v", total, synthesized, counts)
}

// constRefNodesUnder collects every node whose position `irNodePos` reads for
// a `const` or a `ref`.
//
// `*ast.Call` and `*ast.FieldAccess` are in because `Set.new()`,
// `Vector.empty()`, a stdlib `once` read and an app-field read take their
// position from one; both populations are far larger than the routed sites,
// which is the superset this test's header argues for.
func constRefNodesUnder(root ast.Node) []ast.Node {
	var out []ast.Node
	seen := map[ast.Node]bool{}
	var walk func(n ast.Node)
	walk = func(n ast.Node) {
		if isNilNode(n) || seen[n] {
			return
		}
		seen[n] = true
		switch n.(type) {
		case *ast.IntLit, *ast.FloatLit, *ast.StringLit, *ast.DecimalLit,
			*ast.TypeIdent, *ast.Ident, *ast.ListLit, *ast.ListSpreadLit,
			*ast.Call, *ast.FieldAccess:
			out = append(out, n)
		}
		for _, c := range childNodes(n) {
			walk(c)
		}
	}
	walk(root)
	return out
}
