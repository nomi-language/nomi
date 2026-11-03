package analysis

import (
	"fmt"
	"strings"
)

// Interface bounds over the types that have no declaration to hang an impl
// on: a tuple, an anonymous struct and a function type.
//
// No impl can be written for one (`impl Shape for #(Int, Int)` names no
// implementing type), so none implements a program's interface, and std's
// interfaces reach one only structurally (the spec's *Bounds and dispatch in
// generic code*):
//
//   - a tuple is Equatable, Hashable, Comparable and Display when every
//     element is;
//   - an anonymous struct is Equatable, Hashable and Display when every field
//     is, and Struct always; it is never Comparable;
//   - a function is none of these;
//   - every type is Debug.
//
// Struct, Debug, Display and Comparable are decided before this is asked
// (typeImplementsInterface), and an anonymous struct's Comparable is left
// to recordConformanceRecording, which names why it cannot have one. Every
// other bound is decided here, so `where T: Shape` and `where T: Hashable`
// reject `#(1, fn x -> x)` at the call rather than accepting it and leaving
// the IR builder to decline the body.

// structuralConformance answers whether t meets iface when t is a tuple, an
// anonymous struct or a function type, asking part for each element or
// field; decided is false for every other type, which its impls decide.
func structuralConformance(t Type, iface *InterfaceType, part func(Type) bool) (implements, decided bool) {
	resolved := resolveTypeVar(t)
	switch resolved.(type) {
	case *FuncType:
		return false, true
	case *TupleType, *AnonStructType:
	default:
		return false, false
	}
	_, anon := resolved.(*AnonStructType)
	switch {
	case isStdInterfaceNamed(iface, "Equatable"), isStdInterfaceNamed(iface, "Hashable"), isStdInterfaceNamed(iface, "Display"):
	case isStdInterfaceNamed(iface, "Comparable") && !anon:
	case isStdInterfaceNamed(iface, "Comparable"):
		return false, false
	default:
		return false, true
	}
	parts, _ := displayParts(resolved)
	for _, p := range parts {
		if !part(p) {
			return false, true
		}
	}
	return true, true
}

// isStdInterfaceNamed reports whether iface is std's interface name, declared
// in std/<lowercased name>.nomi. An unknown origin is std's, as
// isStdDistinct reads it.
func isStdInterfaceNamed(iface *InterfaceType, name string) bool {
	if iface == nil || iface.Name != name {
		return false
	}
	switch iface.Origin {
	case "", "std/" + strings.ToLower(name):
		return true
	}
	return false
}

// structuralBoundHelp is boundHelp for a tuple, an anonymous struct or a
// function type that does not meet bound: for one of std's interfaces it
// meets through its parts, the first part that does not, with that part's
// own help; for any other interface, what it can implement and what to
// write instead. ok is false for every other type.
func (c *checker) structuralBoundHelp(concrete Type, bound *InterfaceType) (help string, ok bool) {
	name := fmt.Sprint(concrete)
	resolved := resolveTypeVar(concrete)
	var kind, part, ifaces string
	switch resolved.(type) {
	case *TupleType:
		kind, part, ifaces = "a tuple", "element", "Equatable, Hashable, Comparable and Display"
	case *AnonStructType:
		if isStdInterfaceNamed(bound, "Comparable") {
			return "", false
		}
		kind, part, ifaces = "an anonymous struct", "field", "Struct, and Equatable, Hashable and Display"
	case *FuncType:
		return fmt.Sprintf("a function value implements no interface but Debug; pass a value of a type that implements `%s`", bound.Name), true
	default:
		return "", false
	}
	if !isStructuralIface(bound) {
		return fmt.Sprintf("no impl can be written for %s, which implements only %s, each when every %s does; declare a struct of your own that implements `%s`", kind, ifaces, part, bound.Name), true
	}
	parts, _ := displayParts(resolved)
	for _, p := range parts {
		if !typeSatisfiesInterfaceForImplMatch(p, bound, c.implsContext(), c.implTypeArgsContext(), nil) {
			return fmt.Sprintf("`%s` is %s when each %s is, and `%s` is not: %s", name, bound.Name, part, p, c.boundHelp(p, bound)), true
		}
	}
	return "", false
}

// mismatchHint is the package's mismatchHint, except where a tuple, an
// anonymous struct or a function value is given for an interface: then it
// is the help a bound on that interface gives the same value (boundHelp), so
// `fn f(h: Hashable)` and `fn f<T>(h: T) where T: Hashable` explain a
// rejected `#(1, q)` alike.
func (c *checker) mismatchHint(want, have Type) string {
	if iface, ok := resolveTV(want).(*InterfaceType); ok {
		switch resolveTypeVar(have).(type) {
		case *TupleType, *AnonStructType, *FuncType:
			if h := c.boundHelp(have, iface); h != "" {
				return h
			}
		}
	}
	return mismatchHint(want, have)
}

// isStructuralIface reports whether bound is one of std's interfaces a
// tuple or an anonymous struct meets through its parts.
func isStructuralIface(bound *InterfaceType) bool {
	for _, n := range []string{"Equatable", "Hashable", "Comparable", "Display"} {
		if isStdInterfaceNamed(bound, n) {
			return true
		}
	}
	return false
}
