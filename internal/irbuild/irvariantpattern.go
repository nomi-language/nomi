package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// variantOf is the variant of k's enum pat's head names, which the checker
// records (PatternVariants): it types a variant pattern against the
// subject's enum. A variant that is not struct-shaped carries one payload,
// a tuple for `V(Int, String)`.
func (g *gen) variantOf(pat *ast.EnumPattern, k kind) *variantDef {
	member, ok := g.patternVariant(pat.Variant)
	if !ok {
		panic("irbuild: a variant pattern whose head does not name the subject's enum " + k.def.nomi)
	}
	v := k.def.variant(member)
	if v == nil {
		panic("irbuild: " + k.def.nomi + " has no variant " + member + ", which the checker recorded for a pattern's head")
	}
	if v.kind != "struct" && len(v.payloads) > 1 {
		panic("irbuild: the variant " + k.def.nomi + "." + member + " carries several payloads outside a struct shape")
	}
	return v
}

// irAttachedVariantPattern reads a literal-attach pattern over an enum
// (`.Obj{"k" => v}`, `.Arr[a, b]`) as the variant pattern it means,
// `.Obj({"k" => v})`: a typed map or list pattern's prefix is the variant's
// name, and the payload is matched against the anonymous pattern. The
// variant pattern carries the typed pattern's record (carryPattern); the
// anonymous payload stands for no pattern the checker checked, and is
// tested by its own shape. Any other pattern is returned unchanged.
func (g *gen) irAttachedVariantPattern(pattern ast.Node) ast.Node {
	var variant *ast.EnumPattern
	switch p := pattern.(type) {
	case *ast.MapPattern:
		if p.TypeName == nil {
			return pattern
		}
		anon := *p
		anon.TypeName = nil
		variant = &ast.EnumPattern{Variant: p.TypeName, Payload: &anon, Line: p.Line, Col: p.Col}
	case *ast.ListPattern:
		if p.TypeName == nil {
			return pattern
		}
		anon := *p
		anon.TypeName = nil
		variant = &ast.EnumPattern{Variant: p.TypeName, Payload: &anon, Line: p.Line, Col: p.Col}
	default:
		return pattern
	}
	return g.carryPattern(variant, pattern)
}

// enumCaseTest tests a variant pattern over subj, a Bool or an enum: the
// tag, then on its success edge the payload read and matched, leaving the
// builder where the payload's test passed with the names bound.
func (bl *irScalarBuilder) enumCaseTest(pattern ast.Node, subj ir.Temp, k kind, next *ir.Block) bool {
	if k == kindBool {
		pat, ok := pattern.(*ast.EnumPattern)
		if !ok {
			return false
		}
		member, ok := bl.g.patternVariant(pat.Variant)
		if !ok || (member != "True" && member != "False") {
			return false
		}
		// `Bool.False(x)` binds the embedded singleton: a value of std/bool's
		// `host type False`, NOT the Bool. It is a marker of that type, so
		// x's impls are False's own; binding the Bool would dispatch them to
		// Bool's, whose derived Display (`False(x) -> Display.to_string(x)`)
		// would then call itself forever.
		binding := pat.Binding
		if id, named := pat.Payload.(*ast.IdentPattern); named && binding == "" {
			binding = id.Name
		} else if _, wild := pat.Payload.(*ast.WildcardPattern); !wild && pat.Payload != nil {
			return false
		}
		singleton := kindInvalid
		if binding != "" {
			// kindInvalid: lookup — std/bool's host marker row; a miss declines.
			if singleton = stdHostOriginKind("std/bool", member); singleton == kindInvalid {
				panic("irbuild: enumCaseTest: std/bool's host marker rows always exist")
			}
		}
		match := ir.NewMatchVariantInto(bl.g.irNodePos(pat), bl.f.NewTemp(), subj,
			bl.g.irTypes().Symbol(irBoolEnumToken, "Bool"), member)
		bl.b.Append(match)
		bl.side(match.Dst(), irScalarSide{k: kindBool})
		bl.passOrFail(pat, match.Dst(), next)
		if binding != "" {
			c := ir.NewMarker(bl.g.irNodePos(pat), bl.f.NewTemp(), bl.g.irTypeSym(singleton.def))
			bl.b.Append(c)
			bl.side(c.Dst(), irScalarSide{k: singleton})
			bl.patternBinding(pat, binding, c.Dst(), singleton)
		}
		return true
	}
	if sp, isStruct := pattern.(*ast.StructPattern); isStruct {
		return bl.structVariantCaseTest(sp, subj, k, next)
	}
	if k.tag != tagNamed || !irRetainedEnumKind(k.def) {
		bl.g.dc.note("a variant pattern over " + k.nomi() + ", which is no retained enum")
		return false
	}
	pat := bl.g.irAttachedVariantPattern(pattern).(*ast.EnumPattern)
	v := bl.g.variantOf(pat, k)
	bl.variantTagTest(pat, subj, k.def, v, next)
	if _, wild := pat.Payload.(*ast.WildcardPattern); pat.Binding == "" && (pat.Payload == nil || wild) {
		return true
	}
	value, vk, ok := bl.variantPatternValue(pat, subj, k.def, v)
	if !ok {
		return false
	}
	if pat.Binding != "" {
		bl.patternBinding(pat, pat.Binding, value, vk)
	}
	if pat.Payload == nil {
		return true
	}
	return bl.patternTest(pat.Payload, value, vk, next)
}

