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

// publish writes path@version to the proxy with files, as a proxy serves a
// pushed tag, and records its hashes: the zip's, and the go.mod's, which
// the proxy synthesizes as a `module` line when files have none.
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
	goMod := files["go.mod"]
	if goMod == "" {
		goMod = "module " + path + "\n"
	}
	write(version+".zip", zipBuf.Bytes())
	write(version+".mod", []byte(goMod))
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
		return io.NopCloser(strings.NewReader(goMod)), nil
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
// database unless the test sets one, and an app module with a nomi.toml and
// no go.mod.
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
	writeFile(t, filepath.Join(app, "nomi.toml"), "[module]\nname = \"app\"\npath = \"example.com/app\"\n")
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

// publishGreet publishes a Nomi module `greet` at each of versions, which
// names a Nomi module `strutil`, and strutil.
func publishGreet(t *testing.T, p *testProxy, versions ...string) {
	t.Helper()
	p.publish(t, "example.com/strutil", "v1.0.0", map[string]string{
		"nomi.toml":    "[module]\nname = \"strutil\"\npath = \"example.com/strutil\"\n",
		"strutil.nomi": "pub fn twice(s: String) -> String\n    s + s\nend\n",
	})
	for _, v := range versions {
		p.publish(t, "example.com/greet", v, greetFiles(v, "hello, ${name} ("+v+")"))
	}
}

// greetFiles is greet's files at version v, saying says.
func greetFiles(v, says string) map[string]string {
	return map[string]string{
		"nomi.toml": "[module]\nname = \"greet\"\npath = \"example.com/greet\"\n\n[dependencies]\n" +
			"strutil = { module = \"example.com/strutil\", version = \"v1.0.0\" }\n",
		"greet.nomi": "pub fn hello(name: String) -> String\n    \"" + says + "\"\nend\n",
	}
}

func TestAdd_WritesTheEntryAndTheLock(t *testing.T) {
	p := newTestProxy(t)
	publishGreet(t, p, "v0.1.0", "v0.2.0")
	app, cache := setup(t, p)

	added, err := Add(app, "", "example.com/greet", Options{})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if added.Name != "greet" || added.Module.Version != "v0.2.0" {
		t.Fatalf("Add without a version or a name answered %+v, want greet at the latest release v0.2.0", added)
	}
	want := module.Version{Path: "example.com/greet", Version: "v0.2.0"}
	dir, _ := modcache.ExtractedDir(cache, want)
	if added.Dir != dir {
		t.Fatalf("Add answered dir %q, want %q", added.Dir, dir)
	}
	if _, err := os.Stat(filepath.Join(dir, "greet.nomi")); err != nil {
		t.Fatalf("the module is not extracted into the module cache: %v", err)
	}

	manifest := readFile(t, filepath.Join(app, "nomi.toml"))
	if want := "[dependencies]\ngreet = { module = \"example.com/greet\", version = \"v0.2.0\" }\n"; !strings.HasSuffix(manifest, want) {
		t.Errorf("nomi.toml is\n%s\nwant it to end with\n%s", manifest, want)
	}
	lock := readFile(t, filepath.Join(app, "nomi.lock"))
	wantLock := lockHeader() +
		"example.com/greet v0.2.0 " + p.sums["example.com/greet v0.2.0"] + " direct\n" +
		"example.com/strutil v1.0.0 " + p.sums["example.com/strutil v1.0.0"] + "\n"
	if lock != wantLock {
		t.Errorf("nomi.lock is\n%s\nwant\n%s", lock, wantLock)
	}
	if got, want := readFile(t, filepath.Join(app, "go.mod")), string(GoModText("example.com/app")); got != want {
		t.Errorf("Add wrote go.mod\n%s\nwant the module line alone\n%s", got, want)
	}

	// An explicit older version moves the entry, under its name.
	if _, err := Add(app, "", "example.com/greet@v0.1.0", Options{}); err != nil {
		t.Fatalf("Add @v0.1.0: %v", err)
	}
	if manifest := readFile(t, filepath.Join(app, "nomi.toml")); !strings.Contains(manifest, "greet = { module = \"example.com/greet\", version = \"v0.1.0\" }") {
		t.Errorf("nomi.toml does not name v0.1.0 after `add @v0.1.0`:\n%s", manifest)
	}

	// The cache-only reader the front end uses finds it, as a Nomi module,
	// at the version nomi.lock locks.
	g, err := modcache.Walk(app, func(m module.Version) (string, error) { return modcache.Cached(cache, m), nil })
	if err != nil {
		t.Fatal(err)
	}
	d, ok := g.Find("example.com/greet")
	if !ok || !d.Nomi || d.Dir == "" || d.Version.Version != "v0.1.0" || d.Name != "greet" || len(g.Missing) != 0 {
		t.Fatalf("modcache.Walk after Add: %+v (missing %v)", g.Deps, g.Missing)
	}
}

