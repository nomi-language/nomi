package main

// `nomi build` from a release: a `nomi` with no compiler source and no Go
// toolchain, beside the prebuilt `nomi-runner` its archive ships, and
// downloading another platform's runner from a release served locally. See
// internal/ffirun/prebuilt.go.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/nomi-language/nomi/internal/ffirun"
	"github.com/nomi-language/nomi/vmrunner"
)

// releaseTestVersion is the tag the fake release is cut as.
const releaseTestVersion = "v0.0.0-reltest"

// fakeRelease is a `nomi` built the way scripts/release.sh builds it, the
// runners it could be installed or served with, and the platform the cross
// runner is for.
type fakeReleaseBuild struct {
	// install holds nomi and a matching nomi-runner, as an unpacked archive.
	install string
	// bare holds the same nomi and no runner.
	bare string
	// mismatch holds the same nomi and a runner with no VCS stamp.
	mismatch string
	// crossRunner is a runner for cross.
	crossRunner string
	cross       string
	// versioned is set when nomi carries a version it would fetch the
	// compiler module at: this checkout is clean at a tag, as a release
	// build is. Its Go-side refusals are then the missing Go toolchain
	// rather than the missing source tree.
	versioned bool
}

var (
	releaseOnce  sync.Once
	releaseBuilt fakeReleaseBuild
	releaseErr   error
)

// crossTarget is a platform other than this one.
func crossTarget() (goos, goarch string) {
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		return "linux", "arm64"
	}
	return "linux", "amd64"
}

// releaseBuild builds the fake release once per test process.
func releaseBuild(t *testing.T) fakeReleaseBuild {
	t.Helper()
	if testing.Short() {
		t.Skip("builds release-shaped binaries; -short")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain to build with")
	}
	releaseOnce.Do(func() {
		root := repoRoot(t)
		base := filepath.Join(filepath.Dir(nomiBin), "release")
		b := fakeReleaseBuild{
			install:  filepath.Join(base, "install"),
			bare:     filepath.Join(base, "bare"),
			mismatch: filepath.Join(base, "mismatch"),
		}
		goos, goarch := crossTarget()
		b.cross = goos + "/" + goarch
		b.crossRunner = filepath.Join(base, "cross", "nomi-runner")
		ldflags := "-s -w -X github.com/nomi-language/nomi/internal/ffirun.ReleaseVersion=" + releaseTestVersion
		steps := []struct {
			out, pkg string
			env      []string
			extra    []string
		}{
			{filepath.Join(b.install, "nomi"), "./cmd/nomi", nil, []string{"-ldflags", ldflags}},
			{filepath.Join(b.install, "nomi-runner"), "./cmd/nomi-runner", nil, []string{"-ldflags", "-s -w"}},
			{filepath.Join(b.bare, "nomi"), "./cmd/nomi", nil, []string{"-ldflags", ldflags}},
			{filepath.Join(b.mismatch, "nomi"), "./cmd/nomi", nil, []string{"-ldflags", ldflags}},
			{filepath.Join(b.mismatch, "nomi-runner"), "./cmd/nomi-runner", nil, []string{"-buildvcs=false", "-ldflags", "-s -w"}},
			{b.crossRunner, "./cmd/nomi-runner", []string{"GOOS=" + goos, "GOARCH=" + goarch}, []string{"-ldflags", "-s -w"}},
		}
		for _, s := range steps {
			args := append(append([]string{"build", "-trimpath"}, s.extra...), "-o", s.out, s.pkg)
			cmd := exec.Command("go", args...)
			cmd.Dir = root
			cmd.Env = append(append(os.Environ(), "CGO_ENABLED=0"), s.env...)
			if out, err := cmd.CombinedOutput(); err != nil {
				releaseErr = fmt.Errorf("go %v: %v\n%s", args, err, out)
				return
			}
		}
		info, err := buildinfo.ReadFile(filepath.Join(b.install, "nomi"))
		if err != nil {
			releaseErr = err
			return
		}
		b.versioned = ffirun.CompilerModuleVersion(info) != ""
		releaseBuilt = b
	})
	if releaseErr != nil {
		t.Fatal(releaseErr)
	}
	return releaseBuilt
}

// releaseEnv is an installed machine's environment: no Go on PATH, nothing
// inherited, an isolated cache.
func releaseEnv(t *testing.T, cacheRoot string, extra ...string) []string {
	t.Helper()
	env := append([]string{"HOME=" + t.TempDir(), "PATH=/usr/bin:/bin", "TERM=dumb",
		"NOMI_FFIRUN_CACHE_ROOT=" + cacheRoot, "NOMI_COLOR=never"}, extra...)
	probe := exec.Command("/usr/bin/env", "go", "version")
	probe.Env = env
	if out, err := probe.CombinedOutput(); err == nil {
		t.Fatalf("PATH=/usr/bin:/bin resolves go, so this test is not toolchain-free: %s", out)
	}
	return env
}

