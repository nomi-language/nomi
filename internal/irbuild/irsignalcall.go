package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// A named step's call in an `Iter.loop`.
//
// The checker admits an iter-sensitive function only as an iteration
// callback itself. Every driver but `Iter.loop` calls it from rt and reads
// its signal there; `Iter.loop` is inlined (iterLoop), so a named step,
// `Iter.loop(step, from: 0)`, is called from the loop's body, and that call
// is the one call of an iter-sensitive function the builder lowers. The
// callee's body is lowered in the adapter's signalling form (namedCtlForm),
// so its answer is a value or a control outcome; the call writes it whole
// into a temporary of type Any and the loop reads it (ir.CtlRead,
// internal/ir/ctl.go):
//
//	continue   the loop goes round again with its state unchanged
//	break      the loop ends with its state
//	break v    the loop ends with v
//	otherwise  the value is the next state

// iterSensitiveCall reports whether a call to decl must lower as
// signallingCall, and declines when it cannot: decl is iter-sensitive and
// the call is not a named step's call in the loop being lowered, which the
// checker rejects.
func (bl *irScalarBuilder) iterSensitiveCall(t *ast.Call, decl *ast.FuncDef, name string) (signalling, ok bool) {
	if !analysis.IterSensitive(decl) {
		return false, true
	}
	if t == nil || t != bl.signalCall || bl.loop == nil {
		irDeclineNote("a call to a function that uses break or continue, which the checker admits only as an iteration callback itself: " + name)
		return true, false
	}
	return true, true
}

// signallingCall emits a named step's call t to callee with args, whose
// declaration is decl and result kind result, and the reading of its
// answer, and answers the call's value.
func (bl *irScalarBuilder) signallingCall(t *ast.Call, decl *ast.FuncDef, callee *ir.Symbol, args []ir.Temp, result kind) (ir.Temp, kind, bool, bool) {
	pos := bl.g.irPos(t.Line, t.Col)
	c := ir.NewCall(pos, bl.f.NewTemp(), ir.OrdinaryCall, callee, args...)
	bl.b.Append(c)
	bl.f.SetType(c.Dst(), ir.AnyType)
	code := ir.NewCtlRead(pos, bl.f.NewTemp(), c.Dst(), ir.CtlPartCode)
	bl.b.Append(code)
	bl.side(code.Dst(), irScalarSide{k: kindInt})

	join := bl.f.NewBlock(pos, "call answered")
	signalled := bl.f.NewBlock(pos, "call signalled")
	bl.b.SetTerm(ir.NewBranch(pos, bl.ctlIs(pos, code.Dst(), ir.CtlNone), join.ID(), signalled.ID()))
	bl.b = signalled
	if !analysis.FuncIterSignals(decl).Break {
		// A callee with no `break` answers a value or its `continue`.
		bl.signalLeave(t, false)
	} else {
		skip := bl.f.NewBlock(pos, "call continued")
		other := bl.f.NewBlock(pos, "call broke")
		bl.b.SetTerm(ir.NewBranch(pos, bl.ctlIs(pos, code.Dst(), ir.CtlSkip), skip.ID(), other.ID()))
		bl.b = skip
		bl.signalLeave(t, false)

		bl.b = other
		stop := bl.f.NewBlock(pos, "call stopped")
		valued := bl.f.NewBlock(pos, "call broke with a value")
		bl.b.SetTerm(ir.NewBranch(pos, bl.ctlIs(pos, code.Dst(), ir.CtlStop), stop.ID(), valued.ID()))
		bl.b = stop
		bl.signalLeave(t, true)

		bl.b = valued
		if result == kindUnit {
			// `break` with a Unit answer is a bare `break`.
			bl.signalLeave(t, true)
		} else {
			v := ir.NewCtlRead(pos, bl.f.NewTemp(), c.Dst(), ir.CtlPartValue)
			bl.b.Append(v)
			bl.side(v.Dst(), irScalarSide{k: result})
			lp := bl.loop
			bl.loopCopy(t, lp.answer, v.Dst())
			bl.loopJump(bl.b, t, lp, lp.exit)
			lp.broke = true
		}
	}

	bl.b = join
	if result == kindUnit {
		u := ir.NewUnit(pos, bl.f.NewTemp())
		bl.b.Append(u)
		bl.side(u.Dst(), irScalarSide{k: kindUnit})
		return u.Dst(), kindUnit, false, true
	}
	v := ir.NewCtlRead(pos, bl.f.NewTemp(), c.Dst(), ir.CtlPartValue)
	bl.b.Append(v)
	bl.side(v.Dst(), irScalarSide{k: result})
	return v.Dst(), result, false, true
}

// ctlIs answers a Bool temporary: whether the control outcome code holds
// is want.
func (bl *irScalarBuilder) ctlIs(pos ir.Pos, code ir.Temp, want ir.Ctl) ir.Temp {
	k := ir.NewInt(pos, bl.f.NewTemp(), int64(want))
	bl.b.Append(k)
	bl.side(k.Dst(), irScalarSide{k: kindInt})
	eq := ir.NewCompare(pos, bl.f.NewTemp(), ir.OpEq, ir.ValInt, code, k.Dst())
	bl.b.Append(eq)
	bl.side(eq.Dst(), irScalarSide{k: kindBool})
	return eq.Dst()
}

// signalLeave ends the current block as a `continue` (brk false) or a bare
// `break` (brk true) in the loop's own body would.
func (bl *irScalarBuilder) signalLeave(at ast.Node, brk bool) {
	lp := bl.loop
	if !brk {
		bl.loopJump(bl.b, at, lp, lp.head)
		return
	}
	bl.loopCopy(at, lp.answer, lp.state)
	bl.loopJump(bl.b, at, lp, lp.exit)
	lp.broke = true
}
