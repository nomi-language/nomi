package irbuild

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// verifyCallForm fails, under substVerify, when the builder lowers t, a
// qualified call of an interface's function whose receiver it holds as a
// value of kind recv, by routine, which answers it as one of want, and the
// checker's record of how the call is answered (analysis.Callee.Form, at
// the instance's types: calleeAt) is none of them. A dispatched record is
// answered by a routine that dispatches (want holds OperatorDispatched)
// on a value held boxed, and by any routine on a value whose kind names
// what it holds, an Iter's view of its source or a std value held unboxed
// (heldAs); a value held no way an interface value is fails. A routine
// that answers several other forms renders through an impl the builder
// writes (a block-local type's Debug, which the record calls structural).
//
// A record with no answer is not compared: a call the builder writes
// holding a kind alone (a main's failure rendering, a std interface
// value's dispatch entry). Every call the checker checked has one
// (TestElaborationGate). Nor is a per-instance record the instance does
// not resolve (an instance entered with no checker type for its
// parameter, the few checkSubstCensus names).
func (g *gen) verifyCallForm(t *ast.Call, recv kind, routine string, want ...analysis.OperatorForm) {
	if !substVerify || t == nil || g.srcFA() == nil {
		return
	}
	got := g.calleeAt(t).Form
	if got == 0 || got == analysis.OperatorPerInstance {
		return
	}
	dispatches := false
	for _, w := range want {
		if w == analysis.OperatorDispatched {
			dispatches = true
		}
		if got == w && w != analysis.OperatorDispatched {
			return
		}
	}
	if got == analysis.OperatorDispatched {
		held := g.heldAs(recv)
		switch {
		case dispatches && held == heldBoxed:
			return
		case !dispatches && (held == heldIterView || held == heldUnboxed):
			return
		}
		g.calleeMismatch(t.Line, t.Col, fmt.Sprintf("%s answers the call as %v over a value held %s, and the record dispatches it", routine, formNames(want), heldNames[held]))
		return
	}
	g.calleeMismatch(t.Line, t.Col, fmt.Sprintf("%s answers the call as %v, the record as %s", routine, formNames(want), formName(got)))
}

var heldNames = [...]string{
	heldNot: "as no interface value", heldBoxed: "boxed", heldIterView: "as an Iter's view", heldUnboxed: "unboxed",
}

func formNames(fs []analysis.OperatorForm) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = formName(f)
	}
	return out
}

func formName(f analysis.OperatorForm) string {
	switch f {
	case analysis.OperatorByImpl:
		return "by impl"
	case analysis.OperatorStructural:
		return "structural"
	case analysis.OperatorDispatched:
		return "dispatched"
	case analysis.OperatorPerInstance:
		return "per instance"
	}
	return "none"
}

// recordNamesImpl reports whether the checker answered t, a qualified
// interface call, by an impl of its operand's type (analysis.Callee.Form,
// at the instance's types). Such a call is not structural, whether or not
// the impl's function is in scope yet: in an import cycle's first pass a
// std module is lowered before the members whose impls it calls
// (lowerStdUnit), so the scope cannot say whether an impl exists.
func (g *gen) recordNamesImpl(t *ast.Call) bool {
	return g.calleeAt(t).Form == analysis.OperatorByImpl
}
