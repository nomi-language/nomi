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
// project's graph, and the replace in the dependency's own go.mod, of a
// directory that does not exist, is not read, as Go reads a replace only in
// the main module. A root the project does not need keeps its own graph.
func TestWalk_ADependencyRootTakesTheMainModulesGraph(t *testing.T) {
	tmp := t.TempDir()
	app := filepath.Join(tmp, "app")
	dep := filepath.Join(tmp, "dep")
	other := filepath.Join(tmp, "other")
	writeFile(t, filepath.Join(app, "go.mod"),
		"module example.com/app\n\ngo 1.27.0\n\nrequire (\n\texample.com/dep v0.1.0\n\texample.com/sib v0.2.0\n)\n\n"+
			"replace example.com/dep => ../dep\n\nreplace example.com/sib => ../sib\n")
	writeFile(t, filepath.Join(app, "nomi.toml"), "[module]\nname = \"app\"\n")
	writeFile(t, filepath.Join(dep, "go.mod"),
		"module example.com/dep\n\ngo 1.27.0\n\nrequire example.com/sib v0.1.0\n\nreplace example.com/sib => ../missing\n")
	writeFile(t, filepath.Join(dep, "nomi.toml"), "[module]\nname = \"dep\"\n")
	writeFile(t, filepath.Join(tmp, "sib", "go.mod"), "module example.com/sib\n\ngo 1.27.0\n")
	writeFile(t, filepath.Join(tmp, "sib", "nomi.toml"), "[module]\nname = \"sib\"\n")
	writeFile(t, filepath.Join(other, "go.mod"),
		"module example.com/other\n\ngo 1.27.0\n\nrequire example.com/sib v0.1.0\n\nreplace example.com/sib => ../missing\n")

	if _, err := Walk(dep, cacheOnly); err == nil || !strings.Contains(err.Error(), "replace target for \"example.com/sib\" does not exist") {
		t.Fatalf("with no main module, the dependency's own go.mod is read; got %v", err)
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
	if !ok || sib.Version.Version != "v0.2.0" || sib.Dir != filepath.Join(tmp, "sib") {
		t.Fatalf("example.com/sib is %+v, want the main module's v0.2.0 at %s", sib, filepath.Join(tmp, "sib"))
	}
	if _, err := Walk(other, cacheOnly); err == nil {
		t.Fatalf("a root the main module does not need keeps its own go.mod, whose replace names no directory")
	}
}
