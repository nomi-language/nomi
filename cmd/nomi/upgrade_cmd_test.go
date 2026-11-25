package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/buildversion"
)

// editorSandboxEnv is this process's environment with every variable an
// editor's directory is found from pointed at home, so a nomi run with it
// reads no real editor directory.
func editorSandboxEnv(home string) []string {
	return append(os.Environ(), editorHomeVars(home)...)
}

// editorHomeVars are the variables editorSandboxEnv sets.
func editorHomeVars(home string) []string {
	return []string{"HOME=" + home, "XDG_CONFIG_HOME=", "XDG_DATA_HOME=", "FLATPAK_XDG_DATA_HOME=",
		"APPDATA=" + home, "LOCALAPPDATA=" + home}
}

// upgradeSandbox is a nomi installed in dir that the in-process `nomi
// upgrade` replaces, the stand-in release host it downloads from, a home
// whose editor directories are temporary, and the runs of the new nomi.
type upgradeSandbox struct {
	host     *releaseHost
	home     string
	exe      string
	newRuns  [][]string
	nvimPack string
}

// newUpgradeSandbox stands in this process as a release nomi of stamp,
// installed as nomi in a temporary directory holding "the old nomi", with
// the release host's latest release latest. PATH holds only a fake cc, so
// Neovim's support can be rewritten and no other editor's command is found.
func newUpgradeSandbox(t *testing.T, stamp, latest string) *upgradeSandbox {
	t.Helper()
	s := &upgradeSandbox{host: &releaseHost{files: map[string][]byte{}, latest: latest}, home: t.TempDir()}
	srv := httptest.NewServer(s.host)
	t.Cleanup(srv.Close)
	s.host.url = srv.URL
	t.Setenv("NOMI_RELEASE_BASE_URL", srv.URL+"/releases")
	t.Cleanup(buildversion.SetStampForTest(stamp))

	s.exe = installedNomi(t, filepath.Join(t.TempDir(), "bin"))
	oldExe, oldRun := upgradeExecutable, runNewNomi
	t.Cleanup(func() { upgradeExecutable, runNewNomi = oldExe, oldRun })
	upgradeExecutable = func() (string, error) { return s.exe, nil }
	runNewNomi = func(_ io.Writer, exe string, args ...string) error {
		s.newRuns = append(s.newRuns, append([]string{exe}, args...))
		return nil
	}

	for _, kv := range editorHomeVars(s.home) {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	if runtime.GOOS != "windows" {
		mustWrite(t, filepath.Join(bin, "cc"), "#!/bin/sh\nwhile [ $# -gt 0 ]; do\n  if [ \"$1\" = -o ]; then out=\"$2\"; fi\n  shift\ndone\nprintf parser > \"$out\"\n")
		if err := os.Chmod(filepath.Join(bin, "cc"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	s.nvimPack = filepath.Join(s.home, ".local", "share", "nvim", "site", "pack", "nomi", "start", "nomi")
	if runtime.GOOS == "windows" {
		s.nvimPack = filepath.Join(s.home, "nvim-data", "site", "pack", "nomi", "start", "nomi")
	}
	return s
}

// installedNomi writes "the old nomi" as nomi in dir and answers its path
// with its symlinks resolved, as upgrade names it (the system's temporary
// directory is a link on macOS).
func installedNomi(t *testing.T, dir string) string {
	t.Helper()
	bin := exe(filepath.Join(dir, "nomi"))
	mustWrite(t, bin, "the old nomi")
	resolved, err := filepath.EvalSymlinks(bin)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

// publish adds a release of version whose archive for this platform holds
// nomi, with a checksums file that gives the archive's SHA-256, or zeros
// when wrongSum is set.
func (s *upgradeSandbox) publish(t *testing.T, version, nomi string, wrongSum bool) {
	t.Helper()
	var buf bytes.Buffer
	archive := fmt.Sprintf("nomi_%s_%s_%s", version, runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		archive += ".zip"
		zw := zip.NewWriter(&buf)
		w, err := zw.Create("nomi.exe")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(nomi)); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		archive += ".tar.gz"
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		if err := tw.WriteHeader(&tar.Header{Name: "./nomi", Mode: 0o755, Size: int64(len(nomi)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(nomi)); err != nil {
			t.Fatal(err)
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
	}
	sum := sha256.Sum256(buf.Bytes())
	hexSum := hex.EncodeToString(sum[:])
	if wrongSum {
		hexSum = strings.Repeat("0", 64)
	}
	s.host.mu.Lock()
	defer s.host.mu.Unlock()
	s.host.files[version+"/"+archive] = buf.Bytes()
	s.host.files[version+"/nomi_"+version+"_checksums.txt"] = []byte(hexSum + "  ./" + archive + "\n")
}

// markNvim stands in Neovim support `nomi editor install` wrote from
// version.
func (s *upgradeSandbox) markNvim(t *testing.T, version string) {
	t.Helper()
	mustWrite(t, filepath.Join(s.nvimPack, "nomi-version"), version+"\n")
}

func (s *upgradeSandbox) nvimVersion(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(s.nvimPack, "nomi-version"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(data))
}

// installed is what the installed nomi holds, and its directory's other
// files.
func (s *upgradeSandbox) installed(t *testing.T) (string, []string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(s.exe))
	if err != nil {
		t.Fatal(err)
	}
	var others []string
	for _, e := range entries {
		if e.Name() != filepath.Base(s.exe) {
			others = append(others, e.Name())
		}
	}
	return mustRead(t, s.exe), others
}

func (s *upgradeSandbox) upgrade(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	err := runUpgrade(&out, &errOut, args)
	return out.String(), errOut.String(), err
}

// TestUpgrade_ReplacesAnOlderRelease: a release nomi upgrades to the latest
// release: the file is the archive's nomi, nothing else is left in its
// directory, and the new nomi is run to rewrite the editors' support.
func TestUpgrade_ReplacesAnOlderRelease(t *testing.T) {
	s := newUpgradeSandbox(t, "v0.0.1", "v0.0.2")
	s.publish(t, "v0.0.2", "the new nomi", false)
	s.markNvim(t, "v0.0.1 (aaaaaaaaaaaa)")
	out, errOut, err := s.upgrade(t)
	if err != nil {
		t.Fatalf("nomi upgrade: %v\n%s%s", err, out, errOut)
	}
	if want := "Upgraded nomi from v0.0.1 to v0.0.2 at " + s.exe + "\n" +
		"Rewrote nvim's Nomi support\n" +
		"Restart your editor to load the new files.\n"; out != want {
		t.Errorf("nomi upgrade prints\n%s\nwant\n%s", out, want)
	}
	if got, others := s.installed(t); got != "the new nomi" || len(others) != 0 {
		t.Errorf("the installed nomi holds %q, beside %q", got, others)
	}
	if want := [][]string{{s.exe, "editor", "install", "nvim"}}; !slices.EqualFunc(s.newRuns, want, slices.Equal) {
		t.Errorf("the new nomi ran %q, want %q", s.newRuns, want)
	}
}

// TestUpgrade_ANamedOlderRelease: a version named, an older one too, is the
// one installed, with or without its v.
func TestUpgrade_ANamedOlderRelease(t *testing.T) {
	s := newUpgradeSandbox(t, "v0.0.2", "v0.0.2")
	s.publish(t, "v0.0.1", "the first nomi", false)
	out, errOut, err := s.upgrade(t, "0.0.1")
	if err != nil {
		t.Fatalf("nomi upgrade 0.0.1: %v\n%s%s", err, out, errOut)
	}
	if want := "Upgraded nomi from v0.0.2 to v0.0.1 at " + s.exe + "\n"; out != want {
		t.Errorf("nomi upgrade 0.0.1 prints\n%s\nwant\n%s", out, want)
	}
	if got, _ := s.installed(t); got != "the first nomi" {
		t.Errorf("the installed nomi holds %q", got)
	}
	for _, bad := range []string{"main", "v1", "v0.0.1-3-gabcdef1"} {
		if _, _, err := s.upgrade(t, bad); err == nil || err.Error() != "nomi upgrade: "+bad+" is not a release version such as v0.0.1" &&
			err.Error() != "nomi upgrade: v"+bad+" is not a release version such as v0.0.1" {
			t.Errorf("nomi upgrade %s: %v", bad, err)
		}
	}
}

// TestUpgrade_ABadChecksumChangesNothing: an archive whose SHA-256 is not
// the checksums file's is refused, and the installed nomi, its directory
// and the editors' support are as they were.
func TestUpgrade_ABadChecksumChangesNothing(t *testing.T) {
	s := newUpgradeSandbox(t, "v0.0.1", "v0.0.2")
	s.publish(t, "v0.0.2", "a tampered nomi", true)
	s.markNvim(t, "v0.0.1 (aaaaaaaaaaaa)")
	_, _, err := s.upgrade(t)
	if err == nil || !strings.Contains(err.Error(), "does not match the release's checksum file") ||
		!strings.Contains(err.Error(), "Nothing was installed") {
		t.Fatalf("nomi upgrade of a tampered archive: %v", err)
	}
	if got, others := s.installed(t); got != "the old nomi" || len(others) != 0 {
		t.Errorf("after a refused upgrade the installed nomi holds %q, beside %q", got, others)
	}
	if len(s.newRuns) != 0 || s.nvimVersion(t) != "v0.0.1 (aaaaaaaaaaaa)" {
		t.Errorf("a refused upgrade ran %q and left Neovim's support from %s", s.newRuns, s.nvimVersion(t))
	}
}

// TestUpgrade_AlreadyTheLatestRewritesTheEditors: at the latest release,
// upgrade says so, downloads nothing and replaces nothing, and rewrites the
// support nomi wrote from another version.
func TestUpgrade_AlreadyTheLatestRewritesTheEditors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake cc is a shell script")
	}
	s := newUpgradeSandbox(t, "v0.0.2", "v0.0.2")
	s.markNvim(t, "v0.0.1 (aaaaaaaaaaaa)")
	out, errOut, err := s.upgrade(t)
	if err != nil {
		t.Fatalf("nomi upgrade: %v\n%s%s", err, out, errOut)
	}
	want := "nomi at " + s.exe + " is already the latest release, v0.0.2\n" +
		"Rewrote nvim's Nomi support\n" +
		"Restart your editor to load the new files.\n"
	if out != want {
		t.Errorf("nomi upgrade prints\n%s\nwant\n%s", out, want)
	}
	if got := s.nvimVersion(t); got != buildversion.Name() {
		t.Errorf("Neovim's support is from %s, want %s", got, buildversion.Name())
	}
	if got, _ := s.installed(t); got != "the old nomi" || len(s.newRuns) != 0 || s.host.requestCount() != 1 {
		t.Errorf("the installed nomi holds %q after %d requests, and the new nomi ran %q", got, s.host.requestCount(), s.newRuns)
	}
	// Now nothing is stale: one line, and no restart.
	if out, _, err := s.upgrade(t, "v0.0.2"); err != nil || out != "nomi at "+s.exe+" is already v0.0.2\n" {
		t.Errorf("a second nomi upgrade: %v\n%s", err, out)
	}
}

// TestUpgrade_CheckChangesNothing: --check prints this nomi, the latest
// release, what it would install and the support it would rewrite, exits
// 0, and replaces, downloads and rewrites nothing.
func TestUpgrade_CheckChangesNothing(t *testing.T) {
	s := newUpgradeSandbox(t, "v0.0.1", "v0.0.2")
	s.publish(t, "v0.0.2", "the new nomi", false)
	s.markNvim(t, "v0.0.1 (aaaaaaaaaaaa)")
	out, errOut, err := s.upgrade(t, "--check")
	if err != nil {
		t.Fatalf("nomi upgrade --check: %v\n%s%s", err, out, errOut)
	}
	want := "This nomi is " + buildversion.Name() + ", at " + s.exe + "\n" +
		"The latest release is v0.0.2\n" +
		"nomi upgrade would install v0.0.2\n" +
		"It would rewrite the Nomi support of nvim (from v0.0.1 (aaaaaaaaaaaa))\n"
	if out != want {
		t.Errorf("nomi upgrade --check prints\n%s\nwant\n%s", out, want)
	}
	if got, _ := s.installed(t); got != "the old nomi" || len(s.newRuns) != 0 || s.nvimVersion(t) != "v0.0.1 (aaaaaaaaaaaa)" {
		t.Errorf("--check changed something: nomi holds %q, the new nomi ran %q", got, s.newRuns)
	}
	if n := s.host.requested("/releases/download/v0.0.2/nomi_v0.0.2_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"); n != 0 {
		t.Errorf("--check downloaded the archive %d times", n)
	}
}

// TestUpgrade_HomebrewAndSourceBuildsRefuse: a nomi in a Homebrew Cellar,
// and one built from source, is not replaced: upgrade names the command
// that updates it, downloads nothing, still rewrites the editors' support,
// and exits 1.
func TestUpgrade_HomebrewAndSourceBuildsRefuse(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake cc is a shell script")
	}
	cases := []struct {
		name, stamp string
		cellar      bool
		args        []string
		want        string
	}{
		{"homebrew", "v0.0.1", true, nil, "This nomi was installed by Homebrew, so nomi upgrade does not replace it.\nTo update it: brew upgrade nomi\n"},
		{"go install", "", false, nil, "This nomi was built with go install, so nomi upgrade does not replace it.\nTo update it: go install github.com/nomi-language/nomi/cmd/nomi@latest\n"},
		{"go install of a version", "", false, []string{"v0.0.1"}, "This nomi was built with go install, so nomi upgrade does not replace it.\nTo update it: go install github.com/nomi-language/nomi/cmd/nomi@v0.0.1\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newUpgradeSandbox(t, c.stamp, "v0.0.2")
			if c.cellar {
				s.exe = installedNomi(t, filepath.Join(t.TempDir(), "Cellar", "nomi", "0.0.1", "bin"))
				link := filepath.Join(t.TempDir(), "nomi")
				if err := os.Symlink(s.exe, link); err != nil {
					t.Fatal(err)
				}
				upgradeExecutable = func() (string, error) { return link, nil }
			}
			s.markNvim(t, "v0.0.1 (aaaaaaaaaaaa)")
			out, errOut, err := s.upgrade(t, c.args...)
			if !errors.Is(err, errUpgradeRefused) || errOut != c.want {
				t.Errorf("nomi upgrade: %v, says\n%s\nwant\n%s", err, errOut, c.want)
			}
			if !strings.Contains(out, "Rewrote nvim's Nomi support\n") || s.nvimVersion(t) != buildversion.Name() {
				t.Errorf("a refused upgrade did not rewrite Neovim's support:\n%s", out)
			}
			if got, _ := s.installed(t); got != "the old nomi" || len(s.newRuns) != 0 || s.host.requestCount() != 0 {
				t.Errorf("a refused upgrade: nomi holds %q after %d requests", got, s.host.requestCount())
			}
		})
	}
}

// TestUpgrade_WhoUpdatesEachInstall: a Cellar path is Homebrew's whatever
// built it, a stamped binary elsewhere upgrades itself, a build that
// records a commit is a clone's, and one that records none is go install's.
func TestUpgrade_WhoUpdatesEachInstall(t *testing.T) {
	clone := &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "0123456789abcdef"}}}
	proxy := &debug.BuildInfo{Main: debug.Module{Version: "v0.0.1"}}
	cases := []struct {
		exe    string
		stamp  bool
		info   *debug.BuildInfo
		want   installKind
		update string
	}{
		{"/opt/homebrew/Cellar/nomi/0.0.1/bin/nomi", true, nil, installHomebrew, "brew upgrade nomi"},
		{"/home/linuxbrew/.linuxbrew/Cellar/nomi/0.0.1/bin/nomi", false, clone, installHomebrew, "brew upgrade nomi"},
		{"/home/me/.nomi/bin/nomi", true, clone, installRelease, ""},
		{"/home/me/go/bin/nomi", false, clone, installCheckout, "git pull && make build-cli"},
		{"/home/me/go/bin/nomi", false, proxy, installGoInstall, "go install github.com/nomi-language/nomi/cmd/nomi@latest"},
	}
	if runtime.GOOS == "windows" {
		// Windows has no make.
		cases[3].update = "git pull && go install ./cmd/nomi"
	}
	for _, c := range cases {
		got := installKindOf(c.exe, c.stamp, c.info)
		if got != c.want || c.update != "" && !strings.HasSuffix(got.updateCommand(""), c.update) {
			t.Errorf("%s (stamped %v): kind %d, %q; want %d, ending %q", c.exe, c.stamp, got, got.updateCommand(""), c.want, c.update)
		}
	}
}

