package analysis

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// Short names and full paths (spec §14 *Short names and full paths*).
//
// A file import binds the file under two names: its short name, the last
// segment of its path or its `as` alias (`posts` for `import db::posts`), and
// its full path (`db::posts`). The full path is a scope entry like any other
// name; no identifier can spell it, so nothing shadows it, and it is how a
// file is reached when its short name is taken:
//
//   - two imports that bind one short name to two files leave the name
//     Ambiguous, and a use of it is an error that names both full paths;
//   - a declaration of the file with the short name keeps the name
//     (collidesWithImportedModule, bindFileShortName);
//   - a local with the short name wins inside its scope (lookupModuleScope
//     looks no further than the closest binding).
//
// A full path names only a file the file imports as a file: path-only, with
// an alias, or with `self` in its brace list. Any other full path is an
// error that says to import it.

// importFullPath is the full path n imports, `db::posts`, or "" when the
// path is a single segment, whose full path is its short name.
func importFullPath(n *ast.ImportStmt) string {
	segs := n.ModulePath
	if n.FileSegments > 0 && n.FileSegments <= len(segs) {
		segs = segs[:n.FileSegments]
	}
	if len(segs) < 2 {
		return ""
	}
	parts := make([]string, len(segs))
	for i, seg := range segs {
		parts[i] = ast.ImportNodeName(seg)
	}
	return strings.Join(parts, ast.PathSeparator)
}

// importSpelledPath is n's file path as written, one segment or several.
func importSpelledPath(n *ast.ImportStmt) string {
	if full := importFullPath(n); full != "" {
		return full
	}
	if len(n.ModulePath) == 0 {
		return ""
	}
	return ast.ImportNodeName(n.ModulePath[0])
}

// bindFileShortName binds sym, a file import's symbol, under its short name
// in scope, and reports whether it took the name:
//
//   - a declaration of the file holds the name, and keeps it;
//   - another import binds the name to another file, and the name becomes
//     ambiguous between the two;
//   - otherwise sym takes the name.
func (b *builder) bindFileShortName(scope *Scope, sym *Symbol, n *ast.ImportStmt) bool {
	name := sym.Name
	if !IsSynthesizedLine(sym.Pos.Line) && scope == b.file.ModuleScope {
		if full := importFullPath(n); full != "" {
			if b.file.ImportedFiles == nil {
				b.file.ImportedFiles = map[string][]string{}
			}
			if !slices.Contains(b.file.ImportedFiles[name], full) {
				b.file.ImportedFiles[name] = append(b.file.ImportedFiles[name], full)
			}
		}
	}
	prior := scope.LookupLocal(name)
	if prior == nil || IsSynthesizedLine(sym.Pos.Line) {
		scope.Define(sym)
		return true
	}
	if prior.Kind == SymbolModule && symbolCameFromImport(prior) && !IsSynthesizedLine(prior.Pos.Line) {
		if prior.Ambiguous == nil && (prior.ModuleScope == sym.ModuleScope || sym.ModuleScope == nil || prior.ModuleScope == nil) {
			// The same file imported twice is the unused-import check's
			// to report, and a file that did not resolve is already an
			// error at its path.
			scope.Define(sym)
			return true
		}
		candidates := slices.Clone(prior.Ambiguous)
		if candidates == nil {
			if imp, ok := prior.Node.(*ast.ImportStmt); ok {
				candidates = []string{importSpelledPath(imp)}
			}
		}
		if spelled := importSpelledPath(n); !slices.Contains(candidates, spelled) {
			candidates = append(candidates, spelled)
		}
		scope.Define(&Symbol{Name: name, Kind: SymbolModule, Pos: prior.Pos, Node: prior.Node, Ambiguous: candidates})
		if scope == b.file.ModuleScope {
			b.forgetFileShortName(name)
		}
		return false
	}
	if prior.Kind == SymbolModule || symbolCameFromImport(prior) ||
		prior.Kind == SymbolInterfaceMethod || isLocalVariant(prior) ||
		prior.Pos.Line <= 0 || IsSynthesizedLine(prior.Pos.Line) {
		scope.Define(sym)
		return true
	}
	// A declaration of the file has the name. It keeps it, and the file is
	// reached by its full path.
	return false
}

// forgetFileShortName drops a file short name from the builder's module
// tables, once a declaration holds it or two imports share it.
func (b *builder) forgetFileShortName(name string) {
	delete(b.modules, name)
	delete(b.moduleSyms, name)
}

