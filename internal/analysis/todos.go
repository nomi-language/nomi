package analysis

import (
	"sort"

	"github.com/nomi-language/nomi/internal/ast"
)

// Todos is every `todo` expression in nodes, in source order. `nomi build`
// refuses a program with any, and the language server reports each one.
func Todos(nodes []ast.Node) []*ast.Todo {
	todos, _ := TodosAndDbgs(nodes)
	return todos
}

// Dbgs is every `dbg` expression in nodes, in source order. `nomi build`
// refuses a program with any, and the language server reports each one.
func Dbgs(nodes []ast.Node) []*ast.Dbg {
	_, dbgs := TodosAndDbgs(nodes)
	return dbgs
}

// TodosAndDbgs is Todos and Dbgs from one walk of nodes.
func TodosAndDbgs(nodes []ast.Node) (todos []*ast.Todo, dbgs []*ast.Dbg) {
	for _, n := range nodes {
		WalkNodes(n, func(c ast.Node) {
			switch v := c.(type) {
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
func containsTodo(n ast.Node) bool {
	found := false
	WalkNodes(n, func(c ast.Node) {
		if _, ok := c.(*ast.Todo); ok {
			found = true
		}
	})
	return found
}

// isTodoValue reports whether n's value is a `todo`: the expression itself,
// or a pipeline whose last stage is one.
func isTodoValue(n ast.Node) bool {
	switch v := ungroupExpr(n).(type) {
	case *ast.Todo:
		return true
	case *ast.Binary:
		if v.Op == "|>" {
			_, ok := ungroupExpr(v.Right).(*ast.Todo)
			return ok
		}
	}
	return false
}