const releaseHello = "import std/io\n\nfn main() {\n  io.print(\"hi\")\n}\n"

// TestBuildFromRelease_TheInstalledRunnerBuildsEveryProgram: a release-shaped
// `nomi` with its archive's nomi-runner beside it, no compiler source and no
// Go, builds every whole-program corpus program, and each binary
// matches its golden record. A program with Go FFI or importing std/compiler
// is refused with the reason, and nothing is built from source or downloaded.
func TestBuildFromRelease_TheInstalledRunnerBuildsEveryProgram(t *testing.T) {
	b := releaseBuild(t)
	cache := t.TempDir()
	env := releaseEnv(t, cache)
	ffi, compiler := 0, 0
	ffiRefusal := "this project has Go bindings, and `nomi build` links them into the executable"
	compilerRefusal := "this program imports std/compiler, whose hosts link the whole front end"
	if b.versioned {
		ffiRefusal = "no Go toolchain: this project has Go FFI, so `nomi build` compiles a runner"
		compilerRefusal = "no Go toolchain: `nomi build` builds the runner"
	}
	built := buildMatchesRecords(t, filepath.Join(b.install, "nomi"), env,
		[]recordPopulation{{"corpus", "tests"}},
		func(stderr string) bool {
			switch {
			case strings.Contains(stderr, ffiRefusal):
				ffi++
				return true
			case strings.Contains(stderr, compilerRefusal):
				compiler++
				return true
			}
			return false
		})
	if built == 0 {
		t.Fatal("no program was built, so this test compared nothing")
	}
	if ffi == 0 {
		t.Fatal("no FFI program was refused; tests/18-ffi-and-dynamic has Go FFI projects, so the refusal went unexercised")
	}
	entries, err := os.ReadDir(cache)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if _, err := os.Stat(filepath.Join(cache, e.Name(), "nomi-runner")); err == nil || e.Name() == "runners" {
			t.Fatalf("the cache holds %s: a runner was built or downloaded instead of the installed one", e.Name())
		}
	}
	t.Logf("%d programs built from the installed runner; refused: %d with Go FFI, %d importing std/compiler",
		built, ffi, compiler)
}

// TestBuildFromRelease_AVariantNamedLikeAnInterfaceBuilds: a release nomi has
// no stdlib source tree on disk, and it must resolve names as a checkout's
// does. It used to skip the prelude imports there, so a variant named `Debug`
// shadowed the interface and the build failed with "impl block: 'Debug' is
// not an interface".
func TestBuildFromRelease_AVariantNamedLikeAnInterfaceBuilds(t *testing.T) {
	b := releaseBuild(t)
	dir := t.TempDir()
	entry := filepath.Join(dir, "levels.nomi")
	mustWrite(t, entry, `import std/io

enum Level {
  Info
  Debug
  Display
}

derive Display for Level

fn main() {
  io.print(Debug.inspect(Level.Debug))
  io.print(Display.to_string(Level.Display))
}
`)
	env := releaseEnv(t, t.TempDir())
	out := filepath.Join(t.TempDir(), "levels")
	got := runProcess(t, env, dir, "", filepath.Join(b.install, "nomi"), "build", entry, "-o", out)
	if got.exit != 0 {
		t.Fatalf("exit %d:\n%s", got.exit, got.transcript())
	}
	ran := runProcess(t, env, dir, "", out)
	if ran.exit != 0 || ran.stdout != "Debug\nDisplay\n" {
		t.Fatalf("exit %d, want Debug and Display printed:\n%s", ran.exit, ran.transcript())
	}
}

// TestBuildFromRelease_AMismatchedRunnerIsRefused: a runner beside nomi that
// does not come from nomi's commit is refused by name, and nothing is written.
func TestBuildFromRelease_AMismatchedRunnerIsRefused(t *testing.T) {
	b := releaseBuild(t)
	dir := t.TempDir()
	entry := filepath.Join(dir, "hello.nomi")
	mustWrite(t, entry, releaseHello)
	out := filepath.Join(t.TempDir(), "hello")
	got := runProcess(t, releaseEnv(t, t.TempDir()), dir, "", filepath.Join(b.mismatch, "nomi"), "build", entry, "-o", out)
	want := "the runner at " + filepath.Join(b.mismatch, "nomi-runner") + " was built from an unrecorded commit and this nomi from commit "
	if got.exit == 0 || !strings.Contains(got.stderr, want) ||
		!strings.Contains(got.stderr, "Install nomi and nomi-runner from one release archive") {
		t.Fatalf("exit %d, want the refusal %q:\n%s", got.exit, want, got.transcript())
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("a refused build left something at -o (%v)", err)
	}
}

