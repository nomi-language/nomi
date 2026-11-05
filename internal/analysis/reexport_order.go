package analysis

import "github.com/nomi-language/nomi/internal/ast"

// A project build defines a file's imports when it walks the file's
// annotations (Sweep B-ann), one file at a time. A name another file
// re-exports with `pub import` is in that file's scope only once its import
// is defined, so a file walked before the facade read the facade without it:
// `import facade.Reaction` was "file 'facade' has no exported name
// 'Reaction'", depending on the files' names. defineReexports defines every
// re-export before any file's annotations are walked, each once the names it
// selects are in their file's scope, so a re-export of a re-export waits for
// the one it reads, and Sweep B-ann skips what it defined
// (builder.reexportsDefined).

// reexportFile is one file of the build, as defineReexports reads it.
type reexportFile struct {
	b     *builder
	scope *Scope
	nodes []ast.Node
}

// defineReexports defines the selective `pub import` statements of files,
// in rounds: a round defines each statement whose names its module already
// holds, and the rounds stop when one defines nothing. A statement left
// then (a name no file declares, or re-exports that read each other in a
// cycle) is defined by Sweep B-ann in its place, which reports it.
func defineReexports(files []reexportFile) {
	type pending struct {
		f reexportFile
		n *ast.ImportStmt
	}
	var todo []pending
	for _, f := range files {
		for _, node := range f.nodes {
			switch n := node.(type) {
			case *ast.ImportStmt:
				if n.Pub && len(n.Names) > 0 {
					todo = append(todo, pending{f, n})
				}
			case *ast.ImportBlock:
				for _, entry := range n.Entries {
					if entry.Pub && len(entry.Names) > 0 {
						todo = append(todo, pending{f, entry})
					}
				}
			}
		}
	}
	for len(todo) > 0 {
		var left []pending
		for _, p := range todo {
			if !p.f.b.reexportResolves(p.n) {
				left = append(left, p)
				continue
			}
			p.f.b.defineImport(p.n, p.f.scope, false /* nested */)
			if p.f.b.reexportsDefined == nil {
				p.f.b.reexportsDefined = make(map[*ast.ImportStmt]bool)
			}
			p.f.b.reexportsDefined[p.n] = true
		}
		if len(left) == len(todo) {
			return
		}
		todo = left
	}
}

// reexportResolves reports whether defining n now would find each name it
// selects: its module resolves (as defineImport resolves it, the longest
// prefix of the path that names a file) and holds every name. A path that
// goes on past its file into an owner type (`Bool.{self, True}`) selects
// the owner's members, which do not wait on any import, and a path that
// names no file is defined at once, which reports it.
func (b *builder) reexportResolves(n *ast.ImportStmt) bool {
	path := make([]string, len(n.ModulePath))
	for i, seg := range n.ModulePath {
		path[i] = ast.ImportNodeName(seg)
	}
	for split := len(path); split >= max(1, n.FileSegments); split-- {
		scope := b.resolveStdlibImport(path[:split])
		if scope == nil {
			scope, _ = b.resolveImport(path[:split])
		}
		if scope == nil {
			continue
		}
		if split < len(path) {
			return true
		}
		for _, name := range n.Names {
			if scope.LookupLocal(ast.ImportNodeName(name)) == nil {
				return false
			}
		}
		return true
	}
	return true
}
