package analysis

import "sync"

// Variance of a declared generic type's parameters (spec §5 *Covariant
// conversion*).
//
// Every value is immutable, so a type parameter that a type's values only
// hold, and never take, may widen: a `Box<Int>` is a `Box<Display>` when
// Box's `T` appears only in its fields. Nothing is written by the
// programmer: each parameter's variance is read off where it appears in the
// declaration's fields (a struct's), payloads (an enum's variants') or
// inner type (a distinct type's, as a newtype's variance is its inner
// type's):
//
//   - at the top of a field, inside a List, Vector, Set, Map (key or
//     value), Maybe or Result, a tuple or a record, or as an Iter's element:
//     the polarity it stands at;
//   - in a function type's parameter: the opposite polarity; in its result,
//     the same;
//   - as an argument of another declared generic type: that type's own
//     variance for the parameter, composed (a recursive type reads its own
//     variance, solved to a fixed point);
//   - as an argument of anything else (an interface other than Iter, a std
//     struct other than Set, a std or host distinct type): both
//     polarities, since such a value carries its type arguments as they are.
//
// A parameter seen only at positive positions is covariant, only at
// negative ones contravariant, at both invariant. One the fields never
// name is covariant: no value of it is held, so any conversion is sound.

// Variance is how a type parameter's argument may change under a
// conversion of the type's value.
type Variance uint8

const (
	// VarianceUnused is a parameter no field names, which converts as a
	// covariant one does. It is only a fixed point's start.
	VarianceUnused Variance = 0
	// VarianceCovariant: F<A> converts to F<B> when A converts to B.
	VarianceCovariant Variance = 1
	// VarianceContravariant: F<A> converts to F<B> when B converts to A.
	VarianceContravariant Variance = 2
	// VarianceInvariant: F<A> is F<B> only when A and B are one type.
	VarianceInvariant Variance = VarianceCovariant | VarianceContravariant
)

func (v Variance) String() string {
	switch v {
	case VarianceCovariant, VarianceUnused:
		return "covariant"
	case VarianceContravariant:
		return "contravariant"
	}
	return "invariant"
}

// flip is v at the opposite polarity.
func (v Variance) flip() Variance {
	return (v&VarianceCovariant)<<1 | (v&VarianceContravariant)>>1
}

// compose is the polarity of a position of polarity inner inside a position
// of polarity outer.
func compose(outer, inner Variance) Variance {
	var out Variance
	if inner&VarianceCovariant != 0 {
		out |= outer
	}
	if inner&VarianceContravariant != 0 {
		out |= outer.flip()
	}
	return out
}

// varianceCache holds each built declaration's variances, keyed by its first
// type parameter, which every instance of the declaration shares and no
// other declaration has. Std's declarations are shared by every check a
// process runs, so the cache is too.
var varianceCache sync.Map // *TypeParam_ -> []Variance

// TypeParamVariances is the variance of each of t's type parameters, where
// t is a user-declared generic struct, enum or distinct type, or std's
// Maybe or Result. It
// is nil for any other type, and for a declaration whose body is not built
// yet, whose arguments then compare exactly.
func TypeParamVariances(t Type) []Variance {
	defs, ok := variantDecl(t)
	if !ok {
		return nil
	}
	if v, ok := varianceCache.Load(defs[0]); ok {
		return v.([]Variance)
	}
	s := &varianceSolver{table: map[*TypeParam_][]Variance{}, decls: map[*TypeParam_]Type{}}
	if !s.collect(t) {
		return nil
	}
	for changed := true; changed; {
		changed = false
		for key, d := range s.decls {
			next := s.of(d)
			old := s.table[key]
			for i := range next {
				if next[i] != old[i] {
					changed = true
				}
			}
			s.table[key] = next
		}
	}
	for key, v := range s.table {
		varianceCache.LoadOrStore(key, v)
	}
	return s.table[defs[0]]
}

