package irbuild

import (
	"slices"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// Covariant conversion without work (spec §5 *Covariant conversion*). The
// checker admits a value where the same type at wider type arguments is
// expected when each part converts by its parameter's variance
// (internal/analysis, type_arg_conversion.go). Where each element of a std
// collection is, unchanged, an element of the wider type (an Int where a
// Display is, since the VM's existential is the concrete value; a function
// whose parameters and result convert so; a nested collection, tuple or
// record whose parts do), the value is the wider value already, so the
// builder relabels it: one copy of the reference, whatever the collection
// holds, and no element is read (TestRelabel_ConvertsNoElement counts it).
// Any other conversion is a rebuild (irembedsrebuild.go). Where the checker
// recorded the conversion, its record's cost decides (relabelsAt).
//
// A record (a tuple, an anonymous record, a Maybe's or Result's variant)
// may then hold a scalar in a scalar slot where its new type says a
// reference: the VM reads a field through the record's own descriptor
// (vm.frame.writeField), and equality and hashing read each slot by its own
// layout (rt.Keys.hashSlot), so the relabelled value reads, compares and
// hashes as one built at the wider type.

// relabels reports whether a value of kind have is, unchanged, a value of
// kind want. A generic struct instance an element erases into its interface
// is noted erased as an erasure of that element is (irErases).
func (bl *irScalarBuilder) relabels(have, want kind) bool {
	if have == want {
		return true
	}
	if irConjKind(have) {
		// A conjunction widening to a member or to fewer members
		// (conjunction.go).
		return conjHolds(have, want)
	}
	if irConjKind(want) {
		// A value entering a conjunction unboxed.
		return bl.g.irErases(want, have) && bl.g.irBoxPlanFor(want, have, nil) == nil
	}
	if irExistentialKind(want) {
		return bl.erasesUnboxed(want, have)
	}
	if have.tag != want.tag {
		return false
	}
	switch have.tag {
	case tagList, tagMap, tagTuple, tagAnonStruct, tagSeq:
		// An `Iter<Int>` where an `Iter<Display>` is expected yields each
		// Int unchanged, which is the Display value already.
		if have.comp == nil || want.comp == nil || len(have.comp.parts) != len(want.comp.parts) || !slices.Equal(have.comp.names, want.comp.names) {
			return false
		}
		for i := range have.comp.parts {
			if !bl.relabels(have.comp.parts[i], want.comp.parts[i]) {
				return false
			}
		}
		return true
	case tagFunc:
		if have.comp == nil || want.comp == nil || !irCallableValueKind(want) {
			return false
		}
		hp, wp := funcParams(have), funcParams(want)
		if len(hp) != len(wp) {
			return false
		}
		for i := range hp {
			// A call through want passes want's arguments to have's body.
			if !bl.relabels(wp[i], hp[i]) {
				return false
			}
		}
		return bl.relabels(funcResult(have), funcResult(want))
	case tagNamed:
	default:
		return false
	}
	if he, ok := setElem(have); ok {
		we, wok := setElem(want)
		return wok && bl.relabels(he, we)
	}
	if he, ok := vectorElem(have); ok {
		we, wok := vectorElem(want)
		return wok && bl.relabels(he, we)
	}
	hd, wd := have.def, want.def
	if hd == nil || wd == nil || hd.preludeOf == nil || wd.preludeOf == nil || hd.preludeOf.spec != wd.preludeOf.spec ||
		!irRelabelPrelude(hd) || len(hd.preludeArgs) != len(wd.preludeArgs) {
		return false
	}
	for i := range hd.preludeArgs {
		if !bl.relabels(hd.preludeArgs[i], wd.preludeArgs[i]) {
			return false
		}
	}
	return true
}

// relabelsAt is relabels for the value of the expression at, as the
// checker's record of its conversion decides (analysis.Conversion.Cost):
// relabelled where the record says it converts with no work, rebuilt where
// it says it is rebuilt, and by its kinds where the record leaves it to
// each instance of a generic body, or there is none. The record is what
// the hover reads (internal/hoverdoc, conversions.go), so what the builder
// does is what the hover says. agrees is false where the record says no
// work and the kinds cannot relabel: the builder declines rather than
// rebuild a value the hover says costs nothing.
func (bl *irScalarBuilder) relabelsAt(at ast.Node, have, want kind) (relabel, agrees bool) {
	rec := bl.g.conversionAt(at)
	if rec == nil || rec.Cost == analysis.ConversionPerInstance || bl.g.project(rec.From) != have || bl.g.project(rec.To) != want {
		return bl.relabels(have, want), true
	}
	switch {
	case rec.Cost == analysis.ConversionRebuilds:
		return false, true
	case !bl.relabels(have, want):
		bl.g.dc.note("a " + have.nomi() + " the checker says converts to " + want.nomi() + " with no work, which its kinds cannot relabel")
		return false, false
	}
	return true, true
}

// passesRelabelled reports whether the argument at, of kind have, passes
// to a parameter of kind want as it is (relabelsAt).
func (bl *irScalarBuilder) passesRelabelled(at ast.Node, have, want kind) bool {
	relabel, agrees := bl.relabelsAt(at, have, want)
	return relabel && agrees
}

// irRelabelPrelude reports whether d is an instance of the prelude enums
// whose type arguments convert: Maybe and Result.
func irRelabelPrelude(d *typeDef) bool {
	return d.preludeOf != nil && (d.preludeOf.spec.nomi == "Maybe" || d.preludeOf.spec.nomi == "Result")
}

// relabel answers v, of kind have, as a value of kind want that relabels
// (relabels): a copy of the reference, typed want.
func (bl *irScalarBuilder) relabel(at ast.Node, v ir.Temp, want kind) ir.Temp {
	n := ir.NewCopy(bl.g.irNodePos(at), bl.f.NewTemp(), v)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: want, copy: irCopyRelabel})
	return n.Dst()
}
