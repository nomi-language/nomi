package irbuild

import (
	"slices"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// A value, not a construction, of a type that holds an embedded type, used
// where the same type at the embedding enum is expected: `w: Maybe<Shape> =
// m` for `m: Maybe<Circle>` and `enum Shape { embeds Circle }`, or the
// narrower operand of `m == Some(Shape.Dot)`. The checker admits it (a
// Maybe<Circle> is assignable to a Maybe<Shape>), and a construction in that
// position is built at the expected type, so its embedded value widens where
// it sits. A value already built is not the same runtime value: a Shape
// holding a Circle is the enum's record around the Circle record
// (ir.NewMakeVariantEmbed), and a Maybe<Shape> holds that record where a
// Maybe<Circle> holds the Circle. So the value is rebuilt at the wider type,
// not relabelled:
//
//   - a tuple, an anonymous record or a user generic struct is made again
//     from its parts, each converted to the wider part (coerceEmpty, which
//     widens an embedded value and comes back here for a nested one);
//   - a generic enum instance (`Maybe`, `Result`, a user `Box<T>`) goes
//     through an adapter function that matches each variant and makes it
//     again at the wider instance with its payloads converted. The adapter
//     takes itself as a capture, so an enum holding its own instance
//     (`Chain<T>` in `Link{head: T, tail: Chain<T>}`) rebuilds its tail by
//     calling itself;
//   - a List, Set, Vector or Map is mapped through the element's adapter
//     (`Iter.map`'s operation over the collection's view, then the matching
//     materializer), so a Map's keys and values are both converted.
//
// An enum holding its own instance inside a collection (`Node(List<Tree<T>>)`,
// `Node(Map<String, Tree<T>>)`) rebuilds each element with the enum's own
// adapter. An element adapter is another function, which takes the enum
// adapter as a capture (rebuildSelfRef).

// rebuildWidened answers v, a value of kind have, as a value of kind want,
// where the two are the same tuple, record, generic struct, generic enum or
// collection shape and each part converts. ok is false when the shapes
// differ or a part does not convert; the parts read before the failure stay
// in the block, as effect-free reads of v whose results nothing uses.
func (bl *irScalarBuilder) rebuildWidened(at ast.Node, v ir.Temp, have, want kind) (ir.Temp, bool) {
	if have == want {
		return v, true
	}
	switch {
	case have.tag == tagTuple && want.tag == tagTuple:
		return bl.rebuildTuple(at, v, have, want)
	case have.tag == tagAnonStruct && want.tag == tagAnonStruct:
		return bl.rebuildRecord(at, v, have, want)
	case have.tag == tagList && want.tag == tagList:
		return bl.rebuildList(at, v, have, want)
	case have.tag == tagMap && want.tag == tagMap:
		return bl.rebuildMap(at, v, have, want)
	}
	if he, isSet := setElem(have); isSet {
		if we, wset := setElem(want); wset && have != kindEmptySet && want != kindEmptySet && irRetainedSetKind(have) && irRetainedSetKind(want) {
			return bl.rebuildMapped(at, v, ir.IterOverSet, ir.IterToSet, he, we, want)
		}
		return ir.NoTemp, false
	}
	if he, isVector := vectorElem(have); isVector {
		if we, wvector := vectorElem(want); wvector && have != kindEmptyVector && want != kindEmptyVector && (irRetainedVectorKind(have) || bl.g.irVectorValueKind(have)) && (irRetainedVectorKind(want) || bl.g.irVectorValueKind(want)) {
			return bl.rebuildMapped(at, v, ir.IterOverVector, ir.IterToVector, he, we, want)
		}
		return ir.NoTemp, false
	}
	if have.tag != tagNamed || want.tag != tagNamed || have.def == nil || want.def == nil || !irSameGeneric(have.def, want.def) {
		return ir.NoTemp, false
	}
	if have.def.isEnum {
		if !irRetainedEnumKind(have.def) || !irRetainedEnumKind(want.def) || len(have.def.variants) != len(want.def.variants) {
			return ir.NoTemp, false
		}
		fn, ok := bl.rebuildAdapter(at, have, want)
		if !ok {
			return ir.NoTemp, false
		}
		call := ir.NewIndirectCall(bl.g.irNodePos(at), bl.f.NewTemp(), ir.OrdinaryCall, fn, v)
		bl.b.Append(call)
		bl.side(call.Dst(), irScalarSide{k: want})
		return call.Dst(), true
	}
	return bl.rebuildStruct(at, v, have, want)
}

// irSameGeneric reports whether a and b are two instances of one generic
// declaration: a user generic struct or enum, or a prelude enum.
func irSameGeneric(a, b *typeDef) bool {
	if a.genericOf != nil {
		return a.genericOf == b.genericOf
	}
	return a.preludeOf != nil && b.preludeOf != nil && a.preludeOf.spec == b.preludeOf.spec
}

// rebuildPart converts one part read off the value being rebuilt into the
// wider part, as an argument enters its parameter.
func (bl *irScalarBuilder) rebuildPart(at ast.Node, v ir.Temp, have, want kind) (ir.Temp, bool) {
	out, k, ok := bl.coerceEmpty(at, v, have, want)
	return out, ok && k == want
}

func (bl *irScalarBuilder) rebuildTuple(at ast.Node, v ir.Temp, have, want kind) (ir.Temp, bool) {
	if have.comp == nil || want.comp == nil || len(have.comp.parts) != len(want.comp.parts) || !irRetainedTupleKind(have) || !irRetainedTupleKind(want) {
		return ir.NoTemp, false
	}
	parts := make([]ir.Temp, len(have.comp.parts))
	for i, hp := range have.comp.parts {
		p := bl.tupleProjection(at, v, i, hp)
		w, ok := bl.rebuildPart(at, p, hp, want.comp.parts[i])
		if !ok {
			return ir.NoTemp, false
		}
		parts[i] = w
	}
	n := ir.NewMakeTuple(bl.g.irNodePos(at), bl.f.NewTemp(), parts)
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: want})
	return n.Dst(), true
}

