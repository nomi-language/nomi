package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// A module function's NAME used as a value — `Iter.map(double)`, `f = double`,
// `apply(double, 7)`, `Token.Computation(double)`.
//
// # What a reference lowers to
//
// A reference to a monomorphic module function is an `ir.RefFunc` naming the
// function's symbol (filefuncref.go), with no wrapper and no captures. Its kind
// is the function kind funcKind interns from the declaration's parameter and
// result kinds, the same kind a lambda of that signature has, so a named
// function and a lambda are interchangeable in every position:
// `apply(double, 7)` and `apply(fn x -> x * 2, 7)` pass the same kind at the
// same slot.
//
// Two shapes are not the plain reference:
//
//   - A function with defaulted parameters, named where the position's type
//     takes only its first parameters, is a function value that fills the
//     rest from their defaults (defaultFilledRef, ownerfuncref.go).
//   - A generic function. The checker instantiates the reference against the
//     function type its position expects and records the instantiated
//     signature on it (analysis/generic_func_ref.go); the value is that
//     instance, at the type arguments the checker recorded, as a call's is
//     (monoInstanceAt).
//
// A reference is not a dispatch. Nomi has no `.method()` postfix form: `x.field`
// is field access and a qualified call is `Type.method(x)`, so a name in value
// position has no receiver to bind. See methodref.go for the qualified
// spelling, which is the same value through a different resolver.

// checkedRefKinds is the signature the checker instantiated a generic function
// REFERENCE at (internal/analysis/generic_func_ref.go records it on the
// reference as a call's is recorded on its callee), in this gen's kinds.
// recorded is false when the checker recorded none of that arity: a reference
// that is not generic carries no CallType.
func (g *gen) checkedRefKinds(ref ast.Node, arity int) (params []kind, result kind, recorded bool) {
	if g.fa == nil {
		return nil, kindInvalid, false
	}
	line, col := calleeRefPos(ref)
	sym := g.srcFA().References[analysis.Pos{Line: line, Col: col}]
	if sym == nil {
		return nil, kindInvalid, false
	}
	ft, _ := sym.CallType.(*analysis.FuncType)
	if ft == nil || len(ft.Params) != arity {
		return nil, kindInvalid, false
	}
	params, result = g.funcTypeKinds(ft)
	return params, result, true
}

// funcTypeKinds is a checked function type's parameter and result kinds,
// each held in the value domain.
func (g *gen) funcTypeKinds(ft *analysis.FuncType) ([]kind, kind) {
	params := make([]kind, len(ft.Params))
	for i, pt := range ft.Params {
		params[i] = g.project(pt)
		// A Unit parameter (`Iter.each(units, io.inspect)`) is in the
		// domain, as irCallableValueKind admits it in a function kind.
		irHeldOperand(params[i], "a function reference's parameter")
	}
	result := g.project(ft.Return)
	irHeldOperand(result, "a function reference's result")
	return params, result
}
