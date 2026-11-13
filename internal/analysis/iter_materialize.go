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
	if wantP, ok := isRouteType(want); ok && wantP == TypeUnit {
		if haveP, ok := isRouteType(have); ok && haveP != TypeUnit {
			// routeCallHint says which function takes the parameters.
			return ""
		}
	}
	if h := typeArgumentVarianceHint(want, have); h != "" {
		return h
	}
	return iterMaterializeHint(want, have)
}

// typeArgumentVarianceHint explains a generic value refused where the same
// type at other type arguments is expected (spec §5 *Covariant
// conversion*): a type argument at an invariant parameter compares exactly,
// one at a contravariant parameter converts only the other way, and a
// collection of functions whose parameter would narrow cannot convert.
func typeArgumentVarianceHint(want, have Type) string {
	want, have = resolveTV(want), resolveTV(have)
	if wf, ok := want.(*FuncType); ok {
		if hf, ok := have.(*FuncType); ok && len(wf.Params) == len(hf.Params) {
			why := unimplementedPart(normalizeReturn(wf.Return), normalizeReturn(hf.Return))
			for i := range wf.Params {
				if why == "" {
					// The expected function's argument is passed to have's.
					why = unimplementedPart(hf.Params[i], wf.Params[i])
				}
			}
			if why != "" {
				return fmt.Sprintf("%s does not convert to %s: %s", have, want, why)
			}
		}
		return ""
	}
	wantArgs, haveArgs := containerArgs(want), containerArgs(have)
	if len(wantArgs) == 0 || len(wantArgs) != len(haveArgs) || !sameGenericDecl(want, have) {
		return ""
	}
	for i := range wantArgs {
		w, h := resolveTV(wantArgs[i]), resolveTV(haveArgs[i])
		if containsTypeVar(w) || containsTypeVar(h) || TypesIdentical(w, h) {
			continue
		}
		name, param := genericNames(want, i)
		// A distinct type holds its parameter in its inner type, a struct
		// or enum in its fields.
		holds, take, takes := "fields both hold", "take", "fields only take"
		if _, distinct := want.(*DistinctType); distinct {
			holds, take, takes = "inner type both holds", "takes", "inner type only takes"
		}
		switch argVariance(want, i) {
		case VarianceInvariant:
			if len(TypeParamVariances(want)) > 0 {
				return fmt.Sprintf("%s does not convert to %s: %s's type parameter %s is invariant, since %s's %s a %s and %s one; "+
					"build a new %s from the value's parts", have, want, name, param, name, holds, param, take, want)
			}
			return fmt.Sprintf("%s does not convert to %s: %s's type arguments compare exactly, since its values carry them; "+
				"build a new %s from the value's parts", have, want, name, want)
		case VarianceContravariant:
			return fmt.Sprintf("%s does not convert to %s: %s's type parameter %s is contravariant, since %s's %s a %s, "+
				"so it converts only where %s converts to %s", have, want, name, param, name, takes, param, w, h)
		}
		if wf, ok := w.(*FuncType); ok {
			if hf, ok := h.(*FuncType); ok && len(wf.Params) == len(hf.Params) {
				if hint := funcElementHint(want, have, wf, hf); hint != "" {
					return hint
				}
			}
		}
		if why := unimplementedPart(w, h); why != "" {
			return fmt.Sprintf("%s does not convert to %s: %s", have, want, why)
		}
	}
	return ""
}

// unimplementedPart says which part of have, a covariant position's type,
// does not implement the interface want holds at the same place, or "" when
// none is found: `Unit does not implement Display` for a Maybe<Unit> where
// a Maybe<Display> is expected.
func unimplementedPart(want, have Type) string {
	want, have = resolveTV(want), resolveTV(have)
	if containsTypeVar(want) || containsTypeVar(have) || TypesIdentical(want, have) {
		return ""
	}
	if it, ok := want.(*InterfaceType); ok && !isStdIterType(it) {
		if _, param := have.(*TypeParam_); param {
			return ""
		}
		// An interface value converts to no other interface: `Named`
		// does not implement Display, though each value it holds may.
		return fmt.Sprintf("%s does not implement %s", have, it)
	}
	if !sameGenericDecl(want, have) {
		return ""
	}
	ws, hs := containerArgs(want), containerArgs(have)
	if len(ws) != len(hs) {
		return ""
	}
	for i := range ws {
		if argVariance(want, i) != VarianceCovariant {
			continue
		}
		if why := unimplementedPart(ws[i], hs[i]); why != "" {
			return why
		}
	}
	return ""
}

// sameGenericDecl reports whether a and b are instances of one generic
// type: two Lists, two Maps, or one declaration's.
func sameGenericDecl(a, b Type) bool {
	switch a := a.(type) {
	case *ListType:
		_, ok := b.(*ListType)
		return ok
	case *MapType:
		_, ok := b.(*MapType)
		return ok
	case *StructType:
		b, ok := b.(*StructType)
		return ok && sameNominalIdentity(a.Origin, a.Name, b.Origin, b.Name)
	case *EnumType:
		b, ok := b.(*EnumType)
		return ok && sameNominalIdentity(a.Origin, a.Name, b.Origin, b.Name)
	case *DistinctType:
		b, ok := b.(*DistinctType)
		return ok && sameNominalIdentity(a.Origin, a.Name, b.Origin, b.Name)
	}
	return false
}

// genericNames is how a hint names the generic type t and its type
// parameter i.
func genericNames(t Type, i int) (string, string) {
	var name string
	var params []string
	switch t := t.(type) {
	case *StructType:
		name, params = t.Name, t.TypeParams
	case *EnumType:
		name, params = t.Name, t.TypeParams
	case *DistinctType:
		name, params = t.Name, t.TypeParams
	}
	if i < len(params) {
		return name, params[i]
	}
	return name, "T"
}

// funcElementHint explains a collection of functions refused where one of
// another function type is expected because a parameter would narrow: each
// function takes only its own parameter type, and the wider type may be
// passed any value of the interface. Empty for any other difference.
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
	return ""
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

// runtimeNounHint explains a `String` where std/phrase's `Noun` is
// wanted: a noun is a `noun` declaration, whose gender and forms the
// compiler checks every message against, and text read at run time has
// none of them.
func runtimeNounHint(want, have Type) string {
	st, ok := resolveTV(want).(*StructType)
	if !ok || st.Name != "Noun" || st.Origin != "std/phrase" || !TypesEqual(resolveTV(have), TypeString) {
		return ""
	}
	return "a `Noun` is a `noun` declaration, whose gender and forms the compiler holds every phrase to; text known only at run time, such as a product's name from a database, has neither, so pass a declared noun (`shop.book`), or give the phrase a `String` parameter it writes as `{$name}`"
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