// rebuildRecord makes an anonymous record again at want from each field.
func (bl *irScalarBuilder) rebuildRecord(at ast.Node, v ir.Temp, have, want kind) (ir.Temp, bool) {
	if have.comp == nil || want.comp == nil || !slices.Equal(have.comp.names, want.comp.names) || len(have.comp.parts) != len(want.comp.parts) ||
		!irRetainedRecordKind(have) || !irRetainedRecordKind(want) {
		return ir.NoTemp, false
	}
	ops := make([]ir.Temp, len(have.comp.parts))
	for i, name := range have.comp.names {
		hp := have.comp.parts[i]
		p := ir.NewProjRecordField(bl.g.irNodePos(at), bl.f.NewTemp(), v, name, irParamShape(hp))
		bl.b.Append(p)
		bl.side(p.Dst(), irScalarSide{k: hp})
		w, ok := bl.rebuildPart(at, p.Dst(), hp, want.comp.parts[i])
		if !ok {
			return ir.NoTemp, false
		}
		ops[i] = w
	}
	m := ir.NewMakeRecord(bl.g.irNodePos(at), bl.f.NewTemp(), want.comp.names, ops)
	bl.b.Append(m)
	bl.side(m.Dst(), irScalarSide{k: want})
	return m.Dst(), true
}

// rebuildStruct makes a user generic struct again at want from each field.
func (bl *irScalarBuilder) rebuildStruct(at ast.Node, v ir.Temp, have, want kind) (ir.Temp, bool) {
	hd, wd := have.def, want.def
	if !irRetainedStructKind(hd) || !irRetainedStructKind(wd) || len(hd.fields) != len(wd.fields) {
		return ir.NoTemp, false
	}
	ops := make([]ir.Temp, len(wd.fields))
	for i := range hd.fields {
		f := &hd.fields[i]
		if wd.fields[i].nomi != f.nomi {
			return ir.NoTemp, false
		}
		p := ir.NewProjField(bl.g.irNodePos(at), bl.f.NewTemp(), v,
			bl.g.irTypes().Symbol(f, f.nomi), f.nomi, irParamShape(f.k))
		bl.b.Append(p)
		bl.side(p.Dst(), irScalarSide{k: f.k})
		w, ok := bl.rebuildPart(at, p.Dst(), f.k, wd.fields[i].k)
		if !ok {
			return ir.NoTemp, false
		}
		ops[i] = w
	}
	m := ir.NewMakeStruct(bl.g.irNodePos(at), bl.f.NewTemp(), bl.g.irTypeSym(wd), irStructFieldNames(wd), ops)
	bl.b.Append(m)
	bl.side(m.Dst(), irScalarSide{k: want})
	return m.Dst(), true
}

