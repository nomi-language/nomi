package runners

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCheckRunner(t *testing.T) {
	const rev = "0cf65c8a453eb7b52dc6e743ed90d1a6aa5f33c5"
	const other = "86190a45e0000000000000000000000000000000"
	self := buildIdentity{Path: "github.com/nomi-language/nomi/cmd/nomi", Revision: rev, GOOS: "darwin", GOARCH: "arm64"}
	runner := buildIdentity{Path: runnerMainPath, Revision: rev, GOOS: "linux", GOARCH: "amd64"}
	target := Target{GOOS: "linux", GOARCH: "amd64"}
	with := func(f func(*buildIdentity)) buildIdentity {
		r := runner
		f(&r)
		return r
	}
	cases := []struct {
		name         string
		self, runner buildIdentity
		want         string // "" accepts
	}{
		{"same commit", self, runner, ""},
		{"another commit", self, with(func(r *buildIdentity) { r.Revision = other }),
			"built from commit 86190a45e000 and this nomi from commit 0cf65c8a453e"},
		{"dirty against clean", self, with(func(r *buildIdentity) { r.Modified = true }),
			"built from commit 0cf65c8a453e-dirty and this nomi from commit 0cf65c8a453e"},
		{"no commit on the runner", self, with(func(r *buildIdentity) { r.Revision = "" }),
			"built from an unrecorded commit"},
		{"no commit on nomi", buildIdentity{Path: "github.com/nomi-language/nomi/cmd/nomi"}, runner, "this nomi records no source commit"},
		{"another platform", self, with(func(r *buildIdentity) { r.GOOS = "darwin" }), "is built for darwin/amd64, not linux/amd64"},
		{"not a runner", self, with(func(r *buildIdentity) { r.Path = "github.com/nomi-language/nomi/cmd/nomi" }), "is not a nomi-vm"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkRunner(c.self, c.runner, "/r/nomi-vm", target, Variant{})
			if c.want == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want an error containing %q", err, c.want)
			}
		})
	}
}

// TestCheckRunner_TheReactorNamesItsOwnPackage: the wasip1 reactor must be
// built from cmd/nomi-vm-reactor, and the command runner from cmd/nomi-vm,
// so neither is taken for the other.
func TestCheckRunner_TheReactorNamesItsOwnPackage(t *testing.T) {
	const rev = "0cf65c8a453eb7b52dc6e743ed90d1a6aa5f33c5"
	self := buildIdentity{Path: "github.com/nomi-language/nomi/cmd/nomi", Revision: rev}
	target := Target{GOOS: "wasip1", GOARCH: "wasm"}
	reactor := buildIdentity{Path: reactorMainPath, Revision: rev, GOOS: "wasip1", GOARCH: "wasm"}
	command := buildIdentity{Path: runnerMainPath, Revision: rev, GOOS: "wasip1", GOARCH: "wasm"}
	if err := checkRunner(self, reactor, "/r/nomi-vm-reactor", target, Variant{Reactor: true}); err != nil {
		t.Fatalf("the reactor was refused: %v", err)
	}
	if err := checkRunner(self, command, "/r/nomi-vm-reactor", target, Variant{Reactor: true}); err == nil ||
		!strings.Contains(err.Error(), "is not a nomi-vm-reactor") {
		t.Fatalf("the command runner was taken for the reactor: %v", err)
	}
	if err := checkRunner(self, reactor, "/r/nomi-vm", target, Variant{}); err == nil ||
		!strings.Contains(err.Error(), "is not a nomi-vm") {
		t.Fatalf("the reactor was taken for the command runner: %v", err)
	}
}

