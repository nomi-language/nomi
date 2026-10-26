package modfetch

import (
	"bytes"
	"crypto/rand"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"golang.org/x/mod/module"
	"golang.org/x/mod/sumdb"
	"golang.org/x/mod/sumdb/dirhash"
	"golang.org/x/mod/sumdb/note"
	modzip "golang.org/x/mod/zip"

	"github.com/nomi-language/nomi/internal/fileuri"
	"github.com/nomi-language/nomi/internal/modcache"
)

// testProxy is a GOPROXY on the file system that a test publishes modules
// to, so nothing here touches the network.
type testProxy struct {
	dir  string
	sums map[string]string // "path version[/go.mod]" -> h1 hash
}

func newTestProxy(t *testing.T) *testProxy {
	t.Helper()
	return &testProxy{dir: t.TempDir(), sums: map[string]string{}}
}

func (p *testProxy) url() string { return fileuri.FromPath(filepath.ToSlash(p.dir)) }

// publish writes path@version to the proxy with files (go.mod among them),
// as a proxy serves a pushed tag, and records its go.sum hashes.
func (p *testProxy) publish(t *testing.T, path, version string, files map[string]string) {
	t.Helper()
	enc, err := module.EscapePath(path)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(p.dir, filepath.FromSlash(enc), "@v")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var zf []modzip.File
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		zf = append(zf, memFile{name: name, data: []byte(files[name])})
	}
	var zipBuf bytes.Buffer
	m := module.Version{Path: path, Version: version}
	if err := modzip.Create(&zipBuf, m, zf); err != nil {
		t.Fatal(err)
	}
	write := func(name string, data []byte) {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(version+".zip", zipBuf.Bytes())
	write(version+".mod", []byte(files["go.mod"]))
	write(version+".info", []byte(`{"Version":"`+version+`","Time":"2026-01-01T00:00:00Z"}`))
	list, _ := os.ReadFile(filepath.Join(dir, "list"))
	write("list", append(list, []byte(version+"\n")...))

	zipPath := filepath.Join(dir, version+".zip")
	h, err := dirhash.HashZip(zipPath, dirhash.Hash1)
	if err != nil {
		t.Fatal(err)
	}
	p.sums[path+" "+version] = h
	modHash, err := dirhash.Hash1([]string{"go.mod"}, func(string) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(files["go.mod"])), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	p.sums[path+" "+version+"/go.mod"] = modHash
}

// tamper replaces path@version's zip on the proxy with other contents, as a
// compromised proxy would serve it.
func (p *testProxy) tamper(t *testing.T, path, version string, files map[string]string) {
	t.Helper()
	saved := p.sums
	p.sums = map[string]string{}
	enc, _ := module.EscapePath(path)
	dir := filepath.Join(p.dir, filepath.FromSlash(enc), "@v")
	list, _ := os.ReadFile(filepath.Join(dir, "list"))
	p.publish(t, path, version, files)
	_ = os.WriteFile(filepath.Join(dir, "list"), list, 0o644)
	p.sums = saved
}

type memFile struct {
	name string
	data []byte
}

func (f memFile) Path() string                 { return f.name }
func (f memFile) Lstat() (os.FileInfo, error)  { return memInfo(f), nil }
func (f memFile) Open() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(f.data)), nil }

type memInfo memFile

func (i memInfo) Name() string       { return filepath.Base(i.name) }
func (i memInfo) Size() int64        { return int64(len(i.data)) }
func (i memInfo) Mode() os.FileMode  { return 0o644 }
func (i memInfo) ModTime() time.Time { return time.Time{} }
func (i memInfo) IsDir() bool        { return false }
func (i memInfo) Sys() any           { return nil }

