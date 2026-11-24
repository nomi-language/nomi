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
// A declared type with an `impl Equatable` whose function has a body is
// exempt, type arguments and fields alike: its equality is whatever that
// body answers, and the impl's where bounds are all it asks of the type
// arguments. A type whose impl answers by its shape (ImplAsksParts: a
// derive, or std's List, Map, Set and Vector, whose `equal` is a `host
// fn`) is held through its type arguments, since that impl compares
// them; its fields are the derive's or the host's to answer for. A type
// with no impl is held through every part.
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
		fix = "; collect it into a list with `Iter.to_list` to compare its elements"
	}
	if part == resolveTypeVar(t) {
		return fmt.Sprintf("`%s` cannot compare `%s`: %s has no equality%s", op, t, what, fix)
	}
	return fmt.Sprintf("`%s` cannot compare `%s`: it holds `%s`, and %s has no equality%s", op, t, part, what, fix)
}

// ownEquality is what a declared type's own impl of Equatable, or of
// Hashable, answers by.
type ownEquality uint8

const (
	// noOwnEquality: the type has no impl; `==` compares every type
	// argument and field.
	noOwnEquality ownEquality = iota
	// equalityByShape: an impl that answers by the type's shape
	// (ImplAsksParts), which compares the type arguments.
	equalityByShape
	// equalityByBody: an impl whose function has a body, which decides
	// whatever the type's parts hold.
	equalityByBody
)

// ownEqualityOf is what an impl answers by: b is the type's impl block,
// nil where none answers alone, and has whether the type has an impl.
func ownEqualityOf(has bool, b *ast.ImplBlock) ownEquality {
	switch {
	case !has:
		return noOwnEquality
	case ImplAsksParts(b):
		return equalityByShape
	}
	return equalityByBody
}

// ownEquality is what t's own impl of iface, Equatable or Hashable,
// answers by. The prelude reserves both names, so no program declares a
// second interface of either name and the name is the interface.
func (c *checker) ownEquality(t Type, iface string) ownEquality {
	if !c.hasOwnImpl(t, OriginUnresolved, iface) {
		return noOwnEquality
	}
	return ownEqualityOf(true, c.fa.implBlockFor(t, iface, OriginUnresolved, nil))
}

// noEqualityPart answers the first part of t with no equality.
func (c *checker) noEqualityPart(t Type, seen map[Type]bool) (Type, bool) {
	return noEqualityPartWith(t, c.ownEquatable, seen)
}

// ownEquatable is what t's own impl of Equatable answers by.
func (c *checker) ownEquatable(t Type) ownEquality {
	return c.ownEquality(t, "Equatable")
}

// noEqualityPartWith answers the first part of t with no equality, where
// own says what a declared type's own `impl Equatable` answers by. It is
// the one definition of what `==` accepts, which the checker's operators
// and the free conformance checks of bounds share (structural_bounds.go).
func noEqualityPartWith(t Type, own func(Type) ownEquality, seen map[Type]bool) (Type, bool) {
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
			if part, ok := noEqualityPartWith(t, own, seen); ok {
				return part, true
			}
		}
		return nil, false
	}
	amongFields := func(fs []FieldDef) (Type, bool) {
		for _, f := range fs {
			if part, ok := noEqualityPartWith(f.Type, own, seen); ok {
				return part, true
			}
		}
		return nil, false
	}
	// declared answers for a declared type: nothing past an impl with a
	// body, the type arguments past one that answers by shape.
	declared := func(t Type, args []Type) (part Type, none, decided bool) {
		switch own(t) {
		case equalityByBody:
			return nil, false, true
		case equalityByShape:
			part, none = among(args)
			return part, none, true
		}
		part, none = among(args)
		return part, none, none
	}
	switch ty := t.(type) {
	case *FuncType:
		return ty, true
	case *InterfaceType:
		if isStdIterType(ty) {
			return ty, true
		}
		return nil, false
	case *ConjunctionType:
		for _, m := range ty.Members {
			if isStdIterType(m) {
				return ty, true
			}
		}
		return nil, false
	case *TupleType:
		return among(ty.Elems)
	case *ListType:
		return noEqualityPartWith(ty.Elem, own, seen)
	case *MapType:
		return among([]Type{ty.Key, ty.Val})
	case *AnonStructType:
		return amongFields(ty.Fields)
	case *StructType:
		// A type argument first: a generic type's equality by shape,
		// std's `Option<T>` and `Set<T>` among them, compares its
		// elements.
		if part, none, decided := declared(ty, ty.TypeArgs); decided {
			return part, none
		}
		return amongFields(ty.Fields)
	case *EnumType:
		if part, none, decided := declared(ty, ty.TypeArgs); decided {
			return part, none
		}
		for _, v := range ty.Variants {
			if part, ok := noEqualityPartWith(v.DataType, own, seen); ok {
				return part, true
			}
			if part, ok := amongFields(v.Fields); ok {
				return part, true
			}
		}
	case *DistinctType:
		// As for a struct: std's `Vector<T>` compares its elements.
		if part, none, decided := declared(ty, ty.TypeArgs); decided {
			return part, none
		}
		return noEqualityPartWith(ty.Inner, own, seen)
	}
	return nil, false
}

