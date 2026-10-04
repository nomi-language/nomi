package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// untypedListPair types an untyped `[]` operand of a two-list call in place:
// beside a typed list it takes that list's type, and beside another `[]`
// both take `List<Int>`. Two empty lists have no element for a comparator or
// an equality to read, so the element type chosen there is unobservable; it
// only gives the temporaries a stored type. False when a `[]` cannot be
// coerced.
func (bl *irScalarBuilder) untypedListPair(t *ast.Call, args irQualArgs) bool {
	lk, rk := args.kinds[0], args.kinds[1]
	want := kindInvalid
	switch {
	case lk.tag == tagEmptyList && rk.tag == tagEmptyList:
		want = listKindIn(bl.g, kindInt)
	case lk.tag == tagEmptyList && rk.tag == tagList:
		want = rk
	case rk.tag == tagEmptyList && lk.tag == tagList:
		want = lk
	default:
		return true
	}
	for i := range args.kinds {
		if args.kinds[i].tag != tagEmptyList {
			continue
		}
		var at ast.Node = t
		if i < len(t.Args) {
			at = t.Args[i]
		}
		v, k, ok := bl.coerceEmpty(at, args.temps[i], args.kinds[i], want)
		if !ok {
			return false
		}
		args.temps[i], args.kinds[i] = v, k
	}
	return true
}

// containerCompareAny lowers `List.compare(a, b)` / `Vector.compare(a, b)`
// through containerCompare where the element comparator is one it can name,
// and otherwise through std's `impl Comparable for List<T>` (or Vector's)
// instantiated at the element type, as `Comparable.compare(a, b)` is: that
// body's element comparison dispatches in turn, so a List of Lists, of Bools
// or of tuples compares by std's own lexicographic body.
func (bl *irScalarBuilder) containerCompareAny(t *ast.Call, args irQualArgs, owner string) (ir.Temp, kind, bool, bool) {
	if v, k, mobile, ok := bl.containerCompare(t, args, owner); ok {
		return v, k, mobile, ok
	}
	if v, k, mobile, ok, handled := bl.ifaceContainerCall(t, args, "Comparable", "compare"); handled {
		return v, k, mobile, ok
	}
	return ir.NoTemp, kindInvalid, false, false
}

// containerCompare lowers `List.compare(a, b)` and `Vector.compare(a, b)`:
// std's lexicographic body, whose `Comparable.compare(ha, hb)` on the
// elements is the element type's impl. That impl is passed to the machine
// intrinsic as a function value, as Iter.sort passes it, so the element
// order is the element type's own body and not a second implementation.
func (bl *irScalarBuilder) containerCompare(t *ast.Call, args irQualArgs, owner string) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if !args.ok || len(args.kinds) != 2 {
		return no()
	}
	if owner == "List" && !bl.untypedListPair(t, args) {
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
	ordering := stdEnumKind(stdEnumOrdering)
	var ref *ir.Ref
	if irNominalElemKind(elem) {
		local := bl.localCompareItem(elem)
		if local == nil {
			return no()
		}
		ref = ir.NewRefFunc(bl.g.irNodePos(t), bl.f.NewTemp(), bl.g.irCalleeSym(local, elem.nomi()+"."+local.name))
		bl.b.Append(ref)
		bl.side(ref.Dst(), irScalarSide{k: funcKindIn(bl.g, local.params, local.result)})
	} else {
		if elem != kindInt && elem != kindFloat && elem != kindString && elem != kindBool {
			return no()
		}
		cmp := bl.g.stdCompareAt(elem, ordering)
		if cmp == nil || cmp.irBody == nil {
			return no()
		}
		ref = ir.NewRefFunc(bl.g.irNodePos(t), bl.f.NewTemp(), cmp.irBody.Sym())
		bl.b.Append(ref)
		bl.side(ref.Dst(), irScalarSide{k: funcKindIn(bl.g, cmp.params, cmp.result)})
	}
	name := owner + ".compare"
	c := ir.NewHostCall(bl.g.irPos(t.Line, t.Col), bl.f.NewTemp(), irCallSite(t), bl.g.irCalleeSym(name, name),
		args.temps[0], args.temps[1], ref.Dst())
	bl.b.Append(c)
	bl.side(c.Dst(), irScalarSide{k: ordering, deferrable: true})
	return c.Dst(), ordering, false, true
}
