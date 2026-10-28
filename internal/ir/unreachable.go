package ir

import "strconv"

// THE `unreachable` TRAP: what an `unreachable "reason"` expression lowers
// to.
//
// `unreachable` is the program's claim that it never reaches a point: an
// invariant it guarantees itself. Reaching it is a bug, and stops the
// program with rt.UnreachableError's text, `line N: unreachable: <reason>`,
// at the program's line, as any other trap. The text is rt's.
//
// It has a destination for Todo's reason (todo.go): the checker gives
// `unreachable` the type its position expects, so to the code around it it
// is an operand of that type, whose temporary is typed and never written.
type Unreachable struct {
	pos    Pos
	dst    Temp
	reason string
}

// NewUnreachable is the trap an `unreachable` takes. pos is the keyword's
// position, whose line the report names; reason is the literal's text.
func NewUnreachable(pos Pos, dst Temp, reason string) *Unreachable {
	requirePos(pos, "NewUnreachable")
	return &Unreachable{pos: pos, dst: dst, reason: reason}
}

// Reason is the invariant the programmer stated.
func (n *Unreachable) Reason() string { return n.reason }

// Faults is FaultUnreachable, always.
func (n *Unreachable) Faults() Faults { return FaultUnreachable }

func (n *Unreachable) Pos() Pos                     { return n.pos }
func (n *Unreachable) Dst() Temp                    { return n.dst }
func (n *Unreachable) AppendUses(dst []Temp) []Temp { return dst }

func (n *Unreachable) String() string {
	return n.dst.String() + " = unreachable " + strconv.Quote(n.reason)
}
func (n *Unreachable) irNode()  {}
func (n *Unreachable) irInstr() {}
