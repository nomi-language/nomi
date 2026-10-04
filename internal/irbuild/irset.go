package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

func irRetainedSetKind(k kind) bool {
	if k == kindEmptySet {
		return true
	}
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
	if k.tag == tagList && irRetainedListKind(k) && k != kindEmptyList {
		return true
	}
	if elem, isVector := vectorElem(k); isVector && k != kindEmptyVector && irSetElemKind(elem) {
		return true
	}
	if elem, isSet := setElem(k); isSet && k != kindEmptySet && irSetElemKind(elem) {
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

func (bl *irScalarBuilder) setMake(t *ast.SetLit) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if len(t.Items) == 0 {
		n := ir.NewEmptySet(bl.g.irNodePos(t), bl.f.NewTemp(), nil)
		bl.b.Append(n)
		return n.Dst(), kindEmptySet, true, true
	}
	values, elem, ok := bl.lowerSameKindItems(t.Items, irSetElemKind)
	if !ok {
		return no()
	}
	k := bl.g.setKindOf(elem)
	if !irRetainedSetKind(k) {
		return no()
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
	case "size", "contains?", "insert", "remove", "union", "intersection", "difference", "subset?":
	default:
		return nil
	}
	fn := setFuncs[method]
	if len(args.kinds) != fn.args {
		return nil
	}
	k := args.kinds[0]
	if k == kindEmptySet && len(args.kinds) == 2 && irScalarLeafKind(args.kinds[1]) {
		k = bl.g.setKindOf(args.kinds[1])
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
		v, got, ok := bl.coerceEmpty(t.Args[i], args.temps[i], have, want)
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