// variantTagTest tests whether subj is the variant v of d that pat names,
// branching to next when it is not. A pattern the checker records as
// matching every value (the variant of a one-variant enum) names the only
// variant subj can be, so it has no tag to test.
func (bl *irScalarBuilder) variantTagTest(pat ast.Node, subj ir.Temp, d *typeDef, v *variantDef, next *ir.Block) {
	if bl.g.patternAlwaysMatches(pat) {
		return
	}
	match := bl.variantTest(pat, subj, d, v)
	bl.b.Append(match)
	bl.side(match.Dst(), irScalarSide{k: kindBool})
	bl.passOrFail(pat, match.Dst(), next)
}

// variantTest asks whether subj is variant v of d. An `embeds` variant's test
// names the embedded type, which a widened value may still be.
func (bl *irScalarBuilder) variantTest(at ast.Node, subj ir.Temp, d *typeDef, v *variantDef) *ir.Match {
	if v.kind == "embedded" {
		return ir.NewMatchVariantEmbedInto(bl.g.irNodePos(at), bl.f.NewTemp(), subj, bl.g.irTypeSym(d), v.nomi,
			bl.g.irTypeSym(v.embeds))
	}
	return ir.NewMatchVariantInto(bl.g.irNodePos(at), bl.f.NewTemp(), subj, bl.g.irTypeSym(d), v.nomi)
}

// irSlotlessValue is the one value of a slotless payload's type
// (irSlotlessPayload): Unit, or the marker itself.
func (bl *irScalarBuilder) irSlotlessValue(at ast.Node, k kind) ir.Temp {
	var c *ir.Const
	if k == kindUnit {
		c = ir.NewUnit(bl.g.irNodePos(at), bl.f.NewTemp())
	} else {
		c = ir.NewMarker(bl.g.irNodePos(at), bl.f.NewTemp(), bl.g.irTypeSym(k.def))
	}
	bl.b.Append(c)
	bl.side(c.Dst(), irScalarSide{k: k})
	return c.Dst()
}

// variantPayload reads v's single payload off subj. A payload with no slot,
// Unit or a marker, embedded or not, is made from its type.
func (bl *irScalarBuilder) variantPayload(at ast.Node, subj ir.Temp, d *typeDef, v *variantDef) ir.Temp {
	part := v.payloads[0]
	if part.slot < 0 {
		return bl.irSlotlessValue(at, part.k)
	}
	var projection *ir.Proj
	if v.kind == "embedded" {
		projection = ir.NewProjPayloadEmbed(bl.g.irNodePos(at), bl.f.NewTemp(), subj,
			bl.g.irTypeSym(d), v.nomi, bl.g.irTypeSym(v.embeds), irParamShape(part.k))
	} else {
		projection = ir.NewProjPayload(bl.g.irNodePos(at), bl.f.NewTemp(), subj,
			bl.g.irTypeSym(d), v.nomi, 0, irParamShape(part.k))
	}
	bl.b.Append(projection)
	bl.side(projection.Dst(), irScalarSide{k: part.k})
	return projection.Dst()
}

// variantPatternValue reads the value variant v's pattern takes apart off subj,
// which the checker types the payload pattern against: a struct-shaped
// variant's record of its fields (variantRecord), an `embeds` of a
// distinct over a value's inner value (spec §4 *Enums (Algebraic Data
// Types)*: `Identifier.UserId(name)` binds the String, `Shape.Wrapped({r:})`
// matches the Circle), and any other variant's one payload.
func (bl *irScalarBuilder) variantPatternValue(at ast.Node, subj ir.Temp, d *typeDef, v *variantDef) (ir.Temp, kind, bool) {
	if v.kind == "struct" {
		return bl.variantRecord(at, subj, d, v)
	}
	value, k := bl.variantPayload(at, subj, d, v), v.payloads[0].k
	if irEmbedsValueDistinct(v) {
		value, k = bl.distinctProjection(at, value, k, false, "irvariantpattern.go variantPatternValue"), v.embeds.inner
	}
	return value, k, true
}

