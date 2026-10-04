package analysis

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
)

// Diagnostics that name two types which print the same.
//
// A nominal type's `Name` is its short, user-facing name (see
// nominal_identity.go), so a mismatch between two declarations of `Point`
// renders as `expected Point, got Point`: true, and unreadable. Every
// diagnostic that formats types goes through typeSprintf (or the checker's
// typef), a drop-in for fmt.Sprintf. When two of its Type arguments render to
// the same text but contain a nominal name declared in two different files,
// both are rendered with that name qualified by its declaring file's import
// path, the way Nomi source spells it:
//
//	expected Point, got Point              ->  expected b.Point, got a.Point
//	expected List<Point>, got List<Point>  ->  expected List<b.Point>, got List<a.Point>
//	expected Ordering, got Ordering        ->  expected std/ordering.Ordering, got main.Ordering
//
// This is Go's and Rust's convention: `cannot use p (variable of struct type
// a.Point) as b.Point value`, and rustc's "expected `b::Point`, found
// `a::Point`". Only the colliding names are qualified, so `List<Point>`
// qualifies `Point` and leaves `List` alone. Arguments whose renders already
// differ are left as they are, so every other message is byte-identical to
// fmt.Sprintf's.

// typeSprintf is fmt.Sprintf with same-name nominal types disambiguated. The
// checker's typef is the same, and also spells the entry file by its name.
func typeSprintf(format string, args ...any) string {
	return sprintTypes("", format, args)
}

// typeErrorf is fmt.Errorf over typeSprintf.
func typeErrorf(format string, args ...any) error {
	return errors.New(sprintTypes("", format, args))
}

// typef is typeSprintf with the entry file spelled by its own name
// (`main.Ordering`) when the checker knows it.
func (c *checker) typef(format string, args ...any) string {
	return sprintTypes(c.entryName(), format, args)
}

// entryName is the name the project entry file's types are qualified with:
// the file's base name without `.nomi`. Empty when the checker is not
// analyzing the entry file, which leaves the choice to the type itself.
func (c *checker) entryName() string {
	if c == nil || c.fa == nil || c.fa.Origin != OriginEntry || c.fa.FilePath == "" {
		return ""
	}
	return strings.TrimSuffix(filepath.Base(c.fa.FilePath), ".nomi")
}

func sprintTypes(entry, format string, args []any) string {
	var idx []int
	for i, a := range args {
		if t, ok := a.(Type); ok && !isNilType(t) {
			idx = append(idx, i)
		}
	}
	if len(idx) < 2 {
		return fmt.Sprintf(format, args...)
	}
	// Render each Type once. Every verb these messages use (%s, %v, %q)
	// formats a Stringer exactly as it formats its String(), so the rendered
	// text stands in for the Type in the ordinary case.
	out := make([]any, len(args))
	copy(out, args)
	rendered := map[int]string{}
	for _, i := range idx {
		rendered[i] = args[i].(Type).String()
		out[i] = rendered[i]
	}
	amb := map[string]bool{}
	collide := map[int]bool{}
	for x := 0; x < len(idx); x++ {
		for y := x + 1; y < len(idx); y++ {
			if rendered[idx[x]] != rendered[idx[y]] {
				continue
			}
			a, b := args[idx[x]].(Type), args[idx[y]].(Type)
			names := ambiguousNominalNames(a, b)
			if len(names) == 0 {
				continue
			}
			for n := range names {
				amb[n] = true
			}
			collide[idx[x]] = true
			collide[idx[y]] = true
		}
	}
	for i := range collide {
		var sb strings.Builder
		writeQualifiedType(&sb, args[i].(Type), amb, entry)
		out[i] = sb.String()
	}
	return fmt.Sprintf(format, out...)
}

// isNilType reports a typed nil (`(*StructType)(nil)` in a Type), whose
// String would panic.
func isNilType(t Type) bool {
	v := reflect.ValueOf(t)
	return v.Kind() == reflect.Pointer && v.IsNil()
}

// ambiguousNominalNames returns the nominal names that occur in a and b with
// two or more different resolved origins.
func ambiguousNominalNames(a, b Type) map[string]bool {
	origins := map[string]map[string]bool{}
	visit := func(name, origin string) {
		if origin == OriginUnresolved {
			return
		}
		if origins[name] == nil {
			origins[name] = map[string]bool{}
		}
		origins[name][origin] = true
	}
	walkNominals(a, visit, 0)
	walkNominals(b, visit, 0)
	var out map[string]bool
	for name, set := range origins {
		if len(set) > 1 {
			if out == nil {
				out = map[string]bool{}
			}
			out[name] = true
		}
	}
	return out
}

// maxTypeRenderDepth bounds the walks below; a type deeper than this is not
// something a reader can use, and a cyclic TypeVar chain must not hang a
// diagnostic.
const maxTypeRenderDepth = 64

