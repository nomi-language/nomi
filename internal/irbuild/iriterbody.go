package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// iterCall retains list, string, bytes, range, vector, map, set and user-impl
// views, ordinary callbacks, bounded consumption, cardinality and
// `known_count`, the positional terminals (`first`, `last`, `at`, `find`),
// pairing stages (`with_index`, `zip`), `Iter.from`, `reverse`, `partition`,
// sorting and materialization, each naming its rt driver. Sequence values flow
// between these operations and every value position (irRetainedSeqKind); a
// source entering a declared `Iter<T>` is viewed as the sequence (seqView).
// irIterOwnArity is the operand count of an `Iter` operation iterCall lowers
// itself, and false for any other: those are instantiated from their std
// bodies as any generic std function is (`Iter.frequencies`).
func irIterOwnArity(method string) (int, bool) {
	switch method {
	case "map", "filter", "reduce", "scan", "map_reduce", "take", "sort_with", "sort_by", "any_match", "all_match", "each_while", "find", "zip", "concat", "get", "partition", "group_by",
		"drop", "take_while", "drop_while", "each", "flat_map", "chunks", "windows", "chunk_by", "join":
		return 2, true
	case "to_list", "count", "is_empty", "sort", "first", "last", "reverse", "with_index", "known_count", "is_infinite",
		"cycle", "to_set", "to_vector", "to_map":
		return 1, true
	}
	return 0, false
}

