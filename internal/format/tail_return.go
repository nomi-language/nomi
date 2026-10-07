package format

import "github.com/nomi-language/nomi/internal/ast"

// stripTailReturns removes each `return` that is the last thing a function,
// lambda or test body does. A block's last expression is its value, and
// `return` exits the nearest function boundary (a named function, a lambda
// or a test body, never an enclosing one), so `return x` there means `x`.
// The rewrite is syntactic: `nomi fmt` runs without analysis.
//
// Tail position starts at the body of every `fn` (free, impl and interface
// default), every lambda, `then` lambdas and iteration callbacks included,
// and every test body, and runs through the last statement of a block, both
// branches of an `if`, every arm of a `case`, and a braced arm body.
// Nothing else is tail position: a `tests` group's `setup`, a `once`
// initializer, a `concurrent` block, a binding's `else` and any non-last
// statement keep their `return`s.
//
// A `return value` becomes `value`, a brace-opening value included: a
// return's value, a statement and an arm body are parsed by the same
// expression parser, so `{n: n}` is the same literal in each place. A test
// body has no declared result type, so its returns may carry values of
// different types; a tail `if` or `case` there keeps its value returns,
// whose branches would otherwise have to agree on one type.
//
// A bare `return` goes only when the statement before it is an expression
// statement other than `dbg` or an assertion (in a test body, an assertion
// too): such a statement must already be `Unit`, so it can take the bare
// return's place as the value. A bare `return` that is its
// block's only statement, or that follows a binding, a `dbg` or anything
// else, stays, and so does any bare `return` that carries a comment. The
// checker rejects every one of those that ends a body
// (analysis/useless_return.go), but the fix is the author's to choose.
func stripTailReturns(nodes []ast.Node) {
	// A body is rewritten before its children are visited, so a lambda
	// inside a returned value is still reached through the value's new
	// position. A node shared by two parents is rewritten once.
	seen := map[ast.Node]bool{}
	for _, n := range nodes {
		ast.Inspect(n, func(n ast.Node) bool {
			if seen[n] {
				return false
			}
			seen[n] = true
			switch n := n.(type) {
			case *ast.FuncDef:
				tailStripper{}.body(n.Body)
			case *ast.Lambda:
				tailStripper{}.body(n.Body)
			case *ast.InterfaceMethod:
				body, _ := n.Body.(*ast.Block)
				tailStripper{}.body(body)
			case *ast.TestDecl:
				if !n.Group {
					tailStripper{untyped: true}.body(n.Body)
				}
			}
			return true
		})
	}
}

// tailStripper rewrites one body's tail. untyped marks a body with no
// declared result type (a test body); keepValues marks a branch of a tail
// `if` or `case` in such a body, where a `return value` stays.
type tailStripper struct {
	untyped    bool
	keepValues bool
}

// branch is the stripper for the branches of a tail `if` or `case`.
func (s tailStripper) branch() tailStripper {
	if s.untyped {
		s.keepValues = true
	}
	return s
}

// body rewrites the tail of a function, lambda or test body, when there is
// one.
func (s tailStripper) body(b *ast.Block) {
	if b != nil {
		s.block(b)
	}
}

// block rewrites the tail of a block in tail position.
func (s tailStripper) block(b *ast.Block) {
	for len(b.Stmts) > 0 {
		last := len(b.Stmts) - 1
		switch v := b.Stmts[last].(type) {
		case *ast.Return:
			if v.Value != nil {
				if s.keepValues {
					return
				}
				b.Stmts[last] = &ast.ExprStmt{
					TriviaCarrier: v.TriviaCarrier,
					SpanCarrier:   v.SpanCarrier,
					Expr:          v.Value,
					Line:          v.Line,
				}
				s.expr(v.Value)
				return
			}
			if last == 0 || hasNonBlankTrivia(v.Leading) || hasNonBlankTrivia(v.Trailing) ||
				!s.canStandForBareReturn(b.Stmts[last-1]) {
				return
			}
			b.Stmts = b.Stmts[:last]
			// The statement before is the new tail; strip it in turn.
		case *ast.ExprStmt:
			s.expr(v.Expr)
			return
		default:
			return
		}
	}
}

// expr rewrites an expression in tail position.
func (s tailStripper) expr(n ast.Node) {
	switch v := n.(type) {
	case *ast.Block:
		s.block(v)
	case *ast.If:
		if v.Then != nil {
			s.branch().block(v.Then)
		}
		s.branch().expr(v.Else)
	case *ast.Case:
		for i := range v.Branches {
			s.branch().arm(&v.Branches[i])
		}
	}
}

// arm rewrites a `case` arm in tail position. An arm whose body is
// `return value` becomes `value`, the return's comments moving to the value.
func (s tailStripper) arm(br *ast.CaseBranch) {
	ret, ok := br.Body.(*ast.Return)
	if !ok {
		s.expr(br.Body)
		return
	}
	if ret.Value == nil || s.keepValues {
		return
	}
	value, ok := ret.Value.(ast.HasTrivia)
	if !ok || (len(ret.Leading) > 0 && len(value.GetLeading()) > 0) {
		return
	}
	for _, t := range ret.Leading {
		value.AddLeading(t)
	}
	for _, t := range ret.Trailing {
		value.AddTrailing(t)
	}
	br.Body = ret.Value
	s.expr(ret.Value)
}

// canStandForBareReturn reports whether stmt can become the block's value in
// place of a bare `return` that follows it. A non-final expression statement
// must be `Unit` unless it is a `dbg` observation or an assertion, so any
// expression statement but those already is the value a bare return gives.
// A test body takes an assertion too: a body ending in one is judged by it,
// and a failed one has already ended the case.
func (s tailStripper) canStandForBareReturn(stmt ast.Node) bool {
	if _, ok := stmt.(*ast.Assertion); ok {
		return s.untyped
	}
	es, ok := stmt.(*ast.ExprStmt)
	if !ok || es.Expr == nil {
		return false
	}
	if _, ok := es.Expr.(*ast.Assertion); ok {
		return s.untyped
	}
	return !endsInDbg(es.Expr)
}

func endsInDbg(n ast.Node) bool {
	switch v := n.(type) {
	case *ast.Dbg:
		return true
	case *ast.Binary:
		return v.Op == "|>" && endsInDbg(v.Right)
	}
	return false
}
