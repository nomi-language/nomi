package irbuild

// Tests for the branch and jump classes lowered through `internal/ir`
// (irjump.go). The golden files (internal/expectation) hold the VM's output
// for every program; what is here is a property an output comparison cannot
// see.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/std"
)

// TestIRBranch_EveryBranchSiteHasAPosition measures the premise `ir.At` panics
// on, for every position a branch needs: the construct's own, each arm's, each
// guard's and each condition's, over the corpus and the stdlib.
func TestIRBranch_EveryBranchSiteHasAPosition(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	ifs, cases, arms, positions, bare := 0, 0, 0, 0, 0
	var unpositioned []string

	note := func(where, what string, line int) {
		positions++
		if line < 1 {
			unpositioned = append(unpositioned, fmt.Sprintf("%s: %s has no line", where, what))
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
		case *ast.If:
			ifs++
			note(where, "the `if`", v.Line)
			note(where, "its then arm", firstInt(nodePos(v.Then)))
			if v.Else != nil {
				note(where, "its else arm", firstInt(nodePos(v.Else)))
			}
			switch {
			case v.CondPattern != nil:
			case isNilNode(v.Cond):
				// A BARE `if`: `x |> if { … }`, whose condition is the piped
				// value and which `pipeIfStage` splices before lowering.
				// COUNTED SEPARATELY rather than excluded, because the
				// population is the reason `ifInto` fences the shape instead
				// of trusting the parser — see native.go's isNilNode arm.
				bare++
			default:
				note(where, "its condition", firstInt(nodePos(v.Cond)))
			}
		case *ast.Case:
			cases++
			note(where, "the `case`", v.Line)
			for i := range v.Branches {
				arms++
				note(where, "an arm", v.Branches[i].Line)
				if v.Branches[i].Guard != nil {
					note(where, "an arm guard", firstInt(nodePos(v.Branches[i].Guard)))
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

	if ifs < 100 || cases < 50 {
		t.Fatalf("only %d `if` and %d `case` nodes found, which cannot be true — the walk "+
			"is broken and the zero below would be a false one", ifs, cases)
	}
	// Plant the positive for the bare population too. If it were zero, the
	// arm above would be dead and `ifInto`'s fence would be untested by any
	// tree.
	if bare == 0 {
		t.Error("no bare pipe-stage `if` in either tree, so the claim that such a node " +
			"carries no condition position is unmeasured")
	}
	if len(unpositioned) > 0 {
		t.Errorf("%d branch position(s) carry no 1-based line, so `ir.At` would panic on "+
			"them:\n%s", len(unpositioned), strings.Join(unpositioned, "\n"))
	}
	t.Logf("%d `if` (of which %d bare pipe stages) and %d `case` nodes over %d arms; "+
		"%d positions, 0 without a line", ifs, bare, cases, arms, positions)
}
