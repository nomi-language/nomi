package vm

import (
	"math"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// A fused Maybe: a crossing that answers a Maybe or a Result whose every
// use takes it apart at once, `case Map.get(m, k)` with `Some(v)` and `None`
// arms, `Vector.get(v, i)?` or `case String.to_int(s)`. The compiler keeps
// the value as two registers instead of a record: the temporary's own
// reference register holds the payload of a Some or an Ok, or the record a
// None or an Err is, and a word register holds 1 for a Some or an Ok and 0
// otherwise. The variant tests read the word (opTagIs), the payload read
// takes the reference register as it is (opProjM), and `?` leaves only on a
// 0 (opTryM). A Some or an Ok is then no allocation at all.
//
// A crossing fuses when it can answer in parts: Map.get and Vector.get by
// their fast paths below, and every adapter whose binding answers a Maybe
// or a Result through its parts form (hostadapt.PartsFunc, which
// internal/hostgen writes for every such binding with a payload of its
// own). A crossing with neither keeps its record, which fusing would only
// take apart.
//
// A use the fused form cannot answer (a return of the value, a call that
// takes it, a copy, a test of another enum) keeps the record: the compiler
// fuses a temporary only when every use is one of the three. A None or an
// Err in the reference register is the record it is, so a payload read of
// an Err and `?` run their handlers over it and report what they reported.
//
// Anything that reads the temporary boxed (a stopped frame's variables, an
// assertion's trace, a handler) gets the Maybe rebuilt (frame.readPacked).
// A debug machine fuses as a plain one does, so its bytecode is the plain
// one plus its hooks. A debug or profiling machine's crossing goes through
// hookedCallHost instead of the fast path, as opHost's does, so the debugger
// records it and the profiler samples at its return.

// fastMaybe is a fused crossing's fast path: the lookup, done in the loop over
// the operands' registers, the adapter's parts form, or none, for a crossing
// that runs what it lands on and takes the record it answers apart.
type fastMaybe uint8

const (
	fastNone fastMaybe = iota
	fastMapGet
	fastVectorGet
	fastParts
)

// fusedIntrinsics are the intrinsics that answer a Maybe in registers, with
// their fast paths. An adapter answers in parts by its binding instead.
var fusedIntrinsics = map[string]fastMaybe{
	"Map.get":    fastMapGet,
	"Vector.get": fastVectorGet,
}

// fastPathOf is how a crossing named name answers in parts, or fastNone.
func (m *Machine) fastPathOf(name string) fastMaybe {
	if intr := m.hosts[name]; intr != nil {
		return fusedIntrinsics[name]
	}
	if parts, err := m.adapterParts(name); err == nil && parts != nil {
		return fastParts
	}
	return fastNone
}

// carriesParts reports whether ty is the prelude's Maybe or Result, the two
// enums a fused temporary holds.
func carriesParts(ty *ir.ValType) bool {
	if ty == nil || ty.Kind() != ir.KindEnum || ty.Sym() == nil {
		return false
	}
	name := ty.Sym().Name()
	return name == maybeDesc.Name || name == resultDesc.Name
}

// fuseMaybes picks the crossings whose Maybe the function keeps in registers
// (valueGroups, whose uses are the variant tests, the payload reads and `?`)
// and gives each a word register for its tag. It runs after
// assignRegisters, so the tags take the registers after every temporary's.
func (cp *compiler) fuseMaybes() {
	root, groups := cp.valueGroups(func(in ir.Instr) bool {
		call, isCall := in.(*ir.Call)
		if !isCall || call.Form() != ir.CalleeDirect || !call.Crosses() {
			return false
		}
		return carriesParts(cp.typeOf(call.Dst())) && cp.m.fastPathOf(call.Callee().Name()) != fastNone
	}, func(in ir.Instr) ir.Temp {
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
		}
		return ir.NoTemp
	}, false, nil)
	if len(groups) == 0 {
		return
	}
	tags := map[ir.Temp]uint32{}
	for _, g := range groups {
		tags[g] = uint32(cp.c.nW)
		cp.c.nW++
	}
	cp.fused = map[ir.Temp]uint32{}
	for t, g := range root {
		cp.fused[t] = tags[g]
		ty := cp.typeOf(g)
		cp.pack(t, packedTemp{kind: packMaybe, tag: tags[g], result: ty.Sym().Name() == resultDesc.Name})
	}
}

// fusedMatch reports whether n is a variant test a fused Maybe or Result
// answers.
func fusedMatch(n *ir.Match) bool {
	if n.Kind() != ir.MatchVariant || !n.Answers() || n.Embeds() != nil || n.Sym() == nil {
		return false
	}
	switch n.Sym().Name() {
	case maybeDesc.Name:
		return n.Variant() == "Some" || n.Variant() == "None"
	case resultDesc.Name:
		return n.Variant() == "Ok" || n.Variant() == "Err"
	}
	return false
}

