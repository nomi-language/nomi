package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"testing"
	"unicode/utf16"
)

// fakeNomiSource is the nomi.exe the PowerShell installer's test releases
// hold. It prints the text after the last fakeNomiMarker in its own file,
// which the test appends to the built program, past the end any loader
// reads; one build then serves every release. Given `hold`, it says
// "held" and runs until its stdin closes, to stand for a nomi.exe that is
// running when the installer replaces it. Given `editor`, it appends its
// arguments, as one line, to the file NOMI_TEST_LOG names.
const fakeNomiSource = `package main

import (
	"bytes"
	"io"
	"os"
	"strings"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "hold" {
		os.Stdout.WriteString("held\n")
		_, _ = io.Copy(io.Discard, os.Stdin)
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "editor" {
		log, err := os.OpenFile(os.Getenv("NOMI_TEST_LOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			os.Exit(2)
		}
		_, _ = log.WriteString(strings.Join(os.Args[1:], " ") + "\n")
		_ = log.Close()
		return
	}
	exe, err := os.Executable()
	if err != nil {
		os.Exit(2)
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		os.Exit(2)
	}
	marker := []byte("\n#fake-nomi-lines\n")
	i := bytes.LastIndex(data, marker)
	if i < 0 {
		os.Exit(2)
	}
	os.Stdout.Write(data[i+len(marker):])
}
`

const fakeNomiMarker = "\n#fake-nomi-lines\n"

