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
	ks, _, ok := g.calleeTypeArgsOf(ref, names, false)
	return ks, ok
}

// calleeTypedArgs is calleeTypeArgs with each argument as the checker's
// type too, which an instance the call makes carries (substFrame): the
// recorded type with the enclosing instances' types put for their type
// parameters (checkerTypeArg), or the enclosing instance's type for a
// parameter that is its own; nil where that has none.
func (g *gen) calleeTypedArgs(ref ast.Node, names []string) ([]kind, []analysis.Type, bool) {
	return g.calleeTypeArgsOf(ref, names, true)
}

func (g *gen) calleeTypeArgsOf(ref ast.Node, names []string, typed bool) ([]kind, []analysis.Type, bool) {
	if len(names) == 0 {
		return nil, nil, true
	}
	if g.fa == nil || ref == nil {
		panic("irbuild: calleeTypeArgs: the builder lowers only checked programs, and every caller passes a callee node")
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
	var checked []analysis.Type
	if typed {
		checked = make([]analysis.Type, len(names))
	}
	for i, name := range names {
		var k kind
		if t, found := types[name]; found {
			k = g.project(t)
			if typed {
				checked[i] = g.checkerTypeArg(t)
			}
		} else if sym.CallType != nil {
			return nil, nil, false
		} else if k, found = g.genericSubstKind(name); !found {
			return nil, nil, false
		} else if typed {
			checked[i], _ = g.genericSubstType(name)
		}
		if k != kindUnit && !irCallableValueKind(k) {
			return nil, nil, false
		}
		out[i] = k
	}
	return out, checked, true
}

// monoInstanceAt is the instance of this gen's generic template tpl that ref
// (a call's callee, or the function used as a value) names: tpl at the type
// arguments the checker instantiated it at.
func (g *gen) monoInstanceAt(ref ast.Node, tpl *monoTemplate, name string) (*monoInst, bool) {
	args, types, ok := g.calleeTypedArgs(ref, tpl.params)
	if !ok {
		return nil, false
	}
	inst, why, _ := g.resolveMonoInstance(tpl, args, types, name)
	if why != "" || inst == nil {
		return nil, false
	}
	return inst, true
}

// recordedTypeArg is the kind the checker instantiated the type parameter
// name of t's callee at, and the checker's type it was read from at this
// instance (checkerTypeArg), nil where it has none. A std operation lowered as a host crossing
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
func (g *gen) recordedTypeArg(t *ast.Call, method, name string) (kind, analysis.Type) {
	sym := g.calleeRecord(t, method)
	if sym == nil {
		return kindInvalid, nil
	}
	arg := analysis.CallTypeArgs(sym)[name]
	k := g.valueKindOf(arg)
	// kindInvalid: sentinel — valueKindOf's nothing-recorded answer, which carries no type either.
	if k == kindInvalid {
		return kindInvalid, nil
	}
	return k, g.checkerTypeArg(arg)
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

// solvedParamType is the checker's type solvedParamKind reads its kind
// off, at this instance (checkerTypeArg), or nil.
func (g *gen) solvedParamType(t *ast.Call, method string, i int) analysis.Type {
	sym := g.calleeRecord(t, method)
	if sym == nil {
		return nil
	}
	ft, _ := sym.CallType.(*analysis.FuncType)
	if ft == nil || i >= len(ft.Params) {
		return nil
	}
	return g.checkerTypeArg(ft.Params[i])
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
