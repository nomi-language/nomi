package irbuild

import "testing"

// TestEnumNominalListPayload_Runs pins a user enum whose payload is a List of
// a user struct: positional, as a struct-shaped variant's field, and reaching
// the enum back through the list. A struct field of the same type ran; the
// enum was declined at its constructor.
func TestEnumNominalListPayload_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const want = "4 3 0\n" +
		"Poly([Point{x: 1, y: 2}, Point{x: 3, y: 4}])\n" +
		"Path{points: [Point{x: 5, y: 6}], name: \"ab\"}\n" +
		"True\n" +
		"False\n" +
		"7\n" +
		"Branch([Grove{trees: [Leaf(1), Leaf(2)]}, Grove{trees: [Branch([Grove{trees: [Leaf(4)]}])]}])\n"
	got := vmReference(fixture("enum_nominal_list_payload.nomi"))
	if got.stdout != want || got.exit != 0 || got.stderr != "" {
		t.Fatalf("want stdout=%q\ngot  %s", want, got)
	}
}
