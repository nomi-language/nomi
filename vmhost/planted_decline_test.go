package vmhost

import (
	"testing"

	"github.com/nomi-language/nomi/internal/irbuild"
)

// plantDecline makes the builder decline every call to `planted_decline`
// until t ends (irbuild.PlantDeclineForTest). A test about how a blocked
// program is reported blocks on that call, not on a real lowering gap,
// which a fix would close.
func plantDecline(t *testing.T) {
	t.Helper()
	t.Cleanup(irbuild.PlantDeclineForTest("planted_decline", plantedReason))
}

// plantedReason is the builder's reason for a planted decline.
const plantedReason = "a planted decline"
