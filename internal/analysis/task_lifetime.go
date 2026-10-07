package analysis

import (
	"fmt"
	"github.com/nomi-language/nomi/internal/ast"
)

// CheckTaskLifetime enforces Rules 2 and 3 of the task lifetime rules
// (spec §20):
//
//	Rule 2 — every `Task<T>` value bound by a binding inside a
//	`concurrent { }` block must be consumed (referenced anywhere after
//	the binding, lexically inside the block — typically by `Task.await(t)`).
//	A binding whose name is never referenced is rejected with diagnostic
//	`"Task bound to '<name>' is never awaited"`.
//
//	Rule 3 — a `Task<T>` value (or any composite containing one) cannot
//	escape its enclosing `concurrent { }` block. The block's tail
//	expression is the escape vector: if its syntactic shape produces a
//	Task value (a direct `Task.spawn(...)` call, a reference to a Task
//	binding declared inside the block, or a tuple / list / struct
//	literal containing any of the above), reject with diagnostic
//	`"Task<T> cannot escape its enclosing concurrent block"`.
//
// Both rules are local to a single `ConcurrentBlock`: the only producer
// of Task values is `Task.spawn(...)` (from `std/tasks` — Rule 1 already
// enforces that every Task.spawn call is lexically inside some concurrent
// block), so a syntactic trace from the block's tail back to its
// spawn-site is sufficient. No interprocedural analysis required.
//
// The use-once rule is conservative in v1: we accept "the binding name
// is referenced anywhere later in the block" as sufficient evidence of
// consumption, even when the reference isn't `Task.await(t)` directly. The
// strict await-or-discard-via-await form is the documented preference,
// but the cases the rule must catch are dominated by "forgot to await
// my fetch" — which always shows up as a binding whose name is never
// referenced. Tighter follow-on enforcement (require the use to be a
// transitive `await` call) can land later without surface change.
func CheckTaskLifetime(fa *FileAnalysis, nodes []ast.Node) []TypeError {
	tl := &taskLifetime{fa: fa}
	for _, node := range nodes {
		tl.walkSubtree(node)
	}
	return tl.errors
}

type taskLifetime struct {
	fa     *FileAnalysis
	errors []TypeError
}

// walkSubtree finds every ConcurrentBlock under n, in any position (a
// function or impl body, a test body, a case guard, a named argument, an
// assertion, a nested block) and checks each one. A block inside another
// block is checked on its own.
func (tl *taskLifetime) walkSubtree(n ast.Node) {
	ast.Inspect(n, func(n ast.Node) bool {
		if cb, ok := n.(*ast.ConcurrentBlock); ok {
			tl.checkBlock(cb)
		}
		return true
	})
}

// checkBlock applies Rules 2 and 3 to one ConcurrentBlock. Rule 2
// scans bindings inside the block for unreferenced Task bindings.
// Rule 3 examines the block's tail expression for a Task-producing
// shape.
func (tl *taskLifetime) checkBlock(cb *ast.ConcurrentBlock) {
	if cb.Body == nil || len(cb.Body.Stmts) == 0 {
		return
	}
	// Rule 2: collect bindings whose RHS is `Task.spawn(...)`. Each binding
	// records its name + the binding's position (for the diagnostic
	// anchor). Then sweep the rest of the block for references to that
	// name; absence means the binding is never awaited.
	taskBindings := tl.collectTaskBindings(cb.Body)
	if len(taskBindings) > 0 {
		referenced := tl.collectReferencedNames(cb.Body)
		for _, tb := range taskBindings {
			if referenced[tb.refKey] {
				continue
			}
			tl.errors = append(tl.errors, TypeError{
				Line:    tb.line,
				Col:     tb.col,
				Message: fmt.Sprintf("Task bound to '%s' is never awaited", tb.name),
			})
		}
	}

	// Rule 3: examine the block's tail expression. The tail is the last
	// statement of the block's Body — if it's an ExprStmt or a value-
	// producing expression, check for a Task-producing syntactic shape
	// rooted at the binding scope of the block itself.
	tail := cb.Body.Stmts[len(cb.Body.Stmts)-1]
	tailExpr := unwrapExprStmt(tail)
	if tl.tailEscapes(tailExpr, taskBindings) {
		line, col := positionOf(tailExpr)
		tl.errors = append(tl.errors, TypeError{
			Line:    line,
			Col:     col,
			Message: "Task<T> cannot escape its enclosing concurrent block",
		})
	}
}