// TestUpgrade_ReplacingAnExecutable: the new file is renamed over the old,
// so a process reading the old one keeps it, and on Windows the running
// file is moved aside first, which removeAside removes. Both paths run on
// every system. A directory that cannot be written is named, and nothing
// changes.
func TestUpgrade_ReplacingAnExecutable(t *testing.T) {
	for _, goos := range []string{"linux", "windows"} {
		t.Run(goos, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "nomi")
			mustWrite(t, bin, "old")
			held, err := os.Open(bin)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			if err := replaceExecutable(bin, []byte("new"), goos); err != nil {
				t.Fatal(err)
			}
			if got := mustRead(t, bin); got != "new" {
				t.Errorf("%s holds %q", bin, got)
			}
			var kept bytes.Buffer
			if _, err := kept.ReadFrom(held); err != nil || kept.String() != "old" {
				t.Errorf("the file held open reads %q, %v", kept.String(), err)
			}
			want := []string{"nomi"}
			if goos == "windows" {
				want = []string{"nomi", "nomi.old"}
				if got := mustRead(t, asideName(bin)); got != "old" {
					t.Errorf("the aside file holds %q", got)
				}
			}
			if got := dirNames(t, dir); !slices.Equal(got, want) {
				t.Errorf("%s holds %q, want %q", dir, got, want)
			}
			removeAside(bin)
			if got := dirNames(t, dir); !slices.Equal(got, []string{"nomi"}) {
				t.Errorf("after removeAside %s holds %q", dir, got)
			}
			removeAside(bin) // nothing to remove, and nothing said
		})
	}
	t.Run("a directory nomi cannot write", func(t *testing.T) {
		if runtime.GOOS == "windows" || os.Geteuid() == 0 {
			t.Skip("needs a directory the test cannot write to")
		}
		dir := t.TempDir()
		bin := filepath.Join(dir, "nomi")
		mustWrite(t, bin, "old")
		if err := os.Chmod(dir, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
		err := replaceExecutable(bin, []byte("new"), runtime.GOOS)
		if err == nil || !strings.HasPrefix(err.Error(), dir+", the directory nomi is in, is not writable: ") {
			t.Errorf("got %v, want an error naming %s", err, dir)
		}
		if got := mustRead(t, bin); got != "old" {
			t.Errorf("%s holds %q", bin, got)
		}
	})
}

