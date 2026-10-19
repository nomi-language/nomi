package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// Map storage and operations use rt in both consumers. Plain scalar keys
// avoid nominal hash/equality dispatch; values need no boxing coercions.
func irRetainedMapKind(k kind) bool {
	if k == kindEmptyMap {
		return true
	}
	return k.tag == tagMap && k.comp != nil && len(k.comp.parts) == 2 &&
		irMapKeyKind(k.comp.parts[0]) && irMapValueKind(k.comp.parts[1])
}

// irMapKeyKind is a retained map's key domain. The VM hashes and compares a
// key as `==` does: a declared type through its hand-written Equatable and
// Hashable (recorded by irRecordKeyImpls), everything else structurally
// (vm/keys.go). So a key may be a scalar leaf, Unit (one value, rt.HashUnit),
// a Decimal (scale-insensitive: rt.EqDecimal and rt.HashDecimal), a tuple, a
// list, or a retained struct or enum (a prelude Maybe among them).
func irMapKeyKind(k kind) bool {
	if k == kindUnit || isDecimalKind(k) || irScalarLeafKind(k) || irRetainedTupleKind(k) || irRetainedListKind(k) || irRetainedLeafKind(k) || irByteValueKind(k) {
		return true
	}
	if elem, vector := vectorElem(k); vector && k != kindEmptyVector {
		return irMapKeyKind(elem)
	}
	if elem, set := setElem(k); set && k != kindEmptySet {
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

// irMapTransportKind admits a declared struct key. Only mapMake builds one,
// after irStructEqualityKind settles that native hashes and compares the key
// structurally; Map calls and Debug keep their scalar-key checks.
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
		irExistentialKind(k) ||
		// Any value a key may be, a nested map among them.
		(k != kindEmptyMap && k != kindEmptyList && irMapKeyKind(k)) ||
		irHeldValueKind(k)
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
	if k.tag == tagMap && k != kindEmptyMap && irRetainedMapKind(k) {
		// A map of held values (`Map<String, Map<String, Iter<Int>>>`),
		// which has no equality to be a key with.
		return true
	}
	if k == kindEmptyList || k == kindEmptyVector {
		return false
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
	return bl.mapMakeOf(t, nil)
}

// mapMakeOf builds a map literal; wants, when not nil, is the expected key
// and value kind each entry is lowered against.
func (bl *irScalarBuilder) mapMakeOf(t *ast.MapLit, wants []kind) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if t.TypeName != nil {
		// `%Json.Obj{"k" => v}` is the variant call over the anonymous map.
		anon := *t
		anon.TypeName = nil
		return bl.attachedVariant(t, t.TypeName, &anon)
	}
	if t.Spread != nil {
		if len(t.Entries) == 0 {
			return no()
		}
		return bl.mapUpdate(t, wants)
	}
	if len(t.Entries) == 0 {
		return bl.emptyMapLit(t, wants)
	}
	return bl.mapEntries(t, wants)
}

// emptyMapLit builds `%{}`, the empty map. Like `[]`, it is the map its
// position makes it when the checker solved the key and value types there,
// and otherwise the empty map of no type yet.
func (bl *irScalarBuilder) emptyMapLit(t *ast.MapLit, wants []kind) (ir.Temp, kind, bool, bool) {
	n := ir.NewMakeMap(bl.g.irNodePos(t), bl.f.NewTemp(), nil)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: kindEmptyMap, pureMake: true})
	parts := wants
	if len(parts) != 2 {
		parts = bl.checkedLiteralParts(t, tagMap)
	}
	if len(parts) == 2 && irFullyTyped(parts[0]) && irFullyTyped(parts[1]) {
		want := bl.g.mapKind(parts[0], parts[1])
		if irRetainedMapKind(want) {
			if v, k, ok := bl.coerceEmpty(t, n.Dst(), kindEmptyMap, want); ok && k == want {
				return v, k, true, true
			}
		}
	}
	return n.Dst(), kindEmptyMap, true, true
}

// mapUpdate builds `%{..base, key => value}`: the base, then the entries as
// a map literal, then `Map.merge(base, literal)`, so the entries win and
// keep the order Map.merge gives them. The checker typed the literal as the
// base's `Map<K, V>`, which is the kind both operands are lowered to.
func (bl *irScalarBuilder) mapUpdate(t *ast.MapLit, wants []kind) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	base, bk, _, ok := bl.lower(t.Spread)
	if !ok {
		return no()
	}
	if wants == nil && bk.tag == tagMap && bk != kindEmptyMap && bk.comp != nil && len(bk.comp.parts) == 2 {
		wants = bk.comp.parts
	}
	var lit ir.Temp
	var lk kind
	if mapHasHole(t) {
		// An entry updates the value at its key, which it reads from the
		// base: the base is read once per such entry and merged once.
		base = bl.holdTemp(t.Spread, base, bk)
		lit, lk, _, ok = bl.mapEntriesOver(t, wants, base, bk)
	} else {
		lit, lk, _, ok = bl.mapEntries(t, wants)
	}
	if !ok {
		return no()
	}
	if bk == kindEmptyMap {
		// `%{..%{}, "a" => 1}`: the entries say what the base holds.
		if base, bk, ok = bl.coerceEmpty(t.Spread, base, bk, lk); !ok {
			return no()
		}
	}
	if bk != lk || !irRetainedMapKind(lk) {
		return no()
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
		if ast.UpdateHole(e.Value) != nil {
			return true
		}
	}
	return false
}

