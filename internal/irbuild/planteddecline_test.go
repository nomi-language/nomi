package irbuild

import "testing"

// plantDecline makes the builder decline every call to `planted_decline`
// until t ends (PlantDeclineForTest). A test about how a blocked program is
// reported blocks on that call, not on a real lowering gap, which a fix
// would close.
func plantDecline(t *testing.T) {
	t.Cleanup(PlantDeclineForTest("planted_decline", "a planted decline"))
}
