package vmhost

import (
	"strings"
	"testing"
)

// A deferred call that traps while an earlier trap unwinds does not hide it:
// the run ends with the first trap, then the cleanup's, as the program's own
// failure. It used to end as a machine limit ("the VM cannot run this
// program"), since joining the two dropped the first one's kind.
func TestRun_ACleanupTrapDuringATrapReportsBoth(t *testing.T) {
	o := runInput(t.TempDir(), `fn boom(n: Int) {
    dbg 10 / n
}

fn main() {
    zero = 0
    defer boom(zero)
    dbg 1 / zero
}
`)
	if o.class != runTrap {
		t.Fatalf("the run is %v (%s), not the program's own failure:\n%s", o.class, o.crash, o.transcript)
	}
	want := "line 8: division by zero; cleanup error: line 7: deferred cleanup failed: line 2: division by zero"
	if !strings.Contains(o.transcript, want) {
		t.Fatalf("the run reports\n%s\nwant it to hold %q", o.transcript, want)
	}
}
