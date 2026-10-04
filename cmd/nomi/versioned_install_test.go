package main

import (
	"bytes"
	"debug/buildinfo"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/mod/module"
	modzip "golang.org/x/mod/zip"

	"github.com/nomi-language/nomi/internal/ffirun"
)

// versionedTestVersion is the version the test publishes this tree as. It
// is never a real tag, so nothing can fetch it from anywhere but the test's
// own proxy.
const versionedTestVersion = "v0.99.0"

// TestVersionedInstallRunsAGoFFIProject installs `nomi` the way a user does,
// `go install github.com/nomi-language/nomi/cmd/nomi@<version>` with
// -trimpath, from a file GOPROXY serving this tree as v0.99.0, and runs and
// builds a project with Go bindings from a directory with no checkout in
// reach. The wrapper must require the compiler module at that version and
// let Go fetch it: there is no source tree for it to replace the module with.
//
// Everything Go fetches comes from the test's proxy and, for the compiler's
// own dependencies, the parent's module cache served as a second file proxy,
// so the test needs no network. GOMODCACHE and GOPATH are private, so the
// installed module is fetched by this test and deleted with it.
func TestVersionedInstallRunsAGoFFIProject(t *testing.T) {
	if testing.Short() {
		t.Skip("installs nomi from a module proxy and builds a wrapper; -short")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go toolchain")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git to list the module's files")
	}
	root := repoRoot(t)
	tmp := t.TempDir()
	proxy := filepath.Join(tmp, "proxy")
	publishModule(t, root, proxy, versionedTestVersion)

	parentModCache := goEnv(t, goBin, "GOMODCACHE")
	goProxy := "file://" + filepath.ToSlash(proxy) + ",file://" +
		filepath.ToSlash(filepath.Join(parentModCache, "cache", "download"))
	cacheRoot := filepath.Join(tmp, "nomi-cache")
	gobin := filepath.Join(tmp, "bin")
	env := []string{
		"HOME=" + filepath.Join(tmp, "home"),
		"PATH=" + filepath.Dir(goBin) + string(os.PathListSeparator) + "/usr/bin" + string(os.PathListSeparator) + "/bin",
		"TERM=dumb",
		"NOMI_COLOR=never",
		"NOMI_FFIRUN_CACHE_ROOT=" + cacheRoot,
		"GOPROXY=" + goProxy,
		"GOSUMDB=off",
		"GOTOOLCHAIN=local",
		"GOMODCACHE=" + filepath.Join(tmp, "modcache"),
		"GOPATH=" + filepath.Join(tmp, "gopath"),
		"GOCACHE=" + goEnv(t, goBin, "GOCACHE"),
		"GOBIN=" + gobin,
		"GOFLAGS=-modcacherw",
	}

	install := exec.Command(goBin, "install", "-trimpath", "github.com/nomi-language/nomi/cmd/nomi@"+versionedTestVersion)
	install.Dir = tmp
	install.Env = env
	if out, err := install.CombinedOutput(); err != nil {
		t.Fatalf("go install: %v\n%s", err, out)
	}
	nomi := filepath.Join(gobin, "nomi")
	info, err := buildinfo.ReadFile(nomi)
	if err != nil {
		t.Fatal(err)
	}
	if got := ffirun.CompilerModuleVersion(info); got != versionedTestVersion {
		t.Fatalf("the installed nomi says %s %s, which is not versioned as %s", info.Main.Path, info.Main.Version, versionedTestVersion)
	}

	proj := filepath.Join(tmp, "proj")
	files := map[string]string{
		"nomi.toml": "[module]\nname = \"shouter\"\nentry_points = [\"main\"]\n",
		"go.mod":    "module shouter\n\ngo 1.27.0\n",
		"shout.go":  "package shouter\n\nimport \"strings\"\n\nfunc Shout(s string) string { return strings.ToUpper(s) }\n",
		"ffi.nomi":  "gopkg \"shouter\" as shout\n\npub fn shout_it(s: String): String go shout.Shout\n",
		"main.nomi": "import {\n  std/io\n  ffi\n}\n\nfn main() {\n  io.print(ffi.shout_it(\"quiet\"))\n}\n",
	}
	for name, content := range files {
		mustWrite(t, filepath.Join(proj, name), content)
	}
	hello := filepath.Join(tmp, "hello", "hello.nomi")
	mustWrite(t, hello, releaseHello)

	run := func(dir string, name string, args ...string) string {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %s: %v\n%s", filepath.Base(name), strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}

	if got := run(proj, nomi, "run", "main.nomi"); got != "QUIET" {
		t.Fatalf("nomi run printed %q, want QUIET", got)
	}
	assertVersionedWrappers(t, cacheRoot)

	if err := os.MkdirAll(filepath.Join(tmp, "out"), 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(tmp, "out", "shouter")
	run(proj, nomi, "build", "main.nomi", "-o", exe)
	if got := run(tmp, exe); got != "QUIET" {
		t.Fatalf("the built FFI executable printed %q, want QUIET", got)
	}

	// A pure-Nomi program has no release to download a runner from (this
	// nomi is not a release archive), so its runner is built from the module
	// version too.
	helloExe := filepath.Join(tmp, "out", "hello")
	run(filepath.Dir(hello), nomi, "build", hello, "-o", helloExe)
	if got := run(tmp, helloExe); got != "hi" {
		t.Fatalf("the built pure-Nomi executable printed %q, want hi", got)
	}
	assertVersionedWrappers(t, cacheRoot)
}

// assertVersionedWrappers checks every go.mod the run staged under cacheRoot:
// each requires the compiler module at the test's version, none replaces it,
// and there is at least one.
func assertVersionedWrappers(t *testing.T, cacheRoot string) {
	t.Helper()
	n := 0
	err := filepath.WalkDir(cacheRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "go.mod" {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		got := string(data)
		if !strings.Contains(got, "github.com/nomi-language/nomi "+versionedTestVersion) {
			t.Errorf("%s does not require the compiler module at %s:\n%s", path, versionedTestVersion, got)
		}
		if strings.Contains(got, "replace github.com/nomi-language/nomi") || strings.Contains(got, "replace (") {
			t.Errorf("%s replaces a module; the versioned wrapper must fetch the compiler:\n%s", path, got)
		}
		n++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatalf("no staged go.mod under %s, so the run did not take the wrapper path", cacheRoot)
	}
}

func goEnv(t *testing.T, goBin, name string) string {
	t.Helper()
	out, err := exec.Command(goBin, "env", name).Output()
	if err != nil {
		t.Fatalf("go env %s: %v", name, err)
	}
	return strings.TrimSpace(string(out))
}

// publishModule writes the files a GOPROXY serves for the compiler module at
// version into proxy: the module zip holds this checkout's working tree,
// tracked files and new ones git does not ignore, as the proxy would hold it
// once committed, tagged and pushed. zip.Create leaves out what a real module
// zip leaves out (nested modules, vendor directories).
func publishModule(t *testing.T, root, proxy, version string) {
	t.Helper()
	out, err := exec.Command("git", "-C", root, "ls-files", "-z", "--cached", "--others", "--exclude-standard").Output()
	if err != nil {
		t.Skipf("listing tracked files (not a git checkout?): %v", err)
	}
	var files []modzip.File
	for _, rel := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Lstat(abs)
		if err != nil || !info.Mode().IsRegular() {
			continue // deleted in the working tree, or a symlink
		}
		files = append(files, zipFile{rel: rel, abs: abs})
	}
	dir := filepath.Join(proxy, "github.com", "nomi-language", "nomi", "@v")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var zipBuf bytes.Buffer
	if err := modzip.Create(&zipBuf, module.Version{Path: "github.com/nomi-language/nomi", Version: version}, files); err != nil {
		t.Fatalf("creating the module zip: %v", err)
	}
	goMod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"list":            []byte(version + "\n"),
		version + ".info": []byte(`{"Version":"` + version + `","Time":"2026-01-01T00:00:00Z"}`),
		version + ".mod":  goMod,
		version + ".zip":  zipBuf.Bytes(),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

type zipFile struct{ rel, abs string }

func (f zipFile) Path() string                 { return f.rel }
func (f zipFile) Lstat() (os.FileInfo, error)  { return os.Lstat(f.abs) }
func (f zipFile) Open() (io.ReadCloser, error) { return os.Open(f.abs) }