// variantDecl reports whether t is a declared generic type whose parameters
// have a variance, and its type parameters.
func variantDecl(t Type) ([]*TypeParam_, bool) {
	switch t := resolveTV(t).(type) {
	case *StructType:
		if t.unbuilt != nil || len(t.TypeParamDefs) == 0 || isStdOrigin(t.Origin) {
			return nil, false
		}
		return t.TypeParamDefs, true
	case *EnumType:
		if t.unbuilt != nil || len(t.TypeParamDefs) == 0 {
			return nil, false
		}
		if isStdOrigin(t.Origin) && !stdCollectionArgs(t) {
			return nil, false
		}
		return t.TypeParamDefs, true
	case *DistinctType:
		// A program's generic distinct type, whose variance is its inner
		// type's, as a newtype's is (`type Ids<T> List<T>` is covariant).
		// A std or host one (`Task<T>`, `Channel<T>`) has no inner type
		// the program can see.
		if t.unbuilt != nil || len(t.TypeParamDefs) == 0 || isStdOrigin(t.Origin) || t.Inner == nil {
			return nil, false
		}
		return t.TypeParamDefs, true
	}
	return nil, false
}

// varianceSolver solves the variances of a group of declarations that name
// one another, from VarianceUnused up to a fixed point.
type varianceSolver struct {
	table map[*TypeParam_][]Variance
	decls map[*TypeParam_]Type
}

// collect adds t's declaration and every declaration its fields name; false
// when one of them is not built.
func (s *varianceSolver) collect(t Type) bool {
	defs, ok := variantDecl(t)
	if !ok {
		return true
	}
	if _, seen := s.decls[defs[0]]; seen {
		return true
	}
	if v, ok := varianceCache.Load(defs[0]); ok {
		s.table[defs[0]] = v.([]Variance)
		return true
	}
	if !variantDeclBuilt(t) {
		return false
	}
	s.decls[defs[0]] = t
	s.table[defs[0]] = make([]Variance, len(defs))
	ok = true
	forEachFieldType(t, func(ft Type) {
		walkTypes(ft, func(inner Type) {
			if !s.collect(inner) {
				ok = false
			}
		})
	})
	return ok
}

// variantDeclBuilt reports whether every declaration t names is built.
func variantDeclBuilt(t Type) bool {
	switch t := resolveTV(t).(type) {
	case *StructType:
		return t.unbuilt == nil
	case *EnumType:
		return t.unbuilt == nil
	case *DistinctType:
		return t.unbuilt == nil
	}
	return true
}

// of is the variance of each of d's parameters under the table's current
// solution.
func (s *varianceSolver) of(d Type) []Variance {
	defs, _ := variantDecl(d)
	out := make([]Variance, len(defs))
	forEachFieldType(d, func(ft Type) {
		s.walk(ft, VarianceCovariant, defs, out)
	})
	return out
}

// walk marks in out the polarity pol at which each of defs appears in t.
func (s *varianceSolver) walk(t Type, pol Variance, defs []*TypeParam_, out []Variance) {
	if pol == VarianceUnused {
		return
	}
	switch tt := resolveTV(t).(type) {
	case *TypeParam_:
		for i, d := range defs {
			if d == tt {
				out[i] |= pol
			}
		}
	case *ListType:
		s.walk(tt.Elem, pol, defs, out)
	case *MapType:
		s.walk(tt.Key, pol, defs, out)
		s.walk(tt.Val, pol, defs, out)
	case *TupleType:
		for _, e := range tt.Elems {
			s.walk(e, pol, defs, out)
		}
	case *AnonStructType:
		for _, f := range tt.Fields {
			s.walk(f.Type, pol, defs, out)
		}
	case *FuncType:
		for _, p := range tt.Params {
			s.walk(p, pol.flip(), defs, out)
		}
		s.walk(tt.Return, pol, defs, out)
	case *PartialType:
		s.walk(tt.Inner, VarianceInvariant, defs, out)
	case *InterfaceType:
		inner := VarianceInvariant
		if isStdIterType(tt) {
			inner = pol
		}
		for _, a := range tt.TypeArgs {
			s.walk(a, compose(inner, VarianceCovariant), defs, out)
		}
	case *StructType:
		if isStdSetType(tt) {
			for _, a := range tt.TypeArgs {
				s.walk(a, pol, defs, out)
			}
			return
		}
		s.walkArgs(tt, tt.TypeArgs, pol, defs, out)
	case *EnumType:
		s.walkArgs(tt, tt.TypeArgs, pol, defs, out)
	case *DistinctType:
		if tt.Name == "Vector" && isStdOrigin(tt.Origin) {
			for _, a := range tt.TypeArgs {
				s.walk(a, pol, defs, out)
			}
			return
		}
		if _, ok := variantDecl(tt); ok {
			s.walkArgs(tt, tt.TypeArgs, pol, defs, out)
			return
		}
		for _, a := range tt.TypeArgs {
			s.walk(a, VarianceInvariant, defs, out)
		}
	}
}

