package irbuild

// `irParamShape`'s decline arms, one kind at a time.
//
// The retained populations do not reach every arm, so each is checked here.
// `internal/ir`'s fixtures cover the rule's handling of a declined shape (a
// parameter recording `ValUnknown` leaves the rule silent); they say nothing
// about which kinds decline, which is this function's own decision.
//
// The controls are in the same table, because a function that answered
// `ValUnknown` for everything would pass a decline-only test.

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

func TestIRParamShape_TheDeclinesAndTheirControls(t *testing.T) {
	iface := &ifaceDef{}
	tp := &typeParamDef{}
	leaf := &typeDef{nomi: "Bytes", isDistinct: true, inner: kindInvalid, rtOpaque: true}
	marker := &typeDef{nomi: "Expired", isDistinct: true, inner: kindInvalid}
	wrapper := &typeDef{nomi: "Email", isDistinct: true, inner: kindString}
	enum := &typeDef{nomi: "Colour", isEnum: true}
	record := &typeDef{nomi: "Point", fields: []fieldDef{{}}}

	for _, c := range []struct {
		name string
		k    kind
		want ir.ValShape
		// why is the reason a decline is one, empty for a stated shape.
		why string
	}{
		// The declines.
		{"an operand whose lowering was refused", kindInvalid, ir.ValUnknown,
			"irTypeOf answers nil for tagInvalid"},
		{"an existential", existential(iface), ir.ValUnknown,
			"the concrete type is erased, which is the one thing ir.TypeForm records"},
		{"a bound-free type parameter", typeParamKind(tp), ir.ValUnknown,
			"one body, many instantiations, so the declaration cannot name one shape"},
		{"a bare None", kind{tag: tagBareNone}, ir.ValUnknown,
			"an untyped literal the checker solves from context"},
		{"an rt-opaque stdlib leaf", named(leaf), ir.ValUnknown,
			"a leaf this builder may not look inside, so it builds no ProjInner for one"},
		{"a malformed named kind", kind{tag: tagNamed}, ir.ValUnknown,
			"no def to read a shape off"},

		// The controls, one per shape a parameter can state.
		{"Unit", kindUnit, ir.ValUnit, ""},
		{"Bool", kindBool, ir.ValBool, ""},
		{"Int", kindInt, ir.ValInt, ""},
		{"Float", kindFloat, ir.ValFloat, ""},
		{"String", kindString, ir.ValString, ""},
		{"a tuple", kind{tag: tagTuple}, ir.ValTuple, ""},
		{"a record", kind{tag: tagAnonStruct}, ir.ValStruct, ""},
		{"a function value", kind{tag: tagFunc}, ir.ValFunc, ""},
		{"a list", kind{tag: tagList}, ir.ValContainer, ""},
		{"an untyped empty list", kind{tag: tagEmptyList}, ir.ValContainer, ""},
		{"a declared struct", named(record), ir.ValStruct, ""},
		{"a declared enum", named(enum), ir.ValVariant, ""},
		{"a wrapping distinct", named(wrapper), ir.ValDistinct, ""},
		{"a zero-sized marker", named(marker), ir.ValDistinct, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := irParamShape(c.k); got != c.want {
				t.Fatalf("irParamShape answered %s, want %s", got, c.want)
			}
			if c.why != "" {
				t.Logf("declined: %s", c.why)
			}
		})
	}

	// The Decimal exception, which is the one `rtOpaque` leaf a condition
	// names. `ir.DomainDecimal`'s operands want `ir.ValDecimal`, so declining
	// it would leave every Decimal arithmetic position unfenced while the Int
	// and Float ones are fenced.
	//
	// Skipped rather than faked if the anchor is absent: `decimalKind()`
	// answers `kindInvalid` when `stdHostSpecs` has no Decimal row, and
	// `isDecimalKind` refuses to match `kindInvalid` for the reason its own
	// header gives — matching it would claim every refused expression as a
	// Decimal.
	if d := decimalKind(); d == kindInvalid {
		t.Log("NOTE: Decimal is not anchored in this process, so the exception " +
			"below is untested rather than passing")
	} else if got := irParamShape(d); got != ir.ValDecimal {
		t.Errorf("the anchored Decimal answered %s, want Decimal; it is the one "+
			"rtOpaque leaf a shape condition names", got)
	}

	// The controls are not vacuous. A function answering ValUnknown for
	// everything passes every decline case above, so the count of distinct
	// non-unknown answers is asserted rather than assumed.
	seen := map[ir.ValShape]bool{}
	for _, k := range []kind{kindUnit, kindBool, kindInt, kindFloat, kindString,
		{tag: tagTuple}, {tag: tagAnonStruct}, {tag: tagFunc}, {tag: tagList},
		named(record), named(enum), named(wrapper)} {
		if s := irParamShape(k); s != ir.ValUnknown {
			seen[s] = true
		}
	}
	if len(seen) != 11 {
		t.Errorf("the controls produced %d distinct shapes, want 11; a producer that "+
			"declined everything would pass the decline cases above", len(seen))
	}
}
