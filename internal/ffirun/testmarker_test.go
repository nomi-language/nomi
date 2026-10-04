package ffirun

import "testing"

// The wrapper's test mode reports `<marker> passed failed blocked`, which
// strips from the visible output and crosses as counts.
func TestParseTestOutput_ReadsTheMarker(t *testing.T) {
	passed, failed, blocked, visible, ok := parseTestOutput("BLOCKED b [f] x\n" + ffiTestResultMarker + " 2 0 1\n")
	if !ok || passed != 2 || failed != 0 || blocked != 1 || visible != "BLOCKED b [f] x\n" {
		t.Fatalf("marker: %d %d %d %q %v", passed, failed, blocked, visible, ok)
	}
	if _, _, _, _, ok = parseTestOutput(ffiTestResultMarker + " 2 x 1\n"); ok {
		t.Fatal("a malformed marker was accepted")
	}
	if _, _, _, _, ok = parseTestOutput(ffiTestResultMarker + " 3 1\n"); ok {
		t.Fatal("a marker without the blocked count was accepted")
	}
}