// taskBinding records one binding declared inside a ConcurrentBlock
// whose RHS is `Task.spawn(...)`. `refKey` mirrors `name` today, but the
// pair is kept so a future destructure-aware version can switch the
// lookup key (e.g., one element of a tuple destructure) without
// changing the diagnostic-facing identifier.
type taskBinding struct {
	name   string
	refKey string
	line   int
	col    int
}

// collectTaskBindings collects the `name = Task.spawn(...)` bindings
// anywhere in a ConcurrentBlock's body: in nested blocks, if and case
// branches, and a block on a binding's right-hand side. Tuple, struct and
// distinct destructures are not tracked; they don't produce a meaningful
// "is this binding awaited?" question without per-element type tracking.
//
// A nested ConcurrentBlock's bindings are its own checkBlock's
// responsibility, and a Lambda's or FuncDef's bindings live in their own
// scope, so the walk does not enter them.
func (tl *taskLifetime) collectTaskBindings(block *ast.Block) []taskBinding {
	var bindings []taskBinding
	for _, stmt := range block.Stmts {
		ast.Inspect(stmt, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.ConcurrentBlock, *ast.Lambda, *ast.FuncDef:
				return false
			case *ast.Binding:
				if tl.isSpawnCall(node.Value) {
					bindings = append(bindings, taskBinding{
						name:   node.Name,
						refKey: node.Name,
						line:   node.Line,
						col:    node.Col,
					})
				}
			}
			return true
		})
	}
	return bindings
}

// collectReferencedNames returns the set of identifier names used
// anywhere inside the block, in any expression position. A binding's own
// name is a declaration, not an Ident, so it is not collected. Nested
// FuncDef bodies have their own scope and are skipped, and a field name
// (`x.t`) is not a use of a binding `t`. Rule 2 uses the result to detect
// "binding never referenced".
//
// Nested ConcurrentBlocks ARE descended: an outer `t = Task.spawn(...)`
// referenced inside a nested `concurrent { Task.await(t) }` counts as a
// use of `t`. The nested block has its own checkBlock invocation that
// applies Rules 2 and 3 to its own bindings; this descent only widens
// what counts as a *reference* for the OUTER block's Rule 2 sweep.
//
// Lambda bodies ARE descended: an `Iter.map(tasks, |t| Task.await(t))`
// counts the `t` parameter's uses inside the lambda as references, even
// though the parameter shadows the outer name. The consumer (Rule 2)
// matches on names only, so the conservative direction (accept any name
// match) is correct for the dominant "forgot to await my fetch" case the
// rule catches.
func (tl *taskLifetime) collectReferencedNames(block *ast.Block) map[string]bool {
	refs := make(map[string]bool)
	var visit func(n ast.Node) bool
	visit = func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.Ident:
			refs[node.Name] = true
		case *ast.FuncDef:
			return false
		case *ast.FieldAccess:
			ast.Inspect(node.Object, visit)
			return false
		}
		return true
	}
	for _, stmt := range block.Stmts {
		ast.Inspect(stmt, visit)
	}
	return refs
}

// isSpawnCall reports whether `n` is a Call to `Task.spawn` or
// `Task.spawn_all` — the two block-owned spawns.
//
// Both bind something that must be consumed before the block closes:
// `spawn` a `Task<T>` awaited with `Task.await`, `spawn_all` a
// `List<Task<T>>` awaited with `Task.await_all`. Rule 2's
// "is this name referenced later in the block?" check does not care
// which, so recognising the producer is the whole extension.
//
// Uses fa.References to resolve the callee through the Resolved chain.
func (tl *taskLifetime) isSpawnCall(n ast.Node) bool {
	call, ok := n.(*ast.Call)
	if !ok {
		return false
	}
	var pos Pos
	switch fn := call.Func.(type) {
	case *ast.Ident:
		pos = Pos{Line: fn.Line, Col: fn.Col}
	case *ast.FieldAccess:
		if fn.Field == nil {
			return false
		}
		pos = Pos{Line: fn.Field.Line, Col: fn.Field.Col}
	default:
		return false
	}
	sym, ok := tl.fa.References[pos]
	if !ok {
		return false
	}
	return isTaskSpawnSymbol(tl.fa, resolvedSymbol(sym))
}

