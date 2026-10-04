package irbuild

import "github.com/nomi-language/nomi/internal/ir"

// The shape of a parameter's value.
//
// `ir.Lint`'s `RuleOperandShape` asks, at each position `internal/vm` checks
// an operand's type, whether the graph states a shape that contradicts what
// the position needs. A parameter's temporary is written by nothing, so no
// derivation over a body can say what arrives in it. The shape recorded on
// `ir.Param` is that fact. See `ir.Param`'s own header.
//
// # Why the producer can answer it
//
// `irFuncShellFor` already holds the parameter's kind — `paramGo[i].k`, which
// is `sig.params[i]`, which came off the checked AST. So this is a read, not
// an inference, and there is exactly ONE production `AddParam` call site to
// supply it at.
//
// # Why it is a shape and not an `*ir.Type`
//
// `ir.Slot` and `ir.Cell` carry an `*ir.Type`, so the shape of this field
// looks wrong beside them. An `*ir.Type` is a NAME plus a two-member
// `TypeForm` plus a table pointer, its methods are `Name`, `Form`,
// `Existential` and `String`, and `ir/table.go` forbids reading the name as an
// identity. Nothing in an `*ir.Type` says Int. A `ValShape` answers the
// question and holds nothing else.
//
// # The decline is a first-class answer, and the list is closed
//
// `kind` has twenty tags and a value shape is not derivable from all of them.
// `ir.ValUnknown` is what this function answers where it is not, and the rule
// is then silent at that position. A wrong shape is the failure mode, not a
// missing one: a recorded shape lands at a position whose demand is also
// stated, so the two either agree or `RuleOperandShape` reports the
// contradiction, and `irScalarLower` panics on it over the whole retained
// population.
//
// So the arms below are ordered by how they DISCRIMINATE and every decline
// carries the reason it is one:
//
//	tagInvalid      `g.irTypeOf` answers nil for it. The operand's own lowering
//	                was refused and the refusal is already recorded.
//	tagIface        an EXISTENTIAL: a value whose concrete type has been
//	                ERASED, which is the one distinction `ir.TypeForm`
//	                records. There is no static shape to state.
//	tagTypeParam    a bound-free type parameter inside the body of the generic
//	                function that declares it. One body, many instantiations,
//	                value-passing — so the shape differs per call site and the
//	                declaration cannot name one.
//	tagSeq          a lowered `Iter<T>`, distinct from a callable function.
//	                Retained sequences cross function-value signatures,
//	                but ValShape declares no sequence shape, so this
//	                declines rather than mislabelling it ValFunc.
//	tagBareNone     the prelude `None` written with no type argument: an
//	                UNTYPED literal whose type the checker solves from context.
//	                `ir.ConstBareVariant` writes `ValVariant` for the
//	                CONSTRUCTED form, and a bare `None` in a parameter position
//	                has been discharged by `coerce` before it gets here, so
//	                this arm is a fence rather than a population.
//	an rtOpaque def a blessed stdlib primitive whose Go type rt declares
//	                (`rt.Bytes`, `rt.Duration`, `rt.Instant`). types.go: a LEAF
//	                "this builder may not look inside", so it builds no
//	                `ir.ProjInner` for one and `ValDistinct` would be a claim
//	                about a representation nothing here inspects. THE ANCHORED
//	                `Decimal` IS THE EXCEPTION, because it is the one such leaf
//	                a condition names — `ir.DomainDecimal`'s operands.
//	a generic std   `Set<Int>`, `Vector<Int>`, `Sender<T>`. TWO READINGS
//	instance        DISAGREE and neither is obviously the graph's: this
//	                builder's def for a `genStructOf` instance carries FIELDS,
//	                which reads as `ValStruct`, and a Nomi `#{1, 2}` lowers to
//	                `ir.MakeSet`, which `shapeWritten` reads as
//	                `ValContainer`. The producer declines rather than picking
//	                one and making a `Set` parameter contradict a `Set`
//	                literal.

