package irbuild

import (
	"os"
	"strings"
	"testing"
)

// Default parameter values, destructuring `fn` parameters, and argument slot
// placement.
//
// A fixture's output is held to its golden record, and a literal `want` says
// what that record must SAY, because a recording can be wrong.

// TestDefaults_CalleeScopeIsLoadBearing states which line of the fixture is the
// scope guard, and what breaking it looks like.
//
// A default is evaluated in the callee's environment, so `offset`'s `base()`
// reaches the module function even though the caller has an Int local of that
// name. callDefaultsThrough swaps the scope stack for that. Without the swap
// `base` would resolve to the caller's Int, which is why this guard is stated
// rather than left to an output comparison.
func TestDefaults_CalleeScopeIsLoadBearing(t *testing.T) {
	src := readFixture(t, "defaults.nomi")
	for _, needle := range []string{"b: Int = base()", "base = 1"} {
		if !strings.Contains(src, needle) {
			t.Fatalf("the callee-scope guard %q is gone from defaults.nomi; a default\n"+
				"resolved in the caller's scope would now lower", needle)
		}
	}
}

// readFixture is a fixture's own source, for the guards that assert a
// load-bearing LINE is still in it. A fixture whose crucial case somebody
// simplified away passes every differential comparison it has left.
func readFixture(t *testing.T, name string) string {
	t.Helper()
	src, err := os.ReadFile(fixture(name))
	if err != nil {
		t.Fatal(err)
	}
	return string(src)
}
