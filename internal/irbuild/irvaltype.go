package irbuild

// The stored IR value type of a temporary, read off this builder's type
// answer for the value.
//
// `kind` is the builder's projection of the checker's type (inferred.go's
// `project`, and the annotation readers beside it). irValType maps it into
// `ir.ValType`, whose vocabulary is Nomi's and not Go's: a declared type is its
// declaration identity (`irTypeSym`), a structural type is its components, and
// a bound-free type parameter or an untyped literal's missing argument is
// `ir.AnyType`. See internal/ir/valtype.go.

import "github.com/nomi-language/nomi/internal/ir"

// irIfaceTypeToken keys an interface's identity in the IR table, apart from
// any other symbol an `*ifaceDef` might be interned for.
type irIfaceTypeToken struct{ d *ifaceDef }

// irStdTypeToken keys a generic or host std type's declaration: one symbol
// for `Channel` whatever it is instantiated at, named by its declaring
// module. irTypeSym would key on the instance's def, and name a per-gen
// instance or a host type after the module that USES it (`main.Context`).
type irStdTypeToken struct{ origin, nomi string }

// irStdTypeSym is the declaration symbol of the std type nomi declared in
// origin, spelled `channels.Channel`.
func (g *gen) irStdTypeSym(origin, nomi string) *ir.Symbol {
	return g.irTypes().Symbol(irStdTypeToken{origin, nomi}, irModuleQualifier(origin)+"."+nomi)
}

// irValType is the IR value type of a value of kind k, or nil for a kind that
// names no value (kindInvalid).
func (g *gen) irValType(k kind) *ir.ValType {
	if v, ok := g.irValTypeMemo[k]; ok {
		if v == nil && g.irValTypeBusy[k] {
			// A type reaching itself through something other than a declared
			// struct's or enum's field, which enters itself before recursing.
			// No such Nomi type lowers today; answer the erased type rather
			// than recurse forever.
			return ir.AnyType
		}
		return v
	}
	if g.irValTypeMemo == nil {
		g.irValTypeMemo = map[kind]*ir.ValType{}
		g.irValTypeBusy = map[kind]bool{}
	}
	g.irValTypeMemo[k] = nil
	g.irValTypeBusy[k] = true
	v := g.irValTypeOf(k)
	delete(g.irValTypeBusy, k)
	g.irValTypeMemo[k] = v
	return v
}

func (g *gen) irValTypeOf(k kind) *ir.ValType {
	switch k.tag {
	case tagUnit:
		return ir.UnitType
	case tagNever:
		return ir.NeverType
	case tagInt:
		return ir.IntType
	case tagFloat:
		return ir.FloatType
	case tagString:
		return ir.StringType
	case tagBool:
		return ir.BoolType
	case tagTypeParam:
		return ir.AnyType
	case tagIface:
		if k.iface == nil {
			return nil
		}
		return ir.NewIfaceType(g.irTypes().Symbol(irIfaceTypeToken{k.iface}, k.iface.nomi))
	case tagEmptyList:
		return ir.NewListType(ir.AnyType)
	case tagEmptySet:
		return ir.NewSetType(ir.AnyType)
	case tagEmptyVector:
		return ir.NewVectorType(ir.AnyType)
	case tagEmptyMap:
		return ir.NewMapType(ir.AnyType, ir.AnyType)
	case tagList, tagSeq, tagMap, tagTuple, tagAnonStruct, tagFunc:
		return g.irCompValType(k)
	case tagNamed:
		return g.irNamedValType(k)
	}
	return nil
}

// irValTypes maps each kind; nil when any of them names no value.
func (g *gen) irValTypes(ks []kind) []*ir.ValType {
	out := make([]*ir.ValType, len(ks))
	for i, k := range ks {
		if out[i] = g.irValType(k); out[i] == nil {
			return nil
		}
	}
	return out
}

func (g *gen) irCompValType(k kind) *ir.ValType {
	if k.comp == nil {
		return nil
	}
	parts := g.irValTypes(k.comp.parts)
	if parts == nil && len(k.comp.parts) > 0 {
		return nil
	}
	switch k.tag {
	case tagList:
		return ir.NewListType(parts[0])
	case tagSeq:
		return ir.NewSeqType(parts[0])
	case tagMap:
		return ir.NewMapType(parts[0], parts[1])
	case tagTuple:
		return ir.NewTupleType(parts...)
	case tagAnonStruct:
		return ir.NewRecordType(k.comp.names, parts)
	case tagFunc:
		n := len(parts) - 1
		return ir.NewFuncType(parts[:n], parts[n])
	}
	return nil
}

