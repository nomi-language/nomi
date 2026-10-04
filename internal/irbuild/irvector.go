package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

func irRetainedVectorKind(k kind) bool {
	if k == kindEmptyVector {
		return true
	}
	elem, ok := vectorElem(k)
	return ok && irScalarLeafKind(elem)
}

// irVectorValueKind is a Vector whose element is any structural value: the
// VM's vector holds values of every kind, and the element-agnostic
// operations (length, at, push, concat, …) never read one.
func (g *gen) irVectorValueKind(k kind) bool {
	elem, ok := vectorElem(k)
	return ok && (irScalarLeafKind(elem) || g.irStructuralValueKind(elem))
}

func (bl *irScalarBuilder) vectorMake(t *ast.VectorLit) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if len(t.Items) == 0 {
		n := ir.NewEmptyVector(bl.g.irNodePos(t), bl.f.NewTemp(), nil)
		bl.b.Append(n)
		return n.Dst(), kindEmptyVector, true, true
	}
	// Native vector construction forces every impure element.
	values, elem, ok := bl.lowerSameKindItems(t.Items, func(k kind) bool {
		return irScalarLeafKind(k) || bl.g.irStructuralValueKind(k)
	})
	if !ok {
		return no()
	}
	k := bl.g.vectorKindOf(elem)
	if !irRetainedVectorKind(k) && bl.g.irValType(k) == nil {
		return no()
	}
	n := ir.NewMakeVector(bl.g.irNodePos(t), bl.f.NewTemp(), values)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: k})
	return n.Dst(), k, false, true
}

func (bl *irScalarBuilder) vectorCallPlan(t *ast.Call, args irQualArgs, method string) *irQualPlan {
	if !args.ok {
		return nil
	}
	switch method {
	case "length", "at", "push", "concat", "set", "next_item":
	default:
		return nil
	}
	fn := vectorFuncs[method]
	if len(args.kinds) != fn.vecs+len(fn.rest) {
		return nil
	}
	k := args.kinds[0]
	if k == kindEmptyVector && method == "concat" {
		k = args.kinds[1]
	}
	if k == kindEmptyVector {
		// `Vector.next_item(#[])`: an element operand types the vector;
		// with none, nothing constrains the element and no value of it
		// is built or observed, so it is typed as listCallPlan types
		// `List.head([])`.
		k = bl.g.vectorKindOf(kindInt)
		if method == "push" || method == "set" {
			if e := args.kinds[len(args.kinds)-1]; irScalarLeafKind(e) {
				k = bl.g.vectorKindOf(e)
			}
		}
	}
	elem, ok := vectorElem(k)
	if !ok {
		return nil
	}
	if !irScalarLeafKind(elem) && !bl.g.irStructuralValueKind(elem) {
		return nil
	}
	for i, have := range args.kinds {
		want := k
		if i >= fn.vecs {
			want = elem
			if fn.rest[i-fn.vecs] == "int" {
				want = kindInt
			}
		}
		v, got, ok := bl.coerceEmpty(t.Args[i], args.temps[i], have, want)
		if !ok {
			return nil
		}
		args.temps[i], args.kinds[i] = v, got
	}
	if method == "at" || method == "set" || method == "next_item" {
		if _, anchored := bl.g.preludeByName["Maybe"]; !anchored {
			return nil
		}
	}
	result, ok := bl.g.vectorResultKind(fn.result, k, elem, t)
	if !ok || !(irRetainedValueKind(result) || bl.g.irValType(result) != nil) {
		return nil
	}
	name := "Vector." + method
	return &irQualPlan{token: name, name: name, result: result, host: true}
}

// lowerSameKindItems lowers a vector or set literal's items, left to right,
// to one element kind that accept admits. A bare `None` beside typed items
// (`#[Some(1), None]`) takes their kind, as the checker gives it: it is
// lowered after them, and having no effect, lowering it later reorders
// nothing. listMakeOf does the same for a list.
func (bl *irScalarBuilder) lowerSameKindItems(items []ast.Node, accept func(kind) bool) ([]ir.Temp, kind, bool) {
	values := make([]ir.Temp, len(items))
	// kindInvalid: sentinel — no item has been lowered yet.
	elem := kindInvalid
	var deferred []int
	for i, item := range items {
		if name, _, _, bare := preludeValueName(item); bare && name == "None" && len(items) > len(deferred)+1 {
			deferred = append(deferred, i)
			continue
		}
		v, k, _, ok := bl.lower(item)
		// kindInvalid: sentinel — this is the first lowered item.
		if !ok || !accept(k) || (elem != kindInvalid && k != elem) {
			return nil, kindInvalid, false
		}
		elem = k
		values[i] = v
	}
	for _, i := range deferred {
		v, k, _, ok := bl.lowerTypedOperand(items[i], elem)
		if !ok || k != elem {
			return nil, kindInvalid, false
		}
		values[i] = v
	}
	return values, elem, true
}