// iterCall lowers t, a call of the `Iter` operation method, over its
// source's view. srcAt is the source expression, whose view the checker
// recorded (viewSourceAt), or nil where the callee's own type names the
// source (`Range.each_while(r, f)`) or the source is no expression the
// checker typed against an Iter (a spread's part): its kind names it.
func (bl *irScalarBuilder) iterCall(t *ast.Call, args irQualArgs, method string, srcAt ast.Node) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if !args.ok || !bl.g.iterOwns("Iter") {
		return no()
	}
	if method == "from" {
		// A constructor: no source and no view.
		if len(args.temps) != 1 || args.kinds[0] != kindInt {
			return no()
		}
		k := seqKindIn(bl.g, kindInt)
		n := ir.NewIter(bl.g.irNodePos(t), bl.f.NewTemp(), ir.IterFrom, ir.IterOverNothing, args.temps[0])
		bl.b.Append(n)
		bl.side(n.Dst(), irScalarSide{k: k})
		return n.Dst(), k, false, true
	}
	if method == "iterate" || method == "repeat" {
		return bl.iterConstructor(t, args, method)
	}
	want, own := irIterOwnArity(method)
	if !own {
		return no()
	}
	direction := ir.NoTemp
	if (method == "sort" || method == "sort_by") && len(args.temps) == want+1 {
		// `Iter.sort(xs, Direction.Descending)`, `Iter.sort_by(xs, dir,
		// key)`: the Direction travels as the sort's last operand.
		dk := args.kinds[1]
		if dk.tag != tagNamed || dk.def != stdEnumDefs()[stdEnumDirection] {
			return no()
		}
		direction = args.temps[1]
		args = irQualArgs{
			temps:  append([]ir.Temp{args.temps[0]}, args.temps[2:]...),
			kinds:  append([]kind{args.kinds[0]}, args.kinds[2:]...),
			mobile: append([]bool{args.mobile[0]}, args.mobile[2:]...),
			ok:     true,
		}
	}
	if len(args.temps) != want {
		return no()
	}
	src, sk := args.temps[0], args.kinds[0]
	if method == "to_list" && (sk.tag == tagList || sk.tag == tagEmptyList) {
		return src, sk, args.mobile[0], true
	}
	if _, ok := vectorElem(sk); method == "to_vector" && ok {
		// A Vector is immutable, so materializing one answers it as-is.
		return src, sk, args.mobile[0], true
	}
	if method == "is_empty" && sk.tag == tagEmptyList {
		// `Iter.is_empty([])`: the literal has no elements, so the answer is
		// the constant std's `first(source) == None` computes.
		c := ir.NewBool(bl.g.irNodePos(t), bl.f.NewTemp(), method == "is_empty")
		bl.b.Append(c)
		bl.side(c.Dst(), irScalarSide{k: kindBool})
		return c.Dst(), kindBool, true, true
	}
	if method == "count" && sk.tag == tagEmptyList {
		n := ir.NewInt(bl.g.irNodePos(t), bl.f.NewTemp(), 0)
		bl.b.Append(n)
		bl.side(n.Dst(), irScalarSide{k: kindInt})
		return n.Dst(), kindInt, true, true
	}
	if sk == kindEmptyList && len(t.Args) >= 1 {
		// `Iter.first([])`, `[] |> Iter.chunks(2)`: nothing constrains the
		// element, so no value of it is built or observed, as listCallPlan
		// argues for `List.head([])`. The literal is typed at a callback's
		// parameter when there is one, and otherwise as a List of Int, the
		// representation of an element the program never holds.
		elem := kindInt
		if method == "join" {
			elem = kindString
		} else if want == 2 && len(args.kinds) > 1 && args.kinds[1].tag == tagFunc && len(funcParams(args.kinds[1])) >= 1 {
			elem = funcParams(args.kinds[1])[0]
			if irSeededIterMethod(method) && len(funcParams(args.kinds[1])) == 2 {
				// A seeded callback takes the accumulator first.
				elem = funcParams(args.kinds[1])[1]
			}
		}
		v, k, ok := bl.coerceEmpty(t.Args[0], src, sk, bl.g.listKind(elem))
		if !ok {
			return no()
		}
		src, sk = v, k
		args.temps[0], args.kinds[0] = v, k
	}
	vs, ok := bl.viewSourceFor(srcAt, sk)
	if !ok {
		return no()
	}
	elem := vs.elem
	switch vs.dom {
	case analysis.IterViewRange:
		if method == "known_count" && !bl.inheritsStepsBetween(elem) {
			// A Range's count protocol is answered with rt.RangeKnownCount
			// rather than through the view; `count` and `is_empty`
			// walk the view.
			return bl.rangeKnownCount(t, src, elem)
		}
	case analysis.IterViewMap:
		// A Map pushes `(K, V)` pairs. sk is a lowered value, in the domain
		// (inValueDomain), and a Map there is a retained one: its key has
		// equality (the checker's key check) and irMapValueKind holds any
		// value.
		if !irRetainedMapKind(sk) || !irRetainedTupleKind(elem) {
			panic("iterCall: a Map source in the value domain that is not a retained map: " + sk.nomi())
		}
	case analysis.IterViewSet:
		if !irRetainedSetKind(sk) {
			panic("iterCall: a Set source in the value domain that is not a retained set: " + sk.nomi())
		}
	}
	result := listKindIn(bl.g, elem)
	var comparator *stdFunc
	if method == "take" {
		if args.kinds[1] != kindInt {
			return no()
		}
		result = seqKindIn(bl.g, elem)
	}
	var localCompare *implItem
	var siblingCompare *ir.Symbol
	if method == "sort" {
		var ok bool
		if localCompare, siblingCompare, comparator, ok = bl.sortComparator(elem); !ok {
			return no()
		}
	}
	var sortKey kind
	var sortKeyCmp *stdFunc
	var sortKeyLocal *implItem
	var sortKeySibling *ir.Symbol
	if method == "sort_by" {
		fk := args.kinds[1]
		if fk.tag != tagFunc || len(funcParams(fk)) != 1 || funcParams(fk)[0] != elem {
			return no()
		}
		// The key's comparator is chosen as `sort` chooses the element's.
		sortKey = funcResult(fk)
		var ok bool
		if sortKeyLocal, sortKeySibling, sortKeyCmp, ok = bl.sortComparator(sortKey); !ok {
			return no()
		}
	}
	if method == "sort_with" {
		cb := args.kinds[1]
		if cb.tag != tagFunc || len(funcParams(cb)) != 2 || funcParams(cb)[0] != elem || funcParams(cb)[1] != elem || funcResult(cb) != stdEnumKind(stdEnumOrdering) {
			return no()
		}
	}
	if method == "map" || method == "filter" {
		fk := args.kinds[1]
		if fk.tag != tagFunc || len(funcParams(fk)) != 1 || funcParams(fk)[0] != elem || !(irCallableValueKind(funcResult(fk)) || (method == "map" && funcResult(fk) == kindUnit)) {
			return no()
		}
		result = seqKindIn(bl.g, funcResult(fk))
		if method == "filter" {
			if funcResult(fk) != kindBool {
				return no()
			}
			result = seqKindIn(bl.g, elem)
		}
	}
	if method == "any_match" || method == "all_match" || method == "each_while" || method == "find" || method == "partition" {
		// Native iterPredicate: a one-parameter Bool function over the element.
		fk := args.kinds[1]
		if fk.tag != tagFunc || len(funcParams(fk)) != 1 || funcParams(fk)[0] != elem || funcResult(fk) != kindBool {
			return no()
		}
		result = kindBool
		if method == "find" {
			mk, ok := bl.iterMaybeKind(elem)
			if !ok {
				return no()
			}
			result = mk
		}
	}
	if method == "reduce" {
		fk := args.kinds[1]
		if fk.tag != tagFunc || len(funcParams(fk)) != 2 || funcParams(fk)[1] != elem || funcParams(fk)[0] != funcResult(fk) || !irCallOperandKind(funcResult(fk)) {
			return no()
		}
		result = funcResult(fk)
		if bl.ctlSeed == ir.NoTemp {
			// iterReduceCall lowers the starting value.
			return no()
		}
	}
	accK := result
	if method == "scan" {
		// A reduce's callback whose every state is emitted.
		fk := args.kinds[1]
		if fk.tag != tagFunc || len(funcParams(fk)) != 2 || funcParams(fk)[1] != elem || funcParams(fk)[0] != funcResult(fk) || !irCallOperandKind(funcResult(fk)) {
			return no()
		}
		accK = funcResult(fk)
		result = seqKindIn(bl.g, accK)
		if bl.ctlSeed == ir.NoTemp {
			// iterReduceCall lowers the starting value.
			return no()
		}
	}
	if method == "map_reduce" {
		// `(A, T) -> (A, U)`, answering `(A, List<U>)`.
		fk := args.kinds[1]
		if fk.tag != tagFunc || len(funcParams(fk)) != 2 || funcParams(fk)[1] != elem {
			return no()
		}
		accK = funcParams(fk)[0]
		r := funcResult(fk)
		if r.tag != tagTuple || r.comp == nil || len(r.comp.parts) != 2 || r.comp.parts[0] != accK ||
			!irCallOperandKind(accK) || !irCallOperandKind(r.comp.parts[1]) {
			return no()
		}
		result = bl.g.tupleKind([]kind{accK, listKindIn(bl.g, r.comp.parts[1])})
		if !irRetainedTupleKind(result) || bl.ctlSeed == ir.NoTemp {
			return no()
		}
	}
	// A callback lowered under a signalling form makes its operation the
	// signalling one.
	signalling := (method == "map" || method == "filter" || irSeededIterMethod(method) || method == "find" || method == "any_match" || method == "all_match") &&
		len(args.temps) > 1 && bl.iterSignalling(args, 1)
	appendOp := func(at ast.Node, op ir.IterOp, over ir.IterDomain, k kind, operands ...ir.Temp) ir.Temp {
		n := ir.NewIter(bl.g.irNodePos(at), bl.f.NewTemp(), op, over, operands...)
		if signalling && op.HasSignallingForm() {
			n = ir.NewIterSignalling(bl.g.irNodePos(at), n.Dst(), op, over, operands...)
		}
		bl.b.Append(n)
		bl.side(n.Dst(), irScalarSide{k: k})
		return n.Dst()
	}
	if method == "count" && vs.dom == analysis.IterViewList {
		return appendOp(t, ir.IterCount, ir.IterOverList, kindInt, src), kindInt, false, true
	}
	if method == "count" && vs.dom == analysis.IterViewMap {
		return appendOp(t, ir.IterCount, ir.IterOverMap, kindInt, src), kindInt, false, true
	}
	if method == "known_count" {
		// iterKnownCount's arms: the families whose count std overrides, and
		// the families that inherit the declining default without a view.
		mk, ok := bl.iterMaybeKind(kindInt)
		if !ok {
			return no()
		}
		over := ir.IterOverNothing
		switch vs.dom {
		case analysis.IterViewList:
			over = ir.IterOverList
		case analysis.IterViewString:
			over = ir.IterOverString
		case viewSeq:
			over = ir.IterOverSeq
		case analysis.IterViewVector:
			over = ir.IterOverVector
		case analysis.IterViewMap:
			over = ir.IterOverMap
		case analysis.IterViewSet:
			over = ir.IterOverSet
		case analysis.IterViewMaybe:
			over = ir.IterOverMaybe
		}
		if over != ir.IterOverNothing {
			return appendOp(t, ir.IterKnownCount, over, mk, src), mk, false, true
		}
	}
	switch vs.dom {
	case analysis.IterViewRange:
		if elem == kindInt {
			src = appendOp(t.Args[0], ir.IterView, ir.IterOverIntRange, seqKindIn(bl.g, elem), src)
			break
		}
		if elem == codepointKind() {
			src = appendOp(t.Args[0], ir.IterView, ir.IterOverCodepointRange, seqKindIn(bl.g, elem), src)
			break
		}
		view, ok := bl.rangeView(t, src, elem)
		if !ok {
			return no()
		}
		src = appendOp(t.Args[0], ir.IterView, ir.IterOverRange, seqKindIn(bl.g, elem), view...)
	case analysis.IterViewMap:
		src = bl.mapView(t.Args[0], src, sk, elem)
	case analysis.IterViewSet:
		src = appendOp(t.Args[0], ir.IterView, ir.IterOverSet, seqKindIn(bl.g, elem), src)
	case analysis.IterViewMaybe:
		src = appendOp(t.Args[0], ir.IterView, ir.IterOverMaybe, seqKindIn(bl.g, elem), src)
	case analysis.IterViewVector:
		src = appendOp(t.Args[0], ir.IterView, ir.IterOverVector, seqKindIn(bl.g, elem), src)
	case analysis.IterViewImpl:
		src = bl.userView(t.Args[0], src, sk, vs.user, elem)
	case analysis.IterViewList:
		src = appendOp(t.Args[0], ir.IterView, ir.IterOverList, seqKindIn(bl.g, elem), src)
	case analysis.IterViewBytes:
		src = appendOp(t.Args[0], ir.IterView, ir.IterOverBytes, seqKindIn(bl.g, elem), src)
	case analysis.IterViewString:
		src = appendOp(t.Args[0], ir.IterView, ir.IterOverString, seqKindIn(bl.g, elem), src)
	}
	if v, k, ok, handled := bl.iterStage(t, args, method, src, elem); handled {
		return v, k, false, ok
	}
	switch method {
	case "first", "last":
		mk, ok := bl.iterMaybeKind(elem)
		if !ok {
			return no()
		}
		op := ir.IterFirst
		if method == "last" {
			op = ir.IterLast
		}
		return appendOp(t, op, ir.IterOverSeq, mk, src), mk, false, true
	case "known_count":
		// A Bytes or user source inherits the declining default, reached
		// through the view.
		mk, _ := bl.iterMaybeKind(kindInt)
		return appendOp(t, ir.IterKnownCount, ir.IterOverSeq, mk, src), mk, false, true
	case "is_infinite":
		// Every family is asked through its view: a collection's view never
		// ends, a Range's and a user source's answer their own.
		return appendOp(t, ir.IterInfinite, ir.IterOverSeq, kindBool, src), kindBool, false, true
	case "get":
		mk, ok := bl.iterMaybeKind(elem)
		if !ok || args.kinds[1] != kindInt {
			return no()
		}
		return appendOp(t, ir.IterGet, ir.IterOverSeq, mk, src, args.temps[1]), mk, false, true
	case "partition":
		list := listKindIn(bl.g, elem)
		pair := bl.g.tupleKind([]kind{list, list})
		if !irRetainedTupleKind(pair) {
			return no()
		}
		return bl.iterPaired(t, ir.IterPartition, pair, src, args.temps[1]), pair, false, true
	case "reverse":
		return appendOp(t, ir.IterReverse, ir.IterOverSeq, result, src), result, false, true
	case "group_by":
		return bl.iterGroupBy(t, src, elem, args.temps[1], args.kinds[1])
	case "with_index":
		pair := bl.g.tupleKind([]kind{kindInt, elem})
		irMustHold(irRetainedTupleKind(pair), "with_index", pair)
		k := seqKindIn(bl.g, pair)
		return bl.iterPaired(t, ir.IterWithIndex, k, src), k, false, true
	case "zip":
		right, relem, ok := bl.iterPlainView(t.Args[1], args.temps[1], args.kinds[1])
		if !ok {
			return no()
		}
		pair := bl.g.tupleKind([]kind{elem, relem})
		irMustHold(irRetainedTupleKind(pair), "zip", pair)
		k := seqKindIn(bl.g, pair)
		return bl.iterPaired(t, ir.IterZip, k, src, right), k, false, true
	case "concat":
		// Both operands viewed as sources of one element kind, joined by
		// rt.SeqConcat.
		right, relem, ok := bl.iterPlainView(t.Args[1], args.temps[1], args.kinds[1])
		if !ok || relem != elem {
			return no()
		}
		k := seqKindIn(bl.g, elem)
		return appendOp(t, ir.IterConcat, ir.IterOverSeq, k, src, right), k, false, true
	case "take":
		return appendOp(t, ir.IterTake, ir.IterOverSeq, result, src, args.temps[1]), result, false, true
	case "sort":
		cmp := bl.comparatorOperand(t, elem, localCompare, siblingCompare, comparator)
		return appendOp(t, ir.IterSort, ir.IterOverSeq, result, sortOperands(direction, src, cmp)...), result, false, true
	case "sort_with":
		return appendOp(t, ir.IterSortWith, ir.IterOverSeq, result, src, args.temps[1]), result, false, true
	case "sort_by":
		keyFn := ir.NewCopy(bl.g.irNodePos(t.Args[1]), bl.f.NewTemp(), args.temps[1])
		bl.b.Append(keyFn)
		bl.side(keyFn.Dst(), irScalarSide{k: args.kinds[1], copy: irCopyProjection})
		cmp := bl.comparatorOperand(t, sortKey, sortKeyLocal, sortKeySibling, sortKeyCmp)
		n := ir.NewIter(bl.g.irNodePos(t), bl.f.NewTemp(), ir.IterSortBy, ir.IterOverSeq, sortOperands(direction, src, keyFn.Dst(), cmp)...)
		bl.b.Append(n)
		bl.side(n.Dst(), irScalarSide{k: result})
		return n.Dst(), result, false, true
	case "count":
		return appendOp(t, ir.IterCount, ir.IterOverSeq, kindInt, src), kindInt, false, true
	case "join":
		// `String.join(parts, separator)`, which qualCall routes here.
		if elem != kindString || args.kinds[1] != kindString {
			return no()
		}
		return appendOp(t, ir.IterJoin, ir.IterOverSeq, kindString, src, args.temps[1]), kindString, false, true
	case "is_empty":
		return appendOp(t, ir.IterEmpty, ir.IterOverSeq, kindBool, src), kindBool, false, true
	}
	if method == "any_match" {
		return appendOp(t, ir.IterAny, ir.IterOverSeq, kindBool, src, args.temps[1]), kindBool, false, true
	}
	if method == "all_match" {
		return appendOp(t, ir.IterAll, ir.IterOverSeq, kindBool, src, args.temps[1]), kindBool, false, true
	}
	if method == "each_while" {
		return appendOp(t, ir.IterEachWhile, ir.IterOverSeq, kindBool, src, args.temps[1]), kindBool, false, true
	}
	if method == "find" {
		return appendOp(t, ir.IterFind, ir.IterOverSeq, result, src, args.temps[1]), result, false, true
	}
	if method == "map" {
		return appendOp(t, ir.IterMap, ir.IterOverSeq, result, src, args.temps[1]), result, false, true
	}
	if method == "filter" {
		return appendOp(t, ir.IterFilter, ir.IterOverSeq, result, src, args.temps[1]), result, false, true
	}
	if irSeededIterMethod(method) {
		// The source, the seed iterReduceCall lowered at the accumulator's
		// kind, then the callback.
		if bl.ctlSeed == ir.NoTemp {
			return no()
		}
		operands := []ir.Temp{src, bl.ctlSeed, args.temps[1]}
		op := ir.IterReduce
		switch method {
		case "scan":
			op = ir.IterScan
		case "map_reduce":
			op = ir.IterMapReduce
		}
		return appendOp(t, op, ir.IterOverSeq, result, operands...), result, false, true
	}
	return appendOp(t, ir.IterToList, ir.IterOverSeq, result, src), result, false, true
}

