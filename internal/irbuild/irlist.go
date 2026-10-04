package irbuild

import (
	"slices"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// irRetainedListKind admits lists whose leaves have scalar Debug semantics.
// Empty literals can acquire an element type at a consuming position.
func irRetainedListKind(k kind) bool {
	if k == kindEmptyList {
		return true
	}
	if k.tag != tagList || k.comp == nil || len(k.comp.parts) != 1 {
		return false
	}
	elem := k.comp.parts[0]
	return irScalarLeafKind(elem) || irRetainedListKind(elem)
}

// irListTransportKind admits represented scalar wrappers as list elements,
// tuples of retained values (`Iter.to_map`'s entries), Dynamic
// (`Dynamic.as_list`), and a local interface's existential (`List<Display>`),
// which the VM holds as the concrete values.
// Debug, equality and patterns keep their narrower scalar-list checks.
func irListTransportKind(k kind) bool {
	return k.tag == tagList && k.comp != nil && len(k.comp.parts) == 1 &&
		(irRetainedLeafKind(k.comp.parts[0]) || irListTransportKind(k.comp.parts[0]) || irRetainedVectorKind(k.comp.parts[0]) || irRetainedSetKind(k.comp.parts[0]) || irRetainedRecordKind(k.comp.parts[0]) || isDecimalKind(k.comp.parts[0]) || irNominalElemKind(k.comp.parts[0]) || irPreludeElemKind(k.comp.parts[0]) || irLeafTupleKind(k.comp.parts[0]) || irRetainedTupleKind(k.comp.parts[0]) || irListMapElemKind(k.comp.parts[0]) || irDynamicKind(k.comp.parts[0]) || irByteElemKind(k.comp.parts[0]) || irExistentialKind(k.comp.parts[0]) || irConcHandleKind(k.comp.parts[0]) || k.comp.parts[0] == kindUnit)
}

// irByteElemKind is std's Byte as a list element (`Bytes.to_list`, a list
// literal of bytes): a word-bank leaf, compared structurally in a list as
// rt.Equal compares it.
func irByteElemKind(k kind) bool {
	// kindInvalid: sentinel — an unanchored Byte must not match a refused operand.
	return k != kindInvalid && k == irByteKind()
}

// irListMapElemKind is a Map over scalar leaves carried as a list element
// (`List<Map<String, Int>>`).
func irListMapElemKind(k kind) bool {
	return k.tag == tagMap && k != kindEmptyMap && irRetainedMapKind(k) && k.comp != nil &&
		irScalarLeafKind(k.comp.parts[0]) && irScalarLeafKind(k.comp.parts[1])
}

// irLeafTupleKind is a structural tuple of scalar leaves, such as a
// `(String, String)` pair, carried as a list element.
func irLeafTupleKind(k kind) bool {
	if k.tag != tagTuple || k.comp == nil || len(k.comp.parts) < 2 {
		return false
	}
	for _, part := range k.comp.parts {
		if !irScalarLeafKind(part) {
			return false
		}
	}
	return true
}

// irPreludeElemKind is an anchored prelude enum instance, such as the
// Fragment<String> a typed literal's handler receives, carried as a list
// element.
func irPreludeElemKind(k kind) bool {
	return k.tag == tagNamed && k.def != nil && k.def.preludeOf != nil && irRetainedEnumKind(k.def)
}

// irNominalElemKind is a retained declared struct or user enum carried as a
// list element. Debug, equality and patterns over such lists keep their own
// checks.
func irNominalElemKind(k kind) bool {
	return k.tag == tagNamed && k.def != nil &&
		(irRetainedStructKind(k.def) || (irRetainedEnumKind(k.def) && k.def.preludeOf == nil))
}

// listMake evaluates a spread tail first, then heads from left to right.
// A plain literal leaves its last element inline; spread heads are forced.
func (bl *irScalarBuilder) listMake(at ast.Node, items []ast.Node, tail ast.Node) (ir.Temp, kind, bool, bool) {
	// kindInvalid: sentinel — no annotation supplies the element kind.
	return bl.listMakeOf(at, items, tail, kindInvalid)
}

// irAnnotatedNominalList reports whether a binding's list literal takes its
// nominal element kind from the annotation, as annotatedList does, so
// embedded values widen into it.
func irAnnotatedNominalList(t *ast.ListLit, want kind) bool {
	return t.TypeName == nil && len(t.Items) != 0 && want.tag == tagList && want.comp != nil &&
		len(want.comp.parts) == 1 && (irNominalElemKind(want.comp.parts[0]) || irPreludeElemKind(want.comp.parts[0]) || irStdStructExistential(want.comp.parts[0]) || irExistentialKind(want.comp.parts[0]) || want.comp.parts[0].tag == tagSeq)
}

func (bl *irScalarBuilder) listMakeOf(at ast.Node, items []ast.Node, tail ast.Node, want kind) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if len(items) == 0 && tail == nil {
		c := ir.NewEmptyList(bl.g.irNodePos(at), bl.f.NewTemp(), nil)
		bl.b.Append(c)
		return c.Dst(), kindEmptyList, true, true
	}
	values := make([]ir.Temp, len(items))
	kinds := make([]kind, len(items))
	elem := want
	rest := ir.NoTemp
	restKind := kindInvalid
	if tail != nil {
		v, k, _, ok := bl.lower(tail)
		if !ok || !(irRetainedListKind(k) || irListTransportKind(k)) {
			return no()
		}
		rest, restKind = v, k
		if k.tag == tagList {
			elem = k.comp.parts[0]
		}
	}
	// A bare `None` beside typed elements (`[Some(1), None]`) takes their
	// element kind, as the checker gives it: it is lowered after them. It
	// has no effect, so lowering it later does not reorder anything.
	var deferred []int
	for i, item := range items {
		var v ir.Temp
		var k kind
		var ok bool
		// kindInvalid: sentinel — no annotation supplies the element kind.
		if name, _, _, bare := preludeValueName(item); bare && name == "None" && want == kindInvalid && tail == nil && len(items) > len(deferred)+1 {
			deferred = append(deferred, i)
			continue
		}
		// kindInvalid: sentinel — no annotation supplies the element kind.
		if want != kindInvalid {
			// The annotation's element type is each item's context, so a bare
			// `None` is that element's variant.
			v, k, _, ok = bl.lowerTypedOperand(item, want)
		} else {
			v, k, _, ok = bl.lower(item)
		}
		if !ok || (!irRetainedLeafKind(k) && !irRetainedListKind(k) && !irNominalElemKind(k) && !irLeafTupleKind(k) && !irRetainedTupleKind(k) && !irEmbeddedIn(want, k) &&
			!isDecimalKind(k) && !irPreludeElemKind(k) && !irListMapElemKind(k) && !irByteElemKind(k) && !(irExistentialKind(k)) &&
			// kindInvalid: sentinel — no annotation supplies an element to erase into.
			!(want != kindInvalid && bl.g.irErases(want, k)) &&
			// Any other value with a stored type: the VM's list holds values
			// of every kind (a function, an Iter, a Context, a list of
			// structs), and each consumer of the list (Debug, equality,
			// patterns) checks the element kinds it reads itself.
			!(k != kindUnit && bl.g.irValType(k) != nil)) {
			return no()
		}
		values[i] = v
		kinds[i] = k
	}
	// kindInvalid: sentinel — no typed spread tail supplied an element kind.
	if elem == kindInvalid {
		if len(items) == 0 {
			return rest, kindEmptyList, true, true
		}
		typed := kinds
		if len(deferred) > 0 {
			typed = nil
			for i, e := range kinds {
				if !slices.Contains(deferred, i) {
					typed = append(typed, e)
				}
			}
		}
		elem = bl.g.elementCandidate(typed)
	}
	for _, i := range deferred {
		v, k, _, ok := bl.lowerTypedOperand(items[i], elem)
		if !ok || k != elem {
			return no()
		}
		values[i], kinds[i] = v, k
	}
	for i := range values {
		var ok bool
		values[i], _, ok = bl.coerceEmpty(items[i], values[i], kinds[i], elem)
		if !ok {
			return no()
		}
	}
	k := bl.g.listKind(elem)
	if tail != nil {
		var ok bool
		rest, _, ok = bl.coerceEmpty(tail, rest, restKind, k)
		if !ok {
			return no()
		}
	}
	n := ir.NewMakeList(bl.g.irNodePos(at), bl.f.NewTemp(), values, rest)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: k})
	return n.Dst(), k, false, true
}

