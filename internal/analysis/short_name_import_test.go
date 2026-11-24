package analysis_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/modcache"
	"github.com/nomi-language/nomi/internal/modfetch/modfetchtest"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
)

// shortNameProject writes module app, whose nomi.toml names module
// example.com/greet `greet`, and answers app's root and its entry. depDir,
// when set, is greet's directory, which the entry names; otherwise greet is
// read from the module cache.
func shortNameProject(t *testing.T, mainSrc, depDir string) (root, entry string) {
	t.Helper()
	return namedProject(t, mainSrc, "greet", depDir)
}

// namedProject is shortNameProject with app naming greet name.
func namedProject(t *testing.T, mainSrc, name, depDir string) (root, entry string) {
	t.Helper()
	root = filepath.Join(t.TempDir(), "app")
	dep := name + " = { repo = \"example.com/greet\", version = \"v0.1.0\""
	if depDir != "" {
		dep += ", dir = " + fmt.Sprintf("%q", depDir)
	}
	writeTestFile(t, filepath.Join(root, "nomi.toml"), "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[dependencies]\n"+dep+" }\n")
	entry = filepath.Join(root, "main.nomi")
	writeTestFile(t, entry, mainSrc)
	return root, entry
}

// greetModule writes module greet's files into dir.
func greetModule(t *testing.T, dir string, withRootFile bool) {
	t.Helper()
	for name, text := range greetFiles(withRootFile) {
		writeTestFile(t, filepath.Join(dir, name), text)
	}
}

// greetFiles is module greet's files.
func greetFiles(withRootFile bool) map[string]string {
	files := map[string]string{
		"nomi.toml":  "[module]\nname = \"greet\"\n",
		"words.nomi": "pub fn word() -> String\n    \"hello\"\nend\n",
	}
	if withRootFile {
		files["greet.nomi"] = "import greet::words\n\npub fn hello(name: String) -> String\n    \"${words.word()}, ${name}\"\nend\n"
	}
	return files
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func projectErrors(t *testing.T, root, entry string) []string {
	t.Helper()
	data, err := os.ReadFile(entry)
	if err != nil {
		t.Fatal(err)
	}
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(string(data)))
	lib := loadStd()
	fa := analysis.BuildProject(nodes, lib.Primitives, lib.Modules, lib.Files, root, std.MakeLoader())
	var got []string
	for _, e := range fa.TypeErrors {
		s := fmt.Sprintf("%d:%d: %s", e.Line, e.Col, e.Message)
		if len(e.Hints) > 0 {
			s += "; " + strings.Join(e.Hints, "; ")
		}
		got = append(got, s)
	}
	return got
}

// `import greet` from another module names greet's root file greet.nomi
// (spec §14), in every import form, and dedups with `greet/greet`.
func TestShortNameImport_NamesTheModulesRootFile(t *testing.T) {
	dep := filepath.Join(t.TempDir(), "greet")
	greetModule(t, dep, true)
	for _, src := range []string{
		"import\n    greet\n    std::io\nend\n\nfn main()\n    io.print(greet.hello(\"you\"))\nend\n",
		"import\n    greet.hello\n    std::io\nend\n\nfn main()\n    io.print(hello(\"you\"))\nend\n",
		"import\n    greet.{self, hello}\n    std::io\nend\n\nfn main()\n    io.print(greet.hello(hello(\"you\")))\nend\n",
		"import\n    greet::greet\n    std::io\nend\n\nfn main()\n    io.print(greet.hello(\"you\"))\nend\n",
		"import\n    greet as g\n    greet::words\n    std::io\nend\n\nfn main()\n    io.print(g.hello(words.word()))\nend\n",
	} {
		root, entry := shortNameProject(t, src, dep)
		if errs := projectErrors(t, root, entry); len(errs) != 0 {
			t.Errorf("%q:\n  %s", src, strings.Join(errs, "\n  "))
		}
		if path, ok := analysis.ImportFile(root, []string{"greet"}); !ok || path != filepath.Join(dep, "greet.nomi") {
			t.Errorf("ImportFile(greet) = %q, %v; want %s", path, ok, filepath.Join(dep, "greet.nomi"))
		}
	}
}

// A module without a root file of its name is a directory under its short
// name alone: `import greet` binds its root, and `greet::words` reaches
// words.nomi there.
func TestShortNameImport_AModuleWithoutARootFileIsItsRootDirectory(t *testing.T) {
	dep := filepath.Join(t.TempDir(), "greet")
	greetModule(t, dep, false)
	root, entry := shortNameProject(t, "import greet\n\nfn main()\n    _ = greet::words.word()\nend\n", dep)
	if errs := projectErrors(t, root, entry); len(errs) != 0 {
		t.Errorf("the front end rejects `import greet` of a module with no greet.nomi:\n  %s", strings.Join(errs, "\n  "))
	}
	root, entry = shortNameProject(t, "import greet\n\nfn main()\n    _ = greet::nosuch.word()\nend\n", dep)
	errs := projectErrors(t, root, entry)
	want := "4:9: `greet::nosuch` names no file: greet has no nosuch.nomi; greet holds `greet::words`"
	if len(errs) == 0 {
		t.Fatal("the front end admits a path to a file module greet does not hold")
	}
	if errs[0] != want {
		t.Errorf("got\n  %s\nwant\n  %s", strings.Join(errs, "\n  "), want)
	}
}

