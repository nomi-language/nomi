package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/nomi-language/nomi/internal/termcolor"
)

// TestLibModules runs the first-party modules under lib/ as their checkout
// runs them: `nomi test` on the sqlite driver and on sqlkit, `nomi tool
// sqlkit prepare --check` and `nomi test .` in the shop example, and the
// driver's Go tests (`go test ./...` in lib/sql/sqlite, its own Go
// module). Each is its own module with its own go.mod, which root `go test
// ./...` does not reach. They require modernc.org/sqlite, which the
// compiler's module does not: `go mod download` in lib/sql/sqlite puts it in
// the Go module cache first, from the network when the cache lacks it.
// Every case must pass.
func TestLibModules(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds the sqlite driver's runner; -short")
	}
	root := repoRoot(t)
	sqliteDir := filepath.Join(root, "lib", "sql", "sqlite")
	download := exec.Command("go", "mod", "download")
	download.Dir = sqliteDir
	if out, err := download.CombinedOutput(); err != nil {
		t.Fatalf("go mod download in lib/sql/sqlite: %v\n%s", err, out)
	}
	// One wrapper cache for every run, so the shop's runner reuses what the
	// driver's built; the managed toolchain is never downloaded.
	cache := t.TempDir()
	env := append(buildEnv(cache), noManagedGoEnv(t.TempDir())...)
	nomi := func(t *testing.T, dir string, args ...string) string {
		t.Helper()
		run := runProcess(t, env, dir, "", nomiBin, args...)
		out := run.stdout + run.stderr
		if run.exit != 0 {
			t.Fatalf("nomi %v in %s exited %d:\n%s", args, dir, run.exit, out)
		}
		return out
	}
	passes := func(t *testing.T, dir string, args ...string) {
		t.Helper()
		out := termcolor.StripANSI(nomi(t, dir, args...))
		summary := regexp.MustCompile(`test result: \S+\. (\d+) passed, (\d+) failed(?:, (\d+) blocked)?`).FindStringSubmatch(out)
		if summary == nil {
			t.Fatalf("no summary line in `nomi %v`:\n%s", args, out)
		}
		passed, _ := strconv.Atoi(summary[1])
		if passed == 0 || summary[2] != "0" || (summary[3] != "" && summary[3] != "0") {
			t.Fatalf("`nomi %v` in %s: %s; every case must pass.\n\n%s", args, dir, summary[0], vmCorpusNonPassing(out))
		}
	}
	t.Run("sqlite", func(t *testing.T) {
		passes(t, root, "test", filepath.Join("lib", "sql", "sqlite"))
	})
	t.Run("sqlkit", func(t *testing.T) {
		passes(t, root, "test", filepath.Join("lib", "sql", "sqlkit"))
	})
	t.Run("shop", func(t *testing.T) {
		shop := filepath.Join(root, "lib", "sql", "examples", "shop")
		nomi(t, shop, "tool", "sqlkit", "prepare", "--check")
		passes(t, shop, "test", ".")
	})
	t.Run("sqlite's Go tests", func(t *testing.T) {
		cmd := exec.Command("go", "test", "-count=1", "./...")
		cmd.Dir = sqliteDir
		cmd.Env = os.Environ()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go test ./... in lib/sql/sqlite: %v\n%s", err, out)
		}
	})
}
