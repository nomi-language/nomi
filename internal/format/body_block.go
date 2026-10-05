package format

import (
	"reflect"

	"github.com/nomi-language/nomi/internal/ast"
)

// normalizeBodies is the formatter's rewrite of function, lambda and test
// bodies: tail returns go (stripTailReturns), then a body that is one block
// becomes that block's statements (flattenBodyBlocks). Stripping comes first
// because it makes such bodies: `return { a = 1  a + 1 }` becomes the block
// alone.
func normalizeBodies(nodes []ast.Node) {
	stripTailReturns(nodes)
	flattenBodyBlocks(nodes)
}

// flattenBodyBlocks rewrites a body whose only statement is a block
// expression, `fn f(): Int { { a = 1  a + 1 } }`, to the block's statements,
// `fn f(): Int { a = 1  a + 1 }`. A body is that of a `fn` (free, impl or
// interface default), a lambda, a test, a `tests` group's `setup`, either
// branch of an `if`, or a braced `case` arm. The rewrite is syntactic: `nomi fmt` runs without
// analysis.
//
// The inner block's scope ends where the body's does, and nothing else is
// in the body, so its bindings see and shadow the same names either way, and
// its value is the body's. The block is left alone when it is empty (`{}` is
// not a block body), when a comment follows its closing brace on that line,
// and when it holds, directly, a `defer`, a `with`, a nested `fn` or an
// import. A `defer` or `with` lasts to the end of its block, which would
// still be the body's end, but the compiler lowers those differently in a
// nested block and in a body, so moving one could change whether the program
// runs. A nested `fn` or import would join the scope of the parameters, where
// a name it shares with one would be a duplicate.
//
// A comment before the inner block's opening brace moves to its first
// statement, and one before its closing brace to the end of the body.
func flattenBodyBlocks(nodes []ast.Node) {
	w := bodyBlockWalker{seen: map[uintptr]bool{}}
	for _, n := range nodes {
		w.walk(reflect.ValueOf(n))
	}
}

type bodyBlockWalker struct {
	seen map[uintptr]bool
}

var (
	ifType         = reflect.TypeOf(ast.If{})
	caseBranchType = reflect.TypeOf(ast.CaseBranch{})
)

// walk flattens every body reachable from v. A body is flattened before its
// children are visited, so an inner body is reached at its new position.
func (w bodyBlockWalker) walk(v reflect.Value) {
	switch v.Kind() {
	case reflect.Interface:
		if !v.IsNil() {
			w.walk(v.Elem())
		}
	case reflect.Ptr:
		if v.IsNil() || w.seen[v.Pointer()] {
			return
		}
		w.seen[v.Pointer()] = true
		w.walk(v.Elem())
	case reflect.Struct:
		switch v.Type() {
		case funcDefType, lambdaType, interfaceMethodTyp:
			flattenBodyBlock(v.FieldByName("Body").Interface())
		case testDeclType:
			if v.FieldByName("Group").Bool() {
				flattenBodyBlock(v.FieldByName("Setup").Interface())
			} else {
				flattenBodyBlock(v.FieldByName("Body").Interface())
			}
		case ifType:
			flattenBodyBlock(v.FieldByName("Then").Interface())
			flattenBodyBlock(v.FieldByName("Else").Interface())
		case caseBranchType:
			flattenBodyBlock(v.FieldByName("Body").Interface())
		}
		for i := 0; i < v.NumField(); i++ {
			w.walk(v.Field(i))
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			w.walk(v.Index(i))
		}
	}
}

// flattenBodyBlock flattens body, when it is a block, for as long as its
// only statement is a block it may take the place of.
func flattenBodyBlock(body any) {
	outer, ok := body.(*ast.Block)
	if !ok || outer == nil {
		return
	}
	for len(outer.Stmts) == 1 {
		stmt, ok := outer.Stmts[0].(*ast.ExprStmt)
		if !ok || hasNonBlankTrivia(stmt.Trailing) {
			return
		}
		inner, ok := stmt.Expr.(*ast.Block)
		if !ok || len(inner.Stmts) == 0 || hasNonBlankTrivia(inner.Leading) || !flattenable(inner) {
			return
		}
		lead := nonBlank(stmt.Leading)
		if len(lead) > 0 {
			first, ok := inner.Stmts[0].(leadingSetter)
			if !ok {
				return
			}
			first.SetLeading(append(lead, first.GetLeading()...))
		}
		if tail := nonBlank(inner.Trailing); len(tail) > 0 {
			outer.Trailing = append(tail, outer.Trailing...)
		}
		outer.Stmts = inner.Stmts
	}
}

// flattenable reports whether block's statements may join the body around
// it: none of them is a `defer`, a `with`, a nested `fn` or an import.
func flattenable(block *ast.Block) bool {
	for _, s := range block.Stmts {
		switch s.(type) {
		case *ast.Defer, *ast.With, *ast.FuncDef, *ast.ImportStmt, *ast.ImportBlock:
			return false
		}
	}
	return true
}

func nonBlank(trivia []ast.Trivia) []ast.Trivia {
	var out []ast.Trivia
	for _, t := range trivia {
		if t.Kind != ast.TriviaBlankLine {
			out = append(out, t)
		}
	}
	return out
}

// leadingSetter is a node whose leading trivia can be replaced.
type leadingSetter interface {
	GetLeading() []ast.Trivia
	SetLeading([]ast.Trivia)
}
