package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// THE REST OF std/iter's ADAPTERS AND MATERIALIZERS, each one ir.Iter the VM
// drives through rt's Seq function of the same name: `drop`, `take_while`,
// `drop_while`, `cycle`, `each`, `to_set`, `to_vector`, `to_map`, `flat_map`, `flatten`,
// `chunks` and `chunk_by`, and the constructors `iterate` and `repeat`.
// rt's functions state std's bodies (seqsrc.go, seqterm.go).
//
// A callback lowered under a signalling form (`break` / `continue` in the
// lambda) declines: these operations have no signalling reader.

// iterAppend appends one Iter node of result kind k.
func (bl *irScalarBuilder) iterAppend(at ast.Node, op ir.IterOp, over ir.IterDomain, k kind, operands ...ir.Temp) ir.Temp {
	n := ir.NewIter(bl.g.irNodePos(at), bl.f.NewTemp(), op, over, operands...)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: k})
	return n.Dst()
}

// iterPlainCallback reports whether the callback operand is a function of
// one parameter of kind param, lowered without a control signal.
func (bl *irScalarBuilder) iterPlainCallback(args irQualArgs, i int, param kind) (kind, bool) {
	fk := args.kinds[i]
	if fk.tag != tagFunc || len(funcParams(fk)) != 1 || funcParams(fk)[0] != param {
		return kindInvalid, false
	}
	if bl.iterSignalling(args, i) {
		return kindInvalid, false
	}
	return funcResult(fk), true
}

// iterSignalling reports whether callback operand i was lowered under the
// adapter's signalling form (`break` / `continue` in the lambda).
func (bl *irScalarBuilder) iterSignalling(args irQualArgs, i int) bool {
	// Through the Copy irQualLowerArgs holds a final operand in inside an
	// assertion subject, to the lambda itself.
	plan := bl.callablePlan(args.temps[i])
	return (plan != nil && plan.ctl != irCtlNone) || bl.namedCtl(args.temps[i]) != irCtlNone
}

// namedCtl is the signalling form of the iter-sensitive named function temp
// holds a reference to, through the same plain aliases callablePlan follows,
// or irCtlNone.
func (bl *irScalarBuilder) namedCtl(temp ir.Temp) irCtlForm {
	for {
		switch n := bl.f.Def(temp).(type) {
		case *ir.Bind:
			temp = n.Src()
		case *ir.Copy:
			if int(n.Dst()) >= len(bl.sides) || bl.sides[n.Dst()].copy == irCopyNone {
				return irCtlNone
			}
			temp = n.Src()
		case *ir.Ref:
			if int(n.Dst()) >= len(bl.sides) {
				return irCtlNone
			}
			return bl.sides[n.Dst()].ctl
		default:
			return irCtlNone
		}
	}
}

// iterSignalledCallback is iterPlainCallback for take_while and each, whose
// callbacks may signal: rt's SeqTakeWhileCtl and SeqEachCtl drive them.
func (bl *irScalarBuilder) iterSignalledCallback(args irQualArgs, i int, param kind) (kind, bool, bool) {
	fk := args.kinds[i]
	if fk.tag != tagFunc || len(funcParams(fk)) != 1 || funcParams(fk)[0] != param {
		return kindInvalid, false, false
	}
	return funcResult(fk), bl.iterSignalling(args, i), true
}

