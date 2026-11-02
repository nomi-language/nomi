package irbuild

import "testing"

// TestDrillThroughStructVariant_Runs pins a std enum's struct-shaped variant
// imported by name (`import std/supervisors.Backoff.Exponential`) and built
// bare (`Exponential{}`), declared defaults filled as the qualified spelling
// fills them. The builder declined the bare head, since no type is named
// `Exponential`.
func TestDrillThroughStructVariant_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const want = "Exponential{max_restarts: 10, max_elapsed: 15m}\n" +
		"Exponential{max_restarts: 3, max_elapsed: 1m}\n" +
		"Exponential{max_restarts: 3, max_elapsed: 15m}\n" +
		"10 3\n"
	got := vmReference(fixture("drillthrough_struct_variant.nomi"))
	if got.stdout != want || got.exit != 0 || got.stderr != "" {
		t.Fatalf("want stdout=%q\ngot  %s", want, got)
	}
}
