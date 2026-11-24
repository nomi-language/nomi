package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/stdcache"
	"github.com/nomi-language/nomi/std"
)

// TestStdFile_ACachedCopyIsItsModuleOrRefused: a file of the copy of std
// the language server writes for go-to-definition (internal/stdcache) is
// the module it is a copy of when its version directory is this nomi's and
// its bytes are the module's: `nomi test` runs its `//!` cases, `nomi check`
// passes, and `nomi run` refuses it as a module, as for the same file in the
// source tree. A file of another version's directory, or one edited since it
// was written, is refused in one error that says what it is. Each was
// analyzed as a user project and reported about 53 errors, the first
// "type name 'Add' is reserved". `nomi fmt --check` reads only the text, so
// it passes each. The directory is written by the cache's own writer
// (stdcache.Dir.Path), from this checkout's std, whose version is the one
// nomi embeds.
func TestStdFile_ACachedCopyIsItsModuleOrRefused(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	t.Setenv(stdcache.RootEnv, t.TempDir())
	source, err := analysis.StdlibPath()
	if err != nil {
		t.Fatal(err)
	}
	dir := stdcache.New(stdcache.DefaultRoot(), os.DirFS(source))
	own := std.CacheVersion()
	if dir.Version() != own {
		t.Fatalf("this checkout's std is version %s and nomi embeds %s", dir.Version(), own)
	}
	file := dir.Path("option.nomi")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("the cache was not written: %v", err)
	}
	other := filepath.Join(stdcache.DefaultRoot(), "0123456789abcdef", "option.nomi")
	mustWrite(t, other, string(data))
	cacheRoot := t.TempDir()

	run := func(args ...string) (string, int) {
		t.Helper()
		out, err := runNomi(t, cacheRoot, args...)
		requireNoPanic(t, args[0], out)
		var exit *exec.ExitError
		switch {
		case err == nil:
			return out, 0
		case errors.As(err, &exit):
			return out, exit.ExitCode()
		}
		t.Fatalf("nomi %v: %v", args, err)
		return "", 0
	}
	expect := func(args []string, want string, wantExit int) {
		t.Helper()
		out, code := run(args...)
		if strings.TrimSpace(out) != want || code != wantExit {
			t.Errorf("nomi %s: printed\n%s\nexited %d; want\n%s\nexited %d", strings.Join(args, " "), out, code, want, wantExit)
		}
	}

	// This version's file is the module.
	if out, code := run("test", file); code != 0 || !strings.Contains(out, "test result: ok.") || !strings.Contains(out, "option.nomi :: impl / map //! test") {
		t.Errorf("nomi test of this version's file did not run its cases (exit %d):\n%s", code, out)
	}
	expect([]string{"check", file}, "ok "+file, 0)
	expect([]string{"run", file}, "nomi run: "+file+" is a stdlib module, not a program; to run its tests, use `nomi test "+file+"`", 1)
	expect([]string{"fmt", "--check", file}, "", 0)

	// Another version's file is refused.
	refusal := other + " is a cached copy of the standard library from version 0123456789abcdef, read-only, written for go-to-definition; this nomi's standard library is version " + own
	expect([]string{"test", other}, "FAIL "+other+": "+refusal+"\ntest result: FAILED. 0 passed, 1 failed", 1)
	expect([]string{"check", other}, "nomi check: "+refusal, 1)
	expect([]string{"run", other}, "nomi run: "+refusal, 1)
	expect([]string{"fmt", "--check", other}, "", 0)

	// This version's file, edited, is refused.
	if err := os.Chmod(file, 0o644); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, file, string(data)+"\nfn added() -> Int\n    1\nend\n")
	refusal = file + " is a cached copy of the standard library from version " + own + ", read-only, written for go-to-definition, and it is no longer the text of that version's module"
	expect([]string{"test", file}, "FAIL "+file+": "+refusal+"\ntest result: FAILED. 0 passed, 1 failed", 1)
	expect([]string{"check", file}, "nomi check: "+refusal, 1)
	expect([]string{"run", file}, "nomi run: "+refusal, 1)
	expect([]string{"fmt", "--check", file}, "", 0)
}
