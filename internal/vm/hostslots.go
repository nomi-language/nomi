package vm

import (
	"errors"

	"github.com/nomi-language/nomi/hostadapt"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// Crossings over registers. A generated adapter has a register form
// (hostadapt.RegFunc) beside its boxed one: the same call over its operands
// and its answer held unboxed in a hostadapt.Slots, as registers hold them
// (hostadapt package doc). A crossing that lands on one copies each operand
// from its register into the register stack's slots (frame.slotOf, which a
// record's construction reads its fields by), calls the form, and copies the
// answer's parts into its destination's registers (frame.writeSlot, which a
// field read writes by): an Int that fits int64, a Float, a Bool, a Byte, a
// String and a Bytes never box, a tuple answered into a split destination
// (tuplesplit.go) builds no record, and a fused Maybe or Result
// (maybefuse.go) takes its payload into the payload's register, so it builds
// no Some and no Ok. An operand held in another bank is boxed out and taken
// apart, so every operand reaches the form, by the rule fill follows.
//
// A destination that keeps the answer whole (a Maybe or a Result kept as
// its record, a tuple kept whole) gets the record built from the parts
// (hostadapt.Regs.Answer), as the adapter over boxed values builds it, so
// its operands still cross unboxed. A debug or profiling machine's
// crossing runs the adapter over boxed values, through hookedCallHost,
// which records them. A table WithHostLookup answers has no register forms, and
// neither does a host's own table unless internal/hostgen generated it: an
// embedder's adapters (vmhost.WithHosts) are called over boxed values as
// they always are.

// bindRegs gives crossing h the register form it runs through, and reports
// whether it has one (else it runs its adapter boxed).
func (cp *compiler) bindRegs(h *hostCall, fused bool) bool {
	h.regs, h.whole = cp.regsAt(h.name, h.args, h.dst, fused)
	h.direct = h.regs != nil && cp.directSlots(h.args, h.regs.In)
	return h.regs != nil
}

// regsAt is the register form a crossing to name with operands args and
// destination dst runs through, or nil for one that runs its adapter boxed,
// and whether dst keeps the answer whole. fused says the destination is a
// fused Maybe or Result, which takes a carrying form's payload: its one
// part, or a tuple's parts into its payload's (hostMaybe). A tuple's components go straight into a split destination whose
// registers hold them; any other destination keeps the whole answer,
// built from the parts.
func (cp *compiler) regsAt(name string, args []ir.Temp, dst ir.Temp, fused bool) (*hostadapt.Regs, bool) {
	r, err := cp.m.adapterRegs(name)
	if err != nil || r == nil || len(r.In) != len(args) {
		return nil, false
	}
	if fused {
		if !r.Carries {
			return nil, false
		}
		return r, false
	}
	if r.Carries || len(r.Out) <= 1 {
		return r, r.Carries
	}
	ls, splits := cp.split[dst]
	if !splits || len(ls) != len(r.Out) {
		return r, true
	}
	for i, l := range ls {
		if !bankHolds(l.bank(), r.Out[i]) {
			return r, true
		}
	}
	return r, false
}

// directSlots reports whether every operand in args lives in a register
// that holds it as its slot type in does, and none is kept in registers
// of its own, so callSlots copies each straight from its register.
func (cp *compiler) directSlots(args []ir.Temp, in []rt.SlotType) bool {
	for i, t := range args {
		if cp.c.packed != nil && cp.c.packed[t].kind != packNone || !bankHolds(cp.loc(t).bank(), in[i]) {
			return false
		}
	}
	return true
}

// bankHolds reports whether a register of bank b holds a value of slot type
// st as the slot does.
func bankHolds(b bank, st rt.SlotType) bool {
	switch b {
	case bankW:
		return st.Bank() == rt.BankWord
	case bankS:
		return st.Bank() == rt.BankStr
	case bankR:
		return st == rt.SlotRef
	}
	return false
}

// slotMismatch is slotOf's failure for a value that is not of the slot's
// kind, which only a graph built by hand hands a crossing.
type slotMismatch string

func (e slotMismatch) Error() string { return string(e) }

// callSlots runs crossing h through its register form: its operands from
// their registers into the stack's slots, and the form's call. The answer
// is in the slots for the caller to take (writeAnswer, hostMaybe), which
// clears them. ran is false, and nothing called, when an operand is not of
// its slot's kind: the boxed adapter refuses it in its own words.
func (fr *frame) callSlots(h *hostCall) (carried, ran bool, err error) {
	sl := &fr.stk.slots
	if h.direct {
		// Every operand's register holds it as its slot does (directSlots).
		stk, locs := fr.stk, fr.c.locs
		for i, t := range h.args {
			switch l := locs[t]; l.bank() {
			case bankW:
				j := fr.wb + l.reg()
				w := stk.w[j]
				sl.W[i] = w
				if w == rt.BigWord && h.regs.In[i] == rt.SlotInt {
					sl.Big[i], _ = stk.b[j].(*rt.BigInt)
				}
			case bankS:
				sl.S[i] = stk.s[fr.sb+l.reg()]
			default:
				sl.R[i] = stk.r[fr.rb+l.reg()]
			}
		}
		carried, err = callRegs(h.regs.Fn, fr.runtime, sl)
		if err != nil {
			fr.clearSlots(h)
		}
		return carried, true, err
	}
	for i, t := range h.args {
		w, big, s, r, err := fr.slotOf(t, h.regs.In[i])
		if err != nil {
			sl.Clear(i)
			var mismatch slotMismatch
			if errors.As(err, &mismatch) {
				return false, false, nil
			}
			return false, true, err
		}
		sl.W[i], sl.Big[i], sl.S[i], sl.R[i] = w, big, s, r
	}
	carried, err = callRegs(h.regs.Fn, fr.runtime, sl)
	if err != nil {
		fr.clearSlots(h)
	}
	return carried, true, err
}

// clearSlots drops what crossing h left in the stack's slots.
func (fr *frame) clearSlots(h *hostCall) {
	fr.stk.slots.Clear(max(len(h.args), len(h.regs.Out)))
}

// writeAnswer writes the answer a register form left in the slots into
// crossing h's destination: the whole answer for one that keeps it, else
// nothing for Unit, its one part, or a tuple's components into the
// destination's split registers.
func (fr *frame) writeAnswer(h *hostCall, carried bool) {
	sl := &fr.stk.slots
	out := h.regs.Out
	switch {
	case h.whole:
		fr.write(h.dst, h.regs.Answer(sl, carried))
	case len(out) == 0:
		fr.write(h.dst, rt.Unit{})
	case len(out) == 1:
		fr.writeSlot(h.dst, out[0], sl.W[0], sl.Big[0], sl.S[0], sl.R[0])
	default:
		// regsAt keeps a tuple answer for a split destination whose
		// components' banks are the parts'.
		for i, l := range fr.c.packed[h.dst].parts {
			fr.setLoc(l, out[i], sl.W[i], sl.Big[i], sl.S[i], sl.R[i])
		}
	}
	fr.clearSlots(h)
}

// setLoc writes a value held as slot type st into register l when l's bank
// holds it as the slot does, and reports whether it did.
func (fr *frame) setLoc(l loc, st rt.SlotType, w uint64, big *rt.BigInt, s string, r any) bool {
	if !bankHolds(l.bank(), st) {
		return false
	}
	switch l.bank() {
	case bankW:
		fr.stk.setWord(fr.wb+l.reg(), w, bigValue(big))
	case bankS:
		fr.stk.s[fr.sb+l.reg()] = s
	default:
		fr.stk.r[fr.rb+l.reg()] = r
	}
	return true
}
