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
// Vector, an Option or Result, a Set). The VM hashes and compares an element
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

// setMakeOf builds the set literal t at its kind (literalKind: its record,
// or want, the kind of the position, where the builder made it), each item
// lowered against the set's element (lowerItemsAt).
func (bl *irScalarBuilder) setMakeOf(t *ast.ListLit, want kind) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if len(t.Spreads) == 1 && t.Spreads[0].At == 0 {
		return bl.setUpdate(t)
	}
	if len(t.Spreads) > 0 {
		return bl.setSpreadLit(t)
	}
	if len(t.Items) == 0 {
		return bl.emptyLiteral(t, setSpreadJoin, want)
	}
	k, ok := bl.builtLiteralKind(t, setSpreadJoin, want)
	if !ok {
		return no()
	}
	// std's Set instantiates at each element the VM hashes and compares.
	elem, _ := setElem(k)
	if !irRetainedSetKind(k) {
		bl.g.dc.note("a set literal outside the retained sets: " + k.nomi())
		return no()
	}
	values, ok := bl.lowerItemsAt(t.Items, elem)
	if !ok || !bl.g.mapKeyOK(elem, t) {
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
	fn, intrinsic := setFuncs[method]
	if !intrinsic || len(args.kinds) != fn.args {
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
