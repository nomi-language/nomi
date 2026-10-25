package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/ffirun"
	"github.com/nomi-language/nomi/internal/gotoolchain"
)

// TestReleaseArchive_RunsAGoBindingFromAFetchedModule is the worked example
// of distributing a module, run against a `nomi` built the way
// scripts/release.sh builds one: in a clean git repository at a tag
// (v0.99.0), -trimpath, with the release version stamped. It runs from a
// directory with no checkout, with no `go` on PATH.
//
// A Nomi module example.com/greet, with a `gopkg` binding to a Go package in
// the same module, is published to a file module proxy. An app runs `nomi add
// example.com/greet@v0.1.0`, imports it by its short name (`import greet`),
// and `nomi run`s it: nomi downloads greet through the proxy protocol, then,
// finding no `go`, downloads "Go" from a local archive server, checks it
// against the checksum compiled into this nomi, and builds the wrapper with
// it against the compiler module at v0.99.0, also from the proxy. The "Go"
// archive is fake: its go/bin/go is a script that runs this machine's Go, so
// the test downloads nothing from the network.
//
// It takes about a minute (two Go builds), so -short skips it. CI runs it in
// `go test ./...` (the cmd/nomi package), where the compiler module's
// dependencies come from the runner's module cache served as a second file
// proxy.
func TestReleaseArchive_RunsAGoBindingFromAFetchedModule(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a release nomi and a wrapper; -short")
	}
	if runtime.GOOS == "windows" {
		t.Skip("the fake Go archive's go is a shell script")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go toolchain")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git to stamp the release build")
	}
	const noGoPath = "/usr/bin:/bin"
	for _, dir := range filepath.SplitList(noGoPath) {
		if _, err := os.Stat(filepath.Join(dir, "go")); err == nil {
			t.Skipf("%s holds a go, so the test cannot take Go off PATH", dir)
		}
	}
	root := repoRoot(t)
	tmp := t.TempDir()
	goRoot := goEnv(t, goBin, "GOROOT")
	parentModCache := goEnv(t, goBin, "GOMODCACHE")

	// The module proxy: the compiler at the release's version, and greet.
	proxy := filepath.Join(tmp, "proxy")
	publishModule(t, root, proxy, versionedTestVersion)
	publishFiles(t, proxy, "example.com/greet", "v0.1.0", map[string]string{
		"go.mod":    "module example.com/greet\n\ngo 1.22\n",
		"nomi.toml": "[module]\nname = \"greet\"\n",
		"greet.nomi": "gopkg \"example.com/greet/goside\" as goside\n\n" +
			"fn shout(s: String) -> String go goside.Shout\n\n" +
			"/// Greets name, loudly.\npub fn hello(name: String) -> String {\n    shout(\"hello, ${name}\")\n}\n",
		"goside/goside.go": "package goside\n\nimport \"strings\"\n\nfunc Shout(s string) string { return strings.ToUpper(s) + \"!\" }\n",
	})

	archiveURL := ""
	nomi := buildReleaseNomi(t, root, goBin, tmp, func(goVersion string) (string, string) {
		// The archive the built nomi will ask for, and its checksum.
		archive := gotoolchain.ArchiveName(goVersion, runtime.GOOS, runtime.GOARCH)
		data := fakeGoArchive(t, goVersion, "#!/bin/sh\nexec "+filepath.Join(goRoot, "bin", "go")+" \"$@\"\n")
		sum := sha256.Sum256(data)
		archiveURL = serveFile(t, archive, data)
		return archive, hex.EncodeToString(sum[:])
	})

	app := filepath.Join(tmp, "app")
	mustWrite(t, filepath.Join(app, "go.mod"), "module example.com/app\n\ngo 1.27.0\n")
	mustWrite(t, filepath.Join(app, "nomi.toml"), "[module]\nname = \"app\"\nentry_points = [\"main\"]\n")
	mustWrite(t, filepath.Join(app, "main.nomi"), "import {\n    greet\n    std/io\n}\n\nfn main() {\n    io.print(greet.hello(\"you\"))\n}\n")

	toolchains := filepath.Join(tmp, "toolchains")
	env := []string{
		"HOME=" + filepath.Join(tmp, "home"),
		"PATH=" + noGoPath,
		"TERM=dumb",
		"NOMI_COLOR=never",
		"NOMI_FFIRUN_CACHE_ROOT=" + filepath.Join(tmp, "nomi-cache"),
		"NOMI_TOOLCHAIN_ROOT=" + toolchains,
		"NOMI_GO_DOWNLOAD_URL=" + archiveURL,
		"GOPROXY=file://" + filepath.ToSlash(proxy) + ",file://" + filepath.ToSlash(filepath.Join(parentModCache, "cache", "download")),
		"GOSUMDB=off",
		"GOTOOLCHAIN=local",
		"GOMODCACHE=" + filepath.Join(tmp, "modcache"),
		"GOPATH=" + filepath.Join(tmp, "gopath"),
		"GOCACHE=" + goEnv(t, goBin, "GOCACHE"),
		"GOFLAGS=-modcacherw",
	}
	run := func(args ...string) (string, string) {
		t.Helper()
		cmd := exec.Command(nomi, args...)
		cmd.Dir = app
		cmd.Env = env
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("nomi %s: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, stdout.String(), stderr.String())
		}
		return strings.TrimSpace(stdout.String()), stderr.String()
	}

	out, _ := run("add", "example.com/greet@v0.1.0")
	if want := "added example.com/greet v0.1.0 (module greet; import greet)"; out != want {
		t.Fatalf("nomi add printed %q, want %q", out, want)
	}
	goMod, _ := os.ReadFile(filepath.Join(app, "go.mod"))
	if !strings.Contains(string(goMod), "require example.com/greet v0.1.0\n") || strings.Contains(string(goMod), "replace") {
		t.Fatalf("go.mod after nomi add:\n%s", goMod)
	}
	goSum, _ := os.ReadFile(filepath.Join(app, "go.sum"))
	if !strings.Contains(string(goSum), "example.com/greet v0.1.0 h1:") {
		t.Fatalf("go.sum after nomi add:\n%s", goSum)
	}

	out, stderr := run("run", "main.nomi")
	if out != "HELLO, YOU!" {
		t.Fatalf("nomi run printed %q, want HELLO, YOU!\nstderr:\n%s", out, stderr)
	}
	if !strings.Contains(stderr, "nomi: downloading Go") {
		t.Errorf("the managed toolchain's download was not announced:\n%s", stderr)
	}
	if _, err := os.Stat(filepath.Join(toolchains)); err != nil {
		t.Fatalf("no managed toolchain was installed: %v", err)
	}
	assertVersionedWrappers(t, filepath.Join(tmp, "nomi-cache"))

	// Everything is cached now, so --offline runs it, and no proxy is read.
	for i, e := range env {
		if strings.HasPrefix(e, "GOPROXY=") {
			env[i] = "GOPROXY=file:///nonexistent"
		}
	}
	if out, stderr := run("run", "--offline", "main.nomi"); out != "HELLO, YOU!" || strings.Contains(stderr, "downloading") {
		t.Fatalf("nomi run --offline printed %q\nstderr:\n%s", out, stderr)
	}
}

