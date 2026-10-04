package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

func (g *gen) retainedEnumPattern(pattern ast.Node, k kind) (*ast.EnumPattern, *variantDef, bool) {
	pat, ok := pattern.(*ast.EnumPattern)
	if !ok || k.tag != tagNamed || !irRetainedEnumKind(k.def) {
		return nil, nil, false
	}
	owner, member, ok := patternHead(pat.Variant)
	if !ok || (owner != "" && !g.declaredAs(owner, k.def) && !g.preludeOwns(owner, k.def) && !g.stdEnumOwns(owner, k.def)) {
		return nil, nil, false
	}
	v := k.def.variant(member)
	if v == nil {
		return nil, nil, false
	}
	if pat.Binding == "" && pat.Payload == nil {
		return pat, v, true
	}
	if v.kind == "struct" {
		// `.One(p)` over a struct-shaped variant binds the whole payload as
		// the record of its fields, which is the checker's type for p
		// (variantRecordBinding); `.One(_)` binds nothing.
		switch pat.Payload.(type) {
		case nil, *ast.IdentPattern, *ast.WildcardPattern:
			return pat, v, true
		}
		return nil, nil, false
	}
	if len(v.payloads) != 1 {
		return nil, nil, false
	}
	if attached := irAttachedVariantPattern(pat.Payload); attached != pat.Payload {
		// `Some(.Obj{"k" => v})`: the payload is a literal-attach pattern,
		// which is the nested variant pattern `.Obj({"k" => v})`.
		copied := *pat
		copied.Payload = attached
		pat = &copied
	}
	switch pat.Payload.(type) {
	case nil, *ast.WildcardPattern, *ast.IdentPattern:
	case *ast.IntLit:
		if v.payloads[0].k != kindInt {
			return nil, nil, false
		}
	case *ast.StringLit:
		if v.payloads[0].k != kindString {
			return nil, nil, false
		}
	case *ast.FloatLit:
		if v.payloads[0].k != kindFloat {
			return nil, nil, false
		}
	case *ast.DecimalLit:
		if !isDecimalKind(v.payloads[0].k) {
			return nil, nil, false
		}
	case *ast.CodepointLit:
		if v.payloads[0].k != codepointKind() {
			return nil, nil, false
		}
	case *ast.EnumPattern, *ast.StructPattern:
		// A nested variant pattern, tested against the projected payload in
		// the outer test's success arm, as matchArm recurses. A distinct's
		// destructure (`Some(TraceId(id))`) cannot fail and binds its inner
		// value.
		if sub, isEnum := pat.Payload.(*ast.EnumPattern); isEnum && pat.Binding == "" && g.distinctSubPattern(sub, v.payloads[0].k) {
			break
		}
		if pat.Binding != "" || !(irNestedPatternKind(v.payloads[0].k) || irNominalCaseKind(v.payloads[0].k)) {
			return nil, nil, false
		}
	case *ast.MapPattern:
		// A map pattern over a map payload (`Some({"k" => v})`), tested
		// against the projected payload as a map case arm is.
		if k := v.payloads[0].k; pat.Binding != "" || k.tag != tagMap || !irRetainedMapKind(k) {
			return nil, nil, false
		}
	case *ast.ListPattern:
		// A list pattern over a list payload (`.Arr[a, ..rest]`), tested as
		// a list case arm is.
		if k := v.payloads[0].k; pat.Binding != "" || !(irRetainedListKind(k) || irListTransportKind(k)) {
			return nil, nil, false
		}
	case *ast.TuplePattern:
		// A flat tuple pattern over a tuple payload (`Some((n, _))`), tested
		// against the projected payload as a tuple case arm is.
		if _, ok := tupleCaseComponents(pat.Payload, v.payloads[0].k); pat.Binding != "" || !ok {
			return nil, nil, false
		}
	default:
		return nil, nil, false
	}
	return pat, v, true
}

// distinctSubPattern reports whether p destructures a wrapping distinct of
// kind k, `TraceId(id)` or `TraceId(_)`, which matches every value of k.
func (g *gen) distinctSubPattern(p *ast.EnumPattern, k kind) bool {
	if k.tag != tagNamed || k.def == nil || !irWrappingDistinct(k.def) || k.def.rtOpaque {
		return false
	}
	owner, member, ok := patternHead(p.Variant)
	if !ok || owner != "" || g.qualifierKind(member) != k {
		return false
	}
	switch p.Payload.(type) {
	case nil, *ast.WildcardPattern, *ast.IdentPattern:
		return p.Payload == nil || p.Binding == ""
	}
	return false
}

