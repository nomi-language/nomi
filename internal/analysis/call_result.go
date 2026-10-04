package analysis

import "github.com/nomi-language/nomi/internal/ast"

// CallShape is a call written in a tree the checker has not typed: the
// editor's reparse of a statement that does not parse yet.
type CallShape struct {
	// Args are the call's written arguments.
	Args []ast.Node
	// ParamNames are the callee's parameter names, for routing named
	// arguments; nil when the callee's declaration is unknown.
	ParamNames []string
	// Piped marks a pipe stage, whose piped value fills the first
	// parameter; PipedType is that value's type, nil when unknown.
	Piped     bool
	PipedType Type
	// ArgType answers an argument expression's type, nil when unknown.
	ArgType func(ast.Node) Type
}

// CallResultType is the result type of a call to a function of type ft,
// with the callee's type parameters solved from the arguments' types as
// checkGenericCall and checkPipe solve them: each argument is routed to its
// parameter (computePositionalSlots, computePipeArgSlots) and its type is
// unified against the parameter's under the file's impl tables, so a List
// argument solves an `Iter<T>` parameter.
//
// It answers nil rather than a guess: when an argument does not unify, when
// a type parameter the result names stays unsolved (an argument whose type
// is unknown, such as a lambda whose parameter types need inference), when
// the call is a partial application (`_`), and when an argument's type
// holds an unsolved inference variable, which unification would bind in
// the analysis it was read from.
func (fa *FileAnalysis) CallResultType(ft *FuncType, call CallShape) Type {
	if ft == nil {
		return nil
	}
	if !ContainsTypeParam(ft.Return) {
		return ft.Return
	}
	subs, _, ok := fa.solveCallShape(ft, call, -1)
	if !ok {
		return nil
	}
	result := Substitute(ft.Return, subs)
	for _, tp := range collectOrderedTypeParams(&FuncType{Return: result}) {
		if calleeOwns(ft, tp) {
			return nil
		}
	}
	return result
}

// CallArgParamType is the type of the parameter that argument argIndex of a
// call to ft fills, with the callee's type parameters substituted as far as
// the call's other arguments and the piped value solve them. The argument
// itself is not read. The editor asks it what a field accessor being typed
// reads from: in `Iter.map(users, .na)` the accessor fills `(T) -> U`, and
// `users` solves T. Nil when the argument fills no parameter or the other
// arguments do not unify.
func (fa *FileAnalysis) CallArgParamType(ft *FuncType, call CallShape, argIndex int) Type {
	if ft == nil || argIndex < 0 || argIndex >= len(call.Args) {
		return nil
	}
	subs, slots, ok := fa.solveCallShape(ft, call, argIndex)
	if !ok {
		return nil
	}
	slot := -1
	if na, isNamed := call.Args[argIndex].(*ast.NamedArg); isNamed {
		slot = namedArgSlot(na.Name, call.ParamNames)
	} else if argIndex < len(slots) {
		slot = slots[argIndex]
	}
	if slot < 0 || slot >= len(ft.Params) {
		return nil
	}
	return Substitute(ft.Params[slot], subs)
}

// solveCallShape solves ft's type parameters from a call's arguments as
// CallResultType describes, skipping argument skip (-1 skips none), and
// returns the substitution with each written argument's parameter slot.
func (fa *FileAnalysis) solveCallShape(ft *FuncType, call CallShape, skip int) (map[*TypeParam_]Type, []int, bool) {
	if callHasPlaceholder(call.Args) || containsTypeVar(ft) {
		return nil, nil, false
	}
	subs := map[*TypeParam_]Type{}
	impls, implTypeArgs := fa.implsContext(), fa.implTypeArgsContext()
	bind := func(slot int, argTy Type) bool {
		if slot < 0 || slot >= len(ft.Params) || argTy == nil {
			return true
		}
		argTy = ResolveTypeVar(argTy)
		if containsTypeVar(argTy) {
			return false
		}
		argTy = coerceMapToList(argTy, ft.Params[slot])
		argTy = coerceRangeToList(argTy, ft.Params[slot])
		return UnifyWithImpls(ft.Params[slot], argTy, subs, impls, implTypeArgs) == nil
	}
	var slots []int
	if call.Piped {
		if !bind(0, call.PipedType) {
			return nil, nil, false
		}
		slots = computePipeArgSlots(call.Args, 1, call.ParamNames, ft.Params)
	} else {
		slots = computePositionalSlots(call.Args, ft.Params, call.ParamNames)
	}
	for i, arg := range call.Args {
		if i == skip {
			continue
		}
		slot := -1
		if na, ok := arg.(*ast.NamedArg); ok {
			slot = namedArgSlot(na.Name, call.ParamNames)
			arg = na.Value
		} else if i < len(slots) {
			slot = slots[i]
		}
		if slot < 0 {
			continue
		}
		var argTy Type
		if call.ArgType != nil {
			argTy = call.ArgType(arg)
		}
		if !bind(slot, argTy) {
			return nil, nil, false
		}
	}
	return subs, slots, true
}

// calleeOwns reports whether tp is one of the type parameters ft's
// signature names, as opposed to one of the caller's that an argument
// carried in.
func calleeOwns(ft *FuncType, tp *TypeParam_) bool {
	for _, own := range collectOrderedTypeParams(ft) {
		if own == tp {
			return true
		}
	}
	return false
}
