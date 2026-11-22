package modcache

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

// TestWalk_ADependencyRootTakesTheMainModulesGraph: with MainModuleEnv set
// to a project, a walk rooted at a module the project needs is the
// project's graph, and the dir entry in the dependency's own nomi.toml, of
// a directory that does not exist, is not read, as a dir entry applies only
// in the main module. A root the project does not need keeps its own graph.
func TestWalk_ADependencyRootTakesTheMainModulesGraph(t *testing.T) {
	tmp := t.TempDir()
	app := filepath.Join(tmp, "app")
	dep := filepath.Join(tmp, "dep")
	other := filepath.Join(tmp, "other")
	writeFile(t, filepath.Join(app, ManifestName), "[module]\nname = \"app\"\n\n[dependencies]\n"+
		"dep = { module = \"example.com/dep\", version = \"v0.1.0\", dir = \"../dep\" }\n"+
		"sib = { module = \"example.com/sib\", version = \"v0.2.0\", dir = \"../sib\" }\n")
	writeFile(t, filepath.Join(dep, ManifestName), "[module]\nname = \"dep\"\npath = \"example.com/dep\"\n\n[dependencies]\n"+
		"sib = { module = \"example.com/sib\", version = \"v0.1.0\", dir = \"../missing\" }\n")
	writeFile(t, filepath.Join(tmp, "sib", ManifestName), "[module]\nname = \"sib\"\npath = \"example.com/sib\"\n")
	writeFile(t, filepath.Join(other, ManifestName), "[module]\nname = \"other\"\n\n[dependencies]\nsib = { dir = \"../missing\" }\n")

	if _, err := Walk(dep, cacheOnly); err == nil || !strings.Contains(err.Error(), "[dependencies] sib: directory ../missing does not exist") {
		t.Fatalf("with no main module, the dependency's own nomi.toml is the main one; got %v", err)
	}
	t.Setenv(MainModuleEnv, app)
	g, err := Walk(dep, cacheOnly)
	if err != nil {
		t.Fatalf("a walk rooted at a dependency of the main module: %v", err)
	}
	if g.Root != app {
		t.Fatalf("the graph's root is %s, want the main module %s", g.Root, app)
	}
	sib, ok := g.Find("example.com/sib")
	if !ok || !sameDir(sib.Dir, filepath.Join(tmp, "sib")) {
		t.Fatalf("example.com/sib is %+v, want the main module's directory %s", sib, filepath.Join(tmp, "sib"))
	}
	if d, _ := g.Find("example.com/dep"); d.Names["sib"] != "sib" || d.Names["dep"] != "dep" {
		t.Errorf("dep's names are %v, want sib and itself", d.Names)
	}
	if _, err := Walk(other, cacheOnly); err == nil {
		t.Fatalf("a root the main module does not need keeps its own nomi.toml, whose dir names no directory")
	}
}

// Each module the project reaches only through others takes the name the
// first module that reaches it gives it, with a suffix when the project
// gives that name to another module or a file at its root has it.
func TestWalk_NamesInTheProgram(t *testing.T) {
	tmp := t.TempDir()
	app := filepath.Join(tmp, "app")
	writeFile(t, filepath.Join(app, ManifestName), "[module]\nname = \"app\"\n\n[dependencies]\n"+
		"a = { dir = \"../a\" }\nlog = { dir = \"../applog\" }\n")
	writeFile(t, filepath.Join(app, "util.nomi"), "")
	writeFile(t, filepath.Join(tmp, "applog", ManifestName), "[module]\nname = \"log\"\n")
	writeFile(t, filepath.Join(tmp, "a", ManifestName), "[module]\nname = \"a\"\npath = \"example.com/a\"\n\n[dependencies]\n"+
		"log = { module = \"example.com/log\", version = \"v1.0.0\" }\n"+
		"util = { module = \"example.com/util\", version = \"v1.0.0\" }\n")
	cache := t.TempDir()
	t.Setenv("GOMODCACHE", cache)
	for _, m := range []string{"log", "util"} {
		writeFile(t, filepath.Join(cache, "example.com", m+"@v1.0.0", ManifestName), "[module]\nname = \""+m+"\"\n")
		writeFile(t, filepath.Join(cache, "cache", "download", "example.com", m, "@v", "v1.0.0.ziphash"), "h1:x\n")
	}
	g, err := Walk(app, cacheOnly)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := g.Find("example.com/a")
	if a.Names["log"] != "log~2" || a.Names["util"] != "util~2" {
		t.Errorf("a's names are %v, want log~2 and util~2", a.Names)
	}
	if d, _ := g.Named("log"); !sameDir(d.Dir, filepath.Join(tmp, "applog")) || !d.Direct {
		t.Errorf("the project's log is %+v", d)
	}
}

