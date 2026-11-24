package analysis

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
)

// Diagnostics that name two types which print the same.
//
// A nominal type's `Name` is its short, user-facing name (see
// nominal_identity.go), so a mismatch between two declarations of `Point`
// renders as `expected Point, got Point`: true, and unreadable. Every
// diagnostic that formats types goes through typeErrorf or the checker's
// typef, drop-ins for fmt.Errorf and fmt.Sprintf. When two of its Type arguments render to
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
// fmt.Sprintf's. A message read beside types it does not print
// (typefBeside: the `From` impls a `?` could not use) qualifies an argument
// that prints as one of them the same way. distinctTypeTexts is the one
// rendering; a message that builds its text without typef, such as a list
// of impl candidates or a bound's hint, renders its types through it too.

// typeErrorf is fmt.Errorf with same-name nominal types disambiguated.
func typeErrorf(format string, args ...any) error {
	return errors.New(sprintTypes("", format, args, nil))
}

// typef is fmt.Sprintf with same-name nominal types disambiguated and the
// entry file spelled by its own name (`main.Ordering`) when the checker
// knows it. An interface beside a type that is required to implement it
// (`expected Show, got User`) is named as the missing-impl message names
// it (shownInterfaceFor): `b.Show` where User implements another Show.
func (c *checker) typef(format string, args ...any) string {
	return c.typefBeside(nil, format, args...)
}

