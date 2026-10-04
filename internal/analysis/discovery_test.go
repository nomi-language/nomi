package analysis

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

func TestDiscoverProject_FindsTransitiveImports(t *testing.T) {
	tmp := t.TempDir()
	mustWrite := func(rel, content string) string {
		full := filepath.Join(tmp, rel)
		_ = os.MkdirAll(filepath.Dir(full), 0755)
		_ = os.WriteFile(full, []byte(content), 0644)
		return full
	}

	mainPath := mustWrite("main.nomi", "import a\n\nfn main() { Unit }\n")
	mustWrite("a.nomi", "import b\n\npub fn run(): Int { 1 }\n")
	mustWrite("b.nomi", "pub fn helper(): Int { 2 }\n")

	entryNodes := parseFileForDiscoveryTest(t, mainPath)

	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		filePath := filepath.Join(projectRoot, filepath.Join(modulePath...)) + ".nomi"
		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, err
		}
		tokens := lexer.Lex(string(data))
		nodes, _ := parser.ParseWithRecovery(tokens)
		return nodes, nil
	}

	project, err := DiscoverProject(entryNodes, tmp, loader)
	if err != nil {
		t.Fatal(err)
	}

	for _, mod := range []string{"a", "b"} {
		if _, ok := project.Files[mod]; !ok {
			t.Errorf("expected discovered file %q, not found in %v", mod, projectKeys(project))
		}
	}
}

func TestDiscoverProject_LoadsManifest(t *testing.T) {
	tmp := t.TempDir()
	mustWrite := func(rel, content string) string {
		full := filepath.Join(tmp, rel)
		_ = os.MkdirAll(filepath.Dir(full), 0755)
		_ = os.WriteFile(full, []byte(content), 0644)
		return full
	}

	mustWrite("nomi.toml", `[module]
name = "todo"
entry_points = ["main"]
`)
	mainPath := mustWrite("main.nomi", "fn main() { Unit }\n")

	entryNodes := parseFileForDiscoveryTest(t, mainPath)
	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		filePath := filepath.Join(projectRoot, filepath.Join(modulePath...)) + ".nomi"
		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, err
		}
		tokens := lexer.Lex(string(data))
		nodes, _ := parser.ParseWithRecovery(tokens)
		return nodes, nil
	}

	project, err := DiscoverProject(entryNodes, tmp, loader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if project.Manifest == nil {
		t.Fatal("expected Manifest to be populated when nomi.toml is present")
	}
	if project.Manifest.Name != "todo" {
		t.Errorf("Manifest.Name: got %q, want %q", project.Manifest.Name, "todo")
	}
	if got, want := project.Manifest.EntryPoints, []string{"main"}; len(got) != len(want) || got[0] != want[0] {
		t.Errorf("Manifest.EntryPoints: got %v, want %v", got, want)
	}
}

func TestDiscoverProject_NoManifestStaysNil(t *testing.T) {
	tmp := t.TempDir()
	mustWrite := func(rel, content string) string {
		full := filepath.Join(tmp, rel)
		_ = os.MkdirAll(filepath.Dir(full), 0755)
		_ = os.WriteFile(full, []byte(content), 0644)
		return full
	}

	mainPath := mustWrite("main.nomi", "fn main() { Unit }\n")
	entryNodes := parseFileForDiscoveryTest(t, mainPath)
	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		filePath := filepath.Join(projectRoot, filepath.Join(modulePath...)) + ".nomi"
		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, err
		}
		tokens := lexer.Lex(string(data))
		nodes, _ := parser.ParseWithRecovery(tokens)
		return nodes, nil
	}

	project, err := DiscoverProject(entryNodes, tmp, loader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if project.Manifest != nil {
		t.Errorf("expected Manifest to be nil when no nomi.toml present, got %+v", project.Manifest)
	}
}

func TestDiscoverProject_MalformedManifestReturnsError(t *testing.T) {
	tmp := t.TempDir()
	mustWrite := func(rel, content string) string {
		full := filepath.Join(tmp, rel)
		_ = os.MkdirAll(filepath.Dir(full), 0755)
		_ = os.WriteFile(full, []byte(content), 0644)
		return full
	}

	mustWrite("nomi.toml", "[module\nname = \"todo\"\n")
	mainPath := mustWrite("main.nomi", "fn main() { Unit }\n")

	entryNodes := parseFileForDiscoveryTest(t, mainPath)
	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		filePath := filepath.Join(projectRoot, filepath.Join(modulePath...)) + ".nomi"
		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, err
		}
		tokens := lexer.Lex(string(data))
		nodes, _ := parser.ParseWithRecovery(tokens)
		return nodes, nil
	}

	_, err := DiscoverProject(entryNodes, tmp, loader)
	if err == nil {
		t.Fatal("expected error for malformed nomi.toml, got nil")
	}
}

func TestDiscoverProject_HandlesCycle(t *testing.T) {
	tmp := t.TempDir()
	mustWrite := func(rel, content string) string {
		full := filepath.Join(tmp, rel)
		_ = os.MkdirAll(filepath.Dir(full), 0755)
		_ = os.WriteFile(full, []byte(content), 0644)
		return full
	}

	mainPath := mustWrite("main.nomi", "import a.a\n\nfn main() { Unit }\n")
	mustWrite("a.nomi", "import b.b\n\npub struct A { partner: Maybe<Int> }\n")
	mustWrite("b.nomi", "import a.a\n\npub struct B { partner: Maybe<Int> }\n")

	entryNodes := parseFileForDiscoveryTest(t, mainPath)
	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		filePath := filepath.Join(projectRoot, filepath.Join(modulePath...)) + ".nomi"
		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, err
		}
		tokens := lexer.Lex(string(data))
		nodes, _ := parser.ParseWithRecovery(tokens)
		return nodes, nil
	}

	project, err := DiscoverProject(entryNodes, tmp, loader)
	if err != nil {
		t.Fatalf("discovery should not fail on cycle: %v", err)
	}
	if _, ok := project.Files["a"]; !ok {
		t.Error("a not discovered")
	}
	if _, ok := project.Files["b"]; !ok {
		t.Error("b not discovered")
	}
}

func parseFileForDiscoveryTest(t *testing.T, path string) []ast.Node {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tokens := lexer.Lex(string(data))
	nodes, _ := parser.ParseWithRecovery(tokens)
	return nodes
}

func projectKeys(p *Project) []string {
	out := make([]string, 0, len(p.Files))
	for k := range p.Files {
		out = append(out, k)
	}
	return out
}
