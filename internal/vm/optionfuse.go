package vm

import (
	"fmt"
	"math"
	"os"

	"github.com/nomi-language/nomi/hostadapt"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// A fused Option: a call that answers an Option or a Result whose every use
// takes it apart at once, `case Map.get(m, k)` with `Some(v)` and `None`
// arms, `Vector.get(v, i)?`, `case String.to_int(s)` or `(v, next) =
// parse(data, i)!`. The compiler keeps the value as registers instead of a
// record: the payload of a Some or an Ok in a register of the payload's
// bank (a word for an Int, a string for a Bytes; the temporary's own
// reference register for a payload kept boxed), or a register for each
// component of a tuple payload, as a split tuple's (tuplesplit.go), the
// record a None or an Err is in the temporary's own reference register,
// and a word register holding 1 for a Some or an Ok and 0 otherwise. The
// variant tests read the word (opTagIs), the payload read moves the
// payload's register (opProjM), or a tuple payload's components into a
// split tuple's (opTryM's test, then moves), and `?` leaves only on a 0
// (opTryM). A Some or an Ok is then no allocation at all, and neither is
// its payload.
//
// A crossing fuses when it can answer in parts: Map.get, Vector.get and
// Vector.set by their fast paths below, and every adapter whose register
// form carries an Option or a Result (hostadapt.Regs.Carries, which
// internal/hostgen writes for every such binding): its operands are read
// from their registers and its payload's parts written to the payload's
// registers (hostslots.go), so `Bytes.uvarint_at(data, i)` in a loop boxes
// nothing. Two fast paths are reads of a value's storage, which opHostM
// runs in the dispatch loop itself (frame.readFused): Vector.get, and
// `Bytes.get` with the fixed-width integer reads (`Bytes.uint8_at`,
// `Bytes.int32_be_at` and their siblings), which read the bytes from the
// string register with rt.BytesWordAt into the payload's word. A shape a
// read does not cover (an operand or a payload in another bank, an
// unsigned 64-bit Int past int64) goes on to the crossing as before: the
// adapter's register form or the intrinsic. NOMI_NO_FAST_READS
// (NoFastReadsEnv) makes the reads crossings. A crossing with
// neither keeps its record, which fusing would only take apart. A call of a
// Nomi function always fuses: its return delivers into the registers
// (deliverParts), in parts when the callee returns a Some or an Ok of a
// tuple so (wrappedReturns), and otherwise out of the record it returns
// (takeApart), a scalar payload read from its field unboxed.
//
// `Option.with_default(m, d)` and `Result.with_default(r, d)` take the value
// apart too (intrinsic.defaults): the call is opDefaultM, the payload read
// with the default in place of the None's handler, so `Bytes.uint8_at(data,
// i) |> Option.with_default(-1)` builds no Some and boxes no default.
//
// A use the fused form cannot answer (a return of the value, a call that
// takes it, a copy, a test of another enum) keeps the record: the compiler
// fuses a temporary only when every use is one of these. A None or an
// Err in the reference register is the record it is, so a payload read of
// an Err and `?` run their handlers over it and report what they reported.
//
// Anything that reads the temporary boxed (a stopped frame's variables, an
// assertion's trace, a handler) gets the Option rebuilt (frame.readPacked).
// A debug machine fuses as a plain one does, so its bytecode is the plain
// one plus its hooks. A debug or profiling machine's crossing goes through
// hookedCallHost instead of the fast path, as opHost's does, so the debugger
// records it and the profiler samples at its return.

// fastOption is a fused crossing's fast path: the lookup or read, done over
// the operands' registers, the adapter's register form, or none, for a
// crossing that runs what it lands on and takes the record it answers apart.
type fastOption uint8

const (
	fastNone fastOption = iota
	fastMapGet
	fastVectorGet
	fastVectorSet
	fastRegs
	// fastBytesAt is a fixed-width read of a Bytes, run by
	// rt.BytesWordAt, and otherwise by the adapter's register form.
	fastBytesAt
)

// fastPath is a crossing's fast path, and a fastBytesAt read's width, byte
// order and signedness.
type fastPath struct {
	kind        fastOption
	width       uint8
	big, signed bool
}

// fastPaths are the crossings that answer an Option in registers by a fast
// path of their own: three intrinsics, and the std adapters of the Bytes
// reads, which fall back on their register forms. Any other adapter
// answers in parts by its register form.
var fastPaths = map[string]fastPath{
	"Map.get":                  {kind: fastMapGet},
	"Vector.get":               {kind: fastVectorGet},
	"Vector.set":               {kind: fastVectorSet},
	"bytes.Bytes.get":          {kind: fastBytesAt, width: 1},
	"bytes.Bytes.uint8_at":     {kind: fastBytesAt, width: 1},
	"bytes.Bytes.int8_at":      {kind: fastBytesAt, width: 1, signed: true},
	"bytes.Bytes.uint16_be_at": {kind: fastBytesAt, width: 2, big: true},
	"bytes.Bytes.uint16_le_at": {kind: fastBytesAt, width: 2},
	"bytes.Bytes.int16_be_at":  {kind: fastBytesAt, width: 2, big: true, signed: true},
	"bytes.Bytes.int16_le_at":  {kind: fastBytesAt, width: 2, signed: true},
	"bytes.Bytes.uint32_be_at": {kind: fastBytesAt, width: 4, big: true},
	"bytes.Bytes.uint32_le_at": {kind: fastBytesAt, width: 4},
	"bytes.Bytes.int32_be_at":  {kind: fastBytesAt, width: 4, big: true, signed: true},
	"bytes.Bytes.int32_le_at":  {kind: fastBytesAt, width: 4, signed: true},
	"bytes.Bytes.uint64_be_at": {kind: fastBytesAt, width: 8, big: true},
	"bytes.Bytes.uint64_le_at": {kind: fastBytesAt, width: 8},
	"bytes.Bytes.int64_be_at":  {kind: fastBytesAt, width: 8, big: true, signed: true},
	"bytes.Bytes.int64_le_at":  {kind: fastBytesAt, width: 8, signed: true},
}

// NoFastReadsEnv names the environment variable that runs every read
// fastPaths lists as its crossing (Vector.get through the intrinsic's fast
// path in hostOption, a Bytes read through its adapter's register form), for
// every machine the process opens.
const NoFastReadsEnv = "NOMI_NO_FAST_READS"

// fastPathOf is how a crossing named name answers in parts, or fastNone: an
// intrinsic's fast path, a Bytes read's when the adapter answering it has
// its register form, and any other carrying register form.
func (m *Machine) fastPathOf(name string) fastPath {
	p := fastPaths[name]
	if intr := m.hosts[name].fn; intr != nil {
		return p
	}
	r, err := m.adapterRegs(name)
	if err != nil || r == nil || !r.Carries {
		return fastPath{}
	}
	if p.kind == fastBytesAt && os.Getenv(NoFastReadsEnv) == "" {
		return p
	}
	return fastPath{kind: fastRegs}
}

// carriesParts reports whether ty is the prelude's Option or Result, the two
// enums a fused temporary holds.
func carriesParts(ty *ir.ValType) bool {
	if ty == nil || ty.Kind() != ir.KindEnum || ty.Sym() == nil {
		return false
	}
	name := ty.Sym().Name()
	return name == optionDesc.Name || name == resultDesc.Name
}

// fuseOptions picks the calls whose Option the function keeps in registers
// (valueGroups, whose uses are the variant tests, the payload reads and `?`)
// and gives each a word register for its tag. It runs after
// assignRegisters, so the tags take the registers after every temporary's.
// A crossing fuses when it can answer in parts; a call of a Nomi function
// always does, since its return delivers into the registers (deliverParts):
// in parts when the callee returns its Some or Ok of a tuple so
// (wrappedReturns), else taken out of the record it returns.
//
// A call compiled into its caller (inline.go) takes apart an operand whose
// parameter its callee only takes apart (takenApart): the operand fuses as
// it would if the callee's tests and reads were the caller's, and the view
// holds the parameter in the caller's registers (inheritFused).
func (cp *compiler) fuseOptions() {
	cp.inheritFused()
	root, groups := cp.valueGroups(func(in ir.Instr) bool {
		call, isCall := in.(*ir.Call)
		if !isCall || !carriesParts(cp.typeOf(call.Dst())) {
			return false
		}
		if site, _, crosses := cp.hostSite(call); crosses {
			return cp.m.fastPathOf(site.Callee().Name()).kind != fastNone
		}
		return call.Form() == ir.CalleeDirect
	}, cp.fuseTake, false, nil)
	if len(groups) == 0 {
		return
	}
	// A payload every read lands in one word or string register's bank
	// gets a register of that bank, and a tuple payload a register for each
	// component, as a split tuple's; any other stays boxed in the group's
	// own reference register.
	ptypes := map[ir.Temp]*ir.ValType{}
	plain := map[ir.Temp]bool{}
	read := func(g ir.Temp, ty *ir.ValType) {
		if ct := carriedType(cp.typeOf(g)); ct != nil && (classOf(ct) != classOf(ty) || ct.Kind() != ty.Kind()) {
			// An Err's payload read, which runs its handler over the
			// record (opProjM).
			return
		}
		if prev, seen := ptypes[g]; seen && (classOf(prev) != classOf(ty) || prev.Kind() != ty.Kind()) {
			plain[g] = true
		}
		ptypes[g] = ty
	}
	for _, b := range cp.c.fn.Blocks() {
		for _, in := range b.Instrs() {
			switch n := in.(type) {
			case *ir.Proj:
				if g, isMember := root[n.Subject()]; isMember && fusedProj(n) {
					read(g, cp.typeOf(n.Dst()))
				}
			case *ir.Call:
				t := cp.fuseTake(n)
				g, isMember := root[t]
				switch {
				case t == ir.NoTemp || !isMember:
				case cp.defaultTake(n) != ir.NoTemp:
					// A default's payload read, into the call's destination.
					read(g, cp.typeOf(n.Dst()))
				default:
					// The payload reads of a compiled-in callee's parameter.
					for _, ty := range cp.m.takenApart(cp.plans[n])[argIndex(n, t)].reads {
						read(g, ty)
					}
				}
			}
		}
	}
	packs := map[ir.Temp]packedTemp{}
	for _, g := range groups {
		ty := cp.typeOf(g)
		p := packedTemp{kind: packOption, tag: uint32(cp.c.nW), result: ty.Sym().Name() == resultDesc.Name, payload: cp.c.locs[g]}
		cp.c.nW++
		if pt := ptypes[g]; pt != nil && !plain[g] {
			switch classOf(pt) {
			case bankW:
				p.payload, p.ptype = mkLoc(bankW, cp.c.nW), pt
				cp.c.nW++
			case bankS:
				p.payload, p.ptype = mkLoc(bankS, cp.c.nS), pt
				cp.c.nS++
			default:
				if pt.Kind() == ir.KindTuple && pt.NumElems() > 0 && pt.NumElems() <= hostadapt.MaxSlots {
					if p.desc = descOfType(pt); p.desc != nil {
						p.types, p.parts = cp.partRegisters(pt)
					}
				}
			}
		}
		packs[g] = p
	}
	if cp.fused == nil {
		cp.fused = map[ir.Temp]uint32{}
	}
	for t, g := range root {
		cp.fused[t] = packs[g].tag
		cp.pack(t, packs[g])
	}
}

// fuseTake is the fused Option in takes apart: a variant test's subject, a
// payload read's, the operand of a `?`, the Option or Result a default is
// taken from (defaultTake), or the operand of a call compiled into this
// code whose parameter its callee only takes apart (takenApart), when the
// call passes it once.
func (cp *compiler) fuseTake(in ir.Instr) ir.Temp {
	switch n := in.(type) {
	case *ir.Match:
		if fusedMatch(n) {
			return n.Subject()
		}
	case *ir.Proj:
		if fusedProj(n) {
			return n.Subject()
		}
	case *ir.Try:
		return n.Src()
	case *ir.Call:
		if t := cp.defaultTake(n); t != ir.NoTemp {
			return t
		}
		g := cp.plans[n]
		if g == nil {
			return ir.NoTemp
		}
		taken := cp.m.takenApart(g)
		for i := 0; i < n.NumArgs(); i++ {
			if _, apart := taken[i]; apart && argIndex(n, n.Arg(i)) == i {
				return n.Arg(i)
			}
		}
	}
	return ir.NoTemp
}

// NoFusedDefaultEnv names the environment variable that makes every
// `Option.with_default` and `Result.with_default` call its intrinsic over
// the record, for every machine the process opens.
const NoFusedDefaultEnv = "NOMI_NO_FUSED_DEFAULT"

// defaultTake is the Option or Result crossing n takes its answer from when
// n lands on an intrinsic that defaults (intrinsic.defaults), or NoTemp. Its
// answer must sit in registers of one bank with the default and be no
// value kept in registers of its own (a tuple, an Option, a Result), so
// opDefaultM moves either into the destination as it is.
func (cp *compiler) defaultTake(n *ir.Call) ir.Temp {
	if !n.Crosses() || n.Form() != ir.CalleeDirect || n.NumArgs() != 2 || !cp.m.hosts[n.Callee().Name()].defaults ||
		os.Getenv(NoFusedDefaultEnv) != "" {
		return ir.NoTemp
	}
	from, def, dst := n.Arg(0), n.Arg(1), n.Dst()
	ty := cp.typeOf(dst)
	if from == def || !carriesParts(cp.typeOf(from)) || ty == nil || ty.Kind() == ir.KindTuple || carriesParts(ty) ||
		cp.loc(def).bank() != cp.loc(dst).bank() || cp.loc(dst).bank() == bankNone {
		return ir.NoTemp
	}
	return from
}

// argIndex is the one operand of n that is t, or -1 when n passes t more
// than once or not at all.
func argIndex(n *ir.Call, t ir.Temp) int {
	at := -1
	for i := 0; i < n.NumArgs(); i++ {
		if n.Arg(i) == t {
			if at >= 0 {
				return -1
			}
			at = i
		}
	}
	return at
}

// takeApart writes the Option or Result record v into fused temporary t: a
// Some's or an Ok's payload into its payload's registers, else the record
// itself.
func (fr *frame) takeApart(t ir.Temp, tag int, v any) {
	r, ok := enumRecord(v)
	if !ok || (!isEnum(r, optionDesc.Name) && !isEnum(r, resultDesc.Name)) || (variantName(r) != "Some" && variantName(r) != "Ok") {
		fr.setNone(t, tag, v)
		return
	}
	// A Some's or an Ok's payload field, read into the payload's register
	// as the field holds it, so a scalar payload is never boxed.
	p, vd := &fr.c.packed[t], r.Variant()
	if vd.Shape == rt.VariantPositional && len(vd.Fields) == 1 && p.parts == nil {
		f := &vd.Fields[0]
		switch {
		case p.payload.bank() == bankW && f.Type.Bank() == rt.BankWord && f.Type == slotOf(p.ptype):
			var big any
			if b := r.BigSlot(f.Slot); b != nil {
				big = b
			}
			fr.stk.setWord(fr.wb+p.payload.reg(), r.W[f.Slot], big)
			fr.stk.w[fr.wb+tag] = 1
			return
		case p.payload.bank() == bankS && f.Type.Bank() == rt.BankStr:
			fr.stk.s[fr.sb+p.payload.reg()] = r.S[f.Slot]
			fr.stk.w[fr.wb+tag] = 1
			return
		}
	}
	if payload, isSome := carriedPayload(v); isSome {
		fr.setPayload(t, tag, payload)
		return
	}
	fr.setNone(t, tag, v)
}

// setPayload writes a fused crossing's Some or Ok payload v, boxed, into
// the payload's register of fused temporary t, or a tuple payload's parts,
// and its tag 1.
func (fr *frame) setPayload(t ir.Temp, tag int, v any) {
	p := &fr.c.packed[t]
	if p.parts != nil {
		fr.unpack(t, v)
		fr.stk.w[fr.wb+tag] = 1
		return
	}
	switch p.payload.bank() {
	case bankW:
		w, big, ok := unboxWord(v)
		if !ok {
			fr.bad = fmt.Errorf("vm: %s: %s's payload holds %s and received %T", fr.fn.Name(), t, p.ptype, v)
			return
		}
		fr.stk.setWord(fr.wb+p.payload.reg(), w, big)
	case bankS:
		s, ok := unboxStr(v)
		if !ok {
			fr.bad = fmt.Errorf("vm: %s: %s's payload holds %s and received %T", fr.fn.Name(), t, p.ptype, v)
			return
		}
		fr.stk.s[fr.sb+p.payload.reg()] = s
	default:
		fr.stk.r[fr.rb+p.payload.reg()] = v
	}
	fr.stk.w[fr.wb+tag] = 1
}

// setNone writes a fused crossing's None or Err record v into fused
// temporary t's own reference register, and its tag 0. The adapter's None,
// under its own descriptor, is the shared one.
func (fr *frame) setNone(t ir.Temp, tag int, v any) {
	if _, _, isOption := optionParts(v); isOption {
		v = noneValue
	}
	fr.stk.r[fr.rb+fr.c.locs[t].reg()], fr.stk.w[fr.wb+tag] = v, 0
}

// carriedType is the payload type of the Some or the Ok of Option or Result
// type ty, or nil when its layout does not say.
func carriedType(ty *ir.ValType) *ir.ValType {
	if ty == nil || ty.Layout() == nil {
		return nil
	}
	for _, v := range ty.Layout().Variants {
		if (v.Name == "Some" || v.Name == "Ok") && len(v.Fields) == 1 {
			return v.Fields[0].Type
		}
	}
	return nil
}

// fusedMatch reports whether n is a variant test a fused Option or Result
// answers.
func fusedMatch(n *ir.Match) bool {
	if n.Kind() != ir.MatchVariant || !n.Answers() || n.Embeds() != nil || n.Sym() == nil {
		return false
	}
	switch n.Sym().Name() {
	case optionDesc.Name:
		return n.Variant() == "Some" || n.Variant() == "None"
	case resultDesc.Name:
		return n.Variant() == "Ok" || n.Variant() == "Err"
	}
	return false
}

// fusedProj reports whether n is the payload read a fused Option answers.
func fusedProj(n *ir.Proj) bool {
	return n.Kind() == ir.ProjPayload && n.PayloadEmbeds() == nil && n.PayloadField() == "" && n.Index() == 0
}

// fusedUse compiles a use of a fused Option and reports whether in was one.
func (cp *compiler) fusedUse(in ir.Instr) bool {
	if cp.fused == nil {
		return false
	}
	switch n := in.(type) {
	case *ir.Call:
		if from := cp.defaultTake(n); from != ir.NoTemp {
			tag, ok := cp.fused[from]
			if !ok {
				return false
			}
			cp.emit(cp.op(opDefaultM, uint32(n.Dst())), uint32(from), tag, uint32(n.Arg(1)))
			return true
		}
		tag, ok := cp.fused[n.Dst()]
		if !ok || !cp.hostCall(n) {
			return false
		}
		h := &cp.c.hostCalls[len(cp.c.hostCalls)-1]
		if h.fast = cp.m.fastPathOf(h.name); h.fast.kind == fastRegs || h.fast.kind == fastBytesAt {
			if !cp.bindRegs(h, true) {
				h.fast = fastPath{}
			}
		}
		h.inLoop = (h.fast.kind == fastVectorGet || h.fast.kind == fastBytesAt) && os.Getenv(NoFastReadsEnv) == ""
		cp.c.code[len(cp.c.code)-1] = uint32(cp.op(opHostM, uint32(len(cp.c.hostCalls)-1)))
		cp.emit(tag)
		return true
	case *ir.Match:
		tag, ok := cp.fused[n.Subject()]
		if !ok {
			return false
		}
		want := uint32(0)
		if n.Variant() == "Some" || n.Variant() == "Ok" {
			want = 1
		}
		cp.emit(cp.op(opTagIs, uint32(cp.loc(n.Dst()).reg())), tag, want)
		return true
	case *ir.Proj:
		tag, ok := cp.fused[n.Subject()]
		if !ok {
			return false
		}
		cp.emit(cp.op(opProjM, uint32(n.Dst())), uint32(n.Subject()), tag, cp.handlerIndex(n))
		return true
	case *ir.Try:
		tag, ok := cp.fused[n.Src()]
		if !ok {
			return false
		}
		cp.emit(cp.op(opTryM, tag), cp.handlerIndex(n))
		return true
	}
	return false
}

// hostOption runs a fused crossing, leaving its payload in the payload's
// register, or the None or Err record in its destination's reference
// register, and its tag in word register tag of fr's window.
func (m *Machine) hostOption(fr *frame, h *hostCall, tag int) error {
	stk := fr.stk
	fast := h.fast.kind
	if m.hooked {
		// The debugger records each crossing's operands and answer, and
		// the profiler takes its samples at a crossing's return. A
		// profiling machine reads no values, so it sets a vector as a
		// plain one does, in place when owned, inside its crossing.
		if fast == fastVectorSet && m.dbg == nil {
			var done bool
			_, err := m.profCross(fr, h.call, h.name, func() (any, error) {
				var err error
				done, err = m.vectorSetM(fr, h, tag)
				return nil, err
			})
			if done || err != nil {
				return err
			}
		}
		fast = fastNone
	}
	switch fast {
	case fastVectorGet:
		// A read the loop did not run (NoFastReadsEnv).
		if fr.readFused(h, tag) {
			return nil
		}
	case fastVectorSet:
		if done, err := m.vectorSetM(fr, h, tag); done || err != nil {
			return err
		}
	case fastRegs, fastBytesAt:
		carried, ran, err := fr.callSlots(h, m.noteGo)
		if err != nil {
			return err
		}
		if !ran {
			// An operand of another kind, which the adapter refuses.
			break
		}
		sl := &stk.slots
		out, p := h.regs.Out, &fr.c.packed[h.dst]
		switch {
		case !carried:
			fr.setNone(h.dst, tag, sl.R[0])
		case len(out) == 1 && fr.setLoc(p.payload, out[0], sl.W[0], sl.Big[0], sl.S[0], sl.R[0]):
			stk.w[fr.wb+tag] = 1
		case len(out) > 1 && len(p.parts) == len(out) && fr.setParts(p.parts, out, sl):
			// A tuple payload's components, each into its part's register.
			stk.w[fr.wb+tag] = 1
		default:
			fr.setPayload(h.dst, tag, h.regs.Payload(sl))
		}
		fr.clearSlots(h)
		return nil
	case fastMapGet:
		ml := fr.c.locs[h.args[0]]
		if ml.bank() == bankR {
			if mp, ok := stk.r[fr.rb+ml.reg()].(vmap); ok {
				if v, found, done := m.scalarKeyLookup(fr, mp, h.args[1]); done {
					if found {
						fr.setPayload(h.dst, tag, v)
					} else {
						fr.setNone(h.dst, tag, noneValue)
					}
					return nil
				}
			}
		}
	}
	// Any other operand: what the crossing lands on, the record it answers
	// taken apart.
	var args []any
	if m.dbg != nil {
		args = make([]any, len(h.args))
	} else {
		args = fr.hostArgs(len(h.args))
	}
	for i, t := range h.args {
		v, err := fr.read(t)
		if err != nil {
			clear(args)
			return err
		}
		args[i] = v
	}
	var v any
	var err error
	switch {
	case m.hooked:
		v, err = m.hookedCallHost(fr, h, args)
	case h.intr != nil:
		v, err = h.intr(m.boundAt(fr, h.name), h.call.Pos(), args)
	default:
		v, err = callAdapter(m.noteGo, fr, h.call.Pos(), h.fn, args)
	}
	if m.dbg == nil {
		clear(args)
	}
	if err != nil {
		return err
	}
	fr.takeApart(h.dst, tag, v)
	return nil
}

// readFused runs fused crossing h's read (Vector.get's, a Bytes read's)
// over its operands' registers into its payload's register and tag, and
// reports whether it did: false, with nothing written, for a shape it does
// not cover. A big index's word is math.MinInt64, which is past any vector
// or Bytes, as the big index is.
func (fr *frame) readFused(h *hostCall, tag int) bool {
	stk, c := fr.stk, fr.c
	src, il := c.locs[h.args[0]], c.locs[h.args[1]]
	if il.bank() != bankW {
		return false
	}
	i := int64(stk.w[fr.wb+il.reg()])
	switch h.fast.kind {
	case fastVectorGet:
		if src.bank() != bankR {
			return false
		}
		vec, ok := stk.r[fr.rb+src.reg()].(rt.Vector[any])
		if !ok {
			return false
		}
		if got := rt.VectorGet(vec, i); got.Tag == rt.TagSome {
			fr.setPayload(h.dst, tag, got.Some)
		} else {
			fr.setNone(h.dst, tag, noneValue)
		}
		return true
	case fastBytesAt:
		p := &c.packed[h.dst]
		if src.bank() != bankS || (p.payload.bank() != bankW && p.payload.bank() != bankR) {
			return false
		}
		u, ok := rt.BytesWordAt(rt.Bytes(stk.s[fr.sb+src.reg()]), i, int(h.fast.width), h.fast.big, h.fast.signed)
		switch {
		case !ok:
			fr.setNone(h.dst, tag, noneValue)
		case !h.fast.signed && u > math.MaxInt64:
			// An unsigned 64-bit Int past int64, a big Int: the register
			// form answers it.
			return false
		case p.payload.bank() == bankR:
			// A payload nothing reads in a register of its bank (`Some(_)`),
			// kept boxed for a stopped frame's view of the Option.
			ty := carriedType(c.types[h.dst])
			if ty == nil {
				return false
			}
			fr.setPayload(h.dst, tag, boxWord(u, nil, ty))
		default:
			// A signed read of math.MinInt64 is the word rt.BigWord with no
			// shadow, which is that Int.
			stk.setWord(fr.wb+p.payload.reg(), u, nil)
			stk.w[fr.wb+tag] = 1
		}
		return true
	}
	return false
}

// setParts writes the answer's parts in slots sl, of slot types out, into
// registers ls, and reports whether every register holds its part as its
// slot does. A register that does not leaves the rest unwritten.
func (fr *frame) setParts(ls []loc, out []rt.SlotType, sl *hostadapt.Slots) bool {
	for i, l := range ls {
		if !bankHolds(l.bank(), out[i]) {
			return false
		}
	}
	for i, l := range ls {
		fr.setLoc(l, out[i], sl.W[i], sl.Big[i], sl.S[i], sl.R[i])
	}
	return true
}

// vectorSetM is Vector.set's fast path: the vector and the index from their
// registers, the item as it reads, and the set written in place when the
// activation owns the vector (inplace.go), as opHost's in-place form does,
// so an owned set allocates nothing. done is false when an operand is not
// where the fast path reads it, for the intrinsic to answer.
func (m *Machine) vectorSetM(fr *frame, h *hostCall, tag int) (done bool, err error) {
	stk := fr.stk
	vl, il := fr.c.locs[h.args[0]], fr.c.locs[h.args[1]]
	if vl.bank() != bankR || il.bank() != bankW {
		return false, nil
	}
	box := stk.r[fr.rb+vl.reg()]
	vec, ok := box.(rt.Vector[any])
	if !ok {
		return false, nil
	}
	// A big index's word is math.MinInt64, which is past any vector, as
	// the big index is.
	index := int64(stk.w[fr.wb+il.reg()])
	if !rt.VectorInRange(vec, index) {
		fr.setNone(h.dst, tag, noneValue)
		return true, nil
	}
	item, err := fr.read(h.args[2])
	if err != nil {
		return true, err
	}
	owned := h.inPlace != nil && fr.owned&h.need == h.need
	fr.setPayload(h.dst, tag, vectorSetBox(box, vec, index, item, owned))
	return true, nil
}

// carriedPayload is a Some's or an Ok's payload, and false for any other
// value: a None or an Err, which a fused temporary holds as the record.
func carriedPayload(v any) (any, bool) {
	r, ok := enumRecord(v)
	if !ok || (!isEnum(r, optionDesc.Name) && !isEnum(r, resultDesc.Name)) {
		return nil, false
	}
	if name := variantName(r); name != "Some" && name != "Ok" {
		return nil, false
	}
	return payloadOf(r)
}

// scalarKeyLookup looks key temporary t up in mp straight from its register
// when t holds a scalar, which hashes structurally and calls nothing: an Int
// of any size, a Float, a Bool, a Byte or a String is read without boxing
// (rt.MapLookupInt, a big Int's shadow as it is), and a reference that is a
// scalar as it is. done is false for any other key, which the intrinsic
// looks up.
func (m *Machine) scalarKeyLookup(fr *frame, mp vmap, t ir.Temp) (v any, found, done bool) {
	c, stk := fr.c, fr.stk
	l := c.locs[t]
	switch l.bank() {
	case bankW:
		i := fr.wb + l.reg()
		w := stk.w[i]
		switch c.types[t].Kind() {
		case ir.KindInt:
			if w == rt.BigWord {
				if big := stk.b[i]; big != nil {
					v, found = mapLookup(nil, mp, big)
					return v, found, true
				}
			}
			v, found = rt.MapLookupInt(mp, int64(w))
			return v, found, true
		case ir.KindFloat:
			v, found = rt.MapLookupFloat(mp, math.Float64frombits(w))
			return v, found, true
		case ir.KindBool:
			// A Bool and a Byte box without allocating.
			v, found = mapLookup(nil, mp, w != 0)
			return v, found, true
		case ir.KindByte:
			v, found = mapLookup(nil, mp, rt.Byte(w))
			return v, found, true
		}
	case bankS:
		if c.types[t].Kind() == ir.KindString {
			v, found = rt.MapLookupString(mp, stk.s[fr.sb+l.reg()])
			return v, found, true
		}
	case bankR:
		if c.packed != nil && c.packed[t].kind != packNone {
			return nil, false, false
		}
		key := stk.r[fr.rb+l.reg()]
		if key != nil && m.keysOver(nil, key) == nil {
			v, found = mapLookup(nil, mp, key)
			return v, found, true
		}
	}
	return nil, false, false
}
