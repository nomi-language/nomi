package analysis_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
)

// shortNameProject writes module app, whose go.mod requires module `greet`
// (example.com/greet), and answers app's root and its entry. depDir, when
// set, is greet's directory and app replaces greet with it; otherwise greet
// is read from the module cache.
func shortNameProject(t *testing.T, mainSrc, depDir string) (root, entry string) {
	t.Helper()
	root = filepath.Join(t.TempDir(), "app")
	goMod := "module example.com/app\n\ngo 1.22\n\nrequire example.com/greet v0.1.0\n"
	if depDir != "" {
		goMod += "\nreplace example.com/greet => " + depDir + "\n"
	}
	writeTestFile(t, filepath.Join(root, "go.mod"), goMod)
	writeTestFile(t, filepath.Join(root, "nomi.toml"), "[module]\nname = \"app\"\nentry_points = [\"main\"]\n")
	entry = filepath.Join(root, "main.nomi")
	writeTestFile(t, entry, mainSrc)
	return root, entry
}

// greetModule writes module greet's files into dir.
func greetModule(t *testing.T, dir string, withRootFile bool) {
	t.Helper()
	writeTestFile(t, filepath.Join(dir, "go.mod"), "module example.com/greet\n\ngo 1.22\n")
	writeTestFile(t, filepath.Join(dir, "nomi.toml"), "[module]\nname = \"greet\"\n")
	writeTestFile(t, filepath.Join(dir, "words.nomi"), "pub fn word() -> String {\n    \"hello\"\n}\n")
	if withRootFile {
		writeTestFile(t, filepath.Join(dir, "greet.nomi"),
			"import greet/words\n\npub fn hello(name: String) -> String {\n    \"${words.word()}, ${name}\"\n}\n")
	}
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
	lib := std.Load()
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
		"import {\n    greet\n    std/io\n}\n\nfn main() {\n    io.print(greet.hello(\"you\"))\n}\n",
		"import {\n    greet.hello\n    std/io\n}\n\nfn main() {\n    io.print(hello(\"you\"))\n}\n",
		"import {\n    greet.{self, hello}\n    std/io\n}\n\nfn main() {\n    io.print(greet.hello(hello(\"you\")))\n}\n",
		"import {\n    greet/greet\n    std/io\n}\n\nfn main() {\n    io.print(greet.hello(\"you\"))\n}\n",
		"import {\n    greet as g\n    greet/words\n    std/io\n}\n\nfn main() {\n    io.print(g.hello(words.word()))\n}\n",
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

// A module without a root file of its name is imported by path, as before:
// its short name alone is still a module, not a file.
func TestShortNameImport_AModuleWithoutARootFileIsNotAFile(t *testing.T) {
	dep := filepath.Join(t.TempDir(), "greet")
	greetModule(t, dep, false)
	root, entry := shortNameProject(t, "import greet\n\nfn main() {\n    _ = greet.word()\n}\n", dep)
	errs := projectErrors(t, root, entry)
	want := "1:8: `greet` is a module, not a file; an import names a file in it; import a file in it, such as `greet/words`"
	if len(errs) == 0 {
		t.Fatal("the front end admits `import greet` of a module with no greet.nomi")
	}
	if errs[0] != want {
		t.Errorf("got\n  %s\nwant\n  %s", strings.Join(errs, "\n  "), want)
	}
}

// A module in the module cache resolves as a local replace does, and one the
// cache lacks is an import error whose hint says to run `nomi fetch`. Nothing
// is downloaded: the analysis never fetches.
func TestShortNameImport_FromTheModuleCacheAndMissingFromIt(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "modcache")
	t.Setenv("GOMODCACHE", cache)
	t.Setenv("GOPROXY", "off")
	src := "import {\n    greet\n    std/io\n}\n\nfn main() {\n    io.print(greet.hello(\"you\"))\n}\n"

	root, entry := shortNameProject(t, src, "")
	errs := projectErrors(t, root, entry)
	if len(errs) == 0 {
		t.Fatal("the front end admits an import of a module the cache does not hold")
	}
	hint := "go.mod requires module example.com/greet v0.1.0, which is not in the module cache; run `nomi fetch` to download it"
	if !strings.Contains(errs[0], "no module `greet`") || !strings.Contains(errs[0], hint) {
		t.Errorf("the missing module's error:\n  %s\nwant one with the hint %q", strings.Join(errs, "\n  "), hint)
	}
	if _, err := os.Stat(cache); err == nil {
		t.Errorf("the analysis created the module cache %s", cache)
	}

	// Put greet in the cache as the go command (and nomi fetch) lays it out.
	greetModule(t, filepath.Join(cache, "example.com", "greet@v0.1.0"), true)
	writeTestFile(t, filepath.Join(cache, "cache", "download", "example.com", "greet", "@v", "v0.1.0.ziphash"), "h1:x\n")
	if errs := projectErrors(t, root, entry); len(errs) != 0 {
		t.Errorf("with greet in the module cache:\n  %s", strings.Join(errs, "\n  "))
	}
}