// distinctSubBinding binds a distinct sub-pattern's name to the payload's
// inner value.
func (bl *irScalarBuilder) distinctSubBinding(p *ast.EnumPattern, payload ir.Temp, k kind) {
	name, at := p.Binding, ast.Node(p)
	if id, isIdent := p.Payload.(*ast.IdentPattern); isIdent {
		name, at = id.Name, id
	}
	if name == "" {
		return
	}
	inner := bl.distinctProjection(p, payload, k, false, "irvariantpattern.go distinctSubBinding")
	bl.patternBinding(at, name, inner, k.def.inner)
}

// irAttachedVariantPattern reads a literal-attach pattern over an enum
// (`.Obj{"k" => v}`, `.Arr[a, b]`) as the variant pattern it means,
// `.Obj({"k" => v})`: a typed map or list pattern's prefix is the variant's
// name, and the payload is matched against the anonymous pattern. Any other pattern is returned unchanged.
func irAttachedVariantPattern(pattern ast.Node) ast.Node {
	switch p := pattern.(type) {
	case *ast.MapPattern:
		if p.TypeName == nil {
			return pattern
		}
		inner := *p
		inner.TypeName = nil
		return &ast.EnumPattern{Variant: p.TypeName, Payload: &inner, Line: p.Line, Col: p.Col}
	case *ast.ListPattern:
		if p.TypeName == nil {
			return pattern
		}
		inner := *p
		inner.TypeName = nil
		return &ast.EnumPattern{Variant: p.TypeName, Payload: &inner, Line: p.Line, Col: p.Col}
	}
	return pattern
}

// irNestedPatternKind is a payload a nested variant pattern may test: a
// retained enum or a Bool (`Ok(True)`), whose inner test enumCaseTest builds.
func irNestedPatternKind(k kind) bool {
	return k == kindBool || (k.tag == tagNamed && irRetainedEnumKind(k.def))
}