// iterStage lowers the operations above over an already-viewed source src of
// element kind elem. handled is false for every other method.
func (bl *irScalarBuilder) iterStage(t *ast.Call, args irQualArgs, method string, src ir.Temp, elem kind) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, true }
	seq := seqKindIn(bl.g, elem)
	switch method {
	case "drop":
		if args.kinds[1] != kindInt {
			return no()
		}
		return bl.iterAppend(t, ir.IterDrop, ir.IterOverSeq, seq, src, args.temps[1]), seq, true, true
	case "take_while":
		r, signalling, ok := bl.iterSignalledCallback(args, 1, elem)
		if !ok || r != kindBool {
			return no()
		}
		if signalling {
			n := ir.NewIterSignalling(bl.g.irNodePos(t), bl.f.NewTemp(), ir.IterTakeWhile, ir.IterOverSeq, src, args.temps[1])
			bl.b.Append(n)
			bl.side(n.Dst(), irScalarSide{k: seq})
			return n.Dst(), seq, true, true
		}
		return bl.iterAppend(t, ir.IterTakeWhile, ir.IterOverSeq, seq, src, args.temps[1]), seq, true, true
	case "drop_while":
		if r, ok := bl.iterPlainCallback(args, 1, elem); !ok || r != kindBool {
			return no()
		}
		return bl.iterAppend(t, ir.IterDropWhile, ir.IterOverSeq, seq, src, args.temps[1]), seq, true, true
	case "cycle":
		return bl.iterAppend(t, ir.IterCycle, ir.IterOverSeq, seq, src), seq, true, true
	case "each":
		_, signalling, ok := bl.iterSignalledCallback(args, 1, elem)
		if !ok {
			return no()
		}
		if signalling {
			n := ir.NewIterSignalling(bl.g.irNodePos(t), bl.f.NewTemp(), ir.IterEach, ir.IterOverSeq, src, args.temps[1])
			bl.b.Append(n)
			bl.side(n.Dst(), irScalarSide{k: kindUnit})
			return n.Dst(), kindUnit, true, true
		}
		return bl.iterAppend(t, ir.IterEach, ir.IterOverSeq, kindUnit, src, args.temps[1]), kindUnit, true, true
	case "to_set":
		k := bl.g.setKindOf(elem)
		if !irRetainedSetKind(k) {
			return no()
		}
		return bl.iterAppend(t, ir.IterToSet, ir.IterOverSeq, k, src), k, true, true
	case "to_vector":
		k := bl.g.vectorKindOf(elem)
		if !irRetainedVectorKind(k) && !bl.g.irVectorValueKind(k) {
			return no()
		}
		return bl.iterAppend(t, ir.IterToVector, ir.IterOverSeq, k, src), k, true, true
	case "to_map":
		if elem.tag != tagTuple || elem.comp == nil || len(elem.comp.parts) != 2 {
			return no()
		}
		k := bl.g.mapKind(elem.comp.parts[0], elem.comp.parts[1])
		if !irRetainedMapKind(k) {
			return no()
		}
		return bl.iterAppend(t, ir.IterToMap, ir.IterOverSeq, k, src), k, true, true
	case "flat_map":
		r, ok := bl.iterPlainCallback(args, 1, elem)
		if !ok || r.comp == nil || len(r.comp.parts) != 1 {
			return no()
		}
		inner := r.comp.parts[0]
		k := seqKindIn(bl.g, inner)
		switch {
		case r.tag == tagList && (irRetainedListKind(r) || irListTransportKind(r)):
			return bl.iterAppend(t, ir.IterFlatMapList, ir.IterOverSeq, k, src, args.temps[1]), k, true, true
		case r.tag == tagSeq && irRetainedValueKind(inner):
			return bl.iterAppend(t, ir.IterFlatMap, ir.IterOverSeq, k, src, args.temps[1]), k, true, true
		}
		return no()
	case "flatten":
		if elem.comp == nil || len(elem.comp.parts) != 1 {
			return no()
		}
		inner := elem.comp.parts[0]
		k := listKindIn(bl.g, inner)
		if !irRetainedListKind(k) && !irListTransportKind(k) {
			return no()
		}
		switch {
		case elem.tag == tagList:
			return bl.iterAppend(t, ir.IterFlattenList, ir.IterOverSeq, k, src), k, true, true
		case elem.tag == tagSeq:
			return bl.iterAppend(t, ir.IterFlatten, ir.IterOverSeq, k, src), k, true, true
		}
		return no()
	case "chunks", "chunk_by":
		chunk := listKindIn(bl.g, elem)
		if !irRetainedListKind(chunk) && !irListTransportKind(chunk) {
			return no()
		}
		k := seqKindIn(bl.g, chunk)
		if method == "chunks" {
			if args.kinds[1] != kindInt {
				return no()
			}
			return bl.iterAppend(t, ir.IterChunks, ir.IterOverSeq, k, src, args.temps[1]), k, true, true
		}
		if key, ok := bl.iterPlainCallback(args, 1, elem); !ok || !irRetainedValueKind(key) {
			return no()
		}
		return bl.iterAppend(t, ir.IterChunkBy, ir.IterOverSeq, k, src, args.temps[1]), k, true, true
	}
	return ir.NoTemp, kindInvalid, false, false
}

// iterConstructor lowers `Iter.iterate(seed, step)` and `Iter.repeat(x)`:
// sources with no view.
func (bl *irScalarBuilder) iterConstructor(t *ast.Call, args irQualArgs, method string) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if method == "repeat" {
		if len(args.temps) != 1 || !irRetainedValueKind(args.kinds[0]) {
			return no()
		}
		k := seqKindIn(bl.g, args.kinds[0])
		return bl.iterAppend(t, ir.IterRepeat, ir.IterOverNothing, k, args.temps[0]), k, false, true
	}
	if len(args.temps) != 2 || !irRetainedValueKind(args.kinds[0]) {
		return no()
	}
	elem := args.kinds[0]
	r, signalling, ok := bl.iterSignalledCallback(args, 1, elem)
	if !ok || r != elem {
		return no()
	}
	k := seqKindIn(bl.g, elem)
	if signalling {
		// `break v` emits v and stops; a bare `break` stops (rt.IterateEachCtl).
		n := ir.NewIterSignalling(bl.g.irNodePos(t), bl.f.NewTemp(), ir.IterIterate, ir.IterOverNothing, args.temps[0], args.temps[1])
		bl.b.Append(n)
		bl.side(n.Dst(), irScalarSide{k: k})
		return n.Dst(), k, false, true
	}
	return bl.iterAppend(t, ir.IterIterate, ir.IterOverNothing, k, args.temps[0], args.temps[1]), k, false, true
}