// A module in the module cache resolves as a local replace does, and one the
// cache lacks is an import error whose hint says to run `nomi fetch`. Nothing
// is downloaded: the analysis never fetches.
func TestShortNameImport_FromTheModuleCacheAndMissingFromIt(t *testing.T) {
	cache := modfetchtest.CacheDir(t)
	t.Setenv(modcache.CacheEnv, cache)
	src := "import\n    greet\n    std::io\nend\n\nfn main()\n    io.print(greet.hello(\"you\"))\nend\n"

	root, entry := shortNameProject(t, src, "")
	errs := projectErrors(t, root, entry)
	if len(errs) == 0 {
		t.Fatal("the front end admits an import of a module the cache does not hold")
	}
	hint := "the project needs module example.com/greet v0.1.0, which is not in the module cache; run `nomi fetch` to download it"
	if !strings.Contains(errs[0], "no module `greet`") || !strings.Contains(errs[0], hint) {
		t.Errorf("the missing module's error:\n  %s\nwant one with the hint %q", strings.Join(errs, "\n  "), hint)
	}
	if _, err := os.Stat(cache); err == nil {
		t.Errorf("the analysis created the module cache %s", cache)
	}

	// Put greet in the cache as nomi fetch lays it out.
	modfetchtest.CacheRepo(t, cache, "example.com/greet", "v0.1.0", greetFiles(true))
	if errs := projectErrors(t, root, entry); len(errs) != 0 {
		t.Errorf("with greet in the module cache:\n  %s", strings.Join(errs, "\n  "))
	}
}

// The project's name for a dependency is the one its imports use; the
// dependency's own [module].name is not a name in the project. greet's own
// files still import themselves as greet.
func TestShortNameImport_TheProjectsNameForADependency(t *testing.T) {
	dep := filepath.Join(t.TempDir(), "greet")
	greetModule(t, dep, true)
	src := "import\n    hi::greet\n    std::io\nend\n\nfn main()\n    io.print(greet.hello(\"you\"))\nend\n"
	root, entry := namedProject(t, src, "hi", dep)
	if errs := projectErrors(t, root, entry); len(errs) != 0 {
		t.Errorf("an import through the project's name for greet:\n  %s", strings.Join(errs, "\n  "))
	}
	src = "import\n    greet::words\nend\n\nfn main()\n    _ = words.word()\nend\n"
	root, entry = namedProject(t, src, "hi", dep)
	errs := projectErrors(t, root, entry)
	if len(errs) == 0 {
		t.Fatal("the front end admits an import through greet's own name, which the project does not give it")
	}
	if !strings.Contains(errs[0], "no module `greet::words`") {
		t.Errorf("an import through greet's own name:\n  %s", strings.Join(errs, "\n  "))
	}
}

// A dependency's imports resolve through its own [dependencies]: greet's
// file imports `fmt`, the name greet gives a module the project names
// `format`, and the project's `fmt`, another module, is not what it reaches.
func TestShortNameImport_ADependencysImportsAreItsOwn(t *testing.T) {
	tmp := t.TempDir()
	greet, words, other := filepath.Join(tmp, "greet"), filepath.Join(tmp, "words"), filepath.Join(tmp, "other")
	writeTestFile(t, filepath.Join(greet, "nomi.toml"), "[module]\nname = \"greet\"\n\n[dependencies]\n"+
		"fmt = { repo = \"example.com/words\", version = \"v0.1.0\", dir = \"../words\" }\n")
	writeTestFile(t, filepath.Join(greet, "greet.nomi"), "import fmt::words\n\npub fn hello() -> String\n    words.word()\nend\n")
	writeTestFile(t, filepath.Join(words, "nomi.toml"), "[module]\nname = \"fmt\"\n")
	writeTestFile(t, filepath.Join(words, "words.nomi"), "pub fn word() -> String\n    \"hello\"\nend\n")
	writeTestFile(t, filepath.Join(other, "nomi.toml"), "[module]\nname = \"fmt\"\n")
	writeTestFile(t, filepath.Join(other, "words.nomi"), "pub fn other() -> Int\n    1\nend\n")
	root := filepath.Join(tmp, "app")
	writeTestFile(t, filepath.Join(root, "nomi.toml"), "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[dependencies]\n"+
		"fmt = { dir = \"../other\" }\n"+
		"format = { repo = \"example.com/words\", version = \"v0.1.0\", dir = \"../words\" }\n"+
		"greet = { repo = \"example.com/greet\", version = \"v0.1.0\", dir = \"../greet\" }\n")

	entry := filepath.Join(root, "main.nomi")
	writeTestFile(t, entry, "import\n    greet\n    fmt::words\n    std::io\nend\n\nfn main()\n    io.print(greet.hello())\n    _ = words.other()\nend\n")
	if errs := projectErrors(t, root, entry); len(errs) != 0 {
		t.Errorf("each module's imports through its own names:\n  %s", strings.Join(errs, "\n  "))
	}
}