// tailEscapes reports whether the block's tail expression carries a
// Task value out of the block. Three escape shapes are detected:
//
//  1. The tail is a Call to `Task.spawn(...)` directly.
//  2. The tail is an Ident referencing a binding `t = Task.spawn(...)`
//     declared inside the block.
//  3. The tail is a Tuple/Struct/List literal whose elements include
//     any of the above (recursively).
//
// Cases NOT detected in v1:
//
//   - A tail that's a Call to a function whose declared return type
//     contains Task (requires the function's declared type, beyond
//     this local check).
//   - A FieldAccess on a struct whose field type is Task.
//   - A `|>` pipe expression whose post-pipe value is a Task — e.g.
//     `concurrent { Task.spawn(|| 42) |> some_transform }`. The pipe is a
//     Binary node at the AST level; structurally it desugars to
//     `some_transform(Task.spawn(|| 42))`, but reasoning about whether
//     `some_transform`'s result is a Task requires the function's
//     declared return type. Same principled gap as the Call case
//     above.
//
// Rule 3's "no interprocedural analysis" principle keeps these out of
// scope; the dominant escape shape (the task_escape_return demo) is
// shape 1. A type-system-aware follow-on can close all three at once.
func (tl *taskLifetime) tailEscapes(expr ast.Node, taskBindings []taskBinding) bool {
	if expr == nil {
		return false
	}
	switch node := expr.(type) {
	case *ast.Call:
		return tl.isSpawnCall(node)
	case *ast.Ident:
		for _, tb := range taskBindings {
			if tb.refKey == node.Name {
				return true
			}
		}
		return false
	case *ast.TupleLit:
		for _, item := range node.Items {
			if tl.tailEscapes(item, taskBindings) {
				return true
			}
		}
		return false
	case *ast.ListLit:
		for _, item := range node.Items {
			if tl.tailEscapes(item, taskBindings) {
				return true
			}
		}
		return false
	case *ast.VectorLit:
		for _, item := range node.Items {
			if tl.tailEscapes(item, taskBindings) {
				return true
			}
		}
		return false
	case *ast.SetLit:
		for _, item := range node.Items {
			if tl.tailEscapes(item, taskBindings) {
				return true
			}
		}
		return false
	case *ast.StructLit:
		if node.Spread != nil && tl.tailEscapes(node.Spread, taskBindings) {
			return true
		}
		for _, f := range node.Fields {
			if tl.tailEscapes(f.Value, taskBindings) {
				return true
			}
		}
		return false
	case *ast.If:
		return tl.tailEscapes(node.Then, taskBindings) ||
			tl.tailEscapes(node.Else, taskBindings)
	case *ast.Block:
		if len(node.Stmts) == 0 {
			return false
		}
		return tl.tailEscapes(unwrapExprStmt(node.Stmts[len(node.Stmts)-1]), taskBindings)
	case *ast.Case:
		for _, br := range node.Branches {
			if tl.tailEscapes(br.Body, taskBindings) {
				return true
			}
		}
		return false
	}
	return false
}

// unwrapExprStmt returns the underlying expression if `n` is an
// *ast.ExprStmt, else `n` itself.
func unwrapExprStmt(n ast.Node) ast.Node {
	if es, ok := n.(*ast.ExprStmt); ok {
		return es.Expr
	}
	return n
}

// positionOf returns a best-effort (line, col) for a node, falling
// back to (0, 0) when the node lacks position fields.
func positionOf(n ast.Node) (int, int) {
	switch node := n.(type) {
	case *ast.Call:
		return node.Line, node.Col
	case *ast.Ident:
		return node.Line, node.Col
	case *ast.TupleLit:
		return node.Line, node.Col
	case *ast.ListLit:
		return node.Line, node.Col
	case *ast.VectorLit:
		return node.Line, node.Col
	case *ast.SetLit:
		return node.Line, node.Col
	case *ast.StructLit:
		return node.Line, node.Col
	case *ast.If:
		return node.Line, node.Col
	case *ast.With:
		return node.Line, node.Col
	case *ast.Case:
		return node.Line, node.Col
	case *ast.Block:
		return node.Line, node.Col
	case *ast.Binding:
		return node.Line, node.Col
	}
	return 0, 0
}
