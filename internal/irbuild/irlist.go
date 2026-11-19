package irbuild

import (
	"fmt"
	"slices"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// irRetainedListKind admits lists whose leaves have scalar Debug semantics.
func irRetainedListKind(k kind) bool {
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
		(irRetainedLeafKind(k.comp.parts[0]) || irListTransportKind(k.comp.parts[0]) || irRetainedVectorKind(k.comp.parts[0]) || irRetainedSetKind(k.comp.parts[0]) || irRetainedRecordKind(k.comp.parts[0]) || isDecimalKind(k.comp.parts[0]) || irNominalElemKind(k.comp.parts[0]) || irPreludeElemKind(k.comp.parts[0]) || irLeafTupleKind(k.comp.parts[0]) || irRetainedTupleKind(k.comp.parts[0]) || irListMapElemKind(k.comp.parts[0]) || irDynamicKind(k.comp.parts[0]) || irByteValueKind(k.comp.parts[0]) || irStreamKind(k.comp.parts[0]) || irHeldKind(k.comp.parts[0]) || irConcHandleKind(k.comp.parts[0]) || k.comp.parts[0] == kindUnit || (k.comp.parts[0].tag == tagFunc && irCallableValueKind(k.comp.parts[0])))
}

// irHeldListKind is a List the VM holds, whatever its element: a retained
// or transport list, or a List of any value the callable domain holds
// (irRetainedValueKind's List arm), such as a `List<Iter<Int>>`. A stage
// that builds a list from elements it only moves (a chunk, a flattened
// list) asks this; Debug, equality and patterns keep their own checks.
func irHeldListKind(k kind) bool {
	return k.tag == tagList && irRetainedValueKind(k)
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
	return k.tag == tagMap && irRetainedMapKind(k) && k.comp != nil &&
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
// nominal element kind from the annotation, so embedded values widen into it.
func irAnnotatedNominalList(t *ast.ListLit, want kind) bool {
	return t.TypeName == nil && len(t.Items) != 0 && want.tag == tagList && want.comp != nil &&
		len(want.comp.parts) == 1 && (irNominalElemKind(want.comp.parts[0]) || irPreludeElemKind(want.comp.parts[0]) || irStdStructExistential(want.comp.parts[0]) || irHeldKind(want.comp.parts[0]) || want.comp.parts[0].tag == tagSeq)
}

func (bl *irScalarBuilder) listMakeOf(at ast.Node, items []ast.Node, tail ast.Node, want kind) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if len(items) == 0 && tail == nil {
		return bl.emptyLiteral(at, listSpreadJoin, kindInvalid)
	}
	// kindInvalid: sentinel — no annotation supplies the element kind.
	if want == kindInvalid && tail == nil {
		if parts := bl.literalParts(at, listSpreadJoin); parts != nil {
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
		p, ok := bl.collectSpread(at, spreadPart{at: tail, v: v, k: k}, listSpreadJoin)
		if !ok {
			return no()
		}
		v, k = p.v, p.k
		// The tail's elements are any values with a stored type, as the
		// heads' are (a list of Iters, of functions, of structs).
		// A List made of a lowered tail's elements, each held, so each has
		// a stored type.
		if !bl.g.irSpreadTailKind(k) {
			panic("irbuild: a list spread tail of " + k.nomi() + ", whose elements have no stored type")
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
			!isDecimalKind(k) && !irPreludeElemKind(k) && !irListMapElemKind(k) && !irByteElemKind(k) && !(irHeldKind(k)) &&
			// kindInvalid: sentinel — no annotation supplies an element to erase into.
			!(want != kindInvalid && bl.g.irErases(want, k, nil)) &&
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
		v, k, _, ok := bl.lowerWant(items[i], elem)
		if !ok {
			return no()
		}
		values[i], kinds[i] = v, k
	}
	for i := range values {
		var ok bool
		values[i], _, ok = bl.convertTo(items[i], values[i], kinds[i], elem)
		if !ok {
			bl.g.dc.note("a list element of kind " + kinds[i].nomi() + " where " + elem.nomi() + " is expected")
			return no()
		}
	}
	k := bl.g.listKind(elem)
	if tail != nil {
		var ok bool
		rest, _, ok = bl.convertTo(tail, rest, restKind, k)
		if !ok {
			return no()
		}
	}
	n := ir.NewMakeList(bl.g.irNodePos(at), bl.f.NewTemp(), values, rest)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: k})
	return n.Dst(), k, false, true
}

// emptyLiteral builds the empty literal at (`[]`, `Set[]`, `Vector[]`, `{}`)
// of shape's collection, at the kind the checker gave it (emptyOf). The
// checker types an empty literal from where it sits (`return []` after a
// `return [x]`, `break []` against a loop's `List<Int>` state, the `[]` in
// `(1, []) == (1, [2])`), and closes an element type nothing determines
// (internal/analysis's closed_vars.go), so the record says what the literal
// holds.
//
// want, when it is a typed collection of shape's, is the kind of the
// position the literal is lowered for. It types a literal the checker's
// record does not: a copy the builder made of a literal (`Query[]` over its
// list, `Json.Obj{}`'s anonymous map), which has no record, and a generic
// struct's field default (`items: List<T> = []`), recorded in terms of the
// struct's `T` and built for an instance.
func (bl *irScalarBuilder) emptyLiteral(at ast.Node, shape spreadJoin, want kind) (ir.Temp, kind, bool, bool) {
	k := bl.g.checkedKind(at)
	if !shape.typed(k) || !irFullyTyped(k) {
		k = want
	}
	if !shape.typed(k) || !irFullyTyped(k) {
		// The checker types every empty literal it accepts, closing an
		// element nothing determines, and a copy the builder made is
		// lowered for a position of its collection.
		line, col := nodePos(at)
		panic(fmt.Sprintf("irbuild: an empty literal at %s:%d:%d that neither its record (%s) nor its position (%s) types",
			bl.g.nomiPath, line, col, bl.g.checkedKind(at).nomi(), want.nomi()))
	}
	return bl.emptyOf(at, k)
}

// emptyLiteralShape reports whether n is an empty collection literal
// emptyLiteral builds, and of which collection: `[]`, `Set[]`, `Vector[]`
// or `{}`, with no name or a shape head, as the checker decided the
// bracket's collection (ast.ListLit.Kind).
func emptyLiteralShape(n ast.Node) (spreadJoin, bool) {
	switch t := n.(type) {
	case *ast.ListLit:
		if len(t.Items) != 0 || len(t.Spreads) != 0 || (t.TypeName != nil && !t.ShapeHead) {
			return spreadJoin{}, false
		}
		switch t.Kind {
		case ast.SeqVector:
			return vectorSpreadJoin, true
		case ast.SeqSet:
			return setSpreadJoin, true
		}
		return listSpreadJoin, true
	case *ast.MapLit:
		if len(t.Entries) != 0 || len(t.Spreads) != 0 || t.TypeName != nil {
			return spreadJoin{}, false
		}
		return mapSpreadJoin, true
	}
	return spreadJoin{}, false
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
// below its top: `(Int, Shape)`, `List<Shape>`, `Map<Int, Shape>`.
func irNestedEmbeds(k kind) bool {
	if elem, vector := vectorElem(k); vector {
		return irHoldsEmbeds(elem)
	}
	if elem, set := setElem(k); set {
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
// embedded type's enum (`[(1, c), (2, shape)]` is a `List<(Int, Shape)>`)
// or a collection (`[(1, []), (2, [3])]` is a `List<(Int, List<Int>)>`), or
// is a fully typed collection (`[[]]` is a `List<Set<Int>>` where a later
// use fixes the set's element): its items are then lowered against those
// parts, so each embedded value is widened, and each empty collection
// typed, where it sits. It answers nil otherwise; a top-level join of
// embedded values (`[c, shape]`) is found from the lowered items
// (irEmbedsJoin, literalItemsCoerced).
//
// A type argument inference left open (`io.inspect([[]])`, where nothing
// fixes the set's element) is one no value has, and the empty literal in
// the item is typed at the type the checker closed it as.
func (bl *irScalarBuilder) literalParts(at ast.Node, shape spreadJoin) []kind {
	parts := bl.literalPartsOf(bl.g.checkedExprType(at), shape)
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
	case tagList, tagMap:
		return true
	}
	_, set := setElem(k)
	_, vector := vectorElem(k)
	return set || vector
}

// irNestedCollection reports whether k holds a collection below its top:
// `(Int, List<Int>)`, `List<List<Int>>`, `Maybe<Set<Int>>`. An item of such
// an element type may hold an empty collection literal (`(1, [])`) whose own
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
// which an empty collection literal in an item (`[]`, `(1, [])`) is typed
// as where it sits. A generic instance holding such an enum in its type
// arguments (`Maybe<Shape>`, `Box<Shape>`) counts too: an item that
// constructs one (`[Some(c)]`) is built at k, so its embedded value widens.
func irTypedWithin(k kind) bool {
	return irHoldsEmbeds(k) || irArgsHoldEmbeds(k) || ((irIsCollection(k) || irNestedCollection(k)) && irFullyTyped(k))
}

// irArgsHoldEmbeds reports whether a type argument of k, at any depth, is
// or holds an enum with an `embeds` variant: Maybe<Shape>, Box<(Int, Shape)>.
func irArgsHoldEmbeds(k kind) bool {
	for _, p := range irTypeArgs(k) {
		if irHoldsEmbeds(p) || irArgsHoldEmbeds(p) {
			return true
		}
	}
	return false
}

// irFullyTyped reports whether k names a type everywhere: no part of it has
// no representation.
func irFullyTyped(k kind) bool {
	if k.tag == tagInvalid {
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
// projected, as its type arguments (irTypeArgs): the element of a list, set
// or vector, or a map's key and value. It answers nil when the checked type
// is not a collection of shape's.
func (bl *irScalarBuilder) checkedLiteralParts(at ast.Node, shape spreadJoin) []kind {
	return bl.literalPartsOf(bl.g.checkedExprType(at), shape)
}

// literalPartsOf is checkedLiteralParts of the checked type ty.
func (bl *irScalarBuilder) literalPartsOf(ty analysis.Type, shape spreadJoin) []kind {
	k := bl.g.project(ty)
	if !shape.typed(k) {
		return nil
	}
	return irTypeArgs(k)
}

// literalItemsCoerced is part j of collection literal at's checked type
// (checkedLiteralParts), with each lowered item coerced to it in place
// (convertTo). It is the element kind of a set or vector literal, or a map
// literal's key or value kind, whose lowered items do not join to one of
// their kinds, as a list literal's items take theirs. ok is false when the
// checked type is not that collection or an item does not coerce; values
// and kinds are then left as they were.
func (bl *irScalarBuilder) literalItemsCoerced(at ast.Node, shape spreadJoin, j int, items []ast.Node, values []ir.Temp, kinds []kind) (kind, bool) {
	parts := bl.checkedLiteralParts(at, shape)
	if j >= len(parts) {
		return kindInvalid, false
	}
	want := parts[j]
	coerced := make([]ir.Temp, len(values))
	for i := range values {
		v, _, ok := bl.convertTo(items[i], values[i], kinds[i], want)
		if !ok {
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

// convertTo converts v, a value of kind have, to the representation a
// position of kind want holds, where the two differ: a value of Infallible
// retyped (neverAs), an embedded value widened into its `embeds` variant, a
// value boxed into an interface or a conjunction, a record whose fields
// enter by erasure, a collection or function relabelled to wider type
// arguments, a function adapted to wider parameters, a source viewed as an
// `Iter`, and a value rebuilt at wider type arguments. Where have is want it
// answers v unchanged.
//
// When it answers ok, the kind it answers is want: a caller tests ok alone.
func (bl *irScalarBuilder) convertTo(at ast.Node, v ir.Temp, have, want kind) (ir.Temp, kind, bool) {
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
	if irConjKind(want) {
		// A value entering an interface conjunction (conjunction.go).
		if out, how := bl.intoConjunction(at, v, want, have, bl.g.recordedCoercion(at, want, have)); how != irNotCoerced {
			return out, want, true
		}
	}
	if irExistentialKind(want) {
		// A concrete value passed where its interface is expected: the
		// expression's recorded coercion, or one inside a conversion the
		// checker recorded as a whole (a function's result through an
		// adapter, an element's), by the impl the record names. The VM's
		// existential is the concrete value itself, boxed where its runtime
		// type does not name its impl (irboxed.go).
		if out, how := bl.intoExistential(at, v, want, have, bl.g.recordedCoercion(at, want, have)); how != irNotCoerced {
			return out, want, true
		}
	}
	if bl.g.irErasesFields(want, have) {
		// A record literal whose interface-typed fields hold concrete
		// values: each field enters by erasure, which the VM does not
		// represent, so the record is passed unchanged.
		return v, want, true
	}
	if !irExistentialKind(want) {
		// A collection or function at wider type arguments its value
		// already has (irrelabel.go), as the checker's record of the
		// conversion decides.
		relabel, agrees := bl.relabelsAt(at, have, want)
		if !agrees {
			return v, have, false
		}
		if relabel {
			return bl.relabel(at, v, want), want, true
		}
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
	// `w: Maybe<Shape> = m` for `m: Maybe<Circle>`: a value built at the
	// narrower type is rebuilt at the wider one (irembedsrebuild.go).
	if out, rebuilt := bl.rebuildWidened(at, v, have, want); rebuilt {
		return out, want, true
	}
	return v, have, false
}

// erasesUnboxed reports whether a value of kind have is, unchanged, a value
// of the existential want: it erases into want (irErases) and enters it
// without a box (irBoxPlanFor), so a source of such values is viewed as a
// source of want's.
func (bl *irScalarBuilder) erasesUnboxed(want, have kind) bool {
	return irExistentialKind(want) && bl.g.irErases(want, have, nil) && bl.g.irBoxPlanFor(want, have, nil, nil) == nil
}

// lowerWant lowers at as a value of kind want, coercing what it lowers to
// (convertTo) when that differs. When ok, the kind is want: every route
// returns either a lowering of kind want or one convertTo turned into it,
// so a caller compares nothing.
func (bl *irScalarBuilder) lowerWant(at ast.Node, want kind) (ir.Temp, kind, bool, bool) {
	v, k, mobile, ok := bl.lowerTypedOperand(at, want)
	if !ok || k == want {
		return v, k, mobile, ok
	}
	have := k
	v, k, ok = bl.convertTo(at, v, k, want)
	if !ok {
		bl.g.dc.note("a value of kind " + have.nomi() + " where " + want.nomi() + " is expected")
	}
	return v, k, true, ok
}

// lowerTypedOperand supplies branch storage's type without moving coercion
// ahead of the caller's effect-ordering and operand-forcing decisions.
func (bl *irScalarBuilder) lowerTypedOperand(at ast.Node, want kind) (ir.Temp, kind, bool, bool) {
	at = bl.g.holeRead(at)
	// Parentheses are grammar: `describe((if up then c else s))` and
	// `(if up then c else s) |> describe()` want the parameter's type of the
	// `if` inside them, as the bare `if` does.
	for {
		g, grouped := at.(*ast.GroupedExpr)
		if !grouped {
			break
		}
		at = bl.g.holeRead(g.Expr)
	}
	// The routes below that try a shape and fall through: one that declined
	// an operand abandons at, as lowerExact abandons a node.
	defer bl.g.dc.frame()()
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
	if bl.g.dc.abandoned() {
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
	if td, isTodo := bl.g.asTodo(at).(*ast.Todo); isTodo && want != kindInvalid {
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
		// `{a: []}` where a `{a: List<Int>}` is expected: each field's value
		// is lowered against its field type, as a tuple's items are.
		return bl.recordMakeOf(sl, want)
	}
	if shape, empty := emptyLiteralShape(at); empty && shape.typed(want) {
		// An empty literal is built at its recorded kind, or at want where
		// it has no record of its own (emptyLiteral).
		return bl.emptyLiteral(at, shape, want)
	}
	if ll, isList := at.(*ast.ListLit); isList && ll.Kind == ast.SeqList && len(ll.Spreads) == 0 && want.tag == tagList && want.comp != nil && len(want.comp.parts) == 1 &&
		(want.comp.parts[0].tag == tagSeq || want.comp.parts[0].tag == tagFunc || irHeldKind(want.comp.parts[0]) || irTypedWithin(want.comp.parts[0])) && (ll.TypeName == nil || ll.ShapeHead) && len(ll.Items) != 0 {
		// `[xs, [1, 2]]` where a `List<Iter<Int>>` is expected: each element
		// enters the declared `Iter<T>`. `[cnt]` where a
		// `List<(List<Int>) -> Int>` is: each function value enters the
		// declared function type, through an adapter when its parameters are
		// wider (irfuncwiden.go). `[c]` where a `List<Shape>` is, or
		// `[(1, c)]` where a `List<(Int, Shape)>` is: each embedded value
		// widens where it sits. `[[]]` where a `List<List<Int>>` is: each
		// empty collection is typed where it sits.
		return bl.listMakeOf(ll, ll.Items, nil, want.comp.parts[0])
	}
	// `[c]` where a Set or a Vector, or `{1 => c}` where a collection holding an embedded
	// type's enum is expected: each embedded value widens where it sits.
	// `[(1, [])]` where a `Set<(Int, List<Int>)>` is: each empty
	// collection is typed where it sits.
	if sl, isSet := at.(*ast.ListLit); isSet && sl.Kind == ast.SeqSet && (sl.TypeName == nil || sl.ShapeHead) && len(sl.Items) != 0 {
		if elem, set := setElem(want); set && irTypedWithin(elem) {
			return bl.setMakeOf(sl, elem)
		}
	}
	if vl, isVector := at.(*ast.ListLit); isVector && vl.Kind == ast.SeqVector && (vl.TypeName == nil || vl.ShapeHead) && len(vl.Items) != 0 {
		// `[[1]]` where a `Vector<Iter<Int>>` is expected: each element
		// enters the declared `Iter<T>`, as a list literal's does.
		if elem, vector := vectorElem(want); vector && (irTypedWithin(elem) || elem.tag == tagSeq || irHeldKind(elem)) {
			return bl.vectorMakeOf(vl, elem)
		}
	}
	if ml, isMap := at.(*ast.MapLit); isMap && ml.TypeName == nil && len(ml.Entries) != 0 && want.tag == tagMap && want.comp != nil && len(want.comp.parts) == 2 &&
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
		(funcResult(want).tag == tagSeq || irHeldKind(funcResult(want))) {
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

// bracketLit lowers a bracket literal as the collection the checker decided
// it is (ast.ListLit.Kind): a type it names over the collection, built from
// the literal without its name; an unnamed literal that builds the distinct
// type its position expects, the same; or the List, Vector or Set itself.
func (bl *irScalarBuilder) bracketLit(t *ast.ListLit) (ir.Temp, kind, bool, bool) {
	if t.TypeName != nil && !t.ShapeHead {
		anon := *t
		anon.TypeName = nil
		return bl.attachedVariant(t, t.TypeName, &anon)
	}
	if d := bl.g.literalDistinct(t); d != nil && t.TypeName == nil {
		anon := *t
		return bl.compositeDistinctMake(&ast.Call{Args: []ast.Node{&anon}, Line: t.Line, Col: t.Col}, d)
	}
	switch t.Kind {
	case ast.SeqVector:
		return bl.vectorMake(t)
	case ast.SeqSet:
		return bl.setMake(t)
	}
	switch {
	case len(t.Spreads) == 0:
		return bl.listMake(t, t.Items, nil)
	case len(t.Spreads) == 1 && t.Spreads[0].At == len(t.Items):
		// `[x, ..xs]`: the elements consed onto the spread.
		return bl.listMake(t, t.Items, t.Spreads[0].Head)
	}
	return bl.listSpreadLit(t)
}

// literalDistinct is the distinct type an unnamed collection literal builds
// when its position expects one over the collection (`p: Path = [1, 2]`),
// as the checker typed it, or nil.
func (g *gen) literalDistinct(n ast.Node) *typeDef {
	k := g.checkedKind(n)
	if k.tag != tagNamed || k.def == nil || !irCompositeDistinct(k.def) {
		return nil
	}
	return k.def
}
