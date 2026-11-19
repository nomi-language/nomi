package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

func irRetainedSetKind(k kind) bool {
	elem, ok := setElem(k)
	return ok && irSetElemKind(elem)
}

// irSetElemKind is a retained set's element domain: every map key
// (irMapKeyKind), plus the structural containers over elements (a List, a
// Vector, a Maybe or Result, a Set). The VM hashes and compares an element
// as `==` does: a declared type through its hand-written Equatable and
// Hashable, structurally otherwise (vm/keys.go).
func irSetElemKind(k kind) bool {
	if irScalarLeafKind(k) || isDecimalKind(k) || irLeafTupleKind(k) || irMapKeyKind(k) {
		return true
	}
	if k.tag == tagList && irRetainedListKind(k) {
		return true
	}
	if elem, isVector := vectorElem(k); isVector && irSetElemKind(elem) {
		return true
	}
	if elem, isSet := setElem(k); isSet && irSetElemKind(elem) {
		return true
	}
	if irPreludeElemKind(k) {
		for _, v := range k.def.variants {
			for _, pl := range v.payloads {
				if !irSetElemKind(pl.k) {
					return false
				}
			}
		}
		return true
	}
	return false
}

func (bl *irScalarBuilder) setMake(t *ast.ListLit) (ir.Temp, kind, bool, bool) {
	// kindInvalid: sentinel — no expected element kind.
	return bl.setMakeOf(t, kindInvalid)
}

// setMakeOf builds a set literal; elem, when not kindInvalid, is the
// expected element kind each item is lowered against.
func (bl *irScalarBuilder) setMakeOf(t *ast.ListLit, elem kind) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if len(t.Spreads) == 1 && t.Spreads[0].At == 0 {
		return bl.setUpdate(t)
	}
	if len(t.Spreads) > 0 {
		return bl.setSpreadLit(t)
	}
	if len(t.Items) == 0 {
		return bl.emptyLiteral(t, setSpreadJoin, kindInvalid)
	}
	want := elem
	// kindInvalid: sentinel — the items give the element kind.
	if parts := bl.literalParts(t, setSpreadJoin); want == kindInvalid && parts != nil {
		want = parts[0]
	}
	values, elem, ok := bl.lowerSameKindItems(t, setSpreadJoin, t.Items, want, irSetElemKind)
	if !ok {
		return no()
	}
	// Every element lowerSameKindItems admits is a set element
	// (irSetElemKind), and std's Set instantiates at each.
	k := bl.g.setKindOf(elem)
	if !irRetainedSetKind(k) {
		panic("irbuild: no retained Set over the element " + elem.nomi())
	}
	ok = bl.g.mapKeyOK(elem, t)
	if !ok {
		return no()
	}
	n := ir.NewMakeSet(bl.g.irNodePos(t), bl.f.NewTemp(), values)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: k})
	return n.Dst(), k, false, true
}

func (bl *irScalarBuilder) setCallPlan(t *ast.Call, args irQualArgs, method string) *irQualPlan {
	if !args.ok {
		return nil
	}
	switch method {
	case "size", "contains", "insert", "remove", "union", "intersection", "difference", "is_subset_of":
	default:
		return nil
	}
	fn := setFuncs[method]
	if len(args.kinds) != fn.args {
		return nil
	}
	k := args.kinds[0]
	// kindInvalid: sentinel — recordedTypeArg's nothing-recorded answer; the set keeps its operand's kind.
	if e, _ := bl.g.recordedTypeArg(t, method, "T"); e != kindInvalid && irSetElemKind(e) {
		// The set the checker solved T at, which a later operand may
		// have widened past the first (listCallPlan).
		k = bl.g.setKindOf(e)
	}
	elem, ok := setElem(k)
	if !ok || !irSetElemKind(elem) {
		return nil
	}
	for i, have := range args.kinds {
		want := elem
		if i < fn.sets {
			want = k
		}
		v, got, ok := bl.convertTo(t.Args[i], args.temps[i], have, want)
		if !ok {
			return nil
		}
		args.temps[i], args.kinds[i] = v, got
	}
	result := k
	switch fn.result {
	case "int":
		result = kindInt
	case "bool":
		result = kindBool
	}
	ok = bl.g.mapKeyOK(elem, t)
	if !ok {
		return nil
	}
	name := "Set." + method
	return &irQualPlan{token: name, name: name, result: result, host: true}
}
