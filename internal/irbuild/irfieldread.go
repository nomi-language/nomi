package irbuild

// A field read `c.value` in a retained body lowers to an `ir.Proj`. The
// object is lowered once through `bl.lower`, and the projection depends on
// its kind: a declared struct's field (`ProjField`), a record's or tuple's
// field (`recordRead`), a channel's half, an enum's field whose variant is not
// known (`enumFieldRead`), or a Range's declared field.
//
// # Constructs that are not a field read
//
// Many constructs wear a field access's syntax without being one, and each is
// excluded structurally:
//
//   - `Shape.Dot` and `Probe.Reading.Steady` are bare variants. The first has
//     an `*ast.TypeIdent` object and the second a nested `*ast.FieldAccess`
//     one that `dottedTypeQualifier` answers for, so a nested
//     `*ast.FieldAccess` is admitted only when `dottedTypeQualifier` declines
//     it, and `p.address.city` lowers its object through `bl.lower`.
//   - `header.default_name` (a sibling file's `once` read through its API
//     object) and `io.print` without a call (a module name read as a value)
//     have objects that are not a bound local, so `bl.lower` declines them.
//   - a genuine scalar, a distinct and a generic template reach the struct
//     arm with no declared field, and `irRetainedStructKind` answers false.
//
// # The field's kind is not tested again
//
// `irRetainedStructKind` already requires every field of the declaration to
// be in `irRetainedFieldKind`, so a second test at the read would have an
// empty population.
//
// # Mobility is the subject's
//
// A field read never forces, and its mobility is the object's: this
// producer passes the subject's mobility through rather than answering a
// second time.

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// fieldRead lowers `c.value` as an `ir.Proj` of kind `ProjField`.
//
// Every check is a decline and none is a refusal, as in `structMake`. A
// declined read declines the body that holds it.
func (bl *irScalarBuilder) fieldRead(t *ast.FieldAccess) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if t.Field == nil {
		// A parse artifact.
		return no()
	}
	_, appObject := bl.g.appRead(t.Object)
	switch t.Object.(type) {
	case *ast.Ident:
	case *ast.Call:
		// `base_cfg().clock`: a field of a call's result. The call is lowered
		// once, below, and its value projected.
	case *ast.FieldAccess:
		// A field chain `p.address.city`, or a field of an application
		// field (`Config.done.sender`). A dotted type qualifier is a bare
		// variant instead.
		if _, dotted := bl.g.dottedTypeQualifier(t.Object); dotted && !appObject {
			return no()
		}
	case *ast.StructLit, *ast.GroupedExpr, *ast.TupleLit, *ast.If, *ast.Case, *ast.Block, *ast.TaggedString:
		// `Holder{shape: c}.shape`, `(f(x)).name`, `Box"x".contents`: a
		// field of a value the expression builds, lowered once below and
		// projected.
	default:
		// The whole of the bare-variant, dotted-qualifier and
		// sibling-`once` exclusion. See the file header.
		return no()
	}
	// THROUGH `bl.lower` AND NOT THROUGH A SECOND RESOLUTION OF THE NAME,
	// because the Ident arm is where a body-bound name answers with the
	// `ir.Bind`'s own temporary and a parameter or outer local answers with a
	// fresh `ir.NewRefLocal`. Two spellings of "read this name" is exactly
	// what `irparam.go` refuses for a destructuring prologue.
	subj, k, mobile, ok := bl.lower(t.Object)
	if !ok {
		return no()
	}
	return bl.fieldProject(t, t.Field.Name, subj, k, mobile)
}

