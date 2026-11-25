package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"testing"
)

// releaseHost stands in for the GitHub releases of the compiler's
// repository: /releases/latest redirects to the latest tag's page, and
// /releases/download/<version>/<name> serves a release's files. It stands
// in for the site too, serving script, when set, at /install.ps1 as text.
type releaseHost struct {
	mu       sync.Mutex
	latest   string
	files    map[string][]byte // "<version>/<name>"
	script   []byte
	requests []string
	url      string
}

func (h *releaseHost) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.requests = append(h.requests, r.URL.Path)
	switch {
	case r.URL.Path == "/install.ps1" && h.script != nil:
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write(h.script)
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
	return h.publishScript(t, version, goos, goarch, "#!/bin/sh\necho '"+line+"'\n", wrongSum)
}

// publishScript is publish for a `nomi` that is the shell script body.
func (h *releaseHost) publishScript(t *testing.T, version, goos, goarch, body, wrongSum string) (archive string) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
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
	return h.add(version, fmt.Sprintf("nomi_%s_%s_%s", version, goos, goarch), ".tar.gz", buf.Bytes(), wrongSum)
}

// add makes data the archive stem+ext of the release of version, with a
// checksums file that gives its SHA-256, or wrongSum in its place when that
// is not empty, and returns the archive's name.
func (h *releaseHost) add(version, stem, ext string, data []byte, wrongSum string) (archive string) {
	archive = stem + ext
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if wrongSum != "" {
		got = wrongSum
	}
	// As scripts/release.sh writes it: every archive of the release, each
	// named with a leading ./.
	sums := fmt.Sprintf("%s  ./%s_runners%s\n%s  ./%s\n", strings.Repeat("0", 64), stem, ext, got, archive)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.files[version+"/"+archive] = data
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
//
// testInstallScriptProfiles covers the profile it adds its directory to, and
// testInstallScriptOthers what it does about another `nomi` on PATH, with
// and without NOMI_REPLACE.
func TestInstallScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("install.sh is for macOS and Linux; TestInstallPowerShellScript runs Windows' install.ps1")
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
		// A shell the script writes no profile for, unless a case names
		// another: bash, as macOS's /bin/sh, sets SHELL to the user's login
		// shell when it is unset.
		cmd.Env = append([]string{"PATH=" + path, "HOME=" + home, "SHELL=/bin/sh", "NOMI_INSTALL_BASE_URL=" + srv.URL + "/releases"}, env...)
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
	entries := dirNames
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
		// The new nomi's --version names an editor whose support is stale,
		// which the script prints after its line.
		stale := "nvim: its Nomi support is from v0.0.1 (first); run `nomi upgrade`"
		host.publish(t, "v0.0.2", goos, goarch, "nomi v0.0.2 (second)\n"+stale, "")
		latest := host.requested("/releases/latest")
		r := run(t, false, dir+string(os.PathListSeparator)+path, "NOMI_VERSION=v0.0.2")
		if r.exit != 0 {
			t.Fatalf("install.sh exits %d:\n%s%s", r.exit, r.stdout, r.stderr)
		}
		if got := runNomi(t, bin); got != "nomi v0.0.2 (second)\n"+stale+"\n" {
			t.Errorf("the reinstalled nomi prints %q, want the second release's lines", got)
		}
		if want := "Installed nomi v0.0.2 (second) at " + bin + "\n" + stale + "\n"; !strings.Contains(r.stdout, want) {
			t.Errorf("install.sh prints\n%s\nwant\n%s", r.stdout, want)
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
			[]string{"found MINGW64_NT-10.0-26100", "irm https://nomi-lang.org/install.ps1 | iex"}},
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

	testInstallScriptProfiles(t, host, run, path, goarch)
	testInstallScriptOthers(t, host, run, goos, goarch)
}