// TestBuildFromRelease_StdCompilerNeedsGoToBuildItsRunner: releases ship only
// the plain runner, so with no Go a program importing std/compiler is refused
// with why: a release build needs Go to build that runner from the compiler
// module, and a development build without its tree cannot build it at all.
func TestBuildFromRelease_StdCompilerNeedsGoToBuildItsRunner(t *testing.T) {
	b := releaseBuild(t)
	dir := t.TempDir()
	entry := filepath.Join(dir, "nested.nomi")
	mustWrite(t, entry, `import {
  std/compiler
  std/io
}

fn main() {
  io.print(Debug.inspect(compiler.check("fn main() {\n  missing\n}\n")))
}
`)
	out := filepath.Join(t.TempDir(), "nested")
	got := runProcess(t, releaseEnv(t, t.TempDir()), dir, "", filepath.Join(b.install, "nomi"), "build", entry, "-o", out)
	want := []string{"this program imports std/compiler", "Releases ship only the plain runner"}
	if b.versioned {
		want = []string{"no Go toolchain: `nomi build` builds the runner", "Install Go"}
	}
	if got.exit == 0 || !strings.Contains(got.stderr, want[0]) || !strings.Contains(got.stderr, want[1]) {
		t.Fatalf("exit %d:\n%s", got.exit, got.transcript())
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("a refused build left something at -o (%v)", err)
	}
}

// TestBuildFromRelease_GoBindingsRefuseWithoutGo: a release-shaped `nomi`
// on a machine with no Go refuses every command that has to compile a
// project's Go side, exits non-zero, and says what is missing.
//
// What is missing depends on what this checkout stamps into the binary. Built
// at a clean tagged commit, as a release is, it is versioned and would fetch
// the compiler module, so the refusal is the missing Go toolchain. Built
// anywhere else it is a development build whose tree -trimpath hides, so the
// refusal names NOMI_COMPILER_SOURCE.
func TestBuildFromRelease_GoBindingsRefuseWithoutGo(t *testing.T) {
	b := releaseBuild(t)
	nomi := filepath.Join(b.install, "nomi")
	info, err := buildinfo.ReadFile(nomi)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"development build", "NOMI_COMPILER_SOURCE"}
	if ffirun.CompilerModuleVersion(info) != "" {
		want = []string{"no Go toolchain", "Install Go"}
	}
	project := filepath.Join(repoRoot(t), "cmd", "nomi", "testdata", "go_bindings")
	entry := filepath.Join(project, "main.nomi")
	for _, args := range [][]string{
		{"run", entry},
		{"test", project},
		{"check", entry},
		{"build", entry, "-o", filepath.Join(t.TempDir(), "gobindings")},
	} {
		// `nomi test` reports a file it cannot run as that file's failure on
		// stdout; the other commands write stderr.
		got := runProcess(t, releaseEnv(t, t.TempDir()), project, "", nomi, args...)
		out := got.stdout + got.stderr
		if got.exit == 0 || !strings.Contains(out, want[0]) || !strings.Contains(out, want[1]) {
			t.Fatalf("nomi %s: exit %d, want a refusal naming %q and %q:\n%s",
				args[0], got.exit, want[0], want[1], got.transcript())
		}
	}
}

func tarGzFiles(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for name, data := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sha256Hex(data []byte) string {
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:])
}

// localRelease serves the fake release's archive for the cross target and its
// checksum manifest, as GitHub serves a release's assets.
func localRelease(t *testing.T, b fakeReleaseBuild, corruptSum bool) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	runner, err := os.ReadFile(b.crossRunner)
	if err != nil {
		t.Fatal(err)
	}
	goos, goarch := crossTarget()
	archive := fmt.Sprintf("nomi_%s_%s_%s.tar.gz", releaseTestVersion, goos, goarch)
	data := tarGzFiles(t, map[string][]byte{
		"./nomi":        []byte("not the cli"),
		"./nomi-lsp":    []byte("not the lsp"),
		"./nomi-runner": runner,
	})
	sum := sha256Hex(data)
	if corruptSum {
		sum = strings.Repeat("0", 64)
	}
	files := map[string][]byte{
		"/" + releaseTestVersion + "/" + archive:                                    data,
		"/" + releaseTestVersion + "/nomi_" + releaseTestVersion + "_checksums.txt": []byte(sum + "  ./" + archive + "\n"),
	}
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		d, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(d)
	}))
	t.Cleanup(srv.Close)
	return srv, &requests
}

