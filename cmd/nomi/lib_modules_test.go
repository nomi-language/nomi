package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"testing"

	"github.com/nomi-language/nomi/internal/termcolor"
)

// TestLibModules runs the first-party modules under lib/ as their checkout
// runs them: `nomi test` on the sqlite and postgres drivers, on migrate, on
// sqlkit, on the Postgres jobs queue and its notify example, on the S3
// storage backend and its MinIO project (skipped without `minio` on PATH),
// `nomi tool sqlkit generate --check` and `nomi test .` in the shop
// example, `nomi test` on image, and `nomi effects --check` on every
// module, which holds its tables to what its public functions and its
// tests have.
// They hold no Go, so root `go test ./...` finds no package in them.
// They are Nomi alone, sqlite and image with their engines
// as WebAssembly. The postgres, migrate and jobs runs skip where
// postgres.scratch() cannot start a server. Every case must pass.
func TestLibModules(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; runs every lib/ module's tests; -short")
	}
	root := repoRoot(t)
	// One runner cache for every run.
	cache := t.TempDir()
	env := append(buildEnv(cache), artifactCacheEnv(t.TempDir())...)
	// nomiWith runs nomi with extra variables after env's.
	nomiWith := func(t *testing.T, extra []string, dir string, args ...string) string {
		t.Helper()
		run := runProcess(t, append(env[:len(env):len(env)], extra...), dir, "", nomiBin, args...)
		out := run.stdout + run.stderr
		if run.exit != 0 {
			t.Fatalf("nomi %v in %s exited %d:\n%s", args, dir, run.exit, out)
		}
		return out
	}
	nomi := func(t *testing.T, dir string, args ...string) string {
		t.Helper()
		return nomiWith(t, nil, dir, args...)
	}
	passesWith := func(t *testing.T, extra []string, dir string, args ...string) {
		t.Helper()
		out := termcolor.StripANSI(nomiWith(t, extra, dir, args...))
		summary := regexp.MustCompile(`test result: \S+\. (\d+) passed, (\d+) failed(?:, (\d+) blocked)?`).FindStringSubmatch(out)
		if summary == nil {
			t.Fatalf("no summary line in `nomi %v`:\n%s", args, out)
		}
		passed, _ := strconv.Atoi(summary[1])
		if passed == 0 || summary[2] != "0" || (summary[3] != "" && summary[3] != "0") {
			t.Fatalf("`nomi %v` in %s: %s; every case must pass.\n\n%s", args, dir, summary[0], vmCorpusNonPassing(out))
		}
	}
	passes := func(t *testing.T, dir string, args ...string) {
		t.Helper()
		passesWith(t, nil, dir, args...)
	}
	// Each module's [effects] tables say exactly what its public functions,
	// and its tests beyond them, have: `nomi effects --check` passes.
	t.Run("effects lists", func(t *testing.T) {
		var modules []string
		err := filepath.WalkDir(filepath.Join(root, "lib"), func(path string, d os.DirEntry, err error) error {
			switch {
			case err != nil:
				return err
			case d.IsDir() && d.Name() == "testdata":
				return filepath.SkipDir
			case !d.IsDir() && d.Name() == "nomi.toml":
				modules = append(modules, filepath.Dir(path))
			}
			return nil
		})
		if err != nil || len(modules) == 0 {
			t.Fatalf("finding lib/'s modules: %d found, %v", len(modules), err)
		}
		for _, dir := range modules {
			nomi(t, root, "effects", "--check", dir)
		}
	})
	// The driver's localtime test reads the zone the machine is set to: a
	// zone with daylight saving, whatever the machine running this is set to.
	t.Run("sqlite", func(t *testing.T) {
		passesWith(t, []string{"TZ=America/New_York"}, root, "test", filepath.Join("lib", "sql", "sqlite"))
	})
	t.Run("sqlkit", func(t *testing.T) {
		passes(t, root, "test", filepath.Join("lib", "sql", "sqlkit"))
	})
	t.Run("shop", func(t *testing.T) {
		shop := filepath.Join(root, "lib", "sql", "examples", "shop")
		nomi(t, shop, "tool", "sqlkit", "generate", "--check")
		passes(t, shop, "test", ".")
		// The generated test reads the SQL under the module root, from
		// wherever `nomi test` runs.
		passes(t, root, "test", filepath.Join("lib", "sql", "examples", "shop"))
	})
	t.Run("markdown", func(t *testing.T) {
		passes(t, root, "test", filepath.Join("lib", "markdown"))
	})
	t.Run("image", func(t *testing.T) {
		passes(t, root, "test", filepath.Join("lib", "image"))
	})
	// The postgres driver's tests run on postgres.scratch()'s throwaway
	// server, which needs initdb and postgres on PATH and a POSIX shell.
	// The first case of each `nomi test` process starts that server
	// (initdb, then postgres) inside its own time limit. Alone that case
	// takes about 4 seconds; under `make check-full`, with other suites
	// running beside it, starting the server alone passed the default
	// 10s. scratchTimeout gives those runs room for the start and still
	// holds every case to a limit.
	const scratchTimeout = "60s"
	t.Run("postgres", func(t *testing.T) {
		requireScratchPostgres(t)
		passes(t, root, "test", "--timeout", scratchTimeout, filepath.Join("lib", "sql", "postgres"))
	})
	// migrate's tests run each case on SQLite and on postgres.scratch()'s
	// server, and two processes at once on Postgres.
	t.Run("migrate", func(t *testing.T) {
		requireScratchPostgres(t)
		passes(t, root, "test", "--timeout", scratchTimeout, filepath.Join("lib", "sql", "migrate"))
	})
	// The jobs queue over Postgres and the example that ports an app's
	// notifications to it run on the same scratch servers.
	t.Run("jobs/postgres", func(t *testing.T) {
		requireScratchPostgres(t)
		passes(t, root, "test", "--timeout", scratchTimeout, filepath.Join("lib", "jobs", "postgres"))
	})
	t.Run("jobs/examples/notify", func(t *testing.T) {
		requireScratchPostgres(t)
		passes(t, root, "test", "--timeout", scratchTimeout, filepath.Join("lib", "jobs", "examples", "notify"))
	})
	// The S3 backend's own tests need no server: they sign AWS's published
	// examples and answer requests with std/http/testing. The MinIO
	// project runs it against a real server.
	t.Run("storage/s3", func(t *testing.T) {
		passes(t, root, "test", filepath.Join("lib", "storage", "s3"))
	})
	t.Run("storage/minio", func(t *testing.T) {
		requireMinio(t)
		passes(t, root, "test", filepath.Join("lib", "storage", "minio"))
	})
}

// requireMinio skips a test that starts a MinIO server when `minio` is not
// on PATH. A Nomi test cannot skip itself, which is why the MinIO tests are
// a project of their own.
func requireMinio(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("minio"); err != nil {
		t.Skip("minio is not on PATH, so lib/storage/minio has no server to run")
	}
}

// requireScratchPostgres skips a test that needs postgres.scratch()'s
// server when this machine cannot run one.
func requireScratchPostgres(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("postgres.scratch() needs a POSIX shell to stop its server")
	}
	for _, tool := range []string{"initdb", "postgres"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not on PATH, so postgres.scratch() has no server to run", tool)
		}
	}
}
