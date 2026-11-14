package analysis

import (
	"fmt"
	"strings"
)

// Interface bounds over the types that have no declaration to hang an impl
// on: a tuple, an anonymous struct and a function type.
//
// No impl can be written for one (`impl Shape for (Int, Int)` names no
// implementing type), so none implements a program's interface, and std's
// interfaces reach one only structurally (the spec's *Bounds and dispatch in
// generic code*):
//
//   - a tuple is Equatable, Hashable, Comparable and Display when every
//     element is;
//   - an anonymous struct is Equatable, Hashable, Display, and std/json's
//     ToJson and FromJson when every field is, and Struct always; it is
//     never Comparable;
//   - a function is none of these;
//   - every type is Debug.
//
// Struct, Debug, Display and Comparable are decided before this is asked
// (typeImplementsInterface), and an anonymous struct's Comparable is left
// to recordConformanceRecording, which names why it cannot have one. Every
// other bound is decided here, so `where T: Shape` and `where T: Hashable`
// reject `(1, fn x -> x)` at the call rather than accepting it and leaving
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
	case anon && isStdJsonIface(iface):
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

// A `where T: Equatable` or `where T: Hashable` bound means the type has
// equality, which is what `==` accepts and what a Map key or Set element
// needs (spec §5 *Equality and Ordering Semantics*). So a declared struct,
// enum or distinct type with no impl of the bound's interface of its own
// meets it structurally when it has equality: it holds no function, Iter or
// handle to running state at any depth (noEqualityPartWith). Its equality is
// then `==`'s, which compares field by field and reaches any nested type's
// own `impl Equatable`, and its hash is the structural hash a Map key gets.
// A type with an impl of the interface meets it through that impl, decided
// by the impl tables as before.

// structuralEqualityConformance answers whether t meets iface, std's
// Equatable or Hashable, by its structural equality: decided is true when t
// is a declared struct, enum or distinct type with no impl of iface of its
// own (hasImpl, by declared type and interface name). arg decides each
// type argument, so a type parameter meets the bound only through its own
// bounds. Every other type and interface is left undecided.
func structuralEqualityConformance(t Type, iface *InterfaceType, hasImpl func(t Type, iface string) bool, arg func(Type) bool) (implements, decided bool) {
	if !isStdInterfaceNamed(iface, "Equatable") && !isStdInterfaceNamed(iface, "Hashable") {
		return false, false
	}
	resolved := resolveTypeVar(t)
	var name string
	var args []Type
	switch ty := resolved.(type) {
	case *StructType:
		name, args = ty.Name, ty.TypeArgs
	case *EnumType:
		name, args = ty.Name, ty.TypeArgs
	case *DistinctType:
		name, args = ty.Name, ty.TypeArgs
	default:
		return false, false
	}
	if name == "" || hasImpl(resolved, iface.Name) {
		return false, false
	}
	hasEquatable := func(t Type) bool { return hasImpl(t, "Equatable") }
	if _, none := noEqualityPartWith(resolved, hasEquatable, map[Type]bool{}); none {
		return false, true
	}
	for _, a := range args {
		if ContainsTypeParam(a) && !arg(a) {
			return false, true
		}
	}
	return true, true
}

// partMeetsInterfaceValue reports whether p, a part of a tuple or anonymous
// struct entering a position of interface type iface, implements iface. An
// interface value holds an implementor, and a declared type with no impl of
// its own is none: only a bound takes one by its equality, so here the
// parts are held to the impl tables, at any depth of tuples and records.
func partMeetsInterfaceValue(p Type, iface *InterfaceType, impls []ImplTables, implTypeArgs []map[string]map[string][]*ImplTypeArgs) bool {
	if implements, decided := structuralConformance(p, iface, func(q Type) bool {
		return containsTypeVar(q) || partMeetsInterfaceValue(q, iface, impls, implTypeArgs)
	}); decided {
		return implements
	}
	if _, byEquality := structuralEqualityConformance(p, iface, implementedIn(impls), func(Type) bool { return true }); byEquality {
		return false
	}
	return typeSatisfiesInterfaceForImplMatch(p, iface, impls, implTypeArgs, nil)
}

// implementedIn is structuralEqualityConformance's hasImpl over impls, by
// type name.
func implementedIn(impls []ImplTables) func(Type, string) bool {
	return func(t Type, iface string) bool {
		name := interfaceImplName(t)
		return name != "" && interfaceImplemented(t, iface, "", impls)
	}
}