func (bl *irScalarBuilder) rebuildList(at ast.Node, v ir.Temp, have, want kind) (ir.Temp, bool) {
	if have == kindEmptyList || want == kindEmptyList || have.comp == nil || want.comp == nil || len(have.comp.parts) != 1 || len(want.comp.parts) != 1 {
		return ir.NoTemp, false
	}
	return bl.rebuildMapped(at, v, ir.IterOverList, ir.IterToList, have.comp.parts[0], want.comp.parts[0], want)
}

func (bl *irScalarBuilder) rebuildMap(at ast.Node, v ir.Temp, have, want kind) (ir.Temp, bool) {
	if have == kindEmptyMap || want == kindEmptyMap || !irRetainedMapKind(have) || !irRetainedMapKind(want) {
		return ir.NoTemp, false
	}
	hp := bl.g.tupleKind([]kind{have.comp.parts[0], have.comp.parts[1]})
	wp := bl.g.tupleKind([]kind{want.comp.parts[0], want.comp.parts[1]})
	if !irRetainedTupleKind(hp) || !irRetainedTupleKind(wp) {
		return ir.NoTemp, false
	}
	fn, ok := bl.rebuildAdapter(at, hp, wp)
	if !ok {
		return ir.NoTemp, false
	}
	src := bl.mapView(at, v, have, hp)
	mapped := bl.iterAppend(at, ir.IterMap, ir.IterOverSeq, seqKindIn(bl.g, wp), src, fn)
	return bl.iterAppend(at, ir.IterToMap, ir.IterOverSeq, want, mapped), true
}

// rebuildMapped maps a collection's elements of kind he through their
// adapter to we, and materializes the result as want.
func (bl *irScalarBuilder) rebuildMapped(at ast.Node, v ir.Temp, over ir.IterDomain, to ir.IterOp, he, we, want kind) (ir.Temp, bool) {
	if !irCallableValueKind(he) || !irCallableValueKind(we) {
		return ir.NoTemp, false
	}
	fn, ok := bl.rebuildAdapter(at, he, we)
	if !ok {
		return ir.NoTemp, false
	}
	src := bl.iterAppend(at, ir.IterView, over, seqKindIn(bl.g, he), v)
	mapped := bl.iterAppend(at, ir.IterMap, ir.IterOverSeq, seqKindIn(bl.g, we), src, fn)
	return bl.iterAppend(at, to, ir.IterOverSeq, want, mapped), true
}

