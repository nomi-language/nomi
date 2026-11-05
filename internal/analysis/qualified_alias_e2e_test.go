package analysis_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// A typealias in another module, named through its file's qualifier
// (`items.Item`, where qualified_alias_dep's items.nomi declares
// `pub typealias Item source.Item` over a struct in a third module), is a type
// wherever a type is written: in a non-entry file's `once` annotation, a
// parameter and a struct literal. Modules build their types in an order the
// using file does not control, so the alias may not be resolved yet when the
// file registers its qualifier's names; it was then `unknown type "items.Item"`.
func TestQualifiedAlias_AcrossModulesIsAType_E2E(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed; cannot resolve fixture path")
	}
	fixtureRoot := filepath.Join(filepath.Dir(thisFile), "testdata", "qualified_alias_app")
	data, err := os.ReadFile(filepath.Join(fixtureRoot, "main.nomi"))
	if err != nil {
		t.Fatalf("read fixture entry: %v", err)
	}
	entryNodes, _ := parser.ParseWithRecovery(lexer.Lex(string(data)))
	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		fileData, err := os.ReadFile(filepath.Join(projectRoot, filepath.Join(modulePath...)) + ".nomi")
		if err != nil {
			return nil, err
		}
		nodes, _ := parser.ParseWithRecovery(lexer.Lex(string(fileData)))
		return nodes, nil
	}

	lib := loadStd()
	fa, files, _ := analysis.BuildProjectWithCache(entryNodes, lib.Primitives, lib.Modules, lib.Files, fixtureRoot, loader)
	if fa == nil {
		t.Fatal("expected non-nil FileAnalysis")
	}
	errs := entryErrors(fa)
	for _, f := range files {
		errs = append(errs, f.TypeErrors...)
	}
	if len(errs) != 0 {
		t.Fatalf("the front end rejects a qualified alias across modules: %v", errs)
	}
}