// fusedProj reports whether n is the payload read a fused Maybe answers.
func fusedProj(n *ir.Proj) bool {
	return n.Kind() == ir.ProjPayload && n.PayloadEmbeds() == nil && n.PayloadField() == "" && n.Index() == 0
}

// fusedUse compiles a use of a fused Maybe and reports whether in was one.
func (cp *compiler) fusedUse(in ir.Instr) bool {
	if cp.fused == nil {
		return false
	}
	switch n := in.(type) {
	case *ir.Call:
		tag, ok := cp.fused[n.Dst()]
		if !ok || !cp.hostCall(n) {
			return false
		}
		h := &cp.c.hostCalls[len(cp.c.hostCalls)-1]
		if h.fast = cp.m.fastPathOf(h.name); h.fast == fastParts {
			h.parts, _ = cp.m.adapterParts(h.name)
		}
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
		cp.emit(cp.op(opProjM, uint32(n.Dst())), uint32(cp.loc(n.Subject()).reg()), tag, cp.handlerIndex(n))
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

// hostMaybe runs a fused crossing, leaving its payload (or the None or Err
// record) in the reference register ref and its tag in word register tag of
// fr's window.
func (m *Machine) hostMaybe(fr *frame, h *hostCall, ref, tag int) error {
	stk := fr.stk
	fast := h.fast
	if m.hooked {
		// The debugger records each crossing's operands and answer, and
		// the profiler takes its samples at a crossing's return.
		fast = fastNone
	}
	switch fast {
	case fastVectorGet:
		vl, il := fr.c.locs[h.args[0]], fr.c.locs[h.args[1]]
		if vl.bank() == bankR && il.bank() == bankW {
			if vec, ok := stk.r[fr.rb+vl.reg()].(rt.Vector[any]); ok {
				// A big index's word is math.MinInt64, which is past any
				// vector, as the big index is.
				if got := rt.VectorGet(vec, int64(stk.w[fr.wb+il.reg()])); got.Tag == rt.TagSome {
					stk.r[fr.rb+ref], stk.w[fr.wb+tag] = got.Some, 1
				} else {
					stk.r[fr.rb+ref], stk.w[fr.wb+tag] = noneValue, 0
				}
				return nil
			}
		}
	case fastMapGet:
		ml := fr.c.locs[h.args[0]]
		if ml.bank() == bankR {
			if mp, ok := stk.r[fr.rb+ml.reg()].(vmap); ok {
				if v, found, done := m.scalarKeyLookup(fr, mp, h.args[1]); done {
					if found {
						stk.r[fr.rb+ref], stk.w[fr.wb+tag] = v, 1
					} else {
						stk.r[fr.rb+ref], stk.w[fr.wb+tag] = noneValue, 0
					}
					return nil
				}
			}
		}
	}
	// Any other operand: what the crossing lands on, its parts form when it
	// has one, else the record it answers taken apart.
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
	if fast == fastParts {
		v, isSome, err := callParts(h.parts, fr.runtime, args)
		clear(args)
		if err != nil {
			return err
		}
		if isSome {
			stk.r[fr.rb+ref], stk.w[fr.wb+tag] = v, 1
		} else {
			if _, _, isMaybe := maybeParts(v); isMaybe {
				// The adapter's None, under its own descriptor, is the
				// shared one.
				v = noneValue
			}
			stk.r[fr.rb+ref], stk.w[fr.wb+tag] = v, 0
		}
		return nil
	}
	var v any
	var err error
	switch {
	case m.hooked:
		v, err = m.hookedCallHost(fr, h, args)
	case h.intr != nil:
		v, err = h.intr(m.boundAt(fr, h.name), h.call.Pos(), args)
	default:
		v, err = callAdapter(h.fn, fr.runtime, args)
	}
	if m.dbg == nil {
		clear(args)
	}
	if err != nil {
		return err
	}
	if payload, isSome := carriedPayload(v); isSome {
		stk.r[fr.rb+ref], stk.w[fr.wb+tag] = payload, 1
	} else {
		stk.r[fr.rb+ref], stk.w[fr.wb+tag] = v, 0
	}
	return nil
}

// carriedPayload is a Some's or an Ok's payload, and false for any other
// value: a None or an Err, which a fused temporary holds as the record.
func carriedPayload(v any) (any, bool) {
	r, ok := enumRecord(v)
	if !ok || (!isEnum(r, maybeDesc.Name) && !isEnum(r, resultDesc.Name)) {
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
