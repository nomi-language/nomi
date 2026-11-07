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
// example, and the drivers' Go tests (`go test ./...` in each, its own Go
// module). Each is its own module with its own go.mod, which root `go test
// ./...` does not reach. They require modernc.org/sqlite and pgx, which the
// compiler's module does not: `go mod download` in sqlkit, whose module
// graph holds both drivers through migrate, puts them in the Go module cache
// first, from the network when the cache lacks them. The postgres, migrate
// and jobs runs skip where postgres.scratch() cannot start a server. Every
// case must pass.
func TestLibModules(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds the sqlite driver's runner; -short")
	}
	root := repoRoot(t)
	sqliteDir := filepath.Join(root, "lib", "sql", "sqlite")
	download := exec.Command("go", "mod", "download")
	download.Dir = filepath.Join(root, "lib", "sql", "sqlkit")
	if out, err := download.CombinedOutput(); err != nil {
		t.Fatalf("go mod download in lib/sql/sqlkit: %v\n%s", err, out)
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
	// A release's prebuilt lib binaries are built from librunners/, whose
	// main packages are generated from the lib/ modules' bindings.
	t.Run("librunners is current", func(t *testing.T) {
		download := exec.Command("go", "mod", "download")
		download.Dir = filepath.Join(root, "librunners")
		if out, err := download.CombinedOutput(); err != nil {
			t.Fatalf("go mod download in librunners: %v\n%s", err, out)
		}
		check := exec.Command("go", "run", "./internal/ffirun/libgen", "-check")
		check.Dir = root
		if out, err := check.CombinedOutput(); err != nil {
			t.Fatalf("librunners/ is stale; run `go run ./internal/ffirun/libgen`: %v\n%s", err, out)
		}
		vet := exec.Command("go", "vet", "./...")
		vet.Dir = filepath.Join(root, "librunners")
		if out, err := vet.CombinedOutput(); err != nil {
			t.Fatalf("go vet ./... in librunners: %v\n%s", err, out)
		}
	})
	t.Run("sqlite", func(t *testing.T) {
		passes(t, root, "test", filepath.Join("lib", "sql", "sqlite"))
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
	t.Run("sqlite's Go tests", func(t *testing.T) {
		cmd := exec.Command("go", "test", "-count=1", "./...")
		cmd.Dir = sqliteDir
		cmd.Env = os.Environ()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go test ./... in lib/sql/sqlite: %v\n%s", err, out)
		}
	})
	markdownDir := filepath.Join(root, "lib", "markdown")
	t.Run("markdown", func(t *testing.T) {
		download := exec.Command("go", "mod", "download")
		download.Dir = markdownDir
		if out, err := download.CombinedOutput(); err != nil {
			t.Fatalf("go mod download in lib/markdown: %v\n%s", err, out)
		}
		passes(t, root, "test", filepath.Join("lib", "markdown"))
	})
	t.Run("markdown's Go tests", func(t *testing.T) {
		cmd := exec.Command("go", "test", "-count=1", "./...")
		cmd.Dir = markdownDir
		cmd.Env = os.Environ()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go test ./... in lib/markdown: %v\n%s", err, out)
		}
	})
	imageDir := filepath.Join(root, "lib", "image")
	t.Run("image", func(t *testing.T) {
		download := exec.Command("go", "mod", "download")
		download.Dir = imageDir
		if out, err := download.CombinedOutput(); err != nil {
			t.Fatalf("go mod download in lib/image: %v\n%s", err, out)
		}
		passes(t, root, "test", filepath.Join("lib", "image"))
	})
	t.Run("image's Go tests", func(t *testing.T) {
		cmd := exec.Command("go", "test", "-count=1", "./...")
		cmd.Dir = imageDir
		cmd.Env = os.Environ()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go test ./... in lib/image: %v\n%s", err, out)
		}
	})
	passwordsDir := filepath.Join(root, "lib", "password_hashers")
	t.Run("password_hashers", func(t *testing.T) {
		download := exec.Command("go", "mod", "download")
		download.Dir = passwordsDir
		if out, err := download.CombinedOutput(); err != nil {
			t.Fatalf("go mod download in lib/password_hashers: %v\n%s", err, out)
		}
		passes(t, root, "test", filepath.Join("lib", "password_hashers"))
	})
	t.Run("password_hashers's Go tests", func(t *testing.T) {
		cmd := exec.Command("go", "test", "-count=1", "./...")
		cmd.Dir = passwordsDir
		cmd.Env = os.Environ()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go test ./... in lib/password_hashers: %v\n%s", err, out)
		}
	})
	// The postgres driver's tests run on postgres.scratch()'s throwaway
	// server, which needs initdb and postgres on PATH and a POSIX shell.
	postgresDir := filepath.Join(root, "lib", "sql", "postgres")
	t.Run("postgres", func(t *testing.T) {
		requireScratchPostgres(t)
		download := exec.Command("go", "mod", "download")
		download.Dir = postgresDir
		if out, err := download.CombinedOutput(); err != nil {
			t.Fatalf("go mod download in lib/sql/postgres: %v\n%s", err, out)
		}
		passes(t, root, "test", filepath.Join("lib", "sql", "postgres"))
	})
	// migrate's tests run each case on SQLite and on postgres.scratch()'s
	// server, and two processes at once on Postgres.
	t.Run("migrate", func(t *testing.T) {
		requireScratchPostgres(t)
		passes(t, root, "test", filepath.Join("lib", "sql", "migrate"))
	})
	// The jobs queue over Postgres and the example that ports an app's
	// notifications to it run on the same scratch servers.
	t.Run("jobs/postgres", func(t *testing.T) {
		requireScratchPostgres(t)
		passes(t, root, "test", filepath.Join("lib", "jobs", "postgres"))
	})
	t.Run("jobs/examples/notify", func(t *testing.T) {
		requireScratchPostgres(t)
		passes(t, root, "test", filepath.Join("lib", "jobs", "examples", "notify"))
	})
	// The S3 backend's own tests need no server: they sign AWS's published
	// examples and answer requests with std/http_testing. The MinIO
	// project runs it against a real server.
	t.Run("storage/s3", func(t *testing.T) {
		passes(t, root, "test", filepath.Join("lib", "storage", "s3"))
	})
	t.Run("storage/minio", func(t *testing.T) {
		requireMinio(t)
		passes(t, root, "test", filepath.Join("lib", "storage", "minio"))
	})
	t.Run("postgres's Go tests", func(t *testing.T) {
		requireScratchPostgres(t)
		cmd := exec.Command("go", "test", "-count=1", "./...")
		cmd.Dir = postgresDir
		cmd.Env = os.Environ()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go test ./... in lib/sql/postgres: %v\n%s", err, out)
		}
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
