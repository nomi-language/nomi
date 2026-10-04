package irbuild

import (
	"fmt"
	"testing"
)

// `std/compiler.run_file` and the `std/compiler.RunFile` struct it takes.
//
// The two boundaries `run_file` adds to `compiler.run` — a source read from
// DISK under the project root, and a simulated process environment — are
// VALUES, so the output sees them. That is TestRunFile_PinnedText. The field
// DEFAULT is not observable at all; that is stated in the fixture rather than
// papered over.

// TestRunFile_PinnedText pins the reference bytes absolutely.
//
// Each group is a rule with a wrong implementation attached, and for `run_file`
// the tempting wrong implementation is `compiler.run` with an extra parameter
// dropped on the floor — which passes rows 1, 2 and every row of group 5 and
// fails exactly rows 3 and 4.
//
//	default / explicit_empty   THE SAME OBSERVATION FOR TWO REASONS, on purpose.
//	                           Row 1 omits `env` and the builder supplies the
//	                           declared `Map.empty()`; row 2 writes it. An
//	                           omitted `rt.Map` field's Go zero value IS an empty
//	                           map, so these two cannot be distinguished by any
//	                           program — recorded rather than mistaken for
//	                           coverage. See the fixture's header, and see
//	                           `Project.manifest` for the contrasting default
//	                           whose value is NOT the zero value.
//	with_env                   THE ONLY ROW THAT SHOWS THE ENV ARRIVES.
//	                           `probe=arrived` comes out of `os.get` inside the
//	                           run source's `boot()`, which is the sole way a
//	                           Nomi program observes a process environment. A
//	                           `run`-shaped implementation prints `probe=absent`
//	                           here and passes everything else.
//	two_keys                   the env is a MAP and not one slot: two entries,
//	                           one read. A projection that carried only the first
//	                           entry, or overwrote, prints `arrived` or `absent`.
//	empty/suffixed/escaping    THE CONTAINMENT RULES, each its own Err text. They
//	                           are why stdcompilerrun.RunFileSource is ONE
//	                           exported function rather than each caller
//	                           composing the lookup itself: these four strings
//	                           are Nomi-visible and a second copy of them is a
//	                           divergence waiting to be written.
//	missing = True             the fourth rejection is the OS's own and its text
//	                           embeds an ABSOLUTE path, so it is read for shape.
//
// The `[...]` brackets around each captured run are compiler_run.nomi's device
// and load-bearing for the same reason: the run source's own trailing newline is
// PART of the answer, and a bare print would make it indistinguishable from the
// newline this fixture's print adds.
func TestRunFile_PinnedText(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	ran := "target ran\nprobe=%s\ntrimmed=x\n"
	want := "default = [" + fmt.Sprintf(ran, "absent") + "]\n" +
		"explicit_empty = [" + fmt.Sprintf(ran, "absent") + "]\n" +
		"with_env = [" + fmt.Sprintf(ran, "arrived") + "]\n" +
		"two_keys = [" + fmt.Sprintf(ran, "second") + "]\n" +
		"empty = ERR compiler.run_file entry point must not be empty\n" +
		"suffixed = ERR compiler.run_file entry point must omit .nomi\n" +
		"escaping = ERR compiler.run_file entry point must stay under the current project root\n" +
		"missing = True\n"
	if got := vmReference("testdata/run_file/main.nomi"); got.stdout != want || got.exit != 0 {
		t.Fatalf("reference output is not what this fixture pins: %s\nwant stdout=%q", got, want)
	}
}
