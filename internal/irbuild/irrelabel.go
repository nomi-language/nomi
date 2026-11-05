package irbuild

import (
	"slices"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// Type argument conversion without work (spec §5 *Type argument
// conversion*). The checker admits a std collection's value where the same
// collection at wider type arguments is expected when each element is,
// unchanged, an element of the wider type: an Int where a Display is (the
// VM's existential is the concrete value), a function whose parameters and
// result convert so, a nested collection, tuple or record whose parts do
// (analysis.carriesOwnType and unifyAssignArg). Such a value is the wider
// value already, so the builder relabels it: one copy of the reference,
// whatever the collection holds, and no element is read
// (TestRelabel_ConvertsNoElement counts it).
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
	if irExistentialKind(want) {
		return bl.erasesUnboxed(want, have)
	}
	if have.tag != want.tag {
		return false
	}
	switch have.tag {
	case tagList, tagMap, tagTuple, tagAnonStruct:
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
