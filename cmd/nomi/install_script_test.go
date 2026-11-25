package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
)

// releaseHost stands in for the GitHub releases of the compiler's
// repository: /releases/latest redirects to the latest tag's page, and
// /releases/download/<version>/<name> serves a release's files.
type releaseHost struct {
	mu       sync.Mutex
	latest   string
	files    map[string][]byte // "<version>/<name>"
	requests []string
	url      string
}

func (h *releaseHost) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.requests = append(h.requests, r.URL.Path)
	switch {
	case r.URL.Path == "/releases/latest" && h.latest != "":
		http.Redirect(w, r, h.url+"/releases/tag/"+h.latest, http.StatusFound)
	case strings.HasPrefix(r.URL.Path, "/releases/download/"):
		data, ok := h.files[strings.TrimPrefix(r.URL.Path, "/releases/download/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	default:
		http.NotFound(w, r)
	}
}

// publish adds a release of version whose archive for goos/goarch holds a
// `nomi` that prints line, with a checksums file that gives the archive's
// SHA-256, or wrongSum in its place when that is not empty.
func (h *releaseHost) publish(t *testing.T, version, goos, goarch, line, wrongSum string) (archive string) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := "#!/bin/sh\necho '" + line + "'\n"
	if err := tw.WriteHeader(&tar.Header{Name: "./nomi", Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	archive = fmt.Sprintf("nomi_%s_%s_%s.tar.gz", version, goos, goarch)
	sum := sha256.Sum256(buf.Bytes())
	got := hex.EncodeToString(sum[:])
	if wrongSum != "" {
		got = wrongSum
	}
	// As scripts/release.sh writes it: every archive of the release, each
	// named with a leading ./.
	sums := fmt.Sprintf("%s  ./nomi_%s_%s_%s_runners.tar.gz\n%s  ./%s\n", strings.Repeat("0", 64), version, goos, goarch, got, archive)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.files[version+"/"+archive] = buf.Bytes()
	h.files[version+"/nomi_"+version+"_checksums.txt"] = []byte(sums)
	return archive
}

func (h *releaseHost) requestCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.requests)
}

func (h *releaseHost) requested(path string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, p := range h.requests {
		if p == path {
			n++
		}
	}
	return n
}

// installRun is one run of scripts/install.sh.
type installRun struct {
	stdout, stderr string
	exit           int
}

