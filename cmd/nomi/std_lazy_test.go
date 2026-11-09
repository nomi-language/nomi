package main

import (
	"bytes"
	"fmt"
	"io/fs"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/rotation"
)

// The stdlib is built a module at a time: a process builds the prelude and
// what it imports, then each module its programs reach (std.Shared). These
// tests hold a program's answers on such a stdlib to its answers on the
// stdlib built whole (NOMI_STD_WHOLE=1), in fresh processes, so no earlier
// program has built a module for it: `nomi check` and `nomi run`, or `nomi
// test` for a file that declares tests, print the same and exit the same.

// stdLazyFixed are corpus files whose programs reach the stdlib in a way an
// import does not say: an exported function's JSON crossing, a boot, a
// phrase catalog, embedded files.
var stdLazyFixed = []string{
	"tests/01-foundations/standard_streams/streams.nomi",
	"tests/18-ffi-and-dynamic/context_ffi_app/main.nomi",
	"tests/19-locales/phrase_functions_test.nomi",
	"tests/14-modules-and-packaging/embedded_files/embedded_files_test.nomi",
	"std/json.nomi",
	"std/http.nomi",
}

// rotatingStdLazyCount is how many files TestStdLazyRunsAsWholeRotating
// adds to the fixed ones each day.
const rotatingStdLazyCount = 12

// TestStdLazyRunsAsWholeSample runs the fixed files, stdLazyFixed: files
// that reach the stdlib in ways an import does not say (an `export fn`, a
// boot, a phrase catalog, embedded files, a std module's own cases).
// TestStdLazyRunsAsWholeRotating adds rotatingStdLazyCount files of tests/
// and std/ a day. std's TestShared_LazyModulesAreEagerModules holds the
// modules themselves to the stdlib built at once.
func TestStdLazyRunsAsWholeSample(t *testing.T) {
	if testing.Short() {
		t.Skip("runs nomi four times per file; -short")
	}
	runStdLazyFiles(t, stdLazyFixed)
}

// TestStdLazyRunsAsWholeRotating runs a different set of the corpus's and
// the stdlib's files every UTC day (internal/rotation).
func TestStdLazyRunsAsWholeRotating(t *testing.T) {
	if testing.Short() {
		t.Skip("runs nomi four times per file; -short")
	}
	root := repoRoot(t)
	var all []string
	for _, dir := range []string{"tests", "std"} {
		_ = filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(path, ".nomi") && !strings.Contains(path, "_fixtures") {
				rel, _ := filepath.Rel(root, path)
				all = append(all, filepath.ToSlash(rel))
			}
			return nil
		})
	}
	slices.Sort(all)
	if len(all) < 500 {
		t.Fatalf("found %d files under tests and std; the walk is not reading the corpus", len(all))
	}
	set := rotation.For(t, "./cmd/nomi", rotatingStdLazyCount)
	var files []string
	for _, seed := range set.Seeds() {
		files = append(files, all[rand.New(rand.NewSource(seed)).Intn(len(all))])
	}
	if !t.Run("files", func(t *testing.T) { runStdLazyFiles(t, files) }) {
		t.Fatal(set.Failure(set.Start))
	}
}

// runStdLazyFiles checks and runs each file, given by its path under the
// repository, on a stdlib built a module at a time and on one built whole.
func runStdLazyFiles(t *testing.T, files []string) {
	root := repoRoot(t)
	// One stdlib artifact for every process, made once: each run starts
	// with no program kept and this std/ directory.
	seed := t.TempDir()
	if out, err := runStdLazy(t, seed, "", root, false, "check", filepath.Join(root, stdLazyFixed[0])); err != nil {
		t.Fatalf("the seed run failed: %v\n%s", err, out)
	}
	for _, rel := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		run := "run"
		if declaresTests.Match(src) {
			run = "test"
		}
		for _, cmd := range []string{"check", run} {
			lazy, lerr := runStdLazy(t, seed, t.TempDir(), filepath.Dir(path), false, cmd, path)
			whole, werr := runStdLazy(t, seed, t.TempDir(), filepath.Dir(path), true, cmd, path)
			if lazy != whole || fmt.Sprint(lerr) != fmt.Sprint(werr) {
				t.Errorf("nomi %s %s on a stdlib built a module at a time (%v):\n%s\non the stdlib built whole (%v):\n%s", cmd, rel, lerr, lazy, werr, whole)
			}
		}
	}
}

// declaresTests is how a file's source declares tests.
var declaresTests = regexp.MustCompile(`(?m)^\s*test\s+"|//!`)

// durations are what differs between two runs of one program's tests.
var durations = regexp.MustCompile(`\d+(\.\d+)?(ms|µs|s)\b`)

// runStdLazy runs nomi with args in dir, with a cache that starts as seed's
// std/ (cache "" runs on seed itself), under NOMI_STD_WHOLE=1 when whole,
// and answers its output with durations masked.
func runStdLazy(t *testing.T, seed, cache, dir string, whole bool, args ...string) (string, error) {
	t.Helper()
	if cache == "" {
		cache = seed
	} else if err := os.CopyFS(filepath.Join(cache, "std"), os.DirFS(filepath.Join(seed, "std"))); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(nomiBin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "NOMI_ARTIFACT_CACHE="+cache, "NOMI_FFIRUN_CACHE_ROOT="+t.TempDir(), "NOMI_COLOR=never")
	if whole {
		cmd.Env = append(cmd.Env, "NOMI_STD_WHOLE=1")
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return durations.ReplaceAllString(buf.String(), "T"), err
}