// setup gives the test a private module cache, the proxy, no checksum
// database unless the test sets one, and an app module with go.mod.
func setup(t *testing.T, p *testProxy) (app, cache string) {
	t.Helper()
	cache = filepath.Join(t.TempDir(), "modcache")
	t.Setenv("GOMODCACHE", cache)
	t.Setenv("GOPROXY", p.url())
	t.Setenv("GOSUMDB", "off")
	t.Setenv("GONOSUMDB", "")
	t.Setenv("GOPRIVATE", "")
	t.Setenv("GONOPROXY", "")
	// Writable, so t.TempDir can remove it.
	t.Setenv("GOFLAGS", "-modcacherw")
	app = t.TempDir()
	writeFile(t, filepath.Join(app, "go.mod"), "module example.com/app\n\ngo 1.27.0\n")
	writeFile(t, filepath.Join(app, "nomi.toml"), "[module]\nname = \"app\"\n")
	return app, cache
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// publishGreet publishes a Nomi module `greet` that requires a Go module,
// and that Go module.
func publishGreet(t *testing.T, p *testProxy, versions ...string) {
	t.Helper()
	p.publish(t, "example.com/strutil", "v1.0.0", map[string]string{
		"go.mod":     "module example.com/strutil\n\ngo 1.22\n",
		"strutil.go": "package strutil\n\nfunc Twice(s string) string { return s + s }\n",
	})
	for _, v := range versions {
		p.publish(t, "example.com/greet", v, map[string]string{
			"go.mod":     "module example.com/greet\n\ngo 1.22\n\nrequire example.com/strutil v1.0.0\n",
			"nomi.toml":  "[module]\nname = \"greet\"\n",
			"greet.nomi": "pub fn hello(name: String) -> String {\n    \"hello, ${name} (" + v + ")\"\n}\n",
		})
	}
}

func TestAdd_WritesGoModAndGoSum(t *testing.T) {
	p := newTestProxy(t)
	publishGreet(t, p, "v0.1.0", "v0.2.0")
	app, cache := setup(t, p)

	added, err := Add(app, "example.com/greet", Options{})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if added.Module.Version != "v0.2.0" {
		t.Fatalf("Add without a version took %s, want the latest release v0.2.0", added.Module.Version)
	}
	want := module.Version{Path: "example.com/greet", Version: "v0.2.0"}
	dir, _ := modcache.ExtractedDir(cache, want)
	if added.Dir != dir {
		t.Fatalf("Add answered dir %q, want %q", added.Dir, dir)
	}
	if _, err := os.Stat(filepath.Join(dir, "greet.nomi")); err != nil {
		t.Fatalf("the module is not extracted into the module cache: %v", err)
	}

	goMod := readFile(t, filepath.Join(app, "go.mod"))
	for _, line := range []string{
		"require example.com/greet v0.2.0\n",
		"require example.com/strutil v1.0.0 // indirect\n",
	} {
		if !strings.Contains(goMod, line) {
			t.Errorf("go.mod lacks %q:\n%s", line, goMod)
		}
	}
	goSum := readFile(t, filepath.Join(app, "go.sum"))
	wantSum := "" +
		"example.com/greet v0.2.0 " + p.sums["example.com/greet v0.2.0"] + "\n" +
		"example.com/greet v0.2.0/go.mod " + p.sums["example.com/greet v0.2.0/go.mod"] + "\n" +
		"example.com/strutil v1.0.0 " + p.sums["example.com/strutil v1.0.0"] + "\n" +
		"example.com/strutil v1.0.0/go.mod " + p.sums["example.com/strutil v1.0.0/go.mod"] + "\n"
	if goSum != wantSum {
		t.Errorf("go.sum is\n%s\nwant\n%s", goSum, wantSum)
	}

	// An explicit older version moves the requirement.
	if _, err := Add(app, "example.com/greet@v0.1.0", Options{}); err != nil {
		t.Fatalf("Add @v0.1.0: %v", err)
	}
	if goMod := readFile(t, filepath.Join(app, "go.mod")); !strings.Contains(goMod, "require example.com/greet v0.1.0\n") {
		t.Errorf("go.mod does not require v0.1.0 after `add @v0.1.0`:\n%s", goMod)
	}

	// The cache-only reader the front end uses finds it, as a Nomi module.
	g, err := modcache.Walk(app, func(m module.Version) (string, error) { return modcache.Cached(cache, m), nil })
	if err != nil {
		t.Fatal(err)
	}
	d, ok := g.Find("example.com/greet")
	if !ok || !d.Nomi || d.Dir == "" || len(g.Missing) != 0 {
		t.Fatalf("modcache.Walk after Add: %+v (missing %v)", g.Deps, g.Missing)
	}
}

func TestFetch_ATamperedZipFailsVerification(t *testing.T) {
	p := newTestProxy(t)
	publishGreet(t, p, "v0.1.0")
	app, cache := setup(t, p)
	if _, err := Add(app, "example.com/greet@v0.1.0", Options{}); err != nil {
		t.Fatal(err)
	}
	// A fresh machine: the project with its go.sum, an empty cache, and a
	// proxy that now serves other bytes under the same version.
	t.Setenv("GOMODCACHE", filepath.Join(t.TempDir(), "fresh"))
	p.tamper(t, "example.com/greet", "v0.1.0", map[string]string{
		"go.mod":     "module example.com/greet\n\ngo 1.22\n\nrequire example.com/strutil v1.0.0\n",
		"nomi.toml":  "[module]\nname = \"greet\"\n",
		"greet.nomi": "pub fn hello(name: String) -> String {\n    \"pwned\"\n}\n",
	})
	_, err := Fetch(app, Options{})
	if err == nil {
		t.Fatal("Fetch accepted a module zip that does not match go.sum")
	}
	for _, want := range []string{"example.com/greet@v0.1.0", "checksum mismatch", "SECURITY ERROR", "go.sum"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not say %q:\n%v", want, err)
		}
	}
	dir, _ := modcache.ExtractedDir(os.Getenv("GOMODCACHE"), module.Version{Path: "example.com/greet", Version: "v0.1.0"})
	if _, statErr := os.Stat(dir); statErr == nil {
		t.Errorf("the tampered module was extracted into the cache at %s", dir)
	}
	_ = cache
}

