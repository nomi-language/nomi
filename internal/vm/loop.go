package vm

import (
	"fmt"
	"os"
	"strings"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// Loops as jumps: the package doc, "Loops written as tail calls", says
// which self tail calls compile to opLoop and what it keeps.

// NoLoopJumpEnv names the environment variable that compiles every self tail
// call as opTail instead of opLoop, for every machine the process opens.
const NoLoopJumpEnv = "NOMI_NO_LOOP_JUMP"

// WithoutLoopJumps makes every self tail call the machine compiles from now
// on run as opTail, as NoLoopJumpEnv does for a process.
func (m *Machine) WithoutLoopJumps() *Machine {
	m.noLoopJump = true
	return m
}

// loopJumps reports whether m compiles a self tail call to opLoop. A debug
// or profiling machine does not, so its stops and samples are those of the
// tail call as written.
func (m *Machine) loopJumps() bool {
	return !m.hooked && m.dbg == nil && m.prof == nil && !m.noLoopJump && os.Getenv(NoLoopJumpEnv) == ""
}

// loopPlan is one opLoop: the moves that put a self tail call's operands
// into the parameters, per bank in an order where no move overwrites a
// register a later one reads, whether any of them goes through the scratch
// register, and the index of the call's ownership moves plus one, or 0 for
// a call that hands nothing over.
type loopPlan struct {
	w, s, r []regMove
	cycles  bool
	moves   int
}

// regMove copies register src of a bank into register dst of the same bank,
// both in the activation's window. Register loopScratch is the plan's own
// temporary, which breaks a cycle of moves (a swap, a rotation).
type regMove struct{ dst, src int32 }

const loopScratch = -1

// loopPlanOf is the plan of a self tail call n with operands args, or false
// when n is no call opLoop runs: a call from a view, a boot or a function
// that registers deferred calls (which run at each transfer), a callee that
// is not the function itself, or operands that do not fit its parameters.
func (cp *compiler) loopPlanOf(n *ir.Call, callee *ir.Func, args []uint32) (loopPlan, bool) {
	c := cp.c
	if cp.site != nil || !c.tails[n] || c.boot || !cp.m.loopJumps() || cp.m.slotFor(callee).f != c.fn ||
		funcDefers(c.fn) || len(args) != len(c.params) {
		return loopPlan{}, false
	}
	var pending [4][]regMove
	for i, a := range args {
		src, dst := loc(a), c.params[i]
		if src.bank() != dst.bank() {
			return loopPlan{}, false
		}
		if src.bank() == bankNone || src.reg() == dst.reg() {
			continue
		}
		pending[dst.bank()] = append(pending[dst.bank()], regMove{dst: int32(dst.reg()), src: int32(src.reg())})
	}
	p := loopPlan{w: orderMoves(pending[bankW]), s: orderMoves(pending[bankS]), r: orderMoves(pending[bankR])}
	for _, ms := range [][]regMove{p.w, p.s, p.r} {
		for _, mv := range ms {
			p.cycles = p.cycles || mv.dst == loopScratch
		}
	}
	return p, true
}

// orderMoves orders a parallel move, each destination written once, so that
// running it in order reads every source before anything overwrites it: a
// move whose destination no pending move reads goes first. When none is
// left, the rest are cycles, and one is broken by saving a destination's
// value in the scratch register for the moves that read it. Those moves
// run before the next cycle needs the scratch, since the broken cycle and
// the moves reading out of it then unwind without another break.
func orderMoves(pending []regMove) []regMove {
	var out []regMove
	for len(pending) > 0 {
		progressed := false
		for i := 0; i < len(pending); i++ {
			mv := pending[i]
			read := false
			for j, other := range pending {
				if j != i && other.src == mv.dst {
					read = true
					break
				}
			}
			if read {
				continue
			}
			out = append(out, mv)
			pending = append(pending[:i], pending[i+1:]...)
			i--
			progressed = true
		}
		if progressed {
			continue
		}
		// Every pending destination is another move's source. The first
		// destination's value goes to the scratch register, and the moves
		// reading it read the scratch instead, so the first move can run.
		saved := pending[0].dst
		out = append(out, regMove{dst: loopScratch, src: saved})
		for j := range pending {
			if pending[j].src == saved {
				pending[j].src = loopScratch
			}
		}
	}
	return out
}

// loopWords runs a plan's word moves over window w and its shadows sh,
// carrying a big Int's shadow with its word as opMovW does.
func loopWords(w []uint64, sh []any, moves []regMove) {
	var tw uint64
	var tb any
	for _, mv := range moves {
		var x uint64
		var big any
		if mv.src == loopScratch {
			x, big = tw, tb
		} else {
			x = w[mv.src]
			if x == rt.BigWord {
				big = sh[mv.src]
			}
		}
		if mv.dst == loopScratch {
			tw, tb = x, big
			continue
		}
		w[mv.dst] = x
		if x == rt.BigWord {
			sh[mv.dst] = big
		}
	}
}

// loopRegs runs a plan's string or reference moves over window regs.
func loopRegs[T any](regs []T, moves []regMove) {
	var t T
	for _, mv := range moves {
		x := t
		if mv.src != loopScratch {
			x = regs[mv.src]
		}
		if mv.dst == loopScratch {
			t = x
			continue
		}
		regs[mv.dst] = x
	}
}

// String lists the plan's moves as a listing shows them: `w0<-w6` for word
// register 6 into word register 0, `wt` for the scratch register, and
// `owned` when the call hands arguments over.
func (p loopPlan) String() string {
	var parts []string
	for _, bank := range []struct {
		name  string
		moves []regMove
	}{{"w", p.w}, {"s", p.s}, {"r", p.r}} {
		reg := func(i int32) string {
			if i == loopScratch {
				return bank.name + "t"
			}
			return fmt.Sprintf("%s%d", bank.name, i)
		}
		for _, mv := range bank.moves {
			parts = append(parts, reg(mv.dst)+"<-"+reg(mv.src))
		}
	}
	if p.moves != 0 {
		parts = append(parts, "owned")
	}
	if len(parts) == 0 {
		return "no moves"
	}
	return strings.Join(parts, " ")
}