// TestCheckRunner_TheTagsNameTheVariant: a runner's build tags are what
// tell std/compiler's variant and the lean one from the plain runner, so
// none is taken for another, and the order the tags were written in does
// not matter.
func TestCheckRunner_TheTagsNameTheVariant(t *testing.T) {
	const rev = "0cf65c8a453eb7b52dc6e743ed90d1a6aa5f33c5"
	self := buildIdentity{Path: "github.com/nomi-language/nomi/cmd/nomi", Revision: rev}
	target := Target{GOOS: "linux", GOARCH: "amd64"}
	built := func(tags string) buildIdentity {
		return buildIdentity{Path: runnerMainPath, Revision: rev, GOOS: "linux", GOARCH: "amd64", Tags: sortedTags(tags)}
	}
	compiler := Variant{Compiler: true}
	lean := Variant{Compiler: true, NoNetwork: true}
	if err := checkRunner(self, built(compiler.buildTags()), "/r/nomi-vm-compiler", target, compiler); err != nil {
		t.Fatalf("the compiler runner was refused: %v", err)
	}
	if err := checkRunner(self, built("nomi_nowasm,nomi_nonet,nomi_compiler"), "/r/nomi-vm-compiler-nonet", target, lean); err != nil {
		t.Fatalf("the lean compiler runner, its tags in another order, was refused: %v", err)
	}
	cases := []struct {
		name   string
		runner buildIdentity
		v      Variant
		want   string
	}{
		{"a plain runner for std/compiler", built(""), compiler,
			"/r/bin is not a nomi-vm-compiler (it was built with no build tags, and a nomi-vm-compiler with the tags nomi_compiler)"},
		{"the compiler runner for a plain program", built(compiler.buildTags()), Variant{},
			"/r/bin is not a nomi-vm (it was built with the tags nomi_compiler, and a nomi-vm with no build tags)"},
		{"the lean runner for std/compiler", built(Variant{NoNetwork: true}.buildTags()), lean,
			"is not a nomi-vm-compiler-nonet (it was built with the tags nomi_nonet,nomi_nowasm"},
		{"the lean compiler runner for the full one", built(lean.buildTags()), compiler,
			"is not a nomi-vm-compiler (it was built with the tags nomi_compiler,nomi_nonet,nomi_nowasm"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkRunner(self, c.runner, "/r/bin", target, c.v)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want an error containing %q", err, c.want)
			}
		})
	}
}

func TestChecksumFor(t *testing.T) {
	sum := strings.Repeat("ab", 32)
	manifest := sum + "  ./nomi_v1_linux_amd64.tar.gz\n" + strings.Repeat("cd", 32) + " *nomi_v1_windows_amd64.zip\n"
	if got, ok := checksumFor([]byte(manifest), "nomi_v1_linux_amd64.tar.gz"); !ok || got != sum {
		t.Fatalf("./-prefixed line: %q %v", got, ok)
	}
	if got, ok := checksumFor([]byte(manifest), "nomi_v1_windows_amd64.zip"); !ok || got != strings.Repeat("cd", 32) {
		t.Fatalf("*-marked line: %q %v", got, ok)
	}
	if _, ok := checksumFor([]byte(manifest), "nomi_v1_darwin_arm64.tar.gz"); ok {
		t.Fatal("found an archive the manifest does not list")
	}
}

// fakeRelease serves one version's archives and checksum manifest the way
// scripts/release.sh lays them out, and counts requests.
type fakeRelease struct {
	files    map[string][]byte
	requests atomic.Int64
}

func (f *fakeRelease) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.requests.Add(1)
	data, ok := f.files[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	_, _ = w.Write(data)
}

func tarGz(t *testing.T, files map[string][]byte) []byte {
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

func zipOf(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sha(data []byte) string {
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:])
}

