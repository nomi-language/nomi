package analysis

import "github.com/nomi-language/nomi/internal/ast"

// ResolvedCall is what the checker decided about one call
// (FileAnalysis.Calls): which argument fills which of the callee's
// parameters, and in what order the arguments are evaluated. A plain call,
// a pipe stage (a call with the piped value placed first) and a partial
// application (a call with holes) each have one, made by the one function
// that places arguments (placeArgs), so the IR builder places nothing
// itself. It says what the call means and nothing of how a machine holds
// its values.
type ResolvedCall struct {
	// Callee is what the call calls, classified (callee.go).
	Callee Callee
	// Slots holds, for each of the callee's parameters in order, what
	// fills it.
	Slots []CallSlot
	// Order is the call's written arguments, as indices into its Args, in
	// the order they are evaluated: the order they are written (spec §8
	// *Evaluation order*). A callee that is an expression is evaluated
	// before them, and a pipe's head after the callee and before them. A
	// default is evaluated after every written argument, in parameter
	// order.
	Order []int
	// Piped marks a pipe stage, whose head fills the slot marked
	// SlotPiped.
	Piped bool
}

// SlotFill is what fills one parameter of a call.
type SlotFill uint8

const (
	// SlotUnfilled is a parameter of a call the checker reported, which
	// nothing fills.
	SlotUnfilled SlotFill = iota
	// SlotArg is filled by the written argument Args[Arg], a named
	// argument by its value.
	SlotArg
	// SlotPiped is filled by the pipe's head.
	SlotPiped
	// SlotDefault is filled by the parameter's default: a declaration's,
	// which FileAnalysis.CallDefaults names, or the default of the lambda
	// or partial the callee is.
	SlotDefault
	// SlotHole is left open by the `&N` written at Args[Arg]: the call is
	// a partial application, and the function it makes takes the
	// parameter.
	SlotHole
	// SlotNone is the `from` of a stateless `Iter.loop(fn -> ...)`, whose
	// state is Unit: the call leaves it out and nothing fills it.
	SlotNone
)

// CallSlot is what fills one parameter of a call.
type CallSlot struct {
	Fill SlotFill
	// Arg is the index of the written argument, for SlotArg and SlotHole.
	Arg int
}

// ArgSlots is the parameter each written argument of the call fills, by
// its index in the call's Args, or -1 for one that fills none.
func (rc *ResolvedCall) ArgSlots(args int) []int {
	out := make([]int, args)
	for i := range out {
		out[i] = -1
	}
	for slot, s := range rc.Slots {
		if (s.Fill == SlotArg || s.Fill == SlotHole) && s.Arg < args {
			out[s.Arg] = slot
		}
	}
	return out
}

// placeArgs is the parameter each written argument of a call fills, -1 for
// one that names no parameter (spec §7 *Lambdas at Call Sites*). paramNames
// are the callee's parameter names, nil when it has no declaration behind
// it, and piped is how many leading parameters a pipe's head fills (0 or
// 1). It is the one place arguments are routed: a direct call, a pipe
// stage and a partial application all go through it.
//
//   - A named argument fills the parameter it names.
//   - Positional arguments fill the parameters left to right, after the
//     piped one. One written after a named argument skips the parameters
//     names claimed; one written before means the slot it sits in, so
//     `add(1, a: 2)` gives `a` two values.
//   - A final positional lambda, field accessor or capture goes to the
//     last parameter when no name claims it, and so does a function
//     passed by name when the last parameter takes a function and the
//     slot the name would otherwise fill does not (routesAsTrailingFunc).
func placeArgs(args []ast.Node, piped int, paramNames []string, params []Type) []int {
	out := make([]int, len(args))
	claimed := make([]bool, len(params))
	for i, arg := range args {
		out[i] = -1
		na, ok := arg.(*ast.NamedArg)
		if !ok {
			continue
		}
		if slot := namedArgSlot(na.Name, paramNames); slot >= 0 && slot < len(params) {
			out[i] = slot
			claimed[slot] = true
		}
	}
	next := piped
	last := -1
	seenNamed := false
	for i, arg := range args {
		if _, isNamed := arg.(*ast.NamedArg); isNamed {
			seenNamed = true
			continue
		}
		if seenNamed {
			for next < len(params) && claimed[next] {
				next++
			}
		}
		out[i] = next
		next++
		last = i
	}
	if last < 0 || len(params) == 0 {
		return out
	}
	lastParam := len(params) - 1
	if claimed[lastParam] || out[last] == lastParam {
		return out
	}
	if isLambdaArg(args[last]) || routesAsTrailingFunc(args[last], params, out[last]) {
		out[last] = lastParam
	}
	return out
}

// positionalSlots is placeArgs with a named argument's entry -1, for the
// callers that route a named argument by its name themselves.
func positionalSlots(args []ast.Node, piped int, paramNames []string, params []Type) []int {
	out := placeArgs(args, piped, paramNames, params)
	for i, arg := range args {
		if _, isNamed := arg.(*ast.NamedArg); isNamed {
			out[i] = -1
		}
	}
	return out
}

