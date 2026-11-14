package vmhost

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/token"
	"github.com/nomi-language/nomi/std"
)

// Advice is what Check suggests about the program's own files, as
// warnings: each function type parameter the style guide writes as an
// interface value (analysis.InterfaceValueHints), with the fix that does,
// and, marked Hint, the hints the language server shows
// (analysis.FileHints), which Check writes only under WithHints. The
// program means the same either way. A file outside the program's
// root (a dependency's) and a std file get none. The warnings are in the
// order of their files' paths, and in each file in the order of their
// positions. Check writes them where WithWarnings says; Load and a run do not.
//
// A file's advice depends on its own signatures and bodies alone, so a
// kept program holds it: the program's record holds the whole program's
// (cache.go), which a check that finds the program kept writes, and the
// file record each file's, which a relink keeps for a file it does not
// check again (filerecord.go).
func (p *Program) Advice() Diagnostics {
	if p.advice != nil {
		return *p.advice
	}
	byFile := map[string]Diagnostics{}
	for _, m := range p.prog.Modules {
		if m.FA == nil || !p.ownsFile(m.Path) {
			continue
		}
		byFile[m.Path] = fileAdvice(m.Path, m.FA, m.Nodes, func() string {
			src, _ := p.sourceOf(m.Path)
			return src
		})
	}
	out := joinAdvice(byFile)
	p.advice = &out
	return out
}

// fileAdvice is the advice of the file at path, analyzed as fa from nodes,
// whose text source answers when a diagnostic needs it. A std file gets
// none.
func fileAdvice(path string, fa *analysis.FileAnalysis, nodes []ast.Node, source func() string) Diagnostics {
	if fa == nil || std.IsSourcePath(path) {
		return nil
	}
	src, read := "", false
	text := func() string {
		if !read {
			src, read = source(), true
		}
		return src
	}
	warns := analysis.InterfaceValueHints(fa, nodes, func() []token.Token { return lexer.Lex(text()) })
	hints := analysis.FileHints(fa, nodes)
	if len(warns) == 0 && len(hints) == 0 {
		return nil
	}
	out := frontend.WarningDiagnostics(path, text(), warns)
	out = append(out, frontend.HintDiagnostics(path, text(), hints)...)
	slices.SortStableFunc(out, func(a, b Diagnostic) int {
		if a.Line != b.Line {
			return a.Line - b.Line
		}
		return a.Col - b.Col
	})
	return out
}

// withoutHints is ds without the diagnostics marked Hint, unless hints.
func withoutHints(ds Diagnostics, hints bool) Diagnostics {
	if hints {
		return ds
	}
	return slices.DeleteFunc(slices.Clone(ds), func(d Diagnostic) bool { return d.Hint })
}

// joinAdvice is the program's advice from each file's: in the order of
// the files' paths.
func joinAdvice(byFile map[string]Diagnostics) Diagnostics {
	paths := make([]string, 0, len(byFile))
	for path, ds := range byFile {
		if len(ds) > 0 {
			paths = append(paths, path)
		}
	}
	slices.Sort(paths)
	var out Diagnostics
	for _, path := range paths {
		out = append(out, byFile[path]...)
	}
	return out
}

// ownsFile reports whether the file at path is the program's own: under
// its root, or anywhere when it has none.
func (p *Program) ownsFile(path string) bool {
	return ownedBy(p.prog.Root, path)
}

// ownedBy reports whether the file at path is the own file of a program
// whose root is root: under it, or anywhere when there is none.
func ownedBy(root, path string) bool {
	if root == "" || !filepath.IsAbs(path) {
		return true
	}
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
