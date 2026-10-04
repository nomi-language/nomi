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
// (vm/keys.go). So a key may be a scalar leaf, a Decimal (scale-insensitive:
// rt.EqDecimal and rt.HashDecimal), a tuple, a list, or a retained
// struct or enum (a prelude Maybe among them).
func irMapKeyKind(k kind) bool {
	if isDecimalKind(k) || irScalarLeafKind(k) || irRetainedTupleKind(k) || irRetainedListKind(k) || irRetainedLeafKind(k) || irByteValueKind(k) {
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
		// Any value a key may be, a nested map among them.
		(k != kindEmptyMap && k != kindEmptyList && irMapKeyKind(k))
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
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if t.TypeName != nil {
		// `Json.Obj{"k" => v}` is the variant call over the anonymous map.
		anon := *t
		anon.TypeName = nil
		return bl.attachedVariant(t, t.TypeName, &anon)
	}
	if t.TypeName != nil || len(t.Entries) == 0 {
		return no()
	}
	values := make([]ir.Temp, 0, len(t.Entries)*2)
	var parts [2]kind
	for i, entry := range t.Entries {
		for j, node := range []ast.Node{entry.Key, entry.Value} {
			v, k, _, ok := bl.lower(node)
			if !ok || (j == 0 && !irMapKeyKind(k) && !bl.g.irStructEqualityKind(k) && !bl.g.irStructuralValueKind(k)) || (j == 1 && !irMapValueKind(k) && !bl.g.irStructuralValueKind(k)) || (i > 0 && k != parts[j]) {
				return no()
			}
			parts[j] = k
			values = append(values, v)
		}
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
	if method == "put" && args.kinds[2] != val {
		return nil
	}
	if method == "get" {
		if _, anchored := bl.g.preludeByName["Maybe"]; !anchored {
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

// emptyMap recognizes the reserved constructor before generic-call routing.
// Explicit type arguments take the call's checked result.
func (bl *irScalarBuilder) emptyMap(t *ast.Call) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	fa, ok := t.Func.(*ast.FieldAccess)
	if !ok || fa.Field == nil || fa.Field.Name != "empty" || len(t.Args) != 0 {
		return no()
	}
	owner, ok := fa.Object.(*ast.TypeIdent)
	if !ok || owner.Name != "Map" {
		return no()
	}
	want := kindEmptyMap
	if len(t.TypeArgs) != 0 {
		want = bl.g.solvedCallReturn(t)
		if want.tag != tagMap || !irRetainedMapKind(want) {
			return no()
		}
	}
	n := ir.NewMakeMap(bl.g.irNodePos(t), bl.f.NewTemp(), nil)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: kindEmptyMap, pureMake: true})
	v, k, ok := bl.coerceEmpty(t, n.Dst(), kindEmptyMap, want)
	return v, k, true, ok
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

// emptyCollection recognizes `Set.new()` and `Vector.empty()`, the empty
// constructors std declares for Set and Vector, as their empty literals:
// `#{}` and `#[]`. The literal takes its element type from the context, as
// `Map.empty()` does; explicit type arguments take the call's checked result.
func (bl *irScalarBuilder) emptyCollection(t *ast.Call) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	fa, ok := t.Func.(*ast.FieldAccess)
	if !ok || fa.Field == nil || len(t.Args) != 0 {
		return no()
	}
	owner, ok := fa.Object.(*ast.TypeIdent)
	if !ok {
		return no()
	}
	if _, shadowed := bl.g.namedType(owner.Name); shadowed && bl.g.stdModule == "" {
		// A user type of the same name; inside std the name is std's own.
		return no()
	}
	var c *ir.Const
	var empty kind
	switch {
	case owner.Name == "Set" && fa.Field.Name == "new":
		c, empty = ir.NewEmptySet(bl.g.irNodePos(t), bl.f.NewTemp(), nil), kindEmptySet
	case owner.Name == "Vector" && fa.Field.Name == "empty":
		c, empty = ir.NewEmptyVector(bl.g.irNodePos(t), bl.f.NewTemp(), nil), kindEmptyVector
	default:
		return no()
	}
	want := empty
	if len(t.TypeArgs) != 0 {
		want = bl.g.solvedCallReturn(t)
		if !irRetainedValueKind(want) {
			return no()
		}
	}
	bl.b.Append(c)
	if want == empty {
		return c.Dst(), empty, true, true
	}
	v, k, ok := bl.coerceEmpty(t, c.Dst(), empty, want)
	return v, k, true, ok
}