// irParamShape is the shape of the values a parameter of kind k receives, or
// `ir.ValUnknown` where this producer declines to state one.
//
// A CLOSED SWITCH OVER THE TAG with the named-type arm split by the same
// discriminators `shapeWritten` reads off an `ir.Make` and an `ir.Const`, so a
// parameter's recorded shape and a constructed value's derived shape are the
// same answer for the same Nomi type. That agreement is the property the whole
// rule rests on: they meet at `shapeOfTemp`, which merges a declaration with
// every writer and declines on disagreement, so two readings that differ would
// silently blank every position instead of reporting one.
func irParamShape(k kind) ir.ValShape {
	switch k.tag {
	case tagUnit:
		return ir.ValUnit
	case tagBool:
		return ir.ValBool
	case tagInt:
		return ir.ValInt
	case tagFloat:
		return ir.ValFloat
	case tagString:
		return ir.ValString
	case tagFunc:
		return ir.ValFunc
	case tagTuple:
		return ir.ValTuple
	case tagAnonStruct:
		// A RECORD. `ir.MakeRecord` and `ir.MakeStruct` both write
		// `ir.ValStruct`, and `ProjRecordField`'s demand is the same as
		// `ProjField`'s, so the two are one shape here for the same reason
		// they are one arm there.
		return ir.ValStruct
	case tagList, tagMap, tagEmptyList, tagEmptyMap, tagEmptySet, tagEmptyVector:
		return ir.ValContainer
	case tagNamed:
		return irNamedParamShape(k)
	}
	// tagInvalid, tagIface, tagTypeParam, tagSeq, tagBareNone. See the header:
	// each is a decline with a stated reason, not a gap.
	return ir.ValUnknown
}

// irNamedParamShape is `irParamShape`'s named-type arm.
//
// ORDER IS THE WHOLE CONTENT OF THIS FUNCTION, because the flags are not
// disjoint: every `stdHostDefs` entry sets `isDistinct` AND `rtOpaque`, a
// generic std struct instance has `fields` like any struct, and the anchored
// `Decimal` is an `rtOpaque` leaf that one condition nevertheless names. So the
// most specific test comes first and the struct answer is the default rather
// than a case.
func irNamedParamShape(k kind) ir.ValShape {
	if k.def == nil {
		// A malformed named kind. `irKindName` renders it `<named>` and
		// `irTypeOf` still interns it, so this is reachable in principle and
		// there is nothing to read a shape off.
		return ir.ValUnknown
	}
	if isDecimalKind(k) {
		// The one rtOpaque leaf a shape condition names. `ir.DomainDecimal`'s
		// operands want `ir.ValDecimal` and `ir.ConstDecimal` writes it, so
		// declining here would leave the Decimal arithmetic positions unfenced
		// while every Int and Float one is fenced.
		return ir.ValDecimal
	}
	if k.def.rtOpaque {
		return ir.ValUnknown
	}
	if _, owner, isChannel := channelElem(k); isChannel && owner == "Channel" {
		// std's Channel, whose two halves `ProjField` reads: the VM holds it
		// as a struct of them.
		return ir.ValStruct
	}
	if _, _, isGenStruct := genStructOf(k); isGenStruct {
		return ir.ValUnknown
	}
	if _, _, isGenHost := genHostOf(k); isGenHost {
		return ir.ValUnknown
	}
	if k.def.isEnum {
		// Including the prelude's `Maybe` and `Result`: `ir.MakeVariant` and
		// `ir.ConstBareVariant` both write `ir.ValVariant`, and
		// `ProjPayload`'s demand is a variant, so an enum instance is one
		// answer whether the builder declared it or rt did.
		return ir.ValVariant
	}
	if k.def.isDistinct {
		// A user `type Email String`, a std opaque newtype, or a zero-sized
		// marker. `ir.MakeDistinct` writes `ir.ValDistinct` and `ProjInner`
		// demands it. A marker has no inner and the VM refuses the unwrap on
		// its own terms — "unwraps the zero-sized type %s, which carries
		// nothing" — which is a different condition from this one and stays
		// the VM's.
		return ir.ValDistinct
	}
	return ir.ValStruct
}
