//go:build rtvectorcount

package rt

// The counting half of vectorcount_off.go. Plain package variables with no
// synchronization: only TestMeasureVectorCosts reads them, it does not run in
// parallel with anything, and no normal build contains this file.

const vecCounting = true

type vecCost struct{ nodes, copies, visits int }

var vecCounted vecCost

func vecCountNode()         { vecCounted.nodes++ }
func vecCountCopy(n int)    { vecCounted.copies += n }
func vecCountVisit()        { vecCounted.visits++ }
func vecCountReset()        { vecCounted = vecCost{} }
func vecCountRead() vecCost { return vecCounted }
