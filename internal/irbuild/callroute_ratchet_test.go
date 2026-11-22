package irbuild

import (
	"testing"
)

// maxCallRouteSites is how many distinct call sites each route that
// decides by spelling or by an operand's kind (callRoutes) answers in the
// programs TestIRGolden_RecordsMatchTheVM runs, as calls written in the
// source, calls the front end writes for a derive, and calls the builder
// made. Each count only goes down: a call the record plans
// (recordedCallPlan, dispatchedCallPlan) needs none of them, and a route
// no call reaches is deleted. A class with no entry is held at 0, as every
// class is: qualImplPlan's routes 3 and 4, a rendering with no record and
// every stdPick choice. The counts leave out sites in std's files
// (noteCallRoute), and were the same with the artifact cache empty, warm
// and off.
var maxCallRouteSites = map[string]int{}

// callRouteRatchet holds the call route census to maxCallRouteSites: every
// route callRoutes holds, from every origin.
func callRouteRatchet() censusRatchet {
	r := censusRatchet{what: "call sites", held: map[string]bool{}, max: map[string]int{},
		fix: "plan the call from the function its record names (recordedCallPlan)"}
	for class := range maxCallRouteSites {
		// A maximum of a class no route holds is reported by
		// checkSiteCensus.
		r.max[class] = maxCallRouteSites[class]
	}
	for route, held := range callRoutes {
		if !held {
			continue
		}
		for _, how := range callOrigins {
			class := string(route) + how
			r.held[class] = true
			r.max[class] = maxCallRouteSites[class]
		}
	}
	return r
}

// checkCallRouteCensus holds the programs' call route census to
// callRouteRatchet and logs std's own lowerings', which are never held.
func checkCallRouteCensus(t *testing.T, program, std map[string][]string) {
	t.Helper()
	checkSiteCensus(t, callRouteRatchet(), program)
	checkSiteCensus(t, censusRatchet{what: "call sites in std's own lowerings"}, std)
}
