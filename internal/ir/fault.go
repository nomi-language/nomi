package ir

// THE EXCEPTIONAL EDGE, AS FAR AS A TRAPPING Int OPERATION REACHES.
//
// Jump, Branch and Return are the whole terminator set, with one successor,
// two and none, WHILE `Arith.Faults()` SAYS AN OPERATION CAN FAULT AT ITS OWN
// POSITION. Twelve operator-and-domain combinations report a fault, and
// without an edge the graph would have nowhere for the fault to go.
//
// A VM has no such edge for free: a dispatch loop that reads `Arith.Faults()`
// and finds a fault has to know where the program counter goes next, and
// "nowhere in the representation" would mean the VM invents the answer.
//
// THE EDGE IS ON THE BLOCK AND NOT ON THE INSTRUCTION, AND NOT A TERMINATOR.
// Three shapes were available and the argument for each is short.
//
//   - A FOURTH TERMINATOR, so a faulting instruction ends its block with two
//     successors. Rejected: it makes every `a + b` a block boundary, so
//     `acc + (acc % 7) + 1` is four blocks, and the block structure stops
//     being the program's control flow and becomes a list of arithmetic
//     operations. `ir.Lint`'s must analysis reads blocks as control flow;
//     this would have made it measure something else.
//   - AN EDGE PER FAULTING INSTRUCTION. Rejected as a field nothing varies:
//     every faulting instruction in one Nomi scope has the SAME handler,
//     because Nomi's only construct that catches a fault is the enclosing
//     frame. A per-instruction field would be N copies of one fact, and the
//     first producer to copy one of them wrongly would have written a graph
//     no consumer could tell was wrong.
//   - AN EDGE PER BLOCK. Taken. It is the exception-table shape — one handler
//     covering a straight run of instructions — and it says exactly what
//     varies: a `try`'s body has a handler, the code after it does not, and
//     the boundary between them is a block boundary already.
//
// NO EDGE MEANS THE FAULT LEAVES THE FUNCTION, and that is recorded rather
// than left as silence. `Block.Fault` answers `(id, false)` and the second
// result is the statement: this block's faults unwind to the caller, which is
// what the VM does, so the default is the behaviour and not an absence.
//
// WHAT IS NOT TAKEN. No producer SETS a fault edge, because no Nomi construct
// catches a fault: `try` is Nomi's non-local exit
// over `Result`/`Maybe` and is a different mechanism, and an assertion's
// failure is a Render plus a report rather than a caught trap. So the
// population of set edges is empty in production and the lint rule below is
// checked against planted graphs. THAT IS STATED RATHER THAN SMOOTHED OVER:
// the rule's value here is that it makes the edge checkable before a producer
// exists, and `CanFault` — which IS exercised over every retained function in
// the corpus — is the half with a population today.

// Faulting is an instruction that can fault at its own position.
//
// AN INTERFACE AND NOT A FIELD, for `Arith.Faults`' own reason: whether an
// operation can fault is DERIVED from what the operation is, so a consumer
// asks the node rather than reading a bit a producer had to set. Two node
// types satisfy it today — `*Arith` (twelve operator-and-domain combinations)
// and `*Proj` — which is what keeps this from being an interface with one
// implementor dressed up as a general rule.
type Faulting interface {
	Instr
	// Faults is the set of ways this instruction can fault at its own
	// position, or 0.
	Faults() Faults
}

// InstrFaults is the set of ways in can fault, or 0 for an instruction that
// cannot.
func InstrFaults(in Instr) Faults {
	f, canFault := in.(Faulting)
	if !canFault {
		return 0
	}
	return f.Faults()
}

// SetFault gives this block an exceptional successor: where control goes when
// any instruction in it faults.
//
// pos is the HANDLER BOUNDARY's position — the construct that catches, not
// the instruction that faults. A faulting instruction already carries its own
// position, which is what the trap message names; this one is what a
// consumer blames the handler frame on.
//
// It rejects a second edge for `SetTerm`'s reason: one block has one handler,
// and a producer that sets two has written a graph whose second edge silently
// wins. A block whose instructions can fault in two different scopes is two
// blocks.
func (b *Block) SetFault(pos Pos, target BlockID) {
	requirePos(pos, "SetFault")
	if b.hasFault {
		panic("ir: Block.SetFault: " + b.id.String() + " already faults to " + b.fault.String())
	}
	b.fault, b.faultPos, b.hasFault = target, pos, true
}

// Fault is the block control transfers to when an instruction here faults,
// and whether there is one. No edge means the fault leaves the function; see
// the file header on why that is the recorded default rather than silence.
func (b *Block) Fault() (BlockID, bool) { return b.fault, b.hasFault }

// FaultPos is the handler boundary's position, valid only when Fault reports
// an edge.
func (b *Block) FaultPos() Pos { return b.faultPos }

// CanFault reports whether any instruction in this block can fault.
//
// DERIVED, never stored. A producer that recorded it would be recording a
// function of the instructions it just appended, and the first append after
// the record would make it wrong.
func (b *Block) CanFault() bool {
	for _, in := range b.instrs {
		if InstrFaults(in).Any() {
			return true
		}
	}
	return false
}

// FirstFaultAt is the index of the first instruction in this block that can
// fault, or len(Instrs()) when none can.
//
// It exists for `Lint`'s available-definitions analysis, which needs to know
// what has been computed when control leaves along the fault edge: everything
// before the first faulting instruction, and nothing after it. A consumer
// that took the whole block's definitions would conclude a handler can read a
// temporary the fault prevented being written.
func (b *Block) FirstFaultAt() int {
	for i, in := range b.instrs {
		if InstrFaults(in).Any() {
			return i
		}
	}
	return len(b.instrs)
}
