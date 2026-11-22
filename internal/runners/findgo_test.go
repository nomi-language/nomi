package runners

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestFindGo_AnEmptyPATHIsNamed: with no NOMI_GO and an empty PATH, which
// exec.LookPath reads as the current directory, the refusal says PATH is
// empty rather than "(looked in )", says what needed Go and what needs none,
// and unwraps to exec.ErrNotFound.
func TestFindGo_AnEmptyPATHIsNamed(t *testing.T) {
	t.Setenv(goOverrideEnv, "")
	t.Setenv("PATH", "")
	if p, err := exec.LookPath("go"); err == nil {
		t.Skipf("PATH=\"\" still resolves go at %s, so there is a `go` in the working "+
			"directory and this test cannot observe the empty-PATH arm", p)
	}
	got, err := findGo()
	if err == nil {
		t.Fatalf("findGo returned %q with an empty PATH", got)
	}
	msg := err.Error()
	for _, s := range []string{
		"no Go toolchain: `nomi build` builds nomi-vm",
		"(PATH is empty)",
		"https://go.dev/dl/",
		"`nomi run` needs no toolchain.",
	} {
		if !strings.Contains(msg, s) {
			t.Errorf("missing %q. Got:\n%s", s, msg)
		}
	}
	if strings.Contains(msg, "looked in") {
		t.Errorf("names directories it did not search:\n%s", msg)
	}
	if !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("the failure does not unwrap to exec.ErrNotFound: %v", err)
	}
}

// TestFindGo_NOMIGOWins: NOMI_GO names the `go` ahead of PATH, as the
// command or as a Go root holding bin/go, and one that does not exist is
// refused by name.
func TestFindGo_NOMIGOWins(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin", exeName("go", runtime.GOOS))
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(goOverrideEnv, bin)
	if got, err := findGo(); err != nil || got != bin {
		t.Fatalf("NOMI_GO=%s: got %q, %v", bin, got, err)
	}
	t.Setenv(goOverrideEnv, dir)
	if got, err := findGo(); err != nil || got != bin {
		t.Fatalf("NOMI_GO=<GOROOT> %s: got %q, %v", dir, got, err)
	}
	t.Setenv(goOverrideEnv, filepath.Join(dir, "missing"))
	if _, err := findGo(); err == nil || !strings.Contains(err.Error(), "NOMI_GO=") {
		t.Fatalf("a NOMI_GO that does not exist: %v", err)
	}
}
