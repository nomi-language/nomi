//go:build !rtvectorcount

package rt

// The persistent vector's work counters, compiled to nothing, as
// mapcount_off.go's are: the bodies are empty and the calls inline away.
// vectorcount_on.go is the counting half, built with `-tags rtvectorcount`.
// TestMeasureVectorCosts re-runs itself under that tag, so a plain `go test`
// still checks the counts.

const vecCounting = false

// vecCost is what one operation did: trie nodes built, elements or child
// pointers copied, and nodes visited on a read.
type vecCost struct{ nodes, copies, visits int }

func vecCountNode()         {}
func vecCountCopy(int)      {}
func vecCountVisit()        {}
func vecCountReset()        {}
func vecCountRead() vecCost { return vecCost{} }
