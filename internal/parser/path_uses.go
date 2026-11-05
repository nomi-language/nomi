package parser

import (
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// NotePathUses sets the PathUses of every path-only import in nodes, a
// file's top-level nodes: the paths written below the name the import
// binds, in the import's scope. A file-level import's scope is the file; an
// import at the top of a block, the block. Every parse of a file runs it, so
// a tree from source always carries them; a caller that builds or edits a
// tree itself runs it again.
//
// The parser does not know whether an import names a file or a directory.
// Resolution decides that; when the import names a directory, PathUses are
// the files it reaches (spec §14 *Directory imports*), and discovery loads
// those and no other file under the directory.
func NotePathUses(nodes []ast.Node) {
	notePathUsesIn(nodes)
	for _, n := range nodes {
		ast.Inspect(n, func(m ast.Node) bool {
			if b, ok := m.(*ast.Block); ok && len(pathOnlyImports(b.Stmts)) > 0 {
				notePathUsesIn(b.Stmts)
			}
			return true
		})
	}
}

// notePathUsesIn sets the PathUses of the path-only imports among stmts
// from the paths the other statements write.
func notePathUsesIn(stmts []ast.Node) {
	imports := pathOnlyImports(stmts)
	if len(imports) == 0 {
		return
	}
	byName := map[string][]*ast.ImportStmt{}
	for _, imp := range imports {
		name := importBinding(imp)
		if name != "" {
			byName[name] = append(byName[name], imp)
		}
	}
	uses := map[*ast.ImportStmt]map[string]bool{}
	note := func(segs []ast.PathSegment) {
		if len(segs) < 2 {
			return
		}
		rest := make([]string, len(segs)-1)
		for i, seg := range segs[1:] {
			rest[i] = seg.Name
		}
		for _, imp := range byName[segs[0].Name] {
			if uses[imp] == nil {
				uses[imp] = map[string]bool{}
			}
			uses[imp][strings.Join(rest, ast.PathSeparator)] = true
		}
	}
	for _, s := range stmts {
		switch s.(type) {
		case *ast.ImportStmt, *ast.ImportBlock:
			continue
		}
		ast.Inspect(s, func(m ast.Node) bool {
			switch m := m.(type) {
			case *ast.FilePath:
				note(m.Segments)
			case *ast.QualifiedType:
				note(m.Path)
			}
			return true
		})
	}
	for _, imp := range imports {
		var list []string
		for use := range uses[imp] {
			list = append(list, use)
		}
		slices.Sort(list)
		imp.PathUses = list
	}
}

// pathOnlyImports are the imports among stmts that bind a file or a
// directory whole: no selected names.
func pathOnlyImports(stmts []ast.Node) []*ast.ImportStmt {
	var out []*ast.ImportStmt
	add := func(n *ast.ImportStmt) {
		if n != nil && len(n.Names) == 0 && len(n.ModulePath) > 0 {
			out = append(out, n)
		}
	}
	for _, s := range stmts {
		switch s := s.(type) {
		case *ast.ImportStmt:
			add(s)
		case *ast.ImportBlock:
			for _, e := range s.Entries {
				add(e)
			}
		}
	}
	return out
}

// importBinding is the name a path-only import binds: its alias, or its
// path's last segment.
func importBinding(n *ast.ImportStmt) string {
	if n.ModuleAlias != nil {
		return ast.ImportNodeName(n.ModuleAlias)
	}
	return ast.ImportNodeName(n.ModulePath[len(n.ModulePath)-1])
}
