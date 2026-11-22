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
//
// A Some or an Ok of a tuple the function builds only to return crosses the
// same way (wrappedReturns): the tuple splits, the Some is never built, and
// the return carries the components with the variant's descriptor
// (packedTemp.wrap), which a caller keeping the Maybe fused takes into its
// payload's registers (maybefuse.go) and any other reader gets built as
// the Some (regStack.ret). A body whose branches each end in a value of
// their own copies them into a return slot, which then holds a tag beside
// parts of its own: a copy of such a Some moves the components in and sets
// it, and a copy of any other value (a None, a Some from elsewhere) clears
// it, and the return carries that value boxed.

// splitTuples picks the tuples the function keeps as components and gives
// each component a register. It runs after assignRegisters.
func (cp *compiler) splitTuples() {
	makes := map[ir.Temp]*ir.Make{}
	calls := map[ir.Temp]*ir.ValType{}
	slots := cp.returnSlots()
	wraps := cp.wrappedReturns()
	root, groups := cp.valueGroups(func(in ir.Instr) bool {
		if p, isProj := in.(*ir.Proj); isProj {
			// A fused Maybe's tuple payload, which the read moves out of
			// the Maybe's payload registers into the components'.
			if !fusedProj(p) || cp.c.packed == nil || cp.c.packed[p.Subject()].kind != packMaybe || cp.c.packed[p.Subject()].parts == nil {
				return false
			}
			calls[p.Dst()] = cp.typeOf(p.Dst())
			return true
		}
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
		case *ir.Make:
			if _, wrapped := wraps.makes[n]; wrapped {
				return n.Operand(0)
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
	cp.wrapReturns(wraps, root, parts, packs)
	cp.split = map[ir.Temp][]loc{}
	for t, g := range root {
		if ls, splits := parts[g]; splits {
			cp.split[t] = ls
			cp.pack(t, packs[g])
		}
	}
}

// returnWraps is what the function's returns of a Maybe or a Result carry
// in parts (wrappedReturns).
type returnWraps struct {
	// makes are the Somes and Oks of a tuple the function builds only to
	// return, each with the descriptor it would be built under.
	makes map[*ir.Make]*rt.TypeDesc
	// slots are the return slots of a Maybe or Result some of whose copies
	// are of those, each with its descriptor.
	slots map[ir.Temp]*rt.TypeDesc
}

// wrappedReturns finds the Somes and Oks of a tuple the function builds
// only to return: `Some((value, next))` as a body's last expression,
// returned as it is or copied into the return slot a body's value takes
// when each branch ends in a value of its own (`if c then None else
// Some((a, b))`). Each one's tuple splits as a tuple the function returns
// does, and the Some is never built: its return carries the components and
// the variant (opRetP over packedTemp.wrap), which a caller that keeps the
// Maybe fused takes with no record between. A return slot holds a tag
// beside its own parts, set where a copy moves a Some's components in, and
// clear where any other value is copied into its reference register, which
// the return then carries boxed.
func (cp *compiler) wrappedReturns() returnWraps {
	f := cp.c.fn
	var out returnWraps
	returns := map[ir.Temp]int{}
	for _, b := range f.Blocks() {
		if r, isRet := b.Term().(*ir.Return); isRet && r.Ctl() == ir.CtlNone && r.HasVal() && carriesParts(cp.typeOf(r.Val())) {
			returns[r.Val()]++
		}
	}
	if len(returns) == 0 {
		return out
	}
	for _, p := range f.Params() {
		delete(returns, p.Temp)
	}
	// A value read only by its returns, or by one copy into a returned
	// slot, written once by a Some or an Ok of a tuple.
	wrapped := func(mk *ir.Make) *rt.TypeDesc {
		v := mk.Dst()
		if cp.writes[v] != 1 || mk.Kind() != ir.MakeVariant || mk.Arity() != 1 || len(mk.Names()) != 0 ||
			(mk.Variant() != "Some" && mk.Variant() != "Ok") || cp.loc(v).bank() != bankR {
			return nil
		}
		if ty := cp.typeOf(mk.Operand(0)); ty == nil || ty.Kind() != ir.KindTuple || ty.NumElems() == 0 {
			return nil
		}
		plan, planned := planMake(mk, cp.c.types[v], cp.typeOf)
		if !planned || plan.desc == nil || plan.shared != nil || plan.tag != 0 {
			return nil
		}
		return plan.desc
	}
	makes := map[ir.Temp]*ir.Make{}
	copies := map[ir.Temp][]ir.Temp{}
	for _, b := range f.Blocks() {
		for _, in := range b.Instrs() {
			switch n := in.(type) {
			case *ir.Make:
				makes[n.Dst()] = n
			case *ir.Copy:
				copies[n.Dst()] = append(copies[n.Dst()], n.Src())
			case *ir.Bind:
				copies[n.Dst()] = append(copies[n.Dst()], n.Src())
			}
		}
	}
	add := func(mk *ir.Make, d *rt.TypeDesc) {
		if out.makes == nil {
			out.makes = map[*ir.Make]*rt.TypeDesc{}
		}
		out.makes[mk] = d
	}
	for v, n := range returns {
		if cp.uses[v] != n {
			continue
		}
		if mk := makes[v]; mk != nil {
			if d := wrapped(mk); d != nil {
				add(mk, d)
			}
			continue
		}
		// A slot: its declaration and its copies write it, and only its
		// returns read it.
		srcs := copies[v]
		if len(srcs) == 0 || cp.writes[v] != 2+len(srcs) {
			continue
		}
		d := descOfType(cp.typeOf(v))
		if d == nil {
			continue
		}
		any := false
		for _, s := range srcs {
			if mk := makes[s]; mk != nil && cp.uses[s] == 1 {
				if md := wrapped(mk); md != nil {
					add(mk, d)
					any = true
				}
			}
		}
		if any {
			if out.slots == nil {
				out.slots = map[ir.Temp]*rt.TypeDesc{}
			}
			out.slots[v] = d
		}
	}
	return out
}

// wrapReturns leaves out each wrapped Some or Ok whose tuple splits
// (retWrap), marks a tuple so returned as its Some or Ok (packedTemp.wrap),
// and gives each return slot a copy into which moves one a tag and parts
// of its own, as a fused Maybe's (the slot's packedTemp, with wrap). A Some
// whose components' registers are not the slot's banks is built.
func (cp *compiler) wrapReturns(w returnWraps, root map[ir.Temp]ir.Temp, parts map[ir.Temp][]loc, packs map[ir.Temp]packedTemp) {
	for mk, desc := range w.makes {
		g, isMember := root[mk.Operand(0)]
		if _, splits := parts[g]; !isMember || !splits {
			continue
		}
		p := packs[g]
		p.wrap = desc
		packs[g] = p
		if cp.retWrap == nil {
			cp.retWrap = map[ir.Temp]ir.Temp{}
		}
		cp.retWrap[mk.Dst()] = mk.Operand(0)
	}
	for v, desc := range w.slots {
		var srcs []ir.Temp
		for _, b := range cp.c.fn.Blocks() {
			for _, in := range b.Instrs() {
				switch mv := in.(type) {
				case *ir.Copy:
					if mv.Dst() == v && cp.retWrap[mv.Src()] != ir.NoTemp {
						srcs = append(srcs, mv.Src())
					}
				case *ir.Bind:
					if mv.Dst() == v && cp.retWrap[mv.Src()] != ir.NoTemp {
						srcs = append(srcs, mv.Src())
					}
				}
			}
		}
		if len(srcs) == 0 {
			continue
		}
		tt := cp.typeOf(cp.retWrap[srcs[0]])
		td := descOfType(tt)
		if td == nil {
			for _, s := range srcs {
				delete(cp.retWrap, s)
			}
			continue
		}
		types, ls := cp.partRegisters(tt)
		kept := 0
		for _, s := range srcs {
			from := parts[root[cp.retWrap[s]]]
			same := len(from) == len(ls)
			for i := 0; same && i < len(ls); i++ {
				same = from[i].bank() == ls[i].bank()
			}
			if !same {
				delete(cp.retWrap, s)
				continue
			}
			kept++
		}
		if kept == 0 {
			continue
		}
		p := packedTemp{kind: packMaybe, tag: uint32(cp.c.nW), result: cp.typeOf(v).Sym().Name() == resultDesc.Name,
			payload: cp.c.locs[v], desc: td, parts: ls, types: types, wrap: desc}
		cp.c.nW++
		cp.pack(v, p)
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
		types, ls := cp.partRegisters(ty)
		root[r] = r
		parts[r] = ls
		packs[r] = packedTemp{kind: packTuple, desc: descOfType(ty), parts: ls, types: types}
	}
}

// partRegisters gives each component of tuple type ty a register of its
// bank, and answers the components' types and registers.
func (cp *compiler) partRegisters(ty *ir.ValType) ([]*ir.ValType, []loc) {
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
	return types, ls
}

// splitUse compiles the construction of a split tuple and the reads of its
// components, and reports whether in was one.
func (cp *compiler) splitUse(in ir.Instr) bool {
	if cp.split == nil {
		return false
	}
	switch n := in.(type) {
	case *ir.Make:
		if _, wrapped := cp.retWrap[n.Dst()]; wrapped {
			// The Some or Ok the return carries in parts.
			cp.emit(cp.op(opNop, 0))
			return true
		}
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
		return cp.slotCopy(n, n.Dst(), n.Src()) || cp.splitCopy(n.Pos(), n.Dst(), n.Src())
	case *ir.Bind:
		return cp.slotCopy(n, n.Dst(), n.Src()) || cp.splitCopy(n.Pos(), n.Dst(), n.Src())
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

// slotCopy compiles copy mv of src into a return slot that carries a Some
// of a tuple in parts (wrapReturns), and reports whether dst was one: a
// Some left out moves its tuple's components into the slot's parts and
// sets the tag; any other value is copied into the slot's reference
// register and clears it.
func (cp *compiler) slotCopy(mv ir.Instr, dst, src ir.Temp) bool {
	if cp.c.packed == nil || int(dst) >= len(cp.c.packed) {
		return false
	}
	p := &cp.c.packed[dst]
	if p.kind != packMaybe || p.wrap == nil {
		return false
	}
	set := uint64(0)
	if x := cp.retWrap[src]; x != ir.NoTemp {
		for i, l := range p.parts {
			if l.bank() != bankNone {
				cp.moveReg(l, cp.split[x][i])
				cp.at(mv.Pos(), false, -1)
			}
		}
		set = 1
	} else if !cp.aliased[mv] {
		cp.move(dst, src)
		cp.at(mv.Pos(), false, -1)
	}
	cp.emit(cp.op(opLoadW, p.tag), cp.word(set))
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

// deliverParts moves the last return into packed temporary t's registers:
// a split tuple's components, or a fused Maybe's tuple payload and its tag,
// from the return scratch when the callee returned them in parts (a Some
// or an Ok of a tuple it kept split, rpack.wrap), else out of the record it
// returned.
func (fr *frame) deliverParts(t ir.Temp) {
	s, p := fr.stk, &fr.c.packed[t]
	if s.rcls == retParts && (s.rpack.wrap != nil) == (p.kind == packMaybe) && len(s.rpack.parts) == len(p.parts) {
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
			if p.kind == packMaybe {
				s.w[fr.wb+int(p.tag)] = 1
			}
			return
		}
	}
	fr.write(t, s.ret())
}

// moveParts moves the parts p holds in fr's window into split tuple t's
// registers, and reports whether t is one whose registers are p's banks.
func (fr *frame) moveParts(t ir.Temp, p *packedTemp) bool {
	c, s := fr.c, fr.stk
	if c.packed[t].kind != packTuple || len(c.packed[t].parts) != len(p.parts) {
		return false
	}
	to := c.packed[t].parts
	for i, l := range to {
		if l.bank() != p.parts[i].bank() {
			return false
		}
	}
	for i, l := range to {
		from := p.parts[i]
		switch l.bank() {
		case bankW:
			s.setWord(fr.wb+l.reg(), s.w[fr.wb+from.reg()], s.b[fr.wb+from.reg()])
		case bankS:
			s.s[fr.sb+l.reg()] = s.s[fr.sb+from.reg()]
		case bankR:
			s.r[fr.rb+l.reg()] = s.r[fr.rb+from.reg()]
		}
	}
	return true
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