// rebuildAdapter answers a function value of kind (have) -> want that
// converts its argument: a generic enum's variants matched and made again,
// or any other part converted as rebuildPart does.
func (bl *irScalarBuilder) rebuildAdapter(at ast.Node, have, want kind) (ir.Temp, bool) {
	fk := funcKindIn(bl.g, []kind{have}, want)
	if !irCallableValueKind(fk) {
		return ir.NoTemp, false
	}
	key := [2]kind{have, want}
	if _, building := bl.g.rebuilding[key]; building {
		// The enum's own instance inside its payload (`Chain<T>` in its
		// `Link`, `List<Tree<T>>` in its `Node`): the adapter being built,
		// reached as its self parameter or as a capture of it.
		return bl.rebuildSelfRef(key)
	}

	pos := bl.g.irNodePos(at)
	f := ir.NewFunc(pos, "widen")
	sh := &irFuncShell{fn: f, syms: map[string]*ir.Symbol{}, params: map[string]ir.Temp{}, patternOK: true}
	sh.frame = newIRFuncFrame(f)
	sh.entry = f.NewBlock(pos, "entry")
	child := &irScalarBuilder{g: bl.g, sh: sh, f: f, b: sh.entry, parent: bl, bound: map[string]ir.Temp{}, boundK: map[string]kind{}, rebuildBody: true}
	x := bl.g.irAddParam(f, ir.NewSymbol("_"), have)
	var ok bool
	recursive := false
	if have.tag == tagNamed && have.def != nil && have.def.isEnum && want.def != nil && irSameGeneric(have.def, want.def) {
		// An enum's adapter takes itself as its last parameter, which the
		// recursive function value fills, so a payload of the same
		// instance is rebuilt by calling it.
		self := bl.g.irAddParam(f, ir.NewSymbol("_"), fk)
		if bl.g.rebuilding == nil {
			bl.g.rebuilding = map[[2]kind]irRebuildSelf{}
		}
		bl.g.rebuilding[key] = irRebuildSelf{f: f, self: self}
		ok = child.rebuildVariants(at, x, have.def, want.def)
		delete(bl.g.rebuilding, key)
		recursive = true
	} else {
		var out ir.Temp
		out, ok = child.rebuildPart(at, x, have, want)
		if ok {
			child.b.SetTerm(ir.NewReturn(pos, out))
		}
	}
	if !ok || ir.Lint(f) != nil {
		return ir.NoTemp, false
	}
	// The enclosing enum adapters this one reached through a collection,
	// each supplied from here as a capture: a trailing parameter of f.
	caps := make([]ir.Temp, len(child.rebuildCaps))
	for i, c := range child.rebuildCaps {
		if caps[i], ok = bl.rebuildSelfRef(c.key); !ok {
			return ir.NoTemp, false
		}
	}
	var n *ir.FuncValue
	if recursive {
		n = ir.NewRecursiveFuncValue(pos, bl.f.NewTemp(), f, caps...)
	} else {
		n = ir.NewFuncValue(pos, bl.f.NewTemp(), f, caps...)
	}
	bl.b.Append(n)
	bl.side(n.Dst(), irScalarSide{k: fk})
	return n.Dst(), true
}

// irRebuildSelf is an enum adapter being built and its self parameter.
type irRebuildSelf struct {
	f    *ir.Func
	self ir.Temp
}

// irRebuildCap is an enclosing enum adapter that an adapter nested inside it
// calls, and the nested adapter's parameter its capture fills.
type irRebuildCap struct {
	key   [2]kind
	param ir.Temp
}

// rebuildSelfRef answers the function value of the enum adapter being built
// for key, as reached from bl's function: the adapter's own self parameter,
// or, from an adapter nested inside it (a List's element adapter, a Map
// entry's tuple adapter), a parameter that the nested adapter's function
// value fills with a capture (rebuildAdapter). So `Node(List<Tree<T>>)`
// rebuilds each element by calling the adapter its Tree is rebuilt by.
func (bl *irScalarBuilder) rebuildSelfRef(key [2]kind) (ir.Temp, bool) {
	self, building := bl.g.rebuilding[key]
	if !building {
		return ir.NoTemp, false
	}
	if self.f == bl.f {
		return self.self, true
	}
	for _, c := range bl.rebuildCaps {
		if c.key == key {
			return c.param, true
		}
	}
	if !bl.rebuildBody {
		irDeclineNote("a value of " + key[0].nomi() + " where " + key[1].nomi() + " is expected, whose type holds itself")
		return ir.NoTemp, false
	}
	p := bl.g.irAddParam(bl.f, ir.NewSymbol("_"), funcKindIn(bl.g, []kind{key[0]}, key[1]))
	bl.rebuildCaps = append(bl.rebuildCaps, irRebuildCap{key: key, param: p})
	return p, true
}

