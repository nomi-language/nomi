package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// Map storage and operations use rt in both consumers. Plain scalar keys
// avoid nominal hash/equality dispatch; values need no boxing coercions.
func irRetainedMapKind(k kind) bool {
	return k.tag == tagMap && k.comp != nil && len(k.comp.parts) == 2 &&
		irMapKeyKind(k.comp.parts[0]) && irMapValueKind(k.comp.parts[1])
}

// irMapKeyKind is a retained map's key domain. The VM hashes and compares a
// key as `==` does: a declared type through its hand-written Equatable and
// Hashable (recorded by irRecordKeyImpls), everything else structurally
// (vm/keys.go). So a key may be a scalar leaf, Unit (one value, rt.HashUnit),
// a Decimal (scale-insensitive: rt.EqDecimal and rt.HashDecimal), a tuple, a
// list, a retained struct or enum (a prelude Option among them), a
// Dynamic (by its contents: rt.DynamicEqual and rt.DynamicHash), or a
// `Type<T>` witness (by the type it names: rt.TypeWitnessEqual and
// rt.TypeWitnessHash).
func irMapKeyKind(k kind) bool {
	if irHeldKind(k) {
		// An interface value: hashed and compared by its run-time type,
		// as `==` on two of them is (ir.ValIface).
		return true
	}
	if k == kindUnit || isDecimalKind(k) || irScalarLeafKind(k) || irRetainedTupleKind(k) || irRetainedListKind(k) || irRetainedLeafKind(k) || irByteValueKind(k) || irStdHandleKind(k) || irDynamicKind(k) || irTypeWitnessKind(k) {
		return true
	}
	if elem, vector := vectorElem(k); vector {
		return irMapKeyKind(elem)
	}
	if elem, set := setElem(k); set {
		// A Set is a record over its membership map: hashed and compared
		// order-insensitively, as its elements are.
		return irSetElemKind(elem)
	}
	if elem, isRange := rangeElem(k); isRange && irRetainedRangeKind(k) {
		// A Range is std's struct of its bounds: hashed and compared
		// structurally, as its endpoints are.
		return irMapKeyKind(elem)
	}
	// kindInvalid: marker — a zero-sized marker has no inner value.
	if k.tag == tagNamed && k.def != nil && k.def.isDistinct && !k.def.rtOpaque && irCompositeDistinct(k.def) && k.def.inner != kindInvalid {
		// A distinct over a key (`Items(List<Int>)`): hashed and compared
		// structurally through its inner value.
		return irMapKeyKind(k.def.inner)
	}
	switch k.tag {
	case tagTuple, tagAnonStruct, tagList, tagMap:
		// Composed of keys: `List<Decimal>`, `Map<String, Int>`, a record.
		if k.comp == nil || len(k.comp.parts) == 0 {
			return false
		}
		for _, p := range k.comp.parts {
			if !irMapKeyKind(p) {
				return false
			}
		}
		return true
	}
	return k.tag == tagNamed && k.def != nil && (irRetainedStructKind(k.def) || irRetainedEnumKind(k.def))
}

// irHoldsExistential reports whether k is, or is composed of, an interface
// value: a position a narrower value enters by erasure.
func irHoldsExistential(k kind) bool {
	if irHeldKind(k) {
		return true
	}
	if k.comp == nil {
		return false
	}
	for _, p := range k.comp.parts {
		if irHoldsExistential(p) {
			return true
		}
	}
	return false
}

// irMapKeyOK reports whether the VM's map operations take key as a key: a
// scalar, or a value rt.Hash and rt.Equal read structurally.
func (g *gen) irMapKeyOK(key kind) bool {
	return irScalarLeafKind(key) || irMapKeyKind(key) || g.irStructuralValueKind(key)
}

// irStructuralValueKind is a value with a stored type that rt.Hash and
// rt.Equal read structurally: anything but
// a function, an Iter, an existential, an unbound type parameter or a host
// handle other than a Vector. A map literal's key and value may be any of
// these; the operations on the map keep their own narrower checks.
func (g *gen) irStructuralValueKind(k kind) bool {
	if isDecimalKind(k) {
		// rt.Decimal: rt.EqDecimal and rt.HashDecimal, scale-insensitive.
		return true
	}
	switch k.tag {
	case tagInvalid, tagFunc, tagSeq, tagIface, tagTypeParam:
		return false
	case tagNamed:
		if _, vector := vectorElem(k); vector {
			return g.irValType(k) != nil
		}
		if irByteValueKind(k) {
			return true
		}
		if k.def == nil || k.def.rtOpaque {
			return false
		}
		if _, _, host := genHostOf(k); host {
			if _, vector := vectorElem(k); !vector {
				return false
			}
		}
	}
	return g.irValType(k) != nil
}