// installTools is a directory holding links to the commands install.sh
// runs, and to nothing else, so a case whose PATH is that directory and the
// case's own finds no `nomi` of the machine the test runs on. Replace mode
// removes files it finds on PATH, so its cases never run with the real one.
func installTools(t *testing.T) string {
	t.Helper()
	tools := t.TempDir()
	link := func(name string) bool {
		target, err := exec.LookPath(name)
		if err != nil {
			return false
		}
		if err := os.Symlink(target, filepath.Join(tools, name)); err != nil {
			t.Fatal(err)
		}
		return true
	}
	for _, name := range []string{"uname", "curl", "tar", "awk", "sed", "mktemp", "mkdir", "mv", "rm", "chmod", "dirname", "basename", "readlink"} {
		if !link(name) {
			t.Skipf("no %s on PATH", name)
		}
	}
	if !link("sha256sum") && !link("shasum") {
		t.Skip("no sha256sum or shasum on PATH")
	}
	// GNU tar runs gzip, and macOS under Rosetta is asked with sysctl.
	link("gzip")
	link("sysctl")
	return tools
}

// testInstallScriptOthers: what install.sh does about another `nomi` on
// PATH, each case in a home of its own with a PATH of its own directories
// and installTools. The other nomis are scripts that print the version line
// each kind of build prints (versionLineFor).
//
//   - With no NOMI_REPLACE it changes nothing. When the PATH a new shell
//     has finds another nomi first, it names the file, what it is and what
//     removes it, per kind; it says nothing when this nomi is first, when
//     the first is a link to this one, and when its directory is not on
//     PATH yet, since the profile's line puts it first.
//   - NOMI_REPLACE=1 removes each nomi built from source, a link to one
//     too, and names each it leaves: Homebrew's, a release, one that does
//     not answer. It leaves a link to the nomi it installed, a directory
//     named nomi and every other file. Then it runs the installed nomi's
//     `editor install --installed --force`, and names no editor command.
//   - A nomi it cannot remove, and an editor command that fails, are each
//     an error line, the rest is still done, and the script exits 1.
//   - After a checksum mismatch replace mode removes nothing and runs
//     nothing.
func testInstallScriptOthers(t *testing.T, host *releaseHost, run func(*testing.T, bool, string, ...string) installRun, goos, goarch string) {
	tools := installTools(t)
	sep := string(os.PathListSeparator)
	rev := &debug.BuildInfo{
		Main:     debug.Module{Version: "(devel)"},
		Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "e8635380091fdebf6ee103ebbd8a47a70eeb0fea"}},
	}
	lines := map[string]string{
		"clone":      versionLineFor("", rev, ""),
		"go install": versionLineFor("v0.0.1", &debug.BuildInfo{Main: debug.Module{Version: "v0.0.1"}}, ""),
		"release":    versionLineFor("v0.0.1", rev, ""),
	}
	// The installed nomi answers --version as a release does, and appends
	// any other arguments to the file NOMI_TEST_LOG names. failing exits 3
	// for them.
	stub := func(version string, failing bool) string {
		body := "#!/bin/sh\nif [ \"$1\" = --version ]; then echo '" + versionLineFor(version, rev, "") + "'; exit 0; fi\n" +
			"printf '%s\\n' \"$*\" >> \"$NOMI_TEST_LOG\"\n"
		if failing {
			body += "exit 3\n"
		}
		return body
	}
	host.publishScript(t, "v0.2.0", goos, goarch, stub("v0.2.0", false), "")
	host.publishScript(t, "v0.2.1", goos, goarch, stub("v0.2.1", true), "")
	host.publishScript(t, "v0.2.2", goos, goarch, stub("v0.2.2", false), strings.Repeat("cd", 32))

	// sandbox is one case's home, with the nomi a kind of build leaves in
	// the directory of that name under it.
	type sandbox struct{ home, dir, log string }
	newSandbox := func(t *testing.T) sandbox {
		home := t.TempDir()
		return sandbox{home: home, dir: filepath.Join(home, ".nomi", "bin"), log: filepath.Join(home, "log")}
	}
	other := func(t *testing.T, s sandbox, name, body string) string {
		t.Helper()
		file := filepath.Join(s.home, name, "nomi")
		mustWrite(t, file, body)
		if err := os.Chmod(file, 0o755); err != nil {
			t.Fatal(err)
		}
		return file
	}
	printing := func(line string) string { return "#!/bin/sh\necho '" + line + "'\n" }
	// homebrew is a nomi in a Cellar and the link to it Homebrew puts on
	// PATH, which it returns.
	homebrew := func(t *testing.T, s sandbox) string {
		t.Helper()
		cellar := other(t, s, filepath.Join("brew", "Cellar", "nomi", "0.0.1", "bin"), printing(lines["release"]))
		link := filepath.Join(s.home, "brew", "bin", "nomi")
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(cellar, link); err != nil {
			t.Fatal(err)
		}
		return link
	}
	pathOf := func(files ...string) string {
		dirs := make([]string, len(files))
		for i, f := range files {
			dirs[i] = filepath.Dir(f)
		}
		return strings.Join(append(dirs, tools), sep)
	}
	env := func(s sandbox, version string, more ...string) []string {
		return append([]string{"HOME=" + s.home, "NOMI_VERSION=" + version, "NOMI_TEST_LOG=" + s.log}, more...)
	}
	const replaceCommand = "curl -fsSL https://nomi-lang.org/install.sh | NOMI_REPLACE=1 sh"

	notices := []struct {
		name  string
		first func(t *testing.T, s sandbox) string
		want  string // after the file's name; <dir> is the install directory
	}{
		{"a build in a clone", func(t *testing.T, s sandbox) string { return other(t, s, "clone", printing(lines["clone"])) },
			", a build from source (" + lines["clone"] + ").\nTo remove it and move your editors to this release, run: " + replaceCommand + "\n"},
		{"a go install build", func(t *testing.T, s sandbox) string { return other(t, s, "gobin", printing(lines["go install"])) },
			", a build from source (" + lines["go install"] + ").\nTo remove it and move your editors to this release, run: " + replaceCommand + "\n"},
		{"Homebrew's", homebrew, ", installed by Homebrew.\nTo remove it, run: brew uninstall nomi\n"},
		{"a release", func(t *testing.T, s sandbox) string { return other(t, s, "release", printing(lines["release"])) },
			", a release installed another way (" + lines["release"] + ").\nRemove it yourself, or put <dir> ahead of it on your PATH.\n"},
		{"one that does not answer", func(t *testing.T, s sandbox) string { return other(t, s, "broken", "#!/bin/sh\nexit 1\n") },
			", which this script cannot identify.\nRemove it yourself, or put <dir> ahead of it on your PATH.\n"},
	}
	for _, c := range notices {
		t.Run("another nomi first, "+c.name, func(t *testing.T) {
			s := newSandbox(t)
			first := c.first(t, s)
			before := mustRead(t, first)
			r := run(t, false, pathOf(first, filepath.Join(s.dir, "nomi")), env(s, "v0.2.0")...)
			if r.exit != 0 {
				t.Fatalf("install.sh exits %d:\n%s%s", r.exit, r.stdout, r.stderr)
			}
			want := "\nYour PATH finds another nomi first: " + first + strings.ReplaceAll(c.want, "<dir>", s.dir)
			if !strings.HasSuffix(r.stdout, want) {
				t.Errorf("install.sh prints\n%s\nwant it to end\n%s", r.stdout, want)
			}
			if got := mustRead(t, first); got != before {
				t.Errorf("without NOMI_REPLACE install.sh changed %s", first)
			}
			if exists(s.log) {
				t.Errorf("without NOMI_REPLACE install.sh ran the installed nomi with %q", mustRead(t, s.log))
			}
		})
	}

	quiet := []struct {
		name string
		path func(t *testing.T, s sandbox) string
	}{
		{"no other nomi", func(t *testing.T, s sandbox) string { return pathOf(filepath.Join(s.dir, "nomi")) }},
		{"another nomi after this one", func(t *testing.T, s sandbox) string {
			return pathOf(filepath.Join(s.dir, "nomi"), other(t, s, "clone", printing(lines["clone"])))
		}},
		{"its directory not on PATH yet", func(t *testing.T, s sandbox) string {
			return pathOf(other(t, s, "clone", printing(lines["clone"])))
		}},
		{"a link to this nomi first", func(t *testing.T, s sandbox) string {
			link := filepath.Join(s.home, "links", "nomi")
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(s.dir, "nomi"), link); err != nil {
				t.Fatal(err)
			}
			return pathOf(link, filepath.Join(s.dir, "nomi"))
		}},
	}
	for _, c := range quiet {
		t.Run("this nomi first, "+c.name, func(t *testing.T) {
			s := newSandbox(t)
			r := run(t, false, c.path(t, s), env(s, "v0.2.0")...)
			if r.exit != 0 {
				t.Fatalf("install.sh exits %d:\n%s%s", r.exit, r.stdout, r.stderr)
			}
			if strings.Contains(r.stdout, "another nomi") || !strings.Contains(r.stdout, "nomi editor install <editor>") {
				t.Errorf("install.sh prints\n%s\nwant no line about another nomi, and the editor command", r.stdout)
			}
		})
	}

	t.Run("replace", func(t *testing.T) {
		s := newSandbox(t)
		ours := filepath.Join(s.dir, "nomi")
		clone := other(t, s, "clone", printing(lines["clone"]))
		beside := filepath.Join(filepath.Dir(clone), "nomi-vm")
		mustWrite(t, beside, "a runner\n")
		gobin := other(t, s, "gobin", printing(lines["go install"]))
		// A link on PATH to a build from source elsewhere.
		built := other(t, s, "checkout", printing(lines["clone"]))
		builtLink := filepath.Join(s.home, "linked", "nomi")
		// A link on PATH to the nomi the script installs.
		oursLink := filepath.Join(s.home, "links", "nomi")
		for link, target := range map[string]string{builtLink: built, oursLink: ours} {
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
		}
		brew := homebrew(t, s)
		release := other(t, s, "release", printing(lines["release"]))
		broken := other(t, s, "broken", "#!/bin/sh\necho 'not a nomi'\n")
		folder := filepath.Join(s.home, "folder", "nomi")
		mustWrite(t, filepath.Join(folder, "kept"), "kept\n")
		// Homebrew's directory is on PATH twice, and is named once.
		path := pathOf(clone, gobin, builtLink, oursLink, brew, release, broken, folder, brew, ours)

		r := run(t, true, path, env(s, "v0.2.0", "NOMI_REPLACE=1")...)
		if r.exit != 0 {
			t.Fatalf("install.sh in replace mode exits %d:\n%s%s", r.exit, r.stdout, r.stderr)
		}
		for _, gone := range []string{clone, gobin, builtLink} {
			if exists(gone) {
				t.Errorf("replace mode left %s, a build from source", gone)
			}
		}
		for _, kept := range []string{beside, built, oursLink, brew, release, broken, filepath.Join(folder, "kept"), ours} {
			if !exists(kept) {
				t.Errorf("replace mode removed %s", kept)
			}
		}
		if target, err := filepath.EvalSymlinks(brew); err != nil || !strings.Contains(target, "Cellar") {
			t.Errorf("Homebrew's nomi is now %q, %v", target, err)
		}
		want := "Removed " + clone + ", a build from source (" + lines["clone"] + ")\n" +
			"Removed " + gobin + ", a build from source (" + lines["go install"] + ")\n" +
			"Removed " + builtLink + ", a build from source (" + lines["clone"] + ")\n" +
			"Left " + brew + ", installed by Homebrew. To remove it, run: brew uninstall nomi\n" +
			"Left " + release + ", a release installed another way (" + lines["release"] + "). Remove it yourself if it is not wanted.\n" +
			"Left " + broken + ", which this script cannot identify. Remove it yourself if it is not wanted.\n" +
			"\n\nA clone of the repository that an old nomi was built in can be deleted by hand. Nomi's caches clear themselves.\nIf Neovim loads Nomi's plugin through a plugin manager, remove that entry from your Neovim configuration and run: nomi editor install nvim\n"
		if !strings.HasSuffix(r.stdout, want) {
			t.Errorf("install.sh in replace mode prints\n%s\nwant it to end\n%s", r.stdout, want)
		}
		if strings.Contains(r.stdout, "nomi editor install <editor>") {
			t.Errorf("install.sh in replace mode names the editor command, which it ran:\n%s", r.stdout)
		}
		if got := mustRead(t, s.log); got != "editor install --installed --force\n" {
			t.Errorf("the installed nomi was run with %q, want its editor install --installed --force alone", got)
		}
	})

	t.Run("replace, with steps that fail", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root removes a file from a directory it cannot write")
		}
		s := newSandbox(t)
		locked := other(t, s, "locked", printing(lines["clone"]))
		clone := other(t, s, "clone", printing(lines["clone"]))
		if err := os.Chmod(filepath.Dir(locked), 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(filepath.Dir(locked), 0o755) })
		r := run(t, false, pathOf(locked, clone), env(s, "v0.2.1", "NOMI_REPLACE=1")...)
		if r.exit != 1 {
			t.Fatalf("install.sh, unable to remove a nomi, exits %d, want 1:\n%s%s", r.exit, r.stdout, r.stderr)
		}
		ours := filepath.Join(s.dir, "nomi")
		for _, want := range []string{
			"install.sh: could not remove " + locked + ", a build from source (" + lines["clone"] + ")\n",
			"install.sh: " + ours + " editor install --installed --force failed. Set up each editor with: " + ours + " editor install <editor>\n",
		} {
			if !strings.Contains(r.stderr, want) {
				t.Errorf("install.sh says\n%s\nwant a line %q", r.stderr, want)
			}
		}
		if !exists(locked) || exists(clone) {
			t.Errorf("after a nomi it could not remove, install.sh left the next one: %v", exists(clone))
		}
		if !strings.Contains(r.stdout, "Removed "+clone+", ") || !strings.Contains(r.stdout, "can be deleted by hand") {
			t.Errorf("install.sh prints\n%s\nwant the nomi it removed and its closing line", r.stdout)
		}
		if got := mustRead(t, s.log); got != "editor install --installed --force\n" {
			t.Errorf("the installed nomi was run with %q", got)
		}
	})

	t.Run("replace, checksum mismatch", func(t *testing.T) {
		s := newSandbox(t)
		clone := other(t, s, "clone", printing(lines["clone"]))
		r := run(t, false, pathOf(clone), env(s, "v0.2.2", "NOMI_REPLACE=1")...)
		if r.exit != 1 || !strings.Contains(r.stderr, "nothing was installed") {
			t.Fatalf("install.sh over an archive whose checksum does not match exits %d:\n%s%s", r.exit, r.stdout, r.stderr)
		}
		if !exists(clone) || exists(s.log) || strings.Contains(r.stdout, "Removed") {
			t.Errorf("after a refused install, replace mode acted: the other nomi is there: %v, the installed nomi ran: %v\n%s", exists(clone), exists(s.log), r.stdout)
		}
		if got := dirNames(t, s.dir); len(got) != 0 {
			t.Errorf("after a refused install %s holds %q", s.dir, got)
		}
	})
}

