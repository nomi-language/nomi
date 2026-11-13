package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// calleeTypeArgs is the type arguments the checker instantiated the generic
// callee named at ref at, one kind per name in names, in this gen's kinds.
// ref is a call's callee node (t.Func) or a function used as a value; the
// checker records its instantiation at the name (calleeRefPos), and
// analysis.CallTypeArgs reads each type argument off it. Every generic
// instantiation this package builds, of a function in this file, another
// file, an impl block or std, is at these arguments, so the instance a call
// names is the one the checker typed it at. Nothing here solves: a name
// with no argument in the record, or an argument outside the value domain,
// answers false, and the call declines.
//
// A type argument may name an enclosing declaration's own type parameter
// (`walk(rest)` inside `fn walk<T>`); projecting it reads the frame the
// enclosing instance pushed (pushMonoSubst). A call the checker recorded
// no instantiation for names a callee whose type parameters are all the
// enclosing declaration's own (VerifyElaboration's rule: `compare(ta, tb)`
// inside `impl Comparable for Vector<T>`'s compare), so each argument is
// that parameter in the frame.
func (g *gen) calleeTypeArgs(ref ast.Node, names []string) ([]kind, bool) {
	if len(names) == 0 {
		return nil, true
	}
	if g.fa == nil || ref == nil {
		return nil, false
	}
	line, col := calleeRefPos(ref)
	sym := g.srcFA().References[analysis.Pos{Line: line, Col: col}]
	if sym == nil {
		// The checker records a reference for every callee and every name
		// used as a value (analysis package doc, "Elaboration").
		panic("irbuild: a generic callee with no reference record")
	}
	types := analysis.CallTypeArgs(sym)
	out := make([]kind, len(names))
	for i, name := range names {
		var k kind
		if t, found := types[name]; found {
			k = g.project(t)
		} else if sym.CallType != nil {
			return nil, false
		} else if k, found = g.genericSubstKind(name); !found {
			return nil, false
		}
		if k != kindUnit && !irCallableValueKind(k) {
			return nil, false
		}
		out[i] = k
	}
	return out, true
}

// monoInstanceAt is the instance of this gen's generic template tpl that ref
// (a call's callee, or the function used as a value) names: tpl at the type
// arguments the checker instantiated it at.
func (g *gen) monoInstanceAt(ref ast.Node, tpl *monoTemplate, name string) (*monoInst, bool) {
	args, ok := g.calleeTypeArgs(ref, tpl.params)
	if !ok {
		return nil, false
	}
	inst, why, _ := g.resolveMonoInstance(tpl, args, name)
	if why != "" || inst == nil {
		return nil, false
	}
	return inst, true
}
