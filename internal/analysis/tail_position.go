package analysis

import "github.com/nomi-language/nomi/internal/ast"

// MarkTailCalls walks every function and lambda body in the file and sets
// IsTailCall = true on Call nodes that land in tail position. Functions are
// top-level `fn`s, impl items, and nested `fn` statements wherever they
// appear (in another function, a test body or a `once` initializer).
//
// Tail position propagates into:
//   - the last statement of a function or lambda body
//   - both arms of if/else (when the if itself is in tail position)
//   - every branch body of a case (when the case itself is in tail position;
//     guards and the scrutinee are not)
//   - the last stmt of a Block
//   - the RHS of |> / `and` / `or` (pipe + short-circuit ops)
//   - the value of a Return (always — `return e` exits the enclosing fn
//     regardless of where the `return` itself sits)
//
// A call in tail position is still not a tail call while a `defer` written
// earlier in a block around it, within the same function, is pending: the
// deferred calls run when that block ends, which is after the call
// returns, and a tail call would run them before the callee instead.
//
// Tail position does NOT cross:
//   - lambda bodies — a lambda has its own tail context, walked from scratch
//     with tail=true at its boundary, regardless of the surrounding context
//   - default-value expressions (evaluated before the body runs; the top-level
//     loop only enters function bodies, never Param.Default)
//   - the value expression of a `with` binding or a `defer` call
//   - the sub-expression of `?` — the keyword performs a pattern-match
//     between the call's return and the function's return, so the call inside
//     `expr?` is not in tail position
//   - non-last stmts of a Block
//   - sub-expressions of Call, Binding/destructure RHS, default values,
//     aggregate literals (list/tuple/map/struct), range bounds, field-access
//     objects, unary operands, and string-interpolation expressions
//
// The pass is a stateless AST mutator — no diagnostics, no state.
func MarkTailCalls(nodes []ast.Node) {
	for _, n := range nodes {
		switch x := n.(type) {
		case *ast.FuncDef:
			markBlock(x.Body, true, false)
		case *ast.ImplBlock:
			// Impl-block methods are FuncDefs nested in Items; each body is a
			// full tail context just like a top-level fn. (ExternFunc items
			// have no body.) Without this, TCO never fires inside an `impl`
			// block — deep tail recursion through a block-form interface-impl
			// method (`Iface.method` dispatch) grows the host stack and
			// overflows, while the decorator form was marked fine.
			for _, item := range x.Items {
				if fn, ok := item.(*ast.FuncDef); ok {
					markBlock(fn.Body, true, false)
				}
			}
		case *ast.TestDecl, *ast.OnceBinding:
			// Neither is a function, so nothing in it is a tail call of its
			// own; the nested `fn`s and lambdas it holds are functions, and
			// mark gives each its own tail context.
			mark(x, false, false)
		}
	}
}