// variantRecord reads struct-shaped variant v's fields off subj as the
// anonymous record of them, which is the checker's type for the variant's
// payload.
func (bl *irScalarBuilder) variantRecord(at ast.Node, subj ir.Temp, d *typeDef, v *variantDef) (ir.Temp, kind, bool) {
	names := make([]string, len(v.payloads))
	parts := make([]kind, len(v.payloads))
	values := make(map[string]ir.Temp, len(v.payloads))
	for i, part := range v.payloads {
		projection := ir.NewProjPayloadField(bl.g.irNodePos(at), bl.f.NewTemp(), subj,
			bl.g.irTypeSym(d), v.nomi, part.nomi, i, irParamShape(part.k))
		bl.b.Append(projection)
		bl.side(projection.Dst(), irScalarSide{k: part.k})
		names[i], parts[i], values[part.nomi] = part.nomi, part.k, projection.Dst()
	}
	k := bl.g.anonStructKind(names, parts)
	if !irRetainedRecordKind(k) {
		bl.g.dc.note("a struct-shaped variant's payload outside the retained records: " + k.nomi())
		return ir.NoTemp, kindInvalid, false
	}
	ops := make([]ir.Temp, len(k.comp.names))
	for i, n := range k.comp.names {
		ops[i] = values[n]
	}
	rec := ir.NewMakeRecord(bl.g.irNodePos(at), bl.f.NewTemp(), k.comp.names, ops)
	bl.b.Append(rec)
	bl.side(rec.Dst(), irScalarSide{k: k, pureMake: true})
	return rec.Dst(), k, true
}

// structVariantCaseTest matches a struct-shaped variant and binds its
// fields: the tag test, then each field in the pattern's order, read only on
// its success edge (structFieldsTest).
func (bl *irScalarBuilder) structVariantCaseTest(pat *ast.StructPattern, subj ir.Temp, k kind, next *ir.Block) bool {
	if pat.TypeName == nil || k.tag != tagNamed || !irRetainedEnumKind(k.def) {
		return false
	}
	member, ok := bl.g.patternVariant(pat.TypeName)
	if !ok {
		// The checker types the pattern against the subject's enum and
		// records the variant its head names.
		panic("irbuild: a struct variant pattern whose head does not name the subject's enum " + k.def.nomi)
	}
	d := k.def
	v := d.variant(member)
	if v != nil && v.kind == "embedded" && irRetainedStructKind(v.embeds) {
		return bl.embeddedStructCaseTest(pat, subj, d, v, next)
	}
	if v == nil || v.kind != "struct" {
		return false
	}
	bl.variantTagTest(pat, subj, d, v, next)
	return bl.structFieldsTest(pat, next, func(f ast.StructPatternField) (ir.Temp, kind) {
		for i, part := range v.payloads {
			if part.nomi != f.Name {
				continue
			}
			projection := ir.NewProjPayloadField(bl.g.irNodePos(pat), bl.f.NewTemp(), subj,
				bl.g.irTypeSym(d), v.nomi, part.nomi, i, irParamShape(part.k))
			bl.b.Append(projection)
			bl.side(projection.Dst(), irScalarSide{k: part.k})
			return projection.Dst(), part.k
		}
		// The checker resolves each field of the pattern on the variant.
		panic("irbuild: a struct variant pattern's field " + f.Name + " the variant does not have")
	})
}

// embeddedStructCaseTest matches an `embeds` variant of a struct and binds
// the embedded struct's fields: the tag test, then each field in the
// pattern's order read off the payload, only on its success edge.
func (bl *irScalarBuilder) embeddedStructCaseTest(pat *ast.StructPattern, subj ir.Temp, d *typeDef, v *variantDef, next *ir.Block) bool {
	bl.variantTagTest(pat, subj, d, v, next)
	payload := bl.variantPayload(pat, subj, d, v)
	return bl.structFieldsTest(pat, next, func(f ast.StructPatternField) (ir.Temp, kind) {
		fd := v.embeds.field(f.Name)
		if fd == nil {
			// As for a struct-shaped variant's field (structVariantCaseTest).
			panic("irbuild: an embedded struct pattern's field " + f.Name + " the struct does not have")
		}
		projection := ir.NewProjField(bl.g.irNodePos(pat), bl.f.NewTemp(), payload,
			bl.g.irTypes().Symbol(fd, f.Name), f.Name, irParamShape(fd.k))
		bl.b.Append(projection)
		bl.side(projection.Dst(), irScalarSide{k: fd.k})
		return projection.Dst(), fd.k
	})
}