// hasEquatableImpl reports whether a declared type carries its own
// `impl Equatable`, written or derived.
func (c *checker) hasEquatableImpl(t Type) bool {
	if c == nil || c.fa == nil || concreteTypeName(t) == "" {
		return false
	}
	return c.hasOwnImpl(t, OriginUnresolved, "Equatable")
}

// holdsNoEqualityOutsideDeclaredTypes reports whether t is or holds a
// function, an Iter or a handle in a part other than a declared type's
// field: a type argument, an element, a tuple's or a record's part. A
// declared type whose impl has a body answers for its type arguments too
// (noEqualityPartWith).
func (c *checker) holdsNoEqualityOutsideDeclaredTypes(t Type) bool {
	_, none := noEqualityPartWith(t, func(t Type) ownEquality {
		if own := c.ownEquatable(t); own != noOwnEquality {
			return own
		}
		// A declared type with no impl is reported by name, with its
		// own help.
		return equalityByShape
	}, map[Type]bool{})
	return none
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

// isStdIterType reports whether t is std's `Iter<T>` interface.
func isStdIterType(t Type) bool {
	return isStdType(t, stdIterType)
}

// A Set element and a Map key are found by equality and hashing, so a value
// with no equality cannot be one (spec §5 *Equality and Ordering Semantics*). rejectKeyWithoutEquality reports
// the first Set or Map a literal, call or pipe builds whose element or key
// has no equality. Other nodes only pass such a value along, so reporting at
// the node that built it, once per collection type per body, is enough.
// Where every key has equality, the node demands Equatable and Hashable at
// each as `==` demands Equatable at its operand (RecordingKindEqualityOperator),
// which asks no impl of a key's type and the where bounds of a written impl
// the machine compares or hashes it through.

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
	coll, key, part, ok := c.keyWithoutEquality(ty)
	if !ok {
		line, col := nodeLineCol(node)
		for _, k := range keysIn(ty) {
			for _, iface := range [...]string{"Equatable", "Hashable"} {
				c.recordConformanceRecording(k, iface, Recording{Pos: c.recPos(line, col), Kind: RecordingKindEqualityOperator, Op: "=="})
			}
		}
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
		fix = "; collect it into a list with `Iter.to_list` first"
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
func (c *checker) keyWithoutEquality(t Type) (coll, key, part Type, ok bool) {
	eachKeyIn(t, func(p, k Type) bool {
		if without, bad := c.noEqualityPart(k, map[Type]bool{}); bad {
			coll, key, part, ok = p, resolveTypeVar(k), without, true
		}
		return !ok
	})
	return coll, key, part, ok
}

// keysIn is the element of each Set and the key of each Map within t.
func keysIn(t Type) []Type {
	var keys []Type
	eachKeyIn(t, func(_, k Type) bool {
		keys = append(keys, k)
		return true
	})
	return keys
}

// eachKeyIn calls each with every Set or Map coll within t and its element
// or key k, until each answers false.
func eachKeyIn(t Type, each func(coll, k Type) bool) {
	more := true
	walkType(t, func(p Type) bool {
		if !more {
			return false
		}
		var k Type
		switch ty := p.(type) {
		case *FuncType, *InterfaceType, *ConjunctionType:
			// A function and a value held as an interface have built no
			// collection their types name: the node that builds one is
			// where it is reported (rejectKeyWithoutEquality).
			return false
		case *MapType:
			k = ty.Key
		case *StructType:
			if isStdSetType(ty) && len(ty.TypeArgs) == 1 {
				k = ty.TypeArgs[0]
			}
		}
		if k != nil {
			more = each(p, k)
		}
		return more
	})
}

// isStdSetType reports whether t is std's `Set<T>`.
func isStdSetType(t Type) bool {
	return isStdType(t, stdSetType)
}