// fieldProject reads field name off subj, a value of kind k, positioned at
// at. It is fieldRead past the object's lowering, shared with the field
// accessor `.name` (irfieldaccessor.go), whose object is its parameter.
func (bl *irScalarBuilder) fieldProject(at ast.Node, name string, subj ir.Temp, k kind, mobile bool) (ir.Temp, kind, bool, bool) {
	no := func() (ir.Temp, kind, bool, bool) { return ir.NoTemp, kindInvalid, false, false }
	if irRetainedRecordKind(k) {
		return bl.recordRead(at, name, subj, k, mobile)
	}
	if _, owner, isChannel := channelElem(k); isChannel && owner == "Channel" {
		return bl.channelHalf(at, name, subj, k, mobile)
	}
	if k.tag == tagNamed && irRetainedEnumKind(k.def) && k.def.preludeOf == nil {
		return bl.enumFieldRead(at, name, subj, k.def, mobile)
	}
	if _, isRange := rangeElem(k); isRange && irRetainedRangeKind(k) {
		// `r.start`, `r.end`, `r.inclusive`: the VM holds a Range as std's
		// struct record, so its declared fields read like any struct's.
		// std's own Range bodies (`impl Display for Range<T>`) read them.
		if f := k.def.field(name); f != nil && irRetainedValueKind(f.k) {
			p := ir.NewProjField(bl.g.irNodePos(at), bl.f.NewTemp(), subj,
				bl.g.irTypes().Symbol(f, name), name, irParamShape(f.k))
			bl.b.Append(p)
			bl.side(p.Dst(), irScalarSide{k: f.k})
			return p.Dst(), f.k, mobile, true
		}
	}
	if k.tag != tagNamed || !irRetainedStructKind(k.def) {
		// `tagNamed` excludes the tuple, record and existential arms;
		// `irRetainedStructKind` excludes an enum, a distinct, a generic
		// template, an unlowerable declaration, a field-less one and one
		// with a boxed field.
		return no()
	}
	f := k.def.field(name)
	if f == nil {
		// An unknown struct field.
		return no()
	}
	p := ir.NewProjField(bl.g.irNodePos(at), bl.f.NewTemp(), subj,
		bl.g.irTypes().Symbol(f, name), name, irParamShape(f.k))
	bl.b.Append(p)
	if irProjKindObserved != nil {
		irProjKindObserved("irfieldread.go fieldRead", p, f.k)
	}
	bl.side(p.Dst(), irScalarSide{k: f.k})
	return p.Dst(), f.k, mobile, true
}

// enumFieldRead reads `s.f` off an enum value whose variant is not known:
// the field of a struct-shaped variant, of an embedded struct, or of a
// positional struct payload. The checker types the read from the first
// variant that supplies f; a value of a variant that does not faults at run
// time with rt's texts (ir.ProjEnumField's Faults says whether one exists).
func (bl *irScalarBuilder) enumFieldRead(at ast.Node, name string, subj ir.Temp, d *typeDef, mobile bool) (ir.Temp, kind, bool, bool) {
	var fk kind
	found, faults := false, false
	for i := range d.variants {
		v := &d.variants[i]
		vk, supplies := irVariantFieldKind(v, name)
		if !supplies {
			faults = true
			continue
		}
		if !found {
			fk, found = vk, true
		} else if vk != fk {
			irDeclineNote("an enum field read whose variants disagree on its type: ." + name)
			return ir.NoTemp, kindInvalid, false, false
		}
	}
	if !found || !irRetainedValueKind(fk) {
		return ir.NoTemp, kindInvalid, false, false
	}
	p := ir.NewProjEnumField(bl.g.irNodePos(at), bl.f.NewTemp(), subj, bl.g.irTypeSym(d), name, faults, irParamShape(fk))
	bl.b.Append(p)
	bl.side(p.Dst(), irScalarSide{k: fk})
	return p.Dst(), fk, mobile, true
}

// irVariantFieldKind is the kind of field name in variant v's payload, and
// whether v supplies it at all.
func irVariantFieldKind(v *variantDef, name string) (kind, bool) {
	switch v.kind {
	case "struct":
		for _, p := range v.payloads {
			if p.nomi == name {
				return p.k, true
			}
		}
	case "embedded":
		if v.embeds != nil && !v.embeds.isEnum && !v.embeds.isDistinct {
			if f := v.embeds.field(name); f != nil {
				return f.k, true
			}
		}
	case "positional":
		if len(v.payloads) == 1 {
			pk := v.payloads[0].k
			if pk.tag == tagNamed && pk.def != nil && !pk.def.isEnum && !pk.def.isDistinct {
				if f := pk.def.field(name); f != nil {
					return f.k, true
				}
			}
		}
	}
	return kindInvalid, false
}