func (bl *irScalarBuilder) enumCaseTest(pattern ast.Node, subj ir.Temp, k kind, arm, next *ir.Block) bool {
	if k == kindBool {
		pat, ok := pattern.(*ast.EnumPattern)
		if !ok {
			return false
		}
		owner, member, ok := patternHead(pat.Variant)
		if !ok || (owner != "" && owner != "Bool") || (member != "True" && member != "False") {
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
				return false
			}
		}
		match := ir.NewMatchVariantInto(bl.g.irNodePos(pat), bl.f.NewTemp(), subj,
			bl.g.irTypes().Symbol(irBoolEnumToken, "Bool"), member)
		bl.b.Append(match)
		bl.side(match.Dst(), irScalarSide{k: kindBool})
		bl.b.SetTerm(ir.NewBranch(bl.g.irNodePos(pat), match.Dst(), arm.ID(), next.ID()))
		bl.b = arm
		if binding != "" {
			c := ir.NewMarker(bl.g.irNodePos(pat), bl.f.NewTemp(), bl.g.irTypeSym(singleton.def))
			bl.b.Append(c)
			bl.side(c.Dst(), irScalarSide{k: singleton})
			bl.patternBinding(pat, binding, c.Dst(), singleton)
		}
		return true
	}
	if sp, isStruct := pattern.(*ast.StructPattern); isStruct {
		return bl.structVariantCaseTest(sp, subj, k, arm, next)
	}
	pattern = irAttachedVariantPattern(pattern)
	pat, v, ok := bl.g.retainedEnumPattern(pattern, k)
	if !ok {
		irDeclineNote("a `case` enum pattern outside retained bare/positional payload tests")
		return false
	}
	match := bl.variantTest(pat, subj, k.def, v)
	bl.b.Append(match)
	bl.side(match.Dst(), irScalarSide{k: kindBool})
	if v.kind == "struct" {
		bl.b.SetTerm(ir.NewBranch(bl.g.irNodePos(pat), match.Dst(), arm.ID(), next.ID()))
		bl.b = arm
		return bl.variantRecordBinding(pat, subj, k.def, v)
	}
	_, intTest := pat.Payload.(*ast.IntLit)
	_, stringTest := pat.Payload.(*ast.StringLit)
	_, floatTest := pat.Payload.(*ast.FloatLit)
	_, decimalTest := pat.Payload.(*ast.DecimalLit)
	_, codepointTest := pat.Payload.(*ast.CodepointLit)
	literal := intTest || stringTest || floatTest || decimalTest || codepointTest
	subEnum, nestedEnum := pat.Payload.(*ast.EnumPattern)
	distinctSub := nestedEnum && bl.g.distinctSubPattern(subEnum, v.payloads[0].k)
	nestedEnum = nestedEnum && !distinctSub
	_, nestedStruct := pat.Payload.(*ast.StructPattern)
	nested := nestedEnum || nestedStruct
	_, nestedTuple := pat.Payload.(*ast.TuplePattern)
	tupleTests := nestedTuple && !tupleCaseIrrefutable(pat.Payload, v.payloads[0].k)
	_, nestedMap := pat.Payload.(*ast.MapPattern)
	_, nestedList := pat.Payload.(*ast.ListPattern)
	success := arm
	if literal || nested || tupleTests || nestedMap || nestedList {
		success = bl.f.NewBlock(bl.g.irNodePos(pat.Payload), "payload test")
	}
	bl.b.SetTerm(ir.NewBranch(bl.g.irNodePos(pat), match.Dst(), success.ID(), next.ID()))
	bl.b = success
	if pat.Binding == "" && pat.Payload == nil {
		return true
	}
	if irEmbedsEnum(v) {
		// `Drawable.Shape(s)`: the variant's one value carries no payload,
		// so there is nothing to bind; an arm that reads the name declines
		// where it reads it.
		switch pat.Payload.(type) {
		case nil, *ast.IdentPattern, *ast.WildcardPattern:
			return true
		}
		return false
	}
	value, payloadKind := bl.enumPatternPayload(pat, subj, k.def, v)
	if distinctSub {
		bl.distinctSubBinding(subEnum, value, payloadKind)
		return true
	}
	if nestedTuple {
		if tupleTests {
			return bl.tupleCaseTest(pat.Payload, value, payloadKind, arm, next)
		}
		bl.tupleCaseBindings(pat.Payload, value, payloadKind)
		return true
	}
	if nestedMap {
		return bl.mapCaseTest(pat.Payload, value, payloadKind, arm, next)
	}
	if nestedList {
		return bl.listCaseTest(pat.Payload, value, payloadKind, arm, next)
	}
	if nested {
		// The inner test reads the payload inline and branches to the same
		// arm and the same next arm, so a failed inner test abandons the
		// whole arm as matchArm's nested block does.
		return bl.patternValueTest(pat.Payload, value, payloadKind, arm, next)
	}
	if literal {
		lit, _, _, ok := bl.litFor(pat.Payload, payloadKind)
		if !ok {
			return false
		}
		match := ir.NewMatchLitInto(bl.g.irNodePos(pat.Payload), bl.f.NewTemp(), value, lit)
		bl.b.Append(match)
		bl.b.SetTerm(ir.NewBranch(bl.g.irNodePos(pat.Payload), match.Dst(), arm.ID(), next.ID()))
		bl.b = arm
	}
	return true
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

