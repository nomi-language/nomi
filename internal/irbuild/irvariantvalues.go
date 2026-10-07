package irbuild

import (
	"strconv"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// irRetainedEnumKind reports whether the VM carries values of enum d.
func irRetainedEnumKind(d *typeDef) bool {
	return irRetainedEnumKindIn(d, nil)
}

// irRetainedEnumKindIn is irRetainedEnumKind inside a walk already deciding
// the types on outer (irNominalCoinductive).
func irRetainedEnumKindIn(d *typeDef, outer []*typeDef) bool {
	if d == nil || !d.isEnum || !d.lowerable || d.rtOpaque || len(d.variants) == 0 {
		return false
	}
	for _, v := range d.variants {
		switch v.kind {
		case "bare":
			if len(v.payloads) != 0 {
				return false
			}
		case "struct":
			// Struct-shaped variants carry scalar-leaf fields and retained
			// structs. A user struct or enum that reaches this enum back sits
			// in a boxed slot and is decided co-inductively
			// (irNominalCoinductive), so its check never re-enters this one.
			for _, p := range v.payloads {
				if irSelfPayload(d, p.k) {
					// `Let {value: Expr, body: Expr}`: the enum itself, held
					// by reference; the layout does not recurse.
					continue
				}
				if p.slot >= 0 && p.slot < len(d.slots) && irCyclicNominal(p.k, d.slots[p.slot].boxed, outer) {
					if !irNominalCoinductive(p.k, append(outer, d)) {
						return false
					}
					continue
				}
				if p.slot < 0 || p.slot >= len(d.slots) || d.slots[p.slot].boxed {
					return false
				}
				// A user enum in an unboxed slot (`op: Op`): the slot is
				// unboxed only when the payload cannot reach this enum
				// back, so its own check never re-enters this one.
				userEnum := p.k.tag == tagNamed && p.k.def != nil && p.k.def.isEnum && p.k.def.preludeOf == nil && irRetainedEnumKind(p.k.def)
				if !(irRetainedLeafKind(p.k) || irChannelFieldKind(p.k) || irStructPayload(p.k) || userEnum ||
					(d.preludeOf == nil && irPreludeVariantField(d, p.k, outer)) || (d.preludeOf == nil && irSeqPayload(d, p.k, outer)) ||
					(d.preludeOf == nil && irNominalListPayload(p.k, append(outer, d))) ||
					(d.preludeOf == nil && irAcyclicValuePayload(p.k, append(outer, d)))) {
					return false
				}
			}
		case "embedded":
			if d.preludeOf != nil || !irRetainedEmbed(d, &v) {
				return false
			}
		case "positional":
			if len(v.payloads) != 1 {
				return false
			}
			// The slot first: a payload that reaches this enum back sits in
			// a boxed slot. A user struct or enum there is decided
			// co-inductively (irNominalCoinductive), and the other boxed
			// payload admitted is a tuple naming the enum itself
			// (irSelfTuplePayload), which compares kinds without re-entering;
			// boxing is a Go spelling and the VM holds records by reference.
			payload := v.payloads[0].k
			if irSelfPayload(d, payload) {
				continue
			}
			if slot := v.payloads[0].slot; d.preludeOf == nil && slot >= 0 && slot < len(d.slots) && irCyclicNominal(payload, d.slots[slot].boxed, outer) {
				if !irNominalCoinductive(payload, append(outer, d)) {
					return false
				}
				continue
			}
			if slot := v.payloads[0].slot; slot < 0 || slot >= len(d.slots) || (d.slots[slot].boxed && !irSelfTuplePayload(d, payload)) {
				return false
			}
			// A user enum in an unboxed slot (`Wrapped Wrapper<Int>`), as a
			// struct-shaped variant's field admits one.
			userEnum := d.preludeOf == nil && payload.tag == tagNamed && payload.def != nil && payload.def.isEnum && payload.def.preludeOf == nil && irRetainedEnumKind(payload.def)
			if d.preludeOf == nil && irSeqPayload(d, payload, outer) {
				continue
			}
			if d.preludeOf == nil && irNominalListPayload(payload, append(outer, d)) {
				continue
			}
			if d.preludeOf == nil && irAcyclicValuePayload(payload, append(outer, d)) {
				continue
			}
			if !userEnum && !irRetainedLeafKind(payload) && !isDecimalKind(payload) &&
				!(payload != kindEmptyVector && irRetainedVectorKind(payload)) && !(payload != kindEmptySet && irRetainedSetKind(payload)) &&
				!(d.preludeOf != nil && irRetainedPreludePayload(payload)) && !irSelfContainerPayload(d, payload) && !irSelfTuplePayload(d, payload) && !irEnumContainerPayload(d, payload) && !(d.preludeOf == nil && (irLeafTupleKind(payload) || irStructPayload(payload) || irFlatPayload(payload))) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// irSeqPayload admits a lowered sequence as a user enum's payload
// (`Lazy Iter<Int>`, `Kids Iter<Tree>`), held as its closure. An element that
// is a user struct or enum is decided co-inductively with d on the walk, so an
// enum whose payload iterates over itself does not recurse; any other element
// must name no declared type at any depth, so irRetainedValueKind cannot
// re-enter d.
func irSeqPayload(d *typeDef, k kind, outer []*typeDef) bool {
	if k.tag != tagSeq || k.comp == nil || len(k.comp.parts) != 1 {
		return false
	}
	e := k.comp.parts[0]
	if e == kindUnit {
		return true
	}
	if e.tag == tagNamed && e.def != nil && e.def.preludeOf == nil && !e.def.isDistinct &&
		e.def.genStructOf == nil && e.def.genericOf == nil {
		return irNominalCoinductive(e, append(outer, d))
	}
	var declared func(k kind) bool
	declared = func(k kind) bool {
		if k.def != nil {
			return true
		}
		if k.comp != nil {
			for _, p := range k.comp.parts {
				if declared(p) {
					return true
				}
			}
		}
		return false
	}
	return !declared(e) && irRetainedValueKind(e)
}

// irPreludeVariantField is a user enum's struct-shaped variant field that is a
// Maybe or Result (`List {filter: Maybe<Status>}`) over scalar leaves, lists,
// Unit and user enums without embedded variants: the field a struct admits
// (irUserStructFieldKind). A user enum payload is decided co-inductively with
// d on the walk, so `Node {next: Maybe<Tree>}` inside Tree does not recurse.
func irPreludeVariantField(d *typeDef, k kind, outer []*typeDef) bool {
	if k.tag != tagNamed || k.def == nil || k.def.preludeOf == nil || !k.def.isEnum || !k.def.lowerable || len(k.def.variants) == 0 {
		return false
	}
	walk := append(append([]*typeDef(nil), outer...), d)
	for _, v := range k.def.variants {
		for _, p := range v.payloads {
			switch {
			case irRetainedLeafKind(p.k), irRetainedListKind(p.k), p.k == kindUnit:
			case p.k.tag == tagNamed && p.k.def != nil && p.k.def.isEnum && p.k.def.preludeOf == nil && !irEnumEmbeds(p.k.def):
				if !irNominalCoinductive(p.k, walk) {
					return false
				}
			default:
				return false
			}
		}
	}
	return true
}

// irCyclicNominal reports whether a payload or field of kind k is a user
// struct or enum reached back through a cycle: it sits in a boxed slot,
// which is how the layout breaks a recursive type, or it names a
// type the walk is already deciding. The VM holds every record by
// reference, so the recursion is in the values and not in the layout.
func irCyclicNominal(k kind, boxed bool, outer []*typeDef) bool {
	if k.tag != tagNamed || k.def == nil || k.def.preludeOf != nil || k.def.isDistinct ||
		k.def.genStructOf != nil || k.def.genericOf != nil {
		return false
	}
	if boxed {
		return true
	}
	for _, o := range outer {
		if o == k.def {
			return true
		}
	}
	return false
}

// irNominalCoinductive decides a user struct or enum co-inductively: one the
// walk is already deciding (on outer) is assumed retained, so `enum Loop {
// Go Cell }` beside `struct Cell { next: Loop }` is decided once, each type
// assuming the other.
func irNominalCoinductive(k kind, outer []*typeDef) bool {
	for _, o := range outer {
		if o == k.def {
			return true
		}
	}
	if k.def.isEnum {
		return irRetainedEnumKindIn(k.def, outer)
	}
	return irRetainedStructKindIn(k.def, outer)
}

// irSelfPayload is a user enum's payload that is the enum itself
// (`Link Chain`, `Let {value: Expr}`). The VM holds every variant value by
// reference, so the recursion is in the values and not in the layout.
func irSelfPayload(d *typeDef, k kind) bool {
	return d.preludeOf == nil && k.tag == tagNamed && k.def == d
}

// irSelfContainerPayload admits an enum's `List<Self>` or
// `Map<String, Self>` payload: std/json's `Arr` and `Obj`, and a user enum
// written the same way. The rt type holds the list or map in its own slot,
// and the VM holds a list and map of variant values, so the recursion is in
// the values and not in the layout.
func irSelfContainerPayload(d *typeDef, k kind) bool {
	if d.preludeOf != nil || k.comp == nil {
		return false
	}
	self := named(d)
	switch k.tag {
	case tagList:
		return len(k.comp.parts) == 1 && k.comp.parts[0] == self
	case tagMap:
		return len(k.comp.parts) == 2 && k.comp.parts[0] == kindString && k.comp.parts[1] == self
	}
	return false
}

// irSelfTuplePayload admits a user enum's tuple payload whose parts are
// scalar leaves or the enum itself: a tree's `Node (Tree, Int, Tree)`. The
// variant's reference slot holds the tuple and the tuple holds variant
// values, so, as for irSelfContainerPayload, the recursion is in the values
// and not in the layout.
func irSelfTuplePayload(d *typeDef, k kind) bool {
	if d.preludeOf != nil || k.tag != tagTuple || k.comp == nil || len(k.comp.parts) < 2 {
		return false
	}
	self, selfSeen := named(d), false
	for _, p := range k.comp.parts {
		switch {
		case p == self:
			selfSeen = true
		case irScalarLeafKind(p) && p.def == nil:
		default:
			return false
		}
	}
	return selfSeen
}

// irFlatPayload admits a user enum's positional payload whose own shape
// cannot reach the enum back: a distinct over a tuple, map or list of scalar
// leaves (`Coord Pair`, `Counts Kvs`), a prelude instance over scalar leaves
// (`Inner Maybe<Int>`), a function over scalar leaves (`Computation (Int) ->
// Int`) and a local interface's existential (`Render Renderable`). Each is
// held in the variant's reference slot.
func irFlatPayload(k kind) bool {
	leaves := func(parts []kind) bool {
		for _, p := range parts {
			if !irScalarLeafKind(p) || p.def != nil {
				return false
			}
		}
		return true
	}
	switch {
	case irExistentialKind(k):
		return true
	case k.tag == tagFunc && k.comp != nil:
		return leaves(k.comp.parts)
	case k.tag == tagNamed && k.def != nil && k.def.preludeOf != nil:
		return leaves(k.def.preludeArgs) && irRetainedEnumKind(k.def)
	case k.tag == tagNamed && k.def != nil && irCompositeDistinct(k.def):
		in := k.def.inner
		return in.comp != nil && in.tag != tagFunc && leaves(in.comp.parts)
	}
	return false
}

// irAcyclicValuePayload admits a user enum's payload of any value the VM
// carries (`Has Maybe<Maybe<Int>>`, `Has (Shape, String)`, `Has
// Maybe<Result<Int, String>>`) that reaches no type the walk is deciding.
// The VM holds the payload in the variant's slot as it holds any operand,
// and a payload that reaches the enum back is left to the self and
// co-inductive arms, so deciding this one cannot re-enter the walk.
//
// A payload holding a channel or task handle is left to the channel field
// rule (irChannelFieldKind): irKindReaches does not look through a handle's
// element, so `Ask {reply: Channel<Request>}` inside Request would recurse.
func irAcyclicValuePayload(k kind, deciding []*typeDef) bool {
	return !irHoldsConcHandle(k, 0) && !irKindReaches(k, deciding) && irCallableValueKind(k)
}

// irHoldsConcHandle reports whether k is, or holds in a component or a type
// argument, a channel or task handle. depth bounds the walk, which only
// descends through structural parts and type arguments.
func irHoldsConcHandle(k kind, depth int) bool {
	if depth > 16 {
		return true
	}
	if _, _, isChannel := channelElem(k); isChannel {
		return true
	}
	if _, isTask := taskElem(k); isTask {
		return true
	}
	parts := kindParts(k)
	if k.def != nil && k.def.genericOf != nil {
		parts = k.def.genericArgs
	}
	for _, p := range parts {
		if irHoldsConcHandle(p, depth+1) {
			return true
		}
	}
	return false
}

// irStructPayload is a retained struct carried positionally by a user enum
// (`Wrap Circle`). A retained struct's fields exclude enums, so no layout
// recursion follows.
func irStructPayload(k kind) bool {
	return k.tag == tagNamed && k.def != nil && irRetainedStructKind(k.def)
}

// irEnumContainerPayload admits a user enum's List or Map payload over scalar
// leaves (`Arr List<Int>`, `Obj Map<String, Int>`). The value sits in the
// variant's own slot, as a leaf does, and the container holds no nominal
// value, so no layout recursion follows.
func irEnumContainerPayload(d *typeDef, k kind) bool {
	if d.preludeOf != nil || k.comp == nil {
		return false
	}
	for _, p := range k.comp.parts {
		if !irScalarLeafKind(p) || p.def != nil {
			return false
		}
	}
	switch k.tag {
	case tagList:
		return irRetainedListKind(k)
	case tagMap:
		return irRetainedMapKind(k)
	}
	return false
}

// irNominalListPayload admits a user enum's List payload of any element a
// struct field of the same type admits (irComposedFieldKind), user structs and
// enums among them (`Poly List<Point>`). A declared element is decided
// co-inductively with the walk, so an element that holds the enum back does
// not recurse. The VM holds the list in the variant's slot.
func irNominalListPayload(k kind, deciding []*typeDef) bool {
	return k.tag == tagList && k != kindEmptyList && irComposedFieldKind(k, deciding)
}

// irRetainedEmbed admits an `embeds` variant of a retained struct in an
// unboxed slot, or of a zero-sized marker, which has no slot.
func irRetainedEmbed(d *typeDef, v *variantDef) bool {
	if v.embeds == nil || len(v.payloads) != 1 || v.payloads[0].k != named(v.embeds) {
		return false
	}
	if irEmbedsEnum(v) {
		return true
	}
	slot := v.payloads[0].slot
	if irRetainedMarker(v.embeds) {
		return slot < 0
	}
	// A struct or a scalar-wrapping distinct: the VM's enum value is the
	// embedded value itself, a distinct boxed as its own record.
	return (irRetainedStructKind(v.embeds) || (irWrappingDistinct(v.embeds) && !v.embeds.rtOpaque)) &&
		slot >= 0 && slot < len(d.slots) && !d.slots[slot].boxed
}

// embedWiden widens a value of an embedded type into its `embeds` variant, as
// widenIntoVariant does: the tag and the value in its slot, or the tag alone
// for a marker. The source must be free of effects: the widened literal is
// mobile, and a marker's source is not spelled at all.
func (bl *irScalarBuilder) embedWiden(at ast.Node, src ir.Temp, d *typeDef, v *variantDef) (ir.Temp, kind, bool) {
	if !bl.effectFree(src) {
		return src, kindInvalid, false
	}
	payload := src
	if v.payloads[0].slot < 0 {
		payload = ir.NoTemp
	}
	make := ir.NewMakeVariantEmbed(bl.g.irNodePos(at), bl.f.NewTemp(), bl.g.irTypeSym(d), v.nomi,
		bl.g.irTypeSym(v.embeds), payload)
	bl.b.Append(make)
	k := named(d)
	bl.side(make.Dst(), irScalarSide{k: k, pureMake: true})
	return make.Dst(), k, true
}

// effectFree reports whether the current block defines t without effects: a
// constant, a local read, a forced copy or a pure construction.
func (bl *irScalarBuilder) effectFree(t ir.Temp) bool {
	// A parameter of the function being built arrives as a value: the
	// adapter closure irfuncwiden.go builds widens its parameters into an
	// `embeds` enum directly.
	for _, p := range bl.f.Params() {
		if p.Temp == t {
			return true
		}
	}
	instrs := bl.b.Instrs()
	for i := len(instrs) - 1; i >= 0; i-- {
		def, ok := instrs[i].(interface{ Dst() ir.Temp })
		if !ok || def.Dst() != t {
			continue
		}
		switch n := instrs[i].(type) {
		case *ir.Const, *ir.Bind:
			return true
		case *ir.Ref:
			return n.Kind() == ir.RefLocal
		case *ir.Copy:
			return int(t) < len(bl.sides) && bl.sides[t].copy == irCopyForce
		case *ir.Make:
			return int(t) < len(bl.sides) && bl.sides[t].pureMake
		}
		return false
	}
	return false
}

// Prelude wrappers can carry one represented user enum. Its own payloads are
// scalar leaves, so this does not recursively admit nominal layouts.
func irRetainedPreludePayload(k kind) bool {
	if k == kindUnit || irByteValueKind(k) || isDecimalKind(k) || irHostHandleKind(k) || irDynamicKind(k) || irStdMarkerKind(k) || irRetainedLeafKind(k) || irRetainedListKind(k) || irRetainedVectorKind(k) || irLeafTupleKind(k) {
		return true
	}
	if irRetainedTupleKind(k) {
		// A tuple of retained values (`List.next_item`'s `(T, List<T>)`).
		return true
	}
	if k.tag == tagNamed && irCompositeDistinct(k.def) {
		// A distinct over a tuple, a list, a map or a function
		// (`Maybe<Pair>` for `type Pair (Int, String)`), held as its record.
		return true
	}
	if irRetainedSeqKind(k) {
		// A lowered sequence (`Some(xs |> Iter.map(f))`), held as its closure.
		return true
	}
	if irListTransportKind(k) {
		// A list of represented elements (`List<Maybe<Int>>`); Debug and
		// equality over the payload keep their own checks.
		return true
	}
	if k.tag == tagMap && k != kindEmptyMap && irRetainedMapKind(k) {
		// A map (`Result<Map<String, Int>, E>`), held as the map value.
		return true
	}
	if (k != kindEmptySet && irRetainedSetKind(k)) || irRetainedRecordKind(k) {
		// A set (`Maybe<Set<Int>>`) or an anonymous record
		// (`Maybe<{x: Int}>`), held as its record.
		return true
	}
	if k.tag == tagNamed && k.def != nil && !k.def.isEnum && irRetainedStructKind(k.def) {
		return true
	}
	if irDynamicContainerKind(k) {
		return true
	}
	if irExistentialKind(k) {
		// A local interface's existential (`Fragment<Display>`): the VM
		// holds the concrete value in the variant's reference slot.
		return true
	}
	if k.tag == tagFunc && irCallableValueKind(k) {
		// A function value (`List.head(fns)`'s `Maybe<(Int) -> Int>`), held
		// in the variant's reference slot.
		return true
	}
	if k.tag == tagList && k.comp != nil && len(k.comp.parts) == 1 && k.comp.parts[0].tag == tagFunc && irCallableValueKind(k.comp.parts[0]) {
		// A list of function values (`Ok([f, g])`), held as the list.
		return true
	}
	if k.tag == tagNamed && k.def != nil && k.def.preludeOf != nil && irRetainedEnumKind(k.def) {
		// One prelude wrapper inside another (`Some(Ok(99))`,
		// `Some(Outcome.Completed(5))`), to any depth: a prelude instance's
		// payload types are its arguments, a finite tree, so the check
		// terminates.
		for _, v := range k.def.variants {
			for _, p := range v.payloads {
				failure := p.k.tag == tagNamed && p.k.def != nil && p.k.def == namedPayloadDefs()[outcomeFailure]
				if p.k != kindUnit && !failure && !irRetainedPreludePayload(p.k) {
					return false
				}
			}
		}
		return true
	}
	return k.tag == tagNamed && k.def != nil && k.def.preludeOf == nil && irRetainedEnumKind(k.def) && !irEnumEmbeds(k.def)
}

// irEmbedsEnum reports whether v is an `embeds` variant of another enum. No
// value widens into one (the front end rejects `f(Shape.Circle(1.5))` where a
// Drawable is wanted), so its one value is the payload-less
// `Drawable.Shape{}`, and its layout is a bare variant (irStateVariants).
func irEmbedsEnum(v *variantDef) bool {
	return v.kind == "embedded" && v.embeds != nil && v.embeds.isEnum
}

// irEnumEmbeds reports whether an enum has an `embeds` variant.
func irEnumEmbeds(d *typeDef) bool {
	for _, v := range d.variants {
		if v.kind == "embedded" {
			return true
		}
	}
	return false
}

func (bl *irScalarBuilder) retainedVariant(field *ast.FieldAccess) (*typeDef, *variantDef, bool) {
	if field.Field == nil {
		return nil, nil, false
	}
	if d := bl.g.dotOwners[field]; d != nil {
		// A `.Variant` shorthand, whose enum the checked type names. See
		// dotEnumDef.
		if !irRetainedEnumKind(d) {
			return nil, nil, false
		}
		v := d.variant(field.Field.Name)
		return d, v, v != nil
	}
	var name string
	if owner, ok := field.Object.(*ast.TypeIdent); ok {
		name = owner.Name
	} else if dotted, ok := bl.g.dottedTypeQualifier(field.Object); ok {
		// A namespaced enum (`Probe.Reading.Spike`) or one named through a
		// whole-file import (`shapes.Shape.Circle`).
		name = dotted
	} else if d := bl.stdQualifiedOwnerDef(field.Object); d != nil && irRetainedEnumKind(d) {
		// A std enum named through its module's qualifier
		// (`json.Json.Int(3)`).
		v := d.variant(field.Field.Name)
		return d, v, v != nil
	} else {
		return nil, nil, false
	}
	d, found := bl.g.namedType(name)
	if !found && name == "Ordering" && isSynthesized(field) {
		// A derived `compare` names `Ordering.Less` qualified whatever the
		// file imports (the front end resolves it the compiler-known way),
		// so std/bool's `derive Comparable for True` needs no import.
		d, found = stdEnumDefs()[stdEnumOrdering], true
	}
	if !found || !irRetainedEnumKind(d) {
		return nil, nil, false
	}
	v := d.variant(field.Field.Name)
	return d, v, v != nil
}

// stdQualifiedOwnerDef is the std type a `mod.Type` qualifier names through a
// whole-module import (qualifiedStdDef), or nil.
func (bl *irScalarBuilder) stdQualifiedOwnerDef(n ast.Node) *typeDef {
	q, isFA := n.(*ast.FieldAccess)
	if !isFA || q.Field == nil {
		return nil
	}
	mod, isIdent := q.Object.(*ast.Ident)
	if !isIdent || irQualIsLocal(bl, mod.Name) {
		return nil
	}
	return bl.g.qualifiedStdDef(mod.Name + "." + q.Field.Name)
}

func (bl *irScalarBuilder) variantValue(at ast.Node, d *typeDef, v *variantDef, args []ir.Temp, pure bool) (ir.Temp, kind, bool, bool) {
	make := ir.NewMakeVariant(bl.g.irNodePos(at), bl.f.NewTemp(), bl.g.irTypeSym(d), v.nomi, args)
	if v.kind == "struct" {
		var names []string
		for _, p := range v.payloads {
			names = append(names, p.nomi)
		}
		make = ir.NewMakeVariantFields(bl.g.irNodePos(at), make.Dst(), bl.g.irTypeSym(d), v.nomi, names, args)
	}
	bl.b.Append(make)
	k := named(d)
	bl.side(make.Dst(), irScalarSide{k: k, pureMake: pure})
	return make.Dst(), k, pure, true
}

func (bl *irScalarBuilder) variantBare(field *ast.FieldAccess) (ir.Temp, kind, bool, bool) {
	d, v, ok := bl.retainedVariant(field)
	if !ok {
		return bl.genericVariantBare(field)
	}
	if v.kind == "embedded" && irRetainedMarker(v.embeds) {
		// `State.Expired` — an embedded marker named by its variant is the
		// marker widened into the enum, which has no storage to read.
		return bl.embedMarker(field, d, v)
	}
	if v.kind != "bare" {
		return ir.NoTemp, kindInvalid, false, false
	}
	return bl.variantValue(field, d, v, nil, true)
}

// genericVariantBare builds a payload-free variant of a user generic enum
// where no expected type names the instance (`[Tree.Leaf, Tree.Node(2)]`), at
// the instance the checker solved for this site.
func (bl *irScalarBuilder) genericVariantBare(field *ast.FieldAccess) (ir.Temp, kind, bool, bool) {
	d, v, ok := bl.genericVariant(field, nil, kindInvalid)
	if !ok || v.kind != "bare" {
		return ir.NoTemp, kindInvalid, false, false
	}
	return bl.variantValue(field, d, v, nil, true)
}

// embedMarker widens an embedded marker into its variant: the tag alone, as
// variantValue drops a zero-sized payload's slot. The VM answers the marker
// value itself, as it does for any widened embedded value.
func (bl *irScalarBuilder) embedMarker(at ast.Node, d *typeDef, v *variantDef) (ir.Temp, kind, bool, bool) {
	make := ir.NewMakeVariantEmbed(bl.g.irNodePos(at), bl.f.NewTemp(), bl.g.irTypeSym(d), v.nomi,
		bl.g.irTypeSym(v.embeds), ir.NoTemp)
	bl.b.Append(make)
	k := named(d)
	bl.side(make.Dst(), irScalarSide{k: k, pureMake: true})
	return make.Dst(), k, true, true
}

// genericVariant resolves `Wrapper.Wrapped` of a user generic enum to the
// instance the checker solved at `at`: the result of the constructor call, or
// the expected kind a payload-free variant is built at.
func (bl *irScalarBuilder) genericVariant(field *ast.FieldAccess, call *ast.Call, want kind) (*typeDef, *variantDef, bool) {
	if field.Field == nil {
		return nil, nil, false
	}
	var name string
	if owner, ok := field.Object.(*ast.TypeIdent); ok {
		name = owner.Name
	} else if dotted, ok := bl.g.dottedTypeQualifier(field.Object); ok {
		name = dotted
	} else {
		return nil, nil, false
	}
	tpl, instantiate, isTemplate := bl.g.genericTemplateNamed(name)
	if !isTemplate || tpl.enumDecl() == nil {
		return nil, nil, false
	}
	k := want
	if call != nil {
		ft := bl.g.checkedCallSignature(call)
		if ft == nil {
			return nil, nil, false
		}
		k = bl.g.templateInstanceOf(instantiate, ft.Return)
		// kindInvalid: sentinel — the caller had no expected type to pass, not an operand's kind.
	} else if k == kindInvalid {
		// No expected type: the instance the checker solved for this
		// payload-free variant. A type argument nothing constrained
		// (`(Box.Empty, 1)`) projects to no kind and declines.
		if bl.g.fa == nil {
			return nil, nil, false
		}
		sym := bl.g.fa.References[analysis.Pos{Line: field.Field.Line, Col: field.Field.Col}]
		if sym == nil || sym.VariantType == nil {
			return nil, nil, false
		}
		k = bl.g.templateInstanceOf(instantiate, sym.VariantType)
	}
	if k.tag != tagNamed || k.def == nil || k.def.genericOf != tpl || !irRetainedEnumKind(k.def) {
		return nil, nil, false
	}
	v := k.def.variant(field.Field.Name)
	return k.def, v, v != nil
}

func (bl *irScalarBuilder) variantCall(call *ast.Call, field *ast.FieldAccess) (ir.Temp, kind, bool, bool) {
	d, v, ok := bl.retainedVariant(field)
	if !ok {
		d, v, ok = bl.genericVariant(field, call, kindInvalid)
	}
	if ok && irStructShapedVariant(v) {
		return bl.variantCallForm(call, d, v)
	}
	if ok && v.kind == "embedded" && v.embeds != nil && irWrappingDistinct(v.embeds) && len(call.Args) == 1 && namedArgNode(call.Args) == nil {
		// `Identifier.UserId("bob")`: the embedded distinct built from its
		// inner value, widened into its variant.
		src, k, _, vok := bl.lowerWant(call.Args[0], v.embeds.inner)
		if !vok || k != v.embeds.inner {
			return ir.NoTemp, kindInvalid, false, false
		}
		m := ir.NewMakeDistinct(bl.g.irNodePos(call), bl.f.NewTemp(), bl.g.irTypeSym(v.embeds), src)
		bl.b.Append(m)
		inner := named(v.embeds)
		bl.side(m.Dst(), irScalarSide{k: inner, pureMake: true})
		dst, wk, wok := bl.embedWiden(call, m.Dst(), d, v)
		return dst, wk, true, wok
	}
	if !ok || v.kind != "positional" || len(call.Args) == 0 {
		return ir.NoTemp, kindInvalid, false, false
	}
	argNode := call.Args[0]
	if len(call.Args) > 1 {
		// `JV.Pos(10, 20)` over a tuple payload: the arguments are the
		// tuple's components.
		if pk := v.payloads[0].k; pk.tag != tagTuple || pk.comp == nil || len(pk.comp.parts) != len(call.Args) {
			return ir.NoTemp, kindInvalid, false, false
		}
		argNode = &ast.TupleLit{Items: call.Args, Line: call.Line, Col: call.Col}
	}
	arg, k, pure, ok := bl.lowerTypedOperand(argNode, v.payloads[0].k)
	if ok && k != v.payloads[0].k {
		// An empty literal or a concrete implementer takes the payload's
		// declared type (`Token.Render(Person{...})`).
		arg, k, ok = bl.coerceEmpty(argNode, arg, k, v.payloads[0].k)
	}
	if !ok || k != v.payloads[0].k {
		return ir.NoTemp, kindInvalid, false, false
	}
	return bl.variantValue(call, d, v, []ir.Temp{arg}, pure)
}

// irStructShapedVariant reports whether a variant is constructed as a struct
// is: an inline struct-shaped variant, or an `embeds` of a struct.
func irStructShapedVariant(v *variantDef) bool {
	return v.kind == "struct" || (v.kind == "embedded" && v.embeds != nil && !v.embeds.isDistinct && !v.embeds.isEnum)
}

// variantCallForm builds the record call form `Shape.Rect({...})` or
// `Shape.Circle({...})`, piped or not: a literal argument is the brace form's
// fields, positioned at the call. A record value is declined.
func (bl *irScalarBuilder) variantCallForm(call *ast.Call, d *typeDef, v *variantDef) (ir.Temp, kind, bool, bool) {
	if len(call.Args) != 1 {
		return ir.NoTemp, kindInvalid, false, false
	}
	lit, isLit := anonArgAsLiteral(call.Args[0])
	if !isLit {
		return bl.variantFromRecord(call, d, v)
	}
	return bl.variantFromLit(call, lit, d, v)
}

// variantFromLit builds a struct-shaped variant at `at` from the fields of
// lit. An embedded struct is built as a struct literal and widened; an inline
// variant's fields are its payloads.
func (bl *irScalarBuilder) variantFromLit(at ast.Node, lit *ast.StructLit, d *typeDef, v *variantDef) (ir.Temp, kind, bool, bool) {
	if v.kind == "embedded" {
		inner, k, _, ok := bl.structMakeOf(lit, v.embeds)
		if !ok || k != named(v.embeds) {
			return ir.NoTemp, kindInvalid, false, false
		}
		dst, k, ok := bl.embedWiden(at, inner, d, v)
		return dst, k, true, ok
	}
	return bl.variantFields(at, lit, d, v)
}

// variantStructLit builds a struct-shaped variant from a brace literal, as
// variantFieldValues does: written fields in source order, each forced when
// impure, then omitted fields' declared defaults in declaration order.
func (bl *irScalarBuilder) variantStructLit(t *ast.StructLit, enum, variant string) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	d, found := bl.g.namedType(enum)
	if tpl, instantiate, isTemplate := bl.g.genericTemplateNamed(enum); isTemplate {
		// A generic enum's struct variant: the instance its field values
		// solve (`shapes.Holder.Of{value: 5}` is a `Holder<Int>`).
		args, ok := bl.g.checkedVariantTypeArgs(t, tpl, variant)
		if !ok {
			return no()
		}
		k, ok := instantiate(args)
		if !ok || k.tag != tagNamed || k.def == nil {
			return no()
		}
		d, found = k.def, true
	}
	if !found {
		// `random.Error.OsEntropy{...}`: a std enum named through its
		// module's qualifier.
		d = bl.g.qualifiedStdDef(enum)
		found = d != nil
	}
	if !found {
		return no()
	}
	return bl.variantStructLitOf(t, d, d.variant(variant))
}

// variantStructLitOf builds the brace literal t of d's variant v.
func (bl *irScalarBuilder) variantStructLitOf(t *ast.StructLit, d *typeDef, v *variantDef) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if !irRetainedEnumKind(d) {
		return no()
	}
	if v != nil && irEmbedsEnum(v) && len(t.Fields) == 0 && t.Spread == nil {
		// `Drawable.Shape{}`: an embedded enum's variant built with no
		// payload. It is the only value the
		// variant ever holds (irEmbedsEnum).
		make := ir.NewMakeVariant(bl.g.irNodePos(t), bl.f.NewTemp(), bl.g.irTypeSym(d), v.nomi, nil)
		bl.b.Append(make)
		k := named(d)
		bl.side(make.Dst(), irScalarSide{k: k, pureMake: true})
		return make.Dst(), k, true, true
	}
	if v == nil || !irStructShapedVariant(v) {
		return no()
	}
	return bl.variantFromLit(t, t, d, v)
}

// variantFields builds an inline struct-shaped variant at `at` from the
// fields of t.
func (bl *irScalarBuilder) variantFields(at ast.Node, t *ast.StructLit, d *typeDef, v *variantDef) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	vals := make(map[string]ir.Temp, len(v.payloads))
	for _, f := range t.Fields {
		var target *payload
		for i := range v.payloads {
			if v.payloads[i].nomi == f.Name {
				target = &v.payloads[i]
				break
			}
		}
		if target == nil {
			return no()
		}
		if _, dup := vals[f.Name]; dup {
			return no()
		}
		// Typed by the payload, so a bare `None` or `Ok(v)` takes its kind,
		// as a struct literal's field does.
		src, k, mobile, ok := bl.lowerTypedOperand(f.Value, target.k)
		if !ok {
			return no()
		}
		if !mobile {
			cp := ir.NewCopy(bl.g.irNodePos(f.Value), bl.f.NewTemp(), src)
			bl.b.Append(cp)
			bl.side(cp.Dst(), irScalarSide{k: k, copy: irCopyForce})
			src = cp.Dst()
		}
		src, k, ok = bl.coerceEmpty(f.Value, src, k, target.k)
		if !ok || k != target.k {
			return no()
		}
		vals[f.Name] = src
	}
	return bl.variantFinish(at, t, d, v, vals)
}

// variantFromRecord builds a struct-shaped variant from a record VALUE
// (`Shape.Rectangle(tall())`, `Shape.Circle(round())`), as structFromRecord
// builds a struct: the record is evaluated once and bound to a name no
// program can spell, and the variant is built from a literal reading its
// fields, so omitted payloads take their defaults exactly as the brace form
// fills them.
func (bl *irScalarBuilder) variantFromRecord(call *ast.Call, d *typeDef, v *variantDef) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if len(call.Args) != 1 || namedArgNode(call.Args) != nil {
		return no()
	}
	src, k, _, ok := bl.lower(call.Args[0])
	if !ok || !irRetainedRecordKind(k) {
		return no()
	}
	line, col := nodePos(call.Args[0])
	name := "%record" + strconv.Itoa(line) + "." + strconv.Itoa(col)
	bl.patternBinding(call.Args[0], name, src, k)
	lit := &ast.StructLit{Line: call.Line, Col: call.Col}
	for _, f := range k.comp.names {
		lit.Fields = append(lit.Fields, ast.StructFieldVal{Name: f, Line: line, Col: col,
			Value: &ast.FieldAccess{Object: &ast.Ident{Name: name, Line: line, Col: col},
				Field: &ast.Ident{Name: f, Line: line, Col: col}, Line: line, Col: col}})
	}
	return bl.variantFromLit(call, lit, d, v)
}

