package analysis

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// A binary with no stdlib source tree on disk (built with -trimpath and
// installed from a release) has no stdlibPath. It must still get the prelude
// imports, read through the loader, and the same ones the tree gives: without
// them the prelude was only the user file's parent scope, and an enum variant
// named `Debug` shadowed the interface ("impl block: 'Debug' is not an
// interface" from `nomi build` with an installed release).
func TestPreludeImports_WithoutTheSourceTreeReadsThroughTheLoader(t *testing.T) {
	stdRoot, err := StdlibPath()
	if err != nil {
		t.Fatal(err)
	}
	fromTree, err := preludeImports(stdRoot, nil)
	if err != nil || len(fromTree) == 0 {
		t.Fatalf("the source tree's prelude gives %d imports (err %v)", len(fromTree), err)
	}

	var asked [][]string
	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		asked = append(asked, append([]string{projectRoot}, modulePath...))
		data, err := os.ReadFile(filepath.Join(stdRoot, filepath.Join(modulePath[1:]...)) + ".nomi")
		if err != nil {
			return nil, err
		}
		nodes, _ := parser.ParseWithRecovery(lexer.Lex(string(data)))
		return nodes, nil
	}
	preludeImportsCacheMu.Lock()
	delete(preludeImportsCache, "")
	preludeImportsCacheMu.Unlock()
	t.Cleanup(func() {
		preludeImportsCacheMu.Lock()
		delete(preludeImportsCache, "")
		preludeImportsCacheMu.Unlock()
	})

	fromLoader, err := preludeImports("", loader)
	if err != nil {
		t.Fatal(err)
	}
	if len(asked) != 1 || len(asked[0]) != 3 || asked[0][0] != "" || asked[0][1] != "std" || asked[0][2] != "prelude" {
		t.Fatalf("the loader was asked for %v, want std/prelude once", asked)
	}
	if len(fromLoader) != len(fromTree) {
		t.Fatalf("the loader's prelude gives %d imports, the tree's %d", len(fromLoader), len(fromTree))
	}
	for i := range fromTree {
		if got, want := importText(fromLoader[i]), importText(fromTree[i]); got != want {
			t.Errorf("import %d: loader %q, tree %q", i, got, want)
		}
	}
}

func importText(s *ast.ImportStmt) string {
	text := ""
	for _, seg := range s.ModulePath {
		text += nodeName(seg) + "/"
	}
	for _, name := range s.Names {
		text += " " + nodeName(name)
	}
	return text
}

func nodeName(n ast.Node) string {
	switch v := n.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.TypeIdent:
		return v.Name
	}
	return "?"
}