// testInstallScriptProfiles: with its directory not on PATH, install.sh
// adds one line to the profile of the shell $SHELL names, in a home of its
// own per case, and says which file it changed: zsh's .zshrc (under
// ZDOTDIR when set), bash's .bashrc, and on macOS also .bash_profile, or
// .profile when that is the one there; fish's conf.d. A rerun adds nothing.
// Another shell, and NOMI_NO_MODIFY_PATH=1, change no file and print the
// line. A custom NOMI_INSTALL_DIR is the directory the line names.
func testInstallScriptProfiles(t *testing.T, host *releaseHost, run func(*testing.T, bool, string, ...string) installRun, path, goarch string) {
	arch := map[string]string{"amd64": "x86_64", "arm64": "arm64"}[goarch]
	host.publish(t, "v0.1.0", "linux", goarch, "nomi v0.1.0 (linux)", "")
	host.publish(t, "v0.1.1", "darwin", goarch, "nomi v0.1.1 (darwin)", "")
	block := func(line string) string { return "\n# nomi\n" + line + "\n" }
	sh := block(`export PATH="$HOME/.nomi/bin:$PATH"`)
	cases := []struct {
		name  string
		env   []string
		macOS bool
		dir   string            // NOMI_INSTALL_DIR under the home, "" for the default
		pre   map[string]string // the home's files before
		want  map[string]string // the home's files after, but for the install directory
		said  string            // the files the script names, "" when it prints the line
	}{
		{"zsh", []string{"SHELL=/bin/zsh"}, false, "", nil, map[string]string{".zshrc": sh}, "~/.zshrc"},
		{"zsh with ZDOTDIR", []string{"SHELL=/usr/local/bin/zsh", "ZDOTDIR=<home>/zdot"}, false, "",
			map[string]string{"zdot/.zshrc": "setopt autocd\n"}, map[string]string{"zdot/.zshrc": "setopt autocd\n" + sh}, "~/zdot/.zshrc"},
		{"bash on Linux", []string{"SHELL=/bin/bash"}, false, "", nil, map[string]string{".bashrc": sh}, "~/.bashrc"},
		{"bash on macOS", []string{"SHELL=/bin/bash"}, true, "", nil,
			map[string]string{".bashrc": sh, ".bash_profile": sh}, "~/.bashrc and ~/.bash_profile"},
		{"bash on macOS with a .profile", []string{"SHELL=/bin/bash"}, true, "",
			map[string]string{".profile": "umask 022\n"}, map[string]string{".bashrc": sh, ".profile": "umask 022\n" + sh}, "~/.bashrc and ~/.profile"},
		{"fish", []string{"SHELL=/opt/homebrew/bin/fish"}, true, "", nil,
			map[string]string{".config/fish/conf.d/nomi.fish": block(`fish_add_path --path "$HOME/.nomi/bin"`)}, "~/.config/fish/conf.d/nomi.fish"},
		{"another shell", []string{"SHELL=/bin/ksh"}, false, "", nil, map[string]string{}, ""},
		{"NOMI_NO_MODIFY_PATH", []string{"SHELL=/bin/zsh", "NOMI_NO_MODIFY_PATH=1"}, false, "", nil, map[string]string{}, ""},
		{"a custom directory", []string{"SHELL=/bin/zsh"}, false, "tools/bin", nil,
			map[string]string{".zshrc": block(`export PATH="$HOME/tools/bin:$PATH"`)}, "~/.zshrc"},
	}
	for _, c := range cases {
		t.Run("profile, "+c.name, func(t *testing.T) {
			home := t.TempDir()
			for name, content := range c.pre {
				mustWrite(t, filepath.Join(home, name), content)
			}
			dir := filepath.Join(home, ".nomi", "bin")
			env := []string{"HOME=" + home, "NOMI_INSTALL_ARCH=" + arch, "NOMI_INSTALL_OS=Linux", "NOMI_VERSION=v0.1.0"}
			if c.macOS {
				env = append(env, "NOMI_INSTALL_OS=Darwin", "NOMI_VERSION=v0.1.1")
			}
			if c.dir != "" {
				dir = filepath.Join(home, c.dir)
				env = append(env, "NOMI_INSTALL_DIR="+dir)
			}
			for _, kv := range c.env {
				env = append(env, strings.ReplaceAll(kv, "<home>", home))
			}
			r := run(t, false, path, env...)
			if r.exit != 0 {
				t.Fatalf("install.sh exits %d:\n%s%s", r.exit, r.stdout, r.stderr)
			}
			files := homeFiles(t, home, dir)
			if !maps.Equal(files, c.want) {
				t.Errorf("the home holds %q, want %q", files, c.want)
			}
			if c.said == "" {
				if !strings.Contains(r.stdout, dir+" is not on your PATH") || strings.Contains(r.stdout, "Added ") {
					t.Errorf("install.sh changes no profile and prints\n%s\nwant the PATH line", r.stdout)
				}
				return
			}
			for _, want := range []string{
				"Added " + dir + " to PATH in " + c.said + "\n",
				"Open a new terminal to have nomi on your PATH",
				dir + "/nomi editor install <editor>    # helix, nvim, vscode or zed\n",
			} {
				if !strings.Contains(r.stdout, want) {
					t.Errorf("install.sh prints\n%s\nwant a line holding %q", r.stdout, want)
				}
			}
			again := run(t, false, path, env...)
			if again.exit != 0 || strings.Contains(again.stdout, "Added ") {
				t.Errorf("a rerun exits %d and prints\n%s", again.exit, again.stdout)
			}
			if files := homeFiles(t, home, dir); !maps.Equal(files, c.want) {
				t.Errorf("after a rerun the home holds %q, want %q", files, c.want)
			}
		})
	}
}

// homeFiles is every file under home but in the install directory dir, by
// slash path, with its content.
func homeFiles(t *testing.T, home, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(home, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && p == dir {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			rel, _ := filepath.Rel(home, p)
			files[filepath.ToSlash(rel)] = mustRead(t, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
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
