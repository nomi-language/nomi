package irbuild

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// verifyCallForm fails, under substVerify, when the builder lowers t, a
// qualified call of an interface's function, by routine, which answers it
// as one of want, and the checker's record of how the call is answered
// (analysis.Callee.Form, at the instance's types: calleeAt) is none of
// them. A routine that answers several forms renders a value whose kind
// the builder holds unboxed (an interface value's operand lowered as the
// value it holds, which the record calls dispatched) or through an impl
// the builder writes (a block-local type's Debug, which the record calls
// structural).
//
// A record with no answer is not compared: a call the builder writes
// holding a kind alone (a main's failure rendering, a std interface
// value's dispatch entry), and one whose receiver's type the checker left
// with an open variable (`Result<Int, ?1>`). Nor is a per-instance record
// the instance does not resolve (a std instance's frame has no checker
// type for its parameter).
func (g *gen) verifyCallForm(t *ast.Call, routine string, want ...analysis.OperatorForm) {
	if !substVerify || t == nil || g.srcFA() == nil {
		return
	}
	got := g.calleeAt(t).Form
	if got == 0 || got == analysis.OperatorPerInstance {
		return
	}
	for _, w := range want {
		if got == w {
			return
		}
	}
	g.calleeMismatch(t.Line, t.Col, fmt.Sprintf("%s answers the call as %v, the record as %s", routine, formNames(want), formName(got)))
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
