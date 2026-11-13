package analysis

import (
	"fmt"

	"github.com/nomi-language/nomi/rt"

	"github.com/nomi-language/nomi/internal/ast"
)

// A function value and a lazy `Iter<T>` have no equality. A function's only
// identity is its closure, and an Iter is a function too: comparing two would
// either compare closures (so a value is unequal to an identical rebuild) or run
// both sources (so `==` could consume, and never answer on an infinite one).
// `==` and `!=` over such a value, or over a tuple, record, collection, prelude
// wrapper or struct that holds one, are rejected here, so the program never
// reaches a comparison the VM cannot answer (spec §5, "Functions and Iters
// cannot be compared").
//
// A declared struct or enum with its own `impl Equatable` is exempt: its
// equality is whatever that impl answers. Its type arguments are not.
//
// A handle has no equality either: every std host type that is not a value
// (rt.StdHandleTypes), `Task<T>`, `Sender<T>`, `Context`, a `Reader`, a
// `File`, a `Child` and a `Conn` among them. Each is a reference to
// something the runtime runs or holds outside the program, with no value to
// compare, and the runtime answers no equality for one. So `==` over one,
// or over a value holding one, is rejected as it is for a function.

// noEqualityMessage is the diagnostic for `op` over operand type t, or "" when
// every part of t has equality.
func (c *checker) noEqualityMessage(t Type, op string) string {
	part, ok := c.noEqualityPart(t, map[Type]bool{})
	if !ok {
		return ""
	}
	what := noEqualityWhat(part)
	fix := ""
	if isStdIterType(part) {
		fix = "; materialize it with `Iter.to_list` to compare its elements"
	}
	if part == resolveTypeVar(t) {
		return fmt.Sprintf("`%s` cannot compare `%s`: %s has no equality%s", op, t, what, fix)
	}
	return fmt.Sprintf("`%s` cannot compare `%s`: it holds `%s`, and %s has no equality%s", op, t, part, what, fix)
}

// noEqualityPart answers the first part of t with no equality.
func (c *checker) noEqualityPart(t Type, seen map[Type]bool) (Type, bool) {
	return noEqualityPartWith(t, c.hasEquatableImpl, seen)
}

// noEqualityPartWith answers the first part of t with no equality, where
// hasImpl reports whether a declared type carries its own `impl Equatable`.
// It is the one definition of what `==` accepts, which the checker's
// operators and the free conformance checks of bounds share
// (structural_bounds.go).
func noEqualityPartWith(t Type, hasImpl func(Type) bool, seen map[Type]bool) (Type, bool) {
	t = resolveTypeVar(t)
	if t == nil || seen[t] {
		return nil, false
	}
	seen[t] = true
	if isStdHandleType(t) {
		return t, true
	}
	among := func(ts []Type) (Type, bool) {
		for _, t := range ts {
			if part, ok := noEqualityPartWith(t, hasImpl, seen); ok {
				return part, true
			}
		}
		return nil, false
	}
	amongFields := func(fs []FieldDef) (Type, bool) {
		for _, f := range fs {
			if part, ok := noEqualityPartWith(f.Type, hasImpl, seen); ok {
				return part, true
			}
		}
		return nil, false
	}
	switch ty := t.(type) {
	case *FuncType:
		return ty, true
	case *InterfaceType:
		if isStdIterType(ty) {
			return ty, true
		}
		return nil, false
	case *TupleType:
		return among(ty.Elems)
	case *ListType:
		return noEqualityPartWith(ty.Elem, hasImpl, seen)
	case *MapType:
		return among([]Type{ty.Key, ty.Val})
	case *AnonStructType:
		return amongFields(ty.Fields)
	case *StructType:
		// A type argument first: a generic type's equality, std's
		// `Maybe<T>` and `List<T>` among them, compares its elements.
		if part, ok := among(ty.TypeArgs); ok {
			return part, true
		}
		if hasImpl(ty) {
			return nil, false
		}
		return amongFields(ty.Fields)
	case *EnumType:
		// A type argument first: a generic type's equality, std's
		// `Maybe<T>` and `List<T>` among them, compares its elements.
		if part, ok := among(ty.TypeArgs); ok {
			return part, true
		}
		if hasImpl(ty) {
			return nil, false
		}
		for _, v := range ty.Variants {
			if part, ok := noEqualityPartWith(v.DataType, hasImpl, seen); ok {
				return part, true
			}
			if part, ok := amongFields(v.Fields); ok {
				return part, true
			}
		}
	case *DistinctType:
		// A type argument first, as for a struct: std's `Vector<T>` has
		// its own `impl Equatable`, which compares its elements.
		if part, ok := among(ty.TypeArgs); ok {
			return part, true
		}
		if hasImpl(ty) {
			return nil, false
		}
		return noEqualityPartWith(ty.Inner, hasImpl, seen)
	}
	return nil, false
}

// hasEquatableImpl reports whether a declared type carries its own
// `impl Equatable`, written or derived.
func (c *checker) hasEquatableImpl(t Type) bool {
	if c == nil || c.fa == nil {
		return false
	}
	name := concreteTypeName(t)
	if name == "" {
		return false
	}
	return c.hasOwnImpl(t, "Equatable")
}

// noEqualityWhat names the kind of value part is, for a diagnostic.
func noEqualityWhat(part Type) string {
	switch {
	case isStdIterType(part):
		return "an Iter"
	case isStdHandleType(part):
		return "a handle to running state"
	}
	return "a function value"
}

