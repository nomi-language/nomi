package frontend

import (
	"fmt"
	"os"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/internal/syntheticextern"
)

// checkFromInterfaces is set by NOMI_CHECK_FROM_INTERFACES=1: after the
// front end checks a program, it checks every project file of the program
// again against the interfaces of the files it reads
// (analysis.ProgramInterfaces), and a file whose own diagnostics differ
// from its check from source fails the program with the difference. The
// expectation suites run under it in scripts/check-full.sh, so every
// program they check holds checking against interfaces to checking from
// source.
var checkFromInterfaces = os.Getenv("NOMI_CHECK_FROM_INTERFACES") == "1"

// compareFromInterfaces checks every project file of the program Analyze
// built (the entry fa with its nodes, and the files it reached) against
// interfaces, and answers an error naming the first file whose own
// diagnostics differ from local's, the program's check from source.
func (c *Checker) compareFromInterfaces(fa *analysis.FileAnalysis, nodes []ast.Node, siblingFAs map[string]*analysis.FileAnalysis, siblingNodes map[string][]ast.Node, local map[string]*analysis.FileCheck) error {
	pi := analysis.NewProgramInterfaces(c.sourceProgram(fa, nodes, siblingFAs, siblingNodes))
	for _, key := range pi.Keys() {
		want := local[key]
		if want == nil {
			continue
		}
		got := pi.Check(key)
		for _, part := range []struct {
			name      string
			want, got []analysis.TypeError
		}{
			{"build", want.Build, got.Build},
			{"check", want.Check, got.Check},
			{"entry checks", want.EntryChecks, got.EntryChecks},
		} {
			if w, g := renderErrors(part.want), renderErrors(part.got); w != g {
				name := key
				if name == "" {
					name = "the entry"
				}
				return fmt.Errorf("checked against interfaces, %s's %s diagnostics differ from its check from source:\nfrom source:\n%sagainst interfaces:\n%s", name, part.name, w, g)
			}
		}
	}
	return nil
}

// Interfaces answers the program the Checker last checked as interfaces,
// against which each of its project files can be checked
// (analysis.ProgramInterfaces), or nil when it checked none.
func (c *Checker) Interfaces() *analysis.ProgramInterfaces {
	if c.last == nil {
		return nil
	}
	l := c.last
	return analysis.NewProgramInterfaces(c.sourceProgram(l.fa, l.nodes, l.siblingFAs, l.siblingNodes))
}

// sourceProgram describes the program Analyze built, the entry fa with its
// nodes and the files it reached, for checking its files against
// interfaces.
func (c *Checker) sourceProgram(fa *analysis.FileAnalysis, nodes []ast.Node, siblingFAs map[string]*analysis.FileAnalysis, siblingNodes map[string][]ast.Node) *analysis.SourceProgram {
	files := map[string]*analysis.FileAnalysis{c.entryModRel: fa}
	nodesOf := map[string][]ast.Node{c.entryModRel: nodes}
	for key, sib := range siblingFAs {
		if analysis.IsStdlibKey(key) || key == c.entryModRel {
			continue
		}
		files[key], nodesOf[key] = sib, siblingNodes[key]
	}
	loader := c.Loader()
	return &analysis.SourceProgram{
		Root:            c.root,
		Entry:           c.entryModRel,
		EntryPath:       c.entryPath,
		PlaceEntry:      c.placeEntry,
		VirtualManifest: c.cfg.VirtualManifest,
		Manifest:        c.manifest(),
		Files:           files,
		Nodes:           nodesOf,
		Source: func(key string) ([]ast.Node, bool) {
			if key == c.entryModRel {
				nodes, err := parser.Parse(lexer.Lex(c.entrySrc))
				if err != nil {
					return nil, false
				}
				if nodes, err = syntheticextern.Inject(nodes, c.cfg.SyntheticExterns, true); err != nil {
					return nil, false
				}
				return analysis.ExpandDeclarations(nodes, c.site(c.entryModRel)), true
			}
			path := strings.Split(key, "/")
			if files[key].FilePath != analysis.ResolveModulePath(c.root, path) {
				// A file of another module.
				return nil, false
			}
			nodes, err := loader(c.root, path)
			return nodes, err == nil
		},
		PrepareEntry: func(nodes []ast.Node) ([]ast.Node, []analysis.TypeError) {
			nodes, _ = analysis.LowerDerives(nodes)
			if !c.entryMode.Tests {
				StripAttachedTests(nodes)
			}
			nodes, _ = analysis.SynthesizeDerives(nodes)
			return analysis.SynthesizeUniversalDebug(nodes), nil
		},
		Loader:     loader,
		Primitives: c.lib.Primitives,
		Modules:    c.lib.Modules,
		StdlibFAs:  c.lib.Files,
		AppTypes:   fa.Program().AppTypes,
	}
}

// renderErrors renders errs one a line, with what a diagnostic shows.
func renderErrors(errs []analysis.TypeError) string {
	var b strings.Builder
	for _, e := range errs {
		fmt.Fprintf(&b, "  %d:%d-%d:%d %s", e.Line, e.Col, e.EndLine, e.EndCol, e.Message)
		if e.File != "" {
			fmt.Fprintf(&b, " (in %s)", e.File)
		}
		for _, h := range e.Hints {
			fmt.Fprintf(&b, " hint: %s", h)
		}
		b.WriteByte('\n')
	}
	return b.String()
}