// irSeededArgs splits a seeded iteration call's arguments as the checker
// routes them: the source (for `reduce`, `scan` and `map_reduce`,
// withSource; `Iter.loop` has none), then the starting value (`from`,
// positional or named) and the callback (the last positional argument, or
// `f:`). A stateless `Iter.loop(fn -> ...)` has no starting value, and from
// is nil. It answers false for any other shape, which the checker rejects.
func irSeededArgs(t *ast.Call, withSource bool) (src, cb, from ast.Node, ok bool) {
	var positional []ast.Node
	for _, a := range t.Args {
		na, named := a.(*ast.NamedArg)
		if !named {
			positional = append(positional, a)
			continue
		}
		switch {
		case na.Name == "from" && from == nil:
			from = na.Value
		case na.Name == "f" && cb == nil:
			cb = na.Value
		default:
			return nil, nil, nil, false
		}
	}
	if withSource {
		if len(positional) == 0 {
			return nil, nil, nil, false
		}
		src, positional = positional[0], positional[1:]
	}
	if cb == nil && len(positional) > 0 {
		cb, positional = positional[len(positional)-1], positional[:len(positional)-1]
	}
	if from == nil && len(positional) > 0 {
		from, positional = positional[0], positional[1:]
	}
	if cb == nil || len(positional) != 0 {
		return nil, nil, nil, false
	}
	return src, cb, from, true
}

