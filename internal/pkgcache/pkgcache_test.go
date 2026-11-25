package pkgcache

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// useCache gives the test a package cache of its own, removed when it
// ends although Install leaves it read-only.
func useCache(t *testing.T) string {
	t.Helper()
	cache := filepath.Join(t.TempDir(), "packages")
	t.Setenv(CacheEnv, cache)
	t.Cleanup(func() { _ = RemoveTree(cache) })
	return cache
}

// cached puts repo at tag into cache with files, as a fetch leaves it, and
// answers its directory. (internal/pkgfetch/pkgfetchtest's CacheRepo does
// the same for other packages' tests, which can import it.)
func cached(t *testing.T, cache, repo, tag string, files map[string]string) string {
	t.Helper()
	dir := RepoDir(cache, repo, tag)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dir), ".tmp-")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	var list []RepoFile
	for _, name := range names {
		writeFile(t, filepath.Join(tmp, filepath.FromSlash(name)), files[name])
		sum := sha256.Sum256([]byte(files[name]))
		list = append(list, RepoFile{Path: name, Sum: hex.EncodeToString(sum[:])})
	}
	if err := Install(cache, repo, tag, strings.Repeat("a", 40), tmp, list); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestWalk_ADependencyRootTakesTheMainPackagesGraph: with MainPackageEnv set
// to a project, a walk rooted at a package the project needs is the
// project's graph, and the dir entry in the dependency's own nomi.toml, of
// a directory that does not exist, is not read, since the main package names
// that package. A root the project does not need keeps its own graph.
func TestWalk_ADependencyRootTakesTheMainPackagesGraph(t *testing.T) {
	useCache(t)
	tmp := t.TempDir()
	app := filepath.Join(tmp, "app")
	dep := filepath.Join(tmp, "dep")
	other := filepath.Join(tmp, "other")
	writeFile(t, filepath.Join(app, ManifestName), "[package]\nname = \"app\"\n\n[dependencies]\n"+
		"dep = { repo = \"example.com/dep\", version = \"v0.1.0\", dir = \"../dep\" }\n"+
		"sib = { repo = \"example.com/sib\", version = \"v0.2.0\", dir = \"../sib\" }\n")
	writeFile(t, filepath.Join(dep, ManifestName), "[package]\nname = \"dep\"\n\n[dependencies]\n"+
		"sib = { repo = \"example.com/sib\", version = \"v0.1.0\", dir = \"../missing\" }\n")
	writeFile(t, filepath.Join(tmp, "sib", ManifestName), "[package]\nname = \"sib\"\n")
	writeFile(t, filepath.Join(other, ManifestName), "[package]\nname = \"other\"\n\n[dependencies]\nsib = { dir = \"../missing\" }\n")

	if _, err := Walk(dep, cacheOnly); err == nil || !strings.Contains(err.Error(), "[dependencies] sib: directory ../missing does not exist") {
		t.Fatalf("with no main package, the dependency's own nomi.toml is the main one; got %v", err)
	}
	t.Setenv(MainPackageEnv, app)
	g, err := Walk(dep, cacheOnly)
	if err != nil {
		t.Fatalf("a walk rooted at a dependency of the main package: %v", err)
	}
	if g.Root != app {
		t.Fatalf("the graph's root is %s, want the main package %s", g.Root, app)
	}
	sib, ok := g.Find(ID{Repo: "example.com/sib"})
	if !ok || !sameDir(sib.Dir, filepath.Join(tmp, "sib")) {
		t.Fatalf("example.com/sib is %+v, want the main package's directory %s", sib, filepath.Join(tmp, "sib"))
	}
	if d, _ := g.Find(ID{Repo: "example.com/dep"}); d.Names["sib"] != "sib" || d.Names["dep"] != "dep" {
		t.Errorf("dep's names are %v, want sib and itself", d.Names)
	}
	if _, err := Walk(other, cacheOnly); err == nil {
		t.Fatalf("a root the main package does not need keeps its own nomi.toml, whose dir names no directory")
	}
}

// Each package the project reaches only through others takes the name the
// first package that reaches it gives it, with a suffix when the project
// gives that name to another package or a file at its root has it.
func TestWalk_NamesInTheProgram(t *testing.T) {
	cache := useCache(t)
	tmp := t.TempDir()
	app := filepath.Join(tmp, "app")
	writeFile(t, filepath.Join(app, ManifestName), "[package]\nname = \"app\"\n\n[dependencies]\n"+
		"a = { dir = \"../a\" }\nlog = { dir = \"../applog\" }\n")
	writeFile(t, filepath.Join(app, "util.nomi"), "")
	writeFile(t, filepath.Join(tmp, "applog", ManifestName), "[package]\nname = \"log\"\n")
	writeFile(t, filepath.Join(tmp, "a", ManifestName), "[package]\nname = \"a\"\n\n[dependencies]\n"+
		"log = { repo = \"example.com/log\", version = \"v1.0.0\" }\n"+
		"util = { repo = \"example.com/util\", version = \"v1.0.0\" }\n")
	for _, m := range []string{"log", "util"} {
		cached(t, cache, "example.com/"+m, "v1.0.0", map[string]string{ManifestName: "[package]\nname = \"" + m + "\"\n"})
	}
	g, err := Walk(app, cacheOnly)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := g.Named("a")
	if a.Names["log"] != "log~2" || a.Names["util"] != "util~2" {
		t.Errorf("a's names are %v, want log~2 and util~2", a.Names)
	}
	if d, _ := g.Named("log"); !sameDir(d.Dir, filepath.Join(tmp, "applog")) || !d.Direct {
		t.Errorf("the project's log is %+v", d)
	}
}