func TestFetch_RefusesAModuleGoSumDoesNotListUnlessItMayUpdate(t *testing.T) {
	p := newTestProxy(t)
	publishGreet(t, p, "v0.1.0")
	app, _ := setup(t, p)
	writeFile(t, filepath.Join(app, "go.mod"), "module example.com/app\n\ngo 1.27.0\n\nrequire example.com/greet v0.1.0\n")

	_, err := Fetch(app, Options{})
	if err == nil || !strings.Contains(err.Error(), "missing go.sum entry for module example.com/greet v0.1.0") ||
		!strings.Contains(err.Error(), "nomi fetch") {
		t.Fatalf("a read-only fetch of a module go.sum does not list: %v", err)
	}
	if _, err := os.Stat(filepath.Join(app, "go.sum")); err == nil {
		t.Fatal("a read-only fetch wrote go.sum")
	}

	t.Setenv("GOFLAGS", "-modcacherw -mod=mod")
	if _, err := Fetch(app, Options{}); err != nil {
		t.Fatalf("GOFLAGS=-mod=mod: %v", err)
	}
	if goSum := readFile(t, filepath.Join(app, "go.sum")); !strings.Contains(goSum, "example.com/greet v0.1.0 "+p.sums["example.com/greet v0.1.0"]) {
		t.Fatalf("GOFLAGS=-mod=mod did not record the module in go.sum:\n%s", goSum)
	}
}

func TestFetch_Offline(t *testing.T) {
	p := newTestProxy(t)
	publishGreet(t, p, "v0.1.0")
	app, _ := setup(t, p)
	if _, err := Add(app, "example.com/greet@v0.1.0", Options{}); err != nil {
		t.Fatal(err)
	}

	// Cold cache: refused, naming the module.
	warm := os.Getenv("GOMODCACHE")
	t.Setenv("GOMODCACHE", filepath.Join(t.TempDir(), "cold"))
	_, err := Fetch(app, Options{Offline: true})
	if err == nil {
		t.Fatal("an offline fetch into an empty cache succeeded")
	}
	if !strings.Contains(err.Error(), "module example.com/greet v0.1.0") || !strings.Contains(err.Error(), "--offline") {
		t.Fatalf("the offline error does not name the module and --offline: %v", err)
	}

	// Warm cache: nothing to download, so offline works, and the proxy is
	// never read (it is gone).
	t.Setenv("GOMODCACHE", warm)
	t.Setenv("GOPROXY", "file:///nonexistent/proxy")
	g, err := Fetch(app, Options{Offline: true})
	if err != nil {
		t.Fatalf("an offline fetch with everything cached: %v", err)
	}
	if d, ok := g.Find("example.com/greet"); !ok || !d.Nomi {
		t.Fatalf("the offline fetch does not answer the cached module: %+v", g.Deps)
	}
}

