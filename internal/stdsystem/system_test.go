//go:build !js && !wasip1

package stdsystem

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/rt"
)

// Each fact is the one Go reports.
func TestSystem_FactsAreGosOwn(t *testing.T) {
	if got, want := OS(), runtime.GOOS; got != want {
		t.Errorf("os is %q, want %q", got, want)
	}
	if got, want := Arch(), runtime.GOARCH; got != want {
		t.Errorf("arch is %q, want %q", got, want)
	}
	if got, want := CPUCount(), int64(runtime.NumCPU()); got != want {
		t.Errorf("cpu_count is %d, want %d", got, want)
	}
	name, err := os.Hostname()
	if got := Hostname(); err != nil || got.Tag != rt.TagOk || got.Ok != name {
		t.Errorf("hostname is %+v, want %q (%v)", got, name, err)
	}
	exe, err := os.Executable()
	if got := Executable(); err != nil || got.Tag != rt.TagOk || got.Ok != exe {
		t.Errorf("executable is %+v, want %q (%v)", got, exe, err)
	}
	heap, obtained := Memory()
	if heap <= 0 || obtained < heap {
		t.Errorf("memory is heap %d, obtained %d: want a heap and at least as much obtained", heap, obtained)
	}
	// A test binary is built from source, so it is no release.
	if got := NomiRelease(); got.Tag != rt.TagNone {
		t.Errorf("nomi_release is %+v in a test binary, want None", got)
	}
}

// data_dir follows the rules Go's UserCacheDir follows for its own
// variable: an absolute XDG_DATA_HOME, else ~/.local/share, and a relative
// one is refused, on the Unix systems other than macOS.
func TestSystem_DataDirFollowsTheXDGRules(t *testing.T) {
	switch runtime.GOOS {
	case "darwin", "ios", "windows", "plan9":
		t.Skip("XDG_DATA_HOME is read on Linux and the Unix systems other than macOS")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")
	if got := DataDir(); got.Tag != rt.TagOk || got.Ok != filepath.Join(home, ".local", "share") {
		t.Errorf("with no XDG_DATA_HOME, data_dir is %+v", got)
	}
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	if got := DataDir(); got.Tag != rt.TagOk || got.Ok != filepath.Join(home, "data") {
		t.Errorf("with XDG_DATA_HOME, data_dir is %+v", got)
	}
	t.Setenv("XDG_DATA_HOME", "data")
	got := DataDir()
	if got.Tag == rt.TagOk {
		t.Fatalf("a relative XDG_DATA_HOME is answered as %q", got.Ok)
	}
	var parts []string
	for l := got.Err; l != nil; l = l.Tail {
		parts = append(parts, l.Head)
	}
	if want := "Unavailable system.data_dir path in $XDG_DATA_HOME is relative"; strings.Join(parts, " ") != want {
		t.Errorf("a relative XDG_DATA_HOME is %q, want %q", parts, want)
	}
}
