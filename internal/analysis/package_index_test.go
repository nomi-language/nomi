package analysis

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/pkgcache"
	"github.com/nomi-language/nomi/internal/pkgfetch/pkgfetchtest"
)

// writeFile is a small helper that mkdir-p's the parent and writes
// the given content. Tests fail-fast on filesystem errors.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// sameDirs fails unless got and want name one directory: t.TempDir on macOS
// hands back a path under /var/folders/... that is a link to /private/var/...
func sameDirs(t *testing.T, what, got, want string) {
	t.Helper()
	g, _ := filepath.EvalSymlinks(got)
	w, _ := filepath.EvalSymlinks(want)
	if g == "" || g != w {
		t.Fatalf("%s is %q, want %s", what, got, want)
	}
}

// TestBuildPackageIndex_ADirDependency: a `todo` project names a sibling
// package with a dir entry. The index holds it by the name todo's
// [dependencies] gives it, at its directory.
func TestBuildPackageIndex_ADirDependency(t *testing.T) {
	tmp := t.TempDir()
	todo := filepath.Join(tmp, "todo")
	stringkit := filepath.Join(tmp, "stringkit")
	writeFile(t, filepath.Join(todo, "nomi.toml"), "[package]\nname = \"todo\"\n\n[dependencies]\nstringkit = { dir = \"../stringkit\" }\n")
	writeFile(t, filepath.Join(stringkit, "nomi.toml"), "[package]\nname = \"stringkit\"\n")

	idx, err := BuildPackageIndex(todo)
	if err != nil {
		t.Fatalf("BuildPackageIndex: %v", err)
	}
	if len(idx.Roots) != 1 || idx.Names[""]["stringkit"] != "stringkit" {
		t.Fatalf("index = %#v, want stringkit alone", idx)
	}
	sameDirs(t, "stringkit's root", idx.Roots["stringkit"], stringkit)
	if self := idx.Names["stringkit"]["stringkit"]; self != "stringkit" {
		t.Errorf("stringkit's own name is %q in its names, want itself", self)
	}
}

// TestBuildPackageIndex_NoManifest: a directory without a nomi.toml is an
// empty index, no error, which keeps single-file `nomi run` valid.
func TestBuildPackageIndex_NoManifest(t *testing.T) {
	idx, err := BuildPackageIndex(t.TempDir())
	if err != nil {
		t.Fatalf("BuildPackageIndex on a directory without nomi.toml: %v", err)
	}
	if len(idx.Roots) != 0 || len(idx.Names[""]) != 0 {
		t.Fatalf("expected an empty index, got %#v", idx)
	}
}

// TestBuildPackageIndex_NotInThePackageCache: a dependency the package cache
// does not hold is left out of the index, not an error, since the analysis
// never downloads; an import of it reports it missing
// (TestShortNameImport_FromThePackageCacheAndMissingFromIt).
func TestBuildPackageIndex_NotInThePackageCache(t *testing.T) {
	pkgfetchtest.Cache(t)
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "nomi.toml"),
		"[package]\nname = \"todo\"\n\n[dependencies]\nremote = { repo = \"example.com/remote\", version = \"v1.2.3\" }\n")
	idx, err := BuildPackageIndex(tmp)
	if err != nil {
		t.Fatalf("BuildPackageIndex: %v", err)
	}
	if len(idx.Roots) != 0 {
		t.Fatalf("expected an empty index, got %#v", idx)
	}
}

// TestBuildPackageIndex_MissingDir: a dir entry naming a directory that does
// not exist is an error that names the directory.
func TestBuildPackageIndex_MissingDir(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "nomi.toml"),
		"[package]\nname = \"todo\"\n\n[dependencies]\nstringkit = { dir = \"../does-not-exist\" }\n")
	_, err := BuildPackageIndex(tmp)
	if err == nil {
		t.Fatalf("BuildPackageIndex admits a dir entry whose directory does not exist")
	}
	if want := "[dependencies] stringkit: directory ../does-not-exist does not exist"; !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q, want one with %q", err, want)
	}
}