// stdHandleTypes are std's handles, by declaring module: every std host
// type that is not a value (rt.StdHandleTypes), a task, a channel half, a
// stream, a file, a process, a connection and the like.
var stdHandleTypes = func() map[[2]string]bool {
	m := map[[2]string]bool{}
	for _, t := range rt.StdHandleTypes() {
		m[[2]string{t.Origin, t.Name}] = true
	}
	return m
}()

// isStdHandleType reports whether t is one of stdHandleTypes.
func isStdHandleType(t Type) bool {
	switch ty := t.(type) {
	case *PrimitiveType:
		return stdHandleTypes[[2]string{ty.Origin, ty.Name_}]
	case *DistinctType:
		return stdHandleTypes[[2]string{ty.Origin, ty.Name}]
	}
	return false
}

// isStdIterType reports whether t is std's `Iter<T>` interface. A program
// cannot declare its own `Iter`, so an unknown origin is std's.
func isStdIterType(t Type) bool {
	it, ok := t.(*InterfaceType)
	return ok && it.Name == "Iter" && (it.Origin == "std/iter" || it.Origin == "")
}

// A Set element and a Map key are found by equality and hashing, so a value
// with no equality cannot be one (spec §5 *Equality and Ordering Semantics*). rejectKeyWithoutEquality reports
// the first Set or Map a literal, call or pipe builds whose element or key
// has no equality. Other nodes only pass such a value along, so reporting at
// the node that built it, once per collection type per body, is enough.

// keyWithoutEqualityReport identifies one reported collection type in one
// body.
type keyWithoutEqualityReport struct {
	body *ast.FuncDef
	coll string
}

// rejectKeyWithoutEquality reports ty, the type node answers, when node
// builds a Set or Map whose element or key has no equality, and answers ty.
func (c *checker) rejectKeyWithoutEquality(node ast.Node, ty Type) Type {
	switch n := node.(type) {
	case *ast.MapLit, *ast.Call:
	case *ast.ListLit:
		if n.Kind != ast.SeqSet {
			return ty
		}
	case *ast.Binary:
		if n.Op != "|>" {
			return ty
		}
	default:
		return ty
	}
	coll, key, part, ok := c.keyWithoutEquality(ty, map[Type]bool{})
	if !ok {
		return ty
	}
	report := keyWithoutEqualityReport{body: c.currentFnDecl, coll: coll.String()}
	if i, seen := c.keyWithoutEqualityReported[report]; seen && i < len(c.errors) && c.errors[i].Message == c.keyWithoutEqualityMessage(coll, key, part) {
		// Reported, and still among the errors: a speculative check that
		// reported it and then dropped its errors reports it again.
		return ty
	}
	if c.keyWithoutEqualityReported == nil {
		c.keyWithoutEqualityReported = map[keyWithoutEqualityReport]int{}
	}
	c.keyWithoutEqualityReported[report] = len(c.errors)
	line, col := nodeLineCol(node)
	c.addError(line, col, c.keyWithoutEqualityMessage(coll, key, part))
	return ty
}

// keyWithoutEqualityMessage is the diagnostic for a Set or Map coll whose
// element or key, key, holds part, a value with no equality.
func (c *checker) keyWithoutEqualityMessage(coll, key, part Type) string {
	role := "a Map key"
	if isStdSetType(coll) {
		role = "a Set element"
	}
	what := noEqualityWhat(part)
	fix := ""
	if isStdIterType(part) {
		fix = "; materialize it with `Iter.to_list` first"
	}
	msg := fmt.Sprintf("`%s` cannot be %s: %s has no equality or hashing%s", key, role, what, fix)
	if part != key {
		msg = fmt.Sprintf("`%s` cannot be %s: it holds `%s`, and %s has no equality or hashing%s", key, role, part, what, fix)
	}
	return msg
}

// keyWithoutEquality answers the first Set or Map within t whose element or
// key has no equality: the collection, its element or key, and the part of
// that with no equality.
func (c *checker) keyWithoutEquality(t Type, seen map[Type]bool) (coll, key, part Type, ok bool) {
	t = resolveTypeVar(t)
	if t == nil || seen[t] {
		return nil, nil, nil, false
	}
	seen[t] = true
	var inner []Type
	switch ty := t.(type) {
	case *MapType:
		if p, bad := c.noEqualityPart(ty.Key, map[Type]bool{}); bad {
			return ty, resolveTypeVar(ty.Key), p, true
		}
		inner = []Type{ty.Key, ty.Val}
	case *StructType:
		if isStdSetType(ty) && len(ty.TypeArgs) == 1 {
			if p, bad := c.noEqualityPart(ty.TypeArgs[0], map[Type]bool{}); bad {
				return ty, resolveTypeVar(ty.TypeArgs[0]), p, true
			}
		}
		inner = ty.TypeArgs
	case *EnumType:
		inner = ty.TypeArgs
	case *DistinctType:
		inner = ty.TypeArgs
	case *ListType:
		inner = []Type{ty.Elem}
	case *TupleType:
		inner = ty.Elems
	case *AnonStructType:
		for _, f := range ty.Fields {
			inner = append(inner, f.Type)
		}
	}
	for _, it := range inner {
		if coll, key, part, ok := c.keyWithoutEquality(it, seen); ok {
			return coll, key, part, true
		}
	}
	return nil, nil, nil, false
}

// isStdSetType reports whether t is std's `Set<T>`. A program may declare its
// own `Set`, whose origin is its file; an unknown origin is std's.
func isStdSetType(t Type) bool {
	st, ok := t.(*StructType)
	return ok && st.Name == "Set" && (st.Origin == "std/sets" || st.Origin == "")
}
