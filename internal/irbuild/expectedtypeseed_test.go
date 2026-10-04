package irbuild

import "testing"

// A generic call seeded from its expected type runs on the VM
// (testdata/expected_type_seed.nomi), and its output matches the golden record.
//
// `Iter.to_map([(.North, .Cave)])` in a `Map<Direction, Place>` position
// resolves its dot variants only because the checker solves K and V from the
// expected type and checks the list's items against the parameter's
// `Iter<(Direction, Place)>`. `Iter.to_map([])` type-checks without the seed,
// but its K and V would stay inference variables at the call, so no
// instantiation would be recorded and the program would be BLOCKED.
func TestExpectedTypeSeed_RunsOnTheVM(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	path := fixture("expected_type_seed.nomi")
	const want = "Room{exits: {North => Cave, East => Hall}}\n" +
		"Index{words: {=>}}\n" +
		"{=>}\n" +
		"{North => Cave}\n" +
		"{North => Cave}\n" +
		"{=>}\n"
	got := vmReference(path)
	if got.stdout != want || got.stderr != "" || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s--- want ---\n%s",
			got.exit, got.stdout, got.stderr, want)
	}
	golden := goldenReference(t, path)
	if got.stdout != golden.stdout || got.exit != golden.exit {
		t.Errorf("VM and golden output differ:\nVM (exit %d):\n%s\ngolden (exit %d):\n%s",
			got.exit, got.stdout, golden.exit, golden.stdout)
	}
}