// iterReduceCall lowers a seeded `Iter.` call, method `reduce`, `scan` or
// `map_reduce`. Its starting value is its `from` argument, and its callback
// is any function value: a lambda, a capture, a named function. A reduce's
// signalling lambda is lowered in the reduce form `(acc, bool)`; a scan's or
// a map_reduce's in the adapter form, since each emits per element and its
// `continue` emits nothing. An iter-sensitive named function, and a capture
// whose own body signals, signal in the adapter form, whose skip and stop a
// reduction reads as keeping its accumulator.
//
// The operands are lowered in Nomi's order: the source, the starting value,
// then the callback. A lambda callback's starting value is lowered at the
// kind its first parameter takes; any other callback is lowered first, which
// has no effect (a function's name or a capture), so the starting value
// takes the kind of the callback's first parameter.
func (bl *irScalarBuilder) iterReduceCall(t *ast.Call, method string) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	srcNode, cbNode, from, ok := irSeededArgs(t, true)
	if !ok || from == nil {
		return no()
	}
	lam, isLambda := cbNode.(*ast.Lambda)
	if isLambda {
		if len(lam.Params) != 2 {
			return no()
		}
		if bl.foldCallbacks == nil {
			bl.foldCallbacks = map[*ast.Lambda]bool{}
		}
		bl.foldCallbacks[lam] = method != "map_reduce"
		defer delete(bl.foldCallbacks, lam)
		if bl.g.callbackCarriesSignal(lam) {
			if method == "reduce" {
				defer bl.markCtl(lam, irCtlReduce)()
			} else {
				defer bl.markCtl(lam, irCtlAdapter)()
			}
		}
	} else {
		if ref := irFuncRefNode(cbNode); ref != nil {
			defer bl.markCtlRef(ref)()
		}
		undo, ok := bl.markSignalCallback(cbNode)
		if !ok {
			return no()
		}
		defer undo()
	}
	args := irQualArgs{temps: make([]ir.Temp, 2), kinds: make([]kind, 2), mobile: make([]bool, 2), ok: true}
	src, sk, mobile, ok := bl.lower(srcNode)
	if !ok {
		return no()
	}
	args.temps[0], args.kinds[0], args.mobile[0] = src, sk, mobile
	lowerSeed := func(seed ast.Node, accK kind) (ir.Temp, bool) {
		v, k, _, ok := bl.lowerWant(seed, accK)
		if !ok {
			return ir.NoTemp, false
		}
		v, _, ok = bl.coerceEmpty(seed, v, k, accK)
		return v, ok
	}
	seedTemp := ir.NoTemp
	if isLambda {
		// The accumulator's kind as the callback's first parameter takes
		// it, so a pattern parameter (`fn (n, xs), x ->`) takes its starting
		// value whole at the kind its `continue` answers.
		if seedTemp, ok = lowerSeed(from, bl.g.lambdaParamKind(lam.Params[0])); !ok {
			return no()
		}
	}
	cb, ck, cmobile, ok := bl.lower(cbNode)
	if !ok {
		return no()
	}
	if !isLambda {
		if ck.tag != tagFunc || len(funcParams(ck)) != 2 {
			return no()
		}
		if seedTemp, ok = lowerSeed(from, funcParams(ck)[0]); !ok {
			return no()
		}
	}
	outerSeed := bl.ctlSeed
	bl.ctlSeed = seedTemp
	defer func() { bl.ctlSeed = outerSeed }()
	// Inside an assertion subject the callback is held and the operands are
	// handed to recordedQualCall for the `values:` rows, as irQualLowerArgs
	// does for every other qualified call.
	if bl.recording > 0 && bl.qualRecord == t && !irHeldValue(bl.f, cb, bl.sides) {
		cp := ir.NewCopy(bl.g.irNodePos(cbNode), bl.f.NewTemp(), cb)
		bl.b.Append(cp)
		bl.side(cp.Dst(), irScalarSide{k: ck, copy: irCopyHold})
		cb, cmobile = cp.Dst(), true
	}
	args.temps[1], args.kinds[1], args.mobile[1] = cb, ck, cmobile
	if bl.qualRecord == t {
		// The rows name the written arguments, `from:` among them.
		rec := irQualArgs{temps: make([]ir.Temp, len(t.Args)), kinds: make([]kind, len(t.Args)), mobile: make([]bool, len(t.Args)), ok: true}
		for i, a := range t.Args {
			switch argExpr(a) {
			case srcNode:
				rec.temps[i], rec.kinds[i], rec.mobile[i] = src, sk, mobile
			case cbNode:
				rec.temps[i], rec.kinds[i], rec.mobile[i] = cb, ck, cmobile
			case from:
				rec.temps[i], rec.kinds[i] = seedTemp, funcParams(ck)[0]
			default:
				rec.ok = false
			}
		}
		bl.qualRecordArgs, bl.qualRecordSeen = rec, true
	}
	return bl.iterCall(t, args, method, srcNode)
}

