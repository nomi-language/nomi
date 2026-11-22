package analysis

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

// TestBuildModuleIndex_ADirDependency: a `todo` project names a sibling
// module with a dir entry. The index holds it by the name todo's
// [dependencies] gives it, at its directory.
func TestBuildModuleIndex_ADirDependency(t *testing.T) {
	tmp := t.TempDir()
	todo := filepath.Join(tmp, "todo")
	stringkit := filepath.Join(tmp, "stringkit")
	writeFile(t, filepath.Join(todo, "nomi.toml"), "[module]\nname = \"todo\"\n\n[dependencies]\nstringkit = { dir = \"../stringkit\" }\n")
	writeFile(t, filepath.Join(stringkit, "nomi.toml"), "[module]\nname = \"stringkit\"\n")

	idx, err := BuildModuleIndex(todo)
	if err != nil {
		t.Fatalf("BuildModuleIndex: %v", err)
	}
	if len(idx.Roots) != 1 || idx.Names[""]["stringkit"] != "stringkit" {
		t.Fatalf("index = %#v, want stringkit alone", idx)
	}
	sameDirs(t, "stringkit's root", idx.Roots["stringkit"], stringkit)
	if self := idx.Names["stringkit"]["stringkit"]; self != "stringkit" {
		t.Errorf("stringkit's own name is %q in its names, want itself", self)
	}
}

// TestBuildModuleIndex_NoManifest: a directory without a nomi.toml is an
// empty index, no error, which keeps single-file `nomi run` valid.
func TestBuildModuleIndex_NoManifest(t *testing.T) {
	idx, err := BuildModuleIndex(t.TempDir())
	if err != nil {
		t.Fatalf("BuildModuleIndex on a directory without nomi.toml: %v", err)
	}
	if len(idx.Roots) != 0 || len(idx.Names[""]) != 0 {
		t.Fatalf("expected an empty index, got %#v", idx)
	}
}

// TestBuildModuleIndex_NotInTheModuleCache: a dependency the module cache
// does not hold is left out of the index, not an error, since the analysis
// never downloads; an import of it reports it missing
// (TestShortNameImport_FromTheModuleCacheAndMissingFromIt).
func TestBuildModuleIndex_NotInTheModuleCache(t *testing.T) {
	t.Setenv("GOMODCACHE", t.TempDir())
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "nomi.toml"),
		"[module]\nname = \"todo\"\n\n[dependencies]\nremote = { module = \"example.com/remote\", version = \"v1.2.3\" }\n")
	idx, err := BuildModuleIndex(tmp)
	if err != nil {
		t.Fatalf("BuildModuleIndex: %v", err)
	}
	if len(idx.Roots) != 0 {
		t.Fatalf("expected an empty index, got %#v", idx)
	}
}

// TestBuildModuleIndex_MissingDir: a dir entry naming a directory that does
// not exist is an error that names the directory.
func TestBuildModuleIndex_MissingDir(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "nomi.toml"),
		"[module]\nname = \"todo\"\n\n[dependencies]\nstringkit = { dir = \"../does-not-exist\" }\n")
	_, err := BuildModuleIndex(tmp)
	if err == nil {
		t.Fatalf("BuildModuleIndex admits a dir entry whose directory does not exist")
	}
	if want := "[dependencies] stringkit: directory ../does-not-exist does not exist"; !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q, want one with %q", err, want)
	}
}

// TestBuildModuleIndex_NotANomiModule: a dependency whose directory holds
// no nomi.toml is not indexed.
func TestBuildModuleIndex_NotANomiModule(t *testing.T) {
	tmp := t.TempDir()
	todo := filepath.Join(tmp, "todo")
	writeFile(t, filepath.Join(todo, "nomi.toml"), "[module]\nname = \"todo\"\n\n[dependencies]\ngokit = { dir = \"../gokit\" }\n")
	writeFile(t, filepath.Join(tmp, "gokit", "kit.go"), "package gokit\n")
	idx, err := BuildModuleIndex(todo)
	if err != nil {
		t.Fatalf("BuildModuleIndex: %v", err)
	}
	if len(idx.Roots) != 0 || len(idx.Names[""]) != 0 {
		t.Fatalf("expected an empty index, got %#v", idx)
	}
}