// walkNominals visits every nominal type (name, origin) in the parts of t
// that String renders.
func walkNominals(t Type, visit func(name, origin string), depth int) {
	if t == nil || isNilType(t) || depth > maxTypeRenderDepth {
		return
	}
	depth++
	each := func(ts []Type) {
		for _, x := range ts {
			walkNominals(x, visit, depth)
		}
	}
	switch v := t.(type) {
	case *PrimitiveType:
		if v.Origin != "" {
			visit(v.Name_, v.Origin)
		}
	case *StructType:
		visit(v.Name, v.Origin)
		each(v.TypeArgs)
	case *EnumType:
		visit(v.Name, v.Origin)
		each(v.TypeArgs)
	case *DistinctType:
		visit(v.Name, v.Origin)
		each(v.TypeArgs)
	case *InterfaceType:
		visit(v.Name, v.Origin)
		each(v.TypeArgs)
	case *FuncType:
		each(v.Params)
		walkNominals(v.Return, visit, depth)
	case *TupleType:
		each(v.Elems)
	case *ListType:
		walkNominals(v.Elem, visit, depth)
	case *MapType:
		walkNominals(v.Key, visit, depth)
		walkNominals(v.Val, visit, depth)
	case *AnonStructType:
		for _, f := range v.Fields {
			walkNominals(f.Type, visit, depth)
		}
	case *PartialType:
		walkNominals(v.Inner, visit, depth)
	case *TypeVar:
		walkNominals(v.Resolved, visit, depth)
	}
}

// writeQualifiedType renders t exactly as t.String() does, except that a
// nominal type whose name is in amb is prefixed with its declaring file.
// Every kind String renders by recursion is handled here; any other kind
// has no nested types and renders through its own String.
func writeQualifiedType(sb *strings.Builder, t Type, amb map[string]bool, entry string) {
	if t == nil || isNilType(t) {
		sb.WriteString("?")
		return
	}
	list := func(ts []Type) {
		for i, x := range ts {
			if i > 0 {
				sb.WriteString(", ")
			}
			if x == nil {
				sb.WriteString("?")
				continue
			}
			writeQualifiedType(sb, x, amb, entry)
		}
	}
	nominal := func(name, origin string, args []Type) {
		if amb[name] && origin != OriginUnresolved {
			sb.WriteString(originQualifier(origin, entry))
			sb.WriteString(".")
		}
		sb.WriteString(name)
		if len(args) > 0 {
			sb.WriteString("<")
			list(args)
			sb.WriteString(">")
		}
	}
	switch v := t.(type) {
	case *PrimitiveType:
		nominal(v.Name_, v.Origin, nil)
	case *StructType:
		nominal(v.Name, v.Origin, v.TypeArgs)
	case *EnumType:
		nominal(v.Name, v.Origin, v.TypeArgs)
	case *DistinctType:
		nominal(v.Name, v.Origin, v.TypeArgs)
	case *InterfaceType:
		nominal(v.Name, v.Origin, v.TypeArgs)
	case *FuncType:
		sb.WriteString("(")
		list(v.Params)
		sb.WriteString(") -> ")
		if v.Return == nil {
			sb.WriteString("Unit")
		} else {
			writeQualifiedType(sb, v.Return, amb, entry)
		}
	case *TupleType:
		sb.WriteString("(")
		list(v.Elems)
		sb.WriteString(")")
	case *ListType:
		sb.WriteString("List<")
		writeQualifiedType(sb, v.Elem, amb, entry)
		sb.WriteString(">")
	case *MapType:
		sb.WriteString("Map<")
		writeQualifiedType(sb, v.Key, amb, entry)
		sb.WriteString(", ")
		writeQualifiedType(sb, v.Val, amb, entry)
		sb.WriteString(">")
	case *AnonStructType:
		sb.WriteString("{")
		for i, f := range v.Fields {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(f.Name)
			sb.WriteString(": ")
			writeQualifiedType(sb, f.Type, amb, entry)
		}
		sb.WriteString("}")
	case *PartialType:
		if v.Inner == nil {
			sb.WriteString("Partial<?>")
			return
		}
		sb.WriteString("Partial<")
		writeQualifiedType(sb, v.Inner, amb, entry)
		sb.WriteString(">")
	case *TypeVar:
		if v.Resolved != nil {
			writeQualifiedType(sb, v.Resolved, amb, entry)
			return
		}
		sb.WriteString(v.String())
	default:
		sb.WriteString(t.String())
	}
}

// originQualifier spells a declaring file the way an import names it:
// `std/ordering`, `shapes`, `stringkit/pad`. The entry file has no import
// path of its own, so it is spelled by its file name (`main`) when the
// caller knows it, and by OriginEntry otherwise.
func originQualifier(origin, entry string) string {
	if origin == OriginEntry && entry != "" {
		return entry
	}
	return origin
}
