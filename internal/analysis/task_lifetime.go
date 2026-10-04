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

// walkSubtree descends an AST subtree looking for ConcurrentBlock
// nodes. Each ConcurrentBlock triggers checkBlock + recursive descent
// into nested blocks.
func (tl *taskLifetime) walkSubtree(n ast.Node) {
	if n == nil {
		return
	}
	switch node := n.(type) {
	case *ast.ConcurrentBlock:
		tl.checkBlock(node)
		if node.Body != nil {
			for _, stmt := range node.Body.Stmts {
				tl.walkSubtree(stmt)
			}
		}
	case *ast.Block:
		for _, stmt := range node.Stmts {
			tl.walkSubtree(stmt)
		}
	case *ast.Lambda:
		if node.Body != nil {
			for _, stmt := range node.Body.Stmts {
				tl.walkSubtree(stmt)
			}
		}
	case *ast.Call:
		tl.walkSubtree(node.Func)
		for _, arg := range node.Args {
			tl.walkSubtree(arg)
		}
	case *ast.ExprStmt:
		tl.walkSubtree(node.Expr)
	case *ast.GroupedExpr:
		tl.walkSubtree(node.Expr)
	case *ast.If:
		tl.walkSubtree(node.Cond)
		tl.walkSubtree(node.Then)
		tl.walkSubtree(node.Else)
	case *ast.Binary:
		tl.walkSubtree(node.Left)
		tl.walkSubtree(node.Right)
	case *ast.Unary:
		tl.walkSubtree(node.Right)
	case *ast.Binding:
		tl.walkSubtree(node.Value)
	case *ast.TupleDestructure:
		tl.walkSubtree(node.Value)
	case *ast.StructDestructure:
		tl.walkSubtree(node.Value)
	case *ast.MapDestructure:
		tl.walkSubtree(node.Value)
	case *ast.DistinctDestructure:
		tl.walkSubtree(node.Value)
	case *ast.PatternBinding:
		tl.walkSubtree(node.Value)
		for _, e := range node.ElseNodes() {
			tl.walkSubtree(e)
		}
	case *ast.FieldAccess:
		tl.walkSubtree(node.Object)
	case *ast.Return:
		tl.walkSubtree(node.Value)
	case *ast.Break:
		tl.walkSubtree(node.Value)
	case *ast.ListLit:
		for _, item := range node.Items {
			tl.walkSubtree(item)
		}
	case *ast.VectorLit:
		for _, item := range node.Items {
			tl.walkSubtree(item)
		}
	case *ast.SetLit:
		for _, item := range node.Items {
			tl.walkSubtree(item)
		}
	case *ast.TupleLit:
		for _, item := range node.Items {
			tl.walkSubtree(item)
		}
	case *ast.MapLit:
		for _, entry := range node.Entries {
			tl.walkSubtree(entry.Key)
			tl.walkSubtree(entry.Value)
		}
	case *ast.StructLit:
		if node.Spread != nil {
			tl.walkSubtree(node.Spread)
		}
		for _, f := range node.Fields {
			tl.walkSubtree(f.Value)
		}
	case *ast.Case:
		tl.walkSubtree(node.Value)
		for _, br := range node.Branches {
			tl.walkSubtree(br.Guard)
			tl.walkSubtree(br.Body)
		}
	case *ast.TryOp:
		tl.walkSubtree(node.Expr)
	case *ast.Dbg:
		tl.walkSubtree(node.Expr)
	case *ast.StringInterp:
		for _, part := range node.Parts {
			if se, ok := part.(ast.StringExpr); ok {
				tl.walkSubtree(se.Expr)
			}
		}
	case *ast.With:
		tl.walkSubtree(node.Value)
	case *ast.Defer:
		tl.walkSubtree(node.Call)
	case *ast.FuncDef:
		// Nested fn — descend; its own ConcurrentBlocks are valid
		// boundaries inside.
		if node.Body != nil {
			tl.walkSubtree(node.Body)
		}
	case *ast.OnceBinding:
		// `once` bindings are module-level — their RHS expression can
		// contain a ConcurrentBlock (computed lazily on first access).
		tl.walkSubtree(node.Value)
	}
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

// collectTaskBindings walks the immediate body of a ConcurrentBlock,
// collecting `Binding{Name: ..., Value: Call{Func: Ident("spawn"),
// ...}}` entries. Tuple/struct/distinct destructures are not currently
// tracked — they don't produce a meaningful "is this binding
// awaited?" question without per-element type tracking, which is out
// of scope for v1.
//
// The walk descends into nested blocks (if-then-else branches,
// case-branches, explicit blocks) so a `t = Task.spawn(...)` nested under an
// `if cond { ... }` inside a ConcurrentBlock is still tracked. Nested
// ConcurrentBlocks become their own checkBlock invocation — we skip
// them here so each block's bindings stay scoped to their owner.
//
// Nested Lambdas and FuncDefs are NOT descended (their bindings live
// in their own scope).
func (tl *taskLifetime) collectTaskBindings(block *ast.Block) []taskBinding {
	var bindings []taskBinding
	var visit func(n ast.Node)
	visit = func(n ast.Node) {
		if n == nil {
			return
		}
		switch node := n.(type) {
		case *ast.ConcurrentBlock:
			// Nested concurrent block — its bindings are its own
			// checkBlock's responsibility.
			return
		case *ast.Lambda, *ast.FuncDef:
			// Different scope.
			return
		case *ast.Block:
			for _, stmt := range node.Stmts {
				visit(stmt)
			}
		case *ast.ExprStmt:
			visit(node.Expr)
		case *ast.GroupedExpr:
			visit(node.Expr)
		case *ast.If:
			visit(node.Then)
			visit(node.Else)
			// node.Cond is an expression, not a binding site — skip.
		case *ast.Case:
			for _, br := range node.Branches {
				visit(br.Body)
			}
		case *ast.With:
			visit(node.Value)
		case *ast.Defer:
			visit(node.Call)
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
	}
	for _, stmt := range block.Stmts {
		visit(stmt)
	}
	return bindings
}

// collectReferencedNames returns the set of identifier names that
// appear as Reference uses anywhere inside the block (excluding
// binding declarations themselves and excluding nested FuncDef
// bodies, which have their own scope). The result is used by Rule 2
// to detect "binding never referenced".
//
// Nested ConcurrentBlocks ARE descended: an outer `t = Task.spawn(...)`
// referenced inside a nested `concurrent { Task.await(t) }` counts as a
// use of `t`. The nested block has its own checkBlock invocation that
// applies Rules 2 and 3 to its own bindings; this descent only widens
// what counts as a *reference* for the OUTER block's Rule 2 sweep.
// Same conservative-direction stance as for Lambda bodies (below):
// any name match suffices.
//
// Lambda bodies ARE descended: a `lists.map(tasks, |t| Task.await(t))`
// pattern (design doc Rule 2 corner case) counts the `t` parameter's
// uses inside the lambda as references, even though the parameter
// shadows the outer name. The consumer (Rule 2) matches on names only,
// so the conservative direction — accept any name match — is correct
// for the dominant "forgot to await my fetch" case the rule catches.
func (tl *taskLifetime) collectReferencedNames(block *ast.Block) map[string]bool {
	refs := make(map[string]bool)
	var visit func(n ast.Node)
	visit = func(n ast.Node) {
		if n == nil {
			return
		}
		switch node := n.(type) {
		case *ast.Ident:
			refs[node.Name] = true
		case *ast.FuncDef:
			// Different scope.
			return
		case *ast.ConcurrentBlock:
			// Nested concurrent block — descend into its body so a
			// reference from inside the nested block counts as use of
			// an outer binding. The nested block's own Rule 2 / Rule 3
			// checks run via checkBlock independently.
			if node.Body != nil {
				for _, stmt := range node.Body.Stmts {
					visit(stmt)
				}
			}
		case *ast.Lambda:
			if node.Body != nil {
				for _, stmt := range node.Body.Stmts {
					visit(stmt)
				}
			}
		case *ast.Block:
			for _, stmt := range node.Stmts {
				visit(stmt)
			}
		case *ast.ExprStmt:
			visit(node.Expr)
		case *ast.GroupedExpr:
			visit(node.Expr)
		case *ast.If:
			visit(node.Cond)
			visit(node.Then)
			visit(node.Else)
		case *ast.Case:
			visit(node.Value)
			for _, br := range node.Branches {
				visit(br.Guard)
				visit(br.Body)
			}
		case *ast.With:
			visit(node.Value)
		case *ast.Defer:
			visit(node.Call)
		case *ast.Binding:
			// Walk the RHS — references inside it count. The LHS
			// (node.Name) is a declaration, not a reference.
			visit(node.Value)
		case *ast.TupleDestructure:
			visit(node.Value)
		case *ast.StructDestructure:
			visit(node.Value)
		case *ast.MapDestructure:
			visit(node.Value)
		case *ast.DistinctDestructure:
			visit(node.Value)
		case *ast.PatternBinding:
			visit(node.Value)
			for _, e := range node.ElseNodes() {
				visit(e)
			}
		case *ast.Call:
			visit(node.Func)
			for _, arg := range node.Args {
				visit(arg)
			}
		case *ast.FieldAccess:
			visit(node.Object)
		case *ast.Binary:
			visit(node.Left)
			visit(node.Right)
		case *ast.Unary:
			visit(node.Right)
		case *ast.Return:
			visit(node.Value)
		case *ast.Break:
			visit(node.Value)
		case *ast.ListLit:
			for _, item := range node.Items {
				visit(item)
			}
		case *ast.VectorLit:
			for _, item := range node.Items {
				visit(item)
			}
		case *ast.SetLit:
			for _, item := range node.Items {
				visit(item)
			}
		case *ast.TupleLit:
			for _, item := range node.Items {
				visit(item)
			}
		case *ast.MapLit:
			for _, entry := range node.Entries {
				visit(entry.Key)
				visit(entry.Value)
			}
		case *ast.StructLit:
			if node.Spread != nil {
				visit(node.Spread)
			}
			for _, f := range node.Fields {
				visit(f.Value)
			}
		case *ast.TryOp:
			visit(node.Expr)
		case *ast.Dbg:
			visit(node.Expr)
		case *ast.StringInterp:
			for _, part := range node.Parts {
				if se, ok := part.(ast.StringExpr); ok {
					visit(se.Expr)
				}
			}
		}
	}
	for _, stmt := range block.Stmts {
		visit(stmt)
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
