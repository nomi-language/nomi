package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"golang.org/x/mod/module"
	modzip "golang.org/x/mod/zip"
)

// A pure-Nomi dependency is fetched through the module proxy with no Go
// toolchain: `nomi add` requires and downloads it, `nomi run` and `nomi
// check` use it by its short name, `--offline` refuses a cold module cache by
// naming the module, and `nomi fetch` fills the cache so `--offline` works.
// The proxy is a directory and PATH holds no `go`.
func TestModules_APureNomiDependencyNeedsNoGo(t *testing.T) {
	tmp := t.TempDir()
	proxy := filepath.Join(tmp, "proxy")
	publishFiles(t, proxy, "example.com/strings2", "v1.2.0", map[string]string{
		"go.mod":        "module example.com/strings2\n\ngo 1.22\n",
		"nomi.toml":     "[module]\nname = \"strings2\"\n",
		"strings2.nomi": "import strings2/twice\n\npub fn shout(s: String) -> String {\n    twice.twice(String.to_upper(s))\n}\n",
		"twice.nomi":    "pub fn twice(s: String) -> String {\n    \"${s}${s}\"\n}\n",
	})
	app := filepath.Join(tmp, "app")
	mustWrite(t, filepath.Join(app, "go.mod"), "module example.com/app\n\ngo 1.27.0\n")
	mustWrite(t, filepath.Join(app, "nomi.toml"), "[module]\nname = \"app\"\nentry_points = [\"main\"]\n")
	mustWrite(t, filepath.Join(app, "main.nomi"), "import {\n    std/io\n    strings2\n}\n\nfn main() {\n    io.print(strings2.shout(\"hey\"))\n}\n")

	emptyPath := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(emptyPath, 0o755); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(tmp, "modcache")
	env := []string{
		"HOME=" + filepath.Join(tmp, "home"),
		"PATH=" + emptyPath,
		"NOMI_COLOR=never",
		"NOMI_FFIRUN_CACHE_ROOT=" + filepath.Join(tmp, "nomi-cache"),
		"NOMI_TOOLCHAIN_ROOT=" + filepath.Join(tmp, "toolchains"),
		"GOPROXY=file://" + filepath.ToSlash(proxy),
		"GOSUMDB=off",
		"GOMODCACHE=" + cache,
		"GOFLAGS=-modcacherw",
	}
	if p := os.Getenv("NOMI_STD_PATH"); p != "" {
		env = append(env, "NOMI_STD_PATH="+p)
	}
	nomi := func(dir string, args ...string) (string, string, error) {
		t.Helper()
		cmd := exec.Command(nomiBin, args...)
		cmd.Dir = dir
		cmd.Env = env
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		return strings.TrimSpace(stdout.String()), stderr.String(), err
	}
	must := func(args ...string) string {
		t.Helper()
		out, stderr, err := nomi(app, args...)
		if err != nil {
			t.Fatalf("nomi %s: %v\n%s\n%s", strings.Join(args, " "), err, out, stderr)
		}
		return out
	}

	// Offline with nothing cached: refused, naming the module.
	_, stderr, err := nomi(app, "add", "--offline", "example.com/strings2@v1.2.0")
	if err == nil || !strings.Contains(stderr, "example.com/strings2") || !strings.Contains(stderr, "--offline") {
		t.Fatalf("nomi add --offline with a cold cache: %v\n%s", err, stderr)
	}

	if out := must("add", "example.com/strings2"); out != "added example.com/strings2 v1.2.0 (module strings2; import strings2)" {
		t.Fatalf("nomi add printed %q", out)
	}
	if out := must("run", "main.nomi"); out != "HEYHEY" {
		t.Fatalf("nomi run printed %q, want HEYHEY", out)
	}
	if out := must("check", "--offline", "main.nomi"); !strings.Contains(out, "ok") {
		t.Fatalf("nomi check --offline printed %q", out)
	}

	// Another machine: the project as committed, an empty module cache.
	for i, e := range env {
		if strings.HasPrefix(e, "GOMODCACHE=") {
			env[i] = "GOMODCACHE=" + filepath.Join(tmp, "modcache2")
		}
	}
	_, stderr, err = nomi(app, "run", "--offline", "main.nomi")
	if err == nil {
		t.Fatal("nomi run --offline with a cold module cache succeeded")
	}
	for _, want := range []string{"module example.com/strings2 v1.2.0", "--offline"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("nomi run --offline with a cold cache does not say %q:\n%s", want, stderr)
		}
	}
	if out := must("fetch"); !strings.Contains(out, "1 module in the module cache") {
		t.Fatalf("nomi fetch printed %q", out)
	}
	if out := must("run", "--offline", "main.nomi"); out != "HEYHEY" {
		t.Fatalf("nomi run --offline after nomi fetch printed %q", out)
	}
	// A program argument spelled --offline is the program's.
	if out := must("run", "main.nomi", "--offline"); out != "HEYHEY" {
		t.Fatalf("nomi run main.nomi --offline printed %q", out)
	}

	// No go.mod: nomi add says what to create.
	bare := filepath.Join(tmp, "bare")
	mustWrite(t, filepath.Join(bare, "main.nomi"), "fn main() {\n}\n")
	_, stderr, err = nomi(bare, "add", "example.com/strings2")
	if err == nil || !strings.Contains(stderr, "no go.mod") {
		t.Fatalf("nomi add with no go.mod: %v\n%s", err, stderr)
	}
}

// publishFiles writes path@version, holding files, to the file module proxy
// at proxy.
func publishFiles(t *testing.T, proxy, path, version string, files map[string]string) {
	t.Helper()
	enc, err := module.EscapePath(path)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(proxy, filepath.FromSlash(enc), "@v")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	var zf []modzip.File
	for _, name := range names {
		zf = append(zf, memZipFile{name: name, data: []byte(files[name])})
	}
	var zipBuf bytes.Buffer
	if err := modzip.Create(&zipBuf, module.Version{Path: path, Version: version}, zf); err != nil {
		t.Fatal(err)
	}
	list, _ := os.ReadFile(filepath.Join(dir, "list"))
	for name, data := range map[string][]byte{
		"list":            append(list, []byte(version+"\n")...),
		version + ".info": []byte(`{"Version":"` + version + `","Time":"2026-01-01T00:00:00Z"}`),
		version + ".mod":  []byte(files["go.mod"]),
		version + ".zip":  zipBuf.Bytes(),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

type memZipFile struct {
	name string
	data []byte
}

func (f memZipFile) Path() string                 { return f.name }
func (f memZipFile) Lstat() (os.FileInfo, error)  { return memZipInfo(f), nil }
func (f memZipFile) Open() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(f.data)), nil }

type memZipInfo memZipFile

func (i memZipInfo) Name() string       { return filepath.Base(i.name) }
func (i memZipInfo) Size() int64        { return int64(len(i.data)) }
func (i memZipInfo) Mode() os.FileMode  { return 0o644 }
func (i memZipInfo) ModTime() time.Time { return time.Time{} }
func (i memZipInfo) IsDir() bool        { return false }
func (i memZipInfo) Sys() any           { return nil }