// TestInstallScript runs scripts/install.sh, the script behind `curl -fsSL
// https://nomi-lang.org/install.sh | sh`, against a local stand-in for the
// release host, under /bin/sh and under dash when there is one, since the
// script must hold to POSIX sh:
//
//   - piped into sh as curl pipes it, it finds the latest release by its
//     redirect, checks the archive against the checksums file, installs
//     `nomi` and nothing else, and prints the PATH line and the next command;
//   - run again for a pinned version with its directory on PATH, it replaces
//     `nomi` by a rename (a file opened before still reads the old one) and
//     asks for no latest release;
//   - an archive whose SHA-256 is not the one the checksums file gives is
//     refused, and the directory holds no `nomi` and nothing left over;
//   - an operating system or architecture with no release, and a machine
//     with neither sha256sum nor shasum, are refused before any download,
//     with a message that names what was found.
func TestInstallScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("install.sh is for macOS and Linux; Windows installs from the release's zip")
	}
	goos, goarch := runtime.GOOS, runtime.GOARCH
	if (goos != "darwin" && goos != "linux") || (goarch != "amd64" && goarch != "arm64") {
		t.Skipf("no release of nomi is built for %s/%s", goos, goarch)
	}
	script, err := filepath.Abs(filepath.Join("scripts", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	shells := []string{"/bin/sh"}
	if dash, err := exec.LookPath("dash"); err == nil {
		shells = append(shells, dash)
	}
	for _, shell := range shells {
		t.Run(filepath.Base(shell), func(t *testing.T) {
			testInstallScriptUnder(t, shell, script, goos, goarch)
		})
	}
}

func testInstallScriptUnder(t *testing.T, shell, script, goos, goarch string) {
	host := &releaseHost{files: map[string][]byte{}}
	srv := httptest.NewServer(host)
	defer srv.Close()
	host.url = srv.URL
	home := t.TempDir()

	run := func(t *testing.T, piped bool, path string, env ...string) installRun {
		t.Helper()
		cmd := exec.Command(shell, script)
		if piped {
			f, err := os.Open(script)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			cmd = exec.Command(shell)
			cmd.Stdin = f
		}
		cmd.Env = append([]string{"PATH=" + path, "HOME=" + home, "NOMI_INSTALL_BASE_URL=" + srv.URL + "/releases"}, env...)
		var out, errOut strings.Builder
		cmd.Stdout, cmd.Stderr = &out, &errOut
		err := cmd.Run()
		r := installRun{stdout: out.String(), stderr: errOut.String()}
		if exitErr := (*exec.ExitError)(nil); errors.As(err, &exitErr) {
			r.exit = exitErr.ExitCode()
		} else if err != nil {
			t.Fatalf("running install.sh under %s: %v", shell, err)
		}
		return r
	}
	entries := func(t *testing.T, dir string) []string {
		t.Helper()
		list, err := os.ReadDir(dir)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, e := range list {
			names = append(names, e.Name())
		}
		return names
	}
	runNomi := func(t *testing.T, bin string) string {
		t.Helper()
		out, err := exec.Command(bin).Output()
		if err != nil {
			t.Fatalf("running the installed %s: %v", bin, err)
		}
		return string(out)
	}
	path := os.Getenv("PATH")

	dir := filepath.Join(home, ".nomi", "bin")
	bin := filepath.Join(dir, "nomi")
	t.Run("install", func(t *testing.T) {
		host.latest = "v0.0.1"
		host.publish(t, "v0.0.1", goos, goarch, "nomi v0.0.1 (first)", "")
		r := run(t, true, path)
		if r.exit != 0 {
			t.Fatalf("install.sh exits %d:\n%s%s", r.exit, r.stdout, r.stderr)
		}
		if got := runNomi(t, bin); got != "nomi v0.0.1 (first)\n" {
			t.Errorf("the installed nomi prints %q, want the first release's line", got)
		}
		if got := entries(t, dir); !slices.Equal(got, []string{"nomi"}) {
			t.Errorf("%s holds %q, want nomi alone", dir, got)
		}
		if host.requested("/releases/latest") != 1 {
			t.Errorf("install.sh asked for the latest release %d times, want once", host.requested("/releases/latest"))
		}
		for _, want := range []string{
			"Installed nomi v0.0.1 (first) at " + bin,
			dir + " is not on your PATH",
			`export PATH="$HOME/.nomi/bin:$PATH"`,
			"nomi editor install <editor>",
		} {
			if !strings.Contains(r.stdout, want) {
				t.Errorf("install.sh prints\n%s\nwant a line holding %q", r.stdout, want)
			}
		}
	})

	t.Run("rerun", func(t *testing.T) {
		old, err := os.Open(bin)
		if err != nil {
			t.Fatalf("the first install left no nomi: %v", err)
		}
		defer old.Close()
		host.publish(t, "v0.0.2", goos, goarch, "nomi v0.0.2 (second)", "")
		latest := host.requested("/releases/latest")
		r := run(t, false, dir+string(os.PathListSeparator)+path, "NOMI_VERSION=v0.0.2")
		if r.exit != 0 {
			t.Fatalf("install.sh exits %d:\n%s%s", r.exit, r.stdout, r.stderr)
		}
		if got := runNomi(t, bin); got != "nomi v0.0.2 (second)\n" {
			t.Errorf("the reinstalled nomi prints %q, want the second release's line", got)
		}
		var kept bytes.Buffer
		if _, err := kept.ReadFrom(old); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(kept.String(), "(first)") {
			t.Errorf("a nomi opened before the rerun reads %q: the rerun wrote over the file instead of renaming a new one over it", kept.String())
		}
		if got := entries(t, dir); !slices.Equal(got, []string{"nomi"}) {
			t.Errorf("%s holds %q after the rerun, want nomi alone", dir, got)
		}
		if host.requested("/releases/latest") != latest {
			t.Errorf("install.sh with NOMI_VERSION asked for the latest release")
		}
		if strings.Contains(r.stdout, "not on your PATH") || !strings.Contains(r.stdout, "Next, set up your editor") {
			t.Errorf("with its directory on PATH, install.sh prints\n%s\nwant no PATH line and the next command", r.stdout)
		}
	})

	t.Run("checksum mismatch", func(t *testing.T) {
		wrong := strings.Repeat("ab", 32)
		archive := host.publish(t, "v0.0.3", goos, goarch, "nomi v0.0.3 (tampered)", wrong)
		fresh := filepath.Join(t.TempDir(), "bin")
		r := run(t, false, path, "NOMI_VERSION=v0.0.3", "NOMI_INSTALL_DIR="+fresh)
		if r.exit == 0 {
			t.Fatalf("install.sh installs an archive whose checksum does not match:\n%s", r.stdout)
		}
		want := regexp.MustCompile(`install\.sh: the SHA-256 of ` + regexp.QuoteMeta(archive) +
			` is [0-9a-f]{64}, and nomi_v0\.0\.3_checksums\.txt says ` + wrong + `; nothing was installed`)
		if !want.MatchString(r.stderr) {
			t.Errorf("install.sh says\n%s\nwant a line matching %s", r.stderr, want)
		}
		if got := entries(t, fresh); len(got) != 0 {
			t.Errorf("after a refused install %s holds %q, want nothing", fresh, got)
		}
	})

	refusals := []struct {
		name string
		env  []string
		want []string
	}{
		{"operating system", []string{"NOMI_INSTALL_OS=FreeBSD"},
			[]string{"there is no release of nomi for the operating system FreeBSD", "https://nomi-lang.org/tooling/install/"}},
		{"architecture", []string{"NOMI_INSTALL_OS=Linux", "NOMI_INSTALL_ARCH=riscv64"},
			[]string{"there is no release of nomi for the architecture riscv64", "https://nomi-lang.org/tooling/install/"}},
		{"windows", []string{"NOMI_INSTALL_OS=MINGW64_NT-10.0-26100", "NOMI_INSTALL_ARCH=x86_64"},
			[]string{"found MINGW64_NT-10.0-26100", "download the zip"}},
	}
	for _, c := range refusals {
		t.Run("unsupported "+c.name, func(t *testing.T) {
			fresh := filepath.Join(t.TempDir(), "bin")
			before := host.requestCount()
			r := run(t, false, path, append(c.env, "NOMI_INSTALL_DIR="+fresh)...)
			if r.exit != 1 {
				t.Fatalf("install.sh with %v exits %d, want 1:\n%s%s", c.env, r.exit, r.stdout, r.stderr)
			}
			for _, want := range c.want {
				if !strings.Contains(r.stderr, want) {
					t.Errorf("install.sh with %v says\n%s\nwant a line holding %q", c.env, r.stderr, want)
				}
			}
			if host.requestCount() != before {
				t.Errorf("install.sh with %v downloaded something before refusing", c.env)
			}
			if _, err := os.Stat(fresh); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("install.sh with %v created %s", c.env, fresh)
			}
		})
	}

	t.Run("no sha256 tool", func(t *testing.T) {
		tools := t.TempDir()
		for _, name := range []string{"uname", "curl", "tar"} {
			target, err := exec.LookPath(name)
			if err != nil {
				t.Skipf("no %s on PATH", name)
			}
			if err := os.Symlink(target, filepath.Join(tools, name)); err != nil {
				t.Fatal(err)
			}
		}
		fresh := filepath.Join(t.TempDir(), "bin")
		before := host.requestCount()
		r := run(t, false, tools, "NOMI_INSTALL_DIR="+fresh)
		if r.exit != 1 || !strings.Contains(r.stderr, "needs sha256sum or shasum to check the download") {
			t.Errorf("install.sh with neither sha256sum nor shasum exits %d and says\n%s\nwant exit 1 and that it needs one", r.exit, r.stderr)
		}
		if host.requestCount() != before {
			t.Errorf("install.sh downloaded something with no way to check it")
		}
	})
}

