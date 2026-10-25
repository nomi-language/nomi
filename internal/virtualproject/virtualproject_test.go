package virtualproject

import "testing"

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
// included; any other kind of file is refused.
func TestFromMarkedSourceKeepsCatalogsByPath(t *testing.T) {
	src := "// FILE: main.nomi\nfn main() {}\n\n// FILE: locales/pl/cart.toml\n[cart_count]\n\n// FILE: nomi.toml\n[module]\nname = \"shop\"\n\n[i18n]\nlocales = [\"en\", \"pl\"]\n"
	p, err := FromMarkedSource(src)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.VirtualFiles["locales/pl/cart.toml"]; got != "[cart_count]\n\n" {
		t.Errorf("the catalog's virtual file is %q", got)
	}
	if _, err := FromMarkedSource("// FILE: main.nomi\nfn main() {}\n// FILE: notes.txt\nhi\n"); err == nil {
		t.Error("a .txt file was accepted")
	}
}
