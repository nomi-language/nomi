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
	if h := typeArgumentVarianceHint(want, have); h != "" {
		return h
	}
	return iterMaterializeHint(want, have)
}

// typeArgumentVarianceHint explains a generic value refused where the same
// type at other type arguments is expected (spec §5 *Type argument
// conversion*): a declared generic type's arguments compare exactly; a std
// collection's convert only where each element already is a value of the
// wider type, so the hint names the explicit conversion where one would be
// needed per element, and says why a function element whose parameter
// would narrow cannot convert at all.
func typeArgumentVarianceHint(want, have Type) string {
	want, have = resolveTV(want), resolveTV(have)
	wantArgs, haveArgs := containerArgs(want), containerArgs(have)
	if len(wantArgs) == 0 || len(wantArgs) != len(haveArgs) {
		return ""
	}
	for i := range wantArgs {
		w, h := resolveTV(wantArgs[i]), resolveTV(haveArgs[i])
		if containsTypeVar(w) || containsTypeVar(h) || TypesIdentical(w, h) {
			continue
		}
		if !convertibleArgs(want) {
			return fmt.Sprintf("%s does not convert to %s: a declared generic type's type arguments compare exactly, "+
				"since its values carry them; build a new %s from the value's parts", have, want, want)
		}
		if wf, ok := w.(*FuncType); ok {
			if hf, ok := h.(*FuncType); ok && len(wf.Params) == len(hf.Params) {
				return funcElementHint(want, have, wf, hf)
			}
			continue
		}
		iface, ok := w.(*InterfaceType)
		if !ok || isStdIterType(iface) {
			continue
		}
		if _, alsoIface := h.(*InterfaceType); alsoIface || carriesOwnType(h) {
			continue
		}
		why := fmt.Sprintf("%s becomes %s boxed", h, iface)
		if _, param := h.(*TypeParam_); param {
			why = fmt.Sprintf("%s may be a type that becomes %s boxed", h, iface)
		}
		return fmt.Sprintf("%s does not convert to %s: %s, so the conversion would build a new %s element by element; "+
			"write it where that is meant: `%s`", have, want, why, collectionName(want), explicitConversion(want))
	}
	return ""
}

// funcElementHint explains a collection of functions refused where one of
// another function type is expected: a parameter that would narrow is
// unsound, and any other difference needs a wrapper per element.
func funcElementHint(want, have Type, wf, hf *FuncType) string {
	for i := range wf.Params {
		wp, hp := resolveTV(wf.Params[i]), resolveTV(hf.Params[i])
		if _, wIface := wp.(*InterfaceType); wIface {
			if _, hIface := hp.(*InterfaceType); !hIface {
				return fmt.Sprintf("%s does not convert to %s: each function takes only %s, and one of type %s may be passed any %s",
					have, want, hp, wf, wp)
			}
		}
	}
	return fmt.Sprintf("%s does not convert to %s: each %s would need a wrapper to become %s, one per element; "+
		"wrap them where that is meant: `Iter.map(fs, fn f -> fn x -> f(x)) |> Iter.to_list()`", have, want, hf, wf)
}

// collectionName is how a hint names the std collection t.
func collectionName(t Type) string {
	switch t := t.(type) {
	case *ListType:
		return "List"
	case *MapType:
		return "Map"
	case *StructType:
		return t.Name
	case *EnumType:
		return t.Name
	case *DistinctType:
		return t.Name
	}
	return "value"
}

// explicitConversion is the call that converts each element of the std
// collection t where t is expected.
func explicitConversion(t Type) string {
	switch t := t.(type) {
	case *MapType:
		return "Map.map_values(m, fn v -> v)"
	case *EnumType:
		if t.Name == "Result" {
			return "Result.map(r, fn x -> x)"
		}
		return "Maybe.map(m, fn x -> x)"
	case *StructType:
		return "Iter.map(s, fn x -> x) |> Iter.to_set()"
	case *DistinctType:
		return "Iter.map(v, fn x -> x) |> Iter.to_vector()"
	}
	return "Iter.map(xs, fn x -> x) |> Iter.to_list()"
}

// containerArgs are t's type arguments as a container: a list's element, a
// map's key and value, a generic struct's or enum's arguments.
func containerArgs(t Type) []Type {
	switch t := resolveTV(t).(type) {
	case *ListType:
		return []Type{t.Elem}
	case *MapType:
		return []Type{t.Key, t.Val}
	case *StructType:
		return t.TypeArgs
	case *EnumType:
		return t.TypeArgs
	case *DistinctType:
		return t.TypeArgs
	}
	return nil
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
