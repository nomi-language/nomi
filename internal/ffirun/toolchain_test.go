package ffirun

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/gotoolchain"
)

// TestBuildWrapperBinary_NoGoToolchainFailsByName pins ffirun's half of the
// missing-toolchain account.
//
// `nomi run` / `test` / `check` on a project with Go FFI stages a wrapper that
// links nomi/vmhost with adapters generated for the user's bindings, and
// building it needs `go`. In a source checkout under `PATH=/usr/bin:/bin`:
//
//	nomi run: ffirun: building wrapper: exec: "go": executable file not found in $PATH
//
// True, and it named neither the PATH it searched nor the fact that a project
// WITHOUT Go FFI runs with no toolchain at all — which is the thing a user in
// this position most needs to know, since it tells them the install is fine.
// Both halves come from internal/gotoolchain now, so `nomi build` and this path
// cannot drift apart.
//
// WHITE-BOX ON PURPOSE, with a zero Result. The toolchain is resolved before
// anything on the Result is read, so staging a real project would add a minute
// of discovery and codegen to observe a lookup failure. The arrangement is
// asserted rather than assumed: if the resolution ever moves after the first
// field read, this test panics instead of passing.
func TestBuildWrapperBinary_NoGoToolchainFailsByName(t *testing.T) {
	empty := t.TempDir()
	t.Setenv("PATH", empty)
	if p, err := exec.LookPath("go"); err == nil {
		t.Fatalf("PATH=%s still resolves go at %s, so the failure below would not be "+
			"caused by the scrub", empty, p)
	}
	err := buildWrapperBinary(&Result{}, filepath.Join(empty, "out"))
	if err == nil {
		t.Fatal("buildWrapperBinary SUCCEEDED with no `go` on PATH")
	}
	var notFound *gotoolchain.NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("the failure is not a *gotoolchain.NotFoundError: %v", err)
	}
	msg := err.Error()
	for _, must := range []string{
		"ffirun:",
		"no Go toolchain",
		"this project has Go FFI", // why THIS command needs one
		empty,                     // where it looked
		"https://go.dev/dl/",
		"A project with no Go FFI runs with no toolchain at all",
	} {
		if !strings.Contains(msg, must) {
			t.Errorf("the message does not contain %q. Got:\n%s", must, msg)
		}
	}
	// Two things this message must NOT say, and both were in the first draft.
	// It ran on a failing `nomi run`, so naming `nomi build` as the thing that
	// needs a toolchain reads as an unrelated suggestion; and the wrapper links
	// the COMPILER's module, so its floor is that module's `go` directive and
	// not anything ffirun staged — a version read off the wrong go.mod would be
	// worse than none.
	if strings.Contains(msg, "`nomi build` compiles") {
		t.Errorf("a failing `nomi run` explained what `nomi build` does:\n%s", msg)
	}
	if strings.Contains(msg, "The generated module asks for Go") {
		t.Errorf("the wrapper's failure names a generated module's `go` directive, and "+
			"there is no generated module on this path:\n%s", msg)
	}
}