// walkArgs walks a declared generic type's arguments, each at the
// declaration's variance for its parameter.
func (s *varianceSolver) walkArgs(t Type, args []Type, pol Variance, defs []*TypeParam_, out []Variance) {
	if len(args) == 0 {
		return
	}
	vs := s.paramVariances(t)
	for i, a := range args {
		v := VarianceInvariant
		if i < len(vs) {
			v = vs[i]
			if v == VarianceUnused {
				// A parameter no field names holds no value: nothing at
				// its argument is read.
				continue
			}
		}
		s.walk(a, compose(pol, v), defs, out)
	}
}

// paramVariances is t's declaration's variances under the current
// solution: the table's entry while it is being solved, else the solved
// one, else invariant for a declaration with none.
func (s *varianceSolver) paramVariances(t Type) []Variance {
	defs, ok := variantDecl(t)
	if !ok {
		return nil
	}
	if v, ok := s.table[defs[0]]; ok {
		return v
	}
	return TypeParamVariances(t)
}

// forEachFieldType calls f with each type a value of the declared type t
// holds: a struct's fields, each variant's payload, struct fields and
// embedded type, a distinct type's inner type.
func forEachFieldType(t Type, f func(Type)) {
	switch t := resolveTV(t).(type) {
	case *StructType:
		for _, fd := range t.Fields {
			f(fd.Type)
		}
	case *EnumType:
		for _, v := range t.Variants {
			if v.DataType != nil {
				f(v.DataType)
			}
			for _, fd := range v.Fields {
				f(fd.Type)
			}
			if v.Embedded != nil && v.Embedded != v.DataType {
				f(v.Embedded)
			}
		}
	case *DistinctType:
		if t.Inner != nil {
			f(t.Inner)
		}
	}
}

// walkTypes calls f with t and each type nested in it, without entering a
// declared type's fields.
func walkTypes(t Type, f func(Type)) {
	t = resolveTV(t)
	f(t)
	switch tt := t.(type) {
	case *ListType:
		walkTypes(tt.Elem, f)
	case *MapType:
		walkTypes(tt.Key, f)
		walkTypes(tt.Val, f)
	case *TupleType:
		for _, e := range tt.Elems {
			walkTypes(e, f)
		}
	case *AnonStructType:
		for _, fd := range tt.Fields {
			walkTypes(fd.Type, f)
		}
	case *FuncType:
		for _, p := range tt.Params {
			walkTypes(p, f)
		}
		walkTypes(tt.Return, f)
	case *PartialType:
		walkTypes(tt.Inner, f)
	case *InterfaceType:
		for _, a := range tt.TypeArgs {
			walkTypes(a, f)
		}
	case *ConjunctionType:
		for _, m := range tt.Members {
			walkTypes(m, f)
		}
	case *StructType:
		for _, a := range tt.TypeArgs {
			walkTypes(a, f)
		}
	case *EnumType:
		for _, a := range tt.TypeArgs {
			walkTypes(a, f)
		}
	case *DistinctType:
		for _, a := range tt.TypeArgs {
			walkTypes(a, f)
		}
	}
}
