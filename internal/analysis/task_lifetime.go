package analysis

import (
	"fmt"
	"slices"

	"github.com/nomi-language/nomi/internal/ast"
)

// TaskNotSettledCode tags Rule 2's error: a handle bound from `Task.spawn`
// or `Task.spawn_all` in a `concurrent` block that nothing in the block
// awaits or cancels on every path to the block's end. Args holds the
// binding's name.
const TaskNotSettledCode = "task-not-settled"

// CheckTaskLifetime enforces Rules 2 and 3 of the task lifetime rules
// (spec §16 *Static guarantees*):
//
//	Rule 2: a handle bound from `Task.spawn`, or a list of handles bound
//	from `Task.spawn_all`, inside a `concurrent` block is settled in that
//	block: awaited or cancelled on every path that reaches the block's
//	end. A reference settles it only in a settling position (settleWalk);
//	any other use, `Task.inspect(t)`, `u = [t]`, `_ = t`, a call of a
//	function the program declares, does not, and neither does a settle
//	in one branch only or inside a lambda that captures the handle. An
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
		w := tl.settle(cb.Body, taskBindings)
		for _, tb := range taskBindings {
			if w.settled[tb.node] {
				continue
			}
			tl.errors = append(tl.errors, TypeError{
				Line:    tb.line,
				Col:     tb.col,
				Message: tb.notSettled(w.gaps[tb.sym], w.uses[tb.sym]),
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
// value is a `Task.spawn` or `Task.spawn_all` call: the binding, its name,
// the symbol the checker defined for it, and whether it holds a list of
// handles.
type taskBinding struct {
	node *ast.Binding
	name string
	sym  *Symbol
	many bool
	line int
	col  int
}

// notSettled is Rule 2's message for tb. It names a path that leaves the
// handle unsettled when the walk found one (gap), and otherwise the first
// use the block makes of it when there is one.
func (tb taskBinding) notSettled(gap settleGap, use handleUse) string {
	noun, verb, it := "Task", "is", "it"
	if tb.many {
		noun, verb, it = "Tasks", "are", "them"
	}
	fix := fmt.Sprintf("; pass %s to `Task.await`, `Task.await_all`, `Task.outcome` or `Task.cancel`", it)
	switch {
	case gap.path != "":
		return fmt.Sprintf("%s bound to '%s' %s not awaited or cancelled on every path: %s leaves %s neither awaited nor cancelled%s on that path too",
			noun, tb.name, verb, gap.path, it, fix)
	case gap.line > 0:
		return fmt.Sprintf("%s bound to '%s' %s never awaited or cancelled: line %d awaits or cancels %s only %s%s",
			noun, tb.name, verb, gap.line, it, gap.inside, fix)
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
	return msg + fix
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
						node: node,
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

// settleWalk is Rule 2's one pass over a concurrent block's body. Each
// part of the body answers what it settles on every path through it that
// reaches its end (settledSet), and a handle is settled when the block
// that declares it settles it on every such path.
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
//   - the left of a `|>` whose stage is either of those calls, names a
//     settling function, or is a lambda that settles its own parameter:
//     `handles |> Iter.map(Task.outcome)`.
//
// Paths are the ones the operand table gives (ast.EvaluatedOperands): what
// a node always evaluates settles what it settles, in order; an `if`
// settles what both branches settle, and a missing `else` settles
// nothing; a `case` settles what every arm settles; anything evaluated
// only sometimes or later (a lambda's body, the right of `and`, a
// `subtests` body) settles nothing outside it. A branch that always
// leaves its block (FileAnalysis.leaves: a `return`, `break` or
// `continue`, an `unreachable`, a call that never returns) asks nothing,
// and neither does the path a `?` takes out: every exit from a
// `concurrent` block but its end cancels the tasks still running in it
// (spec §16 *Errors and cancellation*). A lambda settles its parameter when
// its body settles it on every path, by the same rules; a capture, when
// one of its `&N` is in a settling position on every path. A nested
// concurrent block runs once in place, so it settles what its body
// settles. Nested FuncDefs are scopes of their own and are skipped.
type settleWalk struct {
	fa *FileAnalysis
	// handles holds the tracked handles' symbols, and declared maps each
	// tracked binding to its symbol, for the block that declares it.
	handles  map[*Symbol]bool
	declared map[*ast.Binding]*Symbol
	// settled holds each binding the block that declares it settles on
	// every path.
	settled map[*ast.Binding]bool
	// settlesParam holds each Lambda or Capture that settles its own
	// parameter.
	settlesParam map[ast.Node]bool
	// uses holds, per symbol, its earliest reference outside a settling
	// position, for the message.
	uses map[*Symbol]handleUse
	// gaps holds, per handle, the first part of the block the walk found
	// that settles it on some paths but not on all, for the message.
	gaps map[*Symbol]settleGap
}

// settledSet is what a part of a block settles on every path through it
// that reaches its end: the symbols of the handles, and each Lambda or
// Capture whose own parameter it settles.
type settledSet map[any]bool

// with adds what t settles to s and answers the result, which may be s.
func (s settledSet) with(t settledSet) settledSet {
	if len(t) == 0 {
		return s
	}
	if s == nil {
		s = settledSet{}
	}
	for k := range t {
		s[k] = true
	}
	return s
}

// settleGap is a part of the block that settles a handle on some paths and
// not on others: path names the branch that does not, or line and inside
// say where a settle stands that may not run.
type settleGap struct {
	path   string
	line   int
	inside string
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
func (tl *taskLifetime) settle(body *ast.Block, bindings []taskBinding) *settleWalk {
	w := &settleWalk{
		fa:           tl.fa,
		handles:      make(map[*Symbol]bool),
		declared:     make(map[*ast.Binding]*Symbol),
		settled:      make(map[*ast.Binding]bool),
		settlesParam: make(map[ast.Node]bool),
		uses:         make(map[*Symbol]handleUse),
		gaps:         make(map[*Symbol]settleGap),
	}
	for _, tb := range bindings {
		w.declared[tb.node] = tb.sym
		if tb.sym != nil {
			w.handles[tb.sym] = true
		}
	}
	w.walk(body, false, handleUse{}, nil)
	return w
}

// walk visits n once and answers what it settles on every path through
// it. settling says n is in a settling position, use is what holds n for
// a use's message, and capture is the capture whose `&N` n's holes are.
func (w *settleWalk) walk(n ast.Node, settling bool, use handleUse, capture *ast.Capture) settledSet {
	if ast.IsNilNode(n) {
		return nil
	}
	switch node := n.(type) {
	case *ast.FuncDef:
		return nil
	case *ast.Ident:
		sym := w.fa.References[Pos{Line: node.Line, Col: node.Col}]
		if sym == nil {
			return nil
		}
		if settling {
			s := settledSet{sym: true}
			if lam, ok := paramOwner(sym).(*ast.Lambda); ok {
				s[lam] = true
			}
			return s
		}
		if prev, ok := w.uses[sym]; !ok || node.Line < prev.line || node.Line == prev.line && node.Col < prev.col {
			use.line, use.col = node.Line, node.Col
			w.uses[sym] = use
		}
		return nil
	case *ast.PlaceholderParam, *ast.Placeholder:
		if settling && capture != nil {
			return settledSet{capture: true}
		}
		return nil
	case *ast.FieldAccess:
		// A field's name is not a reference to a binding.
		return w.walk(node.Object, settling, use, capture)
	case *ast.Binding:
		return w.walk(node.Value, settling, handleUse{binding: node.Name}, capture)
	case *ast.Lambda:
		// `&N` inside a lambda is not the enclosing capture's.
		body := w.walk(node.Body, settling, handleUse{}, nil)
		w.settlesParam[node] = body[node]
		w.onlySometimes(body, node.Line, "inside a lambda, which may never run")
		return nil
	case *ast.Capture:
		body := w.walk(node.Body, settling, handleUse{}, node)
		w.settlesParam[node] = body[node]
		w.onlySometimes(body, node.Line, "inside a capture, which may never run")
		return nil
	case *ast.Call:
		_, s := w.walkCall(node, settling, use, capture)
		return s
	case *ast.ConcurrentBlock:
		return w.walk(node.Body, settling, use, capture)
	case *ast.Block:
		var s settledSet
		for _, stmt := range node.Stmts {
			s = s.with(w.walk(stmt, settling, use, capture))
		}
		w.closeScope(node, s)
		return s
	case *ast.If:
		s := w.walk(node.Cond, settling, use, capture)
		w.walk(node.CondPattern, settling, use, capture)
		then := w.walk(node.Then, settling, use, capture)
		els := w.walk(node.Else, settling, use, capture)
		elsePath := fmt.Sprintf("the `else` of the `if` at line %d", node.Line)
		if node.Else == nil {
			elsePath = fmt.Sprintf("the missing `else` of the `if` at line %d", node.Line)
		}
		return s.with(w.join([]settleBranch{
			{then, fmt.Sprintf("the `then` branch of the `if` at line %d", node.Line), w.leaves(node.Then)},
			{els, elsePath, node.Else != nil && w.leaves(node.Else)},
		}))
	case *ast.Case:
		s := w.walk(node.Value, settling, use, capture)
		arms := make([]settleBranch, len(node.Branches))
		for i, br := range node.Branches {
			w.onlySometimes(w.walk(br.Pattern, settling, use, capture), br.Line, "in a `case` arm's condition")
			w.onlySometimes(w.walk(br.Guard, settling, use, capture), br.Line, "in a `case` arm's guard")
			arms[i] = settleBranch{
				set:    w.walk(br.Body, settling, use, capture),
				path:   fmt.Sprintf("the arm at line %d of the `case` at line %d", br.Line, node.Line),
				leaves: w.leaves(br.Body),
			}
		}
		return s.with(w.join(arms))
	case *ast.Binary:
		if node.Op == "|>" {
			stageSettles := w.isSettler(node.Right)
			stage := handleUse{callee: calleeText(node.Right)}
			var s settledSet
			if call, ok := node.Right.(*ast.Call); ok {
				stageSettles, s = w.walkCall(call, settling, use, capture)
				stage.callee = calleeText(call.Func)
			} else {
				s = w.walk(node.Right, settling, use, capture)
				stageSettles = stageSettles || w.settlesParam[node.Right]
			}
			return s.with(w.walk(node.Left, settling || stageSettles, stage, capture))
		}
	}
	now := ast.EvaluatedOperands(n)
	var s settledSet
	ast.Children(n, func(c ast.Node) {
		cs := w.walk(c, settling, use, capture)
		if evaluatedNow(now, c) {
			s = s.with(cs)
		} else {
			line, _ := ast.Position(c)
			w.onlySometimes(cs, line, "where it may not run")
		}
	})
	return s
}

// evaluatedNow reports whether c, a child of a node whose always-evaluated
// operands are now, is one of them; a named argument is its value.
func evaluatedNow(now []ast.Node, c ast.Node) bool {
	if named, ok := c.(*ast.NamedArg); ok {
		c = named.Value
	}
	return slices.Contains(now, c)
}

// walkCall visits a call and reports whether it settles its arguments:
// its callee is a settling function, or another argument is one or is a
// lambda or capture that settles its parameter. Function arguments are
// walked first, since what their bodies settle decides the rest. It also
// answers what the call settles on every path.
func (w *settleWalk) walkCall(call *ast.Call, settling bool, use handleUse, capture *ast.Capture) (bool, settledSet) {
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
	s := w.walk(call.Func, settling, use, capture)
	argUse := handleUse{callee: calleeText(call.Func)}
	for i, arg := range call.Args {
		if !isFn[i] {
			s = s.with(w.walk(arg, settling || settles, argUse, capture))
		}
	}
	return settles, s
}

// settleBranch is one branch of an `if` or a `case`: what it settles, the
// path it is for a message, and whether it always leaves its block.
type settleBranch struct {
	set    settledSet
	path   string
	leaves bool
}

// join answers what a branching construct settles on every path through
// it: what each branch that can reach its end settles. A handle some of
// those branches settle and others do not has a gap, at the first that
// does not.
func (w *settleWalk) join(branches []settleBranch) settledSet {
	var live []settleBranch
	for _, b := range branches {
		if !b.leaves {
			live = append(live, b)
		}
	}
	if len(live) == 0 {
		return nil
	}
	var out settledSet
	for k := range live[0].set {
		every := true
		for _, b := range live[1:] {
			every = every && b.set[k]
		}
		if every {
			out = out.with(settledSet{k: true})
		}
	}
	for _, b := range live {
		for k := range b.set {
			sym, ok := k.(*Symbol)
			if !ok || out[k] || !w.handles[sym] {
				continue
			}
			for _, other := range live {
				if !other.set[k] {
					w.gap(sym, settleGap{path: other.path})
					break
				}
			}
		}
	}
	return out
}

// onlySometimes records a gap for each handle s settles, where s is what
// a part settles that runs only sometimes, or later: line is where the
// part is and inside says what it is.
func (w *settleWalk) onlySometimes(s settledSet, line int, inside string) {
	for k := range s {
		if sym, ok := k.(*Symbol); ok && w.handles[sym] {
			w.gap(sym, settleGap{line: line, inside: inside})
		}
	}
}

// gap keeps the first gap the walk finds for sym.
func (w *settleWalk) gap(sym *Symbol, g settleGap) {
	if _, ok := w.gaps[sym]; !ok {
		w.gaps[sym] = g
	}
}

// leaves reports whether n always leaves its block, so that a path
// through it asks nothing (FileAnalysis.leaves).
func (w *settleWalk) leaves(n ast.Node) bool {
	return !ast.IsNilNode(n) && w.fa.leaves(n, true) != 0
}

// closeScope decides the handles block declares: each is settled when s,
// what block settles on every path to its end, holds it, or when no path
// reaches that end.
func (w *settleWalk) closeScope(block *ast.Block, s settledSet) {
	leaves := -1
	for _, stmt := range block.Stmts {
		b, ok := stmt.(*ast.Binding)
		if !ok {
			continue
		}
		sym, tracked := w.declared[b]
		if !tracked {
			continue
		}
		if sym != nil && s[sym] {
			w.settled[b] = true
			continue
		}
		if leaves < 0 {
			leaves = 0
			if w.leaves(block) {
				leaves = 1
			}
		}
		w.settled[b] = leaves == 1
	}
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