// irMapTransportKind admits a declared struct key. Only mapMake builds one;
// the machine hashes and compares the key as `==` does, through the written
// impls the key table names (irRecordKeyImpls). Map calls and Debug keep
// their scalar-key checks.
func irMapTransportKind(k kind) bool {
	return k.tag == tagMap && k.comp != nil && len(k.comp.parts) == 2 &&
		k.comp.parts[0].tag == tagNamed && k.comp.parts[0].def != nil && irRetainedStructKind(k.comp.parts[0].def) &&
		irMapValueKind(k.comp.parts[1])
}

// irMapValueKind is a retained map's value domain: scalar leaves, retained
// lists of them, retained user structs and enums, Dynamic
// (`Dynamic.as_dict`) and an opaque host handle (`Map<String, Regex>`),
// which a map only carries. Keys stay scalar, so
// hashing and ordering are unchanged.
func irMapValueKind(k kind) bool {
	return irScalarLeafKind(k) || (k.tag == tagList && irRetainedListKind(k)) || irSelfContainerEnum(k) || irNominalElemKind(k) || irDynamicKind(k) || irHostHandleKind(k) ||
		// An interface's existential, which the map holds as the
		// concrete value (`Map<String, Display>`).
		irHeldKind(k) ||
		// Any value a key may be, a nested map among them.
		irMapKeyKind(k) ||
		irHeldValueKind(k) ||
		// Any other value the callable domain holds: a function-wrapping
		// distinct, a record holding an Iter (`Map<String, {a: Iter<Int>}>`).
		// A map only carries its values; what reads one checks its kind.
		irCallableValueKind(k)
}

// irHeldValueKind is a function value, an Iter, a map of such values, or a
// List or Vector of any value the callable domain holds (`(Int) -> Int`, `group_by`'s `List<(Int) -> Int>`
// groups), as a container's element or a map's value. The VM's containers
// hold values of every kind, and nothing a container does with an element
// it only carries looks at one: the checker rejects `==` over functions,
// and Debug keeps its own checks.
func irHeldValueKind(k kind) bool {
	if k.tag == tagFunc {
		return irCallableValueKind(k)
	}
	if irRetainedSeqKind(k) {
		// An Iter, held as the closure over its source (`Map<String,
		// Iter<Int>>`), as a binding or a field holds one.
		return true
	}
	if k.tag == tagMap && irRetainedMapKind(k) {
		// A map of held values (`Map<String, Map<String, Iter<Int>>>`),
		// which has no equality to be a key with.
		return true
	}
	_, vector := vectorElem(k)
	return (k.tag == tagList || vector) && irRetainedValueKind(k)
}

// irSelfContainerEnum is a retained std enum whose payloads include its own
// containers, std/json's Json. Such a map value renders through std's Debug
// impl, so irDebugValueKind still declines the map.
func irSelfContainerEnum(k kind) bool {
	if k.tag != tagNamed || !irRetainedEnumKind(k.def) {
		return false
	}
	for _, v := range k.def.variants {
		for _, p := range v.payloads {
			if irSelfContainerPayload(k.def, p.k) {
				return true
			}
		}
	}
	return false
}

func (bl *irScalarBuilder) mapMake(t *ast.MapLit) (ir.Temp, kind, bool, bool) {
	// kindInvalid: sentinel — no position's kind; the literal's record gives its kind.
	return bl.mapMakeOf(t, kindInvalid)
}

