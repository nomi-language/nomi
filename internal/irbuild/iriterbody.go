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
func (bl *irScalarBuilder) iterCall(t *ast.Call, args irQualArgs, method string) (ir.Temp, kind, bool, bool) {
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
	want := 1
	switch method {
	case "map", "filter", "reduce", "take", "sort_with", "sort_by", "any?", "all?", "each_while", "find", "zip", "concat", "at", "partition", "group_by",
		"drop", "take_while", "drop_while", "each", "flat_map", "chunks", "chunk_by", "join":
		want = 2
	case "to_list", "count", "empty?", "not_empty?", "sort", "first", "last", "reverse", "with_index", "known_count",
		"cycle", "to_set", "to_vector", "to_map", "flatten":
	default:
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
	if (method == "empty?" || method == "not_empty?") && sk.tag == tagEmptyList {
		// `Iter.empty?([])`: the literal has no elements, so the answer is
		// the constant std's `first(source) == None` computes.
		c := ir.NewBool(bl.g.irNodePos(t), bl.f.NewTemp(), method == "empty?")
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
		}
		v, k, ok := bl.coerceEmpty(t.Args[0], src, sk, bl.g.listKind(elem))
		if !ok {
			return no()
		}
		src, sk = v, k
		args.temps[0], args.kinds[0] = v, k
	}
	rangeElem, isRange := irIterRangeElem(sk)
	if isRange && method == "known_count" {
		// A Range's count protocol is answered with rt.RangeKnownCount
		// rather than through the view; `count`, `empty?` and `not_empty?`
		// walk the view.
		return bl.rangeKnownCount(t, src, rangeElem)
	}
	vecElem, isVector := vectorElem(sk)
	isVector = isVector && (irRetainedVectorKind(sk) || (irRetainedValueKind(vecElem)))
	// A Map pushes `(K, V)` pairs and a Set its members.
	isMap := sk.tag == tagMap && sk != kindEmptyMap && irRetainedMapKind(sk)
	setMember, isSet := setElem(sk)
	isSet = isSet && sk != kindEmptySet && irRetainedSetKind(sk)
	var userEach *implItem
	var userElem kind
	if !isVector && !isMap && !isSet {
		userEach, userElem = bl.userSource(sk)
	}
	if sk.tag != tagList && sk.tag != tagSeq && sk != kindString && sk != irBytesKind() && !isRange && !isVector && !isMap && !isSet && userEach == nil {
		return no()
	}
	elem := kindString
	if isMap {
		elem = bl.g.tupleKind([]kind{sk.comp.parts[0], sk.comp.parts[1]})
		if !irRetainedTupleKind(elem) {
			return no()
		}
	} else if isSet {
		elem = setMember
	} else if isRange {
		elem = rangeElem
	} else if isVector {
		elem = vecElem
	} else if userEach != nil {
		elem = userElem
	} else if sk == irBytesKind() {
		elem = irByteKind()
	} else if sk != kindString {
		if sk.comp == nil || len(sk.comp.parts) != 1 || !(irRetainedValueKind(sk.comp.parts[0]) || sk.comp.parts[0] == kindUnit || (irRetainedSeqKind(sk.comp.parts[0])) || (irExistentialKind(sk.comp.parts[0]))) {
			return no()
		}
		elem = sk.comp.parts[0]
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
		if fk.tag != tagFunc || len(funcParams(fk)) != 1 || funcParams(fk)[0] != elem || !(irRetainedValueKind(funcResult(fk)) || (method == "map" && funcResult(fk) == kindUnit)) {
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
	if method == "any?" || method == "all?" || method == "each_while" || method == "find" || method == "partition" {
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
		if fk.tag != tagFunc || len(funcParams(fk)) != 2 || funcParams(fk)[1] != elem || funcParams(fk)[0] != funcResult(fk) || !irRetainedValueKind(funcResult(fk)) {
			return no()
		}
		result = funcResult(fk)
		if reduceSeed(t) == nil && result != elem {
			return no()
		}
	}
	// A callback lowered under a signalling form makes its operation the
	// signalling one.
	signalling := (method == "map" || method == "filter" || method == "reduce") && len(args.temps) > 1 && bl.iterSignalling(args, 1)
	appendOp := func(at ast.Node, op ir.IterOp, over ir.IterDomain, k kind, operands ...ir.Temp) ir.Temp {
		n := ir.NewIter(bl.g.irNodePos(at), bl.f.NewTemp(), op, over, operands...)
		if signalling && op.HasSignallingForm() {
			n = ir.NewIterSignalling(bl.g.irNodePos(at), n.Dst(), op, over, operands...)
		}
		bl.b.Append(n)
		bl.side(n.Dst(), irScalarSide{k: k})
		return n.Dst()
	}
	if method == "count" && sk.tag == tagList {
		return appendOp(t, ir.IterCount, ir.IterOverList, kindInt, src), kindInt, false, true
	}
	if method == "count" && isMap {
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
		switch {
		case sk.tag == tagList:
			over = ir.IterOverList
		case sk == kindString:
			over = ir.IterOverString
		case sk.tag == tagSeq:
			over = ir.IterOverSeq
		case isVector:
			over = ir.IterOverVector
		case isMap:
			over = ir.IterOverMap
		case isSet:
			over = ir.IterOverSet
		}
		if over != ir.IterOverNothing {
			return appendOp(t, ir.IterKnownCount, over, mk, src), mk, false, true
		}
	}
	if isRange {
		view, ok := bl.rangeView(t, src, elem)
		if !ok {
			return no()
		}
		src = appendOp(t.Args[0], ir.IterView, ir.IterOverRange, seqKindIn(bl.g, elem), view...)
	} else if isMap {
		src = bl.mapView(t.Args[0], src, sk, elem)
	} else if isSet {
		src = appendOp(t.Args[0], ir.IterView, ir.IterOverSet, seqKindIn(bl.g, elem), src)
	} else if isVector {
		src = appendOp(t.Args[0], ir.IterView, ir.IterOverVector, seqKindIn(bl.g, elem), src)
	} else if userEach != nil {
		src = bl.userView(t.Args[0], src, sk, userEach, elem)
	} else if sk.tag == tagList {
		src = appendOp(t.Args[0], ir.IterView, ir.IterOverList, seqKindIn(bl.g, elem), src)
	} else if sk == irBytesKind() {
		src = appendOp(t.Args[0], ir.IterView, ir.IterOverBytes, seqKindIn(bl.g, elem), src)
	} else if sk == kindString {
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
	case "at":
		mk, ok := bl.iterMaybeKind(elem)
		if !ok || args.kinds[1] != kindInt {
			return no()
		}
		return appendOp(t, ir.IterAt, ir.IterOverSeq, mk, src, args.temps[1]), mk, false, true
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
		if !irRetainedTupleKind(pair) {
			return no()
		}
		k := seqKindIn(bl.g, pair)
		return bl.iterPaired(t, ir.IterWithIndex, k, src), k, false, true
	case "zip":
		right, relem, ok := bl.iterPlainView(t.Args[1], args.temps[1], args.kinds[1])
		if !ok {
			return no()
		}
		pair := bl.g.tupleKind([]kind{elem, relem})
		if !irRetainedTupleKind(pair) {
			return no()
		}
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
		return appendOp(t, ir.IterSortWith, ir.IterOverSeq, result, sortOperands(direction, src, cmp)...), result, false, true
	case "sort_with":
		return appendOp(t, ir.IterSortWith, ir.IterOverSeq, result, src, args.temps[1]), result, false, true
	case "sort_by":
		keyFn := ir.NewCopy(bl.g.irNodePos(t.Args[1]), bl.f.NewTemp(), args.temps[1])
		bl.b.Append(keyFn)
		bl.side(keyFn.Dst(), irScalarSide{k: args.kinds[1], copy: irCopyProjection})
		var ref *ir.Ref
		cmp := ir.NoTemp
		if sortKeySibling != nil {
			ref = ir.NewRefFunc(bl.g.irNodePos(t), bl.f.NewTemp(), sortKeySibling)
			bl.b.Append(ref)
			bl.side(ref.Dst(), irScalarSide{k: funcKindIn(bl.g, []kind{sortKey, sortKey}, stdEnumKind(stdEnumOrdering))})
		} else if sortKeyLocal != nil {
			ref = ir.NewRefFunc(bl.g.irNodePos(t), bl.f.NewTemp(), bl.g.irCalleeSym(sortKeyLocal, sortKey.nomi()+"."+sortKeyLocal.name))
			bl.b.Append(ref)
			bl.side(ref.Dst(), irScalarSide{k: funcKindIn(bl.g, sortKeyLocal.params, sortKeyLocal.result)})
		} else if sortKeyCmp.irBody == nil {
			// Decimal's compare is a `host fn`, forwarded as `sort` does.
			fv := ir.NewFuncValue(bl.g.irNodePos(t), bl.f.NewTemp(), bl.irHostForward(t, sortKeyCmp))
			bl.b.Append(fv)
			bl.side(fv.Dst(), irScalarSide{k: funcKindIn(bl.g, sortKeyCmp.params, sortKeyCmp.result)})
			cmp = fv.Dst()
		} else {
			ref = ir.NewRefFunc(bl.g.irNodePos(t), bl.f.NewTemp(), sortKeyCmp.irBody.Sym())
			bl.b.Append(ref)
			bl.side(ref.Dst(), irScalarSide{k: funcKindIn(bl.g, sortKeyCmp.params, sortKeyCmp.result)})
		}
		if ref != nil {
			cmp = ref.Dst()
		}
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
	case "empty?":
		return appendOp(t, ir.IterEmpty, ir.IterOverSeq, kindBool, src), kindBool, false, true
	case "not_empty?":
		return appendOp(t, ir.IterNotEmpty, ir.IterOverSeq, kindBool, src), kindBool, false, true
	}
	if method == "any?" {
		return appendOp(t, ir.IterAny, ir.IterOverSeq, kindBool, src, args.temps[1]), kindBool, false, true
	}
	if method == "all?" {
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
	if method == "reduce" {
		operands := []ir.Temp{src}
		seed := reduceSeed(t)
		if signalling && seed != nil {
			if bl.ctlSeed == ir.NoTemp {
				return no()
			}
			operands = append(operands, bl.ctlSeed)
		} else if seed != nil {
			v, k, _, ok := bl.lowerWant(seed, result)
			if !ok {
				return no()
			}
			v, _, ok = bl.coerceEmpty(seed, v, k, result)
			if !ok {
				return no()
			}
			operands = append(operands, v)
		}
		operands = append(operands, args.temps[1])
		return appendOp(t, ir.IterReduce, ir.IterOverSeq, result, operands...), result, false, true
	}
	return appendOp(t, ir.IterToList, ir.IterOverSeq, result, src), result, false, true
}

// reduceSeed is the initial accumulator an `Iter.reduce` call writes: its
// callback lambda's first parameter default, or nil for a lambda without one
// and for a function value, whose reduction the first element seeds.
func reduceSeed(t *ast.Call) ast.Node {
	if len(t.Args) != 2 {
		return nil
	}
	lam, ok := t.Args[1].(*ast.Lambda)
	if !ok || len(lam.Params) == 0 {
		return nil
	}
	return lam.Params[0].Default
}

func (bl *irScalarBuilder) iterReduceCall(t *ast.Call) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if len(t.Args) != 2 {
		return no()
	}
	lam, ok := t.Args[1].(*ast.Lambda)
	if !ok {
		// A function value (`Iter.reduce(xs, add)`): no seed, so the first
		// element seeds the accumulator. A named function that signals is
		// lowered in the reduce form by its own body (irNamedCtlForm).
		return bl.iterCall(t, bl.irQualLowerArgs(t), "reduce")
	}
	if len(lam.Params) != 2 {
		return no()
	}
	if bl.reduceSeeds == nil {
		bl.reduceSeeds = map[*ast.Lambda]bool{}
	}
	bl.reduceSeeds[lam] = true
	defer delete(bl.reduceSeeds, lam)
	if !callbackCarriesSignal(lam) {
		return bl.iterCall(t, bl.irQualLowerArgs(t), "reduce")
	}
	// The order is the source, then the seed at the accumulator's kind, then
	// the widened callback, so the seed is evaluated before the callback is
	// written.
	defer bl.markCtl(lam, irCtlReduce)()
	args := irQualArgs{temps: make([]ir.Temp, 2), kinds: make([]kind, 2), mobile: make([]bool, 2), ok: true}
	src, sk, mobile, ok := bl.lower(t.Args[0])
	if !ok {
		return no()
	}
	args.temps[0], args.kinds[0], args.mobile[0] = src, sk, mobile
	if seed := lam.Params[0].Default; seed != nil {
		var accK kind
		if lam.Params[0].TypeAnnotation != nil {
			accK = bl.g.typeOf(lam.Params[0].TypeAnnotation)
		} else {
			_, accK = bl.g.inferredParamKind(lam.Params[0])
		}
		v, k, _, ok := bl.lowerWant(seed, accK)
		if !ok {
			return no()
		}
		if v, _, ok = bl.coerceEmpty(seed, v, k, accK); !ok {
			return no()
		}
		bl.ctlSeed = v
		defer func() { bl.ctlSeed = ir.NoTemp }()
	}
	cb, ck, cmobile, ok := bl.lower(lam)
	if !ok {
		return no()
	}
	args.temps[1], args.kinds[1], args.mobile[1] = cb, ck, cmobile
	return bl.iterCall(t, args, "reduce")
}

// irSeqKindOf is the retained kind of a value the checker typed as std's
// `Iter<T>`: the push sequence over T. `project` answers no kind for a generic
// interface instance, so this answers `rt.Seq[T]`'s kind here. Identity is the
// anchor's, not the spelling's.
func (g *gen) irSeqKindOf(t analysis.Type) (kind, bool) {
	it, ok := t.(*analysis.InterfaceType)
	if !ok || len(it.TypeArgs) != 1 || !g.iterOwns("Iter") || g.iter.ty == nil ||
		it.Name != g.iter.ty.Name || it.Origin != g.iter.ty.Origin {
		return kindInvalid, false
	}
	elem := g.project(it.TypeArgs[0])
	if !irRetainedValueKind(elem) {
		return kindInvalid, false
	}
	return seqKindIn(g, elem), true
}

// userSource is the `each_while` a named source's own `impl Iter` declares,
// and the element it pushes, when the retained pipeline can walk it:
// iterUserSource's recognition, a retained element, and a source-written body
// the VM can link. A written `known_count` override declines.
func (bl *irScalarBuilder) userSource(sk kind) (*implItem, kind) {
	it, elem, ok := bl.g.iterUserEachWhile(sk)
	if !ok || irImplSource(it) == nil || !irRetainedValueKind(elem) {
		return nil, kindInvalid
	}
	if kc := bl.g.implsByIface["Iter"][sk].items["known_count"]; kc != nil && !kc.inherited {
		return nil, kindInvalid
	}
	return it, elem
}

// userView views a user source through rt.UserSeq. The view's second operand
// references the impl's `each_while`, which Go names by its Go identifier.
func (bl *irScalarBuilder) userView(at ast.Node, src ir.Temp, sk kind, it *implItem, elem kind) ir.Temp {
	pos := bl.g.irNodePos(at)
	ref := ir.NewRefFunc(pos, bl.f.NewTemp(), bl.g.irCalleeSym(it, sk.nomi()+"."+it.name))
	bl.b.Append(ref)
	bl.side(ref.Dst(), irScalarSide{k: funcKindIn(bl.g, it.params, it.result)})
	n := ir.NewIter(pos, bl.f.NewTemp(), ir.IterView, ir.IterOverUserImpl, src, ref.Dst())
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: seqKindIn(bl.g, elem)})
	return n.Dst()
}

