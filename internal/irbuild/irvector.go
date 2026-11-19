package irbuild

import (
	"slices"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

func irRetainedVectorKind(k kind) bool {
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

func (bl *irScalarBuilder) vectorMake(t *ast.ListLit) (ir.Temp, kind, bool, bool) {
	// kindInvalid: sentinel — no expected element kind.
	return bl.vectorMakeOf(t, kindInvalid)
}

// vectorMakeOf builds a vector literal; elem, when not kindInvalid, is the
// expected element kind each item is lowered against.
func (bl *irScalarBuilder) vectorMakeOf(t *ast.ListLit, elem kind) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if len(t.Spreads) == 1 && (t.Spreads[0].At == 0 || t.Spreads[0].At == len(t.Items)) {
		return bl.vectorUpdate(t)
	}
	if len(t.Spreads) > 0 {
		return bl.vectorSpreadLit(t)
	}
	if len(t.Items) == 0 {
		return bl.emptyLiteral(t, vectorSpreadJoin, kindInvalid)
	}
	// Native vector construction forces every impure element.
	want := elem
	// kindInvalid: sentinel — the items give the element kind.
	if parts := bl.literalParts(t, vectorSpreadJoin); want == kindInvalid && parts != nil {
		want = parts[0]
	}
	values, elem, ok := bl.lowerSameKindItems(t, vectorSpreadJoin, t.Items, want, func(k kind) bool {
		// A Vector holds any value the domain does, as a List does: a
		// sequence, a function, an interface's existential, a channel
		// half, Unit.
		return irCallOperandKind(k)
	})
	if !ok {
		return no()
	}
	k := bl.g.vectorKindOf(elem)
	n := ir.NewMakeVector(bl.g.irNodePos(t), bl.f.NewTemp(), values)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: k})
	return n.Dst(), k, false, true
}

func (bl *irScalarBuilder) vectorCallPlan(t *ast.Call, args irQualArgs, method string) *irQualPlan {
	if !args.ok {
		return nil
	}
	if method == "contains" {
		return bl.seqContainsPlan(t, args, "Vector")
	}
	switch method {
	case "length", "get", "push", "concat", "set", "next_item":
	default:
		return nil
	}
	fn := vectorFuncs[method]
	if len(args.kinds) != fn.vecs+len(fn.rest) {
		return nil
	}
	k := args.kinds[0]
	// kindInvalid: sentinel — recordedTypeArg's nothing-recorded answer; the vector keeps its operand's kind.
	if e, _ := bl.g.recordedTypeArg(t, method, "T"); e != kindInvalid {
		// The vector the checker solved T at, which a later operand may
		// have widened past the first (listCallPlan).
		k = bl.g.vectorKindOf(e)
	}
	elem, ok := vectorElem(k)
	if !ok {
		return nil
	}
	// These operations never read an element, so any element the domain
	// holds is carried as it is.
	if !irCallOperandKind(elem) {
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
		v, got, ok := bl.convertTo(t.Args[i], args.temps[i], have, want)
		if !ok {
			return nil
		}
		args.temps[i], args.kinds[i] = v, got
	}
	if method == "get" || method == "next_item" {
		if _, anchored := bl.g.preludeNamed("Maybe"); !anchored {
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
// (`[Some(1), None]`) takes their kind, as the checker gives it: it is
// lowered after them, and having no effect, lowering it later reorders
// nothing. Items that differ only by an embedded type join to the enum
// that embeds it (`[circle, shape]` is a `Vector<Shape>`), as the checker
// joins them: the embedded ones are widened after every item is lowered.
// listMakeOf does the same for a list. Items that join no other way take
// literal at's checked element type (of shape's collection), each coerced
// to it (literalItemsCoerced).
//
// want, when not kindInvalid, is the element kind (literalParts): every item
// is lowered against it and must arrive as it.
func (bl *irScalarBuilder) lowerSameKindItems(at ast.Node, shape spreadJoin, items []ast.Node, want kind, accept func(kind) bool) ([]ir.Temp, kind, bool) {
	values := make([]ir.Temp, len(items))
	// kindInvalid: sentinel — no element kind is given.
	if want != kindInvalid {
		if !accept(want) {
			bl.g.dc.note("a literal whose element kind is outside its collection's domain: " + want.nomi())
			return nil, kindInvalid, false
		}
		for i, item := range items {
			v, _, _, ok := bl.lowerWant(item, want)
			if !ok {
				return nil, kindInvalid, false
			}
			values[i] = v
		}
		return values, want, true
	}
	var deferred []int
	var typedItems []ast.Node
	var typedValues []ir.Temp
	var typedKinds []kind
	for i, item := range items {
		if name, _, _, bare := preludeValueName(item); bare && name == "None" && len(items) > len(deferred)+1 {
			deferred = append(deferred, i)
			continue
		}
		v, k, _, ok := bl.lower(item)
		if !ok {
			return nil, kindInvalid, false
		}
		typedItems, typedValues, typedKinds = append(typedItems, item), append(typedValues, v), append(typedKinds, k)
	}
	elem, joined := irEmbedsJoin(typedKinds)
	if !joined || !accept(elem) {
		// Items that join no other way take the checked element type, as
		// a list's items do.
		if checked, ok := bl.literalItemsCoerced(at, shape, 0, typedItems, typedValues, typedKinds); ok {
			elem, joined = checked, true
		}
	}
	if !joined || !accept(elem) || !bl.widenEmbedded(typedItems, typedValues, typedKinds, elem) {
		return nil, kindInvalid, false
	}
	j := 0
	for i := range items {
		if slices.Contains(deferred, i) {
			continue
		}
		values[i] = typedValues[j]
		j++
	}
	for _, i := range deferred {
		v, _, _, ok := bl.lowerWant(items[i], elem)
		if !ok {
			return nil, kindInvalid, false
		}
		values[i] = v
	}
	return values, elem, true
}
