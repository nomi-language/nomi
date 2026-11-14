package vm

import (
	"fmt"
	"slices"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// valueGroups finds the values a function builds and only takes apart, which
// the compiler then keeps in registers instead of a record: a fused Maybe
// (maybefuse.go) and a split tuple (tuplesplit.go).
//
// A group is the destination of one defining instruction (defines answers
// true for it) plus the temporaries it is copied into by a copy
// assignRegisters computed straight into the destination's register: a
// `case` subject or a binding the value is moved into. Those copies compile
// to nothing, so the group is one register. A group stands when each member
// has that one definition and every use of a member is the copy or an
// instruction for which takes answers that member; every other use refuses
// the group, and so does a terminator's, except the function's return of a
// member when returns is set. A copy into one of slots is a use takes
// answers, never a join, even when it compiles to nothing.
//
// It answers each member's group, named by its defining destination, for the
// groups that stand, and those groups in temporary order.
func (cp *compiler) valueGroups(defines func(ir.Instr) bool, takes func(ir.Instr) ir.Temp, returns bool, slots map[ir.Temp]bool) (map[ir.Temp]ir.Temp, []ir.Temp) {
	f := cp.c.fn
	var root map[ir.Temp]ir.Temp
	ok := map[ir.Temp]bool{}
	for _, b := range f.Blocks() {
		for _, in := range b.Instrs() {
			if !defines(in) {
				continue
			}
			d := in.Dst()
			if d == ir.NoTemp || int(d) >= len(cp.c.locs) || cp.c.locs[d].bank() != bankR {
				continue
			}
			if root == nil {
				root = map[ir.Temp]ir.Temp{}
			}
			if _, seen := root[d]; seen {
				// Two definitions: not one value to keep in registers.
				ok[d] = false
				continue
			}
			root[d], ok[d] = d, true
		}
	}
	if root == nil {
		return nil, nil
	}
	// The copies into a member's register join their destinations to its
	// group, in instruction order, so a copy of a copy joins too.
	joins := map[ir.Instr]bool{}
	for _, b := range f.Blocks() {
		for _, in := range b.Instrs() {
			if !cp.aliased[in] {
				continue
			}
			var src, dst ir.Temp
			switch mv := in.(type) {
			case *ir.Copy:
				src, dst = mv.Src(), mv.Dst()
			case *ir.Bind:
				src, dst = mv.Src(), mv.Dst()
			default:
				continue
			}
			g, isMember := root[src]
			if !isMember || slots[dst] {
				continue
			}
			if _, taken := root[dst]; taken || cp.c.locs[dst] != cp.c.locs[src] {
				ok[g] = false
				continue
			}
			root[dst] = g
			joins[in] = true
		}
	}
	refuse := func(ts []ir.Temp, but ir.Temp) {
		for _, t := range ts {
			if g, isMember := root[t]; isMember && t != but {
				ok[g] = false
			}
		}
	}
	var buf []ir.Temp
	for _, b := range f.Blocks() {
		for _, in := range b.Instrs() {
			if joins[in] {
				continue
			}
			if d := in.Dst(); slots[d] {
				// A copy into a return slot, which takes its source.
				if src := takes(in); src != ir.NoTemp {
					refuse(in.AppendUses(buf[:0]), src)
					continue
				}
			}
			if d := in.Dst(); d != ir.NoTemp {
				if g, isMember := root[d]; isMember && !(d == g && defines(in)) {
					// Written by something besides its definition.
					ok[g] = false
				}
			}
			if s, isSlot := in.(*ir.Slot); isSlot {
				refuse([]ir.Temp{s.Slot()}, ir.NoTemp)
			}
			taken := takes(in)
			if taken != ir.NoTemp {
				if _, isMember := root[taken]; !isMember {
					taken = ir.NoTemp
				}
			}
			refuse(in.AppendUses(buf[:0]), taken)
		}
		if t := b.Term(); t != nil {
			if r, isRet := t.(*ir.Return); isRet && returns && r.Ctl() == ir.CtlNone && r.HasVal() {
				// The function's return of the value, which carries it
				// in parts (tuplesplit.go).
				refuse(t.AppendUses(buf[:0]), r.Val())
				continue
			}
			refuse(t.AppendUses(buf[:0]), ir.NoTemp)
		}
	}
	var groups []ir.Temp
	for g, stands := range ok {
		if stands {
			groups = append(groups, g)
		}
	}
	slices.Sort(groups)
	for t, g := range root {
		if !ok[g] {
			delete(root, t)
		}
	}
	return root, groups
}

// packKind is how a temporary kept as registers holds its value.
type packKind uint8

const (
	packNone  packKind = iota
	packMaybe          // a fused Maybe: payload or None in its register, tag beside
	packTuple          // a split tuple: its components in registers of their own
)

// packedTemp is one temporary's entry in code.packed.
type packedTemp struct {
	kind packKind
	// tag is a fused Maybe's tag register, and result says the fused value
	// is a Result rather than a Maybe.
	tag    uint32
	result bool
	// desc, parts and types are a split tuple's descriptor, its components'
	// registers and their stored types, in component order.
	desc  *rt.TypeDesc
	parts []loc
	types []*ir.ValType
}

// pack records how t holds its value.
func (cp *compiler) pack(t ir.Temp, p packedTemp) {
	if cp.c.packed == nil {
		cp.c.packed = make([]packedTemp, len(cp.c.locs))
	}
	cp.c.packed[t] = p
}

// readPacked is temporary t's value, rebuilt from the registers that hold it:
// what a handler, a stopped frame's variables or an assertion's trace read
// when they read a fused Maybe or a split tuple. Nothing on a fused or split
// path calls it; it allocates the record those paths do without.
func (fr *frame) readPacked(t ir.Temp) (any, error) {
	c := fr.c
	p := &c.packed[t]
	switch p.kind {
	case packMaybe:
		v := fr.stk.r[fr.rb+c.locs[t].reg()]
		if v == nil {
			break
		}
		if fr.stk.w[fr.wb+int(p.tag)] != 0 {
			if p.result {
				return okValue(v), nil
			}
			return some(v), nil
		}
		return v, nil
	case packTuple:
		vals := make([]any, len(p.parts))
		for i, l := range p.parts {
			vals[i] = fr.boxLoc(l, p.types[i])
			if vals[i] == nil {
				return nil, fmt.Errorf("vm: %s: %s is read before anything wrote it; "+
					"ir.Lint's temp-defined-before-use rule should have caught this", fr.fn.Name(), t)
			}
		}
		return p.desc.Make(vals...), nil
	}
	return nil, fmt.Errorf("vm: %s: %s is read before anything wrote it; "+
		"ir.Lint's temp-defined-before-use rule should have caught this", fr.fn.Name(), t)
}