// TestDownloadRunner fetches a runner from a local fake release: the right
// member is extracted from a .tar.gz and a .zip, a bad checksum installs
// nothing, and a missing archive or an unreachable server names the URL.
func TestDownloadRunner(t *testing.T) {
	const v = "v9.9.9"
	linux := tarGz(t, map[string][]byte{
		"./nomi":     []byte("the cli"),
		"./nomi-lsp": []byte("the lsp"),
		"./nomi-vm":  []byte("the linux runner"),
	})
	windows := zipOf(t, map[string][]byte{
		"nomi.exe":    []byte("the cli"),
		"nomi-vm.exe": []byte("the windows runner"),
	})
	rel := &fakeRelease{files: map[string][]byte{
		"/" + v + "/nomi_v9.9.9_linux_amd64.tar.gz": linux,
		"/" + v + "/nomi_v9.9.9_windows_amd64.zip":  windows,
		"/" + v + "/nomi_v9.9.9_checksums.txt": []byte(fmt.Sprintf("%s  ./nomi_v9.9.9_linux_amd64.tar.gz\n%s  ./nomi_v9.9.9_windows_amd64.zip\n%s  ./nomi_v9.9.9_linux_arm64.tar.gz\n",
			sha(linux), sha(windows), strings.Repeat("0", 64))),
		"/" + v + "/nomi_v9.9.9_linux_arm64.tar.gz": linux,
	}}
	srv := httptest.NewServer(rel)
	defer srv.Close()

	t.Run("tar.gz", func(t *testing.T) {
		dest := filepath.Join(t.TempDir(), "nomi-vm")
		var progress bytes.Buffer
		if err := downloadRunner(srv.URL, v, Target{"linux", "amd64"}, Variant{}, dest, &progress); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(dest); string(got) != "the linux runner" {
			t.Fatalf("extracted %q", got)
		}
		if want := srv.URL + "/v9.9.9/nomi_v9.9.9_linux_amd64.tar.gz"; !strings.Contains(progress.String(), want) {
			t.Fatalf("progress %q does not name %s", progress.String(), want)
		}
		// Windows has no executable bit; a file runs by its .exe name.
		if info, _ := os.Stat(dest); runtime.GOOS != "windows" && info.Mode().Perm()&0o100 == 0 {
			t.Fatalf("the runner is not executable: %v", info.Mode())
		}
	})
	t.Run("zip", func(t *testing.T) {
		dest := filepath.Join(t.TempDir(), "nomi-vm.exe")
		if err := downloadRunner(srv.URL, v, Target{"windows", "amd64"}, Variant{}, dest, nil); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(dest); string(got) != "the windows runner" {
			t.Fatalf("extracted %q", got)
		}
	})
	t.Run("bad checksum", func(t *testing.T) {
		dir := t.TempDir()
		dest := filepath.Join(dir, "nomi-vm")
		err := downloadRunner(srv.URL, v, Target{"linux", "arm64"}, Variant{}, dest, nil)
		if err == nil || !strings.Contains(err.Error(), "does not match the release's checksum file") ||
			!strings.Contains(err.Error(), "Nothing was installed") {
			t.Fatalf("got %v", err)
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Fatalf("a bad checksum left %d file(s) behind", len(entries))
		}
	})
	t.Run("not in the manifest", func(t *testing.T) {
		err := downloadRunner(srv.URL, v, Target{"darwin", "arm64"}, Variant{}, filepath.Join(t.TempDir(), "nomi-vm"), nil)
		if err == nil || !strings.Contains(err.Error(), "lists no nomi_v9.9.9_darwin_arm64.tar.gz") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("no such release", func(t *testing.T) {
		err := downloadRunner(srv.URL, "v0.0.1", Target{"linux", "amd64"}, Variant{}, filepath.Join(t.TempDir(), "nomi-vm"), nil)
		want := srv.URL + "/v0.0.1/nomi_v0.0.1_checksums.txt: HTTP 404"
		if err == nil || !strings.Contains(err.Error(), want) ||
			!strings.Contains(err.Error(), "check that v0.0.1 is a published release") {
			t.Fatalf("got %v, want it to name %s", err, want)
		}
	})
	t.Run("no network", func(t *testing.T) {
		closed := httptest.NewServer(http.NotFoundHandler())
		base := closed.URL
		closed.Close()
		err := downloadRunner(base, v, Target{"linux", "amd64"}, Variant{}, filepath.Join(t.TempDir(), "nomi-vm"), nil)
		if err == nil || !strings.Contains(err.Error(), base+"/v9.9.9/nomi_v9.9.9_checksums.txt") ||
			!strings.Contains(err.Error(), "Connect to the network") ||
			!strings.Contains(err.Error(), base+"/v9.9.9/nomi_v9.9.9_linux_amd64.tar.gz") {
			t.Fatalf("got %v", err)
		}
	})
}

// TestDownloadRunner_JSWasmBringsItsWasmExec: the js/wasm archive is the
// runner and wasm_exec.js, and both are cached side by side, where WasmExec
// finds the JS; an archive without the JS installs nothing. wasip1/wasm
// needs no JS.
func TestDownloadRunner_JSWasmBringsItsWasmExec(t *testing.T) {
	const v = "v9.9.9"
	js := tarGz(t, map[string][]byte{
		"./nomi-vm":      []byte("the js runner"),
		"./wasm_exec.js": []byte("// the archive's wasm_exec.js"),
	})
	noJS := tarGz(t, map[string][]byte{"./nomi-vm": []byte("the js runner")})
	wasi := tarGz(t, map[string][]byte{
		"./nomi-vm":         []byte("the wasip1 runner"),
		"./nomi-vm-reactor": []byte("the wasip1 reactor"),
	})
	serve := func(jsArchive []byte) *httptest.Server {
		rel := &fakeRelease{files: map[string][]byte{
			"/" + v + "/nomi_v9.9.9_js_wasm.tar.gz":     jsArchive,
			"/" + v + "/nomi_v9.9.9_wasip1_wasm.tar.gz": wasi,
			"/" + v + "/nomi_v9.9.9_checksums.txt": []byte(fmt.Sprintf("%s  ./nomi_v9.9.9_js_wasm.tar.gz\n%s  ./nomi_v9.9.9_wasip1_wasm.tar.gz\n",
				sha(jsArchive), sha(wasi))),
		}}
		srv := httptest.NewServer(rel)
		t.Cleanup(srv.Close)
		return srv
	}
	t.Run("js/wasm", func(t *testing.T) {
		dest := filepath.Join(t.TempDir(), "nomi-vm")
		if err := downloadRunner(serve(js).URL, v, Target{"js", "wasm"}, Variant{}, dest, nil); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(dest); string(got) != "the js runner" {
			t.Fatalf("extracted %q", got)
		}
		path, err := WasmExec(dest)
		if err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(path); string(got) != "// the archive's wasm_exec.js" {
			t.Fatalf("wasm_exec.js is %q", got)
		}
	})
	t.Run("js/wasm without its JS", func(t *testing.T) {
		dir := t.TempDir()
		err := downloadRunner(serve(noJS).URL, v, Target{"js", "wasm"}, Variant{}, filepath.Join(dir, "nomi-vm"), nil)
		if err == nil || !strings.Contains(err.Error(), "the archive holds no wasm_exec.js") {
			t.Fatalf("got %v", err)
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Fatalf("a refused archive left %d file(s) behind", len(entries))
		}
	})
	t.Run("wasip1/wasm", func(t *testing.T) {
		dest := filepath.Join(t.TempDir(), "nomi-vm")
		if err := downloadRunner(serve(js).URL, v, Target{"wasip1", "wasm"}, Variant{}, dest, nil); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(dest); string(got) != "the wasip1 runner" {
			t.Fatalf("extracted %q", got)
		}
		if _, err := WasmExec(dest); err == nil {
			t.Fatal("a wasip1 runner has a wasm_exec.js beside it")
		}
	})
	t.Run("wasip1/wasm reactor", func(t *testing.T) {
		dest := filepath.Join(t.TempDir(), "nomi-vm-reactor")
		var progress bytes.Buffer
		if err := downloadRunner(serve(js).URL, v, Target{"wasip1", "wasm"}, Variant{Reactor: true}, dest, &progress); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(dest); string(got) != "the wasip1 reactor" {
			t.Fatalf("extracted %q", got)
		}
		if !strings.Contains(progress.String(), "downloading nomi-vm-reactor for wasip1/wasm") {
			t.Fatalf("progress %q does not name the reactor", progress.String())
		}
	})
}

// TestDownloadRunner_AnArchiveWithoutTheReactorSaysSo: a wasip1 archive
// holding only the command runner installs no reactor and names it.
func TestDownloadRunner_AnArchiveWithoutTheReactorSaysSo(t *testing.T) {
	const v = "v9.9.9"
	wasi := tarGz(t, map[string][]byte{"./nomi-vm": []byte("the wasip1 runner")})
	srv := httptest.NewServer(&fakeRelease{files: map[string][]byte{
		"/" + v + "/nomi_v9.9.9_wasip1_wasm.tar.gz": wasi,
		"/" + v + "/nomi_v9.9.9_checksums.txt":      []byte(sha(wasi) + "  ./nomi_v9.9.9_wasip1_wasm.tar.gz\n"),
	}})
	defer srv.Close()
	dir := t.TempDir()
	err := downloadRunner(srv.URL, v, Target{"wasip1", "wasm"}, Variant{Reactor: true}, filepath.Join(dir, "nomi-vm-reactor"), nil)
	if err == nil || !strings.Contains(err.Error(), "the archive holds no nomi-vm-reactor") {
		t.Fatalf("got %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("a refused archive left %d file(s) behind", len(entries))
	}
}

// TestRunner_OnlyWasip1HasAReactor: asking for the reactor on another
// target is refused before anything is looked up or built.
func TestRunner_OnlyWasip1HasAReactor(t *testing.T) {
	for _, target := range []Target{{"js", "wasm"}, {"linux", "amd64"}} {
		_, err := Runner(target, Variant{Reactor: true}, nil)
		if err == nil || !strings.Contains(err.Error(), "only wasip1/wasm has a reactor nomi-vm") {
			t.Fatalf("%s: got %v", target, err)
		}
	}
}

// TestReleaseRunner_NotARelease:a nomi with no source, no sibling runner and
// no release tag says there is nowhere to get a runner from.
func TestReleaseRunner_NotARelease(t *testing.T) {
	t.Setenv(CacheRootEnv, t.TempDir())
	old := ReleaseVersion
	ReleaseVersion = ""
	defer func() { ReleaseVersion = old }()
	_, err := releaseRunner(Target{"linux", "amd64"}, Variant{}, nil)
	if err == nil || !strings.Contains(err.Error(), "it is not a release build") {
		t.Fatalf("got %v", err)
	}
}

// prebuiltRelease stands in a release `nomi` cut as v9.9.9 for linux/amd64,
// installed in its own directory, with runners told apart by their contents
// instead of being built: a file reading "compiler runner" has the build info
// of std/compiler's runner from nomi's commit, and one reading "plain runner"
// that of the plain runner. A `go` on PATH and in NOMI_GO records any run in
// the file ranGo names.
type prebuiltRelease struct {
	dir, cache, ranGo string
	target            Target
}

func standInRelease(t *testing.T) prebuiltRelease {
	t.Helper()
	const rev = "0cf65c8a453eb7b52dc6e743ed90d1a6aa5f33c5"
	r := prebuiltRelease{dir: t.TempDir(), cache: t.TempDir(), target: Target{"linux", "amd64"}}
	r.ranGo = filepath.Join(t.TempDir(), "ran-go")
	oldVersion, oldExe, oldSelf, oldRead := ReleaseVersion, executablePath, selfIdentity, readIdentity
	t.Cleanup(func() {
		ReleaseVersion, executablePath, selfIdentity, readIdentity = oldVersion, oldExe, oldSelf, oldRead
	})
	ReleaseVersion = "v9.9.9"
	executablePath = func() (string, error) { return filepath.Join(r.dir, "nomi"), nil }
	selfIdentity = func() buildIdentity {
		return buildIdentity{Path: "github.com/nomi-language/nomi/cmd/nomi", Revision: rev}
	}
	readIdentity = func(bin string) (buildIdentity, error) {
		data, err := os.ReadFile(bin)
		if err != nil {
			return buildIdentity{}, err
		}
		id := buildIdentity{Path: runnerMainPath, Revision: rev, GOOS: "linux", GOARCH: "amd64"}
		switch string(data) {
		case "compiler runner":
			id.Tags = compilerTag
		case "plain runner":
		default:
			return buildIdentity{}, fmt.Errorf("%s is not a Go binary", bin)
		}
		return id, nil
	}
	t.Setenv(CacheRootEnv, r.cache)
	goDir := t.TempDir()
	fakeGo := filepath.Join(goDir, "go")
	if err := os.WriteFile(fakeGo, []byte("#!/bin/sh\ntouch '"+r.ranGo+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", goDir)
	t.Setenv("NOMI_GO", fakeGo)
	return r
}

// noGoRan fails the test if the stand-in release ran `go`.
func (r prebuiltRelease) noGoRan(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(r.ranGo); err == nil {
		t.Fatal("a release nomi ran go")
	}
}

// compilerArchive serves the v9.9.9 release's linux/amd64 compiler archive,
// holding runner as nomi-vm-compiler, and its checksum file, which lists sum
// for it (the archive's own when sum is "").
func compilerArchive(t *testing.T, runner, sum string) (*httptest.Server, *fakeRelease) {
	t.Helper()
	data := tarGz(t, map[string][]byte{
		"./nomi-vm-compiler":       []byte(runner),
		"./nomi-vm-compiler-nonet": []byte("lean compiler runner"),
	})
	if sum == "" {
		sum = sha(data)
	}
	rel := &fakeRelease{files: map[string][]byte{
		"/v9.9.9/nomi_v9.9.9_linux_amd64_compiler.tar.gz": data,
		"/v9.9.9/nomi_v9.9.9_checksums.txt":               []byte(sum + "  ./nomi_v9.9.9_linux_amd64_compiler.tar.gz\n"),
	}}
	srv := httptest.NewServer(rel)
	t.Cleanup(srv.Close)
	t.Setenv(releaseBaseURLEnv, srv.URL)
	return srv, rel
}

// TestPrebuilt_TheCompilerRunnerBesideNomi: a release nomi takes
// std/compiler's runner installed beside it, and refuses a file of that
// name that is the plain runner.
func TestPrebuilt_TheCompilerRunnerBesideNomi(t *testing.T) {
	r := standInRelease(t)
	bin := filepath.Join(r.dir, "nomi-vm-compiler")
	if err := os.WriteFile(bin, []byte("compiler runner"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := Runner(r.target, Variant{Compiler: true}, nil)
	if err != nil || got != bin {
		t.Fatalf("got %q, %v, want the runner beside nomi, %s", got, err, bin)
	}
	if err := os.WriteFile(bin, []byte("plain runner"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err = Runner(r.target, Variant{Compiler: true}, nil)
	if want := bin + " is not a nomi-vm-compiler (it was built with no build tags"; err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("got %v, want the refusal %q", err, want)
	}
	r.noGoRan(t)
}

// TestPrebuilt_TheCompilerRunnerIsDownloaded: with none beside it, a release
// nomi downloads std/compiler's runner from its target's compiler archive,
// verifies the archive against the checksum file and the runner's build info
// against nomi, caches it, and reuses the cache without asking again. A bad
// checksum or a plain runner in the archive installs nothing.
func TestPrebuilt_TheCompilerRunnerIsDownloaded(t *testing.T) {
	t.Run("download, then cache", func(t *testing.T) {
		r := standInRelease(t)
		srv, rel := compilerArchive(t, "compiler runner", "")
		var progress bytes.Buffer
		got, err := Runner(r.target, Variant{Compiler: true}, &progress)
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(r.cache, "nomi-vm", "v9.9.9", "linux_amd64", "nomi-vm-compiler")
		if data, _ := os.ReadFile(got); got != want || string(data) != "compiler runner" {
			t.Fatalf("got %s holding %q, want the downloaded runner at %s", got, data, want)
		}
		if url := srv.URL + "/v9.9.9/nomi_v9.9.9_linux_amd64_compiler.tar.gz"; !strings.Contains(progress.String(),
			"nomi: downloading nomi-vm-compiler for linux/amd64 from "+url) {
			t.Fatalf("progress %q does not name %s", progress.String(), url)
		}
		first := rel.requests.Load()
		if again, err := Runner(r.target, Variant{Compiler: true}, nil); err != nil || again != want || rel.requests.Load() != first {
			t.Fatalf("a second lookup got %q, %v after %d requests, then %d", again, err, first, rel.requests.Load())
		}
		r.noGoRan(t)
	})
	t.Run("bad checksum", func(t *testing.T) {
		r := standInRelease(t)
		compilerArchive(t, "compiler runner", strings.Repeat("0", 64))
		_, err := Runner(r.target, Variant{Compiler: true}, nil)
		if err == nil || !strings.Contains(err.Error(), "nomi_v9.9.9_linux_amd64_compiler.tar.gz does not match the release's checksum file") {
			t.Fatalf("got %v", err)
		}
		if _, err := os.Stat(filepath.Join(r.cache, "nomi-vm", "v9.9.9", "linux_amd64", "nomi-vm-compiler")); !os.IsNotExist(err) {
			t.Fatalf("a bad checksum cached a runner (%v)", err)
		}
		r.noGoRan(t)
	})
	t.Run("a plain runner in the archive", func(t *testing.T) {
		r := standInRelease(t)
		compilerArchive(t, "plain runner", "")
		_, err := Runner(r.target, Variant{Compiler: true}, nil)
		if err == nil || !strings.Contains(err.Error(), "the downloaded nomi-vm-compiler: ") ||
			!strings.Contains(err.Error(), "is not a nomi-vm-compiler (it was built with no build tags") {
			t.Fatalf("got %v", err)
		}
		if _, err := os.Stat(filepath.Join(r.cache, "nomi-vm", "v9.9.9", "linux_amd64", "nomi-vm-compiler")); !os.IsNotExist(err) {
			t.Fatalf("a plain runner was cached as the compiler one (%v)", err)
		}
		r.noGoRan(t)
	})
}

// TestPrebuilt_AReleaseWithNoRunnerNeverRunsGo: a release nomi that can
// neither find nor download std/compiler's runner says which archive it
// wanted and why it could not get it, with a `go` on PATH and in NOMI_GO
// that it never runs or suggests. The plain runner beside it is not taken
// instead.
func TestPrebuilt_AReleaseWithNoRunnerNeverRunsGo(t *testing.T) {
	r := standInRelease(t)
	if err := os.WriteFile(filepath.Join(r.dir, "nomi-vm"), []byte("plain runner"), 0o755); err != nil {
		t.Fatal(err)
	}
	closed := httptest.NewServer(http.NotFoundHandler())
	base := closed.URL
	closed.Close()
	cases := []struct {
		name, base string
		want       []string
	}{
		{"no network", base, []string{
			"no nomi-vm-compiler for linux/amd64 is installed, and downloading it from the v9.9.9 release failed: " + base + "/v9.9.9/nomi_v9.9.9_checksums.txt",
			"A release nomi builds no runner: it fetches one that is not installed beside it from the release it was cut as",
			"Connect to the network and re-run, or unpack nomi-vm-compiler from " + base + "/v9.9.9/nomi_v9.9.9_linux_amd64_compiler.tar.gz yourself",
		}},
		{"a release without the archive", "", []string{
			"the v9.9.9 release's checksum file ", "lists no nomi_v9.9.9_linux_amd64_compiler.tar.gz",
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.base == "" {
				srv := httptest.NewServer(&fakeRelease{files: map[string][]byte{
					"/v9.9.9/nomi_v9.9.9_checksums.txt": []byte(strings.Repeat("ab", 32) + "  ./nomi_v9.9.9_linux_amd64.tar.gz\n"),
				}})
				t.Cleanup(srv.Close)
				c.base = srv.URL
			}
			t.Setenv(releaseBaseURLEnv, c.base)
			_, err := Runner(r.target, Variant{Compiler: true}, nil)
			if err == nil {
				t.Fatal("found a runner")
			}
			for _, want := range c.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the error does not say %q:\n%v", want, err)
				}
			}
			for _, never := range []string{"Go installed", "Install Go", "Go toolchain", "go.dev", "NOMI_GO", "`go`"} {
				if strings.Contains(err.Error(), never) {
					t.Errorf("the error mentions %q:\n%v", never, err)
				}
			}
			r.noGoRan(t)
		})
	}
}