// buildReleaseNomi builds cmd/nomi as scripts/release.sh does, from a git
// repository holding this tree tagged as versionedTestVersion, and answers
// the binary. pin is handed the Go version the binary is built with and
// answers the archive name and checksum to compile in as pinned.
func buildReleaseNomi(t *testing.T, root, goBin, tmp string, pin func(goVersion string) (archive, sum string)) string {
	t.Helper()
	src := filepath.Join(tmp, "release-src")
	out, err := exec.Command("git", "-C", root, "ls-files", "-z", "--cached", "--others", "--exclude-standard").Output()
	if err != nil {
		t.Skipf("listing tracked files (not a git checkout?): %v", err)
	}
	for _, rel := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		from := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Lstat(from)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		data, err := os.ReadFile(from)
		if err != nil {
			t.Fatal(err)
		}
		to := filepath.Join(src, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(to, data, info.Mode().Perm()); err != nil {
			t.Fatal(err)
		}
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", src, "-c", "user.name=release", "-c", "user.email=release@example.com",
			"-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	git("init", "-q")
	git("add", "-A")
	git("commit", "-q", "-m", "release")
	git("tag", versionedTestVersion)

	goVersion := strings.TrimSpace(goEnv(t, goBin, "GOVERSION"))
	archive, sum := pin(goVersion)
	bin := filepath.Join(tmp, "release", "nomi")
	ldflags := "-s -w -X github.com/nomi-language/nomi/internal/ffirun.ReleaseVersion=" + versionedTestVersion +
		" -X github.com/nomi-language/nomi/internal/gotoolchain.testPin=" + archive + "=" + sum
	build := exec.Command(goBin, "build", "-C", src, "-trimpath", "-ldflags", ldflags, "-o", bin, "./cmd/nomi")
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOFLAGS=-buildvcs=true", "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the release nomi: %v\n%s", err, out)
	}
	info, err := buildinfo.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	if got := ffirun.CompilerModuleVersion(info); got != versionedTestVersion {
		t.Fatalf("the release nomi is %s %s, not versioned as %s", info.Main.Path, info.Main.Version, versionedTestVersion)
	}
	if info.GoVersion != goVersion {
		t.Fatalf("the release nomi was built with %s, not %s", info.GoVersion, goVersion)
	}
	return bin
}

// serveFile serves data as /name for the rest of the test, and answers the
// server's URL.
func serveFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+name {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// fakeGoArchive is a gzipped tar laid out as go.dev's Go archives are, with
// script as go/bin/go.
func fakeGoArchive(t *testing.T, version, script string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct {
		name string
		mode int64
		body string
	}{
		{"go/VERSION", 0o644, version + "\ntime 2026-01-01T00:00:00Z\n"},
		{"go/bin/go", 0o755, script},
	} {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: f.mode, Size: int64(len(f.body)), Typeflag: tar.TypeReg, ModTime: time.Unix(0, 0)}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(f.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
