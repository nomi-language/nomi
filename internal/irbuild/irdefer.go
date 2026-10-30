package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
	"maps"
)

// irDeferScope is one lexical block's deferred calls, in registration order.
// Its runs are appended at that block's exit.
type irDeferScope struct {
	block *ast.Block
	ids   []int
	// outer is the scope that was innermost when this one opened, so an
	// `Iter.loop` iteration can run every scope it opened (loopJump).
	outer *irDeferScope
	// scoped is true when the block holds its own app-field writes: the
	// running scope was read at its entry and is restored at its exit.
	scoped bool
}

// openDeferScope makes block the scope the next `leading` run registers
// deferred calls in, or answers nil where this builder admits none.
func (bl *irScalarBuilder) openDeferScope(block *ast.Block) *irDeferScope {
	if !bl.deferOK {
		return nil
	}
	scope := &irDeferScope{block: block, outer: bl.openDefers}
	bl.deferScope = scope
	bl.openDefers = scope
	return scope
}

// keepOpenDefers answers a func that makes the open scopes what they are now.
// A lowering that opens a block's scope defers it, so the scope leaves the
// chain however the block's lowering ends, its exit lowered or not.
func (bl *irScalarBuilder) keepOpenDefers() func() {
	saved := bl.openDefers
	return func() { bl.openDefers = saved }
}

// closeDefers appends the scope's runs, most recent first, after the last
// statement's value is routed.
func (bl *irScalarBuilder) closeDefers(scope *irDeferScope, at ast.Node) {
	if scope == nil {
		return
	}
	for i := len(scope.ids) - 1; i >= 0; i-- {
		bl.b.Append(ir.NewRunDefer(bl.g.irNodePos(at), scope.ids[i]))
	}
}

// deferCall lowers `defer call(...)`: the call is lowered at the defer site
// and only the call itself is deferred.
//
// The spec evaluates a deferred call's operands where the `defer` stands, so
// whatever computes them (another call, a `case` or `if`, a widening, a
// default) stays where it was lowered, and the ir.Defer that replaces the
// call reads their temporaries there. A void Go target is declined.
func (bl *irScalarBuilder) deferCall(t *ast.Defer, scope *irDeferScope) bool {
	call, isCall := t.Call.(*ast.Call)
	switch {
	case scope == nil:
		irDeclineNote("a defer outside the own statements of a body, a block or a loop callback")
		return false
	case !isCall:
		irDeclineNote("a defer of something other than a call")
		return false
	case bl.g.ctrl != nil:
		irDeclineNote("a defer inside a loop")
		return false
	}
	entry := bl.b
	start := len(entry.Instrs())
	val, _, _, ok := bl.lower(call)
	if !ok {
		return false
	}
	instrs := bl.b.Instrs()
	if (bl.b == entry && len(instrs) == start) || len(instrs) == 0 || instrs[len(instrs)-1].Dst() != val {
		irDeclineNote("a deferred call that does not lower to one call")
		return false
	}
	c, isIRCall := instrs[len(instrs)-1].(*ir.Call)
	if !isIRCall || int(c.Dst()) >= len(bl.sides) || !bl.sides[c.Dst()].deferrable {
		irDeclineNote("a deferred call to a function that answers no value")
		return false
	}
	bl.defers++
	bl.b.DeferLastCall(bl.g.irNodePos(t), bl.defers)
	scope.ids = append(scope.ids, bl.defers)
	bl.irDiscardStmtUnit(t)
	return true
}

// scopedBlock lowers a block statement, `{ ... }`, with no destination: a Go
// name scope around its statements, each lowered as a
// leading statement, then its deferred calls. Its names do not escape, and a
// `with` among its statements holds to its end. Outside a named function's
// body (a lambda's, say) the block admits no `defer`: openDeferScope answers
// no scope there, and deferCall declines.
//
// Its statements may branch. A `return` in a nested arm leaves the
// activation, which runs the block's registered deferred calls and drops its
// overrides on the way out; the block's normal exit is the block its last
// statement ends in, where its deferred calls run and its overrides are
// restored.
func (bl *irScalarBuilder) scopedBlock(block *ast.Block) bool {
	defer bl.keepOpenDefers()()
	defer bl.g.enterBlockTypes(block)()
	lead, tail := bl.g.irScalarBlock(block, "")
	if tail == nil {
		irDeclineNote(irDeclineBodyWhy)
		return false
	}
	bound, boundK, syms := bl.bound, bl.boundK, bl.sh.syms
	bl.bound, bl.boundK, bl.sh.syms = maps.Clone(bound), maps.Clone(boundK), maps.Clone(syms)
	defer func() { bl.bound, bl.boundK, bl.sh.syms = bound, boundK, syms }()
	scope := bl.openDeferScope(block)
	ws := bl.openWithScope(false)
	if !bl.leading(append(lead, tail)) {
		return false
	}
	bl.closeDefers(scope, block)
	bl.closeWithScope(ws, block)
	return true
}

// irScopeHandle is the identity token of a block's saved scope: its handle
// type and the declaration a RefScope reads and a StoreScope restores.
type irScopeHandle struct{}

// irWithScope is one lexical block's `with` statements: an override holds
// from its statement to the end of the block that contains it.
//
// A root scope is an activation's own body (a function's, a lambda's, a test
// case's, or a block every one of whose paths leaves the activation): the
// activation's end restores what it replaced, so nothing is saved. Any other
// block reads the scope in force before its first `with` and restores it at
// its normal exit, after its deferred calls; an exit that leaves the
// activation (a `return`, a `?`, a failure) restores it by leaving.
type irWithScope struct {
	root   bool
	opened bool
	saved  ir.Temp
}

// openWithScope makes a fresh scope the one the next `leading` run lowers its
// `with` statements in, as openDeferScope does for `defer`, and answers it for
// the block's exit. A `with` in a run no site opened a scope for declines,
// since nothing would restore it.
func (bl *irScalarBuilder) openWithScope(root bool) *irWithScope {
	ws := &irWithScope{root: root, saved: ir.NoTemp}
	bl.withScope = ws
	return ws
}

// closeWithScope restores, at a block's normal exit, the scope its first
// `with` saved.
func (bl *irScalarBuilder) closeWithScope(ws *irWithScope, at ast.Node) {
	if ws == nil || !ws.opened {
		return
	}
	bl.b.Append(ir.NewStoreScope(bl.g.irNodePos(at), bl.g.irTypes().Symbol(irScopeHandle{}, "$scope"), ws.saved))
}

// withStmt lowers the statement `with MyApp.field = value` in scope ws: the
// scope in force is saved before a non-root block's first `with`, then the
// value is evaluated and stored for the rest of the block.
func (bl *irScalarBuilder) withStmt(w *ast.With, ws *irWithScope) bool {
	switch {
	case ws == nil:
		irDeclineNote("a `with` in a block whose exit the builder does not restore")
		return false
	case bl.inTest && !bl.testApp:
		irDeclineNote("an app-field write in a test case with no boot")
		return false
	}
	if !ws.root && !ws.opened {
		ws.saved = bl.f.NewTemp()
		bl.f.SetType(ws.saved, ir.NewHandleType(bl.g.irTypes().Symbol(irScopeHandle{}, "Scope")))
		bl.b.Append(ir.NewRefScope(bl.g.irNodePos(w), ws.saved, bl.g.irTypes().Symbol(irScopeHandle{}, "$scope")))
		ws.opened = true
	}
	return bl.appFieldStore(w)
}