// irSeqKindOf is the retained kind of a value the checker typed as std's
// `Iter<T>`: the push sequence over T. `project` answers no kind for a generic
// interface instance, so this answers `rt.Seq[T]`'s kind here. Identity is the
// anchor's, not the spelling's. The element is any callable value, a function
// among them (`Iter<(Int) -> Int>`), as irRetainedSeqKind's is.
func (g *gen) irSeqKindOf(t analysis.Type) (kind, bool) {
	it, ok := t.(*analysis.InterfaceType)
	if !ok || len(it.TypeArgs) != 1 || !g.iterOwns("Iter") || g.iter.ty == nil ||
		it.Name != g.iter.ty.Name || it.Origin != g.iter.ty.Origin {
		return kindInvalid, false
	}
	elem := g.project(it.TypeArgs[0])
	if elem != kindUnit && !irCallableValueKind(elem) {
		return kindInvalid, false
	}
	return seqKindIn(g, elem), true
}

// userIterSource is a named source's own `impl Iter`, in this file or a
// sibling, as the retained pipeline walks it: its `each_while`, the element
// it pushes, and its written `known_count` and `is_infinite` when it writes
// them.
type userIterSource struct {
	each       userIterFn
	knownCount *userIterFn
	infinite   *userIterFn
	elem       kind
}

// userSource is the `each_while` u, a named source's own `impl Iter`,
// declares, and the element it pushes, when the retained pipeline can walk
// it: iterUserEachWhile's recognition, a retained element, and a
// source-written body the VM can link. A written `known_count` or
// `is_infinite` override the VM cannot link declines.
func (bl *irScalarBuilder) userSource(sk kind, u userIterImpl) (*userIterSource, kind) {
	each, elem, ok := bl.g.iterUserEachWhile(u, sk)
	if !ok || !irRetainedValueKind(elem) {
		return nil, kindInvalid
	}
	src := &userIterSource{each: each, elem: elem}
	if kc := u.d.items["known_count"]; kc != nil && !kc.inherited {
		f, ok := u.fn(bl.g, "known_count")
		if mk, shared := bl.g.sharedMaybe(kindInt); !ok || !shared || len(f.params) != 1 || f.params[0] != sk || f.result != mk {
			return nil, kindInvalid
		}
		src.knownCount = &f
	}
	if inf := u.d.items["is_infinite"]; inf != nil && !inf.inherited {
		f, ok := u.fn(bl.g, "is_infinite")
		if !ok || len(f.params) != 1 || f.params[0] != sk || f.result != kindBool {
			return nil, kindInvalid
		}
		src.infinite = &f
	}
	return src, elem
}

// userView views a user source through rt.UserSeq. The view's second operand
// references the impl's `each_while`, a third its written `known_count`, and
// a fourth its written `is_infinite`. A view with a fourth and no written
// `known_count` holds Unit third: the source inherits the declining default.
func (bl *irScalarBuilder) userView(at ast.Node, src ir.Temp, sk kind, us *userIterSource, elem kind) ir.Temp {
	pos := bl.g.irNodePos(at)
	refTo := func(f userIterFn) ir.Temp {
		ref := ir.NewRefFunc(pos, bl.f.NewTemp(), f.sym)
		bl.b.Append(ref)
		bl.side(ref.Dst(), irScalarSide{k: f.k})
		return ref.Dst()
	}
	operands := []ir.Temp{src, refTo(us.each)}
	if us.knownCount != nil {
		operands = append(operands, refTo(*us.knownCount))
	}
	if us.infinite != nil {
		if len(operands) == 2 {
			unit := ir.NewUnit(pos, bl.f.NewTemp())
			bl.b.Append(unit)
			bl.side(unit.Dst(), irScalarSide{k: kindUnit})
			operands = append(operands, unit.Dst())
		}
		operands = append(operands, refTo(*us.infinite))
	}
	n := ir.NewIter(pos, bl.f.NewTemp(), ir.IterView, ir.IterOverUserImpl, operands...)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: seqKindIn(bl.g, elem)})
	return n.Dst()
}