// irEmbeddedIn reports whether k widens into a retained `embeds` variant of
// want.
func irEmbeddedIn(want, k kind) bool {
	d, _, widens := embedsVariant(want, k)
	return widens && irRetainedEnumKind(d)
}

// coerceEmpty preserves an empty collection's VM value while Go discharges
// its element types through the native coercion routine. A value of an
// embedded type widens into its retained `embeds` variant, as coerce does.
func (bl *irScalarBuilder) coerceEmpty(at ast.Node, v ir.Temp, have, want kind) (ir.Temp, kind, bool) {
	if have == want {
		return v, have, true
	}
	if irEmbeddedIn(want, have) {
		d, variant, _ := embedsVariant(want, have)
		return bl.embedWiden(at, v, d, variant)
	}
	if bl.g.irErases(want, have) {
		// A concrete value passed where its interface is expected: the VM's
		// existential is the concrete value itself, as a struct literal's
		// interface-typed field holds it (irstruct.go), so the operand is
		// passed unchanged.
		return v, want, true
	}
	if bl.g.irErasesFields(want, have) {
		// A record literal whose interface-typed fields hold concrete
		// values: each field enters by erasure, which the VM does not
		// represent, so the record is passed unchanged.
		return v, want, true
	}
	if want.tag == tagSeq && want.comp != nil {
		// A source entering a declared `Iter<T>` position is viewed as the
		// sequence, as an `Iter` call views its source.
		if seq, ok := bl.seqView(at, v, have, seqElem(want)); ok {
			return seq, want, true
		}
		return v, have, false
	}
	list := have == kindEmptyList && want.tag == tagList && (irRetainedListKind(want) || irListTransportKind(want))
	mapValue := have == kindEmptyMap && want.tag == tagMap && irRetainedMapKind(want)
	vector := have == kindEmptyVector && want != kindEmptyVector && (irRetainedVectorKind(want) || bl.g.irVectorValueKind(want))
	set := have == kindEmptySet && want != kindEmptySet && irRetainedSetKind(want)
	if !list && !mapValue && !vector && !set {
		return v, have, false
	}
	n := ir.NewCopy(bl.g.irNodePos(at), bl.f.NewTemp(), v)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: want, copy: irCopyEmptyValue})
	return n.Dst(), want, true
}

