package virtualproject

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A block with a go.mod is its files: the entry is the root module's file
// with `fn main`, never a file of the module under csvdata/, and Write lays
// the files out as the block marks them.
func TestFromMarkedSourceKeepsAProjectOfModulesAsItsFiles(t *testing.T) {
	src := "// FILE: tables.nomi\nimport csvdata\n\n// FILE: csvdata/main.nomi\nfn main() {}\n\n" +
		"// FILE: csvdata/nomi.toml\n[module]\nname = \"csvdata\"\n\n// FILE: main.nomi\nfn main() {}\n\n" +
		"// FILE: data/prices.csv\nsku,cents\n\n\n// FILE: go.mod\nmodule shop\n"
	p, err := FromMarkedSource(src)
	if err != nil {
		t.Fatal(err)
	}
	if p.EntryName != "main.nomi" || p.First != "tables.nomi" || p.VirtualFiles != nil {
		t.Fatalf("entry %q, first %q, virtual files %v", p.EntryName, p.First, p.VirtualFiles)
	}
	if got := p.DataFiles["data/prices.csv"]; got != "sku,cents\n" {
		t.Errorf("the data file is %q", got)
	}
	root, err := p.Write(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "csvdata", "nomi.toml"))
	if err != nil || string(data) != "[module]\nname = \"csvdata\"\n" {
		t.Errorf("csvdata/nomi.toml holds %q (%v)", data, err)
	}
	// With no `fn main`, the entry is the first root file the block marks,
	// the tab it opens on, not the first by name.
	tests, err := FromMarkedSource("// FILE: shop_test.nomi\ntest \"x\" {}\n\n// FILE: queries.nomi\n\n// FILE: go.mod\nmodule shop\n")
	if err != nil || tests.EntryName != "shop_test.nomi" {
		t.Errorf("a block of tests enters at %q (%v)", tests.EntryName, err)
	}
	if _, err := FromMarkedSource("// FILE: go.mod\nmodule a\n// FILE: ../x.nomi\n"); err == nil {
		t.Error("a section outside the project is accepted")
	}
}

func TestFromMarkedSourceSplitsFilesAndManifest(t *testing.T) {
	project, err := FromMarkedSource(`// FILE: math.nomi
pub fn double(n: Int) -> Int {
  n * 2
}

// FILE: main.nomi
import math

fn main() {
  math.double(7)
}

// FILE: nomi.toml
[module]
name = "demo"
entry_points = ["main"]
`)
	if err != nil {
		t.Fatalf("FromMarkedSource: %v", err)
	}
	if project.EntryName != "main" {
		t.Fatalf("EntryName = %q, want main", project.EntryName)
	}
	if project.Manifest == nil || project.Manifest.Name != "demo" {
		t.Fatalf("Manifest = %#v, want demo package", project.Manifest)
	}
	if _, ok := project.VirtualFiles["math"]; !ok {
		t.Fatalf("VirtualFiles missing math: %#v", project.VirtualFiles)
	}
}

func TestFromSourcesNormalizesEntryAndFileNames(t *testing.T) {
	project, err := FromSources("./main.nomi", map[string]string{
		"main.nomi": "fn main() {}",
		"api.nomi":  "pub fn ping() -> Int { 1 }",
	}, nil)
	if err != nil {
		t.Fatalf("FromSources: %v", err)
	}
	if project.EntryName != "main" {
		t.Fatalf("EntryName = %q, want main", project.EntryName)
	}
	if project.EntrySource != "fn main() {}" {
		t.Fatalf("EntrySource = %q", project.EntrySource)
	}
	if _, ok := project.VirtualFiles["api"]; !ok {
		t.Fatalf("VirtualFiles missing normalized api key: %#v", project.VirtualFiles)
	}
}