// typefBeside is typef for a message read beside types it does not name
// (distinctTypeTexts): a `?` that cannot convert a `b.Error` names it so
// where the boundary's type has an `impl From<a.Error>`.
func (c *checker) typefBeside(beside []Type, format string, args ...any) string {
	var recv Type
	for _, a := range args {
		if t, ok := a.(Type); ok && !isNilType(t) {
			if _, iface := resolveTypeVar(t).(*InterfaceType); !iface {
				if _, ok := nominalOrigin(resolveTypeVar(t)); ok {
					recv = t
					break
				}
			}
		}
	}
	if recv != nil {
		args = slices.Clone(args)
		for i, a := range args {
			if it, ok := a.(*InterfaceType); ok && it != nil {
				if shown := c.shownInterfaceFor(recv, it, true); shown != it.String() {
					args[i] = shown
				}
			}
		}
	}
	return sprintTypes(c.entryName(), format, args, beside)
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

// sprintTypes is fmt.Sprintf with each Type argument rendered as
// distinctTypeTexts renders the message's types. Every verb these messages
// use (%s, %v, %q) formats a Stringer exactly as it formats its String(),
// so the rendered text stands in for the Type.
func sprintTypes(entry, format string, args []any, beside []Type) string {
	var idx []int
	var ts []Type
	for i, a := range args {
		if t, ok := a.(Type); ok && !isNilType(t) {
			idx = append(idx, i)
			ts = append(ts, t)
		}
	}
	out := slices.Clone(args)
	for k, text := range distinctTypeTexts(entry, ts, beside) {
		out[idx[k]] = text
	}
	return fmt.Sprintf(format, out...)
}

// distinctTypeTexts is ts as one diagnostic names them: each as renderType
// renders it, except that where two of ts print the same text and hold a
// nominal name two declarations share, both are rendered with that name
// qualified by its declaring file (`a.Error`, `From<b.Error>`). beside are
// types the diagnostic is read against without naming them, such as the
// impls a type has beside the one a message says it lacks: one of ts that
// prints as one of them, with such a name, is qualified too. Every message
// that names types which may print alike renders them here: typef's, a
// collision's instantiations (instantiationLabel), the impls a call or a
// bound chooses among (candidateList, boundArgsError).
func distinctTypeTexts(entry string, ts, beside []Type) []string {
	texts := make([]string, len(ts))
	for i, t := range ts {
		texts[i] = renderType(t, entry)
	}
	besideTexts := make([]string, len(beside))
	for k, b := range beside {
		if b != nil && !isNilType(b) {
			besideTexts[k] = renderType(b, entry)
		}
	}
	amb := map[string]bool{}
	collide := map[int]bool{}
	// differ records the names a and b, which print alike, take from two
	// declarations, and reports whether there are any.
	differ := func(a, b Type) bool {
		names := ambiguousNominalNames(a, b)
		for n := range names {
			amb[n] = true
		}
		return len(names) > 0
	}
	for x := range ts {
		for y := x + 1; y < len(ts); y++ {
			if texts[x] == texts[y] && differ(ts[x], ts[y]) {
				collide[x], collide[y] = true, true
			}
		}
		for k, b := range beside {
			if besideTexts[k] != "" && texts[x] == besideTexts[k] && differ(ts[x], b) {
				collide[x] = true
			}
		}
	}
	for i := range collide {
		var sb strings.Builder
		writeQualifiedType(&sb, ts[i], func(name, _ string) bool { return amb[name] }, entry)
		texts[i] = sb.String()
	}
	return texts
}

// renderType is t as a diagnostic shows it: its String, except that a type
// variable the checker never solved, an internal name (`?23`) that means
// nothing to the reader, is `_`, as in `Option<_>`: an Option of a type the
// program does not fix.
func renderType(t Type, entry string) string {
	if !containsTypeVar(t) {
		return t.String()
	}
	var sb strings.Builder
	writeQualifiedType(&sb, t, nil, entry)
	return sb.String()
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
	walkNominals(a, visit)
	walkNominals(b, visit)
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
func walkNominals(t Type, visit func(name, origin string)) {
	walkType(t, func(p Type) bool {
		if isNilType(p) {
			return false
		}
		switch v := p.(type) {
		case *PrimitiveType:
			if v.Origin != "" {
				visit(v.Name_, v.Origin)
			}
		case *StructType:
			visit(v.Name, v.Origin)
		case *EnumType:
			visit(v.Name, v.Origin)
		case *DistinctType:
			visit(v.Name, v.Origin)
		case *InterfaceType:
			visit(v.Name, v.Origin)
		}
		return true
	})
}

// TypeTextQualifying is t as a diagnostic renders it, with each nominal
// type whose declaring file qualify accepts prefixed with that file's module
// key, as source spells a type another file declares (`repl_in1.Point`). A
// REPL spells a value's type this way, so the spelling keeps naming the
// same declaration when a later input declares another type of its name.
func TypeTextQualifying(t Type, qualify func(origin string) bool) string {
	var sb strings.Builder
	writeQualifiedType(&sb, t, func(_, origin string) bool { return qualify(origin) }, "")
	return sb.String()
}

// writeQualifiedType renders t exactly as t.String() does, except that a
// nominal type qualify accepts is prefixed with its declaring file.
// Every kind String renders by recursion is handled here; any other kind
// has no nested types and renders through its own String.
func writeQualifiedType(sb *strings.Builder, t Type, qualify func(name, origin string) bool, entry string) {
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
			writeQualifiedType(sb, x, qualify, entry)
		}
	}
	nominal := func(name, origin string, args []Type) {
		if qualify != nil && qualify(name, origin) && origin != OriginUnresolved {
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
	case *ConjunctionType:
		for i, m := range v.Members {
			if i > 0 {
				sb.WriteString(" and ")
			}
			writeQualifiedType(sb, m, qualify, entry)
		}
	case *InterfaceType:
		nominal(v.Name, v.Origin, v.TypeArgs)
	case *FuncType:
		sb.WriteString("(")
		list(v.Params)
		sb.WriteString(") -> ")
		if v.Return == nil {
			sb.WriteString("Unit")
		} else {
			writeQualifiedType(sb, v.Return, qualify, entry)
		}
	case *TupleType:
		sb.WriteString("(")
		list(v.Elems)
		sb.WriteString(")")
	case *ListType:
		sb.WriteString("List<")
		writeQualifiedType(sb, v.Elem, qualify, entry)
		sb.WriteString(">")
	case *MapType:
		sb.WriteString("Map<")
		writeQualifiedType(sb, v.Key, qualify, entry)
		sb.WriteString(", ")
		writeQualifiedType(sb, v.Val, qualify, entry)
		sb.WriteString(">")
	case *AnonStructType:
		sb.WriteString("{")
		for i, f := range v.Fields {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(f.Name)
			sb.WriteString(": ")
			writeQualifiedType(sb, f.Type, qualify, entry)
		}
		sb.WriteString("}")
	case *PartialType:
		if v.Inner == nil {
			sb.WriteString("Partial<?>")
			return
		}
		sb.WriteString("Partial<")
		writeQualifiedType(sb, v.Inner, qualify, entry)
		sb.WriteString(">")
	case *TypeVar:
		if v.Resolved != nil {
			writeQualifiedType(sb, v.Resolved, qualify, entry)
			return
		}
		sb.WriteString("_")
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