func (g *gen) irNamedValType(k kind) *ir.ValType {
	d := k.def
	if d == nil {
		return nil
	}
	if isDecimalKind(k) {
		return ir.DecimalType
	}
	if k == irByteKind() {
		return ir.ByteType
	}
	if k == irBytesKind() {
		return ir.BytesType
	}
	if spec, args, ok := genStructOf(k); ok {
		as := g.irValTypes(args)
		if as == nil && len(args) > 0 {
			return nil
		}
		switch spec.nomi {
		case "Set":
			return ir.NewSetType(as[0])
		case "Range":
			return ir.NewRangeType(as[0])
		}
		v := ir.NewStructType(g.irStdTypeSym(spec.origin, spec.nomi), as...)
		g.irValTypeMemo[k] = v
		g.irStateFields(v, d)
		return v
	}
	if spec, args, ok := genHostOf(k); ok {
		as := g.irValTypes(args)
		if as == nil && len(args) > 0 {
			return nil
		}
		if spec.nomi == "Vector" {
			return ir.NewVectorType(as[0])
		}
		return ir.NewHandleType(g.irStdTypeSym(spec.origin, spec.nomi), as...)
	}
	if d.rtOpaque {
		for i, hd := range stdHostDefs() {
			if hd == d && stdHostSpecs[i].origin != "" {
				return ir.NewHandleType(g.irStdTypeSym(stdHostSpecs[i].origin, stdHostSpecs[i].nomi))
			}
		}
		return ir.NewHandleType(g.irTypeSym(d))
	}
	var args []kind
	switch {
	case d.preludeOf != nil:
		args = d.preludeArgs
	case d.genericOf != nil:
		args = d.genericArgs
	}
	as := g.irValTypes(args)
	if as == nil && len(args) > 0 {
		return nil
	}
	switch {
	case d.isEnum:
		v := ir.NewEnumType(g.irTypeSym(d), as...)
		g.irValTypeMemo[k] = v
		g.irStateVariants(v, d)
		return v
	case d.isDistinct:
		// kindInvalid: marker — a zero-sized marker has no inner value.
		if d.inner == kindInvalid {
			return ir.NewDistinctType(g.irTypeSym(d), nil)
		}
		inner := g.irValType(d.inner)
		if inner == nil {
			return nil
		}
		return ir.NewDistinctType(g.irTypeSym(d), inner)
	}
	v := ir.NewStructType(g.irTypeSym(d), as...)
	g.irValTypeMemo[k] = v
	g.irStateFields(v, d)
	return v
}

// irStateFields states a struct type's fields. The type is memoized before
// its fields are mapped, so a field that reaches the struct again (through a
// List, a Maybe, a boxed edge) maps to this same ValType. A field this builder
// has no value type for leaves the layout unstated rather than partial.
func (g *gen) irStateFields(v *ir.ValType, d *typeDef) {
	fields := make([]ir.Field, len(d.fields))
	for i, f := range d.fields {
		ty := g.irValType(f.k)
		if ty == nil {
			return
		}
		fields[i] = ir.Field{Name: f.nomi, Type: ty}
	}
	v.SetFields(fields)
}

// irStateVariants states an enum type's variants in tag order.
func (g *gen) irStateVariants(v *ir.ValType, d *typeDef) {
	variants := make([]ir.Variant, len(d.variants))
	for i, vd := range d.variants {
		var form ir.VariantForm
		switch vd.kind {
		case "bare":
			form = ir.VariantBare
		case "positional":
			form = ir.VariantPositional
		case "struct":
			form = ir.VariantFields
		case "embedded":
			form = ir.VariantEmbedded
			if irEmbedsEnum(&d.variants[i]) {
				// No value widens into an embedded enum: its one value
				// carries no payload (irEmbedsEnum).
				variants[i] = ir.Variant{Name: vd.nomi, Form: ir.VariantBare}
				continue
			}
		default:
			return
		}
		fields := make([]ir.Field, len(vd.payloads))
		for j, p := range vd.payloads {
			ty := g.irValType(p.k)
			if ty == nil {
				return
			}
			fields[j] = ir.Field{Name: p.nomi, Type: ty}
		}
		variants[i] = ir.Variant{Name: vd.nomi, Form: form, Fields: fields}
	}
	v.SetVariants(variants)
}

// irTypeTemp states the value type of t, of kind k, in f. A kind that names
// no value (kindInvalid, which irValType answers nil for) states nothing, and
// RuleTempTyped reports the temporary if the graph goes on to name it.
func (g *gen) irTypeTemp(f *ir.Func, t ir.Temp, k kind) {
	if f == nil || t == ir.NoTemp {
		return
	}
	if ty := g.irValType(k); ty != nil {
		f.SetType(t, ty)
	}
}

// irAddParam declares one parameter of f receiving a value of kind k, with its
// shape and its stored type.
func (g *gen) irAddParam(f *ir.Func, sym *ir.Symbol, k kind) ir.Temp {
	t := f.AddParam(sym, irParamShape(k))
	g.irTypeTemp(f, t, k)
	return t
}
