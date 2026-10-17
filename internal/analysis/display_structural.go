package analysis

import "fmt"

// Display over the types that have no declaration to hang an impl on.
//
// A tuple renders as `#(Display(a), Display(b), ...)` and an anonymous struct
// as `{f: Display(v), ...}` (the spec's Display and Debug section), so one is
// Display exactly when every part is. A function value has no Display at
// all. Every Display question the checker asks about such a type goes
// through the two functions below: an interpolation or interface-typed
// argument (recordConformanceRecording), a `where T: Display` bound
// (typeImplementsInterface), and a bound inside a generic impl's receiver
// (`Maybe<#(Int, Int)>` against `impl<T> Display for Maybe<T> where T:
// Display`, typeSatisfiesInterfaceForImplMatch).

// displayParts is the parts a tuple or anonymous struct renders through
// Display, and false for any other type.
func displayParts(t Type) ([]Type, bool) {
	switch ty := resolveTypeVar(t).(type) {
	case *TupleType:
		return ty.Elems, true
	case *AnonStructType:
		parts := make([]Type, len(ty.Fields))
		for i, f := range ty.Fields {
			parts[i] = f.Type
		}
		return parts, true
	}
	return nil, false
}

// structuralDisplay answers whether t is Display when t is a tuple, an
// anonymous struct or a function type, by asking part for each part; decided
// is false for every other type, which its impls decide.
func structuralDisplay(t Type, part func(Type) bool) (implements, decided bool) {
	if _, fn := resolveTypeVar(t).(*FuncType); fn {
		return false, true
	}
	parts, ok := displayParts(t)
	if !ok {
		return false, false
	}
	for _, p := range parts {
		if !part(p) {
			return false, true
		}
	}
	return true, true
}

// functionNotDisplay is the error for rendering a function value through
// Display (`"${f}"`, a function in a tuple that is interpolated).
func functionNotDisplay(t Type) string {
	return fmt.Sprintf("no impl of `Display` for `%s`; a function value has no `Display`", t)
}
