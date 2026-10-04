package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFFI_AProjectOutsideTheRepositoryRuns copies testdata/go_bindings to a
// temporary directory, outside this repository, and gives it a go.mod holding
// only its own module line: no `require nomi`, no `replace nomi`. A user's
// project has no path it could point such a replace at, so `nomi run`,
// `nomi test` and `nomi build` must link the compiler's module on their own.
//
// The second case keeps a `require nomi` whose `replace` points at another
// module named `nomi` that has none of the compiler's packages: the wrapper's
// own replace must win, or the wrapper cannot import nomi/vmhost.
func TestFFI_AProjectOutsideTheRepositoryRuns(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; compiles a Go FFI wrapper and runner; -short")
	}
	cases := map[string]string{
		"no nomi lines": "module gobindings\n\ngo 1.27.0\n",
		"a replace naming another nomi": "module gobindings\n\ngo 1.27.0\n\nrequire github.com/nomi-language/nomi v0.0.0\n\n" +
			"replace github.com/nomi-language/nomi => ./other-nomi\n",
	}
	for name, goMod := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if strings.HasPrefix(root, repoRoot(t)) {
				t.Fatalf("the temp project %s is inside the repository, so this test proves nothing", root)
			}
			src := filepath.Join(repoRoot(t), "cmd", "nomi", "testdata", "go_bindings")
			for _, file := range []string{"nomi.toml", "main.nomi", "main_test.nomi", "urls.nomi", "urls.go", "hash.nomi", "hash.go"} {
				data, err := os.ReadFile(filepath.Join(src, file))
				if err != nil {
					t.Fatalf("reading %s: %v", file, err)
				}
				mustWrite(t, filepath.Join(root, file), string(data))
			}
			mustWrite(t, filepath.Join(root, "go.mod"), goMod)
			mustWrite(t, filepath.Join(root, "other-nomi", "go.mod"), "module nomi\n\ngo 1.27.0\n")

			env := buildEnv(t.TempDir())
			entry := filepath.Join(root, "main.nomi")
			const want = "example.com/search tags=[go, nomi]\n" +
				"sha256(nomi) = 0f937e60148316557dbe0dce3e862e77d4d8157bfea24ae4b4c1fa367297c2d9\n"

			run := runProcess(t, env, root, "", nomiBin, "run", entry)
			if run.exit != 0 || run.stdout != want {
				t.Fatalf("nomi run: exit %d, want 0 and %q\n%s", run.exit, want, run.transcript())
			}

			test := runProcess(t, env, root, "", nomiBin, "test", root)
			if test.exit != 0 || !strings.Contains(test.stdout, "test result: ok. 8 passed, 0 failed") {
				t.Fatalf("nomi test: exit %d\n%s", test.exit, test.transcript())
			}

			check := runProcess(t, env, root, "", nomiBin, "check", entry)
			if check.exit != 0 {
				t.Fatalf("nomi check: exit %d\n%s", check.exit, check.transcript())
			}

			bin := filepath.Join(t.TempDir(), "gobindings")
			if b := runProcess(t, env, root, "", nomiBin, "build", entry, "-o", bin); b.exit != 0 {
				t.Fatalf("nomi build: exit %d\n%s", b.exit, b.transcript())
			}
			got := runProcess(t, env, root, "", bin)
			if got != run {
				t.Fatalf("the built binary differs from nomi run\n--- nomi run ---\n%s\n--- binary (exit %d) ---\n%s",
					run.transcript(), got.exit, got.transcript())
			}
		})
	}
}