// variantFinish builds the variant from the written payloads in vals,
// filling each omitted one from its declared default in declaration order.
func (bl *irScalarBuilder) variantFinish(at ast.Node, t *ast.StructLit, d *typeDef, v *variantDef, vals map[string]ir.Temp) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	args := make([]ir.Temp, len(v.payloads))
	for i := range v.payloads {
		p := &v.payloads[i]
		if val, given := vals[p.nomi]; given {
			args[i] = val
			continue
		}
		if n, stated := irGoDefaults[p.goDeflt]; stated {
			args[i] = bl.goDefault(t, p.k, n)
			if args[i] == ir.NoTemp {
				return no()
			}
			continue
		}
		if p.goDeflt == "" && p.deflt != nil && d.foreign != "" {
			val, ok := bl.foreignFieldDefault(t, d, v.nomi, p.nomi, p.k)
			if !ok {
				return no()
			}
			args[i] = val
			continue
		}
		if p.goDeflt != "" || p.deflt == nil || d.foreign != "" {
			return no()
		}
		val, ok := bl.declDefault(p.deflt, p.k)
		if !ok {
			return no()
		}
		args[i] = val
	}
	return bl.variantValue(at, d, v, args, true)
}

// stdVariantCall builds a std enum's positional variant named bare, as a
// selective or aliased import binds it (`import std/json.Json.{String as
// JStr}` then `JStr("Ada")`), the call form of the bare arm lowerNode takes
// for a payload-free one.
func (bl *irScalarBuilder) stdVariantCall(call *ast.Call, ti *ast.TypeIdent) (ir.Temp, kind, bool, bool) {
	d, v, ok := bl.g.stdEnumVariantAt(ti.Name, ti.Line, ti.Col)
	if !ok || !irRetainedEnumKind(d) || v.kind != "positional" || len(call.Args) != 1 || hasNamedArg(call.Args) {
		return ir.NoTemp, kindInvalid, false, false
	}
	arg, k, pure, ok := bl.lowerTypedOperand(call.Args[0], v.payloads[0].k)
	if !ok || k != v.payloads[0].k {
		return ir.NoTemp, kindInvalid, false, false
	}
	return bl.variantValue(call, d, v, []ir.Temp{arg}, pure)
}

// irDynamicContainerKind is `List<Dynamic>` or `Map<String, Dynamic>`, the
// payloads `Dynamic.as_list` and `Dynamic.as_dict` answer in a Result.
func irDynamicContainerKind(k kind) bool {
	if k.comp == nil {
		return false
	}
	switch {
	case k.tag == tagList && len(k.comp.parts) == 1:
		return irDynamicKind(k.comp.parts[0])
	case k.tag == tagMap && len(k.comp.parts) == 2:
		return k.comp.parts[0] == kindString && irDynamicKind(k.comp.parts[1])
	}
	return false
}
