package irbuild

import (
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

// vectorMakeOf builds the vector literal t at its kind (literalKind: its
// record, or want, the kind of the position, where the builder made it),
// each item lowered against the vector's element (lowerItemsAt).
func (bl *irScalarBuilder) vectorMakeOf(t *ast.ListLit, want kind) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if len(t.Spreads) == 1 && (t.Spreads[0].At == 0 || t.Spreads[0].At == len(t.Items)) {
		return bl.vectorUpdate(t)
	}
	if len(t.Spreads) > 0 {
		return bl.vectorSpreadLit(t)
	}
	if len(t.Items) == 0 {
		return bl.emptyLiteral(t, vectorSpreadJoin, want)
	}
	k, ok := bl.builtLiteralKind(t, vectorSpreadJoin, want)
	if !ok {
		return no()
	}
	// A Vector holds any value the domain does, as a List does: a
	// sequence, a function, an interface's existential, a channel half,
	// Unit.
	elem, _ := vectorElem(k)
	if !irCallOperandKind(elem) {
		bl.g.dc.note("a vector literal whose element is outside the domain: " + elem.nomi())
		return no()
	}
	// Native vector construction forces every impure element.
	values, ok := bl.lowerItemsAt(t.Items, elem)
	if !ok {
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
	if method == "contains" {
		return bl.seqContainsPlan(t, args, "Vector")
	}
	switch method {
	case "length", "get", "push", "concat", "set", "next_item", "slice":
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
		if _, anchored := preludeSpecAnchor(preludeSpecNamed("Maybe")); !anchored {
			return nil
		}
	}
	// The result is the call's record. A call the builder writes for no
	// source node carries the type the checker gives it (a std interface's
	// thunk for a held type, stdProgThunk, carries InterfaceResult's); one
	// for a value held by its kind alone carries none, its result is the
	// row's, and noteUnrecordedKind counts it.
	result, ok := bl.g.recordedKind(t)
	if !ok {
		bl.noteUnrecordedKind("Vector result", t)
		result, ok = bl.g.vectorResultKind(fn.result, k, elem, t)
	}
	if !ok || !(irRetainedValueKind(result) || bl.g.irValType(result) != nil) {
		return nil
	}
	name := "Vector." + method
	return &irQualPlan{token: name, name: name, result: result, host: true}
}