// maybeIterElem is the element a prelude `Maybe<T>` pushes as an `Iter`
// source (std's `impl Iter for Maybe<T>`): its payload, zero or one times.
func maybeIterElem(k kind) (kind, bool) {
	if !irMaybeKind(k) || len(k.def.preludeArgs) != 1 {
		return kindInvalid, false
	}
	elem := k.def.preludeArgs[0]
	if !irCallableValueKind(elem) && elem != kindUnit {
		return kindInvalid, false
	}
	return elem, true
}

// irIterRangeElem is the element of a Range the retained pipeline can walk.
func irIterRangeElem(k kind) (kind, bool) {
	elem, ok := rangeElem(k)
	return elem, ok && (elem == kindInt || irRangeDistinctElem(elem))
}

// rangeView answers a Range view's operands: the range, and references to the
// element's `Comparable.compare` and `Discrete.next`, std's or the program's
// own impls (elemComparator, discreteRef).
func (bl *irScalarBuilder) rangeView(at ast.Node, src ir.Temp, elem kind) ([]ir.Temp, bool) {
	if !bl.rangeWalksAt(elem, at) {
		return nil, false
	}
	if _, _, _, ok := bl.sortComparator(elem); !ok {
		return nil, false
	}
	maybeElem, _ := bl.g.sharedMaybe(elem)
	cmp, _ := bl.elemComparator(at, elem)
	next, ok := bl.discreteRef(at, elem, "next", []kind{elem}, maybeElem)
	if !ok {
		return nil, false
	}
	return []ir.Temp{src, cmp, next}, true
}

// rangeKnownCount lowers `Iter.known_count(range)` as a host call to
// rt.RangeKnownCount whose extra operands reference the element's
// `Comparable.compare` and `Discrete.steps_between`, std's or the program's
// own impls; the VM drives the rt function over the linked bodies.
func (bl *irScalarBuilder) rangeKnownCount(t *ast.Call, src ir.Temp, elem kind) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	mk, ok := bl.iterMaybeKind(kindInt)
	if !ok {
		return no()
	}
	at := t.Args[0]
	if !bl.rangeWalksAt(elem, at) {
		return no()
	}
	maybeInt, shared := bl.g.sharedMaybe(kindInt)
	if _, _, _, ok := bl.sortComparator(elem); !ok || !shared {
		return no()
	}
	cmp, _ := bl.elemComparator(at, elem)
	steps, ok := bl.discreteRef(at, elem, "steps_between", []kind{elem, elem}, maybeInt)
	if !ok {
		return no()
	}
	pos := bl.g.irNodePos(at)
	c := ir.NewHostCall(pos, bl.f.NewTemp(), irCallSite(at),
		bl.g.irCalleeSym(irRangeKnownCountHost, irRangeKnownCountHost), src, cmp, steps)
	bl.b.Append(c)
	bl.side(c.Dst(), irScalarSide{k: mk, deferrable: true})
	return c.Dst(), mk, false, true
}

// iterGroupBy lowers `Iter.group_by(source, key_fn)` as one IterGroupBy whose
// Go takes the key's hash-and-equality pair spliced before the callback. The
// key is any map key, so the result is a retained Map of Lists; the VM groups
// through rt.SeqGroupByCells with the machine's key kernels (vm/keys.go).
func (bl *irScalarBuilder) iterGroupBy(t *ast.Call, src ir.Temp, elem kind, fn ir.Temp, fk kind) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if fk.tag != tagFunc || len(funcParams(fk)) != 1 || funcParams(fk)[0] != elem {
		return no()
	}
	key := funcResult(fk)
	if !irMapKeyKind(key) {
		return no()
	}
	// A retained map: its key was just held to irMapKeyKind, and its value,
	// `List<elem>`, is a List of the element of an Iter in the value domain
	// (irRetainedSeqKind: callable or Unit), which irRetainedValueKind's
	// List arm, and so irHeldValueKind and irMapValueKind, admit.
	result := bl.g.mapKind(key, listKindIn(bl.g, elem))
	ok := bl.g.irMapKeyOK(key)
	if !ok {
		return no()
	}
	n := ir.NewIter(bl.g.irNodePos(t), bl.f.NewTemp(), ir.IterGroupBy, ir.IterOverSeq, src, fn)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: result})
	return n.Dst(), result, false, true
}

// sortComparator chooses the comparator `Iter.sort` orders its elements by,
// and `Iter.sort_by` its keys by, answering exactly one of three: a declared
// type's local impl, a callee symbol (a sibling file's impl, or the instance
// of a std generic container's impl at k), or std's own retained
// `Comparable.compare` at k. A std type that is neither a declared struct or
// enum nor a container (Duration, Decimal, calendar's Time) takes the third,
// and a `host fn` compare (Decimal's) is admitted there because the caller
// forwards it as a function value (irHostForward).
func (bl *irScalarBuilder) sortComparator(k kind) (*implItem, *ir.Symbol, *stdFunc, bool) {
	if k.tag == tagTuple {
		// A tuple answers none of the three: comparatorOperand builds its
		// lexicographic compare (irtuplecompare.go).
		return nil, nil, nil, bl.tupleComparable(k)
	}
	if irNominalElemKind(k) || irRangeDistinctElem(k) {
		if local := bl.localCompareItem(k); local != nil {
			return local, nil, nil, true
		}
		if sym := bl.siblingCompareSym(k); sym != nil {
			return nil, sym, nil, true
		}
	} else if irContainerBaseName(k) != "" {
		if sym := bl.stdCompareInstanceSym(k); sym != nil {
			return nil, sym, nil, true
		}
	}
	f := bl.g.stdCompareAt(k, stdEnumKind(stdEnumOrdering))
	if f == nil || (f.irBody == nil && !(f.rtCall != "" && irScalarHost(f))) {
		return nil, nil, nil, false
	}
	return nil, nil, f, true
}

