package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// A module function's NAME used as a value — `Iter.map(double)`, `f = double`,
// `apply(double, 7)`, `Token.Computation(double)`.
//
// # Why the key is split
//
// A function reference covers three different situations, and only one of
// them is a representation gap this builder can close. The other two are
// walls, and a wall reported under the same name as a gap is a row nobody can
// size: clearing the gap empties most of the row and leaves the rest looking
// like a regression at whatever position happens to survive.
//
// So each wall has its own key and states what it is a wall against:
//
//   - `function reference with a defaulted parameter`. The answer is
//     not what a Go programmer would guess: a Nomi function value carries its DEFAULTS and
//     its slot-routing rule with it. `g = greet; g()` prints `hello, world` for
//     `fn greet(name: String = "world")`, and `s = scale; s(5, double)` routes
//     the trailing named function to the last slot and defaults `factor`,
//     exactly as the direct call does. A Go func value has neither: its arity is
//     fixed and it has no slots. So the Nomi type `(String) -> String` is not
//     the whole of what the value is, and handing out the Go function would be a
//     value that answers `g()` with a compile error where the program
//     answers `hello, world`. Closing this needs a representation that carries
//     the defaults, which is a decision, not an omission.
//   - A generic function is not a wall. The checker instantiates the
//     reference against the function type its position expects and records
//     the instantiated signature on it (analysis/generic_func_ref.go); the
//     value is that instance, built by resolveMonoInstance as a call's is
//     (funcRefInstance below).
//
// # What DOES lower, and why it costs nothing
//
// A lowered module function is `func <name>(fr *rt.Frame, p0 T0, …) R`, and
// funcKind renders a Nomi function type as `func(*rt.Frame, T0, …) R`. Those are
// the SAME Go type, written by two places that derive it from the same kinds. So
// a reference to a monomorphic module function is its Go name and nothing else —
// no wrapper, no closure, no allocation. The frame threads through exactly as it
// does for a lambda, which is what makes a named function and a lambda
// interchangeable in every position: `apply(double, 7)` and `apply(|x| x * 2, 7)`
// produce the same Go type at the same slot.
//
// That equivalence is also the argument that this is not a dispatch. Nomi has no
// `.method()` postfix form — `x.field` is field access and a qualified call is
// `Type.method(x)` — so a name in value position never had a receiver to bind,
// and turning one into a dispatch would be inventing a construct the language
// does not have. See methodref.go for the qualified spelling, which is the same
// value through a different resolver.

// funcHasDefault reports whether any parameter carries a default value.
//
// Read off the DECLARATION rather than the signature, because fnSig records
// parameter KINDS and a default is an expression: sugar.go evaluates it at the
// call site, in the callee's scope, so nothing about it survives into the kind.
func funcHasDefault(fd *ast.FuncDef) bool {
	if fd == nil {
		return false
	}
	for _, p := range fd.Params {
		if p.Default != nil {
			return true
		}
	}
	return false
}

// checkedRefKinds is the signature the checker instantiated a generic function
// REFERENCE at (generic_func_ref.go records it on the reference as a call's is
// recorded on its callee), in this gen's kinds. ok is false when the checker
// recorded none or a kind is outside the value domain.
func (g *gen) checkedRefKinds(ref ast.Node, arity int) ([]kind, kind, bool) {
	if g.fa == nil {
		return nil, kindInvalid, false
	}
	line, col := calleeRefPos(ref)
	sym := g.fa.References[analysis.Pos{Line: line, Col: col}]
	if sym == nil {
		return nil, kindInvalid, false
	}
	ft, _ := sym.CallType.(*analysis.FuncType)
	return g.funcTypeKinds(ft, arity)
}

// funcTypeKinds is a checked function type's parameter and result kinds, ok
// false unless it has arity parameters and every kind is in the value domain.
func (g *gen) funcTypeKinds(ft *analysis.FuncType, arity int) ([]kind, kind, bool) {
	if ft == nil || len(ft.Params) != arity || irUnsolvedType(ft.Return) {
		return nil, kindInvalid, false
	}
	params := make([]kind, arity)
	for i, pt := range ft.Params {
		if irUnsolvedType(pt) {
			return nil, kindInvalid, false
		}
		params[i] = g.project(pt)
		if !irCallableValueKind(params[i]) {
			return nil, kindInvalid, false
		}
	}
	result := g.project(ft.Return)
	if result != kindUnit && !irCallableValueKind(result) {
		return nil, kindInvalid, false
	}
	return params, result, true
}

// funcRefInstance is the instance of this gen's generic template tpl that the
// reference ref names, solved by monoSolve from the checker's instantiation
// exactly as a direct call's is.
func (g *gen) funcRefInstance(ref ast.Node, tpl *monoTemplate, name string) (*monoInst, bool) {
	params, result, ok := g.checkedRefKinds(ref, len(tpl.decl.Params))
	if !ok {
		return nil, false
	}
	args, ok := g.monoSolve(tpl, params, result)
	if !ok {
		return nil, false
	}
	inst, why, _ := g.resolveMonoInstance(tpl, args, name)
	if why != "" || inst == nil {
		return nil, false
	}
	return inst, true
}
