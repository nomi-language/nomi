package ffirun

import (
	"path/filepath"
	"testing"
)

// TestHostKeyword_AGoBoundStdModuleLeavesTheToolchainFreePath used to assert
// that a fork EXISTED, and it is kept, inverted, because the fork was a
// regression this repo shipped and a test that only ever said "closed" would
// not notice it coming back.
//
// WHAT IT ASSERTED BEFORE. `Prepare` reports `FastPath` — no Go toolchain
// needed, the program runs in process. A program importing a
// first-party co-located adapter left that path, and what happened next was a
// `go build` of the staged wrapper. Observed directly with `go` removed from
// PATH, on a program using `std/calendar`:
//
//	nomi run: ffirun: building wrapper: exec: "go": executable file not found in $PATH
//
// while the same program written against `std/bytes` printed its answer. So a
// `nomi run` of the Go-backed stdlib modules required a Go toolchain and the
// rest did not.
//
// WHAT IT ASSERTS NOW. Every row is FastPath. `std/calendar`, `std/random`
// and `std/regex` declare `host fn` and their Go lives in
// sibling packages (`nomi/stdcalendar` and friends), so there is no `std/<name>/`
// directory for discovery to key on and no `gopkg` in a facade for it to read.
// Nothing under `std/` selects a Go package, and nothing under `std/` needs a
// toolchain.
//
// WHY THE `std/bytes` ROW STAYS. It is the control. It was never on the
// wrapper path, so a change that put EVERY program there — the direction that
// would undo this — moves both rows together and fails this test on the
// control rather than on the subject. Asserting only the calendar row would
// pass under "everything is fast" and under "the fast path stopped meaning
// anything" alike.
//
// AND WHY THE MODULE ROWS ARE SPELLED OUT rather than folded into one. Each
// module reached the wrapper path for its OWN directory entry; a single row
// would pass while the others came back.
func TestHostKeyword_AGoBoundStdModuleLeavesTheToolchainFreePath(t *testing.T) {
	cases := []struct {
		name         string
		source       string
		wantFastPath bool
		why          string
	}{
		{
			name:         "rt-backed primitive declared with host fn",
			source:       "fn size(s: String): Int {\n  Bytes.length(String.to_bytes(s))\n}\n",
			wantFastPath: true,
			why:          "the control: std/bytes was never on the wrapper path",
		},
		{
			name:         "std/calendar",
			source:       "import std/calendar.Date\n\nfn render(d: Date): String {\n  Date.to_string(d)\n}\n",
			wantFastPath: true,
			why:          "calendar's Go is nomi/stdcalendar and its facade declares host fn",
		},
		{
			name:         "std/regex",
			source:       "import std/regex.Regex\n\nfn pat(r: Regex): String {\n  Regex.pattern(r)\n}\n",
			wantFastPath: true,
			why:          "regex's Go is nomi/stdregex and its facade declares host fn",
		},
		{
			name: "std/random",
			source: "import std/random.Seed\n\n" +
				"fn s(n: Int): Seed {\n  Seed.from_int(n)\n}\n",
			wantFastPath: true,
			why:          "random's Go is nomi/stdrandom and its facade declares host fn",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())
			root := t.TempDir()
			entry := filepath.Join(root, "main.nomi")
			mustWriteHelper(t, entry, tc.source)
			res, err := Prepare(entry)
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			if res.FastPath != tc.wantFastPath {
				t.Fatalf("FastPath = %v, want %v — %s", res.FastPath, tc.wantFastPath, tc.why)
			}
			t.Logf("FastPath=%v (%s)", res.FastPath, tc.why)
		})
	}
}

// TestFirstPartyDiscoveryHasNoRemainingTrigger is the other direction, and it
// is the one the test above cannot give: FastPath is also what a program with
// no bindings at all reports, so five FastPath rows are consistent with
// discovery having been broken outright rather than with std having left it.
//
// This asserts that discovery still WORKS — a user's own `gopkg` binding is
// still found and still takes the wrapper path — over the same scope shape the
// rows above use.
func TestFirstPartyDiscoveryHasNoRemainingTrigger(t *testing.T) {
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())
	root := t.TempDir()
	mustWriteHelper(t, filepath.Join(root, "go.mod"), "module example.com/app\n\ngo 1.27.0\n")
	mustWriteHelper(t, filepath.Join(root, "ffi.go"), "package app\n\nfunc Double(n int64) int64 { return n * 2 }\n")
	entry := filepath.Join(root, "main.nomi")
	mustWriteHelper(t, entry, "gopkg \"example.com/app\" as app\n\n"+
		"pub fn double(n: Int): Int go app.Double\n")
	res, err := Prepare(entry)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if res.FastPath {
		t.Fatal("a user's own `gopkg` binding reported FastPath. Discovery is not running at " +
			"all, which makes every FastPath row above vacuous.")
	}
}