// nomi.lock reads back as written, sorted, and a line that is not a
// package's is an error at its line.
func TestLock_ReadsBackAsWritten(t *testing.T) {
	c, h := strings.Repeat("c", 40), "sha256:"+strings.Repeat("0", 64)
	text := lockHeader +
		"example.com/a . v1.0.0 " + c + " " + h + " direct\n" +
		"example.com/a lib/b v1.0.0 " + c + " " + h + "\n" +
		"example.com/b . v0.2.0-rc.1 " + c + " " + h + "\n" +
		"example.com/c x commit " + c + " " + h + " direct\n"
	l, err := ParseLock([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	if m := l.Packages[3]; m.Version != c || m.Commit != c || m.Subdir != "x" {
		t.Errorf("a package at a commit reads as %+v, want its version the commit", m)
	}
	if len(l.Packages) != 4 || !l.Packages[0].Direct || l.Packages[1].Direct || l.Packages[1].Subdir != "lib/b" || l.Packages[0].Subdir != "" {
		t.Fatalf("ParseLock answered %+v", l.Packages)
	}
	l.Packages[0], l.Packages[2] = l.Packages[2], l.Packages[0]
	if got := string(l.Bytes()); got != text {
		t.Errorf("Bytes is\n%s\nwant\n%s", got, text)
	}
	for _, bad := range []struct{ text, want string }{
		{"example.com/a . v1.0.0 " + c + "\n", "1: a line is a repository, its subdir or ., a version, a commit, a sha256: hash and `direct` or nothing"},
		{"example.com/a . v1.0.0 " + c + " " + h + " indirect\n", "1: a line is a repository"},
		{"# c\nexample.com/a . v1 " + c + " " + h + "\n", `2: example.com/a: "v1" is not a version`},
		{"example.com/a . v1.0.0 abc " + h + "\n", `1: example.com/a v1.0.0: "abc" is not a commit`},
		{"example.com/a . commit " + strings.Repeat("c", 64) + " " + h + "\n", `1: example.com/a at a commit: "` + strings.Repeat("c", 64) + `" is not a commit's 40 hex digits`},
		{"example.com/a . v1.0.0 " + c + " h1:a=\n", `1: example.com/a v1.0.0: "h1:a=" is not a sha256: hash`},
		{"example.com/a ../x v1.0.0 " + c + " " + h + "\n", `1: "../x" is not a directory in a repository`},
		{"example.com/a . v1.0.0 " + c + " " + h + "\nexample.com/a . v1.1.0 " + c + " " + h + "\n", "2: example.com/a is listed twice"},
	} {
		_, err := ParseLock([]byte(bad.text))
		if err == nil || !strings.HasPrefix(err.Error(), bad.want) {
			t.Errorf("ParseLock(%q) = %v, want %q", bad.text, err, bad.want)
		}
	}
}

// An entry is `repo` and `version`, with `subdir` when the package is not
// the repository's root, or `dir`, or all of them; anything else is an
// error at its key.
func TestParseManifest_Entries(t *testing.T) {
	f, err := ParseManifest([]byte("[package]\nname = \"app\"\n\n[dependencies]\n" +
		"a = { repo = \"github.com/o/r\", version = \"v1.2.3\" }\n" +
		"b = { repo = \"github.com/o/r\", subdir = \"lib/b\", version = \"v1.2.3-rc.1\", dir = \"../b\" }\n" +
		"c = { dir = \"../c\" }\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := []Requirement{
		{Name: "a", ID: ID{Repo: "github.com/o/r"}, Version: "v1.2.3"},
		{Name: "b", ID: ID{Repo: "github.com/o/r", Subdir: "lib/b"}, Version: "v1.2.3-rc.1", Dir: "../b"},
		{Name: "c", Dir: "../c"},
	}
	if len(f.Requires) != len(want) {
		t.Fatalf("ParseManifest answered %+v", f.Requires)
	}
	for i := range want {
		if f.Requires[i] != want[i] {
			t.Errorf("entry %d is %+v, want %+v", i, f.Requires[i], want[i])
		}
	}
	for _, bad := range []struct{ entry, want string }{
		{`{ repo = "https://github.com/o/r", version = "v1.0.0" }`, `[dependencies] a: "https://github.com/o/r" is not a repository: write it with no scheme`},
		{`{ repo = "github.com/o/r.git", version = "v1.0.0" }`, `[dependencies] a: "github.com/o/r.git" is not a repository: leave out the .git`},
		{`{ repo = "github", version = "v1.0.0" }`, `[dependencies] a: "github" is not a repository: it is a host and a path`},
		{`{ repo = "github.com/o/r", subdir = "lib/../x", version = "v1.0.0" }`, `[dependencies] a: "lib/../x" is not a directory in a repository`},
		{`{ repo = "github.com/o/r", subdir = ".", version = "v1.0.0" }`, `[dependencies] a: "." is not a directory in a repository`},
		{`{ repo = "github.com/o/r", version = "v1.2" }`, `[dependencies] a: "v1.2" is not a version a tag names, such as v1.2.3`},
		{`{ repo = "github.com/o/r", version = "v1.2.3+meta" }`, `[dependencies] a: "v1.2.3+meta" is not a version a tag names`},
		{`{ repo = "github.com/o/r" }`, "[dependencies] a needs a `version` or a `commit`"},
		{`{ repo = "github.com/o/r", version = "v1.0.0", commit = "` + strings.Repeat("a", 40) + `" }`, "[dependencies] a has a `version` and a `commit`; keep one"},
		{`{ repo = "github.com/o/r", commit = "abc1234" }`, `[dependencies] a: "abc1234" is a short commit: write all 40 hex digits of it`},
		{`{ repo = "github.com/o/r", commit = "` + strings.Repeat("A", 40) + `" }`, "[dependencies] a: write the commit in lowercase, " + strings.Repeat("a", 40)},
		{`{ repo = "github.com/o/r", commit = "main" }`, `[dependencies] a: "main" is not a commit: a commit is its 40 hex digits`},
		{`{ commit = "` + strings.Repeat("a", 40) + `", dir = "../x" }`, "[dependencies] a has a commit but no `repo`"},
		{`{ subdir = "lib", dir = "../x" }`, "[dependencies] a has a `subdir` but no `repo`"},
		{`{ version = "v1.0.0", dir = "../x" }`, "[dependencies] a has a version but no `repo`"},
		{`{ module = "example.com/a" }`, "[dependencies] a needs `repo` and `version`, or `dir`"},
	} {
		_, err := ParseManifest([]byte("[dependencies]\na = " + bad.entry + "\n"))
		if err == nil || !strings.HasPrefix(err.Error(), bad.want) {
			t.Errorf("an entry %s: %v, want %q", bad.entry, err, bad.want)
		}
	}
	_, err = ParseManifest([]byte("[dependencies]\na = { repo = \"github.com/o/r\", version = \"v1.0.0\" }\nb = { repo = \"github.com/o/r\", version = \"v1.1.0\" }\n"))
	if err == nil || err.Error() != "[dependencies] a and b both name github.com/o/r; keep one" {
		t.Errorf("two entries for one package: %v", err)
	}
	c := strings.Repeat("0123456789", 4)
	f, err = ParseManifest([]byte("[dependencies]\na = { repo = \"github.com/o/r\", subdir = \"x\", commit = \"" + c + "\" }\n"))
	if err != nil || len(f.Requires) != 1 || f.Requires[0] != (Requirement{Name: "a", ID: ID{Repo: "github.com/o/r", Subdir: "x"}, Version: c}) {
		t.Errorf("an entry at a commit: %+v, %v", f, err)
	}
}

// A repository is taken at one point in a build: two requirements at the
// same commit are one, and a commit and a tag, or two commits, are an
// error that names both requirers, as two lines are. A lock at a tag where
// the requirements name a commit is the same error, naming the lock.
func TestWalk_ARepositoryAtACommitIsAtThatCommitEverywhere(t *testing.T) {
	cache := useCache(t)
	c1, c2 := strings.Repeat("1", 40), strings.Repeat("2", 40)
	for _, v := range []string{c1, c2, "v1.0.0"} {
		cached(t, cache, "example.com/mono", v, map[string]string{
			"a/" + ManifestName: "[package]\nname = \"a\"\n",
			"b/" + ManifestName: "[package]\nname = \"b\"\n",
		})
	}
	app := t.TempDir()
	entry := func(name, at string) string {
		key := "version"
		if IsCommit(at) {
			key = "commit"
		}
		return name + " = { repo = \"example.com/mono\", subdir = \"" + name + "\", " + key + " = \"" + at + "\" }\n"
	}
	write := func(a, b string) {
		writeFile(t, filepath.Join(app, ManifestName), "[package]\nname = \"app\"\n\n[dependencies]\n"+entry("a", a)+entry("b", b))
	}
	write(c1, c1)
	writeFile(t, filepath.Join(app, LockName), lockHeader+"example.com/mono a v1.0.0 "+c2+" sha256:"+strings.Repeat("0", 64)+" direct\n")
	_, err := Walk(app, cacheOnly)
	if want := "nomi.lock holds example.com/mono at v1.0.0 and app requires example.com/mono at commit " + c1 + ": " +
		"a build takes a repository at one point, so its requirements are all at tags or all at one commit; " +
		"`nomi fetch --update` selects again from the manifests"; err == nil || err.Error() != want {
		t.Fatalf("a lock at a tag under requirements at a commit: %v\nwant %s", err, want)
	}
	writeFile(t, filepath.Join(app, LockName), lockHeader+"example.com/mono a commit "+c1+" sha256:"+strings.Repeat("0", 64)+" direct\n")
	g, err := Walk(app, cacheOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range g.Deps {
		if d.Version != c1 {
			t.Errorf("%s is at %s, want commit %s", d.ID, d.Version, c1)
		}
	}
	for _, tc := range []struct{ a, b, want string }{
		{c1, "v1.0.0", "app requires example.com/mono at commit " + c1 + " and app requires example.com/mono v1.0.0: " +
			"a build takes a repository at one point, so its requirements are all at tags or all at one commit"},
		{c1, c2, "app requires example.com/mono at commit " + c1 + " and app requires example.com/mono at commit " + c2 + ": " +
			"a build takes a repository at one point, so every requirement on it at a commit names the same commit"},
	} {
		write(tc.a, tc.b)
		if _, err := Walk(app, cacheOnly); err == nil || err.Error() != tc.want {
			t.Errorf("%s and %s of one repository: %v\nwant %s", tc.a, tc.b, err, tc.want)
		}
	}
}

// A package loaded from a directory brings its own dir entries, each read
// relative to its own directory: one entry for sqlkit reaches migrate,
// postgres and sqlite from their directories, and the package cache holds
// none of them and needs none.
func TestWalk_ADirectoryPackagesDirEntriesResolve(t *testing.T) {
	useCache(t)
	tmp := t.TempDir()
	lib := filepath.Join(tmp, "lib")
	pkg := func(name, deps string) {
		writeFile(t, filepath.Join(lib, name, ManifestName), "[package]\nname = \""+name+"\"\n\n[dependencies]\n"+deps)
	}
	pkg("sqlkit", "migrate = { dir = \"../migrate\" }\npostgres = { dir = \"../postgres\" }\nsqlite = { dir = \"../sqlite\" }\n")
	pkg("migrate", "postgres = { dir = \"../postgres\" }\nsqlite = { dir = \"../sqlite\" }\n")
	pkg("postgres", "")
	pkg("sqlite", "")
	app := filepath.Join(tmp, "app")
	writeFile(t, filepath.Join(app, ManifestName), "[package]\nname = \"app\"\n\n[dependencies]\nsqlkit = { dir = \"../lib/sqlkit\" }\n")
	resolve := func(root string, m Materialize) (*Graph, error) { return Resolve(root, m, nil) }
	for name, walk := range map[string]func(string, Materialize) (*Graph, error){"Walk": Walk, "Resolve": resolve} {
		g, err := walk(app, cacheOnly)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(g.Missing) != 0 || len(g.Deps) != 4 {
			t.Fatalf("%s: deps %+v, missing %v; want the four packages from their directories", name, g.Deps, g.Missing)
		}
		for _, m := range []string{"sqlkit", "migrate", "postgres", "sqlite"} {
			d, ok := g.At(filepath.Join(lib, m))
			if !ok || !d.Local || d.Name != m || d.Direct != (m == "sqlkit") {
				t.Errorf("%s: the package in lib/%s is %+v", name, m, d)
			}
		}
		sqlkit, _ := g.Named("sqlkit")
		migrate, _ := g.Named("migrate")
		if sqlkit.Names["sqlite"] != "sqlite" || migrate.Names["sqlite"] != "sqlite" || sqlkit.Names["migrate"] != "migrate" {
			t.Errorf("%s: sqlkit's names are %v and migrate's %v, want one sqlite", name, sqlkit.Names, migrate.Names)
		}
	}
}

// The main package's entry for a package wins over a dependency's dir entry
// for it, whether the main package names a directory of its own or a
// version, which is read from the package cache. A dependency's entry with
// only a dir names no package and is read as its directory.
func TestWalk_TheMainPackagesEntryWinsOverADependencysDir(t *testing.T) {
	cache := useCache(t)
	tmp := t.TempDir()
	lib := filepath.Join(tmp, "lib")
	writeFile(t, filepath.Join(lib, "sqlkit", ManifestName), "[package]\nname = \"sqlkit\"\n\n[dependencies]\n"+
		"postgres = { repo = \"example.com/lib\", subdir = \"postgres\", version = \"v1.0.0\", dir = \"../postgres\" }\n"+
		"sqlite = { repo = \"example.com/lib\", subdir = \"sqlite\", version = \"v1.0.0\", dir = \"../sqlite\" }\n"+
		"pg = { dir = \"../postgres\" }\n")
	writeFile(t, filepath.Join(lib, "postgres", ManifestName), "[package]\nname = \"postgres\"\n")
	writeFile(t, filepath.Join(lib, "sqlite", ManifestName), "[package]\nname = \"sqlite\"\n")
	fork := filepath.Join(tmp, "fork", "sqlite")
	writeFile(t, filepath.Join(fork, ManifestName), "[package]\nname = \"sqlite\"\n")
	repo := cached(t, cache, "example.com/lib", "v1.0.0", map[string]string{"postgres/" + ManifestName: "[package]\nname = \"postgres\"\n"})
	app := filepath.Join(tmp, "app")
	writeFile(t, filepath.Join(app, ManifestName), "[package]\nname = \"app\"\n\n[dependencies]\n"+
		"sqlkit = { dir = \"../lib/sqlkit\" }\n"+
		"sqlite = { repo = \"example.com/lib\", subdir = \"sqlite\", version = \"v1.0.0\", dir = \"../fork/sqlite\" }\n"+
		"postgres = { repo = \"example.com/lib\", subdir = \"postgres\", version = \"v1.0.0\" }\n")
	g, err := Walk(app, cacheOnly)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Deps) != 4 || len(g.Missing) != 0 {
		t.Fatalf("deps %+v, missing %v; want sqlkit, the fork's sqlite, the cached postgres and sqlkit's pg", g.Deps, g.Missing)
	}
	sqlite, _ := g.Find(ID{Repo: "example.com/lib", Subdir: "sqlite"})
	if !sameDir(sqlite.Dir, fork) {
		t.Errorf("sqlite is %+v, want the main package's directory %s", sqlite, fork)
	}
	postgres, _ := g.Find(ID{Repo: "example.com/lib", Subdir: "postgres"})
	if postgres.Local || postgres.Version != "v1.0.0" || !sameDir(postgres.Dir, filepath.Join(repo, "postgres")) {
		t.Errorf("postgres is %+v, want v1.0.0 from the package cache", postgres)
	}
	sqlkit, _ := g.Named("sqlkit")
	pg, _ := g.At(filepath.Join(lib, "postgres"))
	if sqlkit.Names["sqlite"] != "sqlite" || sqlkit.Names["postgres"] != "postgres" || sqlkit.Names["pg"] != pg.Name || !pg.Local {
		t.Errorf("sqlkit's names are %v and its pg is %+v, want the main package's sqlite and postgres and pg from its directory", sqlkit.Names, pg)
	}
}

// A fetched package's dir entries name its neighbors in its repository at
// its tag, and one that leaves the repository is an error.
func TestWalk_AFetchedPackagesDirEntriesNameItsNeighbors(t *testing.T) {
	cache := useCache(t)
	cached(t, cache, "example.com/mono", "v1.0.0", map[string]string{
		ManifestName:            "[package]\nname = \"mono\"\n",
		"lib/a/" + ManifestName: "[package]\nname = \"a\"\n\n[dependencies]\nb = { dir = \"../b\" }\nroot = { dir = \"../..\" }\nself = { dir = \".\" }\n",
		"lib/b/" + ManifestName: "[package]\nname = \"b\"\n",
		"leaky/" + ManifestName: "[package]\nname = \"leaky\"\n\n[dependencies]\nout = { dir = \"../../outside\" }\n",
		"lib/a/a.nomi":          "",
		"lib/b/b.nomi":          "",
		"elsewhere/" + "x.nomi": "",
	})
	app := t.TempDir()
	writeFile(t, filepath.Join(app, ManifestName), "[package]\nname = \"app\"\n\n[dependencies]\n"+
		"a = { repo = \"example.com/mono\", subdir = \"lib/a\", version = \"v1.0.0\" }\n")
	g, err := Walk(app, cacheOnly)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := g.Find(ID{Repo: "example.com/mono", Subdir: "lib/a"})
	b, okB := g.Find(ID{Repo: "example.com/mono", Subdir: "lib/b"})
	root, okRoot := g.Find(ID{Repo: "example.com/mono"})
	if !okB || !okRoot || b.Version != "v1.0.0" || root.Version != "v1.0.0" || len(g.Deps) != 3 {
		t.Fatalf("deps %+v, want lib/a, lib/b and the root at v1.0.0", g.Deps)
	}
	if a.Names["b"] != "b" || a.Names["root"] != "root" || a.Names["self"] != "" {
		t.Errorf("lib/a's names are %v", a.Names)
	}

	writeFile(t, filepath.Join(app, ManifestName), "[package]\nname = \"app\"\n\n[dependencies]\n"+
		"leaky = { repo = \"example.com/mono\", subdir = \"leaky\", version = \"v1.0.0\" }\n")
	_, err = Walk(app, cacheOnly)
	want := `example.com/mono/leaky v1.0.0: nomi.toml: [dependencies] out: dir "../../outside" leaves the repository`
	if err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Errorf("a fetched package's dir out of its repository: %v, want %s", err, want)
	}
}

// A cycle of directories ends: a and b name each other, and each is read
// once; b's entry for the main package's own directory is not read.
func TestWalk_ACycleOfDirectoriesEnds(t *testing.T) {
	useCache(t)
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "a", ManifestName), "[package]\nname = \"a\"\n\n[dependencies]\nb = { dir = \"../b\" }\n")
	writeFile(t, filepath.Join(tmp, "b", ManifestName), "[package]\nname = \"b\"\n\n[dependencies]\na = { dir = \"../a\" }\nback = { dir = \"../app\" }\n")
	app := filepath.Join(tmp, "app")
	writeFile(t, filepath.Join(app, ManifestName), "[package]\nname = \"app\"\n\n[dependencies]\na = { dir = \"../a\" }\n")
	g, err := Walk(app, cacheOnly)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Deps) != 2 {
		t.Fatalf("deps %+v, want a and b", g.Deps)
	}
	a, _ := g.Named("a")
	b, _ := g.Named("b")
	if a.Names["b"] != "b" || b.Names["a"] != "a" || b.Names["back"] != "" {
		t.Errorf("a's names are %v and b's %v", a.Names, b.Names)
	}
}

