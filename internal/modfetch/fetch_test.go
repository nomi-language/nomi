package modfetch

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/modcache"
	"github.com/nomi-language/nomi/internal/modfetch/modfetchtest"
)

// serve starts a server of repositories and points the host table at it:
// example.com's repositories are read there with GitHub's archive form,
// and `nomi add` probes their paths. PATH holds no git, so nothing the
// test does reaches past the server.
func serve(t *testing.T) *modfetchtest.Server {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
	s := modfetchtest.New(t)
	useHosts(t, map[string]host{"example.com": {base: s.URL, archive: knownHosts["github.com"].archive}})
	return s
}

// useHosts replaces the host table for the test.
func useHosts(t *testing.T, hosts map[string]host) {
	saved := hostTable
	hostTable = hosts
	t.Cleanup(func() { hostTable = saved })
}

// setup gives the test a module cache of its own and an app module with a
// nomi.toml.
func setup(t *testing.T) (app, cache string) {
	t.Helper()
	cache = modfetchtest.Cache(t)
	app = t.TempDir()
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

// strutilFiles is example.com/strutil's files.
var strutilFiles = map[string]string{
	"nomi.toml":    "[module]\nname = \"strutil\"\n",
	"strutil.nomi": "pub fn twice(s: String) -> String\n    s + s\nend\n",
}

// greetFiles is example.com/greet's files at version v, saying says, which
// name strutil.
func greetFiles(v, says string) map[string]string {
	return map[string]string{
		"nomi.toml": "[module]\nname = \"greet\"\n\n[dependencies]\n" +
			"strutil = { repo = \"example.com/strutil\", version = \"v1.0.0\" }\n",
		"greet.nomi": "pub fn hello(name: String) -> String\n    \"" + says + "\"\nend\n",
	}
}

// publishGreet publishes example.com/greet at each of versions, and
// example.com/strutil at v1.0.0, and answers the commits by `repo version`.
func publishGreet(s *modfetchtest.Server, versions ...string) map[string]string {
	commits := map[string]string{"example.com/strutil v1.0.0": s.Tag("example.com/strutil", "v1.0.0", strutilFiles)}
	for _, v := range versions {
		commits["example.com/greet "+v] = s.Tag("example.com/greet", v, greetFiles(v, "hello, ${name} ("+v+")"))
	}
	return commits
}

// lockHeader is the comment nomi.lock starts with.
func lockHeader() string { return string((&modcache.Lock{}).Bytes()) }

func TestAdd_WritesTheEntryAndTheLock(t *testing.T) {
	s := serve(t)
	commits := publishGreet(s, "v0.1.0", "v0.2.0")
	app, cache := setup(t)

	added, err := Add(app, "", "example.com/greet", Options{})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if added.Name != "greet" || added.Module.Version != "v0.2.0" {
		t.Fatalf("Add without a version or a name answered %+v, want greet at the latest release v0.2.0", added)
	}
	dir := modcache.RepoDir(cache, "example.com/greet", "v0.2.0")
	if added.Dir != dir {
		t.Fatalf("Add answered dir %q, want %q", added.Dir, dir)
	}
	if _, err := os.Stat(filepath.Join(dir, "greet.nomi")); err != nil {
		t.Fatalf("the module is not extracted into the module cache: %v", err)
	}

	manifest := readFile(t, filepath.Join(app, "nomi.toml"))
	if want := "[dependencies]\ngreet = { repo = \"example.com/greet\", version = \"v0.2.0\" }\n"; !strings.HasSuffix(manifest, want) {
		t.Errorf("nomi.toml is\n%s\nwant it to end with\n%s", manifest, want)
	}
	lock := readFile(t, filepath.Join(app, "nomi.lock"))
	wantLock := lockHeader() +
		"example.com/greet . v0.2.0 " + commits["example.com/greet v0.2.0"] + " " + modfetchtest.Hash(greetFiles("v0.2.0", "hello, ${name} (v0.2.0)"), "") + " direct\n" +
		"example.com/strutil . v1.0.0 " + commits["example.com/strutil v1.0.0"] + " " + modfetchtest.Hash(strutilFiles, "") + "\n"
	if lock != wantLock {
		t.Errorf("nomi.lock is\n%s\nwant\n%s", lock, wantLock)
	}
	if _, err := os.Stat(filepath.Join(app, "go.mod")); err == nil {
		t.Error("Add wrote a go.mod")
	}

	// An explicit older version moves the entry, under its name.
	if _, err := Add(app, "", "example.com/greet@v0.1.0", Options{}); err != nil {
		t.Fatalf("Add @v0.1.0: %v", err)
	}
	if manifest := readFile(t, filepath.Join(app, "nomi.toml")); !strings.Contains(manifest, "greet = { repo = \"example.com/greet\", version = \"v0.1.0\" }") {
		t.Errorf("nomi.toml does not name v0.1.0 after `add @v0.1.0`:\n%s", manifest)
	}

	// The cache-only reader the front end uses finds it, as a Nomi module,
	// at the version nomi.lock locks.
	g, err := modcache.Walk(app, modcache.CacheOnly)
	if err != nil {
		t.Fatal(err)
	}
	d, ok := g.Find(modcache.ID{Repo: "example.com/greet"})
	if !ok || !d.Nomi || d.Dir == "" || d.Version != "v0.1.0" || d.Name != "greet" || len(g.Missing) != 0 {
		t.Fatalf("modcache.Walk after Add: %+v (missing %v)", g.Deps, g.Missing)
	}
}

// Add takes the name before `=`, and refuses a name another module has.
func TestAdd_UnderAName(t *testing.T) {
	s := serve(t)
	publishGreet(s, "v0.1.0")
	app, _ := setup(t)
	added, err := Add(app, "hi", "example.com/greet@v0.1.0", Options{})
	if err != nil || added.Name != "hi" {
		t.Fatalf("Add under a name: %+v, %v", added, err)
	}
	if manifest := readFile(t, filepath.Join(app, "nomi.toml")); !strings.Contains(manifest, "hi = { repo = \"example.com/greet\", version = \"v0.1.0\" }") {
		t.Errorf("nomi.toml after adding greet as hi:\n%s", manifest)
	}
	_, err = Add(app, "other", "example.com/greet@v0.1.0", Options{})
	if err == nil || err.Error() != "nomi.toml already names example.com/greet hi" {
		t.Errorf("Add of a module the table names under another name: %v", err)
	}
}

// monoFiles is a repository of modules in directories: lib/a names its
// neighbor lib/b by its directory, and the root is a module too.
func monoFiles(v string) map[string]string {
	return map[string]string{
		"nomi.toml":       "[module]\nname = \"mono\"\n",
		"mono.nomi":       "pub fn v() -> String\n    \"" + v + "\"\nend\n",
		"lib/a/nomi.toml": "[module]\nname = \"a\"\n\n[dependencies]\nb = { dir = \"../b\" }\ntop = { dir = \"../..\" }\n",
		"lib/a/a.nomi":    "pub fn one() -> Int\n    1\nend\n",
		"lib/b/nomi.toml": "[module]\nname = \"b\"\n",
		"lib/b/b.nomi":    "pub fn two() -> Int\n    2\nend\n",
		"README.md":       "not a module file of lib/a\n",
	}
}

// A path inside a repository names the module in that directory: Add
// finds the repository by asking for the tags of shorter and shorter
// prefixes, writes `repo` and `subdir`, and the module's dir entries name
// its neighbors in the repository at the same tag, all from one archive.
func TestAdd_ASubdirectoryOfARepository(t *testing.T) {
	s := serve(t)
	commit := s.Tag("example.com/mono", "v1.0.0", monoFiles("v1.0.0"))
	app, cache := setup(t)
	added, err := Add(app, "", "example.com/mono/lib/a", Options{})
	if err != nil {
		t.Fatalf("Add of a subdirectory: %v", err)
	}
	if added.Module != (modcache.Module{ID: modcache.ID{Repo: "example.com/mono", Subdir: "lib/a"}, Version: "v1.0.0"}) {
		t.Fatalf("Add answered %+v", added.Module)
	}
	if manifest := readFile(t, filepath.Join(app, "nomi.toml")); !strings.HasSuffix(manifest, "a = { repo = \"example.com/mono\", subdir = \"lib/a\", version = \"v1.0.0\" }\n") {
		t.Errorf("nomi.toml after adding a subdirectory:\n%s", manifest)
	}
	files := monoFiles("v1.0.0")
	wantLock := lockHeader() +
		"example.com/mono . v1.0.0 " + commit + " " + modfetchtest.Hash(files, "") + "\n" +
		"example.com/mono lib/a v1.0.0 " + commit + " " + modfetchtest.Hash(files, "lib/a") + " direct\n" +
		"example.com/mono lib/b v1.0.0 " + commit + " " + modfetchtest.Hash(files, "lib/b") + "\n"
	if lock := readFile(t, filepath.Join(app, "nomi.lock")); lock != wantLock {
		t.Errorf("nomi.lock is\n%s\nwant\n%s", lock, wantLock)
	}
	entries, err := os.ReadDir(filepath.Join(cache, "example.com"))
	if err != nil || len(entries) != 2 || entries[0].Name() != "mono@v1.0.0" || entries[1].Name() != "mono@v1.0.0+files" {
		t.Errorf("the cache holds %v (%v), want the repository once, with its list of files", entries, err)
	}
	g, err := Fetch(app, Options{Offline: true})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := g.Find(modcache.ID{Repo: "example.com/mono", Subdir: "lib/a"})
	root, _ := g.Find(modcache.ID{Repo: "example.com/mono"})
	if a.Names["b"] != "b" || a.Names["top"] != "top" || root.Name != "top" || root.Version != "v1.0.0" {
		t.Errorf("lib/a's names are %v and the root is %+v, want its neighbors b and the root at v1.0.0", a.Names, root)
	}
}

// A fetched module's dir entry that leaves its repository is an error.
func TestFetch_AFetchedModulesDirOutsideItsRepositoryIsAnError(t *testing.T) {
	s := serve(t)
	s.Tag("example.com/leaky", "v1.0.0", map[string]string{
		"nomi.toml": "[module]\nname = \"leaky\"\n\n[dependencies]\nhome = { dir = \"../elsewhere\" }\n",
	})
	app, _ := setup(t)
	before := readFile(t, filepath.Join(app, "nomi.toml"))
	_, err := Add(app, "", "example.com/leaky", Options{})
	want := `example.com/leaky v1.0.0: nomi.toml: [dependencies] home: dir "../elsewhere" leaves the repository; a fetched module names a module of another repository by ` + "`repo` and `version`"
	if err == nil || err.Error() != want {
		t.Fatalf("Add of a module whose dir leaves its repository: %v\nwant %s", err, want)
	}
	if after := readFile(t, filepath.Join(app, "nomi.toml")); after != before {
		t.Errorf("a failed Add left nomi.toml changed:\n%s", after)
	}
}

// One tag versions a whole repository: two modules of one repository asked
// for at two versions are both taken at the higher.
func TestFetch_OneTagVersionsTheWholeRepository(t *testing.T) {
	s := serve(t)
	s.Tag("example.com/mono", "v1.0.0", monoFiles("v1.0.0"))
	s.Tag("example.com/mono", "v1.1.0", monoFiles("v1.1.0"))
	app, _ := setup(t)
	writeFile(t, filepath.Join(app, "nomi.toml"), "[module]\nname = \"app\"\n\n[dependencies]\n"+
		"a = { repo = \"example.com/mono\", subdir = \"lib/a\", version = \"v1.0.0\" }\n"+
		"b = { repo = \"example.com/mono\", subdir = \"lib/b\", version = \"v1.1.0\" }\n")
	g, err := Fetch(app, Options{Update: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range g.Deps {
		if d.Version != "v1.1.0" {
			t.Errorf("%s is at %s, want every module of the repository at v1.1.0", d.ID, d.Version)
		}
	}
	if lock := readFile(t, filepath.Join(app, "nomi.lock")); strings.Contains(lock, "v1.0.0") {
		t.Errorf("nomi.lock holds the lower version:\n%s", lock)
	}
}

// Two requirements on one repository in different lines, two major
// versions or, below v1, two minor versions, are an error that names both
// requirers. Two in one 0.x line take the higher.
func TestFetch_TwoLinesOfOneRepositoryAreAnError(t *testing.T) {
	s := serve(t)
	for _, v := range []string{"v0.1.0", "v0.1.2", "v0.2.0", "v1.0.0", "v2.0.0"} {
		s.Tag("example.com/mono", v, monoFiles(v))
	}
	for _, tc := range []struct{ project, user, want string }{
		{"v1.0.0", "v2.0.0", "v2.0.0"},
		{"v0.1.0", "v0.2.0", "v0.2.0"},
		{"v0.2.0", "v1.0.0", "v1.0.0"},
		{"v0.1.0", "v0.1.2", ""},
	} {
		user := "example.com/user-" + strings.ReplaceAll(tc.user, ".", "-")
		s.Tag(user, "v1.0.0", map[string]string{
			"nomi.toml": "[module]\nname = \"user\"\n\n[dependencies]\nb = { repo = \"example.com/mono\", subdir = \"lib/b\", version = \"" + tc.user + "\" }\n",
		})
		app, _ := setup(t)
		writeFile(t, filepath.Join(app, "nomi.toml"), "[module]\nname = \"app\"\n\n[dependencies]\n"+
			"a = { repo = \"example.com/mono\", subdir = \"lib/a\", version = \""+tc.project+"\" }\n"+
			"user = { repo = \""+user+"\", version = \"v1.0.0\" }\n")
		g, err := Fetch(app, Options{Update: true})
		if tc.want == "" {
			if err != nil {
				t.Fatalf("%s and %s of one 0.x line: %v", tc.project, tc.user, err)
			}
			if d, _ := g.Find(modcache.ID{Repo: "example.com/mono", Subdir: "lib/a"}); d.Version != tc.user {
				t.Errorf("%s and %s of one 0.x line selected %s, want the higher", tc.project, tc.user, d.Version)
			}
			continue
		}
		want := "app requires example.com/mono " + tc.project + " and " + user + " v1.0.0 requires example.com/mono " + tc.want + ": " +
			"one tag versions a whole repository, so every requirement on it must be in one line, a major version from v1 on and a minor version below v1"
		if err == nil || err.Error() != want {
			t.Errorf("%s and %s of one repository: %v\nwant %s", tc.project, tc.user, err, want)
		}
	}
}

// Files a host serves under a locked tag that differ from the ones locked
// fail verification, and nothing is installed.
func TestFetch_ChangedFilesUnderALockedTagFailVerification(t *testing.T) {
	s := serve(t)
	publishGreet(s, "v0.1.0")
	app, _ := setup(t)
	if _, err := Add(app, "", "example.com/greet@v0.1.0", Options{}); err != nil {
		t.Fatal(err)
	}
	// A fresh machine: the project with its nomi.lock, an empty cache, and
	// a host that now serves other files under the same tag and commit.
	fresh := modfetchtest.Cache(t)
	s.Retag("example.com/greet", "v0.1.0", greetFiles("v0.1.0", "pwned"))
	_, err := Fetch(app, Options{})
	if err == nil {
		t.Fatal("Fetch accepted files that do not match nomi.lock")
	}
	for _, want := range []string{"example.com/greet v0.1.0", "checksum mismatch", "SECURITY ERROR", "nomi.lock", "Nothing was installed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not say %q:\n%v", want, err)
		}
	}
	if _, statErr := os.Stat(modcache.RepoDir(fresh, "example.com/greet", "v0.1.0")); statErr == nil {
		t.Error("the changed repository was installed into the cache")
	}
}

// A tag that names another commit than the one locked fails verification.
func TestFetch_AMovedTagFailsVerification(t *testing.T) {
	s := serve(t)
	publishGreet(s, "v0.1.0")
	app, _ := setup(t)
	if _, err := Add(app, "", "example.com/greet@v0.1.0", Options{}); err != nil {
		t.Fatal(err)
	}
	modfetchtest.Cache(t)
	s.Tag("example.com/greet", "v0.1.0", greetFiles("v0.1.0", "hello, ${name} (v0.1.0)"))
	_, err := Fetch(app, Options{})
	if err == nil || !strings.Contains(err.Error(), "tag v0.1.0 names commit") || !strings.Contains(err.Error(), "SECURITY ERROR\nThe tag has moved") {
		t.Fatalf("Fetch of a moved tag: %v", err)
	}
}

// A nomi.lock line whose hash someone changed fails as changed files do,
// and so does one whose version someone changed: the version it names is
// fetched and its files have another hash.
func TestFetch_ATamperedLockLineFailsVerification(t *testing.T) {
	s := serve(t)
	publishGreet(s, "v0.1.0", "v0.2.0")
	app, _ := setup(t)
	if _, err := Add(app, "", "example.com/greet@v0.1.0", Options{}); err != nil {
		t.Fatal(err)
	}
	locked := readFile(t, filepath.Join(app, "nomi.lock"))
	hash := modfetchtest.Hash(greetFiles("v0.1.0", "hello, ${name} (v0.1.0)"), "")
	for _, tc := range []struct{ what, from, to string }{
		{"hash", hash, "sha256:" + strings.Repeat("0", 64)},
		{"version", "example.com/greet . v0.1.0 ", "example.com/greet . v0.2.0 "},
	} {
		modfetchtest.Cache(t)
		writeFile(t, filepath.Join(app, "nomi.lock"), strings.Replace(locked, tc.from, tc.to, 1))
		_, err := Fetch(app, Options{})
		if err == nil {
			t.Fatalf("Fetch accepted a nomi.lock whose %s was changed", tc.what)
		}
		if !strings.Contains(err.Error(), "SECURITY ERROR") || !strings.Contains(err.Error(), "nomi.lock") {
			t.Errorf("a changed %s: the error is not a security error naming nomi.lock:\n%v", tc.what, err)
		}
	}
}

func TestFetch_RefusesAModuleTheLockDoesNotHoldUnlessItMayUpdate(t *testing.T) {
	s := serve(t)
	commits := publishGreet(s, "v0.1.0")
	app, cache := setup(t)
	writeFile(t, filepath.Join(app, "nomi.toml"), "[module]\nname = \"app\"\n\n[dependencies]\n"+
		"greet = { repo = \"example.com/greet\", version = \"v0.1.0\" }\n")

	_, err := Fetch(app, Options{})
	if err == nil || err.Error() != "nomi.lock has no entry for module example.com/greet v0.1.0; run `nomi fetch` to add it" {
		t.Fatalf("a read-only fetch of a module nomi.lock does not hold: %v", err)
	}
	if _, err := os.Stat(filepath.Join(app, "nomi.lock")); err == nil {
		t.Fatal("a read-only fetch wrote nomi.lock")
	}
	if _, err := os.Stat(modcache.RepoDir(cache, "example.com/greet", "v0.1.0")); err == nil {
		t.Fatal("a read-only fetch installed a module nomi.lock does not hold")
	}

	if _, err := Fetch(app, Options{Update: true}); err != nil {
		t.Fatalf("an updating fetch: %v", err)
	}
	want := "example.com/greet . v0.1.0 " + commits["example.com/greet v0.1.0"] + " " + modfetchtest.Hash(greetFiles("v0.1.0", "hello, ${name} (v0.1.0)"), "") + " direct\n"
	if lock := readFile(t, filepath.Join(app, "nomi.lock")); !strings.Contains(lock, want) {
		t.Fatalf("an updating fetch did not lock the module:\n%s", lock)
	}
}

// A dependency's own requirement raises the version the project's
// requirement selects, and nomi.lock holds the selection: a plain fetch
// then reads only the locked versions.
func TestFetch_SelectsTheHighestRequiredVersion(t *testing.T) {
	s := serve(t)
	publishGreet(s, "v0.1.0")
	s.Tag("example.com/strutil", "v1.2.0", map[string]string{
		"nomi.toml":    "[module]\nname = \"strutil\"\n",
		"strutil.nomi": "pub fn twice(s: String) -> String\n    s + s + s\nend\n",
	})
	s.Tag("example.com/loud", "v1.0.0", map[string]string{
		"nomi.toml": "[module]\nname = \"loud\"\n\n[dependencies]\n" +
			"text = { repo = \"example.com/strutil\", version = \"v1.2.0\" }\n",
		"loud.nomi": "pub fn one() -> Int\n    1\nend\n",
	})
	app, _ := setup(t)
	for _, m := range []string{"example.com/greet@v0.1.0", "example.com/loud@v1.0.0"} {
		if _, err := Add(app, "", m, Options{}); err != nil {
			t.Fatal(err)
		}
	}
	lock := readFile(t, filepath.Join(app, "nomi.lock"))
	if !strings.Contains(lock, "example.com/strutil . v1.2.0 ") || strings.Contains(lock, "example.com/strutil . v1.0.0") {
		t.Fatalf("nomi.lock does not select strutil v1.2.0, which loud asks for:\n%s", lock)
	}
	g, err := Fetch(app, Options{Offline: true})
	if err != nil {
		t.Fatalf("a plain fetch of the locked build list: %v", err)
	}
	d, _ := g.Find(modcache.ID{Repo: "example.com/strutil"})
	if d.Version != "v1.2.0" || d.Name != "strutil" {
		t.Errorf("strutil is %+v, want v1.2.0 under greet's name for it", d)
	}
	if loud, _ := g.Find(modcache.ID{Repo: "example.com/loud"}); loud.Names["text"] != "strutil" {
		t.Errorf("loud's names are %v, want text as strutil", loud.Names)
	}
}

// Offline, a cold cache is refused, naming the module. With the lock
// current and the modules cached, a fetch makes no request, offline or not.
func TestFetch_ACurrentLockAndACachedModuleMakeNoRequest(t *testing.T) {
	s := serve(t)
	publishGreet(s, "v0.1.0")
	app, warm := setup(t)
	if _, err := Add(app, "", "example.com/greet@v0.1.0", Options{}); err != nil {
		t.Fatal(err)
	}

	modfetchtest.Cache(t)
	_, err := Fetch(app, Options{Offline: true})
	if err == nil || err.Error() != "module example.com/greet v0.1.0 is not in the module cache, and --offline forbids fetching it" {
		t.Fatalf("an offline fetch into an empty cache: %v", err)
	}

	t.Setenv(modcache.CacheEnv, warm)
	before := s.Requests()
	for _, offline := range []bool{true, false} {
		g, err := Fetch(app, Options{Offline: offline})
		if err != nil {
			t.Fatalf("a fetch with everything cached (offline %v): %v", offline, err)
		}
		if d, ok := g.Find(modcache.ID{Repo: "example.com/greet"}); !ok || !d.Nomi {
			t.Fatalf("the fetch does not answer the cached module: %+v", g.Deps)
		}
	}
	if n := s.Requests() - before; n != 0 {
		t.Errorf("fetches with a current lock and a warm cache made %d requests, want none", n)
	}
}

func TestFetch_RefusesTheRealModuleCacheUnderGoTest(t *testing.T) {
	s := serve(t)
	publishGreet(s, "v0.1.0")
	app, _ := setup(t)
	t.Setenv(modcache.CacheEnv, "")
	_, err := Add(app, "", "example.com/greet@v0.1.0", Options{})
	if err == nil || !strings.Contains(err.Error(), "NOMI_MODULE_CACHE is unset under `go test`") {
		t.Fatalf("a fetch with no NOMI_MODULE_CACHE under go test: %v", err)
	}
}

// A module whose directory holds no nomi.toml is refused.
func TestAdd_ADirectoryWithNoNomiTomlIsNotAModule(t *testing.T) {
	s := serve(t)
	s.Tag("example.com/gokit", "v1.0.0", map[string]string{"go.mod": "module example.com/gokit\n", "kit.go": "package gokit\n"})
	app, _ := setup(t)
	_, err := Add(app, "", "example.com/gokit", Options{})
	if err == nil || err.Error() != "example.com/gokit v1.0.0 has no nomi.toml, so it is not a Nomi module" {
		t.Errorf("Add of a repository with no nomi.toml: %v", err)
	}
}

// linkFiles is a repository of two modules in directories, lib/a naming
// its neighbor lib/b by its directory, and no module at its root.
func linkFiles() map[string]string {
	return map[string]string{
		"README.md":       "a repository with links\n",
		"lib/a/nomi.toml": "[module]\nname = \"a\"\n\n[dependencies]\nb = { dir = \"../b\" }\n",
		"lib/a/a.nomi":    "pub fn one() -> Int\n    1\nend\n",
		"lib/b/nomi.toml": "[module]\nname = \"b\"\n",
		"lib/b/b.nomi":    "pub fn two() -> Int\n    2\nend\n",
	}
}

// A link elsewhere in a repository changes nothing for a module: it is not
// installed, the list of the repository's files records it, and the
// module's hash covers its regular files alone.
func TestFetch_ALinkOutsideTheModuleIsNotInstalled(t *testing.T) {
	s := serve(t)
	files := linkFiles()
	commit := s.TagWithLinks("example.com/links", "v1.0.0", files, map[string]string{"docs/latest": "../README.md"})
	app, cache := setup(t)
	if _, err := Add(app, "", "example.com/links/lib/b", Options{}); err != nil {
		t.Fatalf("Add of a module beside a link: %v", err)
	}
	wantLock := lockHeader() +
		"example.com/links lib/b v1.0.0 " + commit + " " + modfetchtest.Hash(files, "lib/b") + " direct\n"
	if lock := readFile(t, filepath.Join(app, "nomi.lock")); lock != wantLock {
		t.Errorf("nomi.lock is\n%s\nwant\n%s", lock, wantLock)
	}
	repo := modcache.RepoDir(cache, "example.com/links", "v1.0.0")
	if _, err := os.Lstat(filepath.Join(repo, "docs", "latest")); err == nil {
		t.Error("the link was installed")
	}
	_, listed, err := modcache.ReadRepoFiles(repo)
	if err != nil {
		t.Fatal(err)
	}
	links := 0
	for _, f := range listed {
		if f.Link {
			links++
			if f.Path != "docs/latest" || f.Sum != "" {
				t.Errorf("the list records the link as %+v", f)
			}
		}
	}
	if links != 1 || len(listed) != len(files)+1 {
		t.Errorf("the list records %d links among %d entries, want the one link among %d", links, len(listed), len(files)+1)
	}
	if _, err := Fetch(app, Options{Offline: true}); err != nil {
		t.Errorf("a fetch from the cache: %v", err)
	}
}

// A module whose own directory holds a link is an error when it is
// resolved, naming the link: as the module added, as a neighbor another
// module names by its directory, and as a repository's root module, whose
// directory is the whole repository. The repository is installed, since
// its other modules may hold no link, and nomi.toml is left as it was.
func TestFetch_ALinkInTheModulesDirectoryIsAnError(t *testing.T) {
	const why = ", a link: nomi installs only files and directories, so a module's directory may hold no link"
	for _, tc := range []struct {
		name, repo, add string
		files           map[string]string
		links           map[string]string
		want            string
	}{
		{"the module added", "example.com/links", "example.com/links/lib/b", linkFiles(),
			map[string]string{"lib/b/current": "b.nomi"},
			"example.com/links/lib/b v1.0.0 holds lib/b/current" + why},
		{"a neighbor", "example.com/links", "example.com/links/lib/a", linkFiles(),
			map[string]string{"lib/b/current": "b.nomi"},
			"example.com/links/lib/b v1.0.0 holds lib/b/current" + why},
		{"the root module", "example.com/linky", "example.com/linky",
			map[string]string{"nomi.toml": "[module]\nname = \"linky\"\n"},
			map[string]string{"docs/secret": "/etc/passwd"},
			"example.com/linky v1.0.0 holds docs/secret" + why},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := serve(t)
			s.TagWithLinks(tc.repo, "v1.0.0", tc.files, tc.links)
			app, cache := setup(t)
			_, err := Add(app, "", tc.add, Options{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Add of %s: %v, want an error holding %q", tc.add, err, tc.want)
			}
			if manifest := readFile(t, filepath.Join(app, "nomi.toml")); manifest != "[module]\nname = \"app\"\n" {
				t.Errorf("a refused Add left nomi.toml as\n%s", manifest)
			}
			if _, err := os.Stat(modcache.RepoDir(cache, tc.repo, "v1.0.0")); err != nil {
				t.Errorf("the repository was not installed: %v", err)
			}
		})
	}
}

