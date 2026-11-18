//go:build !analysisleavescount

package analysis

// leaves's work counter, compiled to nothing, as rt's map counters are
// (rt/mapcount_off.go). leavescount_on.go is the counting half, built with
// `-tags analysisleavescount`; TestLeaves_CostGrowsWithTheSquareOfTheDepth
// re-runs itself under that tag, so a plain `go test` still checks the
// count.

const leavesCounting = false

func leavesCount() {}

func leavesCountReset(budget int) {}
func leavesCountRead() int        { return 0 }
