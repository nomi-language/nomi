package irbuild

import (
	"testing"
)

// TestIoInspect_DeclarationOrderIsNotSortedOrder is the anti-vacuity guard on
// io_inspect_surfaces.nomi's structs, the same one dbg_test.go carries for its
// own fixture and for the same reason: alphabetical fields make declaration
// order and sorted order one string, and the assertions above would then pass
// with `io.inspect` wired to the wrong renderer.
func TestIoInspect_DeclarationOrderIsNotSortedOrder(t *testing.T) {
	src := mustReadFixture(t, "io_inspect_surfaces.nomi")
	for _, name := range []string{"Rev", "Custom"} {
		fields := structFieldOrder(src, name)
		if len(fields) < 2 {
			t.Fatalf("struct %s in io_inspect_surfaces.nomi no longer has two fields to order", name)
		}
		if sortedStrings(fields) {
			t.Fatalf("struct %s now declares its fields alphabetically (%v), so "+
				"declaration order and sorted order are the same string and every "+
				"rendering assertion over it is vacuous", name, fields)
		}
	}
}