func (bl *irScalarBuilder) lowerWant(at ast.Node, want kind) (ir.Temp, kind, bool, bool) {
	v, k, mobile, ok := bl.lowerTypedOperand(at, want)
	if !ok || k == want {
		return v, k, mobile, ok
	}
	have := k
	v, k, ok = bl.coerceEmpty(at, v, k, want)
	if !ok {
		irDeclineNote("a value of kind " + have.nomi() + " where " + want.nomi() + " is expected")
	}
	return v, k, true, ok
}

// lowerTypedOperand supplies branch storage's type without moving coercion
// ahead of the caller's effect-ordering and operand-forcing decisions.
func (bl *irScalarBuilder) lowerTypedOperand(at ast.Node, want kind) (ir.Temp, kind, bool, bool) {
	if x, witness := typeWitnessArg(want); witness {
		// A type name where a `Type<T>` is expected is the witness, not the
		// value a marker's name would otherwise be (ircontextvalue.go).
		if v, ok := bl.typeWitnessValue(at, x, want); ok {
			return v, want, true, true
		}
	}
	if v, k, mobile, ok := bl.preludeBareValue(at, want); ok {
		return v, k, mobile, true
	}
	if fa, isField := at.(*ast.FieldAccess); isField && want.tag == tagNamed && want.def != nil && want.def.genericOf != nil {
		// `leaf: Tree<String> = Tree.Leaf`: a payload-free variant of a user
		// generic enum names no type argument, so it is built at the
		// expected instance, as a bare `None` is.
		if d, v, ok := bl.genericVariant(fa, nil, want); ok && v.kind == "bare" {
			return bl.variantValue(fa, d, v, nil, true)
		}
	}
	if c, isCall := at.(*ast.Call); isCall && want.tag == tagNamed && want.def != nil && want.def.preludeOf != nil {
		if v, k, mobile, ok := bl.preludeCallWant(c, want); ok {
			bl.g.irTypeTemp(bl.f, v, k)
			return v, k, mobile, true
		}
	}
	if c, isCall := at.(*ast.Call); isCall {
		if _, owner, isChannel := channelElem(want); isChannel && owner == "Channel" {
			// `ch: Channel<Int> = Channel.unbuffered()`: the annotation is the
			// constructor's instantiation (channelCtorFromTarget), published
			// as ironce.go publishes a `once` initializer's.
			if bl.g.wantOf == nil {
				bl.g.wantOf = map[ast.Node]kind{}
			}
			prev, held := bl.g.wantOf[c]
			bl.g.wantOf[c] = want
			defer func() {
				if held {
					bl.g.wantOf[c] = prev
				} else {
					delete(bl.g.wantOf, c)
				}
			}()
		}
	}
	// kindInvalid: sentinel — no wanted kind passed; todo() then reads the checker's.
	if td, isTodo := at.(*ast.Todo); isTodo && want != kindInvalid {
		// `todo` takes the wanted kind; see irtodo.go.
		return bl.todo(td, want)
	}
	if d, isDbg := at.(*ast.Dbg); isDbg && !isNilNode(d.Expr) {
		// `_none: Maybe<Int> = dbg None`: `dbg` is transparent, so the
		// wanted type is its operand's.
		return bl.dbgWant(d, d.Expr, want)
	}
	if tl, isTuple := at.(*ast.TupleLit); isTuple && want.tag == tagTuple {
		return bl.tupleMakeWant(tl, want)
	}
	if ll, isList := at.(*ast.ListLit); isList && want.tag == tagList && want.comp != nil && len(want.comp.parts) == 1 &&
		want.comp.parts[0].tag == tagSeq && ll.TypeName == nil && len(ll.Items) != 0 {
		// `[xs, [1, 2]]` where a `List<Iter<Int>>` is expected: each element
		// enters the declared `Iter<T>`.
		return bl.listMakeOf(ll, ll.Items, nil, want.comp.parts[0])
	}
	if !bl.inTest && bl.recording == 0 {
		switch at.(type) {
		case *ast.If, *ast.Case:
			v, k, ok := bl.typedRegionValue(at, want)
			return v, k, true, ok
		}
	}
	v, k, mobile, ok := bl.lower(at)
	if ok && k != want && want.tag == tagSeq && want.comp != nil {
		// A source written where a declared `Iter<T>` is expected (a field,
		// a payload, an element) is viewed as the sequence there.
		if seq, viewed := bl.seqView(at, v, k, seqElem(want)); viewed {
			return seq, want, true, true
		}
	}
	return v, k, mobile, ok
}
