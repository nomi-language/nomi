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
	bin := exe(filepath.Join(dir, "nomi"))
	build := exec.Command("go", "build", "-trimpath", "-o", bin, ".")
	build.Dir = pkgDir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building nomi: %v\n%s", err, out)
	}

	// A program whose stdlib use is real: the Date literal is checked against
	// std/calendar and its accessor comes back through the embedded stdlib's
	// declarations.
	proj := filepath.Join(dir, "prog")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(proj, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("nomi.toml", "[package]\nname = \"installcheck\"\nentry_points = [\"main\"]\n")
	write("main.nomi", `import
  std::io
  std::calendar.Date
end

fn main()
  io.print("day ${Date.day(Date"2026-09-12")}")
end
`)

	// An explicit environment, not the parent's. NOMI_STD_PATH is set by this
	// package's own test setup, and inheriting it would hand the child exactly
	// the on-disk stdlib whose absence is the subject.
	run := exec.Command(bin, "run", filepath.Join(proj, "main.nomi"))
	run.Dir = dir
	run.Env = withUserDirs(append([]string{"HOME=" + dir, "PATH=" + noGoPath(t), "TERM=dumb"}, artifactCacheEnv(dir)...))
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
	if err := os.WriteFile(filepath.Join(bootDir, "main.nomi"), []byte(`import std::io

struct App
  port: Int
  context: Context
end

fn boot() -> Result<App, String>
  Ok(App{port: 8080, context: Context.root()})
end

fn main()
  io.print("listening on :${App.port}")
end
`), 0o644); err != nil {
		t.Fatal(err)
	}
	run = exec.Command(bin, "run", filepath.Join(bootDir, "main.nomi"))
	run.Dir = dir
	run.Env = withUserDirs(append([]string{"HOME=" + dir, "PATH=" + noGoPath(t), "TERM=dumb"}, artifactCacheEnv(dir)...))
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
		t.Fatalf("the scrubbed PATH still resolves go, so the run above was not toolchain-free: %s", out)
	}
}

// withUserDirs completes env, which sets HOME, with the variables Windows
// reads instead: USERPROFILE for os.UserHomeDir, LocalAppData for
// os.UserCacheDir and AppData for os.UserConfigDir, each under HOME. On
// other systems they are inert. The last HOME in env decides.
func withUserDirs(env []string) []string {
	home := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "HOME="); ok {
			home = v
		}
	}
	if home == "" {
		return env
	}
	return append(env,
		"USERPROFILE="+home,
		"LocalAppData="+filepath.Join(home, "AppData", "Local"),
		"AppData="+filepath.Join(home, "AppData", "Roaming"),
	)
}