// variantPayload reads v's single payload off subj, as payloadValue does. A
// zero-sized embedded marker has no slot and is reconstructed from its type.
func (bl *irScalarBuilder) variantPayload(at ast.Node, subj ir.Temp, d *typeDef, v *variantDef) ir.Temp {
	part := v.payloads[0]
	if part.slot < 0 {
		c := ir.NewMarker(bl.g.irNodePos(at), bl.f.NewTemp(), bl.g.irTypeSym(v.embeds))
		bl.b.Append(c)
		bl.side(c.Dst(), irScalarSide{k: part.k})
		return c.Dst()
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

func (bl *irScalarBuilder) enumPatternPayload(pat *ast.EnumPattern, subj ir.Temp, d *typeDef, v *variantDef) (ir.Temp, kind) {
	part := v.payloads[0]
	projection := bl.variantPayload(pat, subj, d, v)
	if v.kind == "embedded" && irWrappingDistinct(v.embeds) && (pat.Binding != "" || irIsIdentPattern(pat.Payload)) {
		// `Identifier.UserId(name)`: the name binds the embedded distinct's
		// inner value.
		projection = bl.distinctProjection(pat, projection, part.k, false, "irvariantpattern.go enumPatternPayload")
		part.k = v.embeds.inner
	}
	if pat.Binding != "" {
		bl.patternBinding(pat, pat.Binding, projection, part.k)
	} else if id, binding := pat.Payload.(*ast.IdentPattern); binding {
		bl.patternBinding(id, id.Name, projection, part.k)
	}
	return projection, part.k
}

// variantRecordBinding binds `.V(p)` over a struct-shaped variant V: p is the
// anonymous record of V's fields, built from each field's projection. A
// wildcard or no payload binds nothing.
func (bl *irScalarBuilder) variantRecordBinding(pat *ast.EnumPattern, subj ir.Temp, d *typeDef, v *variantDef) bool {
	name := pat.Binding
	var at ast.Node = pat
	if id, isIdent := pat.Payload.(*ast.IdentPattern); isIdent && name == "" {
		name, at = id.Name, id
	}
	if name == "" {
		return true
	}
	rec, k, ok := bl.variantRecord(pat, subj, d, v)
	if !ok {
		return false
	}
	bl.patternBinding(at, name, rec, k)
	return true
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
		irDeclineNote("a struct-shaped variant's payload outside the retained records: " + k.nomi())
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

// structVariantCaseTest matches a struct-shaped variant and binds its fields,
// as structArm and variantFieldArms do: the tag test, then each field in the
// pattern's order, projected only in the success arm. A field's literal
// sub-pattern tests the projected field and opens the next field's block, as
// variantFieldArms recurses through matchArm.
func (bl *irScalarBuilder) structVariantCaseTest(pat *ast.StructPattern, subj ir.Temp, k kind, arm, next *ir.Block) bool {
	if pat.TypeName == nil || k.tag != tagNamed || !irRetainedEnumKind(k.def) {
		return false
	}
	owner, member, ok := patternHead(pat.TypeName)
	if !ok || (owner != "" && !bl.g.declaredAs(owner, k.def) && !bl.g.preludeOwns(owner, k.def) && !bl.g.stdEnumOwns(owner, k.def)) {
		return false
	}
	d := k.def
	v := d.variant(member)
	if v != nil && v.kind == "embedded" && irRetainedStructKind(v.embeds) {
		return bl.embeddedStructCaseTest(pat, subj, d, v, arm, next)
	}
	if v == nil || v.kind != "struct" {
		return false
	}
	idx := make([]int, len(pat.Fields))
	for i, f := range pat.Fields {
		idx[i] = -1
		for j := range v.payloads {
			if v.payloads[j].nomi == f.Name {
				idx[i] = j
			}
		}
		if idx[i] < 0 || !irFieldSubPattern(f.Pattern, v.payloads[idx[i]].k) {
			return false
		}
	}
	match := ir.NewMatchVariantInto(bl.g.irNodePos(pat), bl.f.NewTemp(), subj, bl.g.irTypeSym(d), v.nomi)
	bl.b.Append(match)
	bl.side(match.Dst(), irScalarSide{k: kindBool})
	tests := irFieldTests(pat)
	tagged := bl.b
	target := irFieldTestTarget(bl, pat, tests, arm)
	tagged.SetTerm(ir.NewBranch(bl.g.irNodePos(pat), match.Dst(), target.ID(), next.ID()))
	for i, f := range pat.Fields {
		if _, wild := f.Pattern.(*ast.WildcardPattern); wild {
			// matchArm binds and tests nothing for `_`, so nothing is read.
			continue
		}
		part := v.payloads[idx[i]]
		projection := ir.NewProjPayloadField(bl.g.irNodePos(pat), bl.f.NewTemp(), subj,
			bl.g.irTypeSym(d), v.nomi, part.nomi, idx[i], irParamShape(part.k))
		bl.b.Append(projection)
		bl.side(projection.Dst(), irScalarSide{k: part.k})
		if !bl.fieldSubTest(pat, f, projection.Dst(), part.k, &tests, arm, next) {
			return false
		}
	}
	return true
}

// irFieldSubPattern reports whether a struct-pattern field's sub-pattern is
// one the retained tests carry: none, a name, a wildcard, or an Int or String
// literal of the field's kind. Retained struct and struct-variant fields are
// unboxed scalar leaves, so a nested variant pattern never reaches a field.
func irFieldSubPattern(p ast.Node, k kind) bool {
	switch p.(type) {
	case nil, *ast.IdentPattern, *ast.WildcardPattern:
		return true
	case *ast.IntLit:
		return k == kindInt
	case *ast.StringLit:
		return k == kindString
	case *ast.FloatLit:
		return k == kindFloat
	case *ast.DecimalLit:
		return isDecimalKind(k)
	case *ast.CodepointLit:
		return k == codepointKind()
	case *ast.EnumPattern:
		// `retryable: True`, `kind: .Some(n)`: a variant test on the field,
		// which patternValueTest builds; `Id(n)` over a distinct unwraps it.
		return k == kindBool || (k.tag == tagNamed && irRetainedEnumKind(k.def)) || (k.tag == tagNamed && irWrappingDistinct(k.def))
	case *ast.StructPattern:
		return k.tag == tagNamed && (irRetainedEnumKind(k.def) || irRetainedStructKind(k.def))
	case *ast.TuplePattern:
		return irRetainedTupleKind(k)
	case *ast.MapPattern:
		return k.tag == tagMap && irRetainedMapKind(k)
	case *ast.ListPattern:
		return irRetainedListKind(k) || irListTransportKind(k)
	}
	return false
}

// irFieldTests counts the fields whose sub-pattern is a test.
func irFieldTests(pat *ast.StructPattern) int {
	n := 0
	for _, f := range pat.Fields {
		switch f.Pattern.(type) {
		case nil, *ast.IdentPattern, *ast.WildcardPattern:
		default:
			n++
		}
	}
	return n
}

// irFieldTestTarget is where a passed test continues: a fresh block while a
// field test remains, and the arm after the last one. So each field test
// opens one nested Go block in field order, as structFieldArms and
// variantFieldArms recurse through matchArm, and a later field is projected
// only inside the tests before it.
func irFieldTestTarget(bl *irScalarBuilder, at ast.Node, remaining int, arm *ir.Block) *ir.Block {
	if remaining == 0 {
		bl.b = arm
		return arm
	}
	b := bl.f.NewBlock(bl.g.irNodePos(at), "payload test")
	bl.b = b
	return b
}

// fieldSubTest binds or tests one projected field. A literal test branches
// to the next field's block, or to the arm after the last test, and to next
// on failure.
func (bl *irScalarBuilder) fieldSubTest(pat *ast.StructPattern, f ast.StructPatternField, val ir.Temp, k kind, remaining *int, arm, next *ir.Block) bool {
	switch p := f.Pattern.(type) {
	case nil:
		bl.patternBinding(pat, f.Binding, val, k)
		return true
	case *ast.IdentPattern:
		bl.patternBinding(p, p.Name, val, k)
		return true
	}
	*remaining--
	cur := bl.b
	target := irFieldTestTarget(bl, f.Pattern, *remaining, arm)
	bl.b = cur
	p := f.Pattern
	switch p.(type) {
	case *ast.IntLit, *ast.StringLit, *ast.FloatLit, *ast.DecimalLit, *ast.CodepointLit:
	default:
		return bl.patternValueTest(p, val, k, target, next)
	}
	lit, _, _, ok := bl.litFor(p, k)
	if !ok {
		return false
	}
	match := ir.NewMatchLitInto(bl.g.irNodePos(p), bl.f.NewTemp(), val, lit)
	bl.b.Append(match)
	bl.b.SetTerm(ir.NewBranch(bl.g.irNodePos(p), match.Dst(), target.ID(), next.ID()))
	bl.b = target
	return true
}

// embeddedStructCaseTest matches an `embeds` variant of a struct and binds
// the embedded struct's fields, as structFieldArms does over payloadValue:
// the tag test, then each field in the pattern's order read off the payload,
// only in the success arm.
func (bl *irScalarBuilder) embeddedStructCaseTest(pat *ast.StructPattern, subj ir.Temp, d *typeDef, v *variantDef, arm, next *ir.Block) bool {
	fields := make([]*fieldDef, len(pat.Fields))
	for i, f := range pat.Fields {
		fields[i] = v.embeds.field(f.Name)
		if fields[i] == nil || !irFieldSubPattern(f.Pattern, fields[i].k) {
			return false
		}
	}
	match := bl.variantTest(pat, subj, d, v)
	bl.b.Append(match)
	bl.side(match.Dst(), irScalarSide{k: kindBool})
	tests := irFieldTests(pat)
	tagged := bl.b
	target := irFieldTestTarget(bl, pat, tests, arm)
	tagged.SetTerm(ir.NewBranch(bl.g.irNodePos(pat), match.Dst(), target.ID(), next.ID()))
	payload := bl.variantPayload(pat, subj, d, v)
	for i, f := range pat.Fields {
		if _, wild := f.Pattern.(*ast.WildcardPattern); wild {
			continue
		}
		fd := fields[i]
		projection := ir.NewProjField(bl.g.irNodePos(pat), bl.f.NewTemp(), payload,
			bl.g.irTypes().Symbol(fd, f.Name), f.Name, irParamShape(fd.k))
		bl.b.Append(projection)
		bl.side(projection.Dst(), irScalarSide{k: fd.k})
		if !bl.fieldSubTest(pat, f, projection.Dst(), fd.k, &tests, arm, next) {
			return false
		}
	}
	return true
}

// irIsIdentPattern reports whether a payload pattern is a plain name.
func irIsIdentPattern(n ast.Node) bool {
	_, ok := n.(*ast.IdentPattern)
	return ok
}
