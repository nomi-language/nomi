//go:build !rtmapcount

package rt

// MapLookup's work counters, compiled to nothing. Every artifact links rt, so
// get pays for counting only in a build that asks for it: these bodies are
// empty, the calls inline away, and the loop is the uncounted one.
//
// A nil-guarded package counter and a counted helper that MapLookup wraps were
// both measured first. Each cost 0.6-1 ns on a 4.4 ns get over a 16-entry map.
// The build tag is the only form that costs nothing.
//
// mapcount_on.go is the counting half, built with `-tags rtmapcount`.
// TestMeasureMapCosts re-runs itself under that tag, so a plain `go test`
// still checks the counts.

const mapCounting = false

func mapCountNode()  {}
func mapCountProbe() {}

type mapCost struct{ nodes, probes int }

func mapCountReset()        {}
func mapCountRead() mapCost { return mapCost{} }
