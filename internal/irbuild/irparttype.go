package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// The checker type of a part of a value: a tuple's slot, a record's or a
// struct's field, a variant's payload, a container's element. Each answers
// nil where the builder holds the value by kind alone.

// partCheckerType is the checker type of part i of a value of kind sk,
// whose checker type is st: a tuple's i-th element, an anonymous record's
// field named as sk's i-th, and a std generic container's i-th type
// argument, in the order stdRecvParts lists sk's (a List's element, a
// Map's key and value, an Option's payload), and an Iter's element. Nil when
// st is nil or not that shape.
func partCheckerType(st analysis.Type, sk kind, i int) analysis.Type {
	containerArg := func(args []analysis.Type) analysis.Type {
		if _, parts, ok := stdRecvParts(sk); ok && len(parts) == len(args) && i < len(args) {
			return args[i]
		}
		return nil
	}
	switch t := analysis.ResolveTypeVar(st).(type) {
	case *analysis.InterfaceType:
		if sk.tag == tagSeq && i == 0 && len(t.TypeArgs) == 1 {
			return t.TypeArgs[0]
		}
	case *analysis.EnumType:
		return containerArg(t.TypeArgs)
	case *analysis.StructType:
		return containerArg(t.TypeArgs)
	case *analysis.DistinctType:
		// A generic host type: a Vector's element.
		return containerArg(t.TypeArgs)
	case *analysis.ListType:
		return containerArg([]analysis.Type{t.Elem})
	case *analysis.MapType:
		return containerArg([]analysis.Type{t.Key, t.Val})
	case *analysis.TupleType:
		if sk.tag == tagTuple && i < len(t.Elems) && len(t.Elems) == len(sk.comp.parts) {
			return t.Elems[i]
		}
	case *analysis.AnonStructType:
		if sk.tag == tagAnonStruct && i < len(sk.comp.names) {
			for _, f := range t.Fields {
				if f.Name == sk.comp.names[i] {
					return f.Type
				}
			}
		}
	}
	return nil
}

// embeddedVariantType is the declared type of the `embeds` variant named
// name of st, a non-generic enum's checker type: the struct or distinct
// itself, where variantPayloadType answers a wrapping distinct's inner
// type. Nil when st is nil, generic or has no such variant.
func embeddedVariantType(st analysis.Type, name string) analysis.Type {
	et, ok := analysis.ResolveTypeVar(st).(*analysis.EnumType)
	if !ok || len(et.TypeParams) > 0 {
		return nil
	}
	for _, v := range et.Variants {
		if v.Name == name && v.Kind == analysis.VariantEmbedded {
			return v.Embedded
		}
	}
	return nil
}

// declaredPartType is the checker type of the field named name of st, a
// struct's or a struct-shaped variant's checker type (variant names the
// variant, "" for a struct), at st's type arguments. Nil when st is nil
// or has no such field.
func declaredPartType(st analysis.Type, variant, name string) analysis.Type {
	var fields []analysis.FieldDef
	var params []*analysis.TypeParam_
	var args []analysis.Type
	switch t := analysis.ResolveTypeVar(st).(type) {
	case *analysis.StructType:
		fields, params, args = t.Fields, t.TypeParamDefs, t.TypeArgs
	case *analysis.EnumType:
		for _, v := range t.Variants {
			if v.Name == variant {
				fields = v.Fields
			}
		}
		params, args = t.TypeParamDefs, t.TypeArgs
	}
	for _, f := range fields {
		if f.Name == name {
			return typeAtArgs(f.Type, params, args)
		}
	}
	return nil
}

// typeAtArgs is t, a declaration's part, with the declaration's type
// parameters params replaced by args, or nil where they do not match.
func typeAtArgs(t analysis.Type, params []*analysis.TypeParam_, args []analysis.Type) analysis.Type {
	if len(params) == 0 {
		return t
	}
	if len(args) != len(params) {
		return nil
	}
	at, ok := analysis.ReplaceTypeParams(t, func(p *analysis.TypeParam_) (analysis.Type, bool) {
		for i, d := range params {
			if d == p {
				return args[i], true
			}
		}
		return nil, false
	})
	if !ok {
		return nil
	}
	return at
}

// opaqueType reports whether st is the checker type of an opaque struct,
// enum or distinct type, whose rendering shows no structure.
func opaqueType(st analysis.Type) bool {
	switch t := analysis.ResolveTypeVar(st).(type) {
	case *analysis.StructType:
		return t.Opaque
	case *analysis.EnumType:
		return t.Opaque
	case *analysis.DistinctType:
		return t.Opaque
	}
	return false
}

// declOpaque reports whether decl, a struct, enum or distinct
// declaration, is opaque, for a part with no checker type (opaqueType).
func declOpaque(decl ast.Node) bool {
	switch d := decl.(type) {
	case *ast.StructDef:
		return d.Opaque
	case *ast.EnumDef:
		return d.Opaque
	case *ast.TypeDef:
		return d.Opaque
	}
	return false
}

// variantPayloadType is the checker type of the payload of the variant
// named name of st, an enum's checker type, at st's type arguments
// (Json's `Obj` is `Map<String, Json>`, `Outcome<Int>`'s `Completed` is
// Int and its `Failed` is Failure). Nil when st is nil or has no such
// variant with a payload.
func variantPayloadType(st analysis.Type, name string) analysis.Type {
	et, ok := analysis.ResolveTypeVar(st).(*analysis.EnumType)
	if !ok {
		return nil
	}
	for _, v := range et.Variants {
		if v.Name == name && v.DataType != nil {
			return typeAtArgs(v.DataType, et.TypeParamDefs, et.TypeArgs)
		}
	}
	return nil
}