// comparatorOperand emits the comparator sortComparator chose for elem as a
// function value: a reference to the program's impl body (this file's or the
// declaring file's) or to std's retained body, or a forwarding body around
// std's host.
func (bl *irScalarBuilder) comparatorOperand(at ast.Node, elem kind, local *implItem, sibling *ir.Symbol, std *stdFunc) ir.Temp {
	if elem.tag == tagTuple && local == nil && sibling == nil && std == nil {
		return bl.tupleComparator(at, elem)
	}
	pos := bl.g.irNodePos(at)
	switch {
	case sibling != nil:
		ref := ir.NewRefFunc(pos, bl.f.NewTemp(), sibling)
		bl.b.Append(ref)
		bl.side(ref.Dst(), irScalarSide{k: funcKindIn(bl.g, []kind{elem, elem}, stdEnumKind(stdEnumOrdering))})
		return ref.Dst()
	case local != nil:
		ref := ir.NewRefFunc(pos, bl.f.NewTemp(), bl.g.irCalleeSym(local, elem.nomi()+"."+local.name))
		bl.b.Append(ref)
		bl.side(ref.Dst(), irScalarSide{k: funcKindIn(bl.g, local.params, local.result)})
		return ref.Dst()
	case std.irBody == nil:
		fv := ir.NewFuncValue(pos, bl.f.NewTemp(), bl.irHostForward(at, std))
		bl.b.Append(fv)
		bl.side(fv.Dst(), irScalarSide{k: funcKindIn(bl.g, std.params, std.result)})
		return fv.Dst()
	}
	ref := ir.NewRefFunc(pos, bl.f.NewTemp(), std.irBody.Sym())
	bl.b.Append(ref)
	bl.side(ref.Dst(), irScalarSide{k: funcKindIn(bl.g, std.params, std.result)})
	return ref.Dst()
}

// elemComparator is elem's `Comparable.compare` as a function value, chosen
// as `Iter.sort` chooses it, or false when elem has none the builder can
// name.
func (bl *irScalarBuilder) elemComparator(at ast.Node, elem kind) (ir.Temp, bool) {
	local, sibling, std, ok := bl.sortComparator(elem)
	if !ok {
		return ir.NoTemp, false
	}
	return bl.comparatorOperand(at, elem, local, sibling, std), true
}

// irRangeKnownCountHost names the VM host for a Range's count protocol.
const irRangeKnownCountHost = "Range.known_count"

// localCompareItem is a nominal element's local `Comparable.compare`, the
// first route compareResult takes, with a body the builder can retain.
// siblingCompareSym is the callee symbol of a sibling file's written
// `impl Comparable for T { fn compare(a: T, b: T) -> Ordering }`, for an element
// type another file of the program declares, or nil.
func (bl *irScalarBuilder) siblingCompareSym(elem kind) *ir.Symbol {
	if elem.tag != tagNamed || elem.def == nil {
		return nil
	}
	call := &ast.Call{Args: []ast.Node{nil, nil}}
	args := irQualArgs{temps: []ir.Temp{ir.NoTemp, ir.NoTemp}, kinds: []kind{elem, elem}, mobile: []bool{true, true}, ok: true}
	p := bl.qualSiblingIfaceImplPlan(call, args, elem.def, "compare", "Comparable")
	if p == nil {
		// A std instance's body at a program's type (a tuple's part in
		// `List.compare<(Int, P)>`): the impl is the program's, which
		// callerImplPlan finds as it does for `Comparable.compare(a, b)`
		// written there.
		p = bl.callerImplPlan(call, args, "Comparable", "compare", elem)
	}
	if p == nil || p.sym == nil || p.result != stdEnumKind(stdEnumOrdering) {
		return nil
	}
	return p.sym
}

// stdCompareInstanceSym is the callee symbol of the instance of a std generic
// container's `Comparable.compare` at k (`impl Comparable for List<T>` at
// `List<Int>`), or nil.
func (bl *irScalarBuilder) stdCompareInstanceSym(k kind) *ir.Symbol {
	s := bl.g.stdInsts
	base := irContainerBaseName(k)
	if s == nil || base == "" {
		return nil
	}
	f := bl.g.stdGenericTemplate(base, "compare")
	if f == nil || !stdGenericTemplateUsable(f) {
		return nil
	}
	ord := stdEnumKind(stdEnumOrdering)
	tps, ib := s.stdTemplateParams(f)
	args, ok := bl.g.stdRecvTypeArgs(tps, ib, k, false)
	if !ok {
		return nil
	}
	inst, _ := s.instantiate(f, tps, args, bl.g)
	if inst == nil || len(inst.view.params) != 2 || inst.view.params[0] != k || inst.view.params[1] != k || inst.view.result != ord {
		return nil
	}
	return inst.sym
}

func (bl *irScalarBuilder) localCompareItem(elem kind) *implItem {
	impl := bl.g.implsByIface["Comparable"][elem]
	if impl == nil || !impl.lowerable {
		return nil
	}
	it := impl.items["compare"]
	if it == nil || !it.lowerable || irImplSource(it) == nil || len(it.params) != 2 ||
		it.params[0] != elem || it.params[1] != elem || it.result != stdEnumKind(stdEnumOrdering) {
		return nil
	}
	return it
}

// markCtl widens one callback to a signalling form while its enclosing call's
// arguments lower, and answers the undo.
func (bl *irScalarBuilder) markCtl(lam *ast.Lambda, form irCtlForm) func() {
	if bl.ctlCallbacks == nil {
		bl.ctlCallbacks = map[*ast.Lambda]irCtlForm{}
	}
	bl.ctlCallbacks[lam] = form
	return func() { delete(bl.ctlCallbacks, lam) }
}

// markCtlCapture widens the lambda a capture callback lowers as
// (captureFunc, partialApplication) to the adapter's signalling form while
// its enclosing call's arguments lower, and answers the undo.
func (bl *irScalarBuilder) markCtlCapture(c *ast.Capture) func() {
	if bl.ctlCaptures == nil {
		bl.ctlCaptures = map[*ast.Capture]bool{}
	}
	bl.ctlCaptures[c] = true
	return func() { delete(bl.ctlCaptures, c) }
}

// lambdaOfCapture lowers lam, the lambda capture c stands for, widened to
// the signalling form when c is a signalling callback (markCtlCapture).
func (bl *irScalarBuilder) lambdaOfCapture(c *ast.Capture, lam *ast.Lambda) (ir.Temp, kind, bool, bool) {
	if bl.ctlCaptures[c] {
		defer bl.markCtl(lam, irCtlAdapter)()
	}
	return bl.lambda(lam)
}

