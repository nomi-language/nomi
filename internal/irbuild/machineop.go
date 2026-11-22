package irbuild

import "github.com/nomi-language/nomi/internal/analysis"

// A std function whose every call the builder lowers to an operation of the
// machine, not to a call of its body: an Iter operation (iterCall: an
// `ir.Iter`, or a constructor such as `Iter.from`), a Range function
// (rangeCall), a Set function the set intrinsics compute (setCallPlan), and
// a Set's `impl Iter` member, which iterCall drives over the Set's view.
// Such a function's body reads a representation the machine does not have
// (`Seq{run: ...}`, a Set's `items` map), so no program runs it and no
// abstract body of it lowers.
//
// What such a function does is what its operation does: it computes, and it
// calls the functions it is passed (an Iter stage's callback), with no
// effect of its own. A call that passes a function that acts is a call that
// can act (vm.EffectWalk.ActCalls counts an `ir.Iter` operand that holds
// one), so the function acts only on what it is passed.
// vmhost.Program.StdEffects answers it that way (StdLowersToMachine).

// StdLowersToMachine reports whether the builder lowers every call of the
// std function id to an operation of the machine rather than a call of its
// body: by the route typeQualCall takes for what the call is qualified with
// (stdQualRoutes), and within an owner-wide route, by the arm's own table.
// Such a function has no effect of its own and calls only the functions it
// is passed.
func StdLowersToMachine(id analysis.StdFunc) bool {
	if id.IsZero() || id.Owner == "" {
		return false
	}
	route, _ := stdQualRouteOf(analysis.Qualifier{Std: analysis.StdType{Module: id.Module, Name: id.Owner}}, id.Name)
	switch route {
	case routeIterSeeded, routeIterLoop:
		return true
	case routeIter:
		return irIterMachineOp(id.Name)
	case routeRange:
		// rangeCall lowers every function std declares on a Range.
		return true
	case routeSet:
		_, intrinsic := setFuncs[id.Name]
		return intrinsic || stdIterMember(id)
	}
	return false
}

// irIterMachineOp reports an Iter function iterCall lowers itself: an
// operation over a source's view (irIterOwnArity) or a constructor.
func irIterMachineOp(method string) bool {
	_, own := irIterOwnArity(method)
	return own || method == "from" || method == "iterate" || method == "repeat"
}

// stdIterMember reports whether id is a function of a std `impl Iter for`
// block. The checker files a std impl's function under the interface its
// block implements as its header names it (analysis.StdFunc.Impl), and in
// std `Iter` is std/iter's.
func stdIterMember(id analysis.StdFunc) bool {
	return id.Impl == stdIter.Name
}
