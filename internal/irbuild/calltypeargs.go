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

// recordedTypeArg is the kind the checker instantiated the type parameter
// name of t's callee at. A std operation lowered as a host crossing
// (listCallPlan, vectorCallPlan, setCallPlan, mapOperandsAtSolved) or by
// the builder itself (`Iter.concat`) reads its element kind here rather
// than off its first operand: `List.concat(circles, shapes)` solved T to
// Circle at the first argument and widened it to Shape at the second (spec
// §4 *Chains and conversion*), so the first operand converts to
// `List<Shape>` as a user function's argument would. method is the
// callee's name, which t must name.
//
// kindInvalid: sentinel — nothing recorded, or a kind outside the value
// domain: a call the builder made for another construct (`xs + ys`, a
// spread, a host call standing for a derived body), which names no callee
// of its own or names the construct's, or a std body's own type
// parameter. The caller keeps the operands' kinds.
func (g *gen) recordedTypeArg(t *ast.Call, method, name string) kind {
	sym := g.calleeRecord(t, method)
	if sym == nil {
		return kindInvalid
	}
	return g.valueKindOf(analysis.CallTypeArgs(sym)[name])
}

// solvedParamKind is the kind of t's i-th parameter in the signature the
// checker instantiated its callee at, as recordedTypeArg reads a type
// argument: an operand a later one widened its type parameter past
// converts to it. kindInvalid: sentinel — as recordedTypeArg's.
func (g *gen) solvedParamKind(t *ast.Call, method string, i int) kind {
	sym := g.calleeRecord(t, method)
	if sym == nil {
		return kindInvalid
	}
	ft, _ := sym.CallType.(*analysis.FuncType)
	if ft == nil || i >= len(ft.Params) {
		return kindInvalid
	}
	return g.valueKindOf(ft.Params[i])
}

// calleeRecord is the checker's reference record at t's callee when t
// calls `Owner.method`, or method by a bare imported name, or nil.
func (g *gen) calleeRecord(t *ast.Call, method string) *analysis.Symbol {
	if g.fa == nil || t == nil {
		return nil
	}
	switch f := t.Func.(type) {
	case *ast.FieldAccess:
		if f.Field == nil || f.Field.Name != method {
			return nil
		}
	case *ast.Ident:
		if f.Name != method {
			return nil
		}
	default:
		return nil
	}
	line, col := calleeRefPos(t.Func)
	return g.srcFA().References[analysis.Pos{Line: line, Col: col}]
}

// valueKindOf is ty projected, or kindInvalid (a sentinel) for no type or
// one outside the value domain.
func (g *gen) valueKindOf(ty analysis.Type) kind {
	if ty == nil {
		return kindInvalid
	}
	if k := g.project(ty); irCallableValueKind(k) {
		return k
	}
	return kindInvalid
}
