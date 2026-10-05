package irbuild

import (
	"reflect"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// testSetupChain lowers a case's group `setup` and its pattern ahead of its
// body, reporting whether they are in the shape.
//
// The setup's statements run in the case's own activation, so a `with` or a
// `defer` in it holds through the body. Nothing the setup binds is visible to
// the case: its names are dropped and their symbols forgotten when it ends,
// and only its value flows on. The case's pattern then binds that value into
// the body's scope. A setup that ends in a statement rather than an
// expression answers Unit. A `return value` in the setup ends it with that
// value (testSetupReturning).
func (bl *irScalarBuilder) testSetupChain(c testCaseDecl) bool {
	ctx, ctxK := ir.NoTemp, kindUnit
	for _, s := range c.setups {
		if s.body == nil {
			continue
		}
		bound, boundK, syms := bl.testScopeSave()
		var v ir.Temp
		var k kind
		var ok bool
		if astHasReturn(s.body) {
			v, k, ok = bl.testSetupReturning(s)
		} else {
			v, k, ok = bl.testSetupBody(s.body)
		}
		bl.bound, bl.boundK, bl.sh.syms = bound, boundK, syms
		if !ok {
			return false
		}
		ctx, ctxK = v, k
	}
	if c.ctxPat != nil {
		return bl.testDestructure(c.ctxPat, ctx, ctxK)
	}
	return true
}

// testScopeSave copies the builder's name tables, so a setup's names can be
// dropped when it ends.
func (bl *irScalarBuilder) testScopeSave() (map[string]ir.Temp, map[string]kind, map[string]*ir.Symbol) {
	bound := make(map[string]ir.Temp, len(bl.bound))
	for n, v := range bl.bound {
		bound[n] = v
	}
	boundK := make(map[string]kind, len(bl.boundK))
	for n, k := range bl.boundK {
		boundK[n] = k
	}
	syms := make(map[string]*ir.Symbol, len(bl.sh.syms))
	for n, s := range bl.sh.syms {
		syms[n] = s
	}
	return bound, boundK, syms
}

// testSetupBody lowers a group's `setup` body: its leading statements as a
// test body's, then its final expression, whose value is the setup's value,
// or Unit when it ends in a statement.
func (bl *irScalarBuilder) testSetupBody(body ast.Node) (ir.Temp, kind, bool) {
	block, isBlock := body.(*ast.Block)
	if !isBlock {
		v, k, _, ok := bl.lower(body)
		return v, k, ok
	}
	if len(block.Stmts) == 0 {
		return bl.testSetupUnit(block)
	}
	last := len(block.Stmts) - 1
	for _, s := range block.Stmts[:last] {
		if !bl.testStmt(s) {
			return ir.NoTemp, kindInvalid, false
		}
	}
	tail, isExpr := block.Stmts[last].(*ast.ExprStmt)
	if !isExpr || isNilNode(tail.Expr) {
		if !bl.testStmt(block.Stmts[last]) {
			return ir.NoTemp, kindInvalid, false
		}
		return bl.testSetupUnit(block.Stmts[last])
	}
	if line, _ := nodePos(tail); !emittableLine(line) {
		return ir.NoTemp, kindInvalid, false
	}
	v, k, _, ok := bl.lower(tail.Expr)
	return v, k, ok
}

// testSetupUnit is the Unit a setup ending in a statement answers.
func (bl *irScalarBuilder) testSetupUnit(at ast.Node) (ir.Temp, kind, bool) {
	u := ir.NewUnit(bl.g.irNodePos(at), bl.f.NewTemp())
	bl.b.Append(u)
	bl.side(u.Dst(), irScalarSide{k: kindUnit})
	return u.Dst(), kindUnit, true
}

// testDestructure binds a case's pattern from its group's setup value: any
// irrefutable pattern, as a destructuring parameter binds one. The checker
// has rejected a refutable pattern.
//
// The projections and binds are built where the setup left the cursor, by the
// parameter destructuring (gen.destructure) a lambda's prologue uses, with
// the cursor's block standing in for the prologue's.
func (bl *irScalarBuilder) testDestructure(pat ast.Node, ctx ir.Temp, k kind) bool {
	if ctx == ir.NoTemp {
		irDeclineNote("a test pattern over a setup with no value")
		return false
	}
	g, sh := bl.g, bl.sh
	entry, at, prologue, patternOK := sh.entry, sh.at, sh.prologue, sh.patternOK
	sh.entry = bl.b
	if sh.at == nil {
		sh.at = pat
	}
	start := len(bl.b.Instrs())
	mark := len(g.errs)
	g.pushScope()
	closePrologue := g.irPrologueOpen(sh)
	ok := g.destructure(pat, expr{t: ctx, k: k}, pat)
	closePrologue(ok)
	g.popScope()
	sh.entry, sh.at, sh.prologue, sh.patternOK = entry, at, prologue, patternOK
	if len(g.errs) != mark {
		// A refusal g.destructure recorded is this case's decline.
		g.errs = g.errs[:mark]
		ok = false
	}
	if !ok {
		irDeclineNote("a test pattern the builder did not destructure")
		return false
	}
	for _, in := range bl.b.Instrs()[start:] {
		b, isBind := in.(*ir.Bind)
		if !isBind {
			continue
		}
		bk := sh.frame.kindOf(b.Dst())
		if !irCallableValueKind(bk) && !irRetainedValueKind(bk) {
			irDeclineNote("a test pattern name's kind outside the domain: " + bk.nomi())
			return false
		}
		bl.side(b.Dst(), irScalarSide{k: bk})
		bl.bound[b.Sym().Name()], bl.boundK[b.Sym().Name()] = b.Dst(), bk
	}
	return true
}

// irSetupExit is where a group's `setup` that holds a `return` leaves: the
// block after the setup, and the slot each exit writes the setup's value
// into (NoTemp when the value is Unit). frames are the block and arm scopes
// the lowering is inside, outermost first, which a `return` closes on its
// way out.
type irSetupExit struct {
	exit   *ir.Block
	slot   ir.Temp
	k      kind
	frames []irSetupFrame
}

// irSetupFrame is one scope inside a setup: its deferred calls and its
// `with` writes end where a `return` leaves it.
type irSetupFrame struct {
	defers *irDeferScope
	with   *irWithScope
}

// pushSetupFrame records a block or arm scope a setup's `return` must close,
// and answers the function that drops it again.
func (bl *irScalarBuilder) pushSetupFrame(defers *irDeferScope, with *irWithScope) func() {
	se := bl.setupExit
	if se == nil {
		return func() {}
	}
	se.frames = append(se.frames, irSetupFrame{defers: defers, with: with})
	return func() { se.frames = se.frames[:len(se.frames)-1] }
}

// testSetupReturning lowers a `setup` that holds a `return`. The setup is a
// run of test statements in value position: its final value, the final value
// of each arm or block that is that position, and every `return value` copy
// the setup's value into one slot and jump to the block after the setup,
// whose type is the checker's join of them all (analysis.TestGroupSetupType).
func (bl *irScalarBuilder) testSetupReturning(s testSetup) (ir.Temp, kind, bool) {
	group, _ := s.group.(*ast.TestDecl)
	k := kindUnit
	if ty := analysis.TestGroupSetupType(bl.g.fa, group); ty != nil {
		k = bl.g.project(ty)
	}
	if k != kindUnit && !irCallableValueKind(k) && !irRetainedValueKind(k) {
		irDeclineNote("a setup whose value's kind is outside the domain: " + k.nomi())
		return ir.NoTemp, kindInvalid, false
	}
	pos := bl.g.irNodePos(s.body)
	se := &irSetupExit{exit: bl.f.NewBlock(pos, "setup-exit"), slot: ir.NoTemp, k: k}
	if k != kindUnit {
		ty := bl.g.irTypeOf(k)
		if ty == nil {
			irDeclineNote("a setup whose value has no IR type: " + k.nomi())
			return ir.NoTemp, kindInvalid, false
		}
		se.slot = bl.f.NewTemp()
		bl.b.Append(ir.NewSlot(pos, se.slot, ty))
	}
	stmts := []ast.Node{s.body}
	if block, isBlock := s.body.(*ast.Block); isBlock {
		stmts = block.Stmts
	} else {
		line, col := nodePos(s.body)
		stmts = []ast.Node{&ast.ExprStmt{Expr: s.body, Line: line, Col: col}}
	}
	prevExit, prevTail := bl.setupExit, bl.testTail
	bl.setupExit, bl.testTail = se, true
	ok := len(stmts) == 0 || bl.testStmts(stmts)
	bl.setupExit, bl.testTail = prevExit, prevTail
	if !ok {
		return ir.NoTemp, kindInvalid, false
	}
	// A setup that ends in a statement falls through with Unit; one whose
	// value is not Unit cannot reach here, and Lint holds the slot to that.
	bl.b.SetTerm(ir.NewJump(pos, se.exit.ID()))
	bl.b = se.exit
	if k == kindUnit {
		return bl.testSetupUnit(s.body)
	}
	bl.side(se.slot, irScalarSide{k: k})
	return se.slot, k, true
}

// setupValue lowers a value that ends a setup: its final value, or a
// `return`'s. The value is copied into the setup's slot (or dropped, for a
// Unit setup), each scope between here and the setup is closed, and control
// jumps past the setup. The builder continues in a fresh block no edge
// reaches, as after a test body's `return`.
func (bl *irScalarBuilder) setupValue(at, value ast.Node) bool {
	se := bl.setupExit
	if bl.recording > 0 {
		irDeclineNote("a setup's value inside an assertion subject")
		return false
	}
	if !isNilNode(value) {
		var val ir.Temp
		var k kind
		var ok bool
		if se.k == kindUnit {
			val, k, _, ok = bl.lower(value)
		} else {
			val, k, _, ok = bl.lowerWant(value, se.k)
		}
		if !ok {
			return false
		}
		switch {
		case se.slot == ir.NoTemp:
			bl.irStatementDrop(value, val, k)
		case k != se.k:
			irDeclineNote("a setup value whose kind is not the setup's: " + k.nomi() + " vs " + se.k.nomi())
			return false
		default:
			bl.b.Append(ir.NewCopy(bl.g.irNodePos(value), se.slot, val))
		}
	} else if se.slot != ir.NoTemp {
		irDeclineNote("a bare return in a setup whose value is not Unit")
		return false
	}
	for i := len(se.frames) - 1; i >= 0; i-- {
		bl.closeDefers(se.frames[i].defers, at)
		bl.closeWithScope(se.frames[i].with, at)
	}
	pos := bl.g.irNodePos(at)
	bl.b.SetTerm(ir.NewJump(pos, se.exit.ID()))
	bl.b = bl.f.NewBlock(pos, "after-setup-value")
	return true
}

// astHasReturn reports whether n holds a `return` that leaves n itself: one
// outside every lambda and nested fn in it, whose returns are their own.
func astHasReturn(n ast.Node) bool {
	found := false
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		if found || !v.IsValid() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer, reflect.Interface:
			if v.IsNil() {
				return
			}
			switch v.Interface().(type) {
			case *ast.Return:
				found = true
				return
			case *ast.Lambda, *ast.FuncDef:
				return
			}
			walk(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				if v.Type().Field(i).IsExported() {
					walk(v.Field(i))
				}
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		}
	}
	walk(reflect.ValueOf(n))
	return found
}