// A block with no `fn main` that shows a solution before its test file is
// entered at the test file, so a run runs its tests.
func TestFromMarkedSourceEntersATestFileWhenThereIsNoMain(t *testing.T) {
	project, err := FromMarkedSource(`// FILE: two_fer.nomi
pub fn two_fer(name: String = "you") -> String {
    "One for ${name}, one for me."
}

// FILE: two_fer_test.nomi
import two_fer.two_fer

test "no name given" {
    assert two_fer() == "One for you, one for me."
}
`)
	if err != nil {
		t.Fatalf("FromMarkedSource: %v", err)
	}
	if project.EntryName != "two_fer_test" {
		t.Fatalf("EntryName = %q, want two_fer_test", project.EntryName)
	}
	if project.First != "two_fer.nomi" {
		t.Fatalf("First = %q, want two_fer.nomi", project.First)
	}
	if _, ok := project.VirtualFiles["two_fer"]; !ok {
		t.Fatalf("VirtualFiles missing two_fer: %#v", project.VirtualFiles)
	}
}

// A block's message catalogs are virtual files by their path, `.toml`
// included; any other kind of file is a data file the program reads at run
// time.
func TestFromMarkedSourceKeepsCatalogsByPath(t *testing.T) {
	src := "// FILE: main.nomi\nfn main() {}\n\n// FILE: locales/pl/cart.toml\n[cart_count]\n\n// FILE: nomi.toml\n[module]\nname = \"shop\"\n\n[i18n]\nlocales = [\"en\", \"pl\"]\n"
	p, err := FromMarkedSource(src)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.VirtualFiles["locales/pl/cart.toml"]; got != "[cart_count]\n\n" {
		t.Errorf("the catalog's virtual file is %q", got)
	}
	data, err := FromMarkedSource("// FILE: data/notes.txt\nhi\n\n// FILE: main.nomi\nfn main() {}\n// FILE: empty.txt\n")
	if err != nil {
		t.Fatal(err)
	}
	if got, empty := data.DataFiles["data/notes.txt"], data.DataFiles["empty.txt"]; got != "hi\n" || empty != "" || len(data.VirtualFiles) != 0 {
		t.Errorf("a .txt file is the data file %q, with virtual files %v", got, data.VirtualFiles)
	}
}

// A block with no go.mod at its root and one in two top-level directories
// holds sibling projects. Its project is its first file's directory, even
// when another project holds the only `fn main`, and its entry and test
// files are that project's alone.
func TestFromMarkedSourcePicksTheFirstFilesProjectOfSiblings(t *testing.T) {
	src := "// FILE: app/app_test.nomi\nimport shared\n\ntest \"t\" {\n    assert shared.one() == 1\n}\n\n" +
		"// FILE: app/more_test.nomi\ntest \"u\" {\n    assert True\n}\n\n" +
		"// FILE: app/go.mod\nmodule app\n\n// FILE: app/nomi.toml\n[module]\nname = \"app\"\n\n" +
		"// FILE: shared/shared.nomi\npub fn one() -> Int {\n    1\n}\n\n" +
		"// FILE: shared/tools/gen.nomi\nfn main() {}\n\n" +
		"// FILE: shared/shared_test.nomi\ntest \"v\" {\n    assert True\n}\n\n" +
		"// FILE: shared/go.mod\nmodule shared\n\n// FILE: shared/nomi.toml\n[module]\nname = \"shared\"\n"
	p, err := FromMarkedSource(src)
	if err != nil {
		t.Fatal(err)
	}
	if p.Dir != "app" || p.EntryName != "app/app_test.nomi" {
		t.Fatalf("Dir %q, EntryName %q; want app and app/app_test.nomi", p.Dir, p.EntryName)
	}
	if got := strings.Join(p.TestFiles, " "); got != "app/more_test.nomi" {
		t.Errorf("TestFiles = %q, want app/more_test.nomi", got)
	}
	if _, ok := p.DataFiles["app/nomi.toml"]; ok {
		t.Error("the project's nomi.toml is a data file")
	}

	// One go.mod under the root is a module of the root's, not a sibling.
	nested, err := FromMarkedSource("// FILE: main.nomi\nfn main() {}\n\n// FILE: lib/go.mod\nmodule lib\n")
	if err != nil {
		t.Fatal(err)
	}
	if nested.Dir != "" {
		t.Errorf("a block with one module directory has the project %q", nested.Dir)
	}
}