// recordPlacement records how call's written arguments fill the callee's
// parameters (FileAnalysis.Calls), and what fills the parameters they
// leave to their defaults (FileAnalysis.CallDefaults). slots is their
// routing (positionalSlots, moved by routeAroundSeed for a stateless loop),
// piped whether a pipe's head fills the first parameter, and params the
// callee's parameter types. A callee with no function in view behind it
// has no defaults (calleeOf).
func (c *checker) recordPlacement(call *ast.Call, args []ast.Node, slots []int, piped bool, params []Type) {
	if c.fa == nil || call == nil {
		return
	}
	names := c.callDefParams(call.Func)
	rc := &ResolvedCall{Slots: make([]CallSlot, len(params)), Order: make([]int, len(args)), Piped: piped}
	if piped && len(params) > 0 {
		rc.Slots[0] = CallSlot{Fill: SlotPiped}
	}
	for i, arg := range args {
		rc.Order[i] = i
		slot := -1
		if na, ok := arg.(*ast.NamedArg); ok {
			slot = namedArgSlot(na.Name, names)
		} else if i < len(slots) {
			slot = slots[i]
		}
		if slot < 0 || slot >= len(params) || rc.Slots[slot].Fill != SlotUnfilled {
			continue
		}
		fill := SlotArg
		if isPlaceholderArg(arg) {
			fill = SlotHole
		}
		rc.Slots[slot] = CallSlot{Fill: fill, Arg: i}
	}
	if seed := c.statelessLoopSlot(call); seed >= 0 && seed < len(params) && rc.Slots[seed].Fill == SlotUnfilled {
		rc.Slots[seed].Fill = SlotNone
	}
	c.recordCall(call, rc)
	ci := c.calleeOf(call.Func)
	if len(ci.params) != len(params) {
		return
	}
	var omitted []int
	for slot := range rc.Slots {
		if rc.Slots[slot].Fill == SlotUnfilled && ci.params[slot].Default != nil {
			rc.Slots[slot].Fill = SlotDefault
			omitted = append(omitted, slot)
		}
	}
	// A lambda or a partial bound to a name fills its own defaults; a
	// declaration's are filled from the declaration.
	if len(omitted) == 0 || !ci.declared {
		return
	}
	if c.fa.CallDefaults == nil {
		c.fa.CallDefaults = map[*ast.Call]*DefaultedCall{}
	}
	c.fa.CallDefaults[call] = &DefaultedCall{Decl: ci.sym.Node, Params: ci.params, File: ci.sym.SourceFile, Slots: omitted, From: sameFrom(ci.sym.Node, len(omitted)), Ref: ci.copyOf}
}

// recordCall records rc as the record of n, a call this check checked,
// which settleCallees classifies once the check's bodies are checked.
func (c *checker) recordCall(n ast.Node, rc *ResolvedCall) {
	if c.fa.Calls == nil {
		c.fa.Calls = map[ast.Node]*ResolvedCall{}
	}
	c.fa.Calls[n] = rc
	c.recordedCalls = append(c.recordedCalls, n)
}

// PositionalCall is the record of a call that passes the first args of its
// callee's params parameters in order and leaves the rest to their
// defaults: the call a function value makes of the function it stands for.
func PositionalCall(args, params int) *ResolvedCall {
	rc := &ResolvedCall{Slots: make([]CallSlot, params), Order: make([]int, args)}
	for i := range rc.Slots {
		if i < args {
			rc.Slots[i] = CallSlot{Fill: SlotArg, Arg: i}
			rc.Order[i] = i
		} else {
			rc.Slots[i].Fill = SlotDefault
		}
	}
	return rc
}

// recordStageCall records the call a `then` or `tap` stage makes: its
// function, with the piped value as the one argument.
func (c *checker) recordStageCall(stage ast.Node) {
	c.recordCall(stage, &ResolvedCall{Slots: []CallSlot{{Fill: SlotPiped}}, Piped: true})
}

// recordPositional records the placement of call, a form whose arguments
// are its parameters in the order written, after a pipe's head, with no
// names and no defaults: a constructor (`Id(42)`, `Pair(1, "a")`,
// `Point({x: 1})`, `Shape.Rect({...})`), an operator function
// (`Add.add(a, b)`) or a keyword form written as a call
// (`testing.check(c)`). Each such form records it where the form is
// checked. It goes through placeArgs as every call does, so a named
// argument there is placed nowhere.
func (c *checker) recordPositional(call *ast.Call, piped bool) {
	if c.fa == nil || call == nil {
		return
	}
	shift := 0
	if piped {
		shift = 1
	}
	params := make([]Type, len(call.Args)+shift)
	rc := &ResolvedCall{Slots: make([]CallSlot, len(params)), Order: make([]int, len(call.Args)), Piped: piped}
	if piped {
		rc.Slots[0] = CallSlot{Fill: SlotPiped}
	}
	for i, slot := range placeArgs(call.Args, shift, nil, params) {
		rc.Order[i] = i
		if slot < 0 {
			continue
		}
		fill := SlotArg
		if isPlaceholderArg(call.Args[i]) {
			fill = SlotHole
		}
		rc.Slots[slot] = CallSlot{Fill: fill, Arg: i}
	}
	c.recordCall(call, rc)
}