// rebuildVariants is an adapter's body over x, a value of generic enum
// instance hd: one arm per variant, each making the same variant of wd from
// x's payloads converted to wd's. The last variant needs no test.
func (bl *irScalarBuilder) rebuildVariants(at ast.Node, x ir.Temp, hd, wd *typeDef) bool {
	pos := bl.g.irNodePos(at)
	for i := range hd.variants {
		hv, wv := &hd.variants[i], &wd.variants[i]
		if hv.nomi != wv.nomi || hv.kind != wv.kind || len(hv.payloads) != len(wv.payloads) {
			return false
		}
		next := (*ir.Block)(nil)
		if i < len(hd.variants)-1 {
			test := bl.variantTest(at, x, hd, hv)
			bl.b.Append(test)
			arm := bl.f.NewBlock(pos, "widen."+hv.nomi)
			next = bl.f.NewBlock(pos, "widen.next")
			bl.b.SetTerm(ir.NewBranch(pos, test.Dst(), arm.ID(), next.ID()))
			bl.b = arm
		}
		out, ok := bl.rebuildVariant(at, x, hd, hv, wd, wv)
		if !ok {
			return false
		}
		bl.b.SetTerm(ir.NewReturn(pos, out))
		if next != nil {
			bl.b = next
		}
	}
	return true
}

// rebuildVariant makes variant wv of wd from hv's payloads read off x.
func (bl *irScalarBuilder) rebuildVariant(at ast.Node, x ir.Temp, hd *typeDef, hv *variantDef, wd *typeDef, wv *variantDef) (ir.Temp, bool) {
	switch hv.kind {
	case "bare":
		v, _, _, ok := bl.variantValue(at, wd, wv, nil, true)
		return v, ok
	case "embedded":
		p := bl.variantPayload(at, x, hd, hv)
		v, _, ok := bl.embedWiden(at, p, wd, wv)
		return v, ok
	case "positional":
		if len(hv.payloads) != 1 {
			return ir.NoTemp, false
		}
		p, ok := bl.rebuildPayloadRead(at, x, hd, hv, 0)
		if !ok {
			return ir.NoTemp, false
		}
		w, ok := bl.rebuildPart(at, p, hv.payloads[0].k, wv.payloads[0].k)
		if !ok {
			return ir.NoTemp, false
		}
		v, _, _, ok := bl.variantValue(at, wd, wv, []ir.Temp{w}, true)
		return v, ok
	case "struct":
		args := make([]ir.Temp, len(hv.payloads))
		for j := range hv.payloads {
			if hv.payloads[j].nomi != wv.payloads[j].nomi {
				return ir.NoTemp, false
			}
			p, ok := bl.rebuildPayloadRead(at, x, hd, hv, j)
			if !ok {
				return ir.NoTemp, false
			}
			w, ok := bl.rebuildPart(at, p, hv.payloads[j].k, wv.payloads[j].k)
			if !ok {
				return ir.NoTemp, false
			}
			args[j] = w
		}
		v, _, _, ok := bl.variantValue(at, wd, wv, args, true)
		return v, ok
	}
	return ir.NoTemp, false
}

// rebuildPayloadRead reads payload j of hv off x. A zero-sized payload has no
// slot; a marker or Unit is made where it is needed, and any other one
// declines.
func (bl *irScalarBuilder) rebuildPayloadRead(at ast.Node, x ir.Temp, hd *typeDef, hv *variantDef, j int) (ir.Temp, bool) {
	part := hv.payloads[j]
	pos := bl.g.irNodePos(at)
	if part.slot < 0 {
		switch {
		case part.k == kindUnit:
			c := ir.NewUnit(pos, bl.f.NewTemp())
			bl.b.Append(c)
			bl.side(c.Dst(), irScalarSide{k: part.k})
			return c.Dst(), true
		case part.k.tag == tagNamed && part.k.def != nil && irRetainedMarker(part.k.def):
			c := ir.NewMarker(pos, bl.f.NewTemp(), bl.g.irTypeSym(part.k.def))
			bl.b.Append(c)
			bl.side(c.Dst(), irScalarSide{k: part.k})
			return c.Dst(), true
		}
		return ir.NoTemp, false
	}
	var p *ir.Proj
	if hv.kind == "struct" {
		p = ir.NewProjPayloadField(pos, bl.f.NewTemp(), x, bl.g.irTypeSym(hd), hv.nomi, part.nomi, j, irParamShape(part.k))
	} else {
		p = ir.NewProjPayload(pos, bl.f.NewTemp(), x, bl.g.irTypeSym(hd), hv.nomi, j, irParamShape(part.k))
	}
	bl.b.Append(p)
	bl.side(p.Dst(), irScalarSide{k: part.k})
	return p.Dst(), true
}
