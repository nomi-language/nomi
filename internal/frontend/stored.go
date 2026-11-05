package frontend

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/internal/syntheticextern"
)

// StoredSource describes the program whose entry file is path, a file on
// disk, as checking its files against stored interfaces is given it
// (analysis.NewStoredInterfaces), in mode, with no check of the program:
// how its build is made and where each of its files' text comes from, the
// files as they are on disk now. appTypes are the program's application
// types, which only the program's check works out.
func (c *Checker) StoredSource(path string, mode Mode, appTypes []analysis.AppTypeName) (*analysis.SourceProgram, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	entry := c.PrepareFile(path, abs)
	c.entryModRel, c.placeEntry, c.entryMode, c.diagPath = entry, !mode.Tests, mode, abs
	if c.lib == nil {
		return nil, os.ErrInvalid
	}
	loader := c.Loader()
	return &analysis.SourceProgram{
		Root:       c.root,
		Entry:      entry,
		EntryPath:  abs,
		PlaceEntry: c.placeEntry,
		Manifest:   c.manifest(),
		Source: func(key string) ([]ast.Node, bool) {
			if key == entry {
				data, err := os.ReadFile(abs)
				if err != nil {
					return nil, false
				}
				c.entrySrc = string(data)
				nodes, err := parser.Parse(lexer.Lex(c.entrySrc))
				if err != nil {
					return nil, false
				}
				if nodes, err = syntheticextern.Inject(nodes, c.cfg.SyntheticExterns, true); err != nil {
					return nil, false
				}
				return analysis.ExpandDeclarations(nodes, c.site(entry)), true
			}
			nodes, err := loader(c.root, strings.Split(key, "/"))
			return nodes, err == nil
		},
		PrepareEntry: func(nodes []ast.Node) ([]ast.Node, []analysis.TypeError) {
			nodes, errs := analysis.LowerDerives(nodes)
			if !mode.Tests {
				StripAttachedTests(nodes)
			}
			errs = append(errs, analysis.CheckDuplicateTestNames(nodes)...)
			nodes, _ = analysis.SynthesizeDerives(nodes)
			return analysis.SynthesizeUniversalDebug(nodes), errs
		},
		Loader:       loader,
		Primitives:   c.lib.Primitives,
		Modules:      c.lib.Modules,
		StdlibFAs:    c.lib.Files,
		AppTypeNames: appTypes,
	}, nil
}

// SyntaxErrors reports whether a file the Checker's loader read did not
// parse.
func (c *Checker) SyntaxErrors() bool { return len(c.siblingSyntax) > 0 }