// TestHomebrewFormulaRendersFromTheChecksums: scripts/homebrew-formula.sh
// renders packaging/homebrew/nomi.rb from a release's version and checksums
// file into the formula testdata/release/nomi.rb holds, which gives each of
// the four platforms' archives the SHA-256 the checksums file lists for it,
// installs nomi alone and depends on nothing. A checksums file missing one of
// the four archives renders nothing.
func TestHomebrewFormulaRendersFromTheChecksums(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("runs a bash script; the Unix jobs check it")
	}
	script := filepath.Join("scripts", "homebrew-formula.sh")
	sumsPath := filepath.Join("cmd", "nomi", "testdata", "release", "nomi_v0.0.1_checksums.txt")
	render := func(sums string) installRun {
		t.Helper()
		cmd := exec.Command("bash", script, "v0.0.1", sums)
		var out, errOut strings.Builder
		cmd.Stdout, cmd.Stderr = &out, &errOut
		err := cmd.Run()
		r := installRun{stdout: out.String(), stderr: errOut.String()}
		if exitErr := (*exec.ExitError)(nil); errors.As(err, &exitErr) {
			r.exit = exitErr.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return r
	}

	r := render(sumsPath)
	if r.exit != 0 {
		t.Fatalf("homebrew-formula.sh exits %d:\n%s", r.exit, r.stderr)
	}
	want, err := os.ReadFile(filepath.Join("cmd", "nomi", "testdata", "release", "nomi.rb"))
	if err != nil {
		t.Fatal(err)
	}
	if r.stdout != string(want) {
		t.Errorf("homebrew-formula.sh renders\n%s\nwant testdata/release/nomi.rb:\n%s", r.stdout, want)
	}

	sumsText, err := os.ReadFile(sumsPath)
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(sumsText)), "\n") {
		f := strings.Fields(line)
		listed[strings.TrimPrefix(f[1], "./")] = f[0]
	}
	pair := regexp.MustCompile(`url "https://github\.com/nomi-language/nomi/releases/download/v#\{version\}/nomi_v#\{version\}_([a-z]+_[a-z0-9]+)\.tar\.gz"\n\s+sha256 "([0-9a-f]{64})"`)
	var platforms []string
	for _, m := range pair.FindAllStringSubmatch(r.stdout, -1) {
		platforms = append(platforms, m[1])
		if archive := "nomi_v0.0.1_" + m[1] + ".tar.gz"; listed[archive] != m[2] {
			t.Errorf("the formula gives %s the SHA-256 %s, and the checksums file %s", archive, m[2], listed[archive])
		}
	}
	slices.Sort(platforms)
	if !slices.Equal(platforms, []string{"darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64"}) {
		t.Errorf("the formula has a url and sha256 for %q, want the four macOS and Linux platforms", platforms)
	}
	for _, want := range []string{"class Nomi < Formula\n", "  version \"0.0.1\"\n", "    bin.install \"nomi\"\n", "  test do\n"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("the formula has no line %q", strings.TrimSpace(want))
		}
	}
	for _, unwanted := range []string{"depends_on", "@"} {
		if strings.Contains(r.stdout, unwanted) {
			t.Errorf("the formula holds %q:\n%s", unwanted, r.stdout)
		}
	}

	var partial []string
	for _, line := range strings.Split(string(sumsText), "\n") {
		if !strings.HasSuffix(line, "_linux_arm64.tar.gz") {
			partial = append(partial, line)
		}
	}
	short := filepath.Join(t.TempDir(), "nomi_v0.0.1_checksums.txt")
	mustWrite(t, short, strings.Join(partial, "\n"))
	r = render(short)
	if r.exit != 1 || r.stdout != "" || !strings.Contains(r.stderr, "gives no SHA-256 for nomi_v0.0.1_linux_arm64.tar.gz") {
		t.Errorf("homebrew-formula.sh over checksums without linux/arm64 exits %d, prints %d bytes and says\n%s\nwant exit 1, nothing printed, and the archive it lacks named", r.exit, len(r.stdout), r.stderr)
	}
}