// irFuncRefNode is n, inside any parentheses, when it may name a function
// as a value, `keep`, `(keep)` or `lib.keep`, and nil otherwise. The
// checker admits a parenthesized iter-sensitive callback as the bare one,
// since `nomi fmt` drops the parentheses.
func irFuncRefNode(n ast.Node) ast.Node {
	for {
		g, ok := n.(*ast.GroupedExpr)
		if !ok {
			break
		}
		n = g.Expr
	}
	switch n.(type) {
	case *ast.Ident, *ast.FieldAccess:
		return n
	}
	return nil
}

// markCtlRef admits a reference to an iter-sensitive named function, `keep`
// or `lib.keep`, as the callback of an Iter operation while its enclosing
// call's arguments lower, and answers the undo. Such a function signals in
// the adapter form (namedCtlForm), which every operation's driver reads.
func (bl *irScalarBuilder) markCtlRef(ref ast.Node) func() {
	if bl.ctlRefs == nil {
		bl.ctlRefs = map[ast.Node]irCtlForm{}
	}
	bl.ctlRefs[ref] = irCtlAdapter
	return func() { delete(bl.ctlRefs, ref) }
}

// iterMaybeKind is the positional terminals' `Maybe<elem>`, answered when the
// prelude anchors Maybe: the process-wide instance for a package-neutral
// element, and this gen's own instance for a declared type's.
func (bl *irScalarBuilder) iterMaybeKind(elem kind) (kind, bool) {
	bl.g.loadPreludes()
	if _, anchored := bl.g.preludeNamed("Maybe"); !anchored {
		return kindInvalid, false
	}
	mk, shared := bl.g.sharedMaybe(elem)
	if !shared && irCallableValueKind(elem) {
		// `Maybe<Point>` renders package-relative, so it is this gen's
		// instance, as `List.head` over the same list asks for it. The
		// callable domain, not only the retained one: an interface element
		// (`List<Named>`) is held as the concrete value, and `maybeOf`, which
		// `Map.get` and `List.head` ask, already instantiates Maybe at it.
		mk = bl.g.preludeInstance(bl.g.preludeByName["Maybe"], []kind{elem})
		// kindInvalid: propagates — preludeInstance answers kindInvalid for an element Maybe cannot be instantiated at, and the terminal declines.
		shared = mk != kindInvalid
	}
	// elem is a lowered sequence's element, so callable or Unit, and Maybe
	// carries any value of the domain (irRetainedPreludePayload).
	if !shared || !irRetainedValueKind(mk) {
		panic("irbuild: no retained Maybe over the element " + elem.nomi())
	}
	return mk, true
}

// iterPaired appends a stage whose Go realization takes a minted tuple
// constructor as its last operand: `Iter.with_index` and `Iter.zip`.
func (bl *irScalarBuilder) iterPaired(t *ast.Call, op ir.IterOp, k kind, operands ...ir.Temp) ir.Temp {
	n := ir.NewIter(bl.g.irNodePos(t), bl.f.NewTemp(), op, ir.IterOverSeq, operands...)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: k})
	return n.Dst()
}

// iterPlainView views a source operand other than an Iter call's first
// (zip's right side, `Task.spawn_all`'s source) as the sequence of its
// element, as the checker recorded its view (viewSourceAt).
func (bl *irScalarBuilder) iterPlainView(at ast.Node, src ir.Temp, sk kind) (ir.Temp, kind, bool) {
	vs, ok := bl.viewSourceAt(at, sk)
	if !ok {
		return ir.NoTemp, kindInvalid, false
	}
	view := func(over ir.IterDomain) (ir.Temp, kind, bool) {
		n := ir.NewIter(bl.g.irNodePos(at), bl.f.NewTemp(), ir.IterView, over, src)
		bl.b.Append(n)
		bl.side(n.Dst(), irScalarSide{k: seqKindIn(bl.g, vs.elem)})
		return n.Dst(), vs.elem, true
	}
	switch vs.dom {
	case analysis.IterViewVector:
		// sk is a lowered value, in the domain (inValueDomain), whatever
		// its element: Unit, a function, an Iter.
		return view(ir.IterOverVector)
	case analysis.IterViewSet:
		return view(ir.IterOverSet)
	case analysis.IterViewMaybe:
		return view(ir.IterOverMaybe)
	case analysis.IterViewMap, analysis.IterViewRange, analysis.IterViewImpl:
		// `Iter.zip(xs, m)`, `Iter.zip(xs, 5..9)`: a Map pushes its `(K, V)`
		// pairs, a Range its elements and a user source what its
		// `each_while` does, viewed as an Iter call's first operand is.
		if v, ok := bl.viewAs(at, src, sk, vs, vs.elem); ok {
			return v, vs.elem, true
		}
	case analysis.IterViewString:
		return view(ir.IterOverString)
	case analysis.IterViewBytes:
		return view(ir.IterOverBytes)
	case viewSeq:
		return src, vs.elem, true
	case analysis.IterViewList:
		return view(ir.IterOverList)
	}
	return ir.NoTemp, kindInvalid, false
}

// irIterSourceArg is the source expression of a written Iter call: its
// first argument.
func irIterSourceArg(t *ast.Call) ast.Node {
	if len(t.Args) == 0 {
		return nil
	}
	return argExpr(t.Args[0])
}

// mapView views a Map as its `(K, V)` pairs. Go takes a minted tuple
// constructor; the VM builds the tuple itself.
func (bl *irScalarBuilder) mapView(at ast.Node, src ir.Temp, m, pair kind) ir.Temp {
	n := ir.NewIter(bl.g.irNodePos(at), bl.f.NewTemp(), ir.IterView, ir.IterOverMap, src)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: seqKindIn(bl.g, pair)})
	return n.Dst()
}

// sortOperands appends a sort's Direction operand when the call passed one.
func sortOperands(direction ir.Temp, ops ...ir.Temp) []ir.Temp {
	if direction != ir.NoTemp {
		ops = append(ops, direction)
	}
	return ops
}

// irMustHold asserts what the value domain promises a stage that builds a
// composite from the elements it moves (a pair, a chunk, a flattened list):
// every element is a lowered value's, in the domain, and a retained tuple
// or List holds any such value. A failure is a builder bug, not a program
// the builder declines.
func irMustHold(held bool, stage string, k kind) {
	if !held {
		panic("iterCall: " + stage + " builds " + k.nomi() + ", which the domain does not hold")
	}
}
