package analysis

// funcShapeDiffers reports a generic parameter and an argument of which
// exactly one is a function type, where the parameter's own shape is fixed:
// `Maybe<T>` given `(String) -> Maybe<Int>`, or `(T) -> U` given an Int. No
// type argument makes them equal. A parameter that is still a bare type
// parameter or inference variable can be anything, an interface is judged
// by its impls, and a distinct type may wrap a function, so those are left
// to unification's other rules.
func funcShapeDiffers(param, arg Type) bool {
	param, arg = resolveTV(param), resolveTV(arg)
	if param == nil || arg == nil {
		return false
	}
	switch param.(type) {
	case *TypeParam_, *TypeVar, *InterfaceType, *DistinctType:
		return false
	}
	switch arg.(type) {
	case *TypeParam_, *TypeVar, *InterfaceType, *DistinctType:
		return false
	}
	if param == TypeAny || arg == TypeAny || param == TypeInfallible || arg == TypeInfallible {
		return false
	}
	_, paramFunc := param.(*FuncType)
	_, argFunc := arg.(*FuncType)
	return paramFunc != argFunc
}

// headDiffers reports a generic parameter and an argument whose outermost
// type constructors differ, where both are fixed: `Set<T>` given Unit or an
// Int, `List<T>` given a String, `(A, B)` given a three-tuple. No type
// argument makes them equal, so the argument is a mismatch even though the
// parameter's type variables are unsolved. As in funcShapeDiffers, a bare
// type parameter or inference variable, an interface, a distinct type, Any
// and Infallible are left to unification's other rules, and so are the
// widenings unify admits: an embedded type into its enum and True or False
// into Bool.
func headDiffers(param, arg Type) bool {
	param, arg = resolveTV(param), resolveTV(arg)
	if param == nil || arg == nil {
		return false
	}
	open := func(t Type) bool {
		switch t.(type) {
		case *TypeParam_, *TypeVar, *InterfaceType, *DistinctType, *BoundAliasType, *PartialType, *FuncType:
			return true
		}
		return t == TypeAny || t == TypeInfallible
	}
	if open(param) || open(arg) {
		return false
	}
	if isEmptyTuple(param) {
		param = TypeUnit
	}
	if isEmptyTuple(arg) {
		arg = TypeUnit
	}
	if isBoolVariantVsBool(param, arg) || isBoolVariantVsBool(arg, param) {
		return false
	}
	if et, ok := param.(*EnumType); ok && isEmbeddedTypeOf(arg, et) {
		return false
	}
	switch p := param.(type) {
	case *PrimitiveType:
		a, ok := arg.(*PrimitiveType)
		return !ok || !samePrimitive(p, a)
	case *ListType:
		_, ok := arg.(*ListType)
		return !ok
	case *MapType:
		_, ok := arg.(*MapType)
		return !ok
	case *TupleType:
		a, ok := arg.(*TupleType)
		return !ok || len(a.Elems) != len(p.Elems)
	case *StructType:
		a, ok := arg.(*StructType)
		return !ok || !sameNominalIdentity(p.Origin, p.Name, a.Origin, a.Name)
	case *EnumType:
		a, ok := arg.(*EnumType)
		return !ok || !sameNominalIdentity(p.Origin, p.Name, a.Origin, a.Name)
	}
	return false
}