// mapEntries builds a map literal's entries, ignoring a spread; wants is as
// mapMakeOf's.
func (bl *irScalarBuilder) mapEntries(t *ast.MapLit, wants []kind) (ir.Temp, kind, bool, bool) {
	return bl.mapEntriesOver(t, wants, ir.NoTemp, kindEmptyMap)
}

// mapEntriesOver is mapEntries for the entries of a map update over base, of
// kind bk: an entry whose value is an update literal updates the value at its
// key (updateEntry).
func (bl *irScalarBuilder) mapEntriesOver(t *ast.MapLit, wants []kind, base ir.Temp, bk kind) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	// Keys and values are lowered in source order. Entries whose keys (or
	// values) differ only by an embedded type join to the enum that embeds
	// it (`%{1 => circle, 2 => shape}` is a `Map<Int, Shape>`), as the
	// checker joins them; the embedded ones are widened afterwards.
	var temps [2][]ir.Temp
	var items [2][]ast.Node
	var kinds [2][]kind
	// A key or value type holding an embedded type's enum below its top
	// (`Map<Int, #(Int, Shape)>`) is each entry's context (literalParts).
	if wants == nil {
		wants = bl.literalParts(t, tagMap)
	}
	for _, entry := range t.Entries {
		var key ir.Temp
		for j, node := range []ast.Node{entry.Key, entry.Value} {
			var v ir.Temp
			var k kind
			var ok bool
			if j == 1 && base != ir.NoTemp && ast.UpdateHole(node) != nil {
				v, k, ok = bl.updateEntry(node, key, base, bk)
				if !ok {
					return no()
				}
				items[j], temps[j], kinds[j] = append(items[j], node), append(temps[j], v), append(kinds[j], k)
				continue
			}
			if wants != nil {
				v, k, _, ok = bl.lowerWant(node, wants[j])
			} else {
				v, k, _, ok = bl.lower(node)
			}
			if !ok {
				return no()
			}
			if j == 0 && base != ir.NoTemp && ast.UpdateHole(entry.Value) != nil {
				// The key is read by the entry's update and by the map.
				v = bl.holdTemp(node, v, k)
				key = v
			}
			items[j], temps[j], kinds[j] = append(items[j], node), append(temps[j], v), append(kinds[j], k)
		}
	}
	var parts [2]kind
	for j := range parts {
		k, joined := irEmbedsJoin(kinds[j])
		if !joined {
			// Entries that differ by an empty collection (`{1 => [], 2 =>
			// [1]}`) take the checked key or value type, as a list's
			// items do.
			k, joined = bl.literalItemsCoerced(t, tagMap, j, items[j], temps[j], kinds[j])
		}
		if !joined || (j == 0 && !irMapKeyKind(k) && !bl.g.irStructEqualityKind(k) && !bl.g.irStructuralValueKind(k)) || (j == 1 && !irMapValueKind(k) && !bl.g.irStructuralValueKind(k)) ||
			!bl.widenEmbedded(items[j], temps[j], kinds[j], k) {
			return no()
		}
		parts[j] = k
	}
	values := make([]ir.Temp, 0, len(t.Entries)*2)
	for i := range t.Entries {
		values = append(values, temps[0][i], temps[1][i])
	}
	ok := bl.g.irMapKeyOK(parts[0])
	if !ok {
		return no()
	}
	k := bl.g.mapKind(parts[0], parts[1])
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
	if method == "put" && args.kinds[0].tag == tagMap && args.kinds[0] != kindEmptyMap {
		if v, k, ok := bl.mapAtSolvedExistential(t, args.temps[0], args.kinds[0]); ok {
			args.temps[0], args.kinds[0] = v, k
		}
	}
	if args.kinds[0] == kindEmptyMap && method == "put" && irMapKeyKind(args.kinds[1]) && irMapValueKind(args.kinds[2]) {
		want := bl.g.mapKind(args.kinds[1], args.kinds[2])
		v, k, ok := bl.coerceEmpty(t.Args[0], args.temps[0], args.kinds[0], want)
		if !ok {
			return nil
		}
		args.temps[0], args.kinds[0] = v, k
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
		return nil
	}
	if method == "put" && args.kinds[2] != val && !(irExistentialKind(val) && bl.g.irErases(val, args.kinds[2])) {
		// A value entering an interface's existential value is the value
		// itself (`Map.put(m, "b", True)` for a `Map<String, Display>`).
		return nil
	}
	if method == "get" {
		if _, anchored := bl.g.preludeNamed("Maybe"); !anchored {
			return nil
		}
	}
	result, ok := bl.g.mapResultKind(fn.result, key, val, t)
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
	if !irExistentialKind(val) || !bl.g.irErases(val, have.comp.parts[1]) {
		return v, have, false
	}
	n := ir.NewCopy(bl.g.irNodePos(t), bl.f.NewTemp(), v)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: solved})
	return n.Dst(), solved, true
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