// TestBuildModuleIndex_TwoModulesWithOneOwnName: two dependencies whose
// own nomi.toml give them the same [module].name are two modules, each
// under the name the project gives it. A module's own name is only the
// name `nomi add` gives it.
func TestBuildModuleIndex_TwoModulesWithOneOwnName(t *testing.T) {
	tmp := t.TempDir()
	todo := filepath.Join(tmp, "todo")
	writeFile(t, filepath.Join(todo, "nomi.toml"),
		"[module]\nname = \"todo\"\n\n[dependencies]\naudit = { dir = \"../lib-b\" }\nshared = { dir = \"../lib-a\" }\n")
	writeFile(t, filepath.Join(tmp, "lib-a", "nomi.toml"), "[module]\nname = \"shared\"\n")
	writeFile(t, filepath.Join(tmp, "lib-b", "nomi.toml"), "[module]\nname = \"shared\"\n")

	idx, err := BuildModuleIndex(todo)
	if err != nil {
		t.Fatalf("two dependencies with one own name: %v", err)
	}
	sameDirs(t, "shared", idx.Roots[idx.Names[""]["shared"]], filepath.Join(tmp, "lib-a"))
	sameDirs(t, "audit", idx.Roots[idx.Names[""]["audit"]], filepath.Join(tmp, "lib-b"))
	// Each module's own name names itself in its own files.
	if idx.Names["audit"]["shared"] != "audit" || idx.Names["shared"]["shared"] != "shared" {
		t.Errorf("each module's own name: %#v", idx.Names)
	}
}

// TestBuildModuleIndex_NamesArePerModule: a dependency's imports resolve
// through its own [dependencies], never the project's. app names kit's
// dependency `text` as `words`; kit names it `text`. A module only kit
// names takes kit's name for it in the program.
func TestBuildModuleIndex_NamesArePerModule(t *testing.T) {
	tmp := t.TempDir()
	app := filepath.Join(tmp, "app")
	writeFile(t, filepath.Join(app, "nomi.toml"), "[module]\nname = \"app\"\n\n[dependencies]\n"+
		"kit = { module = \"example.com/kit\", version = \"v0.1.0\", dir = \"../kit\" }\n"+
		"words = { module = \"example.com/text\", version = \"v0.1.0\", dir = \"../text\" }\n")
	writeFile(t, filepath.Join(tmp, "kit", "nomi.toml"), "[module]\nname = \"kit\"\npath = \"example.com/kit\"\n\n[dependencies]\n"+
		"pad = { module = \"example.com/pad\", version = \"v0.1.0\" }\n"+
		"text = { module = \"example.com/text\", version = \"v0.1.0\" }\n")
	writeFile(t, filepath.Join(tmp, "text", "nomi.toml"), "[module]\nname = \"text\"\npath = \"example.com/text\"\n")
	cache := t.TempDir()
	t.Setenv("GOMODCACHE", cache)
	writeFile(t, filepath.Join(cache, "example.com", "pad@v0.1.0", "nomi.toml"), "[module]\nname = \"pad\"\npath = \"example.com/pad\"\n")
	writeFile(t, filepath.Join(cache, "cache", "download", "example.com", "pad", "@v", "v0.1.0.ziphash"), "h1:x\n")

	idx, err := BuildModuleIndex(app)
	if err != nil {
		t.Fatalf("BuildModuleIndex: %v", err)
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
	sameDirs(t, "pad", idx.Roots["pad"], filepath.Join(cache, "example.com", "pad@v0.1.0"))
}

// TestLoadManifest_DependencyEntries: each [dependencies] entry is held to
// its form, at its line, with a message that names it.
func TestLoadManifest_DependencyEntries(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{"a = { module = \"example.com/a\", version = \"v1.0.0\" }\nb = { module = \"example.com/a\", version = \"v1.1.0\" }\n",
			"nomi.toml:7: [dependencies] a and b both name example.com/a; keep one"},
		{"shop = { dir = \"../x\" }\n", "nomi.toml:6: [dependencies] shop is this module's own name; name the dependency another way"},
		{"a = { module = \"example.com/a\", version = \"v1.2\" }\n", "nomi.toml:6: [dependencies] a: \"v1.2\" is not a full version such as v1.2.3"},
		{"a = { module = \"example.com/a\" }\n", "nomi.toml:6: [dependencies] a needs a `version`"},
		{"a = { version = \"v1.0.0\" }\n", "nomi.toml:6: [dependencies] a has a version but no `module`"},
		{"a = {}\n", "nomi.toml:6: [dependencies] a needs `module` and `version`, or `dir`"},
		{"Big = { dir = \"../x\" }\n", "nomi.toml:6: [dependencies] \"Big\" is not a dependency name"},
		{"a = { module = \"example.com/a\", verison = \"v1.0.0\" }\n", "nomi.toml:6: [dependencies.a] has no key `verison`; did you mean `version`?"},
	} {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "nomi.toml"), "[module]\nname = \"shop\"\npath = \"example.com/shop\"\n\n[dependencies]\n"+tc.body)
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
