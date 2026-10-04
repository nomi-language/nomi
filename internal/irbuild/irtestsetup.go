package irbuild

import (
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
// expression answers Unit.
func (bl *irScalarBuilder) testSetupChain(c testCaseDecl) bool {
	ctx, ctxK := ir.NoTemp, kindUnit
	for _, s := range c.setups {
		if s.body == nil {
			continue
		}
		bound, boundK, syms := bl.testScopeSave()
		v, k, ok := bl.testSetupBody(s.body)
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
