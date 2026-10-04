package irbuild

import (
	"testing"
)

// TestPinned_OwnerOnceReads runs owner-level `once` bindings (`impl Policy {
// pub once default: Policy = ... }`, read as `Policy.default`) on every
// spelling of the owner: in the declaring file, file-qualified from another
// file (`limits.Limits.cap`), and through a selective type import
// (`Shade.name`). The owners are a struct, an enum and a generic struct; one
// binding is private and read in its own file, and two are built from another
// owner-level once.
//
// The `forcing ...` lines are the initializers' side effects. Each appears
// once, and only at the first read: `Policy.default` is first forced by two
// tasks at once and still prints one line, and `Box.size` prints after `box
// not read yet`, which is laziness.
func TestPinned_OwnerOnceReads(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const want = "" +
		"start\n" +
		"forcing Policy.default\n" +
		"6\n" +
		"3\n" +
		"6\n" +
		"5\n" +
		"slow\n" +
		"mode\n" +
		"box not read yet\n" +
		"forcing Box.size\n" +
		"7\n" +
		"7\n" +
		"forcing Limits.cap\n" +
		"42\n" +
		"84\n" +
		"shade\n" +
		"shade\n"
	got := vmReference(fixture("owner_once/main.nomi"))
	if got.stdout != want || got.exit != 0 || got.stderr != "" {
		t.Fatalf("owner-level once reads are not what this pins:\nwant stdout=%q\ngot  %s",
			want, got)
	}
}