// seqView is src, a value of kind sk, as a lowered `Iter<elem>`: the same view
// iterCall builds over its source, for a source entering a declared `Iter<T>`
// position (a parameter, a field, a payload, an annotated binding). A sequence
// of that element passes unchanged; any other kind declines.
func (bl *irScalarBuilder) seqView(at ast.Node, src ir.Temp, sk, elem kind) (ir.Temp, bool) {
	if !irRetainedValueKind(elem) && elem != kindUnit {
		return ir.NoTemp, false
	}
	want := seqKindIn(bl.g, elem)
	if sk == want {
		return src, true
	}
	if sk == kindEmptyList {
		v, k, ok := bl.coerceEmpty(at, src, sk, bl.g.listKind(elem))
		if !ok {
			return ir.NoTemp, false
		}
		src, sk = v, k
	}
	view := func(over ir.IterDomain, operands ...ir.Temp) ir.Temp {
		n := ir.NewIter(bl.g.irNodePos(at), bl.f.NewTemp(), ir.IterView, over, operands...)
		bl.b.Append(n)
		bl.side(n.Dst(), irScalarSide{k: want})
		return n.Dst()
	}
	switch {
	case sk.tag == tagList && sk.comp != nil && len(sk.comp.parts) == 1 && sk.comp.parts[0] == elem:
		return view(ir.IterOverList, src), true
	case sk == kindString && elem == kindString:
		return view(ir.IterOverString, src), true
	case sk == irBytesKind() && elem == irByteKind():
		return view(ir.IterOverBytes, src), true
	}
	if rangeElem, isRange := irIterRangeElem(sk); isRange {
		if rangeElem != elem {
			return ir.NoTemp, false
		}
		ops, ok := bl.rangeView(at, src, elem)
		if !ok {
			return ir.NoTemp, false
		}
		// The view answers the Range's own known_count, so it carries the
		// element's distance function (rt.Seq's Count).
		maybeInt, shared := bl.g.sharedMaybe(kindInt)
		if !shared {
			return ir.NoTemp, false
		}
		steps := bl.g.stdIfaceFnAt("Discrete.steps_between", elem, []kind{elem, elem}, maybeInt)
		if steps == nil || steps.irBody == nil {
			return ir.NoTemp, false
		}
		stepsRef := ir.NewRefFunc(bl.g.irNodePos(at), bl.f.NewTemp(), steps.irBody.Sym())
		bl.b.Append(stepsRef)
		bl.side(stepsRef.Dst(), irScalarSide{k: funcKindIn(bl.g, steps.params, steps.result)})
		return view(ir.IterOverRange, append(ops, stepsRef.Dst())...), true
	}
	if vecElem, isVector := vectorElem(sk); isVector {
		if vecElem != elem || !(irRetainedVectorKind(sk) || irRetainedValueKind(vecElem)) {
			return ir.NoTemp, false
		}
		return view(ir.IterOverVector, src), true
	}
	if sk.tag == tagMap && sk != kindEmptyMap && irRetainedMapKind(sk) {
		pair := bl.g.tupleKind([]kind{sk.comp.parts[0], sk.comp.parts[1]})
		if pair != elem || !irRetainedTupleKind(pair) {
			return ir.NoTemp, false
		}
		return bl.mapView(at, src, sk, pair), true
	}
	if member, isSet := setElem(sk); isSet {
		if sk == kindEmptySet || !irRetainedSetKind(sk) || member != elem {
			return ir.NoTemp, false
		}
		return view(ir.IterOverSet, src), true
	}
	if it, userElem := bl.userSource(sk); it != nil && userElem == elem {
		return bl.userView(at, src, sk, it, elem), true
	}
	return ir.NoTemp, false
}