// TestBuildFromRelease_CrossTargetDownloadsTheRunner: `--target` for a
// platform with no runner installed downloads that platform's archive from
// the same release, verifies it against the checksum file, caches its
// nomi-runner, and reuses the cache without asking again. A bad checksum
// installs nothing, and an unreachable release names the URL.
func TestBuildFromRelease_CrossTargetDownloadsTheRunner(t *testing.T) {
	b := releaseBuild(t)
	nomi := filepath.Join(b.install, "nomi")
	dir := t.TempDir()
	entry := filepath.Join(dir, "hello.nomi")
	mustWrite(t, entry, releaseHello)
	goos, goarch := crossTarget()
	archive := fmt.Sprintf("/%s/nomi_%s_%s_%s.tar.gz", releaseTestVersion, releaseTestVersion, goos, goarch)

	t.Run("download, then cache", func(t *testing.T) {
		srv, requests := localRelease(t, b, false)
		cache := t.TempDir()
		env := releaseEnv(t, cache, "NOMI_RELEASE_BASE_URL="+srv.URL)
		out := filepath.Join(t.TempDir(), "hello")
		got := runProcess(t, env, dir, "", nomi, "build", "--target", b.cross, entry, "-o", out)
		want := "nomi build: downloading the " + b.cross + " runner from " + srv.URL + archive + "\n"
		if got.exit != 0 || got.stderr != want {
			t.Fatalf("exit %d, want only %q on stderr:\n%s", got.exit, want, got.transcript())
		}
		info, err := buildinfo.ReadFile(out)
		if err != nil {
			t.Fatalf("the %s build is not a Go executable: %v", b.cross, err)
		}
		gotOS, gotArch := "", ""
		for _, s := range info.Settings {
			switch s.Key {
			case "GOOS":
				gotOS = s.Value
			case "GOARCH":
				gotArch = s.Value
			}
		}
		if info.Path != "github.com/nomi-language/nomi/cmd/nomi-runner" || gotOS != goos || gotArch != goarch {
			t.Fatalf("the %s build is %s %v", b.cross, info.Path, info.Settings)
		}
		if _, err := vmrunner.ReadImage(out); err != nil {
			t.Fatalf("the %s build carries no image: %v", b.cross, err)
		}
		cached := filepath.Join(cache, "runners", releaseTestVersion, goos+"_"+goarch, "nomi-runner")
		if _, err := os.Stat(cached); err != nil {
			t.Fatalf("the downloaded runner is not cached at %s: %v", cached, err)
		}
		first := requests.Load()
		again := runProcess(t, env, dir, "", nomi, "build", "--target", b.cross, entry, "-o", out)
		if again.exit != 0 || again.stderr != "" || requests.Load() != first {
			t.Fatalf("a second build asked the release again (%d requests, then %d), exit %d:\n%s",
				first, requests.Load(), again.exit, again.transcript())
		}
	})
	t.Run("bad checksum", func(t *testing.T) {
		srv, _ := localRelease(t, b, true)
		cache := t.TempDir()
		out := filepath.Join(t.TempDir(), "hello")
		got := runProcess(t, releaseEnv(t, cache, "NOMI_RELEASE_BASE_URL="+srv.URL), dir, "", nomi,
			"build", "--target", b.cross, entry, "-o", out)
		if got.exit == 0 || !strings.Contains(got.stderr, srv.URL+archive+" does not match the release's checksum file") {
			t.Fatalf("exit %d:\n%s", got.exit, got.transcript())
		}
		if _, err := os.Stat(out); !os.IsNotExist(err) {
			t.Fatalf("a refused build left something at -o (%v)", err)
		}
		if _, err := os.Stat(filepath.Join(cache, "runners", releaseTestVersion, goos+"_"+goarch, "nomi-runner")); !os.IsNotExist(err) {
			t.Fatalf("a bad checksum cached a runner (%v)", err)
		}
	})
	t.Run("no network", func(t *testing.T) {
		closed := httptest.NewServer(http.NotFoundHandler())
		base := closed.URL
		closed.Close()
		got := runProcess(t, releaseEnv(t, t.TempDir(), "NOMI_RELEASE_BASE_URL="+base), dir, "", nomi,
			"build", "--target", b.cross, entry, "-o", filepath.Join(t.TempDir(), "hello"))
		if got.exit == 0 || !strings.Contains(got.stderr, base+"/"+releaseTestVersion+"/nomi_"+releaseTestVersion+"_checksums.txt") ||
			!strings.Contains(got.stderr, "Connect to the network and re-run") {
			t.Fatalf("exit %d:\n%s", got.exit, got.transcript())
		}
	})
}
