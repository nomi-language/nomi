package ir

// THE NO-MATCH FAULT: the instruction a `case`'s fallthrough block holds.
//
// It exists because a consumer that READS the graph back needs it, where the
// producer alone did not.
//
// `Arms.Fall` says the fallthrough block is "one block in two roles: control
// arrives there exactly when every test answered false, and it leaves there
// for the join. What is IN it — nothing, an else body, or a trap — is its
// INSTRUCTIONS rather than its shape." This instruction is the trap.
// `internal/irbuild`'s `caseInto` builds the block and the trap at the same
// call, so the producer never has to read its own graph back.
//
// A CONSUMER THAT READS THE GRAPH DOES. A read of a retained `case` walks
// blocks and terminators, and the fallthrough block is where the trap goes —
// so with no instruction there the reader would either FABRICATE the trap
// (putting a Nomi-observable fault below the instruction set) or read the
// `case`'s line off an EMPTY block that exists only so a line can be read off
// it, which is a node modelling one consumer.
//
// THE FAULT IS THE NOMI SEMANTIC AND THE TEXT IS IN rt, which is what makes
// this an instruction rather than a consumer's device: no arm matching is a
// runtime fault, not a fallthrough, and its text is rt.NoCaseMatch's. That is
// the fault/delivery division of ir.go's package header at a second
// operation — the predicate-and-message pair
// `rt/arith.go` carries for overflow — so no consumer owns the message.
//
// NO KIND FIELD. There is one way to reach this instruction in Nomi and it is
// `case`: the node IS "no arm of this `case` matched the scrutinee". A kind
// would be a field nothing varies, which is `fault.go`'s own rejection of a
// per-instruction fault edge.
//
// NO OPERANDS AND NO DESTINATION. The scrutinee is not read here — every arm
// has already tested it — and nothing follows, so there is nothing to write.
// `Dst` is NoTemp and `AppendUses` appends nothing, which is `Record`'s shape
// for the destination half and `Slot`'s for neither.
//
// IT IS AN INSTRUCTION AND NOT A TERMINATOR, which is the one place a reader
// might expect otherwise because control does not continue past it. Three
// reasons and the third is the one that decided it:
//
//  1. `fault.go` already rejected a fourth terminator, for a reason that
//     applies here unchanged: the terminator set is the CONTROL FLOW, and
//     `Lint`'s must analysis and `Arms`' reachability both read it as such.
//  2. The producer puts something AFTER it: the block's exit to the join.
//     It is never reached at run time and it is real code the block leaves
//     by.
//  3. A terminator with no successors is `Return`, and this is not a return.
//     A block whose terminator said "nothing follows" would be
//     indistinguishable from the function's exit to every analysis in
//     `lint.go`.
//
// `RuleNoMatchIsLast` is what keeps (2) honest: an instruction after the trap
// is code the graph claims is reachable and no execution reaches. See lint.go.

// NoMatch faults because no arm of a `case` matched its scrutinee.
type NoMatch struct {
	pos Pos
}

// NewNoMatch is the fault a `case` takes when every arm's test answered false.
//
// pos is the `case`'s OWN position and not the last arm's, which is what the
// report prints: `internal/irbuild`'s `caseInto` builds its fallthrough block at
// the `case` node, and the fault blames the `case`. A reader of the retained graph
// takes the trap's line from here rather than from the block, because the line
// is the instruction's fact.
func NewNoMatch(pos Pos) *NoMatch {
	requirePos(pos, "NewNoMatch")
	return &NoMatch{pos: pos}
}

// Faults is FaultNoMatch, always.
//
// THE FIRST `Faulting` INSTRUCTION WHOSE FAULT SET DOES NOT VARY, and that is
// consistent with the interface rather than an exception to it. `fault.go`
// argues for an interface over a field because "whether an operation can fault
// is DERIVED from what the operation is" — and for this operation what it is
// answers immediately. `*Arith` varies over twelve combinations and `*Proj`
// over two; a third implementor whose answer is constant is what makes the
// derivation a rule instead of a table.
func (n *NoMatch) Faults() Faults { return FaultNoMatch }

func (n *NoMatch) Pos() Pos                     { return n.pos }
func (n *NoMatch) Dst() Temp                    { return NoTemp }
func (n *NoMatch) AppendUses(dst []Temp) []Temp { return dst }

func (n *NoMatch) String() string { return "nomatch" }
func (n *NoMatch) irNode()        {}
func (n *NoMatch) irInstr()       {}