// irIterRangeElem is the element of a Range the retained pipeline can walk.
func irIterRangeElem(k kind) (kind, bool) {
	elem, ok := rangeElem(k)
	return elem, ok && (elem == kindInt || irRangeDistinctElem(elem))
}

// rangeView answers a Range view's operands: the range, and references to the
// cached stdlib bodies of the element's `Comparable.compare` and
// `Discrete.next`. Go delivers both references as Go function values.
func (bl *irScalarBuilder) rangeView(at ast.Node, src ir.Temp, elem kind) ([]ir.Temp, bool) {
	cmp := bl.g.stdCompareAt(elem, stdEnumKind(stdEnumOrdering))
	maybeElem, shared := bl.g.sharedMaybe(elem)
	if cmp == nil || cmp.irBody == nil || !shared {
		return nil, false
	}
	next := bl.g.stdIfaceFnAt("Discrete.next", elem, []kind{elem}, maybeElem)
	if next == nil || next.irBody == nil {
		return nil, false
	}
	if ok := bl.g.rangeWalks(elem, at); !ok {
		return nil, false
	}
	cmpRef := ir.NewRefFunc(bl.g.irNodePos(at), bl.f.NewTemp(), cmp.irBody.Sym())
	bl.b.Append(cmpRef)
	bl.side(cmpRef.Dst(), irScalarSide{k: funcKindIn(bl.g, cmp.params, cmp.result)})
	nextRef := ir.NewRefFunc(bl.g.irNodePos(at), bl.f.NewTemp(), next.irBody.Sym())
	bl.b.Append(nextRef)
	bl.side(nextRef.Dst(), irScalarSide{k: funcKindIn(bl.g, next.params, next.result)})
	return []ir.Temp{src, cmpRef.Dst(), nextRef.Dst()}, true
}