// A repository's directory writes each upper-case letter as `!` and its
// lower-case letter, so names that differ in case stay apart.
func TestRepoDir_EscapesUpperCase(t *testing.T) {
	got := RepoDir("/cache", "github.com/BurntSushi/TOML", "v1.0.0-RC.1")
	want := filepath.Join("/cache", "github.com", "!burnt!sushi", "!t!o!m!l@v1.0.0-!r!c.1")
	if got != want {
		t.Errorf("RepoDir is %s, want %s", got, want)
	}
}

// A package's hash covers its own files by their paths from its directory,
// so a package hashes the same at the root of one repository and in a
// subdirectory of another, and differs when a file does.
func TestFilesHash_IsThePackagesOwnFiles(t *testing.T) {
	sum := func(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
	root := []RepoFile{{Path: "a.nomi", Sum: sum("a")}, {Path: "nomi.toml", Sum: sum("m")}}
	nested := []RepoFile{{Path: "README", Sum: sum("r")}, {Path: "lib/x/a.nomi", Sum: sum("a")}, {Path: "lib/x/nomi.toml", Sum: sum("m")}, {Path: "lib/xy/b", Sum: sum("b")}}
	if FilesHash(root, "") != FilesHash(nested, "lib/x") {
		t.Error("a package hashes differently in a subdirectory, or takes in a neighbor whose name shares its prefix")
	}
	changed := []RepoFile{{Path: "a.nomi", Sum: sum("A")}, {Path: "nomi.toml", Sum: sum("m")}}
	if FilesHash(root, "") == FilesHash(changed, "") {
		t.Error("a changed file left the hash as it was")
	}
	want := sha256.Sum256([]byte(sum("a") + "  a.nomi\n" + sum("m") + "  nomi.toml\n"))
	if got := FilesHash(root, ""); got != "sha256:"+hex.EncodeToString(want[:]) {
		t.Errorf("FilesHash is %s", got)
	}
}

// A link the archive held is listed with the repository's files and not
// hashed: it is in the package whose directory holds it, which LinkIn
// names, and in no other, and the list reads back as Install wrote it.
func TestLinkIn_IsThePackagesOwnLinks(t *testing.T) {
	sum := func(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
	files := []RepoFile{
		{Path: "docs/latest", Link: true},
		{Path: "lib/x/a.nomi", Sum: sum("a")},
		{Path: "lib/x/nomi.toml", Sum: sum("m")},
		{Path: "lib/xy/current", Link: true},
		{Path: "lib/xy/nomi.toml", Sum: sum("n")},
	}
	for _, tc := range []struct{ subdir, want string }{
		{"lib/x", ""},
		{"lib/xy", "lib/xy/current"},
		{"", "docs/latest"},
	} {
		if got := LinkIn(files, tc.subdir); got != tc.want {
			t.Errorf("LinkIn of %q is %q, want %q", tc.subdir, got, tc.want)
		}
	}
	plain := []RepoFile{{Path: "a.nomi", Sum: sum("a")}, {Path: "nomi.toml", Sum: sum("m")}}
	if FilesHash(files, "lib/x") != FilesHash(plain, "") || FilesHash(files, "lib/xy") != FilesHash([]RepoFile{{Path: "nomi.toml", Sum: sum("n")}}, "") {
		t.Error("a link changed a package's hash, which covers its regular files alone")
	}

	cache := t.TempDir()
	dir := RepoDir(cache, "example.com/links", "v1.0.0")
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dir), ".tmp-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = RemoveTree(cache) })
	if err := Install(cache, "example.com/links", "v1.0.0", "c0ffee", tmp, files); err != nil {
		t.Fatal(err)
	}
	commit, got, err := ReadRepoFiles(dir)
	if err != nil || commit != "c0ffee" || !reflect.DeepEqual(got, files) {
		t.Errorf("the list reads back as %q, %+v, %v, want %+v", commit, got, err, files)
	}
}

// One tag versions a whole repository: two of its packages asked for at two
// versions are both taken at the higher, and two major versions of it are
// an error naming both requirers.
func TestWalk_OneVersionPerRepository(t *testing.T) {
	cache := useCache(t)
	for _, v := range []string{"v1.0.0", "v1.1.0", "v2.0.0"} {
		cached(t, cache, "example.com/mono", v, map[string]string{
			"a/" + ManifestName: "[package]\nname = \"a\"\n",
			"b/" + ManifestName: "[package]\nname = \"b\"\n",
		})
	}
	app := t.TempDir()
	write := func(va, vb string) {
		writeFile(t, filepath.Join(app, ManifestName), "[package]\nname = \"app\"\n\n[dependencies]\n"+
			"a = { repo = \"example.com/mono\", subdir = \"a\", version = \""+va+"\" }\n"+
			"b = { repo = \"example.com/mono\", subdir = \"b\", version = \""+vb+"\" }\n")
	}
	write("v1.0.0", "v1.1.0")
	g, err := Walk(app, cacheOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range g.Deps {
		if d.Version != "v1.1.0" {
			t.Errorf("%s is at %s, want v1.1.0", d.ID, d.Version)
		}
	}
	write("v1.0.0", "v2.0.0")
	_, err = Walk(app, cacheOnly)
	if err == nil || !strings.HasPrefix(err.Error(), "app requires example.com/mono v1.0.0 and app requires example.com/mono v2.0.0: one tag versions a whole repository") {
		t.Errorf("two major versions of one repository: %v", err)
	}
}

// lockLine is a nomi.lock line for repo's root package at version v.
func lockLine(repo, v string) string {
	return repo + " . " + v + " " + strings.Repeat("a", 40) + " sha256:" + strings.Repeat("b", 64) + " direct\n"
}

// A nomi.lock that holds a repository in another line than the manifest
// asks for, as after an edit of nomi.toml by hand, is an error that names
// the lock, in the walk that reads it and in a resolve that starts from
// its versions; a resolve from none (`nomi fetch --update`) selects from
// the manifest. A lower version in the lock's line is a raise it keeps.
func TestWalk_ALockInAnotherLineThanTheManifestIsAnError(t *testing.T) {
	cache := useCache(t)
	for _, v := range []string{"v1.0.0", "v1.1.0", "v2.0.0"} {
		cached(t, cache, "example.com/util", v, utilFiles)
	}
	app := t.TempDir()
	writeFile(t, filepath.Join(app, ManifestName), "[package]\nname = \"app\"\n\n[dependencies]\n"+
		"util = { repo = \"example.com/util\", version = \"v1.0.0\" }\n")
	writeFile(t, filepath.Join(app, LockName), lockHeader+lockLine("example.com/util", "v2.0.0"))
	want := "nomi.lock holds example.com/util at v2.0.0 and app requires example.com/util v1.0.0: " +
		"one tag versions a whole repository, so every requirement on it must be in one line: " +
		"a major version from v1 on, a minor version from v0.1 on, and below v0.1 one release; " +
		"`nomi fetch --update` selects again from the manifests"
	for what, walk := range map[string]func() (*Graph, error){
		"Walk": func() (*Graph, error) { return Walk(app, cacheOnly) },
		"Resolve from the lock": func() (*Graph, error) {
			return Resolve(app, cacheOnly, map[string]string{"example.com/util": "v2.0.0"})
		},
	} {
		_, err := walk()
		var clash *ClashError
		if !errors.As(err, &clash) || !clash.Held || clash.Repo != "example.com/util" || err.Error() != want {
			t.Errorf("%s with the lock in another line: %v\nwant %s", what, err, want)
		}
	}
	if g, err := Resolve(app, cacheOnly, nil); err != nil || versionsOf(g)["example.com/util"] != "v1.0.0" {
		t.Errorf("a resolve from no versions: %v, %v; want util at v1.0.0", err, g)
	}
	writeFile(t, filepath.Join(app, LockName), lockHeader+lockLine("example.com/util", "v1.1.0"))
	if g, err := Walk(app, cacheOnly); err != nil || versionsOf(g)["example.com/util"] != "v1.1.0" {
		t.Errorf("a lock above the manifest in its line: %v, %v; want util at v1.1.0", err, g)
	}
}

// Prune removes the repositories nothing has resolved for two weeks,
// read-only as they are, and extractions left for an hour, and keeps the
// rest; a hit marks a repository used.
func TestPrune_RemovesWhatNothingResolves(t *testing.T) {
	cache := useCache(t)
	files := map[string]string{ManifestName: "[package]\nname = \"m\"\n"}
	old := cached(t, cache, "example.com/old", "v1.0.0", files)
	used := cached(t, cache, "example.com/used", "v1.0.0", files)
	fresh := cached(t, cache, "example.com/group/fresh", "v1.0.0", files)
	partial := filepath.Join(cache, "example.com", ".tmp-partial")
	writeFile(t, filepath.Join(partial, "x"), "")
	long := time.Now().Add(-15 * 24 * time.Hour)
	for _, p := range []string{old, old + filesSuffix, used, used + filesSuffix, partial} {
		if err := os.Chtimes(p, long, long); err != nil {
			t.Fatal(err)
		}
	}
	if Cached(cache, Package{ID: ID{Repo: "example.com/used"}, Version: "v1.0.0"}) == "" {
		t.Fatal("the used repository is not in the cache")
	}
	Prune(cache)
	for p, kept := range map[string]bool{old: false, old + filesSuffix: false, partial: false, used: true, used + filesSuffix: true, fresh: true} {
		if _, err := os.Stat(p); (err == nil) != kept {
			t.Errorf("after Prune, %s is there: %v, want %v", p, err == nil, kept)
		}
	}
}

// asRelease makes the test's process a release `nomi` at version v until
// the test ends.
func asRelease(t *testing.T, v string) {
	t.Helper()
	SetRelease(v)
	t.Cleanup(func() { SetRelease("") })
}

// [package].nomi is the lowest release a package needs. A release older
// than it refuses the package, naming it, the release it needs and the one
// running: the project itself, a fetched package by its repository,
// directory and version, a package loaded from a directory by its
// directory. The refusal comes before any other problem in the file. A
// release as new or newer, and a `nomi` built from source, refuse nothing,
// and a value that is no release is an error at the key.
func TestWalk_APackageThatNeedsALaterNomiIsRefused(t *testing.T) {
	cache := useCache(t)
	cached(t, cache, "example.com/kit", "v1.2.0", map[string]string{
		"table/" + ManifestName: "[package]\nname = \"table\"\nnomi = \"v0.6.0\"\n",
	})
	app := t.TempDir()
	writeFile(t, filepath.Join(app, "helpers", ManifestName), "[package]\nname = \"helpers\"\nnomi = \"v0.7.0\"\n")
	project := func(needs, deps string) {
		writeFile(t, filepath.Join(app, ManifestName), "[package]\nname = \"app\"\n"+needs+"\n[dependencies]\n"+deps)
	}
	kit := "table = { repo = \"example.com/kit\", subdir = \"table\", version = \"v1.2.0\" }\n"
	helpers := "helpers = { dir = \"./helpers\" }\n"

	for _, tc := range []struct{ release, needs, deps, want string }{
		{"v0.5.0", "nomi = \"v0.6.0\"\n", "bad = { repo = \"no scheme://x\" }\n",
			"package app (this project) needs nomi v0.6.0 or later, and this nomi is v0.5.0; install a newer nomi"},
		{"v0.5.0", "", kit,
			"package example.com/kit/table v1.2.0 needs nomi v0.6.0 or later, and this nomi is v0.5.0; install a newer nomi"},
		{"v0.6.0", "", helpers,
			"the package in ./helpers needs nomi v0.7.0 or later, and this nomi is v0.6.0; install a newer nomi"},
		{"v0.6.0-rc.1", "", kit,
			"package example.com/kit/table v1.2.0 needs nomi v0.6.0 or later, and this nomi is v0.6.0-rc.1; install a newer nomi"},
	} {
		asRelease(t, tc.release)
		project(tc.needs, tc.deps)
		_, err := Walk(app, cacheOnly)
		var tooOld *NomiVersionError
		if !errors.As(err, &tooOld) || err.Error() != tc.want {
			t.Errorf("release %s, %s%s: %v\nwant %s", tc.release, tc.needs, tc.deps, err, tc.want)
		}
	}
	for _, release := range []string{"v0.6.0", "v1.0.0", ""} {
		asRelease(t, release)
		project("nomi = \"v0.6.0\"\n", kit)
		if _, err := Walk(app, cacheOnly); err != nil {
			t.Errorf("release %q refused a package that needs v0.6.0: %v", release, err)
		}
	}
	asRelease(t, "")
	project("nomi = \"v0.7.0\"\n", helpers+kit)
	if _, err := Walk(app, cacheOnly); err != nil {
		t.Errorf("a nomi built from source refused a package: %v", err)
	}
	for _, bad := range []string{"0.6.0", "v0.6", "latest"} {
		_, err := ParseManifest([]byte("[package]\nname = \"app\"\nnomi = \"" + bad + "\"\n"))
		var problem *ManifestProblem
		want := fmt.Sprintf("[package] nomi: %q is not a nomi release, such as v0.2.0", bad)
		if !errors.As(err, &problem) || err.Error() != want || strings.Join(problem.Key, ".") != "package.nomi" {
			t.Errorf("nomi = %q: %v, want %s", bad, err, want)
		}
	}
}

// A version's line is its major version from v1 on, its minor version
// from v0.1 on, and below v0.1 the release itself; a prerelease's is that
// of its release. LineRule states the tier's rule.
func TestLine_HasThreeTiers(t *testing.T) {
	for v, want := range map[string]string{
		"v0.0.1": "v0.0.1", "v0.0.3": "v0.0.3", "v0.0.4-rc.1": "v0.0.4",
		"v0.1.0": "v0.1", "v0.1.9": "v0.1", "v0.2.0-rc.1": "v0.2",
		"v1.0.0": "v1", "v1.4.2": "v1", "v2.0.0-beta": "v2",
	} {
		if got := Line(v); got != want {
			t.Errorf("Line(%s) = %s, want %s", v, got, want)
		}
		rule := "from v1.0.0 each major version"
		switch {
		case strings.HasPrefix(v, "v0.0."):
			rule = "below v0.1.0 each release"
		case strings.HasPrefix(v, "v0."):
			rule = "below v1.0.0 each minor version"
		}
		if got, want := LineRule(v), rule+" is a line of its own"; got != want {
			t.Errorf("LineRule(%s) = %q, want %q", v, got, want)
		}
	}
}
