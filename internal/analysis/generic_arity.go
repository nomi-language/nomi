package analysis

import "fmt"

// declaredArity reports how many type arguments a nominal type's declaration
// requires, and how many the reference being resolved supplied.
//
// The four nominal kinds each carry the declaration's parameter names in
// TypeParams and the use site's arguments in TypeArgs, so one shape answers
// for all of them.
func declaredArity(t Type) (name string, want, got int, isGeneric bool) {
	switch n := t.(type) {
	case *StructType:
		return n.Name, len(n.TypeParams), len(n.TypeArgs), len(n.TypeParams) > 0
	case *EnumType:
		return n.Name, len(n.TypeParams), len(n.TypeArgs), len(n.TypeParams) > 0
	case *InterfaceType:
		return n.Name, len(n.TypeParams), len(n.TypeArgs), len(n.TypeParams) > 0
	case *DistinctType:
		return n.Name, len(n.TypeParams), len(n.TypeArgs), len(n.TypeParams) > 0
	}
	return "", 0, 0, false
}

// arityMessage names the declaration and both counts, because the two ways to
// get this wrong need different fixes: `got 0` means an argument is missing
// from a signature, `got 2` means one too many was written.
func arityMessage(name string, want, got int) string {
	unit := "type arguments"
	if want == 1 {
		unit = "type argument"
	}
	return fmt.Sprintf("`%s` expects %d %s, got %d", name, want, unit, got)
}

// TypeArgCountCode tags the error for a use of a type with more or fewer
// type arguments than the type takes. Its one argument is the type's name
// as the use writes it.
const TypeArgCountCode = "type-argument-count"

// checkArity is the one decision whether a use of the type t, written as
// name with got type arguments, writes as many as t takes (typeArgsTaken).
// It answers every kind of type a name resolves to: a generic declaration
// and one with no type parameters, a primitive, a type parameter and a
// typealias's target alike, so `S<Int>` for `struct S` is the same error as
// `Box<Int, Int>` for `struct Box<T>`.
//
// A use with no type arguments reaches here only where they cannot be
// inferred (ResolveDeclaredType); elsewhere a bare generic name leaves them
// to inference.
func checkArity(t Type, name string, got, line, col int) *TypeError {
	params, required := typeArgsTaken(t)
	if got >= required && got <= params {
		return nil
	}
	e := typeArityError(name, required, params, got, line, col)
	return &e
}

// typeArityError is the error for a use of the type name with got type
// arguments where it takes from required to params. The compiler-known
// spellings `List`, `Map` and `Partial`, resolved by name, report it too.
func typeArityError(name string, required, params, got, line, col int) TypeError {
	return TypeError{Line: line, Col: col, Message: arityRangeMessage(name, required, params, got)}.coded(TypeArgCountCode, name)
}

// typeArgsTaken answers how many type arguments a use of the registry entry
// t may write: at most params, and at least required, the parameters with
// no default. A nominal declaration takes its type parameters, built or not
// (typeParamShape), and a generic typealias takes its own. An entry that already carries type arguments, as a
// typealias of `Box<Int>` does, takes none more, and so does every other
// kind of type: a primitive, a type parameter, and the list, map, tuple,
// function or anonymous struct a typealias names.
func typeArgsTaken(t Type) (params, required int) {
	if at, ok := t.(*AliasTemplate); ok {
		return len(at.Params), len(at.Params)
	}
	if hasTypeArgs(t) {
		return 0, 0
	}
	params, required, _ = typeParamShape(t)
	return params, required
}

// hasTypeArgs reports whether t is an instance: a nominal type with type
// arguments.
func hasTypeArgs(t Type) bool {
	_, _, got, _ := declaredArity(t)
	return got > 0
}