// TestBuildPackageIndex_NotANomiPackage: a dependency whose directory holds
// no nomi.toml is not indexed.
func TestBuildPackageIndex_NotANomiPackage(t *testing.T) {
	tmp := t.TempDir()
	todo := filepath.Join(tmp, "todo")
	writeFile(t, filepath.Join(todo, "nomi.toml"), "[package]\nname = \"todo\"\n\n[dependencies]\ngokit = { dir = \"../gokit\" }\n")
	writeFile(t, filepath.Join(tmp, "gokit", "kit.go"), "package gokit\n")
	idx, err := BuildPackageIndex(todo)
	if err != nil {
		t.Fatalf("BuildPackageIndex: %v", err)
	}
	if len(idx.Roots) != 0 || len(idx.Names[""]) != 0 {
		t.Fatalf("expected an empty index, got %#v", idx)
	}
}

// TestBuildPackageIndex_TwoPackagesWithOneOwnName: two dependencies whose
// own nomi.toml give them the same [package].name are two packages, each
// under the name the project gives it. A package's own name is only the
// name `nomi add` gives it.
func TestBuildPackageIndex_TwoPackagesWithOneOwnName(t *testing.T) {
	tmp := t.TempDir()
	todo := filepath.Join(tmp, "todo")
	writeFile(t, filepath.Join(todo, "nomi.toml"),
		"[package]\nname = \"todo\"\n\n[dependencies]\naudit = { dir = \"../lib-b\" }\nshared = { dir = \"../lib-a\" }\n")
	writeFile(t, filepath.Join(tmp, "lib-a", "nomi.toml"), "[package]\nname = \"shared\"\n")
	writeFile(t, filepath.Join(tmp, "lib-b", "nomi.toml"), "[package]\nname = \"shared\"\n")

	idx, err := BuildPackageIndex(todo)
	if err != nil {
		t.Fatalf("two dependencies with one own name: %v", err)
	}
	sameDirs(t, "shared", idx.Roots[idx.Names[""]["shared"]], filepath.Join(tmp, "lib-a"))
	sameDirs(t, "audit", idx.Roots[idx.Names[""]["audit"]], filepath.Join(tmp, "lib-b"))
	// Each package's own name names itself in its own files.
	if idx.Names["audit"]["shared"] != "audit" || idx.Names["shared"]["shared"] != "shared" {
		t.Errorf("each package's own name: %#v", idx.Names)
	}
}

