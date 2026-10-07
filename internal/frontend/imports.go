package frontend

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// ImportedFiles answers the absolute paths of the project files the program
// whose entry is absPath loads, other than the entry and the stdlib: the files
// its imports reach, at file level or in a block, transitively, resolved
// from the root CheckFile would use. It parses and resolves; it analyzes nothing. A file
// with syntax errors is walked as far as recovery reads it.
func ImportedFiles(absPath string) ([]string, error) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, err
	}
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(string(data)))
	c := New(Config{})
	c.PrepareFile(absPath, absPath)
	proj, err := analysis.DiscoverProjectWithManifest(nodes, c.root, c.Loader(), nil)
	if proj == nil {
		return nil, err
	}
	var paths []string
	for key, path := range proj.FilePaths {
		if analysis.IsStdlibKey(key) || path == absPath {
			continue
		}
		if abs, err := filepath.Abs(path); err == nil {
			paths = append(paths, abs)
		}
	}
	sort.Strings(paths)
	return paths, err
}
