package ir

// Tail calls.
//
// Nomi guarantees that a call in tail position runs in constant stack space
// (spec §12 "Tail-call optimization", §29). The grammar that decides tail
// position is `analysis.MarkTailCalls`, and a producer carries its answer onto
// the call as `TailCall` (see call.go). That mark is the fact the source
// states: the call is written where its value is the function's value.
//
// A CONSUMER NEEDS ONE MORE FACT, AND IT IS A PROPERTY OF THE GRAPH. To run a
// tail call by replacing the caller's activation, nothing the graph does after
// the call may depend on that activation. A producer lowers some tail-marked
// calls into shapes where that does not hold: an `Iter.loop` body is inlined,
// so a call in the loop lambda's tail feeds the loop state rather than a
// Return; a signalling callback returns its value with a control outcome; an
// `embeds` widening wraps the result. None of those is a tail call of the
// function the graph belongs to, whatever the source position says.
//
// TailTransfers is that fact, computed from the graph and nowhere else: a
// tail-marked call whose result reaches a plain Return unchanged. The path
// from the call to the Return may only move the value (Copy), declare storage
// (Slot), run registered deferred calls (RunDefer) and Jump; the call's block
// and every block on the path carry no fault edge, because a fault in the
// callee would otherwise have to land in this activation's handler. Any other
// instruction or terminator on the path is work the caller still has to do,
// and the call is an ordinary one.
//
// THE DEFERRED CALLS ON THE PATH ARE THE CONSUMER'S TO RUN BEFORE THE CALLEE,
// not after it. In a tail-calling function the call's operands are evaluated,
// then the function's pending deferred calls run, most recent first, then the
// callee runs. A consumer that replaces the activation has already skipped
// the RunDefers on the path, so it runs every pending registration at the
// transfer.
//
// A Return with no value accepts the transfer too: the function answers the
// callee's value there as well.
func TailTransfers(f *Func) map[*Call]bool {
	var out map[*Call]bool
	for _, b := range f.Blocks() {
		for i, in := range b.Instrs() {
			c, isCall := in.(*Call)
			if !isCall || !c.Tail() || c.Crosses() || c.Dst() == NoTemp {
				continue
			}
			if !tailReturns(f, b, i+1, c.Dst()) {
				continue
			}
			if out == nil {
				out = map[*Call]bool{}
			}
			out[c] = true
		}
	}
	return out
}

// tailReturns reports whether the value in t, written by the instruction
// before index from in b, reaches a plain Return unchanged.
func tailReturns(f *Func, b *Block, from int, t Temp) bool {
	holds := []Temp{t}
	held := func(x Temp) bool {
		for _, h := range holds {
			if h == x {
				return true
			}
		}
		return false
	}
	seen := map[BlockID]bool{b.ID(): true}
	for {
		if _, handled := b.Fault(); handled {
			return false
		}
		for _, in := range b.Instrs()[from:] {
			switch n := in.(type) {
			case *Copy:
				if !held(n.Src()) {
					return false
				}
				holds = append(holds, n.Dst())
			case *Slot, *RunDefer:
			default:
				return false
			}
		}
		switch term := b.Term().(type) {
		case *Return:
			if term.Ctl() != CtlNone {
				return false
			}
			return !term.HasVal() || held(term.Val())
		case *Jump:
			next := f.Block(term.Target())
			if next == nil || seen[next.ID()] {
				return false
			}
			seen[next.ID()] = true
			b, from = next, 0
		default:
			return false
		}
	}
}