// mapMakeOf builds a map literal at its kind (literalKind: its record, or
// want, the kind of the position, where the builder made it).
func (bl *irScalarBuilder) mapMakeOf(t *ast.MapLit, want kind) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if t.TypeName != nil && !t.ShapeHead {
		// `Json.Obj{"k" => v}` is the variant call over the anonymous map.
		anon := *t
		anon.TypeName = nil
		return bl.attachedVariant(t, t.TypeName, &anon)
	}
	if d := bl.g.literalDistinct(t); d != nil && t.TypeName == nil {
		// `kvs: Kvs = {"a" => 1}`: the distinct type the position expects,
		// over the map.
		anon := *t
		return bl.compositeDistinctMake(t, []ast.Node{&anon}, d)
	}
	if len(t.Spreads) == 1 && t.Spreads[0].At == 0 {
		if len(t.Entries) == 0 {
			return no()
		}
		return bl.mapUpdate(t, want)
	}
	if len(t.Spreads) > 0 {
		return bl.mapSpreadLit(t)
	}
	if len(t.Entries) == 0 {
		return bl.emptyLiteral(t, mapSpreadJoin, want)
	}
	return bl.mapEntries(t, want)
}

// mapUpdate builds `{..base, key => value}`, a map literal with one spread
// written first: the base, then the entries as a map literal, then
// `Map.merge(base, literal)`, so the entries win and keep the order
// Map.merge gives them. The base enters the literal's kind, as any spread
// does (collectSpread), and the entries are built at it.
func (bl *irScalarBuilder) mapUpdate(t *ast.MapLit, want kind) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	spread := t.Spreads[0].Head
	base, bk, _, ok := bl.lower(spread)
	if !ok {
		return no()
	}
	p, ok := bl.collectSpread(t, spreadPart{at: spread, v: base, k: bk}, mapSpreadJoin)
	if !ok {
		return no()
	}
	base, bk = p.v, p.k
	if !mapSpreadJoin.typed(want) {
		// A copy the builder made of a literal, which has no record, takes
		// its base's kind.
		want = bk
	}
	var lit ir.Temp
	var lk kind
	if mapHasHole(t) {
		// An entry updates the value at its key, which it reads from the
		// base: the base is read once per such entry and merged once.
		base = bl.holdTemp(spread, base, bk)
		lit, lk, _, ok = bl.mapEntriesOver(t, want, base, bk)
	} else {
		lit, lk, _, ok = bl.mapEntries(t, want)
	}
	if !ok {
		return no()
	}
	// The entries are built at the literal's kind, which the base entered,
	// and a Map the callable domain holds is a retained one.
	if bk != lk || !irRetainedMapKind(lk) {
		panic("irbuild: a map update whose entries " + lk.nomi() + " are not its base's " + bk.nomi())
	}
	c := ir.NewHostCall(bl.g.irNodePos(t), bl.f.NewTemp(), ir.OrdinaryCall,
		bl.g.irCalleeSym("Map.merge", "Map.merge"), base, lit)
	bl.b.Append(c)
	bl.side(c.Dst(), irScalarSide{k: lk, deferrable: true})
	return c.Dst(), lk, false, true
}

// mapHasHole reports whether an entry's value of the map update t is an
// update literal.
func mapHasHole(t *ast.MapLit) bool {
	for _, e := range t.Entries {
		if readsCurrent(e.Value) {
			return true
		}
	}
	return false
}

// mapEntries builds a map literal's entries, ignoring a spread, at the
// literal's kind (literalKind), want where it has no record.
func (bl *irScalarBuilder) mapEntries(t *ast.MapLit, want kind) (ir.Temp, kind, bool, bool) {
	// kindInvalid: sentinel — no base; no entry reads one.
	return bl.mapEntriesOver(t, want, ir.NoTemp, kindInvalid)
}

// mapEntriesOver is mapEntries for the entries of a map update over base, of
// kind bk: an entry whose value is an update literal updates the value at its
// key (updateEntry). Keys and values are lowered in source order, each
// against the literal's key or value kind, as an argument is against its
// parameter.
func (bl *irScalarBuilder) mapEntriesOver(t *ast.MapLit, want kind, base ir.Temp, bk kind) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	k, ok := bl.builtLiteralKind(t, mapSpreadJoin, want)
	if !ok {
		return no()
	}
	parts := k.comp.parts
	if !bl.g.irMapKeyOK(parts[0]) || (!irMapValueKind(parts[1]) && !bl.g.irStructuralValueKind(parts[1])) {
		bl.g.dc.note("a map literal outside the retained maps: " + k.nomi())
		return no()
	}
	values := make([]ir.Temp, 0, len(t.Entries)*2)
	for _, entry := range t.Entries {
		var key ir.Temp
		for j, node := range []ast.Node{entry.Key, entry.Value} {
			var v ir.Temp
			var ok bool
			if j == 1 && base != ir.NoTemp && readsCurrent(node) {
				var vk kind
				if v, vk, ok = bl.updateEntry(node, key, base, bk); !ok || vk != parts[1] {
					return no()
				}
				values = append(values, v)
				continue
			}
			if v, _, _, ok = bl.lowerWant(node, parts[j]); !ok {
				return no()
			}
			if j == 0 && base != ir.NoTemp && readsCurrent(entry.Value) {
				// The key is read by the entry's update and by the map.
				v = bl.holdTemp(node, v, parts[0])
				key = v
			}
			values = append(values, v)
		}
	}
	n := ir.NewMakeMap(bl.g.irNodePos(t), bl.f.NewTemp(), values)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: k})
	return n.Dst(), k, false, true
}