// Extraction installs regular files and directories only: a link, symbolic
// or hard, is answered as a link and not installed; a path that escapes,
// and an archive with two top directories, are refused.
func TestExtract_RecordsLinksAndRefusesPathsThatEscape(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: "top/", Mode: 0o755})
	_ = tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: "top/a", Mode: 0o644, Size: 1})
	_, _ = tw.Write([]byte("x"))
	_ = tw.WriteHeader(&tar.Header{Typeflag: tar.TypeLink, Name: "top/hard", Linkname: "top/a"})
	_ = tw.WriteHeader(&tar.Header{Typeflag: tar.TypeSymlink, Name: "top/soft", Linkname: "/etc/passwd"})
	_ = tw.Close()
	dest := t.TempDir()
	files, err := extract(&buf, false, dest)
	if err != nil {
		t.Fatalf("extract of a file and two links: %v", err)
	}
	want := []modcache.RepoFile{
		{Path: "a", Sum: "2d711642b726b04401627ca9fbac32f5c8530fb1903cc4db02258717921a4881"},
		{Path: "hard", Link: true},
		{Path: "soft", Link: true},
	}
	if len(files) != len(want) || files[0] != want[0] || files[1] != want[1] || files[2] != want[2] {
		t.Errorf("extract answered %+v, want %+v", files, want)
	}
	for _, name := range []string{"hard", "soft"} {
		if _, err := os.Lstat(filepath.Join(dest, name)); err == nil {
			t.Errorf("extract installed the link %s", name)
		}
	}

	for _, tc := range []struct {
		names []string
		want  string
	}{
		{[]string{"top/", "top/../../escape"}, `the archive holds "top/../../escape", a path that escapes its directory`},
		{[]string{"top/a", "other/b"}, "the archive has two top directories, top and other"},
	} {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		for _, name := range tc.names {
			if strings.HasSuffix(name, "/") {
				_ = tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: name, Mode: 0o755})
				continue
			}
			_ = tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: 0o644, Size: 1})
			_, _ = tw.Write([]byte("x"))
		}
		_ = tw.Close()
		dest := t.TempDir()
		_, err := extract(&buf, false, dest)
		if err == nil || err.Error() != tc.want {
			t.Errorf("extract of %v: %v, want %s", tc.names, err, tc.want)
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(dest), "escape")); err == nil {
			t.Errorf("extract of %v wrote outside its directory", tc.names)
		}
	}
}
