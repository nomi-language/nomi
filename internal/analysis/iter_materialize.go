package analysis

import "fmt"

// mismatchHint is the help line for a value of type have refused where want
// is expected: how to narrow an enum to its embedded type, or how to
// materialize an Iter into the collection expected.
func mismatchHint(want, have Type) string {
	if h := embedsDowncastHint(want, have); h != "" {
		return h
	}
	return iterMaterializeHint(want, have)
}

// iterMaterializeHint names the function that turns an Iter into the std
// collection want when have is an Iter. An Iter is lazy and is not a
// collection, though every collection is an Iter.
func iterMaterializeHint(want, have Type) string {
	if !isStdIterType(resolveTV(have)) {
		return ""
	}
	var fn string
	switch w := resolveTV(want).(type) {
	case *ListType:
		fn = "Iter.to_list"
	case *MapType:
		fn = "Iter.to_map"
	case *StructType:
		if isStdSetType(w) {
			fn = "Iter.to_set"
		}
	case *DistinctType:
		if w.Name == "Vector" && (w.Origin == "std/vectors" || w.Origin == "") {
			fn = "Iter.to_vector"
		}
	}
	if fn == "" {
		return ""
	}
	return fmt.Sprintf("an Iter is not a collection; materialize it with `|> %s()`", fn)
}

// ifaceNarrows reports whether arg, an interface type, is passed where
// param, a concrete type, is expected. A concrete type stands where its
// interface is expected, never the reverse: an Iter is not a List.
func ifaceNarrows(param, arg Type) bool {
	if _, ok := resolveTV(arg).(*InterfaceType); !ok {
		return false
	}
	p := resolveTV(param)
	if p == nil || p == TypeAny || p == TypeInfallible {
		return false
	}
	switch p.(type) {
	case *InterfaceType, *TypeParam_, *TypeVar, *PartialType, *BoundAliasType:
		return false
	}
	return true
}
