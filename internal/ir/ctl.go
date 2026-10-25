package ir

import "strconv"

// A call to an iter-sensitive function that is an iteration callback's
// whole body (`fn acc = 0, x -> keep(acc, x)`).
//
// The callee is lowered as a signalling callback is (Return.Ctl), so its
// answer may be a control outcome rather than a value: its `continue`, its
// bare `break`, or its `break v`. The callback's call writes that answer
// whole into a destination of type Any, and CtlRead reads it:
//
//	o    = call keep(acc, x)        Any: the value, or the control outcome
//	code = ctl code o               Int: Ctl, CtlNone for an ordinary value
//	v    = ctl value o              the value, or `break v`'s v
//
// What the callback does with the code is its own graph: it leaves with the
// callee's outcome as its own `continue`, `break` or `break v` would.

// CtlPart is which part of a signalling call's answer CtlRead reads.
type CtlPart uint8

const (
	// CtlPartCode is the control outcome, an Int: CtlNone for an ordinary
	// answer, else the Ctl the callee's control statement gave.
	CtlPartCode CtlPart = iota + 1
	// CtlPartValue is the answered value: an ordinary answer itself, or the
	// value `break v` carries. A `continue` or bare `break` answers none,
	// and reading one is a machine error.
	CtlPartValue
)

func (p CtlPart) String() string {
	switch p {
	case CtlPartCode:
		return "code"
	case CtlPartValue:
		return "value"
	}
	return "part?"
}

// CtlRead reads one part of a signalling call's answer, src, which the call
// wrote into a temporary of type Any.
type CtlRead struct {
	pos  Pos
	dst  Temp
	src  Temp
	part CtlPart
}

// NewCtlRead reads part of the signalling call answer src into dst.
func NewCtlRead(pos Pos, dst, src Temp, part CtlPart) *CtlRead {
	requirePos(pos, "ir.NewCtlRead")
	if dst == NoTemp || src == NoTemp {
		panic("ir.NewCtlRead needs a destination and an answer")
	}
	if part != CtlPartCode && part != CtlPartValue {
		panic("ir.NewCtlRead: part " + strconv.Itoa(int(part)) + " is no part")
	}
	return &CtlRead{pos: pos, dst: dst, src: src, part: part}
}

func (n *CtlRead) Pos() Pos                     { return n.pos }
func (n *CtlRead) Dst() Temp                    { return n.dst }
func (n *CtlRead) Src() Temp                    { return n.src }
func (n *CtlRead) Part() CtlPart                { return n.part }
func (n *CtlRead) AppendUses(dst []Temp) []Temp { return append(dst, n.src) }
func (n *CtlRead) String() string {
	return n.dst.String() + " = ctl " + n.part.String() + " " + n.src.String()
}
func (n *CtlRead) irNode()  {}
func (n *CtlRead) irInstr() {}
