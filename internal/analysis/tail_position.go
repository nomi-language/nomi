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
// Tail position does NOT cross:
//   - lambda bodies — a lambda has its own tail context, walked from scratch
//     with tail=true at its boundary, regardless of the surrounding context
//   - default-value expressions (evaluated before the body runs; the top-level
//     loop only enters function bodies, never Param.Default)
//   - the value expression of a `with` binding or a `defer` call
//   - the sub-expression of `try` — the keyword performs a pattern-match
//     between the call's return and the function's return, so the call inside
//     `try expr` is not in tail position
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
			markBlock(x.Body, true)
		case *ast.ImplBlock:
			// Impl-block methods are FuncDefs nested in Items; each body is a
			// full tail context just like a top-level fn. (ExternFunc items
			// have no body.) Without this, TCO never fires inside an `impl`
			// block — deep tail recursion through a block-form interface-impl
			// method (`Iface.method` dispatch) grows the host stack and
			// overflows, while the decorator form was marked fine.
			for _, item := range x.Items {
				if fn, ok := item.(*ast.FuncDef); ok {
					markBlock(fn.Body, true)
				}
			}
		case *ast.TestDecl, *ast.OnceBinding:
			// Neither is a function, so nothing in it is a tail call of its
			// own; the nested `fn`s and lambdas it holds are functions, and
			// mark gives each its own tail context.
			mark(x, false)
		}
	}
}

// mark walks a single expression node `n`. `tail` is true iff n's value
// (or, for statement-shaped nodes, the value of the last sub-expression)
// is the value of the enclosing function/lambda body.
func mark(n ast.Node, tail bool) {
	if n == nil {
		return
	}
	switch x := n.(type) {
	case *ast.Call:
		if tail {
			x.IsTailCall = true
		}
		// Sub-expressions of a Call are never in tail position.
		mark(x.Func, false)
		for _, a := range x.Args {
			mark(a, false)
		}
	case *ast.ExprStmt:
		// ExprStmt is a pass-through wrapper around an expression — function
		// bodies always wrap their final expression in one.
		mark(x.Expr, tail)
	case *ast.GroupedExpr:
		mark(x.Expr, tail)
	case *ast.Binding:
		// Binding evaluates to Unit; its RHS is never tail.
		mark(x.Value, false)
	case *ast.TupleDestructure:
		mark(x.Value, false)
	case *ast.StructDestructure:
		mark(x.Value, false)
	case *ast.MapDestructure:
		mark(x.Value, false)
	case *ast.DistinctDestructure:
		mark(x.Value, false)
	case *ast.PatternBinding:
		// The else's value, if any, is bound, never returned; a `return`
		// inside it marks its own operand.
		mark(x.Value, false)
		if x.Else != nil {
			markBlock(x.Else.Block, false)
			for _, arm := range x.Else.Arms {
				mark(arm.Guard, false)
				mark(arm.Body, false)
			}
		}
	case *ast.NamedArg:
		mark(x.Value, false)
	case *ast.Block:
		markBlock(x, tail)
	case *ast.ListLit:
		for _, item := range x.Items {
			mark(item, false)
		}
	case *ast.VectorLit:
		for _, item := range x.Items {
			mark(item, false)
		}
	case *ast.SetLit:
		for _, item := range x.Items {
			mark(item, false)
		}
	case *ast.ListSpreadLit:
		for _, h := range x.Heads {
			mark(h, false)
		}
		mark(x.TailSpread, false)
	case *ast.TupleLit:
		for _, item := range x.Items {
			mark(item, false)
		}
	case *ast.MapLit:
		for _, e := range x.Entries {
			mark(e.Key, false)
			mark(e.Value, false)
		}
	case *ast.StructLit:
		if x.Spread != nil {
			mark(x.Spread, false)
		}
		for _, fld := range x.Fields {
			mark(fld.Value, false)
		}
	case *ast.RangeLit:
		mark(x.Start, false)
		mark(x.End, false)
	case *ast.FieldAccess:
		mark(x.Object, false)
	case *ast.Unary:
		mark(x.Right, false)
	case *ast.StringInterp:
		for _, part := range x.Parts {
			if se, ok := part.(ast.StringExpr); ok {
				mark(se.Expr, false)
			}
		}
	case *ast.If:
		mark(x.Cond, false)
		mark(x.Then, tail)
		mark(x.Else, tail)
	case *ast.Case:
		mark(x.Value, false)
		for _, br := range x.Branches {
			mark(br.Guard, false)
			mark(br.Body, tail)
		}
	case *ast.Lambda:
		// Lambda body has its own tail context — outer `tail` does not cross.
		// The body's last stmt is the lambda's return value.
		markBlock(x.Body, true)
	case *ast.FuncDef:
		// A nested `fn` statement is a function of its own: its body is a
		// full tail context, and the declaration itself has no value.
		markBlock(x.Body, true)
	case *ast.TestDecl:
		// A test body, a group's boot and setup: not functions, so walked
		// with tail false; a `tests` group's nested cases arrive here too.
		mark(x.Boot, false)
		mark(x.Setup, false)
		markBlock(x.Body, false)
	case *ast.OnceBinding:
		mark(x.Value, false)
	case *ast.With:
		// A statement: its value is never the block's.
		mark(x.Value, false)
	case *ast.Defer:
		mark(x.Call, false)
	case *ast.TryOp:
		// `try` performs a pattern-match between the call's return and the
		// function's return; sub-expr is never in tail position.
		mark(x.Expr, false)
	case *ast.Dbg:
		mark(x.Expr, false)
	case *ast.Then:
		mark(x.Lambda, false)
	case *ast.Tap:
		mark(x.Lambda, false)
	case *ast.Return:
		// `return expr` always exits the enclosing function, so expr is in
		// tail position regardless of whether the surrounding context was.
		mark(x.Value, true)
	case *ast.Binary:
		switch x.Op {
		case "|>", "and", "or":
			// RHS is the Binary's value when LHS short-circuits / pipes through.
			mark(x.Left, false)
			mark(x.Right, tail)
		default:
			mark(x.Left, false)
			mark(x.Right, false)
		}
	}
}

// markBlock marks calls inside a block. tail=true means the block's last
// stmt is in tail position relative to the enclosing function/lambda.
func markBlock(b *ast.Block, tail bool) {
	if b == nil {
		return
	}
	for i, stmt := range b.Stmts {
		isLast := i == len(b.Stmts)-1
		mark(stmt, tail && isLast)
	}
}