func (bl *irScalarBuilder) mapCallPlan(t *ast.Call, args irQualArgs, method string) *irQualPlan {
	switch method {
	case "keys", "values":
		return bl.mapProjectionPlan(t, args, method)
	case "map_values", "map_keys":
		return bl.mapMapValuesPlan(t, args, method)
	}
	if !args.ok {
		return nil
	}
	fn, known := mapFuncs[method]
	if !known || len(args.kinds) != fn.args {
		return nil
	}
	if method == "put" && args.kinds[0].tag == tagMap {
		if v, k, ok := bl.mapAtSolvedExistential(t, args.temps[0], args.kinds[0]); ok {
			args.temps[0], args.kinds[0] = v, k
		}
	}
	if !bl.mapOperandsAtSolved(t, args, method) {
		return nil
	}
	if args.kinds[0].tag != tagMap || !irRetainedMapKind(args.kinds[0]) {
		return nil
	}
	k := args.kinds[0]
	key, val := k.comp.parts[0], k.comp.parts[1]
	if method == "merge" {
		if args.kinds[1] != k {
			return nil
		}
	} else if len(args.kinds) > 1 && args.kinds[1] != key {
		if !irHoldsExistential(key) {
			return nil
		}
		// A key entering an interface-valued key position (`Map.get(m, p)`
		// for a `Map<Show, Int>`): erased or boxed as an argument is.
		v, got, ok := bl.convertTo(t.Args[1], args.temps[1], args.kinds[1], key)
		if !ok {
			return nil
		}
		args.temps[1], args.kinds[1] = v, got
	}
	if method == "put" && args.kinds[2] != val && !(irHeldKind(val) && bl.g.irErases(val, args.kinds[2], nil)) {
		// A value entering an interface's existential value is the value
		// itself (`Map.put(m, "b", True)` for a `Map<String, Display>`);
		// one holding it (`(Display, Int)`) enters as an argument does.
		if !irHoldsExistential(val) {
			return nil
		}
		v, got, ok := bl.convertTo(t.Args[2], args.temps[2], args.kinds[2], val)
		if !ok {
			return nil
		}
		args.temps[2], args.kinds[2] = v, got
	}
	if method == "get" {
		if _, anchored := preludeSpecAnchor(preludeSpecNamed("Option")); !anchored {
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
		bl.noteUnrecordedKind("Map result", t)
		result, ok = bl.g.mapResultKind(fn.result, key, val, t)
	}
	if !ok || !irRetainedValueKind(result) {
		return nil
	}
	ok = bl.g.irMapKeyOK(key)
	if !ok {
		return nil
	}
	name := "Map." + method
	return &irQualPlan{token: name, name: name, result: result, host: true}
}

// mapAtSolvedExistential is the map v of kind have retyped as the checker's
// instantiation of call t's map parameter when that instantiation's value
// is an interface's existential that have's value erases into
// (`Map.put(m, "b", True)` for an `m` built as `Map<String, Int>` and solved
// as `Map<String, Display>`). The existential is the concrete value, so the
// map is the same value at the wider kind.
func (bl *irScalarBuilder) mapAtSolvedExistential(t *ast.Call, v ir.Temp, have kind) (ir.Temp, kind, bool) {
	ft := bl.g.checkedCallSignature(t)
	if ft == nil || len(ft.Params) == 0 || have.comp == nil || len(have.comp.parts) != 2 {
		return v, have, false
	}
	solved := bl.g.project(ft.Params[0])
	if solved == have || solved.tag != tagMap || solved.comp == nil || len(solved.comp.parts) != 2 ||
		!irRetainedMapKind(solved) || solved.comp.parts[0] != have.comp.parts[0] {
		return v, have, false
	}
	val := solved.comp.parts[1]
	if !irHeldKind(val) || !bl.g.irErases(val, have.comp.parts[1], nil) {
		return v, have, false
	}
	n := ir.NewCopy(bl.g.irNodePos(t), bl.f.NewTemp(), v)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: solved})
	return n.Dst(), solved, true
}

