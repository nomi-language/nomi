package analysis

import (
	"strconv"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// A declared type whose declaration writes a part with a type that names
// nothing keeps the part, with no type: a struct's or a record variant's
// field (FieldDef.Type nil), a payload or an embedded type
// (VariantDef.DataType nil), a distinct type's inner type
// (DistinctType.InnerOfNoType). The error where the part's type is written
// is the one error, so the type is poison where it is used: nothing that
// reads the part at the type it does not have reports again, here or in a
// declaration that holds the type. The compiler's impls for it are not
// checked (checkImplBlock). A demand of it, or a derive's demand of such a
// part, draws no missing impl (demandOfNoType); a call's interface bound
// on it draws no error (boundError); and a position whose type is an
// interface admits it, as it admits a value of no type
// (ifaceParamAdmits).

// typeExprHasNoType reports whether te is a type expression of fa whose
// resolution failed and was reported where te is written
// (FileAnalysis.TypeExprTypes records it with no type).
func typeExprHasNoType(fa *FileAnalysis, te ast.TypeExpr) bool {
	if fa == nil || te == nil {
		return false
	}
	t, recorded := fa.TypeExprTypes[te]
	return recorded && t == nil
}

// hasPartOfNoType reports whether t is a declared type with a part of no
// type, or with a part whose type holds such a type.
func hasPartOfNoType(t Type) bool {
	return partOfNoType(t, map[string]bool{})
}

// partOfNoType is hasPartOfNoType, with seen holding each declaration
// already asked, by identity (Origin and Name), so a recursive type ends.
func partOfNoType(t Type, seen map[string]bool) bool {
	var parts []Type
	switch tt := resolveTypeVar(t).(type) {
	case *StructType:
		if !firstAsked(seen, tt.Origin, tt.Name) {
			return false
		}
		for _, f := range tt.Fields {
			if f.Type == nil {
				return true
			}
			parts = append(parts, f.Type)
		}
	case *EnumType:
		if !firstAsked(seen, tt.Origin, tt.Name) {
			return false
		}
		for _, v := range tt.Variants {
			if (v.Kind == VariantPositional || v.Kind == VariantEmbedded) && v.DataType == nil {
				return true
			}
			if v.DataType != nil {
				parts = append(parts, v.DataType)
			}
			for _, f := range v.Fields {
				if f.Type == nil {
					return true
				}
				parts = append(parts, f.Type)
			}
		}
	case *DistinctType:
		if tt.InnerOfNoType {
			return true
		}
		if !firstAsked(seen, tt.Origin, tt.Name) {
			return false
		}
		if tt.Inner != nil {
			parts = append(parts, tt.Inner)
		}
	default:
		return false
	}
	for _, p := range parts {
		if typeHoldsPartOfNoType(p, seen) {
			return true
		}
	}
	return false
}

// typeHoldsPartOfNoType reports whether t, or a type it holds (an element,
// a type argument), is a declared type with a part of no type.
func typeHoldsPartOfNoType(t Type, seen map[string]bool) bool {
	found := false
	walkType(t, func(p Type) bool {
		found = found || partOfNoType(p, seen)
		return !found
	})
	return found
}

// firstAsked reports whether the declaration (origin, name) is asked for
// the first time, and notes it in seen.
func firstAsked(seen map[string]bool, origin, name string) bool {
	key := origin + "\x00" + name
	if seen[key] {
		return false
	}
	seen[key] = true
	return true
}

// demandOfNoType reports whether rec, a demand fa's analysis recorded of
// the type named typeName, demands it of a type of no type or of a type
// with a part of no type: the demanded type, which rec.TypeOrigin
// declares, has such a part (hasPartOfNoType), or rec is a derive's demand
// of a part (DeriveCtx.FieldName, as eachDeclPart names it) of the type it
// derives for that has no type, or whose type holds a type with a part of
// no type. The part's error is the one error, so the demand draws no
// missing impl. The live coherence check and the stored facts' reduction
// both drop such a demand (programDemands.settle, DemandFact.NoType).
func (fa *FileAnalysis) demandOfNoType(typeName string, rec Recording) bool {
	if fa == nil || fa.ModuleScope == nil {
		return false
	}
	if rec.Derive != nil && rec.Derive.FieldName != "" {
		if sym := fa.ModuleScope.Lookup(rec.Derive.TypeName); sym != nil {
			part, ok := partType(resolveRef(sym).Type, rec.Derive.FieldName)
			if ok && (part == nil || typeHoldsPartOfNoType(part, map[string]bool{})) {
				return true
			}
		}
	}
	return hasPartOfNoType(fa.typeDeclaredAt(rec.TypeOrigin, typeName))
}

// typeDeclaredAt is the type named name that the file of origin declares
// at its top level (fileOfOrigin), or nil where no file of origin the
// program holds declares one. A std module declares no type with a part
// of no type, so origin a std module's answers nil without a lookup.
func (fa *FileAnalysis) typeDeclaredAt(origin, name string) Type {
	if origin == OriginUnresolved || IsBodyOrigin(origin) || IsStdlibKey(origin) {
		return nil
	}
	home := fa.fileOfOrigin(origin)
	if home == nil || home.ModuleScope == nil {
		return nil
	}
	if sym := home.ModuleScope.Lookup(name); sym != nil {
		return resolveRef(sym).Type
	}
	return nil
}

// partType answers the type of the part of the declared type t that name
// names, as eachDeclPart names a part (`email`, `Email`, `Email.address`,
// `Email[0]`, `inner`, `inner[1]`), and whether t has the part. A part of
// no type answers nil and true.
func partType(t Type, name string) (Type, bool) {
	index := -1
	if open := strings.IndexByte(name, '['); open >= 0 && strings.HasSuffix(name, "]") {
		i, err := strconv.Atoi(name[open+1 : len(name)-1])
		if err != nil {
			return nil, false
		}
		name, index = name[:open], i
	}
	var whole Type
	switch tt := t.(type) {
	case *StructType:
		f, ok := findFieldType(tt.Fields, name)
		if !ok {
			return nil, false
		}
		whole = f
	case *EnumType:
		variant, field, isField := strings.Cut(name, ".")
		vd := variantDefNamed(tt, variant)
		if vd == nil {
			return nil, false
		}
		if isField {
			f, ok := findFieldType(vd.Fields, field)
			if !ok {
				return nil, false
			}
			whole = f
		} else {
			whole = vd.DataType
		}
	case *DistinctType:
		if name != "inner" {
			return nil, false
		}
		whole = tt.Inner
	default:
		return nil, false
	}
	if tuple, ok := whole.(*TupleType); ok && index >= 0 && index < len(tuple.Elems) {
		return tuple.Elems[index], true
	}
	return whole, true
}
