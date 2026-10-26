package virtualproject

import (
	"os"
	"path/filepath"
	"testing"
)

// A block with a go.mod is its files: the entry is the root module's file
// with `fn main`, never a file of the module under macros/, and Write lays
// the files out as the block marks them.
func TestFromMarkedSourceKeepsAProjectOfModulesAsItsFiles(t *testing.T) {
	src := "// FILE: tables.nomi\nimport csvdata\n\n// FILE: macros/csvdata/main.nomi\nfn main() {}\n\n" +
		"// FILE: macros/csvdata/nomi.toml\n[module]\nname = \"csvdata\"\n\n// FILE: main.nomi\nfn main() {}\n\n" +
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
	data, err := os.ReadFile(filepath.Join(root, "macros", "csvdata", "nomi.toml"))
	if err != nil || string(data) != "[module]\nname = \"csvdata\"\n" {
		t.Errorf("macros/csvdata/nomi.toml holds %q (%v)", data, err)
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
