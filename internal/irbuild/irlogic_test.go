package irbuild

// Tests for the logic class lowered through `internal/ir` (irlogic.go).
//
// The property held here is that every short-circuit in the corpus and the
// stdlib has the three positions `ir.BeginShortCircuit` demands.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/std"
)

// TestIRLogic_EveryShortCircuitSiteHasAPosition measures the premise `ir.At`
// panics on, for the three positions a short-circuit needs: the OPERATOR's,
// the LEFT operand's and the RIGHT operand's.
//
// It is a stronger premise than the arith class's, and that is the reason it
// is measured separately rather than folded into
// TestIRArith_EveryArithSiteHasAPosition. An `ir.Arith` needs one position,
// the operator's, and reads its operands as already-lowered temporaries.
// `ir.BeginShortCircuit` takes THREE — opPos for the branch test, lhsPos for
// the copy that makes the left value the answer, and rhsPos for the block the
// right operand lowers into — so a short-circuit whose operands carry no line
// would panic where the same expression's `+` would not.
//
// Over BOTH trees, because they are lowered by different gens: the corpus
// through `Analyze`, the stdlib through `std.Load`.
func TestIRLogic_EveryShortCircuitSiteHasAPosition(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	ands, ors, synthesized := 0, 0, 0
	var unpositioned []string

	check := func(where string, root ast.Node) {
		for _, n := range logicNodesUnder(root) {
			if n.Op == "or" {
				ors++
			} else {
				ands++
			}
			for _, p := range []struct {
				what string
				line int
				col  int
			}{
				{"the operator", n.Line, n.Col},
				{"its left operand", firstInt(nodePos(n.Left)), 0},
				{"its right operand", firstInt(nodePos(n.Right)), 0},
			} {
				if p.line < 1 {
					unpositioned = append(unpositioned,
						fmt.Sprintf("%s: `%s` at line %d col %d — %s has no line",
							where, n.Op, n.Line, n.Col, p.what))
				}
			}
			if !emittableLine(n.Line) {
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

	// PLANT A POSITIVE, on both operators. A walk that found nothing would
	// report zero unpositioned sites, and so would a correct one.
	if ands < 20 {
		t.Fatalf("only %d `and` operators found across the corpus and the stdlib, which "+
			"cannot be true — the walk is broken and the zero below would be a false one", ands)
	}
	if ors < 5 {
		t.Fatalf("only %d `or` operators found, so the `or` half of this reading is vacuous", ors)
	}
	if len(unpositioned) > 0 {
		t.Errorf("%d short-circuit position(s) carry no 1-based line, so `ir.At` would panic "+
			"on them:\n%s", len(unpositioned), strings.Join(unpositioned, "\n"))
	}
	t.Logf("%d `and` and %d `or` operators, %d at a SYNTHESIZED position, "+
		"0 of the %d positions they need without a line",
		ands, ors, synthesized, 3*(ands+ors))
}

// firstInt is nodePos's line, discarding its column. nodePos answers two
// values and only the line is what `ir.At` rejects.
func firstInt(line, _ int) int { return line }

// logicNodesUnder collects every `*ast.Binary` carrying `and` or `or`
// reachable from root.
//
// A SUPERSET of the sites the builder reaches, for the reason arithNodesUnder
// is: the claim being measured is about the trees, and a claim about the trees
// stays true when more of them starts lowering.
func logicNodesUnder(root ast.Node) []*ast.Binary {
	var out []*ast.Binary
	seen := map[ast.Node]bool{}
	var walk func(n ast.Node)
	walk = func(n ast.Node) {
		if isNilNode(n) || seen[n] {
			return
		}
		seen[n] = true
		if v, isBinary := n.(*ast.Binary); isBinary && (v.Op == "and" || v.Op == "or") {
			out = append(out, v)
		}
		for _, c := range childNodes(n) {
			walk(c)
		}
	}
	walk(root)
	return out
}