// bindFileFullPath binds sym's file under its full path in scope, when the
// path has more than one segment.
func (b *builder) bindFileFullPath(scope *Scope, sym *Symbol, n *ast.ImportStmt, nested bool) {
	full := importFullPath(n)
	if full == "" {
		return
	}
	fullSym := &Symbol{Name: full, Kind: SymbolModule, Pos: sym.Pos, Node: n, ModuleScope: sym.ModuleScope}
	scope.Define(fullSym)
	if nested || sym.ModuleScope == nil {
		return
	}
	if b.modules == nil {
		b.modules = make(map[string]*Scope)
	}
	if b.moduleSyms == nil {
		b.moduleSyms = make(map[string]*Symbol)
	}
	b.modules[full] = sym.ModuleScope
	b.moduleSyms[full] = fullSym
}

// walkFilePath resolves a full path written as a qualifier. A path the file
// does not import as a file is an error that says to import it.
func (b *builder) walkFilePath(fp *ast.FilePath, scope *Scope) {
	sym := scope.Lookup(fp.Spelling())
	if sym == nil || sym.Kind != SymbolModule {
		b.file.TypeErrors = append(b.file.TypeErrors, filePathNotImported(fp.Spelling(), fp.Line, fp.Col, len(fp.Spelling())))
		return
	}
	b.recordFilePathRefs(fp, sym)
}

// filePathNotImported is the error for a full path that names no file the
// file imports as a file.
func filePathNotImported(path string, line, col, width int) TypeError {
	return TypeError{
		Line:    line,
		Col:     col,
		EndLine: line,
		EndCol:  col + width,
		Message: fmt.Sprintf("`%s` is not imported", path),
	}.WithHint(fmt.Sprintf("a full path names a file this file imports: add `import %s`", path))
}

// recordFilePathRefs records sym, the file a full path names, at the path's
// first and last segments, and each segment between as the directory it
// names, for hover, go-to-definition and highlighting. Each record spans its
// own segment.
func (b *builder) recordFilePathRefs(fp *ast.FilePath, sym *Symbol) {
	last := len(fp.Segments) - 1
	for i, seg := range fp.Segments {
		pos := Pos{Line: seg.Line, Col: seg.Col}
		if i == 0 || i == last {
			site := *sym
			site.Span = len(seg.Name)
			b.file.References[pos] = &site
			continue
		}
		b.file.References[pos] = &Symbol{Name: seg.Name, Kind: SymbolModule, Pos: pos, Node: sym.Node}
	}
}

// CheckAmbiguousShortNames reports every use of a short name that two file
// imports bind: `notifications` could be db::notifications or
// jobs::notifications; write the full path.
func CheckAmbiguousShortNames(fa *FileAnalysis) []TypeError {
	var positions []Pos
	for pos, ref := range fa.References {
		if ref != nil && ref.Ambiguous != nil && !compilerWritten(pos.Line) {
			positions = append(positions, pos)
		}
	}
	sort.Slice(positions, func(i, j int) bool {
		if positions[i].Line != positions[j].Line {
			return positions[i].Line < positions[j].Line
		}
		return positions[i].Col < positions[j].Col
	})
	errs := make([]TypeError, 0, len(positions))
	for _, pos := range positions {
		errs = append(errs, ambiguousShortName(fa.References[pos], pos))
	}
	return errs
}

// ambiguousShortName is the error at pos for a use of the ambiguous short
// name sym.
func ambiguousShortName(sym *Symbol, pos Pos) TypeError {
	quoted := make([]string, len(sym.Ambiguous))
	copy(quoted, sym.Ambiguous)
	list := strings.Join(quoted[:len(quoted)-1], ", ") + " or " + quoted[len(quoted)-1]
	return TypeError{
		Line:    pos.Line,
		Col:     pos.Col,
		EndLine: pos.Line,
		EndCol:  pos.Col + len(sym.Name),
		Message: fmt.Sprintf("`%s` could be %s; write the full path", sym.Name, list),
	}
}

// shadowedFileHint is the hint for a member access on name, which resolves
// to sym, a value or a declaration, when the file imports a file of that
// short name: the file is reached by its full path. "" when it imports none.
func (fa *FileAnalysis) shadowedFileHint(name, member string, sym *Symbol) string {
	if fa == nil {
		return ""
	}
	paths := fa.ImportedFiles[name]
	if len(paths) == 0 {
		return ""
	}
	what := "a value"
	if sym != nil {
		what = "the " + kindLabel(sym)
		if sym.Pos.Line > 0 && !IsSynthesizedLine(sym.Pos.Line) {
			what += fmt.Sprintf(" on line %d", sym.Pos.Line)
		}
	}
	return fmt.Sprintf("`%s` here is %s, not the file %s; write `%s.%s`", name, what, paths[0], paths[0], member)
}
