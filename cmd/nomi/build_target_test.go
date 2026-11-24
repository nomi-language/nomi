package main

import (
	"runtime"
	"testing"

	"github.com/nomi-language/nomi/internal/runners"
)

// TestBuildTarget_GOOSAndGOARCHChooseNothing: with no --target, `nomi build`
// and `nomi build-vm` build for the platform nomi runs on, whatever GOOS and
// GOARCH the environment exports, and --target alone names another.
func TestBuildTarget_GOOSAndGOARCHChooseNothing(t *testing.T) {
	other := runners.Target{GOOS: "windows", GOARCH: "arm64"}
	if runtime.GOOS == "windows" {
		other = runners.Target{GOOS: "linux", GOARCH: "riscv64"}
	}
	t.Setenv("GOOS", other.GOOS)
	t.Setenv("GOARCH", other.GOARCH)
	host := runners.Target{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}

	b, err := parseBuildArgs([]string{"hello.nomi"})
	if err != nil {
		t.Fatal(err)
	}
	if b.target != host {
		t.Errorf("nomi build with GOOS=%s GOARCH=%s builds for %s, want the host's %s", other.GOOS, other.GOARCH, b.target, host)
	}
	v, err := parseBuildVMArgs(nil)
	if err != nil {
		t.Fatal(err)
	}
	if v.target != host {
		t.Errorf("nomi build-vm with GOOS=%s GOARCH=%s builds for %s, want the host's %s", other.GOOS, other.GOARCH, v.target, host)
	}

	b, err = parseBuildArgs([]string{"hello.nomi", "--target", "wasip1/wasm"})
	if err != nil {
		t.Fatal(err)
	}
	if want := (runners.Target{GOOS: "wasip1", GOARCH: "wasm"}); b.target != want {
		t.Errorf("nomi build --target wasip1/wasm builds for %s", b.target)
	}
}
