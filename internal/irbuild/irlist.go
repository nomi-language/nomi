package irbuild

import (
	"slices"

	"github.com/nomi-language/nomi/internal/analysis"
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
// (`Dynamic.as_list`), a local interface's existential (`List<Display>`),
// which the VM holds as the concrete values, and a function value
// (`List<(Int) -> Int>`).
// Debug, equality and patterns keep their narrower scalar-list checks.
func irListTransportKind(k kind) bool {
	return k.tag == tagList && k.comp != nil && len(k.comp.parts) == 1 &&
		(irRetainedLeafKind(k.comp.parts[0]) || irListTransportKind(k.comp.parts[0]) || irRetainedVectorKind(k.comp.parts[0]) || irRetainedSetKind(k.comp.parts[0]) || irRetainedRecordKind(k.comp.parts[0]) || isDecimalKind(k.comp.parts[0]) || irNominalElemKind(k.comp.parts[0]) || irPreludeElemKind(k.comp.parts[0]) || irLeafTupleKind(k.comp.parts[0]) || irRetainedTupleKind(k.comp.parts[0]) || irListMapElemKind(k.comp.parts[0]) || irDynamicKind(k.comp.parts[0]) || irByteValueKind(k.comp.parts[0]) || irStreamKind(k.comp.parts[0]) || irExistentialKind(k.comp.parts[0]) || irConcHandleKind(k.comp.parts[0]) || k.comp.parts[0] == kindUnit || (k.comp.parts[0].tag == tagFunc && irCallableValueKind(k.comp.parts[0])))
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
// `#(String, String)` pair, carried as a list element.
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
		// The checker types `[]` from where it sits (`return []` after a
		// `return [x]`, `break []` against a loop's `List<Int>` state, the
		// `[]` in `#(1, []) == #(1, [2])`). When it solved the element type,
		// the literal is that list, so every position compares it as one.
		if parts := bl.checkedLiteralParts(at, tagList); len(parts) == 1 && irFullyTyped(parts[0]) {
			want := bl.g.listKind(parts[0])
			bl.side(c.Dst(), irScalarSide{k: kindEmptyList})
			if v, k, ok := bl.coerceEmpty(at, c.Dst(), kindEmptyList, want); ok && k == want {
				return v, k, true, true
			}
		}
		return c.Dst(), kindEmptyList, true, true
	}
	// kindInvalid: sentinel — no annotation supplies the element kind.
	if want == kindInvalid && tail == nil {
		if parts := bl.literalParts(at, tagList); parts != nil {
			want = parts[0]
		}
	}
	values := make([]ir.Temp, len(items))
	kinds := make([]kind, len(items))
	elem := want
	rest := ir.NoTemp
	restKind := kindInvalid
	if tail != nil {
		v, k, _, ok := bl.lower(tail)
		if !ok {
			return no()
		}
		// A tail that is another Iter (`[x, ..maybe]`) is made a List.
		p, ok := bl.collectSpread(spreadPart{at: tail, v: v, k: k}, listSpreadJoin)
		if !ok {
			return no()
		}
		v, k = p.v, p.k
		// The tail's elements are any values with a stored type, as the
		// heads' are (a list of Iters, of functions, of structs).
		if !(irRetainedListKind(k) || irListTransportKind(k) || bl.g.irSpreadTailKind(k)) {
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
			// patterns) checks the element kinds it reads itself. A Unit
			// item is held as the Unit value its temporary carries.
			!(k != kindUnit && bl.g.irValType(k) != nil) && !(k == kindUnit && v != ir.NoTemp)) {
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
			irDeclineNote("a list element of kind " + kinds[i].nomi() + " where " + elem.nomi() + " is expected")
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

// irSpreadTailKind is a list whose element has a stored value type: the VM's
// list holds values of every kind, as listMakeOf's heads are admitted.
func (g *gen) irSpreadTailKind(k kind) bool {
	if k.tag != tagList || k.comp == nil || len(k.comp.parts) != 1 {
		return false
	}
	elem := k.comp.parts[0]
	return elem == kindUnit || g.irValType(elem) != nil
}

// irEmbeddedIn reports whether k widens into a retained `embeds` variant of
// want.
func irEmbeddedIn(want, k kind) bool {
	d, _, widens := embedsVariant(want, k)
	return widens && irRetainedEnumKind(d)
}

// irHoldsEmbeds reports whether k is an enum with an `embeds` variant, or
// holds one in a tuple, record or collection: a literal of k may hold a
// value of an embedded type that must be widened in place.
func irHoldsEmbeds(k kind) bool {
	if k.tag == tagNamed && k.def != nil && k.def.isEnum {
		for i := range k.def.variants {
			if k.def.variants[i].embeds != nil {
				return true
			}
		}
	}
	return irNestedEmbeds(k)
}

// irNestedEmbeds reports whether k holds an enum with an `embeds` variant
// below its top: `#(Int, Shape)`, `List<Shape>`, `Map<Int, Shape>`.
func irNestedEmbeds(k kind) bool {
	if elem, vector := vectorElem(k); vector && k != kindEmptyVector {
		return irHoldsEmbeds(elem)
	}
	if elem, set := setElem(k); set && k != kindEmptySet {
		return irHoldsEmbeds(elem)
	}
	switch k.tag {
	case tagTuple, tagAnonStruct, tagList, tagMap:
		if k.comp == nil {
			return false
		}
		for _, p := range k.comp.parts {
			if irHoldsEmbeds(p) {
				return true
			}
		}
	}
	return false
}

// literalParts is the checker's type of an unannotated collection literal,
// projected, when one of its type arguments holds, below its top, an
// embedded type's enum (`[#(1, c), #(2, shape)]` is a `List<#(Int, Shape)>`)
// or a collection (`[#(1, []), #(2, [3])]` is a `List<#(Int, List<Int>)>`), or
// is a fully typed collection (`[%[]]` is a `List<Set<Int>>` where a later
// use fixes the set's element): its items are then lowered against those
// parts, so each embedded value is widened, and each empty collection
// typed, where it sits. It answers nil otherwise; a top-level join of
// embedded values (`[c, shape]`) is found from the lowered items
// (irEmbedsJoin, literalItemsCoerced).
//
// A type argument the checker left unsolved (`io.inspect([%[]])`, where
// nothing fixes the set's element) is one no value has, filled as a generic
// call's is (spec, "Determined type arguments"), so the empty literal in
// the item is typed at the filled part.
func (bl *irScalarBuilder) literalParts(at ast.Node, of tag) []kind {
	ty := bl.g.checkedExprType(at)
	if ty != nil && irUnsolvedType(ty) {
		ty = irFillHolesByPosition(ty, false)
	}
	parts := bl.literalPartsOf(ty, of)
	for _, p := range parts {
		if irNestedEmbeds(p) || ((irIsCollection(p) || irNestedCollection(p)) && irFullyTyped(p)) || irHoldsExistential(p) {
			return parts
		}
	}
	return nil
}

// irTypeArgs is k's parts or type arguments: a tuple's or record's parts, a
// list's element, a map's key and value, or a generic instance's arguments.
func irTypeArgs(k kind) []kind {
	switch k.tag {
	case tagTuple, tagAnonStruct, tagList, tagMap, tagSeq:
		if k.comp != nil {
			return k.comp.parts
		}
	case tagNamed:
		if d := k.def; d != nil {
			switch {
			case d.preludeArgs != nil:
				return d.preludeArgs
			case d.genStructArgs != nil:
				return d.genStructArgs
			case d.genHostArgs != nil:
				return d.genHostArgs
			case d.genericArgs != nil:
				return d.genericArgs
			}
		}
	}
	return nil
}

// irIsCollection reports whether k is a list, map, set or vector, typed or
// not: a type whose literal may be written empty.
func irIsCollection(k kind) bool {
	switch k.tag {
	case tagList, tagMap, tagEmptyList, tagEmptyMap, tagEmptySet, tagEmptyVector:
		return true
	}
	_, set := setElem(k)
	_, vector := vectorElem(k)
	return set || vector
}

// irNestedCollection reports whether k holds a collection below its top:
// `#(Int, List<Int>)`, `List<List<Int>>`, `Maybe<Set<Int>>`. An item of such
// an element type may hold an empty collection literal (`#(1, [])`) whose own
// kind says nothing of its element type.
func irNestedCollection(k kind) bool {
	for _, p := range irTypeArgs(k) {
		if irIsCollection(p) || irNestedCollection(p) {
			return true
		}
	}
	return false
}

// irTypedWithin reports whether a literal expected to hold items of kind k
// lowers each item against k: k is or holds an embedded type's enum, which
// an item's value widens into where it sits, or k is or holds a collection,
// which an empty collection literal in an item (`[]`, `#(1, [])`) is typed
// as where it sits. A generic instance holding such an enum in its type
// arguments (`Maybe<Shape>`, `Box<Shape>`) counts too: an item that
// constructs one (`[Some(c)]`) is built at k, so its embedded value widens.
func irTypedWithin(k kind) bool {
	return irHoldsEmbeds(k) || irArgsHoldEmbeds(k) || ((irIsCollection(k) || irNestedCollection(k)) && irFullyTyped(k))
}

// irArgsHoldEmbeds reports whether a type argument of k, at any depth, is
// or holds an enum with an `embeds` variant: Maybe<Shape>, Box<#(Int, Shape)>.
func irArgsHoldEmbeds(k kind) bool {
	for _, p := range irTypeArgs(k) {
		if irHoldsEmbeds(p) || irArgsHoldEmbeds(p) {
			return true
		}
	}
	return false
}

// irFullyTyped reports whether k names a type everywhere: no part of it is
// an untyped literal's kind or has no representation.
func irFullyTyped(k kind) bool {
	if k.tag == tagInvalid || untypedLiteral(k) {
		return false
	}
	for _, p := range irTypeArgs(k) {
		if !irFullyTyped(p) {
			return false
		}
	}
	return true
}

// checkedLiteralParts is the checker's type of collection literal at,
// projected, as its type arguments: the element of a list (of tagList), set
// (tagEmptySet) or vector (tagEmptyVector), or a map's key and value
// (tagMap). It answers nil when the checked type is not that collection.
func (bl *irScalarBuilder) checkedLiteralParts(at ast.Node, of tag) []kind {
	return bl.literalPartsOf(bl.g.checkedExprType(at), of)
}

// literalPartsOf is checkedLiteralParts of the checked type ty.
func (bl *irScalarBuilder) literalPartsOf(ty analysis.Type, of tag) []kind {
	k := bl.g.project(ty)
	switch {
	case of == tagList && k.tag == tagList && k.comp != nil && len(k.comp.parts) == 1:
		return k.comp.parts
	case of == tagMap && k.tag == tagMap && k != kindEmptyMap && k.comp != nil && len(k.comp.parts) == 2:
		return k.comp.parts
	case of == tagEmptySet:
		if elem, set := setElem(k); set && k != kindEmptySet {
			return []kind{elem}
		}
	case of == tagEmptyVector:
		if elem, vector := vectorElem(k); vector && k != kindEmptyVector {
			return []kind{elem}
		}
	}
	return nil
}

// literalItemsCoerced is part j of collection literal at's checked type
// (checkedLiteralParts), with each lowered item coerced to it in place
// (coerceEmpty). It is the element kind of a set or vector literal, or a map
// literal's key or value kind, whose lowered items do not join because one
// is still an empty collection (`%[[], [1]]`, `%{1 => [], 2 => [1]}`), as a
// list literal's items take theirs. ok is false when the checked type is not
// that collection, is itself an untyped literal's, or an item does not
// coerce; values and kinds are then left as they were.
func (bl *irScalarBuilder) literalItemsCoerced(at ast.Node, of tag, j int, items []ast.Node, values []ir.Temp, kinds []kind) (kind, bool) {
	parts := bl.checkedLiteralParts(at, of)
	if j >= len(parts) || untypedLiteral(parts[j]) {
		return kindInvalid, false
	}
	want := parts[j]
	coerced := make([]ir.Temp, len(values))
	for i := range values {
		v, k, ok := bl.coerceEmpty(items[i], values[i], kinds[i], want)
		if !ok || k != want {
			return kindInvalid, false
		}
		coerced[i] = v
	}
	copy(values, coerced)
	for i := range kinds {
		kinds[i] = want
	}
	return want, true
}

// irEmbedsJoin is the kind an unannotated literal's items join to, as the
// checker's embedsJoin gives it: the one item kind every other item either
// is or widens into through a retained `embeds` variant (`[circle, shape]`
// is a `List<Shape>`). ok is false when no item kind takes all the others.
func irEmbedsJoin(kinds []kind) (kind, bool) {
	for _, cand := range kinds {
		all := true
		for _, k := range kinds {
			if k != cand && !irEmbeddedIn(cand, k) {
				all = false
				break
			}
		}
		if all {
			return cand, true
		}
	}
	return kindInvalid, false
}

// widenEmbedded widens each lowered item whose kind is not elem into elem's
// `embeds` variant. The items are already lowered, left to right, and a
// widening has no effect, so this reorders nothing.
func (bl *irScalarBuilder) widenEmbedded(items []ast.Node, values []ir.Temp, kinds []kind, elem kind) bool {
	for i := range values {
		if kinds[i] == elem {
			continue
		}
		d, variant, widens := embedsVariant(elem, kinds[i])
		if !widens {
			return false
		}
		v, _, ok := bl.embedWiden(items[i], values[i], d, variant)
		if !ok {
			return false
		}
		values[i], kinds[i] = v, elem
	}
	return true
}

// coerceEmpty preserves an empty collection's VM value while Go discharges
// its element types through the native coercion routine. A value of an
// embedded type widens into its retained `embeds` variant, as coerce does.
func (bl *irScalarBuilder) coerceEmpty(at ast.Node, v ir.Temp, have, want kind) (ir.Temp, kind, bool) {
	if have == want {
		return v, have, true
	}
	if v, ok := bl.neverAs(at, v, have, want); ok {
		return v, want, true
	}
	if irEmbeddedIn(want, have) {
		d, variant, _ := embedsVariant(want, have)
		return bl.embedWiden(at, v, d, variant)
	}
	if bl.g.irBoxedErasure(want, have) {
		// A value whose runtime type does not name its impl enters boxed
		// with the type it has here (irboxed.go).
		return bl.eraseBoxed(at, v, want, have), want, true
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
	if want.tag == tagFunc && have.tag == tagFunc {
		// A function whose parameters are wider than the expected
		// function type's (irfuncwiden.go).
		if adapted, ok := bl.funcWiden(at, v, have, want); ok {
			return adapted, want, true
		}
		return v, have, false
	}
	if want.tag == tagSeq && want.comp != nil {
		// A source entering a declared `Iter<T>` position is viewed as the
		// sequence, as an `Iter` call views its source.
		if seq, ok := bl.seqView(at, v, have, seqElem(want)); ok {
			return seq, want, true
		}
		return v, have, false
	}
	if !bl.g.emptyRetypes(have, want) {
		// `w: Maybe<Shape> = m` for `m: Maybe<Circle>`: a value built at
		// the narrower type is rebuilt at the wider one
		// (irembedsrebuild.go).
		if rebuilt, ok := bl.rebuildWidened(at, v, have, want); ok {
			return rebuilt, want, true
		}
		return v, have, false
	}
	n := ir.NewCopy(bl.g.irNodePos(at), bl.f.NewTemp(), v)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: want, copy: irCopyEmptyValue})
	return n.Dst(), want, true
}

// erasesUnboxed reports whether a value of kind have is, unchanged, a value
// of the existential want: it erases into want (irErases) and enters it
// without a box (irBoxedErasure), so a source of such values is viewed as
// a source of want's.
func (bl *irScalarBuilder) erasesUnboxed(want, have kind) bool {
	return irExistentialKind(want) && bl.g.irErases(want, have) && !bl.g.irBoxedErasure(want, have)
}

// emptyRetypes reports whether a value of kind have is a value of kind want
// once each empty collection in it takes want's type there: have is an
// untyped empty list, map, set or vector and want that collection, or both
// are the same tuple, record or collection shape whose parts each retype
// (`#(Int, [])` to `#(Int, List<Int>)`, `List<List<?>>` to
// `List<List<Int>>`). An empty collection's VM value holds no element type,
// so neither does a structural value around one, and the value is the same.
// A declared generic instance (`Box<T>`, `Maybe<T>`) is not looked inside:
// its value is built at its type arguments.
func (g *gen) emptyRetypes(have, want kind) bool {
	if have == want {
		return true
	}
	switch have {
	case kindEmptyList:
		return want.tag == tagList && irRetainedValueKind(want)
	case kindEmptyMap:
		return want.tag == tagMap && want != kindEmptyMap && irRetainedMapKind(want)
	case kindEmptyVector:
		return want != kindEmptyVector && (irRetainedVectorKind(want) || g.irVectorValueKind(want))
	case kindEmptySet:
		return want != kindEmptySet && irRetainedSetKind(want)
	}
	if he, set := setElem(have); set {
		we, wset := setElem(want)
		return wset && want != kindEmptySet && g.emptyRetypes(he, we) && irRetainedSetKind(want)
	}
	if he, vector := vectorElem(have); vector {
		we, wvector := vectorElem(want)
		return wvector && want != kindEmptyVector && g.emptyRetypes(he, we) && (irRetainedVectorKind(want) || g.irVectorValueKind(want))
	}
	switch have.tag {
	case tagTuple, tagAnonStruct, tagList, tagMap:
	default:
		return false
	}
	if want.tag != have.tag || have.comp == nil || want.comp == nil || len(have.comp.parts) != len(want.comp.parts) || !slices.Equal(have.comp.names, want.comp.names) {
		return false
	}
	for i := range have.comp.parts {
		if !g.emptyRetypes(have.comp.parts[i], want.comp.parts[i]) {
			return false
		}
	}
	return true
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
	at = bl.g.holeRead(at)
	// The routes below that try a shape and fall through: one that declined
	// an operand abandons at, as lowerExact abandons a node.
	defer irLowerFrame()()
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
	if c, isCall := at.(*ast.Call); isCall {
		if v, k, mobile, ok := bl.genericVariantCallWant(c, want); ok {
			return v, k, mobile, true
		}
	}
	if sl, isLit := at.(*ast.StructLit); isLit {
		if v, k, mobile, ok := bl.genericLitWant(sl, want); ok {
			return v, k, mobile, true
		}
	}
	if c, isCall := at.(*ast.Call); isCall && want.tag == tagNamed && want.def != nil && want.def.preludeOf != nil {
		if v, k, mobile, ok := bl.preludeCallWant(c, want); ok {
			bl.g.irTypeTemp(bl.f, v, k)
			return v, k, mobile, true
		}
	}
	if irLowerAbandoned() {
		return ir.NoTemp, kindInvalid, false, false
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
	// kindInvalid: sentinel — no wanted kind passed; the block's tail then has its own.
	if block, isBlock := at.(*ast.Block); isBlock && want != kindInvalid {
		return bl.blockValue(block, want)
	}
	// kindInvalid: sentinel — no wanted kind passed; todo() then reads the checker's.
	if td, isTodo := at.(*ast.Todo); isTodo && want != kindInvalid {
		// `todo` takes the wanted kind; see irtodo.go.
		return bl.todo(td, want)
	}
	// kindInvalid: sentinel — no wanted kind passed; unreachable() then reads the checker's.
	if ur, isUnreachable := at.(*ast.Unreachable); isUnreachable && want != kindInvalid {
		return bl.unreachable(ur, want)
	}
	if d, isDbg := at.(*ast.Dbg); isDbg && !isNilNode(d.Expr) {
		// `_none: Maybe<Int> = dbg None`: `dbg` is transparent, so the
		// wanted type is its operand's.
		return bl.dbgWant(d, d.Expr, want)
	}
	if tl, isTuple := at.(*ast.TupleLit); isTuple && want.tag == tagTuple {
		return bl.tupleMakeWant(tl, want)
	}
	if sl, isRecord := at.(*ast.StructLit); isRecord && sl.TypeName == nil && sl.Spread == nil && want.tag == tagAnonStruct && bl.g.targetStruct(sl) == nil {
		// `#{a: []}` where a `#{a: List<Int>}` is expected: each field's value
		// is lowered against its field type, as a tuple's items are.
		return bl.recordMakeOf(sl, want)
	}
	if ll, isList := at.(*ast.ListLit); isList && want.tag == tagList && want.comp != nil && len(want.comp.parts) == 1 &&
		(want.comp.parts[0].tag == tagSeq || want.comp.parts[0].tag == tagFunc || irExistentialKind(want.comp.parts[0]) || irTypedWithin(want.comp.parts[0])) && ll.TypeName == nil && len(ll.Items) != 0 {
		// `[xs, [1, 2]]` where a `List<Iter<Int>>` is expected: each element
		// enters the declared `Iter<T>`. `[cnt]` where a
		// `List<(List<Int>) -> Int>` is: each function value enters the
		// declared function type, through an adapter when its parameters are
		// wider (irfuncwiden.go). `[c]` where a `List<Shape>` is, or
		// `[#(1, c)]` where a `List<#(Int, Shape)>` is: each embedded value
		// widens where it sits. `[[]]` where a `List<List<Int>>` is: each
		// empty collection is typed where it sits.
		return bl.listMakeOf(ll, ll.Items, nil, want.comp.parts[0])
	}
	// `%[c]`, `#[c]` or `%{1 => c}` where a collection holding an embedded
	// type's enum is expected: each embedded value widens where it sits.
	// `%[#(1, [])]` where a `Set<#(Int, List<Int>)>` is: each empty
	// collection is typed where it sits.
	if sl, isSet := at.(*ast.SetLit); isSet && len(sl.Items) != 0 {
		if elem, set := setElem(want); set && want != kindEmptySet && irTypedWithin(elem) {
			return bl.setMakeOf(sl, elem)
		}
	}
	if vl, isVector := at.(*ast.VectorLit); isVector && len(vl.Items) != 0 {
		// `#[[1]]` where a `Vector<Iter<Int>>` is expected: each element
		// enters the declared `Iter<T>`, as a list literal's does.
		if elem, vector := vectorElem(want); vector && want != kindEmptyVector && (irTypedWithin(elem) || elem.tag == tagSeq || irExistentialKind(elem)) {
			return bl.vectorMakeOf(vl, elem)
		}
	}
	if ml, isMap := at.(*ast.MapLit); isMap && ml.TypeName == nil && len(ml.Entries) != 0 && want.tag == tagMap && want != kindEmptyMap && want.comp != nil && len(want.comp.parts) == 2 &&
		(irTypedWithin(want.comp.parts[0]) || irTypedWithin(want.comp.parts[1])) {
		return bl.mapMakeOf(ml, want.comp.parts)
	}
	if !bl.inTest && bl.recording == 0 {
		switch at.(type) {
		case *ast.If, *ast.Case:
			v, k, ok := bl.typedRegionValue(at, want)
			return v, k, true, ok
		}
	}
	if lam, isLambda := at.(*ast.Lambda); isLambda && want.tag == tagFunc && want.comp != nil &&
		(funcResult(want).tag == tagSeq || irExistentialKind(funcResult(want))) {
		// `Maybe.map(m, fn _ -> [20])` where the checker solved the callback's
		// result as `Iter<Int>`: the lambda answers that sequence
		// (lambdaFunction), so its function value is the wanted one. A
		// lambda whose result is an interface's existential answers it.
		prevFor, prev := bl.g.lambdaWantFor, bl.g.lambdaWant
		bl.g.lambdaWantFor, bl.g.lambdaWant = lam, funcResult(want)
		defer func() { bl.g.lambdaWantFor, bl.g.lambdaWant = prevFor, prev }()
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
