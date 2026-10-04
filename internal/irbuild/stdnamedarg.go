package irbuild

import (
	"sort"
)

// A NAMED ARGUMENT to a stdlib callee.
//
// # Why the std call arm needs its own placement
//
// `callArgs` calls `g.expr` on each written argument, and `g.expr` has no arm
// for `*ast.NamedArg`, so without this file a named argument to a std callee
// would come back invalid and `probe` would report the bare node type. A named
// argument to a USER function lowers through native.go's directCall:
//
//	fn f(a: Int, b: Int): Int          f(b: 1, 2)
//	fn f(a: Int, b: Int = 5): Int      f(1, b: 9)
//	calendar Time.new(hour, minute, second: Int = 0, nanosecond: Int = 0)
//	                                   Time.new(10, 30, 15)
//	                                   Time.new(10, 30, second: 15)
//
// The last two rows are the same std callee, one named argument apart, and
// this file is what places the second.
//
// # EVALUATION ORDER IS NOT SLOT ORDER AND NOT WRITTEN ORDER
//
// resolveArgs partitions the written arguments and drains positionals before
// named ones (see sugar.go's argSlotPlan header), so three orders are in
// play and no two coincide in general:
//
//	Supervisor.new(shutdown_timeout: D, max_running: 4)
//	  written    shutdown_timeout, max_running
//	  evaluation shutdown_timeout, max_running   (both named, written order)
//	  SLOT       max_running, shutdown_timeout   (std declares max_running first)
//
// So this cannot reorder the nodes and hand them to `callArgs`: that would emit
// each argument's statements in SLOT order and DIFF on any call whose named
// arguments are effectful. The loop below walks `plan.order` and writes each
// result into its slot, which is the arrangement native.go's directCall already
// uses for the user-function case and for the same reason.
//
// # Two restrictions, both refusals rather than guesses
//
//  1. CONTIGUITY, CONDITIONALLY. `stdlibInvoke` passes a dense
//     positional prefix and `stdCallArity` asks whether an arity-reduced wrapper
//     exists for that length, so the claimed slots must be exactly 0..n-1. Named
//     arguments can leave a HOLE — `Supervisor.new(restart: R, max_running: 1)`
//     claims slots 0 and 2 — and no ARITY spells that. The tour's
//     concurrency chapter writes exactly that hole.
//
//     A hole over a parameter with a DEFAULT has an answer that an index by
//     LENGTH cannot express, and it is the declaration's own default — filled by
//     a per-slot accessor emitted in the DECLARING module's gen, which is where
//     the arity wrappers already put such a fill and for the same reason. See
//     stdslotfill.go.
//
//     What still refuses is a hole with no default behind it, and an overload set
//     (restriction 2 applies to the FILL as well as to the placement: whose slot
//     1 default is not a question a placement can answer).
//
//  2. ONE PLACEMENT ACROSS THE OVERLOAD SET. A spelling may name several std
//     declarations (`NaiveDateTime.add` is eleven `impl Add<X, NaiveDateTime>`
//     blocks) and `stdPick` selects on the argument KINDS — which cannot be
//     computed until the arguments are placed, which needs the names, which
//     differ per candidate. Rather than break that cycle by guessing, this
//     requires every candidate to yield the SAME placement and refuses when they
//     disagree. A set of one satisfies it trivially.

// stdParamNames is one std declaration's parameter names, positionally, for
// argSlotPlan's name table.
//
// Reads the FIELD and not `f.decl`, because `decl` is a `*ast.FuncDef` and is
// nil for every `host fn`, such as `supervisors.Supervisor.new`.
//
// Answers nil for a nil function, and argSlotPlan's own contract covers a nil or
// short table: it declines to place a named argument rather than guessing, and
// positional behaviour is unaffected.
func stdParamNames(f *stdFunc) []string {
	if f == nil {
		return nil
	}
	return f.paramNames
}

// contiguousSlots reports whether the plan claims exactly slots 0..n-1, and
// names the lowest unclaimed slot below the highest claimed one when it does
// not.
func contiguousSlots(plan argPlan) (int, bool) {
	if len(plan.slots) == 0 {
		return 0, true
	}
	sorted := make([]int, len(plan.slots))
	copy(sorted, plan.slots)
	sort.Ints(sorted)
	for i, s := range sorted {
		if s != i {
			return i, false
		}
	}
	return 0, true
}
