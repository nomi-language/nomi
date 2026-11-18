//go:build analysisleavescount

package analysis

import "fmt"

// The counting half of leavescount_off.go. Plain package variables with no
// synchronization: only TestLeaves_CostGrowsWithTheSquareOfTheDepth reads
// them, the build that holds this file runs that test alone, and no normal
// build contains this file.

const leavesCounting = true

var leavesAsked, leavesBudget int

// leavesCount counts one question put to leaves, and panics past the
// budget the test set, so that work that doubles with each level of
// nesting fails the test at once and never runs to its end.
func leavesCount() {
	leavesAsked++
	if leavesBudget > 0 && leavesAsked > leavesBudget {
		panic(fmt.Sprintf("leaves was asked more than %d times", leavesBudget))
	}
}

func leavesCountReset(budget int) { leavesAsked, leavesBudget = 0, budget }
func leavesCountRead() int        { return leavesAsked }
