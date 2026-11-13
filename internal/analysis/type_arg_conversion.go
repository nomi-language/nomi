package analysis

import "strings"

// Covariant conversion (spec §5 *Covariant conversion*).
//
// Values are immutable, so a type argument that a type's values only hold
// may widen: a List<Int> may stand where a List<Display> is expected, since
// nothing can put a String into the list through the wider view. Where a
// value of F<A> is expected as F<B>, it converts when each type argument
// converts by its parameter's variance, recursively:
//
//   - a std collection's arguments are covariant: List's, Vector's, Set's,
//     Maybe's and Result's, and a Map's key and value;
//   - a user-declared generic struct's or enum's parameters have the
//     variance their fields give them (variance.go): covariant, converting
//     as a value does; contravariant (named only in a function type's
//     parameters), converting the other way; or invariant, comparing
//     exactly;
//   - a std struct's, a host type's and an interface's other than Iter's
//     arguments compare exactly: such a value carries them;
//   - a tuple's elements, a record's fields and a function type's result
//     are covariant, and a function type's parameters contravariant.
//
// A conversion that leaves the value as it is costs nothing: an Int is a
// Display value already, so a List<Int> is relabelled (irbuild's
// relabels). Any other is done element by element: the builder rebuilds
// the collection, record or variant with each part converted, and wraps a
// function whose parameters or result convert (internal/irbuild,
// irembedsrebuild.go, irfuncwiden.go). The checker records each such
// conversion (conversions.go).
//
// A Set's elements and a Map's keys are found by equality and hashing. A
// value entering an interface keeps both: an unboxed one is the value
// itself, and a box compares and hashes by the static type it was made
// from and then by that type's own equality (rt.Keys.Equal). So two
// distinct keys of one type stay distinct once converted, and the rebuilt
// Set or Map has the size it had.

// unifyArg unifies type argument i of the generic type t, a's want with
// b's have, by the variance of t's parameter: a covariant one in m's nested
// mode, a contravariant one swapped, and an invariant one exactly.
func unifyArg(m unifyMode, t Type, i int, want, have Type, subs map[*TypeParam_]Type, impls []ImplTables, implTypeArgs []map[string]map[string][]*ImplTypeArgs, rigid map[string]*TypeParam_) error {
	switch argVariance(t, i) {
	case VarianceCovariant, VarianceUnused:
		return unifyIn(m.nested(), want, have, subs, impls, implTypeArgs, rigid)
	case VarianceContravariant:
		return unifyIn(m.nested(), have, want, subs, impls, implTypeArgs, rigid)
	}
	return unifyIn(m.declNested(), want, have, subs, impls, implTypeArgs, rigid)
}

// argVariance is the variance of t's type parameter i: a std collection's
// are covariant, a user-declared generic type's are its fields' (variance.go),
// and every other type's are invariant.
func argVariance(t Type, i int) Variance {
	switch t.(type) {
	case *ListType, *MapType:
		return VarianceCovariant
	}
	if stdCollectionArgs(t) {
		return VarianceCovariant
	}
	if vs := TypeParamVariances(t); i < len(vs) {
		return vs[i]
	}
	return VarianceInvariant
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

// convertibleArgs reports whether some type argument of t may convert: t is
// a std collection of any shape, List and Map included, or a declared
// generic type with a parameter that is not invariant.
func convertibleArgs(t Type) bool {
	switch t.(type) {
	case *ListType, *MapType:
		return true
	}
	if stdCollectionArgs(t) {
		return true
	}
	for _, v := range TypeParamVariances(t) {
		if v != VarianceInvariant {
			return true
		}
	}
	return false
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
