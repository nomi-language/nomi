package vm

import (
	"sync/atomic"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// # CALLS WHOSE CALLEE IS A VALUE
//
// A call through a function value (`f(acc, x)`) and a dispatched call on an
// interface value (`Weigh.weigh(w)`) name their callee only at run time,
// but their operands' stored types are static, so each operand already sits
// in the bank its parameter takes. Such a call is opCallV: the loop finds
// the callee's bytecode (valueCallee) and enters it at `enter`, the code a
// direct call's opCall enters its callee by: the operands moved from the
// caller's registers into the callee's window, a function value's captures
// written after them (writeCaptures), and the answer delivered into the
// destination's register. Nothing is boxed and no operand slice is made.
//
// A dispatched call keeps the descriptor of the receiver it last selected
// for and the body it selected, so a site whose receivers repeat a type
// selects with one pointer compare. It keeps the body's function table
// entry, as a direct call keeps its callee's, so an impl a REPL input
// redefines (Machine.Redirect) is the one it calls next.
//
// The handler (callInstr) makes every other such call: a tail call, which
// transfers (tail.go); a function value whose body is Go (a driver's
// `yield`); one that keeps app fields other than the caller's, whose entry
// may start a deadline (callValue); a receiver that is a box, which the
// selected body may open (unboxSelves), or no record; a callee whose
// parameters take a bank an operand does not sit in; and every call on a
// debug or profiling machine, whose hooks the handler's path runs.
// TestOpCosts_ACallThroughAValueAllocatesNothing counts the allocations.

// valueCall is one opCallV site.
type valueCall struct {
	call *ir.Call
	// args are the operands' locations, in order.
	args []uint32
	// fn is the function value's reference register, or -1 for a
	// dispatched call, whose receiver is in reference register recv.
	fn, recv int
	// handler is the index of the call in the code's handler table.
	handler uint32
	// hit is a dispatched call's last selection.
	hit atomic.Pointer[dispatchHit]
}

// dispatchHit is the body a dispatched call selected for a receiver of
// descriptor desc.
type dispatchHit struct {
	desc *rt.TypeDesc
	slot *fnSlot
}

// valueCall compiles n, a call through a function value or a dispatched
// call, to opCallV, and reports whether it did.
func (cp *compiler) valueCall(n *ir.Call) bool {
	if cp.c.tails[n] {
		return false
	}
	vc := &valueCall{call: n, fn: -1}
	if n.Form() == ir.CalleeIndirect {
		if !cp.inBank(bankR, n.Fn()) {
			return false
		}
		vc.fn = cp.loc(n.Fn()).reg()
	} else {
		k := n.KeyAt()
		if k < 0 || k >= n.NumArgs() || !cp.inBank(bankR, n.Arg(k)) {
			return false
		}
		vc.recv = cp.loc(n.Arg(k)).reg()
	}
	vc.args = make([]uint32, n.NumArgs())
	for i := range vc.args {
		t := n.Arg(i)
		if t == ir.NoTemp || int(t) >= len(cp.c.locs) || (cp.c.packed != nil && cp.c.packed[t].kind != packNone) {
			return false
		}
		vc.args[i] = uint32(cp.c.locs[t])
	}
	vc.handler = cp.handlerIndex(n)
	cp.c.vcalls = append(cp.c.vcalls, vc)
	cp.emit(cp.op(opCallV, uint32(len(cp.c.vcalls)-1)))
	return true
}

// valueCallee is the bytecode an opCallV site calls and the captures
// written after its operands, or false when the handler makes the call (the
// file comment says when).
func (m *Machine) valueCallee(fr *frame, vc *valueCall, r []any) (*code, []any, bool) {
	if m.hooked {
		return nil, nil, false
	}
	var callee *code
	var caps []any
	if vc.fn >= 0 {
		f, ok := r[vc.fn].(*functionValue)
		if !ok || f.host != nil || f.arity != len(vc.args) || (f.kept != nil && f.kept != rt.KeptFields(fr.runtime)) {
			return nil, nil, false
		}
		callee, caps = m.codeOf(f.fnSlot(m)), f.captures
	} else {
		rec, ok := r[vc.recv].(*rt.Record)
		if !ok || rec == nil || rec.Desc.Kind == rt.KindBoxed {
			return nil, nil, false
		}
		hit := vc.hit.Load()
		if hit == nil || hit.desc != rec.Desc {
			impl := implOf(m.dispatch[vc.call.Callee()], rec)
			if impl == nil {
				return nil, nil, false
			}
			fn, err := m.resolveFunc(impl, fr.fn.Name())
			if err != nil {
				return nil, nil, false
			}
			hit = &dispatchHit{desc: rec.Desc, slot: m.slotFor(fn)}
			vc.hit.Store(hit)
		}
		callee = m.codeOf(hit.slot)
	}
	if len(callee.params) != len(vc.args)+len(caps) || len(callee.params) != len(callee.fn.Params()) {
		return nil, nil, false
	}
	for i, a := range vc.args {
		if callee.params[i].bank() != loc(a).bank() {
			return nil, nil, false
		}
	}
	return callee, caps, true
}

// writeCaptures writes a function value's captures, boxed, into the
// parameters after its n operands, in the window of fr, an activation not
// yet started. A capture its parameter cannot hold closes the window and is
// the call's error.
func (fr *frame) writeCaptures(n int, caps []any) error {
	params := fr.fn.Params()
	for i, v := range caps {
		fr.writeBoxed(params[n+i].Temp, v)
	}
	if fr.bad != nil {
		fr.stk.pop(fr)
		return fr.bad
	}
	return nil
}
