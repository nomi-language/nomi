package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
	"maps"
)

// guardReturn retains a function's conditional exit. The false edge
// continues the surrounding block; the true arm returns from this activation.
// guardShape reports whether a leading `if` is a guard: no `else`, and a
// then-block ending in `return` (or a signalling callback's `break` or
// `continue`). It emits nothing, so a leading `if` of another shape can be
// lowered as a statement region instead.
func (bl *irScalarBuilder) guardShape(t *ast.If) bool {
	// kindInvalid: sentinel — only ordinary lambdas may infer an unset result.
	if bl.inTest || (bl.returnKind == kindInvalid && bl.inferReturn == nil) || t.Else != nil || t.CondPattern != nil || isNilNode(t.Cond) {
		return false
	}
	_, tail := bl.g.irScalarBlock(t.Then, "")
	_, isReturn := tail.(*ast.Return)
	_, isBreak := tail.(*ast.Break)
	_, isContinue := tail.(*ast.Continue)
	return isReturn || (bl.ctl != irCtlNone && (isBreak || isContinue))
}

func (bl *irScalarBuilder) guardReturn(t *ast.If) bool {
	if !bl.guardShape(t) {
		return false
	}
	defer bl.g.enterBlockTypes(t.Then)()
	lead, tail := bl.g.irScalarBlock(t.Then, "")
	ret, isReturn := tail.(*ast.Return)
	cond, ck, _, ok := bl.lower(t.Cond)
	if !ok || ck != kindBool {
		return false
	}
	arm := bl.f.NewBlock(bl.g.irNodePos(t.Then), "return")
	next := bl.f.NewBlock(bl.g.irNodePos(t), "next")
	br := ir.NewBranch(bl.g.irNodePos(t.Cond), cond, arm.ID(), next.ID())
	bl.b.SetTerm(br)
	bound, boundK, syms := bl.bound, bl.boundK, bl.sh.syms
	bl.bound, bl.boundK, bl.sh.syms = maps.Clone(bound), maps.Clone(boundK), maps.Clone(syms)
	defer func() { bl.bound, bl.boundK, bl.sh.syms = bound, boundK, syms }()
	bl.b = arm
	// Every path through the arm leaves the activation, which restores
	// what a `with` in it replaced.
	bl.openWithScope(true)
	if !bl.leading(lead) || bl.b != arm {
		return false
	}
	if isReturn {
		if _, ok := bl.explicitReturn(ret); !ok {
			return false
		}
	} else if !bl.ctlReturn(tail) {
		return false
	}
	bl.b = next
	bl.irDiscardStmtUnit(t)
	return true
}

// explicitReturn is shared by conditional exits and the final statement.
// It preserves the returned operand and the cursor after evaluating it.
func (bl *irScalarBuilder) explicitReturn(ret *ast.Return) (kind, bool) {
	val, k, ok := ir.NoTemp, kindUnit, true
	if !isNilNode(ret.Value) {
		// kindInvalid: sentinel — the first lambda return establishes its result.
		if bl.returnKind == kindInvalid {
			val, k, _, ok = bl.lower(ret.Value)
		} else {
			val, k, _, ok = bl.lowerWant(ret.Value, bl.returnKind)
		}
	}
	if ok && bl.inferReturn != nil {
		bl.returnKind, ok = bl.inferReturn.settle(k)
	}
	if !ok || k != bl.returnKind {
		return kindInvalid, false
	}
	r := ir.NewReturnUnit(bl.g.irNodePos(ret))
	if !isNilNode(ret.Value) {
		r = ir.NewReturn(bl.g.irNodePos(ret), val)
	}
	bl.b.SetTerm(r)
	return k, true
}

// irCtlForm is the signalling convention a callback is lowered under, which
// its enclosing Iter call decides: the reduce form `(acc, bool)` or the
// adapter form `(value, rt.Ctl)`.
type irCtlForm uint8

const (
	irCtlNone irCtlForm = iota
	irCtlReduce
	irCtlAdapter
)

// ctlReturn lowers a `break` or `continue` that ends a guard arm of a
// signalling callback, as breakStmt and continueStmt do: in a reduce, a bare
// `break` stops with the unchanged accumulator and `continue` keeps going
// with it; in an adapter, `continue` skips and a bare `break` stops. `break v`
// answers v and stops in both.
func (bl *irScalarBuilder) ctlReturn(n ast.Node) bool {
	pos := bl.g.irNodePos(n)
	var r *ir.Return
	switch t := n.(type) {
	case *ast.Continue:
		if bl.ctl == irCtlReduce {
			r = ir.NewReturn(pos, bl.ctlAcc)
		} else {
			r = ir.NewReturnCtl(pos, ir.NoTemp, ir.CtlSkip)
		}
	case *ast.Break:
		if isNilNode(t.Value) {
			if bl.ctl == irCtlReduce {
				r = ir.NewReturnCtl(pos, bl.ctlAcc, ir.CtlEmitStop)
			} else {
				r = ir.NewReturnCtl(pos, ir.NoTemp, ir.CtlStop)
			}
			break
		}
		var val ir.Temp
		var k kind
		var ok bool
		// kindInvalid: sentinel — the first answer establishes an inferred result.
		if bl.returnKind == kindInvalid {
			val, k, _, ok = bl.lower(t.Value)
		} else {
			val, k, _, ok = bl.lowerWant(t.Value, bl.returnKind)
		}
		if ok && bl.inferReturn != nil {
			bl.returnKind, ok = bl.inferReturn.settle(k)
		}
		if !ok || k != bl.returnKind {
			return false
		}
		r = ir.NewReturnCtl(pos, val, ir.CtlEmitStop)
	default:
		return false
	}
	bl.b.SetTerm(r)
	return true
}

// irCtlTailGuard reads a signalling callback's tail `if c { break v } else {
// e }` as the guard `if c { break v }` followed by e's statements, which is
// the same program: the then arm leaves the callback and the else arm is what
// runs otherwise. guardReturn then lowers the signal as it lowers one written
// as a guard. Any other tail is returned unchanged.
func (g *gen) irCtlTailGuard(lead []ast.Node, tail ast.Node) ([]ast.Node, ast.Node) {
	t, isIf := tail.(*ast.If)
	if !isIf || t.CondPattern != nil || isNilNode(t.Cond) {
		return lead, tail
	}
	els, isBlock := t.Else.(*ast.Block)
	if !isBlock || g.blockTypes[els] != nil {
		// An else block that declares types keeps its own scope.
		return lead, tail
	}
	_, thenTail := g.irScalarBlock(t.Then, "")
	switch thenTail.(type) {
	case *ast.Break, *ast.Continue:
	default:
		return lead, tail
	}
	elseLead, elseTail := g.irScalarBlock(els, "")
	if elseTail == nil {
		return lead, tail
	}
	guard := *t
	guard.Else = nil
	out := make([]ast.Node, 0, len(lead)+1+len(elseLead))
	out = append(out, lead...)
	out = append(out, &guard)
	out = append(out, elseLead...)
	return out, elseTail
}
