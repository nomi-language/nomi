package irbuild

// The role of one `ir.Copy`, which later lowerings in the same body read back.
//
// A `case` or pattern-`if` subject, and a recorded operand, is copied into a
// temporary of its own unless it is already held in one (irHeldValue).

import "github.com/nomi-language/nomi/internal/ir"

// irCopyRole is why the builder copied a value.
type irCopyRole uint8

const (
	// irCopyNone is the zero value: not a copy the builder recorded.
	irCopyNone irCopyRole = iota
	// irCopyForce holds a non-final operand so a later operand's instructions
	// cannot re-evaluate it. effectFree treats the copy as effect-free.
	irCopyForce
	// irCopyHold holds a `case` subject every arm tests.
	irCopyHold
	// irCopyDrop is a non-final expression statement whose value the block
	// discards (`dbg n` as a leading statement).
	irCopyDrop
	// irCopyEmptyValue retypes an empty collection to its contextual type.
	irCopyEmptyValue
	// irCopyInjected stabilizes a placeholder pipe's left value before the
	// other arguments are lowered.
	irCopyInjected
	// irCopyProjection holds a sort key projection the comparator reads twice.
	irCopyProjection
	// irCopyNever retypes a value of Infallible to its contextual type. It
	// never runs: nothing produces the value it reads (irnever.go).
	irCopyNever
)

// irHeldValue reports whether t is already held in a temporary of its own —
// a parameter, a local read, a binding, a slot or an earlier copy — so a
// lowering that needs the value held does not copy it again. A Bool constant
// counts as held; every other instruction's result is copied.
//
// An injected pipe value whose source was itself held answers for its
// source.
func irHeldValue(f *ir.Func, t ir.Temp, sides []irScalarSide) bool {
	switch n := f.Def(t).(type) {
	case nil:
		// A parameter.
		return true
	case *ir.Ref:
		return n.Kind() == ir.RefLocal
	case *ir.Const:
		return n.Kind() == ir.ConstBool
	case *ir.Bind:
		return true
	case *ir.Copy:
		if int(t) < len(sides) && sides[t].copy == irCopyInjected && sides[t].copyPure {
			return irHeldValue(f, n.Src(), sides)
		}
		return true
	case *ir.Slot:
		return true
	}
	return false
}
