package analysis

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
)

// TaskNotSettledCode tags Rule 2's error: a handle bound from `Task.spawn`
// or `Task.spawn_all` in a `concurrent` block that nothing in the block
// awaits or cancels. Args holds the binding's name.
const TaskNotSettledCode = "task-not-settled"

// CheckTaskLifetime enforces Rules 2 and 3 of the task lifetime rules
// (spec §16 *Static guarantees*):
//
//	Rule 2: a handle bound from `Task.spawn`, or a list of handles bound
//	from `Task.spawn_all`, inside a `concurrent` block is settled in that
//	block: awaited or cancelled. A reference settles it only in a settling
//	position (settleWalk); any other use, `Task.inspect(t)`, `u = [t]`,
//	`_ = t`, a call of a function the program declares, does not. An
//	unsettled binding is the TaskNotSettledCode error at the binding.
//
//	Rule 3: a `Task<T>` value (or any composite containing one) cannot
//	escape its enclosing `concurrent` block. The block's tail expression
//	is the escape vector: if its syntactic shape produces a Task value (a
//	direct `Task.spawn(...)` call, a reference to a Task binding declared
//	inside the block, or a tuple / list / struct literal containing any of
//	the above), it is the error
//	`"Task<T> cannot escape its enclosing concurrent block"`.
//
// Both rules are local to a single `ConcurrentBlock`: the only producers
// of block-owned Task values are `Task.spawn` and `Task.spawn_all` (Rule 1
// already holds every such call inside some block), so a trace within the
// block from the spawn to its uses is sufficient. A handle from
// `Supervisor.spawn` is owned by its supervisor and is not tracked.
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

// checkBlock applies Rules 2 and 3 to one ConcurrentBlock. Rule 2 finds
// the block's Task bindings and reports each one the settle walk does not
// settle. Rule 3 examines the block's tail expression for a Task-producing
// shape.
func (tl *taskLifetime) checkBlock(cb *ast.ConcurrentBlock) {
	if cb.Body == nil || len(cb.Body.Stmts) == 0 {
		return
	}
	taskBindings := tl.collectTaskBindings(cb.Body)
	if len(taskBindings) > 0 {
		w := tl.settle(cb.Body)
		for _, tb := range taskBindings {
			if tb.sym != nil && w.settled[tb.sym] {
				continue
			}
			tl.errors = append(tl.errors, TypeError{
				Line:    tb.line,
				Col:     tb.col,
				Message: tb.notSettled(w.uses[tb.sym]),
				Code:    TaskNotSettledCode,
				Args:    []string{tb.name},
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

// taskBinding records one binding declared inside a ConcurrentBlock whose
// value is a `Task.spawn` or `Task.spawn_all` call: its name, the symbol
// the checker defined for it, and whether it holds a list of handles.
type taskBinding struct {
	name string
	sym  *Symbol
	many bool
	line int
	col  int
}

// notSettled is Rule 2's message for tb, naming the first use the block
// makes of it when there is one.
func (tb taskBinding) notSettled(use handleUse) string {
	noun, verb, it := "Task", "is", "it"
	if tb.many {
		noun, verb, it = "Tasks", "are", "them"
	}
	msg := fmt.Sprintf("%s bound to '%s' %s never awaited or cancelled", noun, tb.name, verb)
	if use.line > 0 {
		var did string
		switch {
		case use.callee != "":
			did = fmt.Sprintf("passes %s to `%s`", it, use.callee)
		case use.binding == "_":
			did = fmt.Sprintf("discards %s with `_ =`", it)
		case use.binding != "":
			did = fmt.Sprintf("copies %s to `%s`", it, use.binding)
		default:
			did = "reads " + it
		}
		msg += fmt.Sprintf(": line %d %s, which neither awaits nor cancels %s", use.line, did, it)
	}
	return msg + fmt.Sprintf("; pass %s to `Task.await`, `Task.await_all`, `Task.outcome` or `Task.cancel`", it)
}

// collectTaskBindings collects the `name = Task.spawn(...)` and
// `name = Task.spawn_all(...)` bindings, the call written plain or as a
// pipe's last stage, anywhere in a ConcurrentBlock's body: in nested
// blocks, if and case branches, and a block on a binding's right-hand
// side. A pattern binding (`(a, b) = ...`, `Id(n) = ...`) is not tracked;
// it does not produce a meaningful "is this binding settled?" question
// without per-element type tracking.
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
				value := node.Value
				if pipe, ok := value.(*ast.Binary); ok && pipe.Op == "|>" {
					value = pipe.Right
				}
				if tl.isSpawnCall(value) {
					bindings = append(bindings, taskBinding{
						name: node.Name,
						sym:  tl.fa.Definitions[Pos{Line: node.Line, Col: node.Col}],
						many: isTaskFunctionSymbol(tl.fa, tl.calleeSymbol(value.(*ast.Call).Func), "spawn_all"),
						line: node.Line,
						col:  node.Col,
					})
				}
			}
			return true
		})
	}
	return bindings
}

