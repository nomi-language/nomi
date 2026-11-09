package analysis

import (
	"sort"

	"github.com/nomi-language/nomi/internal/ast"
)

// Todos is every placeholder `todo` in nodes, in source order. `nomi build`
// refuses a program with any, and the language server reports each one. fa
// is the analysis of nodes, which knows which bare `todo` is the
// placeholder and which reads a binding (FileAnalysis.TodoPlaceholders);
// with none, every bare `todo` is the placeholder, as nothing is bound.
func Todos(nodes []ast.Node, fa *FileAnalysis) []*ast.Todo {
	todos, _ := TodosAndDbgs(nodes, fa)
	return todos
}

// Dbgs is every `dbg` expression in nodes, in source order. `nomi build`
// refuses a program with any, and the language server reports each one.
func Dbgs(nodes []ast.Node) []*ast.Dbg {
	_, dbgs := TodosAndDbgs(nodes, nil)
	return dbgs
}

// TodosAndDbgs is Todos and Dbgs from one walk of nodes.
func TodosAndDbgs(nodes []ast.Node, fa *FileAnalysis) (todos []*ast.Todo, dbgs []*ast.Dbg) {
	for _, n := range nodes {
		WalkNodes(n, func(c ast.Node) {
			switch v := c.(type) {
			case *ast.Ident:
				if fa == nil && v.Name == "todo" {
					todos = append(todos, &ast.Todo{SpanCarrier: v.SpanCarrier, Line: v.Line, Col: v.Col})
				} else if td, ok := fa.AsTodo(v).(*ast.Todo); ok {
					todos = append(todos, td)
				}
			case *ast.Todo:
				todos = append(todos, v)
			case *ast.Dbg:
				dbgs = append(dbgs, v)
			}
		})
	}
	sort.SliceStable(todos, func(i, j int) bool {
		if todos[i].Line != todos[j].Line {
			return todos[i].Line < todos[j].Line
		}
		return todos[i].Col < todos[j].Col
	})
	sort.SliceStable(dbgs, func(i, j int) bool {
		if dbgs[i].Line != dbgs[j].Line {
			return dbgs[i].Line < dbgs[j].Line
		}
		return dbgs[i].Col < dbgs[j].Col
	})
	return todos, dbgs
}

// containsTodo reports whether n has a `todo` anywhere inside it.
func containsTodo(fa *FileAnalysis, n ast.Node) bool {
	found := false
	WalkNodes(n, func(c ast.Node) {
		if _, ok := fa.AsTodo(c).(*ast.Todo); ok {
			found = true
		}
	})
	return found
}

// bottomValueKeyword is `todo` or `unreachable` when n's value is one: the
// expression itself, or a pipeline whose last stage is one. Otherwise "".
func bottomValueKeyword(fa *FileAnalysis, n ast.Node) string {
	switch v := fa.AsTodo(ungroupExpr(n)).(type) {
	case *ast.Todo:
		return "todo"
	case *ast.Unreachable:
		return "unreachable"
	case *ast.Binary:
		if v.Op == "|>" {
			return bottomValueKeyword(fa, v.Right)
		}
	}
	return ""
}

// bottomStage is a pipe stage that is a `todo` or an `unreachable`, which
// traps after the piped value is computed.
func bottomStage(fa *FileAnalysis, stage ast.Node) (ast.Node, bool) {
	switch s := fa.AsTodo(ungroupExpr(stage)).(type) {
	case *ast.Todo, *ast.Unreachable:
		return s, true
	}
	return nil, false
}