// mapOperandsAtSolved converts each map operand of t (the first, and
// `merge`'s second) to the map the checker solved K and V at
// (recordedTypeArg), in place in args, where a later operand widened them
// past it: `Map.put(m, "b", shape)` for m a `Map<String, Circle>` is a
// `Map<String, Shape>`, and m converts to it as a user function's argument
// would. False when a conversion the checker admitted is one convertTo
// cannot do.
func (bl *irScalarBuilder) mapOperandsAtSolved(t *ast.Call, args irQualArgs, method string) bool {
	key, _ := bl.g.recordedTypeArg(t, method, "K")
	val, _ := bl.g.recordedTypeArg(t, method, "V")
	// kindInvalid: sentinel — recordedTypeArg's nothing-recorded answer; the operands keep their own kinds.
	if key == kindInvalid || val == kindInvalid || !irMapKeyKind(key) || !irMapValueKind(val) {
		return true
	}
	want := bl.g.mapKind(key, val)
	maps := 1
	if method == "merge" {
		maps = 2
	}
	for i := 0; i < maps && i < len(args.kinds); i++ {
		have := args.kinds[i]
		if have == want || have.tag != tagMap || !irRetainedMapKind(have) {
			continue
		}
		v, got, ok := bl.convertTo(t.Args[i], args.temps[i], have, want)
		if !ok {
			return false
		}
		args.temps[i], args.kinds[i] = v, got
	}
	return true
}

// mapProjectionPlan is `Map.keys(m)` / `Map.values(m)`: the keys, or the
// values, as a List in the keys' insertion order (rt.MapKeys / rt.MapValues),
// which is what std's `Iter.map |> Iter.to_list` bodies answer.
func (bl *irScalarBuilder) mapProjectionPlan(t *ast.Call, args irQualArgs, method string) *irQualPlan {
	if !args.ok || len(args.kinds) != 1 {
		return nil
	}
	k := args.kinds[0]
	if k.tag != tagMap || !irRetainedMapKind(k) {
		return nil
	}
	elem := k.comp.parts[0]
	if method == "values" {
		elem = k.comp.parts[1]
	}
	result := bl.g.listKind(elem)
	if !irRetainedValueKind(result) {
		return nil
	}
	name := "Map." + method
	return &irQualPlan{token: name, name: name, result: result, host: true}
}

// mapMapValuesPlan is `Map.map_values(m, f)`: every value transformed in the
// keys' insertion order and the map rebuilt (rt.MapMapValues), as std's
// `Iter.map |> Iter.to_map` body does.
//
// `Map.map_keys(m, f)` is its sibling: every key transformed and the map
// rebuilt in insertion order, a colliding later key replacing the earlier
// one's value, as std's `Iter.map |> Iter.to_map` body does.
func (bl *irScalarBuilder) mapMapValuesPlan(t *ast.Call, args irQualArgs, method string) *irQualPlan {
	if !args.ok || len(args.kinds) != 2 {
		return nil
	}
	k, cb := args.kinds[0], args.kinds[1]
	part := 1
	if method == "map_keys" {
		part = 0
	}
	if k.tag != tagMap || !irRetainedMapKind(k) || cb.tag != tagFunc || len(funcParams(cb)) != 1 || funcParams(cb)[0] != k.comp.parts[part] {
		return nil
	}
	u := funcResult(cb)
	result := bl.g.mapKind(k.comp.parts[0], u)
	if method == "map_keys" {
		result = bl.g.mapKind(u, k.comp.parts[1])
	}
	if !irRetainedMapKind(result) {
		return nil
	}
	name := "Map." + method
	return &irQualPlan{token: name, name: name, result: result, host: true}
}