// fakeSumDB serves a checksum database over HTTP whose records come from
// lines (path, version -> its go.sum lines), signed with a key made for the
// test, and answers the GOSUMDB value naming it.
func fakeSumDB(t *testing.T, lines func(path, vers string) string) string {
	t.Helper()
	skey, vkey, err := note.GenerateKey(rand.Reader, "sum.example.test")
	if err != nil {
		t.Fatal(err)
	}
	ts := sumdb.NewTestServer(skey, func(path, vers string) ([]byte, error) {
		return []byte(lines(path, vers)), nil
	})
	srv := httptest.NewServer(sumdb.NewServer(ts))
	t.Cleanup(srv.Close)
	return vkey + " " + srv.URL
}

func TestAdd_ChecksAModuleGoSumDoesNotListAgainstTheChecksumDatabase(t *testing.T) {
	p := newTestProxy(t)
	publishGreet(t, p, "v0.1.0")
	honest := func(path, vers string) string {
		return path + " " + vers + " " + p.sums[path+" "+vers] + "\n" +
			path + " " + vers + "/go.mod " + p.sums[path+" "+vers+"/go.mod"] + "\n"
	}

	t.Run("honest", func(t *testing.T) {
		app, _ := setup(t, p)
		t.Setenv("GOSUMDB", fakeSumDB(t, honest))
		if _, err := Add(app, "example.com/greet@v0.1.0", Options{}); err != nil {
			t.Fatalf("Add against an honest checksum database: %v", err)
		}
	})

	lying := func(path, vers string) string {
		if path == "example.com/greet" {
			return path + " " + vers + " h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n" +
				path + " " + vers + "/go.mod " + p.sums[path+" "+vers+"/go.mod"] + "\n"
		}
		return honest(path, vers)
	}
	t.Run("the proxy and the database disagree", func(t *testing.T) {
		app, _ := setup(t, p)
		t.Setenv("GOSUMDB", fakeSumDB(t, lying))
		_, err := Add(app, "example.com/greet@v0.1.0", Options{})
		if err == nil || !strings.Contains(err.Error(), "SECURITY ERROR") || !strings.Contains(err.Error(), "checksum database") {
			t.Fatalf("Add accepted a zip the checksum database disagrees with: %v", err)
		}
		if goSum, _ := os.ReadFile(filepath.Join(app, "go.sum")); strings.Contains(string(goSum), "example.com/greet v0.1.0 h1:") {
			t.Fatalf("the refused module's checksum was written to go.sum:\n%s", goSum)
		}
	})

	t.Run("GONOSUMDB exempts it", func(t *testing.T) {
		app, _ := setup(t, p)
		t.Setenv("GOSUMDB", fakeSumDB(t, lying))
		t.Setenv("GONOSUMDB", "example.com/greet")
		if _, err := Add(app, "example.com/greet@v0.1.0", Options{}); err != nil {
			t.Fatalf("Add with GONOSUMDB naming the module: %v", err)
		}
	})
}

func TestParseGOPROXY(t *testing.T) {
	got := parseGOPROXY("https://a.example,https://b.example|direct")
	want := []proxyEntry{{url: "https://a.example"}, {url: "https://b.example", fallAny: true}, {url: "direct"}}
	if len(got) != len(want) {
		t.Fatalf("parseGOPROXY = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("parseGOPROXY = %+v, want %+v", got, want)
		}
	}
}

func TestFetch_RefusesTheRealModuleCacheUnderGoTest(t *testing.T) {
	p := newTestProxy(t)
	publishGreet(t, p, "v0.1.0")
	app, _ := setup(t, p)
	t.Setenv("GOMODCACHE", "")
	t.Setenv("GOPATH", t.TempDir())
	_, err := Add(app, "example.com/greet@v0.1.0", Options{})
	if err == nil || !strings.Contains(err.Error(), "GOMODCACHE is unset under `go test`") {
		t.Fatalf("a download with no GOMODCACHE under go test: %v", err)
	}
}