// nomi.lock reads back as written, sorted, and a line that is not a
// module's is an error at its line.
func TestLock_ReadsBackAsWritten(t *testing.T) {
	text := lockHeader +
		"example.com/a v1.0.0 h1:aaa= direct\n" +
		"example.com/b v0.2.0 h1:bbb=\n"
	l, err := ParseLock([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Modules) != 2 || !l.Modules[0].Direct || l.Modules[1].Direct || l.Modules[1].Hash != "h1:bbb=" {
		t.Fatalf("ParseLock answered %+v", l.Modules)
	}
	l.Modules[0], l.Modules[1] = l.Modules[1], l.Modules[0]
	if got := string(l.Bytes()); got != text {
		t.Errorf("Bytes is\n%s\nwant\n%s", got, text)
	}
	for _, bad := range []struct{ text, want string }{
		{"example.com/a v1.0.0\n", "1: a line is a module path, its version, its h1: hash and `direct` or nothing"},
		{"example.com/a v1.0.0 h1:a= indirect\n", "1: a line is a module path"},
		{"# c\nexample.com/a v1 h1:a=\n", "2: example.com/a v1 is not a module path and version"},
		{"example.com/a v1.0.0 sha:a=\n", "1: example.com/a v1.0.0: \"sha:a=\" is not an h1: hash"},
		{"example.com/a v1.0.0 h1:a=\nexample.com/a v1.1.0 h1:b=\n", "2: example.com/a is listed twice"},
	} {
		_, err := ParseLock([]byte(bad.text))
		if err == nil || !strings.HasPrefix(err.Error(), bad.want) {
			t.Errorf("ParseLock(%q) = %v, want %q", bad.text, err, bad.want)
		}
	}
}

// libModule writes the nomi.toml of example.com/lib/<name> in dir/<name>
// with deps as its [dependencies] lines.
func libModule(t *testing.T, dir, name, deps string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, name, ManifestName), "[module]\nname = \""+name+"\"\npath = \"example.com/lib/"+name+"\"\n\n[dependencies]\n"+deps)
}

// libEntry is a [dependencies] line for example.com/lib/<name> at a
// placeholder version, in dir.
func libEntry(name, dir string) string {
	return name + ` = { module = "example.com/lib/` + name + `", version = "v0.0.0-20261102000000-000000000000", dir = "` + dir + `" }` + "\n"
}

// cached puts example.com/<path> at version in the module cache with
// manifest as its nomi.toml, and answers its directory.
func cached(t *testing.T, cache, path, version, manifest string) string {
	t.Helper()
	dir := filepath.Join(cache, "example.com", path+"@"+version)
	writeFile(t, filepath.Join(dir, ManifestName), manifest)
	writeFile(t, filepath.Join(cache, "cache", "download", "example.com", path, "@v", version+".ziphash"), "h1:x\n")
	return dir
}

// A module loaded from a directory brings its own dir entries, each read
// relative to its own directory: one entry for sqlkit reaches migrate,
// postgres and sqlite from their directories, and the module cache holds
// none of them and needs none.
func TestWalk_ADirectoryModulesDirEntriesResolve(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("GOMODCACHE", t.TempDir())
	lib := filepath.Join(tmp, "lib")
	libModule(t, lib, "sqlkit", libEntry("migrate", "../migrate")+libEntry("postgres", "../postgres")+libEntry("sqlite", "../sqlite"))
	libModule(t, lib, "migrate", libEntry("postgres", "../postgres")+libEntry("sqlite", "../sqlite"))
	libModule(t, lib, "postgres", "")
	libModule(t, lib, "sqlite", "")
	app := filepath.Join(tmp, "app")
	writeFile(t, filepath.Join(app, ManifestName), "[module]\nname = \"app\"\n\n[dependencies]\nsqlkit = { dir = \"../lib/sqlkit\" }\n")
	for name, walk := range map[string]func(string, Materialize) (*Graph, error){"Walk": Walk, "Resolve": Resolve} {
		g, err := walk(app, cacheOnly)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(g.Missing) != 0 || len(g.Deps) != 4 {
			t.Fatalf("%s: deps %+v, missing %v; want the four modules from their directories", name, g.Deps, g.Missing)
		}
		for _, m := range []string{"sqlkit", "migrate", "postgres", "sqlite"} {
			d, ok := g.Find("example.com/lib/" + m)
			if !ok || !d.Local || !sameDir(d.Dir, filepath.Join(lib, m)) || d.Direct != (m == "sqlkit") {
				t.Errorf("%s: example.com/lib/%s is %+v, want it from %s", name, m, d, filepath.Join(lib, m))
			}
		}
		sqlkit, _ := g.Find("example.com/lib/sqlkit")
		migrate, _ := g.Find("example.com/lib/migrate")
		sqlite, _ := g.Find("example.com/lib/sqlite")
		if sqlkit.Names["sqlite"] != sqlite.Name || migrate.Names["sqlite"] != sqlite.Name || sqlkit.Names["migrate"] != migrate.Name {
			t.Errorf("%s: sqlkit's names are %v and migrate's %v, want one sqlite", name, sqlkit.Names, migrate.Names)
		}
	}
}

