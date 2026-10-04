package analysis_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
)

// appScopeProject writes `files` under a fresh temp dir and returns the root.
func appScopeProject(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, src := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return root
}

// appScopeCounts is what a project build asked the FileLoader for.
//
// `total` includes the ~49 stdlib modules every build pulls through discovery,
// so it is useless as a gate signal on its own. `payload` counts requests for
// the `settings` module, which is reachable ONLY from `main.nomi` — so a
// nonzero `payload` means main's project was built and a zero means it was
// not, whatever the stdlib did.
type appScopeCounts struct {
	total   int
	payload int
}

// appScopeLoader is the on-disk FileLoader BuildProjectFromEntry needs, plus
// the counters that make the recovery's gate observable rather than
// incidental.
func appScopeLoader(counts *appScopeCounts) analysis.FileLoader {
	return func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		if counts != nil {
			counts.total++
			if len(modulePath) == 1 && modulePath[0] == "settings" {
				counts.payload++
			}
		}
		path := filepath.Join(projectRoot, filepath.Join(modulePath...)) + ".nomi"
		src, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		nodes, _ := parser.ParseWithRecovery(lexer.Lex(string(src)))
		return nodes, nil
	}
}

// appScopeAnalyze builds `entry` (a name relative to root) AS the project
// entry, which is what both the CLI and the LSP fallback branch do for a file
// that is not `main.nomi`. Returns the entry's own FileAnalysis and every
// diagnostic, build-phase and check-phase alike.
func appScopeAnalyze(t *testing.T, root, entry string, counts *appScopeCounts) (*analysis.FileAnalysis, []analysis.TypeError) {
	t.Helper()
	entryPath := filepath.Join(root, entry)
	src, err := os.ReadFile(entryPath)
	if err != nil {
		t.Fatalf("read entry %s: %v", entry, err)
	}
	nodes, parseErrs := parser.ParseWithRecovery(lexer.Lex(string(src)))
	if len(parseErrs) > 0 {
		t.Fatalf("parse %s: %+v", entry, parseErrs)
	}
	lib := std.Load()
	fa, _, _ := analysis.BuildProjectFromEntry(entryPath, nodes, lib.Primitives, lib.Modules, lib.Files, root, appScopeLoader(counts))
	if fa == nil {
		t.Fatalf("BuildProjectFromEntry returned nil for %s", entry)
	}
	errs := append([]analysis.TypeError{}, fa.TypeErrors...)
	errs = append(errs, analysis.CheckTypes(fa, nodes)...)
	return fa, errs
}

// appScopeStruct asserts the analysis landed on the `App` struct.
