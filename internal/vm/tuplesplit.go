package vm

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// A split tuple: a tuple the function builds and only reads components of,
// as `case (a, b)` does with its subject, is kept as its components in
// registers instead of a record. The construction moves each operand into a
// register of the operand's bank, and each component read moves it out, so
// the tuple allocates nothing and a component read boxes nothing.
//
// The components are moved, not read from the operands' own registers at
// the projection, because an operand's register can be written again between
// the two (a loop's next iteration).
//
// A tuple splits when valueGroups stands for it (every use is a component
// read, a copy into its register or the function's return) and every read
// lands in the bank its component lives in. A boxed read of the tuple (a
// stopped frame's variables) gets it rebuilt (frame.readPacked), so a debug
// machine splits as a plain one does.
//
// A split tuple crosses calls in parts. One the function returns is
// returned by opRetP, which copies its components into the register
// stack's return scratch (regStack.setRetParts) instead of building the
// record. A tuple a call answers splits in the caller as one a Make
// builds: deliver moves the components from the scratch into its
// registers, so `(a, b) = pair(i)` allocates nothing. Every other reader of
// a return in parts gets the record (regStack.ret): a caller that keeps the
// value boxed, a call from Go, the debugger, and an activation whose
// deferred calls run after its return (Machine.finish boxes first, since
// they return on the same stack). A tail call needs nothing: the callee's
// return is the caller's. A record arriving where the caller keeps the
// tuple split (a callee that built one, a host, a handler) is taken apart
// into the registers (frame.unpack).

// splitTuples picks the tuples the function keeps as components and gives
// each component a register. It runs after assignRegisters.
func (cp *compiler) splitTuples() {
	makes := map[ir.Temp]*ir.Make{}
	calls := map[ir.Temp]*ir.ValType{}
	slots := cp.returnSlots()
	root, groups := cp.valueGroups(func(in ir.Instr) bool {
		if call, isCall := in.(*ir.Call); isCall {
			// A tuple a call answers, which deliver or write moves into
			// the components' registers.
			ty := cp.typeOf(call.Dst())
			if ty == nil || ty.Kind() != ir.KindTuple || ty.NumElems() == 0 {
				return false
			}
			calls[call.Dst()] = ty
			return true
		}
		n, isMake := in.(*ir.Make)
		if !isMake || n.Kind() != ir.MakeTuple || n.Arity() == 0 {
			return false
		}
		makes[n.Dst()] = n
		return true
	}, func(in ir.Instr) ir.Temp {
		switch n := in.(type) {
		case *ir.Proj:
			if n.Kind() == ir.ProjSlot {
				return n.Subject()
			}
		case *ir.Copy:
			if slots[n.Dst()] {
				return n.Src()
			}
		case *ir.Bind:
			if slots[n.Dst()] {
				return n.Src()
			}
		}
		return ir.NoTemp
	}, true, slots)
	if len(groups) == 0 {
		return
	}
	// arity and part are a group's component count and component i's type,
	// from its Make's operands or its call's tuple type.
	arity := func(g ir.Temp) int {
		if mk := makes[g]; mk != nil {
			return mk.Arity()
		}
		return calls[g].NumElems()
	}
	part := func(g ir.Temp, i int) *ir.ValType {
		if mk := makes[g]; mk != nil {
			return cp.typeOf(mk.Operand(i))
		}
		return calls[g].Elem(i)
	}
	// A read past the tuple, or into another bank, keeps the record.
	refused := map[ir.Temp]bool{}
	for _, b := range cp.c.fn.Blocks() {
		for _, in := range b.Instrs() {
			p, isProj := in.(*ir.Proj)
			if !isProj || p.Kind() != ir.ProjSlot {
				continue
			}
			g, isMember := root[p.Subject()]
			if !isMember {
				continue
			}
			if p.Index() >= arity(g) || cp.loc(p.Dst()).bank() != classOf(part(g, p.Index())) {
				refused[g] = true
			}
		}
	}
	parts := map[ir.Temp][]loc{}
	packs := map[ir.Temp]packedTemp{}
	for _, g := range groups {
		if refused[g] {
			continue
		}
		var desc *rt.TypeDesc
		if mk := makes[g]; mk != nil {
			plan, planned := planMake(mk, cp.c.types[g], cp.typeOf)
			if !planned || plan.desc == nil || plan.shared != nil {
				continue
			}
			desc = plan.desc
		} else if desc = descOfType(calls[g]); desc == nil {
			continue
		}
		types := make([]*ir.ValType, arity(g))
		for i := range types {
			types[i] = part(g, i)
		}
		ls := make([]loc, len(types))
		for i := range ls {
			b := classOf(types[i])
			if mk := makes[g]; mk != nil {
				b = cp.loc(mk.Operand(i)).bank()
			}
			switch b {
			case bankW:
				ls[i] = mkLoc(b, cp.c.nW)
				cp.c.nW++
			case bankS:
				ls[i] = mkLoc(b, cp.c.nS)
				cp.c.nS++
			case bankR:
				ls[i] = mkLoc(b, cp.c.nR)
				cp.c.nR++
			default:
				ls[i] = mkLoc(bankNone, 0)
			}
		}
		parts[g] = ls
		packs[g] = packedTemp{kind: packTuple, desc: desc, parts: ls, types: types}
	}
	cp.splitReturnSlots(slots, root, parts, packs)
	if len(parts) == 0 {
		return
	}
	cp.split = map[ir.Temp][]loc{}
	for t, g := range root {
		if ls, splits := parts[g]; splits {
			cp.split[t] = ls
			cp.pack(t, packs[g])
		}
	}
}