// The main module's entry for a module path wins over a dependency's dir
// entry for it, whether the main module names a directory of its own or
// a version, which is read from the module cache; a dependency's entry
// with only a dir, whose directory holds that path, is read by the path.
func TestWalk_TheMainModulesEntryWinsOverADependencysDir(t *testing.T) {
	tmp := t.TempDir()
	cache := t.TempDir()
	t.Setenv("GOMODCACHE", cache)
	lib := filepath.Join(tmp, "lib")
	libModule(t, lib, "sqlkit", libEntry("postgres", "../postgres")+libEntry("sqlite", "../sqlite")+"pg = { dir = \"../postgres\" }\n")
	libModule(t, lib, "postgres", "")
	libModule(t, lib, "sqlite", "")
	fork := filepath.Join(tmp, "fork")
	libModule(t, fork, "sqlite", "")
	pg := cached(t, cache, "lib/postgres", "v1.0.0", "[module]\nname = \"postgres\"\npath = \"example.com/lib/postgres\"\n")
	app := filepath.Join(tmp, "app")
	writeFile(t, filepath.Join(app, ManifestName), "[module]\nname = \"app\"\n\n[dependencies]\n"+
		"sqlkit = { dir = \"../lib/sqlkit\" }\n"+
		"sqlite = { dir = \"../fork/sqlite\" }\n"+
		"postgres = { module = \"example.com/lib/postgres\", version = \"v1.0.0\" }\n")
	g, err := Walk(app, cacheOnly)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Deps) != 3 || len(g.Missing) != 0 {
		t.Fatalf("deps %+v, missing %v; want sqlkit, the fork's sqlite and the cached postgres", g.Deps, g.Missing)
	}
	sqlite, _ := g.Find("example.com/lib/sqlite")
	if !sameDir(sqlite.Dir, filepath.Join(fork, "sqlite")) {
		t.Errorf("sqlite is %+v, want the main module's directory %s", sqlite, filepath.Join(fork, "sqlite"))
	}
	postgres, _ := g.Find("example.com/lib/postgres")
	if postgres.Local || postgres.Version.Version != "v1.0.0" || !sameDir(postgres.Dir, pg) {
		t.Errorf("postgres is %+v, want v1.0.0 from the module cache", postgres)
	}
	sqlkit, _ := g.Find("example.com/lib/sqlkit")
	if sqlkit.Names["sqlite"] != "sqlite" || sqlkit.Names["postgres"] != "postgres" || sqlkit.Names["pg"] != "postgres" {
		t.Errorf("sqlkit's names are %v, want the main module's sqlite and postgres", sqlkit.Names)
	}
}

// A downloaded module's dir entries name its author's disk and are not
// read, even when a directory there holds a module: its entry with a
// version is taken from the module cache, and one with only a dir is
// skipped.
func TestWalk_ADownloadedModulesDirEntriesAreNotRead(t *testing.T) {
	tmp := t.TempDir()
	cache := t.TempDir()
	t.Setenv("GOMODCACHE", cache)
	cached(t, cache, "dl", "v1.0.0", "[module]\nname = \"dl\"\npath = \"example.com/dl\"\n\n[dependencies]\n"+
		"sib = { module = \"example.com/sib\", version = \"v1.0.0\", dir = \"../sib\" }\n"+
		"only = { dir = \"../only\" }\n")
	sib := cached(t, cache, "sib", "v1.0.0", "[module]\nname = \"sib\"\npath = \"example.com/sib\"\n")
	writeFile(t, filepath.Join(cache, "example.com", "sib", ManifestName), "[module]\nname = \"sib\"\npath = \"example.com/sib\"\n")
	writeFile(t, filepath.Join(cache, "example.com", "only", ManifestName), "[module]\nname = \"only\"\n")
	app := filepath.Join(tmp, "app")
	writeFile(t, filepath.Join(app, ManifestName), "[module]\nname = \"app\"\n\n[dependencies]\ndl = { module = \"example.com/dl\", version = \"v1.0.0\" }\n")
	g, err := Walk(app, cacheOnly)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Deps) != 2 || len(g.Missing) != 0 {
		t.Fatalf("deps %+v, missing %v; want dl and sib from the module cache", g.Deps, g.Missing)
	}
	if d, _ := g.Find("example.com/sib"); d.Local || !sameDir(d.Dir, sib) {
		t.Errorf("sib is %+v, want v1.0.0 from the module cache", d)
	}
	if d, _ := g.Find("example.com/dl"); d.Names["only"] != "" {
		t.Errorf("dl's entry with only a dir was read: %v", d.Names)
	}
}

// A cycle of directories ends: a and b name each other, and each is read
// once; b's entry for the main module's own directory is not read.
func TestWalk_ACycleOfDirectoriesEnds(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("GOMODCACHE", t.TempDir())
	writeFile(t, filepath.Join(tmp, "a", ManifestName), "[module]\nname = \"a\"\n\n[dependencies]\nb = { dir = \"../b\" }\n")
	writeFile(t, filepath.Join(tmp, "b", ManifestName), "[module]\nname = \"b\"\n\n[dependencies]\na = { dir = \"../a\" }\nback = { dir = \"../app\" }\n")
	app := filepath.Join(tmp, "app")
	writeFile(t, filepath.Join(app, ManifestName), "[module]\nname = \"app\"\n\n[dependencies]\na = { dir = \"../a\" }\n")
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
