package irbuild

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

// Tests for the `make` and `proj` classes (irmake.go, irproj.go).

// TestProj_FaultsIsTheOnlyPredicateAndItDiscriminates plants a positive on the
// one predicate this class has.
//
// A predicate that is constantly false is decoration, so this builds two
// nodes, one of each answer, and requires each to report its own.
func TestProj_FaultsIsTheOnlyPredicateAndItDiscriminates(t *testing.T) {
	at := ir.At("t.nomi", 1, 1)
	sym := ir.NewSymbol("Shape")

	partial := ir.NewProjEnumField(at, 1, 2, sym, "radius", true, ir.ValUnknown)
	total := ir.NewProjEnumField(at, 3, 2, sym, "radius", false, ir.ValUnknown)
	if !partial.Faults() {
		t.Error("an enum field no variant supplies everywhere reports Faults() false")
	}
	if total.Faults() {
		t.Error("an enum field EVERY variant supplies reports Faults() true, so the " +
			"predicate is constant and distinguishes nothing")
	}

	// And no other kind can fault: every other projection's location is fixed
	// by the subject's static type.
	for _, p := range []*ir.Proj{
		ir.NewProjField(at, 1, 2, sym, "x", ir.ValUnknown),
		ir.NewProjRecordField(at, 1, 2, "x", ir.ValUnknown),
		ir.NewProjSlot(at, 1, 2, 0, ir.ValUnknown),
		ir.NewProjPayload(at, 1, 2, sym, "Circle", 0, ir.ValUnknown),
		ir.NewProjIfaceField(at, 1, 2, sym, "size", ir.ValUnknown),
		ir.NewProjInner(at, 1, 2, sym, ir.ValUnknown),
	} {
		if p.Faults() {
			t.Errorf("%s reports Faults(); only a tag-dispatched read can fail", p.Kind())
		}
	}
}