// settleWalk is Rule 2's one pass over a concurrent block's body. It finds
// the symbols some reference settles, and the first other use of each.
//
// A reference settles what it names in a settling position, which is any
// part of:
//
//   - an argument of a call of a settling function (isTaskSettleSymbol):
//     `Task.await(t)`, `Task.await_all([a, b])`;
//   - an argument of a call where another argument is a settling function,
//     or a lambda or capture that settles its own parameter:
//     `Iter.each(handles, Task.cancel)`,
//     `Iter.each(handles, fn t -> _ = Task.outcome(t))`;
//   - the left of a `|>` whose stage is either of those calls, or names a
//     settling function: `handles |> Iter.map(Task.outcome)`.
//
// A lambda settles its parameter when the walk of its body settles one of
// its parameters; a capture, when one of its `&N` stands in a settling
// position. The walk enters lambdas and nested concurrent blocks, so a
// reference there settles an outer handle too, and skips nested FuncDefs,
// which are scopes of their own. It does not depend on the order of the
// block's statements.
type settleWalk struct {
	fa *FileAnalysis
	// settled holds each symbol a reference settles.
	settled map[*Symbol]bool
	// settlesParam holds each Lambda or Capture that settles its own
	// parameter.
	settlesParam map[ast.Node]bool
	// uses holds, per symbol, its earliest reference outside a settling
	// position, for the message.
	uses map[*Symbol]handleUse
}

// handleUse is one reference that does not settle what it names: where it
// is and what holds it, the call it is an argument of or the binding it is
// the value of.
type handleUse struct {
	line, col int
	callee    string
	binding   string
}

// settle runs the settle walk over a concurrent block's body.
func (tl *taskLifetime) settle(body *ast.Block) *settleWalk {
	w := &settleWalk{
		fa:           tl.fa,
		settled:      make(map[*Symbol]bool),
		settlesParam: make(map[ast.Node]bool),
		uses:         make(map[*Symbol]handleUse),
	}
	w.walk(body, false, handleUse{}, nil)
	return w
}

// walk visits n once. settling says n is in a settling position, use is
// what holds n for a use's message, and capture is the capture whose `&N`
// n's holes are.
func (w *settleWalk) walk(n ast.Node, settling bool, use handleUse, capture *ast.Capture) {
	switch node := n.(type) {
	case nil:
		return
	case *ast.FuncDef:
		return
	case *ast.Ident:
		sym := w.fa.References[Pos{Line: node.Line, Col: node.Col}]
		if sym == nil {
			return
		}
		if settling {
			w.settled[sym] = true
			if lam, ok := paramOwner(sym).(*ast.Lambda); ok {
				w.settlesParam[lam] = true
			}
			return
		}
		if prev, ok := w.uses[sym]; !ok || node.Line < prev.line || node.Line == prev.line && node.Col < prev.col {
			use.line, use.col = node.Line, node.Col
			w.uses[sym] = use
		}
		return
	case *ast.PlaceholderParam, *ast.Placeholder:
		if settling && capture != nil {
			w.settlesParam[capture] = true
		}
		return
	case *ast.FieldAccess:
		// A field's name is not a reference to a binding.
		w.walk(node.Object, settling, use, capture)
		return
	case *ast.Binding:
		w.walk(node.Value, settling, handleUse{binding: node.Name}, capture)
		return
	case *ast.Lambda:
		// `&N` inside a lambda is not the enclosing capture's.
		if node.Body != nil {
			w.walk(node.Body, settling, handleUse{}, nil)
		}
		return
	case *ast.Capture:
		w.walk(node.Body, settling, handleUse{}, node)
		return
	case *ast.Call:
		w.walkCall(node, settling, use, capture)
		return
	case *ast.Binary:
		if node.Op == "|>" {
			stageSettles := w.isSettler(node.Right)
			stage := handleUse{callee: calleeText(node.Right)}
			if call, ok := node.Right.(*ast.Call); ok {
				stageSettles = w.walkCall(call, settling, use, capture)
				stage.callee = calleeText(call.Func)
			} else {
				w.walk(node.Right, settling, use, capture)
			}
			w.walk(node.Left, settling || stageSettles, stage, capture)
			return
		}
	}
	ast.Children(n, func(c ast.Node) {
		w.walk(c, settling, use, capture)
	})
}

