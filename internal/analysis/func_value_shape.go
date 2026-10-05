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