// hasOwnImpl is structuralEqualityConformance's hasImpl for the checker: t
// has an impl of iface by its own declaration, not one a same-named type of
// another module has (missing_impl_identity.go). Where the identity index
// cannot judge the pair, the bare-name answer stands.
func (c *checker) hasOwnImpl(t Type, iface string) bool {
	if c == nil || c.fa == nil {
		return false
	}
	name := interfaceImplName(t)
	if name == "" || !c.fa.HasImpl(name, iface) {
		return false
	}
	p := c.fa.Program()
	return p.ImplIndex.suppliesReceiver(t, name, iface, p.unindexedOrigins)
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

// isStdJsonIface reports whether iface is std/json's ToJson or FromJson,
// which an anonymous struct meets through its fields: its JSON is an object
// keyed by its field names (internal/irbuild/irrecordjson.go).
func isStdJsonIface(iface *InterfaceType) bool {
	if iface == nil || iface.Name != "ToJson" && iface.Name != "FromJson" {
		return false
	}
	return iface.Origin == "" || iface.Origin == "std/json"
}

// structuralBoundHelp is boundHelp for a tuple, an anonymous struct or a
// function type that does not meet bound: for one of std's interfaces it
// meets through its parts, the first part that does not, with that part's
// own help; for any other interface, what it can implement and what to
// write instead. ok is false for every other type.
func (c *checker) structuralBoundHelp(concrete Type, bound *InterfaceType) (help string, ok bool) {
	if isParseFields(bound) {
		if help, ok := c.parseFieldsHelp(concrete); ok {
			return fmt.Sprintf("`%s` is read from text and written as text field by field, so %s", concrete, help), true
		}
	}
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
		kind, part, ifaces = "an anonymous struct", "field", "Struct, and Equatable, Hashable, Display, ToJson and FromJson"
	case *FuncType:
		return fmt.Sprintf("a function value implements no interface but Debug; pass a value of a type that implements `%s`", bound.Name), true
	default:
		return "", false
	}
	_, anon := resolved.(*AnonStructType)
	if !isStructuralIface(bound) && !(anon && isStdJsonIface(bound)) {
		return fmt.Sprintf("no impl can be written for %s, which implements only %s, each when every %s does; declare a struct of your own that implements `%s`", kind, ifaces, part, bound.Name), true
	}
	parts, _ := displayParts(resolved)
	for _, p := range parts {
		meets := typeSatisfiesInterfaceForImplMatch(p, bound, c.implsContext(), c.implTypeArgsContext(), nil)
		if c.helpForValue {
			meets = partMeetsInterfaceValue(p, bound, c.implsContext(), c.implTypeArgsContext())
		}
		if !meets {
			return fmt.Sprintf("`%s` is %s when each %s is, and `%s` is not: %s", name, bound.Name, part, p, c.boundHelp(p, bound)), true
		}
	}
	return "", false
}

// mismatchHint is the package's mismatchHint, except where a tuple, an
// anonymous struct or a function value is given for an interface: then it
// is the help a bound on that interface gives the same value (boundHelp), so
// `fn f(h: Hashable)` and `fn f<T>(h: T) where T: Hashable` explain a
// rejected `(1, q)` alike.
func (c *checker) mismatchHint(want, have Type) string {
	if conj, ok := resolveTV(want).(*ConjunctionType); ok {
		return c.conjunctionMismatchHint(conj, have)
	}
	if iface, ok := resolveTV(want).(*InterfaceType); ok {
		switch resolveTypeVar(have).(type) {
		case *TupleType, *AnonStructType, *FuncType:
			// The value enters an interface value, which holds only an
			// implementor (partMeetsInterfaceValue).
			saved := c.helpForValue
			c.helpForValue = true
			h := c.boundHelp(have, iface)
			c.helpForValue = saved
			if h != "" {
				return h
			}
		case *TypeParam_:
			// A type parameter enters an interface position through a
			// bound; the help shows the declaration with it added.
			tp := resolveTypeVar(have).(*TypeParam_)
			if tp.Name_ != "self" && tp != c.defaultSelf && c.fnTypeParams[tp.Name_] == tp {
				_, help := c.typeParamBoundOwner(tp, iface.String())
				return help
			}
		case *StructType, *EnumType, *DistinctType, *PrimitiveType:
			// A value of a type that does not implement the interface: the
			// help a bound gives the same type (`derive`, an impl block, or
			// the types that implement a std interface), so a parameter
			// written as an interface value explains a wrong argument as a
			// type parameter bounded by it does.
			if h := mismatchHint(want, have); h != "" {
				return h
			}
			return c.boundHelp(have, iface)
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

// conjunctionMismatchHint names the member of conj a value of type have
// does not enter (spec §12 *Interface conjunctions*): one an interface value
// does not hold, or one the type does not implement, with the help a bound
// gives that type.
func (c *checker) conjunctionMismatchHint(conj *ConjunctionType, have Type) string {
	held := InterfaceMembers(resolveTypeVar(have))
	for _, m := range conj.Members {
		if held != nil {
			found := false
			for _, h := range held {
				found = found || sameNominalIdentity(h.Origin, h.Name, m.Origin, m.Name)
			}
			if !found {
				return fmt.Sprintf("a %s value need not implement %s, so it does not convert to %s", have, m, conj)
			}
			continue
		}
		if c.unify(m, have, nil) == nil {
			continue
		}
		msg := fmt.Sprintf("%s does not implement %s", have, m)
		if h := c.mismatchHint(m, have); h != "" {
			msg += "; " + h
		}
		return msg
	}
	return ""
}
