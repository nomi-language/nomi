package ir

import "strconv"

// Defer registers a call to run when its scope exits: `defer close(conn)`.
//
// The call's operands are read HERE, at registration. The callee runs later, at the RunDefer naming the
// same id or on any earlier exit from the activation — a return, a propagated
// `try` or a fault — in reverse registration order, which is what a Go
// `defer` does. The call's own destination is never written: a deferred
// call's value is discarded.
//
// The call is held rather than appended to a block, so it is not an
// instruction of any block and does not define its destination. Its uses are
// the Defer's uses.
type Defer struct {
	pos  Pos
	id   int
	call *Call
}

// NewDefer registers call under id, which is unique within the function.
func NewDefer(pos Pos, id int, call *Call) *Defer {
	requirePos(pos, "ir.NewDefer")
	if call == nil {
		panic("ir: NewDefer: a deferred call is absent")
	}
	if id <= 0 {
		panic("ir: NewDefer: a deferred call needs a positive id")
	}
	return &Defer{pos: pos, id: id, call: call}
}

// DeferLastCall replaces the call this block appended last with a Defer
// registering it. A producer lowers the deferred call as an ordinary call, so
// its operands are evaluated in place, and then moves the call itself behind
// the registration.
func (b *Block) DeferLastCall(pos Pos, id int) *Defer {
	n := len(b.instrs)
	if n == 0 {
		panic("ir: Block.DeferLastCall: " + b.id.String() + " is empty")
	}
	c, isCall := b.instrs[n-1].(*Call)
	if !isCall {
		panic("ir: Block.DeferLastCall: " + b.instrs[n-1].String() + " is not a call")
	}
	d := NewDefer(pos, id, c)
	b.instrs = b.instrs[:n-1]
	if b.owner != nil && int(c.dst) < len(b.owner.defs) && b.owner.defs[c.dst] == c {
		b.owner.defs[c.dst] = nil
	}
	b.Append(d)
	return d
}

func (d *Defer) Pos() Pos    { return d.pos }
func (d *Defer) Dst() Temp   { return NoTemp }
func (d *Defer) ID() int     { return d.id }
func (d *Defer) Call() *Call { return d.call }
func (d *Defer) AppendUses(dst []Temp) []Temp {
	return d.call.AppendUses(dst)
}
func (d *Defer) String() string {
	return "defer#" + strconv.Itoa(d.id) + " " + d.call.String()
}
func (d *Defer) irNode()  {}
func (d *Defer) irInstr() {}

// RunDefer runs the deferred call registered under id at its scope's normal
// exit, unless an earlier exit already ran it. A scope's RunDefers appear in
// reverse registration order.
type RunDefer struct {
	pos Pos
	id  int
}

func NewRunDefer(pos Pos, id int) *RunDefer {
	requirePos(pos, "ir.NewRunDefer")
	if id <= 0 {
		panic("ir: NewRunDefer: a deferred call needs a positive id")
	}
	return &RunDefer{pos: pos, id: id}
}

func (r *RunDefer) Pos() Pos                     { return r.pos }
func (r *RunDefer) Dst() Temp                    { return NoTemp }
func (r *RunDefer) ID() int                      { return r.id }
func (r *RunDefer) AppendUses(dst []Temp) []Temp { return dst }
func (r *RunDefer) String() string               { return "rundefer#" + strconv.Itoa(r.id) }
func (r *RunDefer) irNode()                      {}
func (r *RunDefer) irInstr()                     {}
