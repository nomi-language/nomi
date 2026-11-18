package irbuild

import (
	"sort"
)

// A NAMED ARGUMENT to a stdlib callee.
//
// # Placement
//
// A named argument to a std callee is placed as the checker recorded
// (callPlan):
//
//	calendar Time.new(hour, minute, second: Int = 0, nanosecond: Int = 0)
//	                                   Time.new(10, 30, 15)
//	                                   Time.new(10, 30, second: 15)
//
// The two rows are the same std callee, one named argument apart.
//
// # Evaluation order is written order, not slot order
//
// Arguments are evaluated as written (spec §8 *Evaluation order*; callPlan's
// order), which is not the order of their slots:
//
//	Supervisor.new(shutdown_timeout: D, max_running: 4)
//	  written    shutdown_timeout, max_running
//	  evaluation shutdown_timeout, max_running
//	  SLOT       max_running, shutdown_timeout   (std declares max_running first)
//
// So a caller cannot lower the arguments in slot order: that would differ on
// any call whose named arguments are effectful. It walks `plan.order` and
// writes each result into its slot.
//
// # Two restrictions, both refusals rather than guesses
//
//  1. A HOLE NEEDS A DEFAULT. Named arguments can leave a HOLE —
//     `Supervisor.new(restart: R, max_running: 1)` claims slots 0 and 2 — and
//     the tour's concurrency chapter writes exactly that hole. A hole over a
//     parameter with a DEFAULT is filled as any omitted slot is, by a per-slot
//     accessor built in the DECLARING module's gen. See stdslotfill.go.
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