// walkCall visits a call and reports whether it settles its arguments:
// its callee is a settling function, or another argument is one or is a
// lambda or capture that settles its parameter. Function arguments are
// walked first, since what their bodies settle decides the rest.
func (w *settleWalk) walkCall(call *ast.Call, settling bool, use handleUse, capture *ast.Capture) bool {
	settles := w.isSettler(call.Func)
	isFn := make([]bool, len(call.Args))
	for i, arg := range call.Args {
		v := arg
		if named, ok := v.(*ast.NamedArg); ok {
			v = named.Value
		}
		switch v.(type) {
		case *ast.Lambda, *ast.Capture:
			isFn[i] = true
			w.walk(v, settling, use, capture)
			settles = settles || w.settlesParam[v]
		default:
			settles = settles || w.isSettler(v)
		}
	}
	w.walk(call.Func, settling, use, capture)
	argUse := handleUse{callee: calleeText(call.Func)}
	for i, arg := range call.Args {
		if !isFn[i] {
			w.walk(arg, settling || settles, argUse, capture)
		}
	}
	return settles
}

// isSettler reports whether n names one of the functions that settle a
// task (isTaskSettleSymbol), as a callee or as a value.
func (w *settleWalk) isSettler(n ast.Node) bool {
	pos, ok := calleeKey(n)
	if !ok {
		return false
	}
	return isTaskSettleSymbol(w.fa, resolvedSymbol(w.fa.References[pos]))
}

// isTaskSettleSymbol reports whether sym is one of std's functions that
// settle every task they are given: they await it, or cancel it, or both
// (`Task.try_await_all` awaits each task and, at the first `Err`, cancels
// the rest and waits for them to stop). The other functions std/tasks
// declares over a task do neither: `Debug.inspect` renders a handle, and
// `Task.race` and `Task.within` take bodies, not tasks.
func isTaskSettleSymbol(fa *FileAnalysis, sym *Symbol) bool {
	for _, name := range [...]string{"await", "await_all", "try_await_all", "outcome", "cancel"} {
		if isTaskFunctionSymbol(fa, sym, name) {
			return true
		}
	}
	return false
}

// paramOwner is the function or lambda sym is a parameter of, a name a
// destructured parameter binds included, and nil for any other symbol.
func paramOwner(sym *Symbol) ast.Node {
	switch {
	case sym.Kind == SymbolParam:
		return sym.Node
	case sym.ParamOf != nil:
		return sym.ParamOf
	}
	return nil
}

// isSpawnCall reports whether `n` is a Call to `Task.spawn` or
// `Task.spawn_all`, the two block-owned spawns.
//
// Both bind something that must be settled before the block closes:
// `spawn` a `Task<T>`, `spawn_all` a `List<Task<T>>`. The callee is
// decided by the symbol the checker resolved it to.
func (tl *taskLifetime) isSpawnCall(n ast.Node) bool {
	call, ok := n.(*ast.Call)
	if !ok {
		return false
	}
	return isTaskSpawnSymbol(tl.fa, tl.calleeSymbol(call.Func))
}

// calleeSymbol is the symbol the checker resolved a callee to, through
// any selective import or alias, or nil.
func (tl *taskLifetime) calleeSymbol(fn ast.Node) *Symbol {
	pos, ok := calleeKey(fn)
	if !ok {
		return nil
	}
	return resolvedSymbol(tl.fa.References[pos])
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
//     `concurrent { Task.spawn(fn -> 42) |> some_transform }`. The pipe is a
//     Binary node at the AST level; structurally it desugars to
//     `some_transform(Task.spawn(fn -> 42))`, but reasoning about whether
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
			if tb.name == node.Name {
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
		for _, item := range node.Operands() {
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
	case *ast.StructLit:
		return node.Line, node.Col
	case *ast.If:
		return node.Line, node.Col
	case *ast.With:
		return node.Line, node.Col
	case *ast.Subtests:
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
