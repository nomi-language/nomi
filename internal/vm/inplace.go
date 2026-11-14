package vm

import (
	"os"
	"sync"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// Updates written in place.
//
// The compiler asks ir.Owns once per function. A struct update whose base
// the activation owns and never reads again is `opUpdateIn`, which writes
// the new fields into the base's record instead of a clone, so a chain of
// updates of a literal allocates the literal and nothing more. The rest of
// opUpdate is unchanged: the same cache, and the same handler when the
// cache cannot resolve the base, which builds a new record.
//
// Ownership crosses direct calls. Each activation carries a mask of the
// parameters its caller handed it as its own (frame.owned). A direct call
// that hands on an argument (ir.Ownership.Moves: a move of an owned class)
// is `opCallO` or `opTailO`, opCall and opTail with the index of its moves:
// the caller sets the callee's bit for each argument whose need its own
// mask meets. An update whose base is owned only through parameters tests
// its need against the mask (code.uneed) and copies when the caller kept
// the value. Every other way into a function (a function value, interface
// dispatch, a call from Go or a host, a handler's call, a tail call with
// deferred calls pending) leaves the mask empty, so a parameter is borrowed
// unless a caller proved it its own. A function's result counts as fresh at
// a call site from its own analysis (ownTable), so `s = step(s)` keeps s
// owned around a loop. A bit at ir.DeepBit and up says the parameter was
// handed over deep, so a field read of it may own the value it reads.
//
// A fold whose seed the activation hands over (ir.Ownership.Folds) hands
// its lambda the accumulator the same way, through activateOwned: bit 0,
// and the deep bit when the seed is owned deep, and again for each step
// whose answer the lambda's ResultOwnership says is fresh (ownedFold).
//
// A debug machine copies, on both sides of every call: it compiles no
// opUpdateIn and no opCallO, so no activation's mask is ever set. A stopped
// frame shows its variables from their registers after their last use, and
// a debugger reading `p` after `q = {..p, x: 1}`, in the caller or the
// callee, must see p's own fields. A profiling machine reads no values, so
// it writes in place as a plain one does.
//
// NoInPlaceEnv set to anything makes every machine the process opens copy,
// and so does WithCopiedUpdates; a differential run compares the two
// (TestExpectation_InPlaceUpdatesRunAsCopying).

// NoInPlaceEnv names the environment variable that turns in-place updates
// off for every machine the process opens.
const NoInPlaceEnv = "NOMI_NO_INPLACE"

// WithCopiedUpdates makes every struct update the machine runs copy its
// base, as NoInPlaceEnv does for a process.
func (m *Machine) WithCopiedUpdates() *Machine {
	m.copyUpdates = true
	return m
}

// ownership answers what this machine writes in place in f and what f's
// direct calls hand on, or nil on a machine that copies.
func (m *Machine) ownership(f *ir.Func) *ir.Ownership {
	if m.dbg != nil || m.copyUpdates || os.Getenv(NoInPlaceEnv) != "" {
		return nil
	}
	return m.codes.owns(m, f)
}

// Ownership answers what this machine writes in place in f and what f's
// direct calls hand on, as its compiler asks: nil on a machine that copies.
// A census reads it (vmhost's TestInPlaceCensus).
func (m *Machine) Ownership(f *ir.Func) *ir.Ownership { return m.ownership(f) }

// move is one argument an opCallO or opTailO may hand its callee: argument
// arg is the callee's own when the caller's mask meets need, and its own
// deep when it meets deep too.
type move struct {
	need, deep uint64
	arg        uint8
}

func movesOf(ms []ir.Move) []move {
	out := make([]move, len(ms))
	for i, mv := range ms {
		out[i] = move{need: mv.Need, deep: mv.DeepNeed, arg: uint8(mv.Arg)}
	}
	return out
}

// ownTable memoizes what each function's returns hand a caller
// (ir.ResultOwnership), which every caller's ownership reads. Functions
// that call each other (a strongly connected component of the call graph,
// a fold's callback included, found as Tarjan's algorithm finds it on the
// analysis's own stack) assume at first that each one's result is fresh
// and deep and returns no parameter, and the analyses run again on what
// they found until no answer changes: a call that returns has a finite
// depth, so its result is what its body returns by induction. No answer
// is stored until its component's answers all hold, so none rests on an
// assumption that later fails. A component that has not settled after
// ownPasses passes answers not fresh for each member, which claims
// nothing.
type ownTable struct {
	mu      sync.Mutex
	results map[*ir.Func]ir.ResultOwnership
	// stack are the functions being analysed, innermost last; onStack
	// their positions, and low, per position, the lowest position the
	// function's analysis reached (Tarjan's lowlink).
	stack   []*ir.Func
	onStack map[*ir.Func]int
	low     []int
	// assume are the answers of the functions of components not settled
	// yet. done are the members analysed in the current pass whose
	// component's root is still on the stack, each with the function its
	// lowlink named, and pending lists them in the order they finished.
	assume  map[*ir.Func]ir.ResultOwnership
	done    map[*ir.Func]*ir.Func
	pending []*ir.Func
	// changes counts the answers a pass changed.
	changes int
	// uses are the bits each function's ownership tests (usesOf).
	uses map[*ir.Func]uint64
}

// ownPasses bounds the passes a component's analysis runs.
const ownPasses = 16

// ownOptimistic is what a function still being analysed is first assumed
// to return.
var ownOptimistic = ir.ResultOwnership{Fresh: true, Deep: true}

// owns answers f's ownership, with its direct callees' results.
func (t *codeTable) owns(m *Machine, f *ir.Func) *ir.Ownership {
	t.own.mu.Lock()
	defer t.own.mu.Unlock()
	o := ir.Owns(f, t.callees(m, f))
	t.pruneMoves(m, f, o)
	return o
}

// pruneMoves drops each move whose callee tests neither of the argument's
// bits, so a call that hands on nothing its callee can use stays opCall:
// handing a value over only lets the callee write it in place, and a
// caller's result does not depend on the bits it set. With t.own.mu held.
func (t *codeTable) pruneMoves(m *Machine, f *ir.Func, o *ir.Ownership) {
	for c, ms := range o.Moves {
		callee, err := m.resolveFunc(c.Callee(), f.Name())
		if err != nil || callee == nil {
			delete(o.Moves, c)
			continue
		}
		uses := t.usesOf(m, callee)
		kept := ms[:0]
		for _, mv := range ms {
			if uses&(1<<mv.Arg|1<<(ir.DeepBit+mv.Arg)) != 0 {
				kept = append(kept, mv)
			}
		}
		if len(kept) == 0 {
			delete(o.Moves, c)
		} else {
			o.Moves[c] = kept
		}
	}
}

// usesOf is the mask of the bits f's own ownership tests: the needs of its
// updates, collection operations, moves, folds and field reads. With
// t.own.mu held.
func (t *codeTable) usesOf(m *Machine, f *ir.Func) uint64 {
	o := &t.own
	if u, ok := o.uses[f]; ok {
		return u
	}
	if o.uses == nil {
		o.uses = map[*ir.Func]uint64{}
	}
	// A function a cycle of calls reaches again while its uses are being
	// found answers none: dropping a move only gives up a write in place.
	o.uses[f] = 0
	own := ir.Owns(f, t.callees(m, f))
	t.pruneMoves(m, f, own)
	var u uint64
	add := func(need uint64) {
		if need != ir.Never {
			u |= need
		}
	}
	for _, n := range own.Updates {
		add(n)
	}
	for _, n := range own.Takes {
		add(n)
	}
	for _, n := range own.Fields {
		add(n)
	}
	for _, fd := range own.Folds {
		add(fd.Need)
		add(fd.DeepNeed)
	}
	for _, ms := range own.Moves {
		for _, mv := range ms {
			add(mv.Need)
			add(mv.DeepNeed)
		}
	}
	o.uses[f] = u
	return u
}

// callees is the ir.Callees f's analysis asks, with t.own.mu held.
func (t *codeTable) callees(m *Machine, f *ir.Func) ir.Callees {
	return ir.Callees{
		Result: func(c *ir.Call) ir.ResultOwnership {
			callee, err := m.resolveFunc(c.Callee(), f.Name())
			if err != nil || callee == nil {
				return ir.ResultOwnership{}
			}
			return t.resultOf(m, callee)
		},
		Writes:  m.writesInPlace,
		Borrows: m.borrows,
		Lambda:  func(body *ir.Func) ir.ResultOwnership { return t.resultOf(m, body) },
	}
}

// resultOf is f's ResultOwnership, with t.own.mu held.
func (t *codeTable) resultOf(m *Machine, f *ir.Func) ir.ResultOwnership {
	o := &t.own
	if r, settled := o.results[f]; settled {
		return r
	}
	if o.onStack == nil {
		o.onStack, o.assume, o.done = map[*ir.Func]int{}, map[*ir.Func]ir.ResultOwnership{}, map[*ir.Func]*ir.Func{}
		o.results = map[*ir.Func]ir.ResultOwnership{}
	}
	// reach records that the function on top of the stack reached
	// position i.
	reach := func(i int) {
		if top := len(o.low) - 1; top >= 0 && i < o.low[top] {
			o.low[top] = i
		}
	}
	if i, on := o.onStack[f]; on {
		reach(i)
		return o.assume[f]
	}
	if _, analysed := o.done[f]; analysed {
		reach(o.lowOf(f))
		return o.assume[f]
	}
	if _, has := o.assume[f]; !has {
		o.assume[f] = ownOptimistic
	}
	idx := len(o.stack)
	o.stack = append(o.stack, f)
	o.low = append(o.low, idx)
	o.onStack[f] = idx
	from := len(o.pending)
	for pass := 1; ; pass++ {
		before := o.changes
		if r := ir.Owns(f, t.callees(m, f)).Result; r != o.assume[f] {
			o.assume[f] = r
			o.changes++
		}
		low := o.low[idx]
		if low < idx {
			// A member of a component whose root is further down: its
			// answer waits for the root's.
			o.done[f] = o.stack[low]
			o.pending = append(o.pending, f)
			o.pop()
			reach(low)
			return o.assume[f]
		}
		if o.changes == before || pass == ownPasses {
			members := append(o.pending[from:], f)
			for _, g := range members {
				r := o.assume[g]
				if o.changes != before {
					r = ir.ResultOwnership{}
				}
				o.results[g] = r
				delete(o.assume, g)
				delete(o.done, g)
			}
			o.pending = o.pending[:from]
			o.pop()
			return o.results[f]
		}
		// Another pass: the members analysed in this one are analysed
		// again on the answers it changed.
		for _, g := range o.pending[from:] {
			delete(o.done, g)
		}
		o.pending = o.pending[:from]
	}
}

// pop removes the function on top of the analysis stack.
func (o *ownTable) pop() {
	top := len(o.stack) - 1
	delete(o.onStack, o.stack[top])
	o.stack, o.low = o.stack[:top], o.low[:top]
}

// lowOf is the stack position the lowlink of an analysed member f named:
// the position of the function it named, or, when that one has left the
// stack, that one's own.
func (o *ownTable) lowOf(f *ir.Func) int {
	for {
		a := o.done[f]
		if i, on := o.onStack[a]; on {
			return i
		}
		f = a
	}
}

// ownedFold answers the mask a fold n hands its callback f's first
// accumulator under, when the activation owns the seed it handed n
// (ir.Ownership.Folds) and f is the callback the analysis read.
func ownedFold(fr *frame, n *ir.Iter, f *functionValue) (uint64, ir.Fold, bool) {
	fd, ok := fr.c.folds[n]
	if !ok || f.host != nil || f.body != fd.Body {
		return 0, fd, false
	}
	mask := fd.FoldMask(fr.owned)
	return mask, fd, mask != 0
}

// inPlaceHost is the in-place form of a collection operation that may
// write an owned receiver (operand 0), or nil for any other crossing.
func inPlaceHost(name string) hostFn {
	switch name {
	case "Vector.set", "Vector.push":
		return vectorHostWith(name, true)
	case "Map.put", "Map.remove":
		return mapHostWith(name, true)
	case "Set.insert", "Set.remove":
		return setHostWith(name, true)
	}
	return nil
}

// writesInPlaceHosts names inPlaceHost's operations.
var writesInPlaceHosts = map[string]bool{
	"Vector.set": true, "Vector.push": true, "Map.put": true, "Map.remove": true,
	"Set.insert": true, "Set.remove": true,
}

// borrowingHosts are the collection operations that read their operands
// and keep none of them: a lookup's answer is an element, never the
// collection, so a read by one does not end the collection's ownership.
var borrowingHosts = map[string]bool{
	"Vector.length": true, "Vector.get": true, "Vector.contains": true,
	"Map.get": true, "Map.size": true, "Map.contains": true,
	"Set.size": true, "Set.contains": true,
}

// writesInPlace answers, for a crossing, the operand it may write in place
// when that operand is owned, or -1: a collection operation the machine's
// own intrinsic answers.
func (m *Machine) writesInPlace(c *ir.Call) int {
	name := c.Callee().Name()
	if !writesInPlaceHosts[name] || m.hosts[name] == nil {
		return -1
	}
	return 0
}

// borrows reports whether a crossing keeps none of its operands.
func (m *Machine) borrows(c *ir.Call) bool {
	name := c.Callee().Name()
	return borrowingHosts[name] && m.hosts[name] != nil
}

// setOwned is the owned Set record v with its membership map replaced by
// items: the record is written in place, since nothing else reaches it.
func setOwned(v any, items vmap) any {
	r, ok := structRecord(v, "sets.Set")
	if !ok || len(r.R) == 0 {
		return setFromMap(items)
	}
	r.R[0] = items
	return r
}

// ownsStorage reports whether rec has slots of its own to write. A record
// with none may be its descriptor's one shared value (a marker), which an
// update must not write into.
func ownsStorage(rec *rt.Record) bool {
	return len(rec.W)+len(rec.S)+len(rec.R) > 0
}