// lockHeader is the comment nomi.lock starts with.
func lockHeader() string { return string((&modcache.Lock{}).Bytes()) }

// Add takes the name before `=`, and refuses a name another module has.
func TestAdd_UnderAName(t *testing.T) {
	p := newTestProxy(t)
	publishGreet(t, p, "v0.1.0")
	app, _ := setup(t, p)
	added, err := Add(app, "hi", "example.com/greet@v0.1.0", Options{})
	if err != nil || added.Name != "hi" {
		t.Fatalf("Add under a name: %+v, %v", added, err)
	}
	if manifest := readFile(t, filepath.Join(app, "nomi.toml")); !strings.Contains(manifest, "hi = { module = \"example.com/greet\", version = \"v0.1.0\" }") {
		t.Errorf("nomi.toml after adding greet as hi:\n%s", manifest)
	}
	_, err = Add(app, "other", "example.com/greet@v0.1.0", Options{})
	if err == nil || err.Error() != "nomi.toml already names example.com/greet hi" {
		t.Errorf("Add of a module the table names under another name: %v", err)
	}
}

func TestFetch_ATamperedZipFailsVerification(t *testing.T) {
	p := newTestProxy(t)
	publishGreet(t, p, "v0.1.0")
	app, cache := setup(t, p)
	if _, err := Add(app, "", "example.com/greet@v0.1.0", Options{}); err != nil {
		t.Fatal(err)
	}
	// A fresh machine: the project with its nomi.lock, an empty cache, and
	// a proxy that now serves other bytes under the same version.
	t.Setenv("GOMODCACHE", filepath.Join(t.TempDir(), "fresh"))
	p.tamper(t, "example.com/greet", "v0.1.0", greetFiles("v0.1.0", "pwned"))
	_, err := Fetch(app, Options{})
	if err == nil {
		t.Fatal("Fetch accepted a module zip that does not match nomi.lock")
	}
	for _, want := range []string{"example.com/greet@v0.1.0", "checksum mismatch", "SECURITY ERROR", "nomi.lock"} {
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

// A nomi.lock line whose hash someone changed fails as a tampered download
// does, and so does one whose version someone changed: the version it
// names is downloaded and its zip has another hash.
func TestFetch_ATamperedLockLineFailsVerification(t *testing.T) {
	p := newTestProxy(t)
	publishGreet(t, p, "v0.1.0", "v0.2.0")
	app, _ := setup(t, p)
	if _, err := Add(app, "", "example.com/greet@v0.1.0", Options{}); err != nil {
		t.Fatal(err)
	}
	locked := readFile(t, filepath.Join(app, "nomi.lock"))
	for _, tc := range []struct{ what, from, to string }{
		{"hash", p.sums["example.com/greet v0.1.0"], "h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="},
		{"version", "example.com/greet v0.1.0 ", "example.com/greet v0.2.0 "},
	} {
		t.Setenv("GOMODCACHE", filepath.Join(t.TempDir(), "fresh"))
		writeFile(t, filepath.Join(app, "nomi.lock"), strings.Replace(locked, tc.from, tc.to, 1))
		_, err := Fetch(app, Options{})
		if err == nil {
			t.Fatalf("Fetch accepted a nomi.lock whose %s was changed", tc.what)
		}
		for _, want := range []string{"checksum mismatch", "SECURITY ERROR", "nomi.lock"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("a changed %s: the error does not say %q:\n%v", tc.what, want, err)
			}
		}
	}
}

func TestFetch_RefusesAModuleTheLockDoesNotHoldUnlessItMayUpdate(t *testing.T) {
	p := newTestProxy(t)
	publishGreet(t, p, "v0.1.0")
	app, _ := setup(t, p)
	writeFile(t, filepath.Join(app, "nomi.toml"), "[module]\nname = \"app\"\n\n[dependencies]\n"+
		"greet = { module = \"example.com/greet\", version = \"v0.1.0\" }\n")

	_, err := Fetch(app, Options{})
	if err == nil || !strings.Contains(err.Error(), "nomi.lock has no entry for module example.com/greet v0.1.0") ||
		!strings.Contains(err.Error(), "nomi fetch") {
		t.Fatalf("a read-only fetch of a module nomi.lock does not hold: %v", err)
	}
	if _, err := os.Stat(filepath.Join(app, "nomi.lock")); err == nil {
		t.Fatal("a read-only fetch wrote nomi.lock")
	}

	t.Setenv("GOFLAGS", "-modcacherw -mod=mod")
	if _, err := Fetch(app, Options{}); err != nil {
		t.Fatalf("GOFLAGS=-mod=mod: %v", err)
	}
	if lock := readFile(t, filepath.Join(app, "nomi.lock")); !strings.Contains(lock, "example.com/greet v0.1.0 "+p.sums["example.com/greet v0.1.0"]+" direct\n") {
		t.Fatalf("GOFLAGS=-mod=mod did not lock the module:\n%s", lock)
	}
}

// A dependency's own requirement raises the version the project's
// requirement selects, and nomi.lock holds the selection: a plain fetch
// then reads only the locked versions.
func TestFetch_SelectsTheHighestRequiredVersion(t *testing.T) {
	p := newTestProxy(t)
	publishGreet(t, p, "v0.1.0")
	p.publish(t, "example.com/strutil", "v1.2.0", map[string]string{
		"nomi.toml":    "[module]\nname = \"strutil\"\npath = \"example.com/strutil\"\n",
		"strutil.nomi": "pub fn twice(s: String) -> String\n    s + s + s\nend\n",
	})
	p.publish(t, "example.com/loud", "v1.0.0", map[string]string{
		"nomi.toml": "[module]\nname = \"loud\"\npath = \"example.com/loud\"\n\n[dependencies]\n" +
			"text = { module = \"example.com/strutil\", version = \"v1.2.0\" }\n",
		"loud.nomi": "pub fn one() -> Int\n    1\nend\n",
	})
	app, _ := setup(t, p)
	for _, m := range []string{"example.com/greet@v0.1.0", "example.com/loud@v1.0.0"} {
		if _, err := Add(app, "", m, Options{}); err != nil {
			t.Fatal(err)
		}
	}
	lock := readFile(t, filepath.Join(app, "nomi.lock"))
	if !strings.Contains(lock, "example.com/strutil v1.2.0 ") || strings.Contains(lock, "example.com/strutil v1.0.0") {
		t.Fatalf("nomi.lock does not select strutil v1.2.0, which loud asks for:\n%s", lock)
	}
	g, err := Fetch(app, Options{Offline: true})
	if err != nil {
		t.Fatalf("a plain fetch of the locked build list: %v", err)
	}
	d, _ := g.Find("example.com/strutil")
	if d.Version.Version != "v1.2.0" || d.Name != "strutil" {
		t.Errorf("strutil is %+v, want v1.2.0 under greet's name for it", d)
	}
	if loud, _ := g.Find("example.com/loud"); loud.Names["text"] != "strutil" {
		t.Errorf("loud's names are %v, want text as strutil", loud.Names)
	}
}

func TestFetch_Offline(t *testing.T) {
	p := newTestProxy(t)
	publishGreet(t, p, "v0.1.0")
	app, _ := setup(t, p)
	if _, err := Add(app, "", "example.com/greet@v0.1.0", Options{}); err != nil {
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

func TestAdd_ChecksAModuleTheLockDoesNotHoldAgainstTheChecksumDatabase(t *testing.T) {
	p := newTestProxy(t)
	publishGreet(t, p, "v0.1.0")
	honest := func(path, vers string) string {
		return path + " " + vers + " " + p.sums[path+" "+vers] + "\n" +
			path + " " + vers + "/go.mod " + p.sums[path+" "+vers+"/go.mod"] + "\n"
	}

	t.Run("honest", func(t *testing.T) {
		app, _ := setup(t, p)
		t.Setenv("GOSUMDB", fakeSumDB(t, honest))
		if _, err := Add(app, "", "example.com/greet@v0.1.0", Options{}); err != nil {
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
		_, err := Add(app, "", "example.com/greet@v0.1.0", Options{})
		if err == nil || !strings.Contains(err.Error(), "SECURITY ERROR") || !strings.Contains(err.Error(), "checksum database") {
			t.Fatalf("Add accepted a zip the checksum database disagrees with: %v", err)
		}
		if lock, _ := os.ReadFile(filepath.Join(app, "nomi.lock")); strings.Contains(string(lock), "example.com/greet") {
			t.Fatalf("the refused module was written to nomi.lock:\n%s", lock)
		}
	})

	t.Run("GONOSUMDB exempts it", func(t *testing.T) {
		app, _ := setup(t, p)
		t.Setenv("GOSUMDB", fakeSumDB(t, lying))
		t.Setenv("GONOSUMDB", "example.com/greet")
		if _, err := Add(app, "", "example.com/greet@v0.1.0", Options{}); err != nil {
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
	_, err := Add(app, "", "example.com/greet@v0.1.0", Options{})
	if err == nil || !strings.Contains(err.Error(), "GOMODCACHE is unset under `go test`") {
		t.Fatalf("a download with no GOMODCACHE under go test: %v", err)
	}
}
