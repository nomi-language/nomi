package irbuild

import (
	"fmt"

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

// listMakeOf builds the list literal at, of the elements items and the
// spread tail, at its kind (literalKind: its record, or want, the kind of
// the position, where the builder made it): each element is lowered against
// the list's element, as an argument is against its parameter, and the tail
// enters the list's kind.
func (bl *irScalarBuilder) listMakeOf(at ast.Node, items []ast.Node, tail ast.Node, want kind) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if len(items) == 0 && tail == nil {
		return bl.emptyLiteral(at, listSpreadJoin, want)
	}
	k, ok := bl.builtLiteralKind(at, listSpreadJoin, want)
	if !ok {
		return no()
	}
	// The VM's list holds values of every kind with a stored type (a
	// function, an Iter, a Context, a list of structs), and each consumer of
	// the list (Debug, equality, patterns) checks the element kinds it reads
	// itself.
	if !bl.g.irSpreadTailKind(k) {
		bl.g.dc.note("a list literal of " + k.nomi() + ", whose elements have no stored type")
		return no()
	}
	elem := k.comp.parts[0]
	rest := ir.NoTemp
	if tail != nil {
		v, tk, _, ok := bl.lower(tail)
		if !ok {
			return no()
		}
		// A tail that is another Iter (`[x, ..option]`) is made a List.
		p, ok := bl.collectSpread(at, spreadPart{at: tail, v: v, k: tk}, listSpreadJoin)
		if !ok {
			return no()
		}
		if rest, _, ok = bl.convertTo(tail, p.v, p.k, k); !ok {
			return no()
		}
	}
	values, ok := bl.lowerItemsAt(items, elem)
	if !ok {
		return no()
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
	k, ok := bl.literalKind(at, shape, want)
	if !ok {
		// The checker types every empty literal it accepts, closing an
		// element nothing determines, and a copy the builder made is
		// lowered for a position of its collection.
		line, col := nodePos(at)
		panic(fmt.Sprintf("irbuild: an empty literal at %s:%d:%d that neither its record (%s) nor its position (%s) types",
			bl.g.nomiPath, line, col, bl.g.checkedKind(at).nomi(), want.nomi()))
	}
	return bl.emptyOf(at, k)
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
		if out, how := bl.intoConjunction(at, v, want, have, bl.recordedCoercion(at, want, have)); how != irNotCoerced {
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
		if out, how := bl.intoExistential(at, v, want, have, bl.recordedCoercion(at, want, have)); how != irNotCoerced {
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
	// `w: Option<Shape> = m` for `m: Option<Circle>`: a value built at the
	// narrower type is rebuilt at the wider one (irembedsrebuild.go).
	if out, rebuilt := bl.rebuildWidened(at, v, have, want); rebuilt {
		return out, want, true
	}
	return v, have, false
}

// erasesUnboxed reports whether a value of kind have, of checker type ht
// or nil, is, unchanged, a value of the existential want: it erases into
// want (irErases) and enters it without a box (irBoxPlanFor), so a source
// of such values is viewed as a source of want's.
func (bl *irScalarBuilder) erasesUnboxed(want, have kind, ht analysis.Type) bool {
	return irExistentialKind(want) && bl.g.irErases(want, have, ht) && bl.g.irBoxPlanFor(want, have, ht, nil) == nil
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
		// `_none: Option<Int> = dbg None`: `dbg` is transparent, so the
		// wanted type is its operand's.
		return bl.dbgWant(d, d.Expr, want)
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
		// `Option.map(m, fn _ -> [20])` where the checker solved the callback's
		// result as `Iter<Int>`: the lambda answers that sequence
		// (lambdaFunction), so its function value is the wanted one. A
		// lambda whose result is an interface's existential answers it.
		prevFor, prev := bl.g.lambdaWantFor, bl.g.lambdaWant
		bl.g.lambdaWantFor, bl.g.lambdaWant = lam, funcResult(want)
		defer func() { bl.g.lambdaWantFor, bl.g.lambdaWant = prevFor, prev }()
	}
	v, k, mobile, ok, built := bl.madeLiteral(at, want)
	if !built {
		v, k, mobile, ok = bl.lower(at)
	}
	if ok && k != want && want.tag == tagSeq && want.comp != nil {
		// A source written where a declared `Iter<T>` is expected (a field,
		// a payload, an element) is viewed as the sequence there.
		if seq, viewed := bl.seqView(at, v, k, seqElem(want)); viewed {
			return seq, want, true, true
		}
	}
	return v, k, mobile, ok
}

// madeLiteral lowers at, a literal the builder made, at want, the kind of
// the position it is lowered for: a copy of a literal under a type's name
// (`Json.Obj{...}`'s map, `Path[...]`'s list), the items of a constructor's
// tuple payload, a derived body's literal, or a generic struct's field
// default recorded in terms of a type parameter this instance does not
// bind. Such a literal has no record of the checker's that names a kind,
// and the kind of the position is its kind (literalKind). A literal of the
// program's is built at its record, which lower reads, and built is false
// for it and for any other node.
func (bl *irScalarBuilder) madeLiteral(at ast.Node, want kind) (ir.Temp, kind, bool, bool, bool) {
	if k, recorded := bl.g.recordedKind(at); recorded && irFullyTyped(k) {
		return ir.NoTemp, kindInvalid, false, false, false
	}
	var v ir.Temp
	var k kind
	var mobile, ok bool
	switch t := at.(type) {
	case *ast.ListLit:
		if t.TypeName != nil && !t.ShapeHead {
			return ir.NoTemp, kindInvalid, false, false, false
		}
		v, k, mobile, ok = bl.bracketLitOf(t, want)
	case *ast.MapLit:
		if t.TypeName != nil && !t.ShapeHead {
			return ir.NoTemp, kindInvalid, false, false, false
		}
		v, k, mobile, ok = bl.mapMakeOf(t, want)
	case *ast.TupleLit:
		v, k, mobile, ok = bl.tupleMakeWant(t, want)
	case *ast.StructLit:
		if t.TypeName != nil || t.Spread != nil || t.EmptyMap || bl.g.targetStruct(t) != nil {
			return ir.NoTemp, kindInvalid, false, false, false
		}
		v, k, mobile, ok = bl.recordMakeOf(t, want)
	default:
		return ir.NoTemp, kindInvalid, false, false, false
	}
	return v, k, mobile, ok, true
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
		return bl.compositeDistinctMake(t, []ast.Node{&anon}, d)
	}
	// kindInvalid: sentinel — no position's kind; the literal's record gives its kind.
	return bl.bracketLitOf(t, kindInvalid)
}

// bracketLitOf builds the List, Vector or Set literal t at its kind
// (literalKind), want where it has no record.
func (bl *irScalarBuilder) bracketLitOf(t *ast.ListLit, want kind) (ir.Temp, kind, bool, bool) {
	switch t.Kind {
	case ast.SeqVector:
		return bl.vectorMakeOf(t, want)
	case ast.SeqSet:
		return bl.setMakeOf(t, want)
	}
	switch {
	case len(t.Spreads) == 0:
		return bl.listMakeOf(t, t.Items, nil, want)
	case len(t.Spreads) == 1 && t.Spreads[0].At == len(t.Items):
		// `[x, ..xs]`: the elements consed onto the spread.
		return bl.listMakeOf(t, t.Items, t.Spreads[0].Head, want)
	}
	return bl.listSpreadLit(t)
}

// lowerItemsAt lowers a collection literal's items, left to right, each
// against elem, the element of the literal's kind (literalKind), and then
// converts each to elem (convertTo) where it differs: an embedded value
// widens, a value enters an interface, a source is viewed as an Iter. The
// conversions have no effect, so making them after every item reorders
// nothing. A Unit item is held as the Unit value its temporary carries.
func (bl *irScalarBuilder) lowerItemsAt(items []ast.Node, elem kind) ([]ir.Temp, bool) {
	values := make([]ir.Temp, len(items))
	kinds := make([]kind, len(items))
	for i, item := range items {
		v, k, _, ok := bl.lowerTypedOperand(item, elem)
		if !ok || v == ir.NoTemp {
			return nil, false
		}
		values[i], kinds[i] = v, k
	}
	for i := range values {
		var ok bool
		if values[i], _, ok = bl.convertTo(items[i], values[i], kinds[i], elem); !ok {
			bl.g.dc.note("a literal's item of kind " + kinds[i].nomi() + " where " + elem.nomi() + " is expected")
			return nil, false
		}
	}
	return values, true
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
