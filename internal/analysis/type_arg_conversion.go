package analysis

import "strings"

// Type argument conversion (spec §5 *Type argument conversion*).
//
// Values are immutable, so a std collection's type argument is covariant: a
// List<Int> may stand where a List<Display> is expected, since nothing can
// put a String into the list through the wider view. The checker admits the
// conversion only where it leaves the value as it is at run time, so a call
// site never pays for it element by element: each element must already be a
// value of the wider type. That holds for
//
//   - an element whose type carries its own run-time identity entering an
//     interface it implements (carriesOwnType): the VM's interface value is
//     the concrete value itself, and a call through it dispatches on the
//     value's type;
//   - a function type whose parameters and result convert so: a call
//     through the wider type passes and returns the same values;
//   - a nested collection, tuple or record whose parts convert so.
//
// A value that enters an interface boxed (a Maybe, a List, a tuple, an
// enum with an `embeds` variant, a std host type, a type parameter) would
// need a box per element, and a function whose parameters or result convert
// another way an adapter per element, so neither converts under a type
// argument: the error's hint names the explicit form.
//
// A declared generic type's arguments (`Box<T>`, a std struct, a generic
// interface other than Iter) compare exactly: its values carry their type
// arguments (an instance's descriptor names them, and an erased instance
// dispatches to the impls recorded under that name), so relabelling one
// would change which impl a call reaches. `embeds` conversion still reaches
// every type argument, and the builder rebuilds the value for it
// (irbuild/irembedsrebuild.go).

// typeArgMode is the mode for t's type arguments: a std collection's
// convert in place (nested), a declared generic type's compare exactly
// (declNested).
func (m unifyMode) typeArgMode(t Type) unifyMode {
	if stdCollectionArgs(t) {
		return m.nested()
	}
	return m.declNested()
}

// stdCollectionArgs reports whether t is a std collection whose type
// arguments are covariant: Maybe, Result, Set or Vector (List and Map have
// types of their own).
func stdCollectionArgs(t Type) bool {
	switch t := t.(type) {
	case *EnumType:
		return (t.Name == "Maybe" && (t.Origin == "std/maybe" || t.Origin == "")) ||
			(t.Name == "Result" && (t.Origin == "std/results" || t.Origin == ""))
	case *StructType:
		return isStdSetType(t)
	case *DistinctType:
		return t.Name == "Vector" && (t.Origin == "std/vectors" || t.Origin == "")
	}
	return false
}

// convertibleArgs reports whether t is a std collection of any shape, List
// and Map included, whose type arguments convert in place.
func convertibleArgs(t Type) bool {
	switch t.(type) {
	case *ListType, *MapType:
		return true
	}
	return stdCollectionArgs(t)
}

// carriesOwnType reports whether a value of type t is, unchanged, a value of
// any interface t implements: its run-time value names its type, so the
// interface value is the value itself (irbuild's erasesUnboxed). That is a
// builtin Int, Float, Bool or String, a declared struct, a declared enum with
// no `embeds` variant, and a declared distinct type. A std enum, a std
// generic struct and a std distinct or host type are not: some enter an
// interface boxed (a Maybe, a Set, a Decimal), and the rule names none of
// them one by one.
func carriesOwnType(t Type) bool {
	switch t := resolveTV(t).(type) {
	case *PrimitiveType:
		return samePrimitive(t, TypeInt) || samePrimitive(t, TypeFloat) || samePrimitive(t, TypeString)
	case *StructType:
		if isStdOrigin(t.Origin) {
			return len(t.TypeArgs) == 0
		}
		return true
	case *EnumType:
		if isStdOrigin(t.Origin) {
			// Bool is std's enum of `embeds True | embeds False`, held as
			// a builtin scalar.
			return t.Name == "Bool" && len(t.TypeArgs) == 0
		}
		for _, v := range t.Variants {
			if v.Kind == VariantEmbedded {
				return false
			}
		}
		return true
	case *DistinctType:
		return !isStdOrigin(t.Origin) && len(t.TypeParams) == 0
	}
	return false
}

// isStdOrigin reports whether origin is a std module's, or unknown (a std
// prelude type built with no origin).
func isStdOrigin(origin string) bool {
	return origin == "" || strings.HasPrefix(origin, "std/")
}