// returnSlots are the temporaries holding a tuple the function returns that
// only copies write and only returns read: `slot t2; ...; t2 = t7; return
// t2`, the form a body's value takes when each branch ends in its own
// tuple.
func (cp *compiler) returnSlots() map[ir.Temp]bool {
	f := cp.c.fn
	slots := map[ir.Temp]bool{}
	for _, b := range f.Blocks() {
		r, isRet := b.Term().(*ir.Return)
		if !isRet || r.Ctl() != ir.CtlNone || !r.HasVal() {
			continue
		}
		if ty := cp.typeOf(r.Val()); ty != nil && ty.Kind() == ir.KindTuple && ty.NumElems() > 0 && cp.loc(r.Val()).bank() == bankR {
			slots[r.Val()] = true
		}
	}
	if len(slots) == 0 {
		return nil
	}
	for _, p := range f.Params() {
		delete(slots, p.Temp)
	}
	var buf []ir.Temp
	for _, b := range f.Blocks() {
		for _, in := range b.Instrs() {
			if d := in.Dst(); slots[d] {
				switch in.(type) {
				case *ir.Copy, *ir.Bind:
				default:
					delete(slots, d)
				}
			}
			for _, t := range in.AppendUses(buf[:0]) {
				delete(slots, t)
			}
		}
		if r, isRet := b.Term().(*ir.Return); isRet && r.Ctl() == ir.CtlNone {
			continue
		}
		if t := b.Term(); t != nil {
			for _, u := range t.AppendUses(buf[:0]) {
				delete(slots, u)
			}
		}
	}
	return slots
}

// splitReturnSlots splits each return slot every copy into which moves a
// split tuple of its own layout, giving it components of its own, and keeps
// the record for the others, along with each tuple copied into one: their
// copies would have to build it.
func (cp *compiler) splitReturnSlots(slots map[ir.Temp]bool, root map[ir.Temp]ir.Temp, parts map[ir.Temp][]loc, packs map[ir.Temp]packedTemp) {
	if len(slots) == 0 {
		return
	}
	sources := map[ir.Temp][]ir.Temp{}
	for _, b := range cp.c.fn.Blocks() {
		for _, in := range b.Instrs() {
			var src, dst ir.Temp
			switch mv := in.(type) {
			case *ir.Copy:
				src, dst = mv.Src(), mv.Dst()
			case *ir.Bind:
				src, dst = mv.Src(), mv.Dst()
			default:
				continue
			}
			if slots[dst] {
				sources[dst] = append(sources[dst], src)
			}
		}
	}
	fits := func(r ir.Temp) bool {
		ty := cp.typeOf(r)
		if len(sources[r]) == 0 || descOfType(ty) == nil {
			return false
		}
		for _, src := range sources[r] {
			g, isMember := root[src]
			ls, splits := parts[g]
			if !isMember || !splits || len(ls) != ty.NumElems() {
				return false
			}
			for i, l := range ls {
				if l.bank() != classOf(ty.Elem(i)) {
					return false
				}
			}
		}
		return true
	}
	// A slot that does not fit keeps every tuple copied into it boxed, which
	// can unfit another slot those tuples reach.
	for changed := true; changed; {
		changed = false
		for r := range slots {
			if fits(r) {
				continue
			}
			for _, src := range sources[r] {
				if g, isMember := root[src]; isMember {
					if _, splits := parts[g]; splits {
						delete(parts, g)
						changed = true
					}
				}
			}
			delete(slots, r)
		}
	}
	for r := range slots {
		ty := cp.typeOf(r)
		types := make([]*ir.ValType, ty.NumElems())
		ls := make([]loc, len(types))
		for i := range types {
			types[i] = ty.Elem(i)
			switch b := classOf(types[i]); b {
			case bankW:
				ls[i] = mkLoc(b, cp.c.nW)
				cp.c.nW++
			case bankS:
				ls[i] = mkLoc(b, cp.c.nS)
				cp.c.nS++
			case bankR:
				ls[i] = mkLoc(b, cp.c.nR)
				cp.c.nR++
			default:
				ls[i] = mkLoc(bankNone, 0)
			}
		}
		root[r] = r
		parts[r] = ls
		packs[r] = packedTemp{kind: packTuple, desc: descOfType(ty), parts: ls, types: types}
	}
}