// TestInstallPowerShellScript runs scripts/install.ps1, the script behind
// `irm https://nomi-lang.org/install.ps1 | iex`, the way that command runs
// it, against a local stand-in for the site and the release host, under
// pwsh and under Windows PowerShell 5.1 when there is one. Each case gives
// the script a USERPROFILE of its own. Every case but those about PATH sets
// NOMI_NO_MODIFY_PATH, and those name a file that stands in for the user
// PATH in the registry (NOMI_INSTALL_USER_PATH_FILE), so no case can
// change the real one. Away from Windows the script runs with
// NOMI_INSTALL_OS and NOMI_INSTALL_ARCH standing for Windows on amd64.
//
//   - Run by `irm | iex`, it finds the latest release by its redirect,
//     checks the zip against the checksums file, installs nomi.exe and
//     nothing else, and prints the PATH line and the next command.
//   - With NOMI_VERSION (without its v) and its directory on PATH, it
//     replaces a nomi.exe that is running, which on Windows stays as
//     nomi.exe.old, the name `nomi upgrade` uses; a rerun removes that.
//   - A zip whose SHA-256 is not the one the checksums file gives is
//     refused, and the installed nomi.exe and the directory are as before.
//   - NOMI_INSTALL_DIR is where nomi.exe goes.
//   - Another operating system, 32-bit Windows, another architecture and a
//     version that is not one are refused before any download; arm64
//     Windows gets the amd64 build and a line that says so.
//   - Not on PATH, the directory goes at the front of the user PATH, whose
//     entries stay as they were, and in the window's PATH, so `nomi.exe`
//     resolves in the same window; a rerun, or a user PATH that holds the
//     directory already, adds nothing.
//   - Another nomi.exe on PATH is named when it comes first, and
//     NOMI_REPLACE=1 removes one built from source
//     (testInstallPowerShellScriptOthers).
func TestInstallPowerShellScript(t *testing.T) {
	var shells []string
	for _, name := range []string{"pwsh", "powershell"} {
		if path, err := exec.LookPath(name); err == nil {
			shells = append(shells, path)
		}
	}
	if len(shells) == 0 {
		t.Skip("no pwsh or powershell on PATH to run install.ps1 with; CI's Windows jobs have both")
	}
	script, err := os.ReadFile(filepath.Join("scripts", "install.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	if i := bytes.IndexFunc(script, func(r rune) bool { return r > 0x7f }); i >= 0 {
		t.Fatalf("scripts/install.ps1 holds a byte past ASCII at offset %d; Windows PowerShell may read the file served to it in another encoding", i)
	}
	fake := buildFakeNomi(t)
	for _, shell := range shells {
		t.Run(filepath.Base(shell), func(t *testing.T) {
			testInstallPowerShellScriptUnder(t, shell, script, fake)
		})
	}
}

// buildFakeNomi builds fakeNomiSource for this machine and returns the
// executable's bytes.
func buildFakeNomi(t *testing.T) []byte {
	t.Helper()
	src := t.TempDir()
	mustWrite(t, filepath.Join(src, "go.mod"), "module fakenomi\n\ngo 1.21\n")
	mustWrite(t, filepath.Join(src, "main.go"), fakeNomiSource)
	out := filepath.Join(src, "fake")
	cmd := exec.Command("go", "build", "-o", out, ".")
	cmd.Dir = src
	cmd.Env = append(os.Environ(), "GOFLAGS=", "GOWORK=off")
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building the fake nomi.exe: %v\n%s", err, msg)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// fakeNomiZip is a release zip holding nomi.exe, the fake that prints
// lines, marked executable for a system that reads the mode.
func fakeNomiZip(t *testing.T, fake []byte, lines string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	hdr := &zip.FileHeader{Name: "nomi.exe", Method: zip.Deflate}
	hdr.SetMode(0o755)
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(append(slices.Clip(fake), fakeNomiMarker+lines...)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// encodedCommand is text as PowerShell's -EncodedCommand takes it: base64
// of its UTF-16LE, which no command line's quoting can change.
func encodedCommand(text string) string {
	units := utf16.Encode([]rune(text))
	raw := make([]byte, 2*len(units))
	for i, u := range units {
		binary.LittleEndian.PutUint16(raw[2*i:], u)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

// psRun is how one case runs the script: with USERPROFILE home, the
// window's PATH path, the command after the script tail, and env after the
// rest of the environment.
type psRun struct {
	home, path, tail string
	plain            bool // `irm | iex` as a user types it, its error not caught
	env              []string
}

func testInstallPowerShellScriptUnder(t *testing.T, shell string, script, fake []byte) {
	host := &releaseHost{files: map[string][]byte{}, script: script}
	srv := httptest.NewServer(host)
	defer srv.Close()
	host.url = srv.URL
	// pwsh keeps its caches under HOME away from Windows; they go here,
	// apart from each case's USERPROFILE.
	shellHome := t.TempDir()
	sep := string(os.PathListSeparator)
	basePath := os.Getenv("PATH")

	run := func(t *testing.T, c psRun) installRun {
		t.Helper()
		text := "irm '" + srv.URL + "/install.ps1' | iex"
		if !c.plain {
			text = "try { " + text + " } catch { [Console]::Error.WriteLine($_.Exception.Message); exit 1 }"
		}
		text += c.tail
		var env []string
		for _, kv := range os.Environ() {
			name, _, _ := strings.Cut(kv, "=")
			switch upper := strings.ToUpper(name); {
			case strings.HasPrefix(upper, "NOMI_"), upper == "USERPROFILE", upper == "HOME", upper == "PATH":
				continue
			}
			env = append(env, kv)
		}
		env = append(env, "USERPROFILE="+c.home, "HOME="+shellHome, "PATH="+c.path, "NOMI_INSTALL_BASE_URL="+srv.URL+"/releases")
		if runtime.GOOS != "windows" {
			env = append(env, "NOMI_INSTALL_OS=Win32NT", "NOMI_INSTALL_ARCH=AMD64")
		}
		env = append(env, c.env...)
		if !slices.Contains(env, "NOMI_NO_MODIFY_PATH=1") && !slices.ContainsFunc(env, func(kv string) bool {
			return strings.HasPrefix(kv, "NOMI_INSTALL_USER_PATH_FILE=")
		}) {
			t.Fatal("this case could write the real user PATH: set NOMI_NO_MODIFY_PATH=1 or NOMI_INSTALL_USER_PATH_FILE")
		}
		cmd := exec.Command(shell, "-NoProfile", "-NonInteractive", "-EncodedCommand", encodedCommand(text))
		cmd.Env = env
		var out, errOut strings.Builder
		cmd.Stdout, cmd.Stderr = &out, &errOut
		err := cmd.Run()
		// PowerShell ends a line with CRLF on Windows.
		lf := strings.NewReplacer("\r\n", "\n")
		r := installRun{stdout: lf.Replace(out.String()), stderr: lf.Replace(errOut.String())}
		if exitErr := (*exec.ExitError)(nil); errors.As(err, &exitErr) {
			r.exit = exitErr.ExitCode()
		} else if err != nil {
			t.Fatalf("running install.ps1 under %s: %v", shell, err)
		}
		return r
	}
	runNomi := func(t *testing.T, bin string) string {
		t.Helper()
		out, err := exec.Command(bin).Output()
		if err != nil {
			t.Fatalf("running the installed %s: %v", bin, err)
		}
		return string(out)
	}
	publish := func(version, lines, wrongSum string) string {
		return host.add(version, "nomi_"+version+"_windows_amd64", ".zip", fakeNomiZip(t, fake, lines), wrongSum)
	}
	downloads := func() int { return host.requestCount() - host.requested("/install.ps1") }
	wantLines := func(t *testing.T, r installRun, lines ...string) {
		t.Helper()
		for _, want := range lines {
			if !strings.Contains(r.stdout, want) {
				t.Errorf("install.ps1 prints\n%s\nwant a line holding %q", r.stdout, want)
			}
		}
	}
	next := "nomi editor install <editor>    # helix, nvim, vscode or zed\n"

	home := t.TempDir()
	dir := filepath.Join(home, ".nomi", "bin")
	bin := filepath.Join(dir, "nomi.exe")
	unrunnable := false
	t.Run("install", func(t *testing.T) {
		host.latest = "v0.0.1"
		publish("v0.0.1", "nomi v0.0.1 (first)\n", "")
		r := run(t, psRun{home: home, path: basePath, plain: true, env: []string{"NOMI_NO_MODIFY_PATH=1"}})
		if r.exit != 0 {
			t.Fatalf("install.ps1 exits %d:\n%s%s", r.exit, r.stdout, r.stderr)
		}
		if info, err := os.Stat(bin); runtime.GOOS != "windows" && err == nil && info.Mode()&0o100 == 0 {
			unrunnable = true
			t.Skipf("%s unpacks nomi.exe without its executable bit, so it cannot run on %s; CI's Windows jobs run this test", shell, runtime.GOOS)
		}
		if got := runNomi(t, bin); got != "nomi v0.0.1 (first)\n" {
			t.Errorf("the installed nomi.exe prints %q, want the first release's line", got)
		}
		if got := dirNames(t, dir); !slices.Equal(got, []string{"nomi.exe"}) {
			t.Errorf("%s holds %q, want nomi.exe alone", dir, got)
		}
		if host.requested("/releases/latest") != 1 {
			t.Errorf("install.ps1 asked for the latest release %d times, want once", host.requested("/releases/latest"))
		}
		wantLines(t, r,
			"Installed nomi v0.0.1 (first) at "+bin+"\n",
			dir+" is not on your PATH",
			`$env:Path = "`+dir+sep+`$env:Path"`,
			next)
	})
	if unrunnable {
		t.Skip("the installed nomi.exe cannot run here")
	}

	t.Run("replace a running nomi.exe", func(t *testing.T) {
		held := exec.Command(bin, "hold")
		stdin, err := held.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		stdout, err := held.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := held.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() {
			stdin.Close()
			_ = held.Wait()
		}()
		if line, _ := bufio.NewReader(stdout).ReadString('\n'); line != "held\n" {
			t.Fatalf("the installed nomi.exe, holding, says %q", line)
		}
		stale := "nvim: its Nomi support is from v0.0.1 (first); run `nomi upgrade`"
		publish("v0.0.2", "nomi v0.0.2 (second)\n"+stale+"\n", "")
		latest := host.requested("/releases/latest")
		r := run(t, psRun{home: home, path: dir + sep + basePath, env: []string{"NOMI_VERSION=0.0.2", "NOMI_NO_MODIFY_PATH=1"}})
		if r.exit != 0 {
			t.Fatalf("install.ps1 exits %d:\n%s%s", r.exit, r.stdout, r.stderr)
		}
		if got := runNomi(t, bin); got != "nomi v0.0.2 (second)\n"+stale+"\n" {
			t.Errorf("the reinstalled nomi.exe prints %q, want the second release's lines", got)
		}
		wantLines(t, r, "Installed nomi v0.0.2 (second) at "+bin+"\n"+stale+"\n", "Next, set up your editor:\n  "+next)
		if strings.Contains(r.stdout, "not on your PATH") {
			t.Errorf("with its directory on PATH, install.ps1 prints\n%s\nwant no PATH line", r.stdout)
		}
		if host.requested("/releases/latest") != latest {
			t.Errorf("install.ps1 with NOMI_VERSION asked for the latest release")
		}
		// Windows cannot remove a running executable, and other systems can.
		want := []string{"nomi.exe"}
		if runtime.GOOS == "windows" {
			want = []string{"nomi.exe", "nomi.exe.old"}
			if old := mustRead(t, bin+".old"); !strings.Contains(old, "(first)") {
				t.Errorf("nomi.exe.old is not the nomi.exe that was running")
			}
		}
		if got := dirNames(t, dir); !slices.Equal(got, want) {
			t.Errorf("%s holds %q, want %q", dir, got, want)
		}
	})

	t.Run("rerun", func(t *testing.T) {
		r := run(t, psRun{home: home, path: basePath, env: []string{"NOMI_VERSION=v0.0.1", "NOMI_NO_MODIFY_PATH=1"}})
		if r.exit != 0 {
			t.Fatalf("install.ps1 exits %d:\n%s%s", r.exit, r.stdout, r.stderr)
		}
		if got := runNomi(t, bin); got != "nomi v0.0.1 (first)\n" {
			t.Errorf("the reinstalled nomi.exe prints %q, want the first release's line", got)
		}
		if got := dirNames(t, dir); !slices.Equal(got, []string{"nomi.exe"}) {
			t.Errorf("%s holds %q after a rerun with no nomi.exe running, want nomi.exe alone", dir, got)
		}
	})

	t.Run("checksum mismatch", func(t *testing.T) {
		wrong := strings.Repeat("ab", 32)
		archive := publish("v0.0.3", "nomi v0.0.3 (tampered)\n", wrong)
		r := run(t, psRun{home: home, path: basePath, env: []string{"NOMI_VERSION=v0.0.3", "NOMI_NO_MODIFY_PATH=1"}})
		if r.exit != 1 {
			t.Fatalf("install.ps1 exits %d over an archive whose checksum does not match, want 1:\n%s%s", r.exit, r.stdout, r.stderr)
		}
		want := regexp.MustCompile(`install\.ps1: the SHA-256 of ` + regexp.QuoteMeta(archive) +
			` is [0-9a-f]{64}, and nomi_v0\.0\.3_checksums\.txt says ` + wrong + `; nothing was installed`)
		if !want.MatchString(r.stderr) {
			t.Errorf("install.ps1 says\n%s\nwant a line matching %s", r.stderr, want)
		}
		if got := runNomi(t, bin); got != "nomi v0.0.1 (first)\n" {
			t.Errorf("after a refused install nomi.exe prints %q, want the one installed before", got)
		}
		if got := dirNames(t, dir); !slices.Equal(got, []string{"nomi.exe"}) {
			t.Errorf("after a refused install %s holds %q, want nomi.exe alone", dir, got)
		}
	})

	t.Run("a custom directory", func(t *testing.T) {
		home := t.TempDir()
		custom := filepath.Join(t.TempDir(), "tools", "bin")
		r := run(t, psRun{home: home, path: basePath, env: []string{"NOMI_VERSION=v0.0.2", "NOMI_INSTALL_DIR=" + custom, "NOMI_NO_MODIFY_PATH=1"}})
		if r.exit != 0 {
			t.Fatalf("install.ps1 exits %d:\n%s%s", r.exit, r.stdout, r.stderr)
		}
		if got := dirNames(t, custom); !slices.Equal(got, []string{"nomi.exe"}) {
			t.Errorf("%s holds %q, want nomi.exe alone", custom, got)
		}
		if got := dirNames(t, home); slices.Contains(got, ".nomi") {
			t.Errorf("with NOMI_INSTALL_DIR, USERPROFILE holds %q, want no .nomi", got)
		}
		wantLines(t, r, "Installed nomi v0.0.2 (second) at "+filepath.Join(custom, "nomi.exe")+"\n", custom+" is not on your PATH")
	})

	t.Run("arm64", func(t *testing.T) {
		home := t.TempDir()
		r := run(t, psRun{home: home, path: basePath, env: []string{"NOMI_INSTALL_ARCH=ARM64", "NOMI_VERSION=v0.0.1", "NOMI_NO_MODIFY_PATH=1"}})
		if r.exit != 0 {
			t.Fatalf("install.ps1 exits %d:\n%s%s", r.exit, r.stdout, r.stderr)
		}
		wantLines(t, r,
			"This is arm64 Windows: installing the amd64 build, which Windows runs under emulation.\n",
			"Installed nomi v0.0.1 (first) at "+filepath.Join(home, ".nomi", "bin", "nomi.exe")+"\n")
	})

	refusals := []struct {
		name string
		env  []string
		want []string
	}{
		{"operating system", []string{"NOMI_INSTALL_OS=Unix"},
			[]string{"install.ps1: this script installs on Windows, and found Unix", "curl -fsSL https://nomi-lang.org/install.sh | sh"}},
		{"32-bit Windows", []string{"NOMI_INSTALL_ARCH=x86"},
			[]string{"install.ps1: there is no release of nomi for 32-bit Windows", "https://nomi-lang.org/tooling/install/"}},
		{"architecture", []string{"NOMI_INSTALL_ARCH=IA64"},
			[]string{"install.ps1: there is no release of nomi for the architecture IA64", "https://nomi-lang.org/tooling/install/"}},
		{"version", []string{"NOMI_VERSION=latest"},
			[]string{"install.ps1: latest is not a release version such as v0.0.1"}},
	}
	for _, c := range refusals {
		t.Run("unsupported "+c.name, func(t *testing.T) {
			fresh := filepath.Join(t.TempDir(), "bin")
			before := downloads()
			r := run(t, psRun{home: t.TempDir(), path: basePath, env: append(c.env, "NOMI_INSTALL_DIR="+fresh, "NOMI_NO_MODIFY_PATH=1")})
			if r.exit != 1 {
				t.Fatalf("install.ps1 with %v exits %d, want 1:\n%s%s", c.env, r.exit, r.stdout, r.stderr)
			}
			for _, want := range c.want {
				if !strings.Contains(r.stderr, want) {
					t.Errorf("install.ps1 with %v says\n%s\nwant a line holding %q", c.env, r.stderr, want)
				}
			}
			if downloads() != before {
				t.Errorf("install.ps1 with %v downloaded something before refusing", c.env)
			}
			if _, err := os.Stat(fresh); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("install.ps1 with %v created %s", c.env, fresh)
			}
		})
	}

	testInstallPowerShellScriptPath(t, run)
	testInstallPowerShellScriptOthers(t, run, publish, fake)
}

// testInstallPowerShellScriptOthers: what install.ps1 does about another
// nomi.exe on PATH, as testInstallScriptOthers checks of install.sh. Each
// case has a USERPROFILE of its own and a PATH of its own directories
// alone, since replace mode removes files it finds on PATH. The other
// nomi.exe files are the fake, printing the version line each kind of
// build prints (versionLineFor).
//
//   - With no NOMI_REPLACE it changes nothing. When PATH finds another
//     nomi.exe first, it names the file, what it is and what removes it;
//     it says nothing when this one is first, when its directory is on
//     PATH twice, and when its directory is not on PATH yet.
//   - NOMI_REPLACE=1 removes each nomi.exe built from source and names
//     each it leaves: one under a Cellar, a release, one that does not
//     run. It leaves a directory named nomi.exe and every other file. Then
//     it runs the installed nomi.exe's `editor install --installed
//     --force`, and names no editor command.
//   - After a checksum mismatch replace mode removes nothing and runs
//     nothing.
func testInstallPowerShellScriptOthers(t *testing.T, run func(*testing.T, psRun) installRun, publish func(version, lines, wrongSum string) string, fake []byte) {
	sep := string(os.PathListSeparator)
	rev := &debug.BuildInfo{
		Main:     debug.Module{Version: "(devel)"},
		Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "e8635380091fdebf6ee103ebbd8a47a70eeb0fea"}},
	}
	clone := versionLineFor("", rev, "")
	goInstall := versionLineFor("v0.0.1", &debug.BuildInfo{Main: debug.Module{Version: "v0.0.1"}}, "")
	release := versionLineFor("v0.0.1", rev, "")
	publish("v0.2.0", versionLineFor("v0.2.0", rev, "")+"\n", "")
	publish("v0.2.2", versionLineFor("v0.2.2", rev, "")+"\n", strings.Repeat("cd", 32))

	// other writes a nomi.exe that prints line into the directory name
	// under home, or one that does not run when line is "".
	other := func(t *testing.T, home, name, line string) string {
		t.Helper()
		file := filepath.Join(home, name, "nomi.exe")
		data := "not a program\n"
		if line != "" {
			data = string(fake) + fakeNomiMarker + line + "\n"
		}
		mustWrite(t, file, data)
		if err := os.Chmod(file, 0o755); err != nil {
			t.Fatal(err)
		}
		return file
	}
	cellar := filepath.Join("brew", "Cellar", "nomi", "0.0.1", "bin")
	pathOf := func(files ...string) string {
		dirs := make([]string, len(files))
		for i, f := range files {
			dirs[i] = filepath.Dir(f)
		}
		return strings.Join(dirs, sep)
	}
	env := func(home, version string, more ...string) []string {
		return append([]string{"NOMI_VERSION=" + version, "NOMI_NO_MODIFY_PATH=1", "NOMI_TEST_LOG=" + filepath.Join(home, "log")}, more...)
	}
	const replaceCommand = "$env:NOMI_REPLACE=1; irm https://nomi-lang.org/install.ps1 | iex"

	notices := []struct {
		name, dir, line string
		want            string // after the file's name; <dir> is the install directory
	}{
		{"a build in a clone", "clone", clone,
			", a build from source (" + clone + ").\nTo remove it and move your editors to this release, run: " + replaceCommand + "\n"},
		{"one under a Cellar", cellar, release, ", installed by Homebrew.\nTo remove it, run: brew uninstall nomi\n"},
		{"one that does not run", "broken", "",
			", which this script cannot identify.\nRemove it yourself, or put <dir> ahead of it on your PATH.\n"},
	}
	for _, c := range notices {
		t.Run("another nomi.exe first, "+c.name, func(t *testing.T) {
			home := t.TempDir()
			dir := filepath.Join(home, ".nomi", "bin")
			first := other(t, home, c.dir, c.line)
			r := run(t, psRun{home: home, path: pathOf(first, filepath.Join(dir, "nomi.exe")), env: env(home, "v0.2.0")})
			if r.exit != 0 {
				t.Fatalf("install.ps1 exits %d:\n%s%s", r.exit, r.stdout, r.stderr)
			}
			want := "\nYour PATH finds another nomi first: " + first + strings.ReplaceAll(c.want, "<dir>", dir)
			if !strings.HasSuffix(r.stdout, want) {
				t.Errorf("install.ps1 prints\n%s\nwant it to end\n%s", r.stdout, want)
			}
			if !exists(first) || exists(filepath.Join(home, "log")) {
				t.Errorf("without NOMI_REPLACE install.ps1 removed %s or ran the installed nomi.exe", first)
			}
		})
	}

	quiet := []struct {
		name string
		path func(t *testing.T, home, bin string) string
	}{
		{"no other nomi.exe", func(t *testing.T, home, bin string) string { return pathOf(bin) }},
		{"another nomi.exe after this one", func(t *testing.T, home, bin string) string {
			return pathOf(bin, other(t, home, "clone", clone))
		}},
		{"its directory not on PATH yet", func(t *testing.T, home, bin string) string {
			return pathOf(other(t, home, "clone", clone))
		}},
		{"its directory on PATH twice", func(t *testing.T, home, bin string) string {
			return filepath.Dir(bin) + string(filepath.Separator) + sep + pathOf(bin, other(t, home, "clone", clone))
		}},
	}
	for _, c := range quiet {
		t.Run("this nomi.exe first, "+c.name, func(t *testing.T) {
			home := t.TempDir()
			bin := filepath.Join(home, ".nomi", "bin", "nomi.exe")
			r := run(t, psRun{home: home, path: c.path(t, home, bin), env: env(home, "v0.2.0")})
			if r.exit != 0 {
				t.Fatalf("install.ps1 exits %d:\n%s%s", r.exit, r.stdout, r.stderr)
			}
			if strings.Contains(r.stdout, "another nomi") || !strings.Contains(r.stdout, "nomi editor install <editor>") {
				t.Errorf("install.ps1 prints\n%s\nwant no line about another nomi, and the editor command", r.stdout)
			}
		})
	}

	t.Run("replace", func(t *testing.T) {
		home := t.TempDir()
		bin := filepath.Join(home, ".nomi", "bin", "nomi.exe")
		cloned := other(t, home, "clone", clone)
		beside := filepath.Join(filepath.Dir(cloned), "nomi-vm.exe")
		mustWrite(t, beside, "a runner\n")
		gobin := other(t, home, "gobin", goInstall)
		brew := other(t, home, cellar, release)
		released := other(t, home, "release", release)
		broken := other(t, home, "broken", "")
		folder := filepath.Join(home, "folder", "nomi.exe")
		mustWrite(t, filepath.Join(folder, "kept"), "kept\n")
		// The Cellar's directory is on PATH twice, and is named once.
		path := pathOf(cloned, gobin, brew, released, broken, folder, brew, bin)

		r := run(t, psRun{home: home, path: path, env: env(home, "v0.2.0", "NOMI_REPLACE=1")})
		if r.exit != 0 {
			t.Fatalf("install.ps1 in replace mode exits %d:\n%s%s", r.exit, r.stdout, r.stderr)
		}
		for _, gone := range []string{cloned, gobin} {
			if exists(gone) {
				t.Errorf("replace mode left %s, a build from source", gone)
			}
		}
		for _, kept := range []string{beside, brew, released, broken, filepath.Join(folder, "kept"), bin} {
			if !exists(kept) {
				t.Errorf("replace mode removed %s", kept)
			}
		}
		want := "Removed " + cloned + ", a build from source (" + clone + ")\n" +
			"Removed " + gobin + ", a build from source (" + goInstall + ")\n" +
			"Left " + brew + ", installed by Homebrew. To remove it, run: brew uninstall nomi\n" +
			"Left " + released + ", a release installed another way (" + release + "). Remove it yourself if it is not wanted.\n" +
			"Left " + broken + ", which this script cannot identify. Remove it yourself if it is not wanted.\n" +
			"\n\nA clone of the repository that an old nomi was built in can be deleted by hand. Nomi's caches clear themselves.\nIf Neovim loads Nomi's plugin through a plugin manager, remove that entry from your Neovim configuration and run: nomi editor install nvim\n"
		if !strings.HasSuffix(r.stdout, want) {
			t.Errorf("install.ps1 in replace mode prints\n%s\nwant it to end\n%s", r.stdout, want)
		}
		if strings.Contains(r.stdout, "nomi editor install <editor>") {
			t.Errorf("install.ps1 in replace mode names the editor command, which it ran:\n%s", r.stdout)
		}
		if got := mustRead(t, filepath.Join(home, "log")); got != "editor install --installed --force\n" {
			t.Errorf("the installed nomi.exe was run with %q, want its editor install --installed --force alone", got)
		}
	})

	t.Run("replace, checksum mismatch", func(t *testing.T) {
		home := t.TempDir()
		cloned := other(t, home, "clone", clone)
		r := run(t, psRun{home: home, path: pathOf(cloned), env: env(home, "v0.2.2", "NOMI_REPLACE=1")})
		if r.exit != 1 || !strings.Contains(r.stderr, "nothing was installed") {
			t.Fatalf("install.ps1 over an archive whose checksum does not match exits %d:\n%s%s", r.exit, r.stdout, r.stderr)
		}
		if !exists(cloned) || exists(filepath.Join(home, "log")) || strings.Contains(r.stdout, "Removed") {
			t.Errorf("after a refused install, replace mode acted: the other nomi.exe is there: %v\n%s", exists(cloned), r.stdout)
		}
	})
}

// testInstallPowerShellScriptPath: with its directory not on the window's
// PATH, install.ps1 puts it at the front of the user PATH, held here by the
// file NOMI_INSTALL_USER_PATH_FILE names, keeping every entry as written,
// and in the window's PATH, where `nomi.exe` then resolves to it. A rerun,
// and a user PATH that names the directory already (with a trailing
// separator here), write nothing. NOMI_NO_MODIFY_PATH=1 writes nothing and
// prints the line.
func testInstallPowerShellScriptPath(t *testing.T, run func(*testing.T, psRun) installRun) {
	resolves := "; Write-Host ('nomi.exe resolves to ' + (Get-Command nomi.exe -CommandType Application | Select-Object -First 1).Source)"
	path := os.Getenv("PATH")
	cases := []struct {
		name     string
		noModify bool
		pre      string // the user PATH before, "-" for none
		want     string // the user PATH after; <dir> is the install directory
	}{
		{"added", false, "-", "<dir>"},
		{"added before the entries there", false, `C:\Tools;%USERPROFILE%\bin;`, `<dir>;C:\Tools;%USERPROFILE%\bin;`},
		{"already there", false, `C:\Tools;<dir>` + string(filepath.Separator), `C:\Tools;<dir>` + string(filepath.Separator)},
		{"NOMI_NO_MODIFY_PATH", true, `C:\Tools`, `C:\Tools`},
	}
	for _, c := range cases {
		t.Run("user PATH, "+c.name, func(t *testing.T) {
			home := t.TempDir()
			dir := filepath.Join(home, ".nomi", "bin")
			store := filepath.Join(t.TempDir(), "user-path.txt")
			if c.pre != "-" {
				mustWrite(t, store, strings.ReplaceAll(c.pre, "<dir>", dir))
			}
			want := strings.ReplaceAll(c.want, "<dir>", dir)
			env := []string{"NOMI_VERSION=v0.0.1", "NOMI_INSTALL_USER_PATH_FILE=" + store}
			tail := resolves
			if c.noModify {
				env = append(env, "NOMI_NO_MODIFY_PATH=1")
				tail = ""
			}
			for i, label := range []string{"first run", "rerun"} {
				r := run(t, psRun{home: home, path: path, tail: tail, env: env})
				if r.exit != 0 {
					t.Fatalf("install.ps1's %s exits %d:\n%s%s", label, r.exit, r.stdout, r.stderr)
				}
				if got := mustRead(t, store); got != want {
					t.Errorf("after the %s the user PATH is %q, want %q", label, got, want)
				}
				added := strings.Contains(r.stdout, "Added "+dir+" to your user PATH")
				if wantAdded := i == 0 && strings.ReplaceAll(c.pre, "<dir>", dir) != want; added != wantAdded {
					t.Errorf("the %s prints\n%s\nwant a line saying it added %s to the user PATH: %v", label, r.stdout, dir, wantAdded)
				}
				if c.noModify {
					if !strings.Contains(r.stdout, dir+" is not on your PATH") {
						t.Errorf("with NOMI_NO_MODIFY_PATH=1 install.ps1 prints\n%s\nwant the PATH line", r.stdout)
					}
					continue
				}
				if want := "nomi.exe resolves to " + filepath.Join(dir, "nomi.exe") + "\n"; !strings.Contains(r.stdout, want) {
					t.Errorf("after the %s, in the same window, install.ps1 prints\n%s\nwant a line holding %q", label, r.stdout, want)
				}
			}
		})
	}
}
