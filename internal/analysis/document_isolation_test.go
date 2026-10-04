package analysis_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/std"
)

// TestDocumentManager_BuildLeavesOtherDocumentsNodesAlone: building one
// document never writes to another open document's installed nodes, which
// language-server requests read without the build lock. The builder records
// an impl item's interface on its FuncDef (ImplIface) and the checker an
// enum on each dot variant (ResolvedEnum); a build that loaded an open
// sibling's installed nodes wrote both into them. decls.nomi has
// impl-only imports of hop.nomi, so its build also type-checks hop for
// CheckImplImports.
func TestDocumentManager_BuildLeavesOtherDocumentsNodesAlone(t *testing.T) {
	fixture := filepath.Join("..", "irbuild", "testdata", "sibimpl_cycle")
	dir := t.TempDir()
	files := map[string]string{"nomi.toml": "[module]\nname = \"app\"\nentry_points = [\"main\"]\n"}
	for _, name := range []string{"main.nomi", "decls.nomi", "back.nomi", "hop.nomi"} {
		src, err := os.ReadFile(filepath.Join(fixture, name))
		if err != nil {
			t.Fatal(err)
		}
		files[name] = string(src)
	}
	files["hop.nomi"] += "\nfn order(): Ordering {\n    .Less\n}\n"
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lib := std.Load()
	dm := analysis.NewDocumentManager()
	dm.SetStdlib(lib.Primitives, lib.Modules, lib.Files)
	dm.SetWorkspaceRoot(dir)
	hop := "file://" + filepath.Join(dir, "hop.nomi")
	decls := "file://" + filepath.Join(dir, "decls.nomi")
	dm.Open(hop, files["hop.nomi"])
	dm.Open(decls, files["decls.nomi"])
	if fa := dm.Snapshot(decls).Analysis; fa == nil || len(fa.ImplImports) == 0 {
		t.Fatal("decls.nomi has no pending impl-only import")
	}

	var impl *ast.FuncDef
	var dot *ast.DotVariant
	var find func(n ast.Node)
	find = func(n ast.Node) {
		switch n := n.(type) {
		case *ast.ImplBlock:
			for _, it := range n.Items {
				if fd, ok := it.(*ast.FuncDef); ok {
					impl = fd
				}
			}
		case *ast.FuncDef:
			if n.Body != nil {
				for _, st := range n.Body.Stmts {
					find(st)
				}
			}
		case *ast.ExprStmt:
			find(n.Expr)
		case *ast.DotVariant:
			dot = n
		}
	}
	for _, n := range dm.Snapshot(hop).Nodes {
		find(n)
	}
	if impl == nil || impl.ImplIface == nil {
		t.Fatal("hop.nomi's impl function has no recorded interface")
	}
	// Clear what hop's own analysis recorded; another document's build must
	// not record it again.
	impl.ImplIface = nil
	if dot != nil {
		dot.ResolvedEnum = ""
	}
	dm.Update(decls, files["decls.nomi"]+"\n")
	if impl.ImplIface != nil {
		t.Error("building decls.nomi wrote ImplIface into hop.nomi's installed nodes")
	}
	if dot != nil && dot.ResolvedEnum != "" {
		t.Error("building decls.nomi wrote ResolvedEnum into hop.nomi's installed nodes")
	}
}