// splitUse compiles the construction of a split tuple and the reads of its
// components, and reports whether in was one.
func (cp *compiler) splitUse(in ir.Instr) bool {
	if cp.split == nil {
		return false
	}
	switch n := in.(type) {
	case *ir.Make:
		ls, splits := cp.split[n.Dst()]
		if !splits {
			return false
		}
		first := true
		for i, l := range ls {
			if l.bank() == bankNone {
				continue
			}
			if !first {
				cp.at(n.Pos(), false, -1)
			}
			first = false
			cp.moveReg(l, cp.loc(n.Operand(i)))
		}
		if first {
			cp.emit(cp.op(opNop, 0))
		}
		return true
	case *ir.Copy:
		return cp.splitCopy(n.Pos(), n.Dst(), n.Src())
	case *ir.Bind:
		return cp.splitCopy(n.Pos(), n.Dst(), n.Src())
	case *ir.Proj:
		ls, splits := cp.split[n.Subject()]
		if !splits || n.Kind() != ir.ProjSlot {
			return false
		}
		d := cp.loc(n.Dst())
		if d.bank() == bankNone {
			cp.emit(cp.op(opNop, 0))
			return true
		}
		cp.moveReg(d, ls[n.Index()])
		return true
	}
	return false
}

// splitCopy compiles a copy of one split tuple into a return slot split
// itself, component by component, and reports whether dst and src were
// those.
func (cp *compiler) splitCopy(pos ir.Pos, dst, src ir.Temp) bool {
	to, into := cp.split[dst]
	from, out := cp.split[src]
	if !into || !out || cp.c.packed[src].desc == nil {
		return false
	}
	if &to[0] == &from[0] {
		// A copy within one group (a binding the tuple is moved into).
		return false
	}
	first := true
	for i, l := range to {
		if l.bank() == bankNone {
			continue
		}
		if !first {
			cp.at(pos, false, -1)
		}
		first = false
		cp.moveReg(l, from[i])
	}
	if first {
		cp.emit(cp.op(opNop, 0))
	}
	return true
}

// moveReg copies register src into register dst of the same bank.
func (cp *compiler) moveReg(dst, src loc) {
	o := opMovR
	switch dst.bank() {
	case bankW:
		o = opMovW
	case bankS:
		o = opMovS
	}
	cp.emit(cp.op(o, uint32(dst.reg())), uint32(src.reg()))
}

// deliverParts moves the last return into split tuple t's registers: from
// the return scratch when the callee returned the tuple in parts, else out
// of the record it returned.
func (fr *frame) deliverParts(t ir.Temp) {
	s, p := fr.stk, &fr.c.packed[t]
	if s.rcls == retParts && len(s.rpack.parts) == len(p.parts) {
		same := true
		for i, l := range p.parts {
			same = same && l.bank() == s.rpack.parts[i].bank()
		}
		if same {
			for i, l := range p.parts {
				switch l.bank() {
				case bankW:
					s.setWord(fr.wb+l.reg(), s.rpw[i], s.rpb[i])
				case bankS:
					s.s[fr.sb+l.reg()] = s.rps[i]
				case bankR:
					s.r[fr.rb+l.reg()] = s.rpr[i]
				}
			}
			return
		}
	}
	fr.unpack(t, s.ret())
}

// unpack moves the components of tuple record v into split tuple t's
// registers.
func (fr *frame) unpack(t ir.Temp, v any) {
	p := &fr.c.packed[t]
	rec, ok := v.(*rt.Record)
	if !ok || rec == nil || rec.Desc.Kind != rt.KindTuple || len(rec.Desc.Fields) != len(p.parts) {
		fr.bad = fmt.Errorf("vm: %s: %s holds a %d-tuple and received %T", fr.fn.Name(), t, len(p.parts), v)
		return
	}
	for i, l := range p.parts {
		x := rec.Field(i)
		switch l.bank() {
		case bankW:
			w, big, ok := unboxWord(x)
			if !ok {
				fr.bad = fmt.Errorf("vm: %s: %s's component %d holds %s and received %T", fr.fn.Name(), t, i, p.types[i], x)
				return
			}
			fr.stk.setWord(fr.wb+l.reg(), w, big)
		case bankS:
			str, ok := unboxStr(x)
			if !ok {
				fr.bad = fmt.Errorf("vm: %s: %s's component %d holds %s and received %T", fr.fn.Name(), t, i, p.types[i], x)
				return
			}
			fr.stk.s[fr.sb+l.reg()] = str
		case bankR:
			fr.stk.r[fr.rb+l.reg()] = x
		}
	}
}