// mark walks a single expression node `n`. `tail` is true iff n's value
// (or, for statement-shaped nodes, the value of the last sub-expression)
// is the value of the enclosing function/lambda body.
func mark(n ast.Node, tail, deferred bool) {
	if n == nil {
		return
	}
	switch x := n.(type) {
	case *ast.Call:
		// A call with a `defer` pending in a block around it is an ordinary
		// call: a tail call would leave the frame, running the deferred
		// calls before the callee rather than after it.
		if tail && !deferred {
			x.IsTailCall = true
		}
		// Sub-expressions of a Call are never in tail position.
		mark(x.Func, false, deferred)
		for _, a := range x.Args {
			mark(a, false, deferred)
		}
	case *ast.ExprStmt:
		// ExprStmt is a pass-through wrapper around an expression — function
		// bodies always wrap their final expression in one.
		mark(x.Expr, tail, deferred)
	case *ast.GroupedExpr:
		mark(x.Expr, tail, deferred)
	case *ast.Capture:
		// The value is the function, not its expression's.
		mark(x.Body, false, deferred)
	case *ast.Binding:
		// Binding evaluates to Unit; its RHS is never tail.
		mark(x.Value, false, deferred)
	case *ast.TupleDestructure:
		mark(x.Value, false, deferred)
	case *ast.StructDestructure:
		mark(x.Value, false, deferred)
	case *ast.DistinctDestructure:
		mark(x.Value, false, deferred)
	case *ast.PatternBinding:
		// The else's value, if any, is bound, never returned; a `return`
		// inside it marks its own operand.
		mark(x.Value, false, deferred)
		if x.Else != nil {
			markBlock(x.Else.Block, false, deferred)
			for _, arm := range x.Else.Arms {
				mark(arm.Guard, false, deferred)
				mark(arm.Body, false, deferred)
			}
		}
	case *ast.NamedArg:
		mark(x.Value, false, deferred)
	case *ast.Block:
		markBlock(x, tail, deferred)
	case *ast.ListLit:
		for _, item := range x.Items {
			mark(item, false, deferred)
		}
	case *ast.VectorLit:
		for _, item := range x.Operands() {
			mark(item, false, deferred)
		}
	case *ast.SetLit:
		for _, item := range x.Operands() {
			mark(item, false, deferred)
		}
	case *ast.ListSpreadLit:
		for _, item := range x.Operands() {
			mark(item, false, deferred)
		}
	case *ast.TupleLit:
		for _, item := range x.Items {
			mark(item, false, deferred)
		}
	case *ast.MapLit:
		for _, sp := range x.Spreads {
			mark(sp.Head, false, deferred)
		}
		for _, e := range x.Entries {
			mark(e.Key, false, deferred)
			mark(e.Value, false, deferred)
		}
	case *ast.StructLit:
		if x.Spread != nil {
			mark(x.Spread, false, deferred)
		}
		for _, fld := range x.Fields {
			mark(fld.Value, false, deferred)
		}
	case *ast.RangeLit:
		mark(x.Start, false, deferred)
		mark(x.End, false, deferred)
	case *ast.FieldAccess:
		mark(x.Object, false, deferred)
	case *ast.Unary:
		mark(x.Right, false, deferred)
	case *ast.StringInterp:
		for _, part := range x.Parts {
			if se, ok := part.(ast.StringExpr); ok {
				mark(se.Expr, false, deferred)
			}
		}
	case *ast.If:
		mark(x.Cond, false, deferred)
		mark(x.Then, tail, deferred)
		mark(x.Else, tail, deferred)
	case *ast.Case:
		mark(x.Value, false, deferred)
		for _, br := range x.Branches {
			mark(br.Guard, false, deferred)
			mark(br.Body, tail, deferred)
		}
	case *ast.Lambda:
		// Lambda body has its own tail context — outer `tail` does not cross.
		// The body's last stmt is the lambda's return value.
		markBlock(x.Body, true, false)
	case *ast.FuncDef:
		// A nested `fn` statement is a function of its own: its body is a
		// full tail context, and the declaration itself has no value.
		markBlock(x.Body, true, false)
	case *ast.TestDecl:
		// A test body, a group's boot and setup: not functions, so walked
		// with tail false; a `group`'s nested cases arrive here too.
		mark(x.Boot, false, deferred)
		mark(x.Setup, false, deferred)
		markBlock(x.Body, false, deferred)
	case *ast.OnceBinding:
		mark(x.Value, false, deferred)
	case *ast.With:
		// A statement: its value is never the block's.
		mark(x.Value, false, deferred)
	case *ast.Subtests:
		// A statement whose body runs once per row and answers nothing.
		mark(x.Rows, false, deferred)
		mark(x.Name, false, deferred)
		markBlock(x.Body, false, deferred)
	case *ast.Defer:
		mark(x.Call, false, deferred)
	case *ast.Unwrap:
		// `f(x)!` tests the call's result after it returns, so the call is
		// never a tail call.
		mark(x.Expr, false, deferred)
	case *ast.TryOp:
		// `?` performs a pattern-match between the call's return and the
		// function's return; sub-expr is never in tail position.
		mark(x.Expr, false, deferred)
	case *ast.Dbg:
		mark(x.Expr, false, deferred)
	case *ast.Then:
		mark(x.Fn(), false, deferred)
	case *ast.Tap:
		mark(x.Fn(), false, deferred)
	case *ast.Return:
		// `return expr` always exits the enclosing function, so expr is in
		// tail position regardless of whether the surrounding context was.
		mark(x.Value, true, deferred)
	case *ast.Binary:
		switch x.Op {
		case "|>", "and", "or":
			// RHS is the Binary's value when LHS short-circuits / pipes through.
			mark(x.Left, false, deferred)
			mark(x.Right, tail, deferred)
		default:
			mark(x.Left, false, deferred)
			mark(x.Right, false, deferred)
		}
	}
}

// markBlock marks calls inside a block. tail=true means the block's last
// stmt is in tail position relative to the enclosing function/lambda.
//
// deferred is true when a `defer` registered earlier in a block around b,
// within the same function, is still pending; a `defer` statement in b
// makes it true for the statements after it.
func markBlock(b *ast.Block, tail, deferred bool) {
	if b == nil {
		return
	}
	for i, stmt := range b.Stmts {
		isLast := i == len(b.Stmts)-1
		mark(stmt, tail && isLast, deferred)
		if _, isDefer := stmt.(*ast.Defer); isDefer {
			deferred = true
		}
	}
}