// TestBuildPackageIndex_NamesArePerPackage: a dependency's imports resolve
// through its own [dependencies], never the project's. app names kit's
// dependency `text` as `words`; kit names it `text`. A package only kit
// names takes kit's name for it in the program. Only the package the cache
// supplied is Fetched.
func TestBuildPackageIndex_NamesArePerPackage(t *testing.T) {
	tmp := t.TempDir()
	app := filepath.Join(tmp, "app")
	writeFile(t, filepath.Join(app, "nomi.toml"), "[package]\nname = \"app\"\n\n[dependencies]\n"+
		"kit = { repo = \"example.com/kit\", version = \"v0.1.0\", dir = \"../kit\" }\n"+
		"words = { repo = \"example.com/text\", version = \"v0.1.0\", dir = \"../text\" }\n")
	writeFile(t, filepath.Join(tmp, "kit", "nomi.toml"), "[package]\nname = \"kit\"\n\n[dependencies]\n"+
		"pad = { repo = \"example.com/pad\", version = \"v0.1.0\" }\n"+
		"text = { repo = \"example.com/text\", version = \"v0.1.0\" }\n")
	writeFile(t, filepath.Join(tmp, "text", "nomi.toml"), "[package]\nname = \"text\"\n")
	cache := pkgfetchtest.Cache(t)
	pkgfetchtest.CacheRepo(t, cache, "example.com/pad", "v0.1.0", map[string]string{"nomi.toml": "[package]\nname = \"pad\"\n"})

	idx, err := BuildPackageIndex(app)
	if err != nil {
		t.Fatalf("BuildPackageIndex: %v", err)
	}
	if got := idx.Names[""]; len(got) != 2 || got["kit"] != "kit" || got["words"] != "words" {
		t.Errorf("app's names are %v, want kit and words", got)
	}
	if got := idx.Names["kit"]; got["text"] != "words" || got["pad"] != "pad" || got["kit"] != "kit" {
		t.Errorf("kit's names are %v, want text as words, pad and itself", got)
	}
	sameDirs(t, "kit's text", idx.Roots[idx.Names["kit"]["text"]], filepath.Join(tmp, "text"))
	if _, ok := idx.Names[""]["pad"]; ok {
		t.Errorf("app names pad, which only kit names")
	}
	sameDirs(t, "pad", idx.Roots["pad"], pkgcache.RepoDir(cache, "example.com/pad", "v0.1.0"))
	// pad came from the package cache; kit and words from their directories.
	if len(idx.Fetched) != 1 || !idx.Fetched["pad"] {
		t.Errorf("fetched packages are %v, want pad alone", idx.Fetched)
	}
}

// TestLoadManifest_DependencyEntries: each [dependencies] entry is held to
// its form, at its line, with a message that names it.
func TestLoadManifest_DependencyEntries(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{"a = { repo = \"example.com/a\", version = \"v1.0.0\" }\nb = { repo = \"example.com/a\", version = \"v1.1.0\" }\n",
			"nomi.toml:7: [dependencies] a and b both name example.com/a; keep one"},
		{"shop = { dir = \"../x\" }\n", "nomi.toml:6: [dependencies] shop is this package's own name; name the dependency another way"},
		{"a = { repo = \"example.com/a\", version = \"v1.2\" }\n", "nomi.toml:6: [dependencies] a: \"v1.2\" is not a version a tag names, such as v1.2.3"},
		{"a = { repo = \"https://example.com/a\", version = \"v1.0.0\" }\n", "nomi.toml:6: [dependencies] a: \"https://example.com/a\" is not a repository: write it with no scheme"},
		{"a = { repo = \"example.com/a\", subdir = \"../b\", version = \"v1.0.0\" }\n", "nomi.toml:6: [dependencies] a: \"../b\" is not a directory in a repository"},
		{"a = { repo = \"example.com/a\" }\n", "nomi.toml:6: [dependencies] a needs a `version`"},
		{"a = { version = \"v1.0.0\" }\n", "nomi.toml:6: [dependencies] a has a version but no `repo`"},
		{"a = {}\n", "nomi.toml:6: [dependencies] a needs `repo` and `version`, or `dir`"},
		{"Big = { dir = \"../x\" }\n", "nomi.toml:6: [dependencies] \"Big\" is not a dependency name"},
		{"a = { repo = \"example.com/a\", verison = \"v1.0.0\" }\n", "nomi.toml:6: [dependencies.a] has no key `verison`; did you mean `version`?"},
		{"a = { module = \"example.com/a\", version = \"v1.0.0\" }\n", "nomi.toml:6: [dependencies.a] has no key `module`"},
	} {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "nomi.toml"), "[package]\nname = \"shop\"\n# The shop.\n\n[dependencies]\n"+tc.body)

		_, err := LoadManifest(dir)
		if err == nil {
			t.Errorf("LoadManifest admits\n%s", tc.body)
			continue
		}
		if got := strings.TrimPrefix(err.Error(), dir+string(filepath.Separator)); !strings.HasPrefix(got, tc.want) {
			t.Errorf("for\n%s got  %s\nwant %s", tc.body, got, tc.want)
		}
	}
}
