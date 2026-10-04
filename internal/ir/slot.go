package ir

// DECLARE STORAGE WITH A TYPE AND NO VALUE. `ir.Bind` names a value and
// `ir.Copy` moves one; neither has a shape for a declaration with no source.
//
// A bytecode VM has no return-the-value path: an `if` in expression position
// is two arms that jump to a join, and each arm has to leave the answer
// somewhere the join can read it. That place is a frame slot, and it has to be
// declared so the frame can be sized.
//
// WHAT THE NODE DOES NOT MODEL, which the repricing document asks to be
// decided rather than discovered: the storage itself. The field that would
// have made it one consumer's is absent. A Slot says a declaration exists, what type it
// holds and where it was written. It does not say where the storage lives —
// a Go `var` in the enclosing block, a frame register, a stack offset — and
// it does not say what the storage holds before it is written. Go's `var`
// zeroes; a VM register may hold whatever the frame allocator left. NOTHING
// IN THIS NODE LETS A CONSUMER DEPEND ON THAT, because `Dst` is NoTemp: the
// declaration is not a definition, so `Lint`'s must analysis still requires
// every path to a read to pass through a WRITE. That is the whole of the
// discipline and it is what makes the node consumer-independent.
//
// THE DESTINATION IS NoTemp AND `Slot()` IS SEPARATE, which is the one design
// decision here worth arguing.
//
// The obvious shape is `Dst() == the declared temporary`, and it is wrong.
// `Instr.Dst` is read by `Func.noteDef` and by `Lint`'s available-definitions
// analysis, both of which mean "this instruction COMPUTES the value in that
// temporary". A slot computes nothing. If a Slot counted as a definition then
// `var s T` followed by a write in ONE arm of two would pass the must
// analysis, and the shape `gen.slot`/`gen.fixSlot` exists to produce —
// several writers into one destination — would stop being checkable at
// exactly the point a VM starts depending on it. Measured in lint_test.go:
// TestLint_ASlotIsNotADefinition plants that arm.
//
// Retained function results, bindings and branch operands use this node. An
// expected type can determine storage before lowering the arms; an inferred
// branch constructs and inserts its declaration once the arms agree. The Go
// consumer may use its slot/fixSlot spelling helpers, but the IR declaration
// itself is immutable and never patched.

// Slot declares storage with a type and no value.
type Slot struct {
	pos  Pos
	slot Temp
	ty   *Type
}

// NewSlot declares storage for a value of type ty, named by temporary slot.
//
// pos is the DECLARATION's position, not the position of whatever writes it.
// A frame's layout is blamed on where the storage was declared, which is the
// line a debugger stops on for `var rN R`.
func NewSlot(pos Pos, slot Temp, ty *Type) *Slot {
	requirePos(pos, "NewSlot")
	if slot == NoTemp {
		panic("ir: NewSlot: storage with no name cannot be written or read; do not declare it")
	}
	if ty == nil {
		panic("ir: NewSlot: storage with no type cannot be sized; " +
			"a slot's type is the whole of what distinguishes it from a temporary")
	}
	return &Slot{pos: pos, slot: slot, ty: ty}
}

// Slot is the temporary that names this storage.
func (s *Slot) Slot() Temp { return s.slot }

// Type is the type of the value the storage holds.
func (s *Slot) Type() *Type { return s.ty }

// InsertSlot places a storage declaration during graph construction, after its
// type has been determined by lowering the region that writes it. It may precede
// instructions in an already terminated block. A slot defines no value, so this
// does not change the function's defining-instruction index or initialization
// facts; every path must still write the slot before reading it.
func (b *Block) InsertSlot(index int, slot *Slot) {
	if slot == nil || index < 0 || index > len(b.instrs) {
		panic("ir: Block.InsertSlot: invalid declaration or instruction position")
	}
	requirePos(slot.Pos(), "Block.InsertSlot")
	b.instrs = append(b.instrs, nil)
	copy(b.instrs[index+1:], b.instrs[index:])
	b.instrs[index] = slot
	if b.owner != nil {
		b.owner.noteDef(slot)
	}
}

func (s *Slot) Pos() Pos { return s.pos }

// Dst is NoTemp. A declaration is not a definition; see the file header.
func (s *Slot) Dst() Temp { return NoTemp }

// AppendUses appends nothing: a declaration reads no value.
func (s *Slot) AppendUses(dst []Temp) []Temp { return dst }

func (s *Slot) String() string {
	return "slot " + s.slot.String() + " " + s.ty.Name()
}

func (s *Slot) irNode()  {}
func (s *Slot) irInstr() {}
