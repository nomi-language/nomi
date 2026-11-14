package vm

import "github.com/nomi-language/nomi/internal/ir"

// Hoisted constants. A scalar or string constant whose temporary has one
// definition and a register of its own is loaded once, at the function's
// entry, instead of where the graph defines it: nothing else writes that
// register, so it holds the constant for the whole activation. A self tail
// call (selfTail) restarts the body after the loads, so a loop written as a
// tail-recursive function, `bounded(zr, zi, ...)`, does not reload its
// constants on every iteration. Every other entry (a call, a transfer, a
// restarted frame) starts at pc 0 and loads them.
//
// A machine counting entries restarts a self tail call at pc 0, where the
// count is (entries.go), and loads the constants again.

// hoistConstants emits the hoisted loads and records the pc a self tail call
// restarts at. It runs after the entry count and before the first block.
func (cp *compiler) hoistConstants() {
	c := cp.c
	owners := map[loc]int{}
	for t := 1; t < len(c.locs); t++ {
		if c.locs[t].bank() != bankNone {
			owners[c.locs[t]]++
		}
	}
	for _, b := range c.fn.Blocks() {
		for _, in := range b.Instrs() {
			n, isConst := in.(*ir.Const)
			if !isConst {
				continue
			}
			d := n.Dst()
			if d == ir.NoTemp || int(d) >= len(c.locs) || cp.writes[d] != 1 || owners[c.locs[d]] != 1 {
				continue
			}
			if b := c.locs[d].bank(); b != bankW && b != bankS {
				continue
			}
			at, sites := len(c.code), len(c.sites)
			cp.at(n.Pos(), false, -1)
			if !cp.typed(n) {
				c.code, c.sites = c.code[:at], c.sites[:sites]
				continue
			}
			if cp.hoisted == nil {
				cp.hoisted = map[ir.Instr]bool{}
			}
			cp.hoisted[n] = true
		}
	}
	if !cp.m.countEntries {
		c.selfEntry = len(c.code)
	}
}

// isHoisted reports whether in is a constant hoistConstants loaded at entry,
// which compiles to nothing where the graph defines it.
func (cp *compiler) isHoisted(in ir.Instr) bool {
	return cp.hoisted != nil && cp.hoisted[in]
}
