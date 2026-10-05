package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestInstalledBinaryRunsWithoutASourceTree builds `nomi` the way a release
// builds it and runs a stdlib-using program with the compiler's source tree
// out of reach and no `go` on PATH.
//
// Every other test in this tree runs under `go test`, where
// analysis.StdlibPath()'s runtime.Caller probe resolves to the in-repo std/
// directory, so the installed layout, where that probe misses, is a state
// nothing else observes. A loader that propagates StdlibPath's error instead
// of reading it as "this file is not stdlib source" breaks every program a
// downloaded `nomi` runs, including one with no imports, while every other
// test stays green.
//
// `-trimpath` is what reproduces the installed layout here: it rewrites runtime.Caller's answer
// to a module-relative path, so the probe looks for `nomi/std` under the
// process's working directory and misses, exactly as it misses on a user's
// machine where the build path does not exist. Releases pass -trimpath anyway
// (scripts/release.sh).
//
// The scrubbed PATH is the other half. `nomi run` must not need a Go
// toolchain; that promise is the entire reason prebuilt binaries are worth
// shipping, and it is asserted nowhere else.
func TestInstalledBinaryRunsWithoutASourceTree(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain to build with")
	}

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	pkgDir := filepath.Dir(thisFile)

	dir := t.TempDir()
	bin := filepath.Join(dir, "nomi")
	build := exec.Command("go", "build", "-trimpath", "-o", bin, ".")
	build.Dir = pkgDir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building nomi: %v\n%s", err, out)
	}

	// A program whose stdlib use is real: std/calendar parses the literal and
	// the accessors come back through the embedded stdlib's declarations.
	proj := filepath.Join(dir, "prog")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(proj, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("nomi.toml", "[module]\nname = \"installcheck\"\nentry_points = [\"main\"]\n")
	write("main.nomi", `import {
  std/io
  std/calendar.Date
}

fn main() {
  case Date"2026-09-12" {
    Ok(d) -> io.print("day ${Date.day(d)}")
    Err(_) -> io.print("parse failed")
  }
}
`)

	// An explicit environment, not the parent's. NOMI_STD_PATH is set by this
	// package's own test setup, and inheriting it would hand the child exactly
	// the on-disk stdlib whose absence is the subject.
	run := exec.Command(bin, "run", filepath.Join(proj, "main.nomi"))
	run.Dir = dir
	run.Env = []string{"HOME=" + dir, "PATH=/usr/bin:/bin", "TERM=dumb"}
	out, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("nomi run failed with no stdlib source tree and no go on PATH: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "day 12" {
		t.Fatalf("nomi run printed %q, want %q", got, "day 12")
	}

	// A program with a boot, on the VM. Without the stdlib on disk the
	// prelude reaches a user file as a parent scope rather than as prepended
	// imports, and the IR builder's std anchors once missed it there: the boot
	// result's `Context` field stopped being lowerable and the VM refused the
	// program as BLOCKED. The tour's wasm runs in exactly this mode.
	bootDir := filepath.Join(dir, "bootprog")
	if err := os.MkdirAll(bootDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bootDir, "main.nomi"), []byte(`import std/io

struct App {
  port: Int
  context: Context
}

fn boot(): App {
  App{port: 8080, context: Context.root()}
}

fn main() {
  io.print("listening on :${App.port}")
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	run = exec.Command(bin, "run", filepath.Join(bootDir, "main.nomi"))
	run.Dir = dir
	run.Env = []string{"HOME=" + dir, "PATH=/usr/bin:/bin", "TERM=dumb"}
	out, err = run.CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "listening on :8080" {
		t.Fatalf("nomi run of a boot program with no stdlib source tree: %v\n%s", err, out)
	}

	// Anti-vacuity: the scrubbed PATH above proves nothing unless `go` is
	// genuinely unreachable through it. A PATH that still resolved `go` would
	// let the assertion pass for the wrong reason.
	probe := exec.Command("/usr/bin/env", "go", "version")
	probe.Env = run.Env
	if out, err := probe.CombinedOutput(); err == nil {
		t.Fatalf("PATH=/usr/bin:/bin still resolves go, so the run above was not toolchain-free: %s", out)
	}
}

// TestDevelopmentBuildWithoutSourceRunsAGoFFIProjectOnlyWithACheckout pins
// what a development build of `nomi` does with Go FFI once the tree it was
// built from is out of reach.
//
// `nomi run` on a project with Go FFI goes through internal/ffirun, which
// stages a wrapper importing the compiler's own packages (nomi/vmhost, with
// adapters generated for the user's bindings), so `go build` compiles them
// from source. A versioned `nomi` fetches that source through Go
// (TestVersionedInstallRunsAGoFFIProject); a development build has no version
// to fetch. So it fails, and what is asserted is that it names the situation
// instead of reporting a stat on a path the user never wrote, and that the way
// out it names, NOMI_COMPILER_SOURCE, works.
func TestDevelopmentBuildWithoutSourceRunsAGoFFIProjectOnlyWithACheckout(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go toolchain to build with")
	}

	bin, dir := buildTrimmedNomi(t)
	proj := filepath.Join(dir, "ffiprog")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(proj, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("nomi.toml", "[module]\nname = \"fficheck\"\nentry_points = [\"main\"]\n")
	write("go.mod", "module fficheck\n\ngo 1.27.0\n")
	write("shout.go", "package fficheck\n\nimport \"strings\"\n\n"+
		"func Shout(s string) string { return strings.ToUpper(s) }\n")
	write("ffi.nomi", "gopkg \"fficheck\" as shout\n\n"+
		"pub fn shout_it(s: String): String go shout.Shout\n")
	write("main.nomi", `import {
  std/io
  ffi
}

fn main() {
  io.print(ffi.shout_it("quiet"))
}
`)

	env := installedEnv(t, dir, goBin)

	run := exec.Command(bin, "run", filepath.Join(proj, "main.nomi"))
	run.Dir = dir
	run.Env = env
	got, err := run.CombinedOutput()
	if err == nil {
		t.Fatalf("`nomi run` on a Go-FFI project SUCCEEDED from a development build with no "+
			"source tree and no %s:\n%s", "NOMI_COMPILER_SOURCE", got)
	}
	if !strings.Contains(string(got), "development build") ||
		!strings.Contains(string(got), "NOMI_COMPILER_SOURCE") {
		t.Fatalf("`nomi run` failed without naming why. Got:\n%s\n\nThe message should say "+
			"that this is a development build without its source and name NOMI_COMPILER_SOURCE, "+
			"because the predecessor reported `stat nomi/go.mod: no such file or directory`, "+
			"a path the user never wrote and no way forward.", got)
	}

	run = exec.Command(bin, "run", filepath.Join(proj, "main.nomi"))
	run.Dir = dir
	run.Env = append(env, "NOMI_COMPILER_SOURCE="+repoRoot(t))
	got, err = run.CombinedOutput()
	if err != nil || strings.TrimSpace(string(got)) != "QUIET" {
		t.Fatalf("`nomi run` with NOMI_COMPILER_SOURCE naming the checkout: %v\n%s", err, got)
	}
}

// buildTrimmedNomi builds a development `nomi` with no source tree in reach
// and returns the binary plus a scratch directory with no source tree above
// it.
//
// `-trimpath` rewrites every `runtime.Caller` answer to a module-relative
// path, which is what puts the compiler's own source out of reach the way it
// is out of reach on a user's machine. `-buildvcs=false` keeps it a
// development build whatever state this checkout is in: built at a clean
// tagged commit it would carry that version and fetch the compiler module
// instead.
func buildTrimmedNomi(t *testing.T) (bin, dir string) {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir = t.TempDir()
	bin = filepath.Join(dir, "nomi")
	build := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-o", bin, ".")
	build.Dir = filepath.Dir(thisFile)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building nomi: %v\n%s", err, out)
	}
	return bin, dir
}

// installedEnv is the environment a `nomi build` runs under in these tests:
// HOME inside the scratch directory, `go` reachable and nothing else on PATH,
// and nothing inherited: NOMI_STD_PATH is set by this package's test setup and
// inheriting it would hand the child the source tree whose absence is the
// subject.
//
// GOMODCACHE and GOCACHE are taken from the parent's `go env` rather than left
// to land under the scratch HOME. Two reasons, and neither weakens what is
// being tested. The module cache holds rt's one dependency, so a private one
// would make every run of this test fetch github.com/rivo/uniseg from the
// network. And Go writes the module cache read-only, so t.TempDir's cleanup
// of a private one fails with `permission denied`, a test failure with
// nothing to do with the subject. The subject is
// the compiler's source tree; Go's own caches are orthogonal and a real
// installation has them too.
func installedEnv(t *testing.T, dir, goBin string) []string {
	t.Helper()
	env := []string{"HOME=" + dir, "PATH=" + filepath.Dir(goBin) + ":/usr/bin:/bin", "TERM=dumb"}
	for _, name := range []string{"GOMODCACHE", "GOCACHE"} {
		out, err := exec.Command(goBin, "env", name).Output()
		if err != nil {
			t.Fatalf("reading %s from the parent toolchain: %v", name, err)
		}
		if v := strings.TrimSpace(string(out)); v != "" {
			env = append(env, name+"="+v)
		}
	}
	return env
}