// rangeKnownCount lowers `Iter.known_count(range)` as a host call to
// rt.RangeKnownCount whose extra operands reference the element's cached
// `Comparable.compare` and `Discrete.steps_between` bodies. Go delivers both
// as Go function values; the VM drives the same rt function over the linked
// bodies.
func (bl *irScalarBuilder) rangeKnownCount(t *ast.Call, src ir.Temp, elem kind) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	mk, ok := bl.iterMaybeKind(kindInt)
	if !ok {
		return no()
	}
	if ok := bl.g.rangeWalks(elem, t.Args[0]); !ok {
		return no()
	}
	cmp := bl.g.stdCompareAt(elem, stdEnumKind(stdEnumOrdering))
	maybeInt, shared := bl.g.sharedMaybe(kindInt)
	if cmp == nil || cmp.irBody == nil || !shared {
		return no()
	}
	steps := bl.g.stdIfaceFnAt("Discrete.steps_between", elem, []kind{elem, elem}, maybeInt)
	if steps == nil || steps.irBody == nil {
		return no()
	}
	at := t.Args[0]
	pos := bl.g.irNodePos(at)
	cmpRef := ir.NewRefFunc(pos, bl.f.NewTemp(), cmp.irBody.Sym())
	bl.b.Append(cmpRef)
	bl.side(cmpRef.Dst(), irScalarSide{k: funcKindIn(bl.g, cmp.params, cmp.result)})
	stepsRef := ir.NewRefFunc(pos, bl.f.NewTemp(), steps.irBody.Sym())
	bl.b.Append(stepsRef)
	bl.side(stepsRef.Dst(), irScalarSide{k: funcKindIn(bl.g, steps.params, steps.result)})
	c := ir.NewHostCall(pos, bl.f.NewTemp(), irCallSite(at),
		bl.g.irCalleeSym(irRangeKnownCountHost, irRangeKnownCountHost), src, cmpRef.Dst(), stepsRef.Dst())
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
	result := bl.g.mapKind(key, listKindIn(bl.g, elem))
	if result.tag != tagMap || !irRetainedMapKind(result) {
		return no()
	}
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
// `impl Comparable for T { fn compare(a: T, b: T): Ordering }`, for an element
// type another file of the program declares, or nil.
func (bl *irScalarBuilder) siblingCompareSym(elem kind) *ir.Symbol {
	if elem.tag != tagNamed || elem.def == nil {
		return nil
	}
	call := &ast.Call{Args: []ast.Node{nil, nil}}
	args := irQualArgs{temps: []ir.Temp{ir.NoTemp, ir.NoTemp}, kinds: []kind{elem, elem}, mobile: []bool{true, true}, ok: true}
	p := bl.qualSiblingIfaceImplPlan(call, args, elem.def, "compare", "Comparable")
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
	args, ok := bl.g.stdInstSolve(f, tps, ib, []kind{k, k}, ord, k)
	if !ok {
		return nil
	}
	inst, _ := s.instantiate(f, tps, args, nil, bl.g)
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

// markCtlRef admits a reference to an iter-sensitive named function as the
// callback of the Iter operation method while its enclosing call's arguments
// lower, and answers the undo. A reduction drives the reduce form and the
// other operations the adapter form, so a function whose arity gives it the
// other form is not admitted.
func (bl *irScalarBuilder) markCtlRef(ref *ast.Ident, method string) func() {
	form := irCtlAdapter
	if method == "reduce" {
		form = irCtlReduce
	}
	if bl.ctlRefs == nil {
		bl.ctlRefs = map[ast.Node]irCtlForm{}
	}
	bl.ctlRefs[ref] = form
	return func() { delete(bl.ctlRefs, ref) }
}

// iterMaybeKind is the positional terminals' `Maybe<elem>`, answered when the
// prelude anchors Maybe: the process-wide instance for a package-neutral
// element, and this gen's own instance for a declared type's.
func (bl *irScalarBuilder) iterMaybeKind(elem kind) (kind, bool) {
	bl.g.loadPreludes()
	if _, anchored := bl.g.preludeByName["Maybe"]; !anchored {
		return kindInvalid, false
	}
	mk, shared := bl.g.sharedMaybe(elem)
	if !shared && irRetainedValueKind(elem) {
		// `Maybe<Point>` renders package-relative, so it is this gen's
		// instance, as `List.head` over the same list asks for it.
		mk = bl.g.preludeInstance(bl.g.preludeByName["Maybe"], []kind{elem})
		// kindInvalid: propagates — preludeInstance answers kindInvalid for an element Maybe cannot be instantiated at, and the terminal declines.
		shared = mk != kindInvalid
	}
	if !shared || !irRetainedValueKind(mk) {
		return kindInvalid, false
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

// iterPlainView views a second source operand — zip's right side — for the
// families whose view takes no minted operand: a sequence, a List, a String,
// Bytes and a Vector.
func (bl *irScalarBuilder) iterPlainView(at ast.Node, src ir.Temp, sk kind) (ir.Temp, kind, bool) {
	view := func(over ir.IterDomain, elem kind) (ir.Temp, kind, bool) {
		n := ir.NewIter(bl.g.irNodePos(at), bl.f.NewTemp(), ir.IterView, over, src)
		bl.b.Append(n)
		bl.side(n.Dst(), irScalarSide{k: seqKindIn(bl.g, elem)})
		return n.Dst(), elem, true
	}
	if vecElem, isVector := vectorElem(sk); isVector && irRetainedVectorKind(sk) {
		return view(ir.IterOverVector, vecElem)
	}
	if member, isSet := setElem(sk); isSet && sk != kindEmptySet && irRetainedSetKind(sk) {
		return view(ir.IterOverSet, member)
	}
	switch {
	case sk == kindString:
		return view(ir.IterOverString, kindString)
	case sk == irBytesKind():
		return view(ir.IterOverBytes, irByteKind())
	case (sk.tag == tagSeq || sk.tag == tagList) && sk.comp != nil && len(sk.comp.parts) == 1 && irRetainedValueKind(sk.comp.parts[0]):
		if sk.tag == tagSeq {
			return src, sk.comp.parts[0], true
		}
		return view(ir.IterOverList, sk.comp.parts[0])
	}
	return ir.NoTemp, kindInvalid, false
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
