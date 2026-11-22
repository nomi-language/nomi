package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// containerCompareAny lowers `List.compare(a, b)` / `Vector.compare(a, b)`
// through containerCompare where the element type's compare is one a
// comparator can be made of, and otherwise through std's `impl Comparable
// for List<T>` (or Vector's) instantiated at the element type, as
// `Comparable.compare(a, b)` is.
func (bl *irScalarBuilder) containerCompareAny(t *ast.Call, args irQualArgs, owner string) (ir.Temp, kind, bool, bool) {
	if v, k, mobile, ok := bl.containerCompare(t, args, owner); ok {
		return v, k, mobile, ok
	}
	if v, k, mobile, ok, handled := bl.ifaceContainerCall(t, args, "compare", bl.g.firstOperandType(t)); handled {
		return v, k, mobile, ok
	}
	return ir.NoTemp, kindInvalid, false, false
}

// containerCompare lowers `List.compare(a, b)` and `Vector.compare(a, b)`:
// std's lexicographic body, whose `Comparable.compare(ha, hb)` on the
// elements is the element type's impl. That impl, the one the checker's
// record of `Comparable.compare` at the element's type names
// (sortComparator), is passed to the machine intrinsic as a function value
// (comparatorOperand), as Iter.sort passes it, so the element order is the
// element type's own body and not a second implementation.
func (bl *irScalarBuilder) containerCompare(t *ast.Call, args irQualArgs, owner string) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if !args.ok || len(args.kinds) != 2 {
		return no()
	}
	var elem kind
	for i, k := range args.kinds {
		var e kind
		switch {
		case owner == "List" && k.tag == tagList && k.comp != nil && len(k.comp.parts) == 1 && (irRetainedListKind(k) || irListTransportKind(k)):
			e = k.comp.parts[0]
		case owner == "Vector":
			var isVector bool
			if e, isVector = vectorElem(k); !isVector || !(irRetainedVectorKind(k) || bl.g.irVectorValueKind(k)) {
				return no()
			}
		default:
			return no()
		}
		if i > 0 && e != elem {
			return no()
		}
		elem = e
	}
	elemType := partCheckerType(bl.g.firstOperandType(t), args.kinds[0], 0)
	p, ok := bl.sortComparator(elem, elemType)
	if !ok {
		return no()
	}
	ref := bl.comparatorOperand(t, elem, elemType, p)
	ordering := stdEnumKind(stdEnumOrdering)
	name := owner + ".compare"
	c := ir.NewHostCall(bl.g.irPos(t.Line, t.Col), bl.f.NewTemp(), irCallSite(t), bl.g.irCalleeSym(name, name),
		args.temps[0], args.temps[1], ref)
	bl.b.Append(c)
	bl.side(c.Dst(), irScalarSide{k: ordering, deferrable: true})
	return c.Dst(), ordering, false, true
}
