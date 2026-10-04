//go:build rtmapcount

package rt

// The counting half of mapcount_off.go. Plain package variables with no
// synchronization: only TestMeasureMapCosts reads them, it does not run in
// parallel with anything, and no normal build contains this file.

const mapCounting = true

type mapCost struct{ nodes, probes int }

var mapCounted mapCost

func mapCountNode()  { mapCounted.nodes++ }
func mapCountProbe() { mapCounted.probes++ }

func mapCountReset()        { mapCounted = mapCost{} }
func mapCountRead() mapCost { return mapCounted }
