package analysis

import "fmt"

// mismatchHint is the help line for a value of type have refused where want
// is expected: how to narrow an enum to its embedded type, or how to
// materialize an Iter into the collection expected.
func mismatchHint(want, have Type) string {
	if h := embedsDowncastHint(want, have); h != "" {
		return h
	}
	if h := runtimeNounHint(want, have); h != "" {
		return h
	}
	return iterMaterializeHint(want, have)
}

// runtimeNounHint explains a `String` where std/message's `Noun` is
// wanted: a noun is a `noun` declaration, whose gender and forms the
// compiler checks every message against, and text read at run time has
// none of them.
func runtimeNounHint(want, have Type) string {
	st, ok := resolveTV(want).(*StructType)
	if !ok || st.Name != "Noun" || st.Origin != "std/message" || !TypesEqual(resolveTV(have), TypeString) {
		return ""
	}
	return "a `Noun` is a `noun` declaration, whose gender and forms the compiler holds every message to; text known only at run time, such as a product's name from a database, has neither, so pass a declared noun (`shop.book`), or give the message a `String` parameter it writes as `{$name}`"
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
