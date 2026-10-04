package analysis_test

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/std"
	"os"
	"path/filepath"
	"testing"
)

// A std module's own imports of prelude types (std/calendar imports Int,
// String and Result) bring them into project discovery as well as the loaded
// prelude. Boot signatures and method arguments must use the project
// declaration.
func TestPreludeLifecycleProject(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.nomi")
	src := `import std/calendar.Date
 struct App { context: Context
 count: Int }
 fn boot(startup: Startup): App {
  App{context: Context.root(), count: Map.size(startup.env)}
 }
 fn main() {
  _ = Date.new(2024, 1, 2)
  _ = Int.to_string(App.count)
  _ = Context.deadline(App.context)
 }
 `
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	lib := std.Load()
	dm := analysis.NewDocumentManager()
	dm.SetStdlib(lib.Primitives, lib.Modules, lib.Files)
	dm.SetWorkspaceRoot(root)
	doc := dm.Open("file://"+path, src)
	if doc.Analysis == nil {
		t.Fatal("no project analysis")
	}
	expectNoErrorsT(t, doc.Analysis.TypeErrors)
}
