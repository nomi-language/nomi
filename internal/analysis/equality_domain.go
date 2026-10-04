package analysis

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
)

// A function value and a lazy `Iter<T>` have no equality. A function's only
// identity is its closure, and an Iter is a function too: comparing two would
// either compare closures (so a value is unequal to an identical rebuild) or run
// both sources (so `==` could consume, and never answer on an infinite one).
// `==` and `!=` over such a value, or over a tuple, record, collection, prelude
// wrapper or struct that holds one, are rejected here, so the program never
// reaches a comparison the VM cannot answer (spec §16, "Values with no
// equality").
//
// A declared struct or enum with its own `impl Equatable` is exempt: its
// equality is whatever that impl answers. Its type arguments are not.

// noEqualityMessage is the diagnostic for `op` over operand type t, or "" when
// every part of t has equality.
func (c *checker) noEqualityMessage(t Type, op string) string {
	part, ok := c.noEqualityPart(t, map[Type]bool{})
	if !ok {
		return ""
	}
	what := "a function value"
	fix := ""
	if isStdIterType(part) {
		what = "an Iter"
		fix = "; materialize it with `Iter.to_list` to compare its elements"
	}
	if part == resolveTypeVar(t) {
		return fmt.Sprintf("`%s` cannot compare `%s`: %s has no equality%s", op, t, what, fix)
	}
	return fmt.Sprintf("`%s` cannot compare `%s`: it holds `%s`, and %s has no equality%s", op, t, part, what, fix)
}

// noEqualityPart answers the first part of t with no equality.
func (c *checker) noEqualityPart(t Type, seen map[Type]bool) (Type, bool) {
	t = resolveTypeVar(t)
	if t == nil || seen[t] {
		return nil, false
	}
	seen[t] = true
	switch ty := t.(type) {
	case *FuncType:
		return ty, true
	case *InterfaceType:
		if isStdIterType(ty) {
			return ty, true
		}
		return nil, false
	case *TupleType:
		return c.noEqualityAmong(ty.Elems, seen)
	case *ListType:
		return c.noEqualityPart(ty.Elem, seen)
	case *MapType:
		return c.noEqualityAmong([]Type{ty.Key, ty.Val}, seen)
	case *AnonStructType:
		return c.noEqualityAmongFields(ty.Fields, seen)
	case *StructType:
		// A type argument first: a generic type's equality, std's
		// `Maybe<T>` and `List<T>` among them, compares its elements.
		if part, ok := c.noEqualityAmong(ty.TypeArgs, seen); ok {
			return part, true
		}
		if c.hasEquatableImpl(ty) {
			return nil, false
		}
		return c.noEqualityAmongFields(ty.Fields, seen)
	case *EnumType:
		// A type argument first: a generic type's equality, std's
		// `Maybe<T>` and `List<T>` among them, compares its elements.
		if part, ok := c.noEqualityAmong(ty.TypeArgs, seen); ok {
			return part, true
		}
		if c.hasEquatableImpl(ty) {
			return nil, false
		}
		for _, v := range ty.Variants {
			if part, ok := c.noEqualityPart(v.DataType, seen); ok {
				return part, true
			}
			if part, ok := c.noEqualityAmongFields(v.Fields, seen); ok {
				return part, true
			}
		}
	case *DistinctType:
		if c.hasEquatableImpl(ty) {
			return nil, false
		}
		return c.noEqualityPart(ty.Inner, seen)
	}
	return nil, false
}

func (c *checker) noEqualityAmong(ts []Type, seen map[Type]bool) (Type, bool) {
	for _, t := range ts {
		if part, ok := c.noEqualityPart(t, seen); ok {
			return part, true
		}
	}
	return nil, false
}

func (c *checker) noEqualityAmongFields(fs []FieldDef, seen map[Type]bool) (Type, bool) {
	for _, f := range fs {
		if part, ok := c.noEqualityPart(f.Type, seen); ok {
			return part, true
		}
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
	if c.fa.Impls[name]["Equatable"] {
		return true
	}
	return c.fa.ProjectImpls != nil && c.fa.ProjectImpls.Impls[name]["Equatable"]
}

// isStdIterType reports whether t is std's `Iter<T>` interface. A program
// cannot declare its own `Iter`, so an unknown origin is std's.
func isStdIterType(t Type) bool {
	it, ok := t.(*InterfaceType)
	return ok && it.Name == "Iter" && (it.Origin == "std/iter" || it.Origin == "")
}

// A Set element and a Map key are found by equality and hashing, so a value
// with no equality cannot be one (spec §30). rejectKeyWithoutEquality reports
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
	case *ast.SetLit, *ast.MapLit, *ast.Call:
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
	if c.keyWithoutEqualityReported[report] {
		return ty
	}
	if c.keyWithoutEqualityReported == nil {
		c.keyWithoutEqualityReported = map[keyWithoutEqualityReport]bool{}
	}
	c.keyWithoutEqualityReported[report] = true
	role := "a Map key"
	if isStdSetType(coll) {
		role = "a Set element"
	}
	what := "a function value"
	fix := ""
	if isStdIterType(part) {
		what = "an Iter"
		fix = "; materialize it with `Iter.to_list` first"
	}
	msg := fmt.Sprintf("`%s` cannot be %s: %s has no equality or hashing%s", key, role, what, fix)
	if part != key {
		msg = fmt.Sprintf("`%s` cannot be %s: it holds `%s`, and %s has no equality or hashing%s", key, role, part, what, fix)
	}
	line, col := nodeLineCol(node)
	c.addError(line, col, msg)
	return ty
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