// dirNames is the names of what dir holds, nil when there is no dir.
func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// TestUpgrade_VersionNamesStaleEditors: `nomi --version` prints a line per
// editor whose support nomi wrote from another version, with the command
// that rewrites it, and nothing for support from this version, for a dev
// extension, or for a directory it cannot read.
func TestUpgrade_VersionNamesStaleEditors(t *testing.T) {
	home := t.TempDir()
	env := editorSandboxEnv(home)
	version := func() observedRun {
		t.Helper()
		o := runProcess(t, env, home, "", nomiBin, "--version")
		if o.exit != 0 || o.stderr != "" {
			t.Fatalf("nomi --version: exit %d\n%s", o.exit, o.transcript())
		}
		return o
	}
	first := version().stdout
	if strings.Count(first, "\n") != 1 {
		t.Fatalf("with no editor set up, nomi --version prints\n%s", first)
	}
	pack := filepath.Join(home, ".local", "share", "nvim", "site", "pack", "nomi", "start", "nomi")
	if runtime.GOOS == "windows" {
		pack = filepath.Join(home, "nvim-data", "site", "pack", "nomi", "start", "nomi")
	}
	mustWrite(t, filepath.Join(pack, "nomi-version"), "v0.0.1 (aaaaaaaaaaaa)\n")
	want := first + "nvim: its Nomi support is from v0.0.1 (aaaaaaaaaaaa); run `nomi editor install nvim`\n"
	if got := version().stdout; got != want {
		t.Errorf("nomi --version prints\n%s\nwant\n%s", got, want)
	}
	mustWrite(t, filepath.Join(pack, "nomi-version"), currentVersion(t)+"\n")
	if got := version().stdout; got != first {
		t.Errorf("with this version's support, nomi --version prints\n%s", got)
	}
	// A marker that cannot be read: a directory in its place.
	if err := os.Remove(filepath.Join(pack, "nomi-version")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(pack, "nomi-version"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := version().stdout; got != first {
		t.Errorf("with an unreadable marker, nomi --version prints\n%s", got)
	}
}

// TestUpgrade_UsageAndHelp: upgrade's words it does not take are usage
// errors, and `nomi help upgrade` prints its usage.
func TestUpgrade_UsageAndHelp(t *testing.T) {
	home := t.TempDir()
	env := editorSandboxEnv(home)
	for _, args := range [][]string{{"upgrade", "--force"}, {"upgrade", "v0.0.1", "v0.0.2"}} {
		o := runProcess(t, env, home, "", nomiBin, args...)
		if o.exit != 1 || !strings.Contains(o.stderr, upgradeUsage) {
			t.Errorf("nomi %s: exit %d\n%s", strings.Join(args, " "), o.exit, o.transcript())
		}
	}
	if o := runProcess(t, env, home, "", nomiBin, "help", "upgrade"); o.exit != 0 || o.stdout != upgradeUsage+"\n" {
		t.Errorf("nomi help upgrade: exit %d\n%s", o.exit, o.transcript())
	}
}
