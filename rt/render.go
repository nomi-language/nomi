package rt

import (
	"fmt"
	"strings"
)

// Nomi's three renderings of a Value, over the runtime's representations.
//
//	RowText      the assertion report's `values:` row
//	DisplayText  a value's structural Display
//	DebugText    the structural Debug.inspect          (internal/vm's debugTextWith)
//
// They are three functions because they disagree, and the disagreements are
// the contract. internal/ir/render.go records six:
//
//	a Decimal   Row `1.50`               Debug `1.50d`
//	a Dynamic   Row `<dynamic: string>`  Debug refuses; std's impl is dispatched
//	a String    Row `"a"b"` unescaped    Debug `"a\"b"`, and Display is raw
//	a struct    Row sorted fields        Debug refuses a named struct; its impl,
//	                                     which prints declaration order, is dispatched
//	a record    Row Row leaves           Debug Debug leaves (both sorted by name)
//	a hand impl Row honours a user's      Debug honours it, through the hook
//	            (RowTextWith's hook)
//
// Each row is kept by construction below rather than by a special case: Row
// and Display recurse through RowText, Debug recurses through DebugText, and
// only DebugText and RowTextWith take a hook. The engine's row hook owns a
// user's hand-written `impl Debug` and nothing else, so a std type's Debug
// (Decimal's `d`) and a derived or universal one never reach a row. rt/render_test.go pins all six, and the golden
// files in testdata/expectations pin what programs print.

// RowText is how a Value reads in an assertion's `values:` row: a String is
// quoted (without escaping) and everything else is its DisplayText. Nested
// values render by this same rule, so a String inside a List is quoted too.
func RowText(v any) string {
	if s, ok := v.(string); ok {
		return InspectString(s)
	}
	return DisplayText(v)
}

// RowTextWith is RowText consulting hook first at every level, so a value
// whose type has a hand-written `impl Debug` reads in the row as that impl
// renders it, at the top or nested in a container. The engine's hook owns
// exactly the types with such an impl; everything else is RowText's.
// RowTextWith(v, nil) is RowText(v).
func RowTextWith(v any, hook DebugHook) (string, error) {
	if hook == nil {
		return RowText(v), nil
	}
	var first error
	var elem func(any) string
	elem = func(x any) string {
		if s, owned, err := hook(x); owned || err != nil {
			if err != nil && first == nil {
				first = err
			}
			return s
		}
		if s, ok := x.(string); ok {
			return InspectString(s)
		}
		switch x.(type) {
		case *List[any], Vector[any], Map[any, any], *Record:
			return DisplayTextWith(x, elem)
		}
		return DisplayText(x)
	}
	s := elem(v)
	return s, first
}

// DisplayText is a Value's structural Display: a top-level String is itself,
// and a container renders its elements by RowText. A user's `impl Display`
// is the engine's to dispatch before reaching this.
func DisplayText(v any) string {
	switch x := v.(type) {
	case int64:
		return FormatInt(x)
	case float64:
		return FormatFloat(x)
	case bool:
		return FormatBool(x)
	case string:
		return x
	case Decimal:
		return x.Display()
	case Byte:
		return InspectByte(x)
	case Bytes:
		return InspectBytes(x)
	case Unit:
		return InspectUnit(x)
	case Dynamic:
		return InspectDynamic(x)
	case *List[any]:
		return FormatListCells[any, List[any]](x, RowText)
	case Vector[any]:
		return FormatVector(x, RowText)
	case Map[any, any]:
		return FormatMap(x, RowText, RowText)
	case *Record:
		return displayRecord(x)
	case HostHandle:
		return "<" + ShortTypeName(x.TypeName) + ">"
	case Seq[any]:
		if x.Src != nil {
			return DisplayText(x.Src)
		}
		return InspectSeq(x)
	case Opaque:
		return x.OpaqueText()
	}
	return fmt.Sprintf("<%T>", v)
}

// DisplayTextWith is Display's container rule: DisplayText's shapes, with
// every nested value — a tuple's components, a record's fields, a list's,
// vector's or map's elements, a variant's payload — rendered by elem, which is
// the engine's Display over a nested value (a declared type's `impl Display`,
// or this again). The spec's intrinsics table: `(Display(a), Display(b))`,
// `{f: Display(v)}`, `[Display(v1), ...]`, `Some(Display(v))`. DisplayText is
// the assertion row's rendering, whose nested Strings are quoted.
func DisplayTextWith(v any, elem func(any) string) string {
	switch x := v.(type) {
	case *List[any]:
		return FormatListCells[any, List[any]](x, elem)
	case Vector[any]:
		return FormatVector(x, elem)
	case Map[any, any]:
		return FormatMap(x, elem, elem)
	case *Record:
		return displayRecordWith(x, elem)
	}
	return DisplayText(v)
}

func displayRecord(r *Record) string { return displayRecordWith(r, RowText) }

func displayRecordWith(r *Record, elem func(any) string) string {
	switch r.Desc.Kind {
	case KindTuple:
		return FormatTuple(recordParts(r, elem))
	case KindEnum:
		v := r.Variant()
		switch v.Shape {
		case VariantEmbedded:
			return elem(r.Field(0))
		case VariantBare:
			return v.Name
		case VariantPositional:
			return FormatVariant(v.Name, elem(r.Field(0)))
		}
		return FormatVariant(v.Name, InspectStruct(ShortTypeName(v.Name), namedParts(r, elem)))
	case KindDistinct:
		if r.NumFields() == 0 {
			return r.Desc.Short
		}
		return FormatVariant(r.Desc.Short, elem(r.Field(0)))
	}
	if r.Desc.Name == "sets.Set" {
		// A Set reads as its members, `{1, 2}`, as its Debug does, never as
		// the `items` map it is built on.
		if keys, ok := setMembers(r); ok {
			return FormatSetElems(keys, elem)
		}
	}
	return InspectStruct(r.Desc.Short, namedParts(r, elem))
}

// setMembers is a Set record's members in insertion order.
func setMembers(r *Record) ([]any, bool) {
	raw, _ := r.FieldNamed("items")
	items, ok := raw.(Map[any, any])
	if !ok {
		return nil, false
	}
	entries := MapEntries(items)
	keys := make([]any, len(entries))
	for i, e := range entries {
		keys[i] = e.Key
	}
	return keys, true
}

func recordParts(r *Record, render func(any) string) []string {
	l := r.Layout()
	parts := make([]string, len(l.Fields))
	for i := range l.Fields {
		parts[i] = render(r.slot(&l.Fields[i]))
	}
	return parts
}

func namedParts(r *Record, render func(any) string) []string {
	l := r.Layout()
	parts := make([]string, len(l.Fields))
	for i := range l.Fields {
		parts[i] = l.Fields[i].Name + ": " + render(r.slot(&l.Fields[i]))
	}
	return parts
}

// FormatTuple joins rendered tuple components: `(1, "a")`.
func FormatTuple(parts []string) string { return "(" + strings.Join(parts, ", ") + ")" }

// FormatVariant is a payload-carrying variant or a distinct value around its
// rendered payload: `Some(1)`, `Id(5)`.
func FormatVariant(name, payload string) string { return name + "(" + payload + ")" }

// FormatMapPairs joins rendered key/value pairs in the order given: `{=>}` when
// empty, `{k => v, w => x}` otherwise. FormatMap is this over an rt.Map.
func FormatMapPairs(keys, vals []string) string {
	if len(keys) == 0 {
		return "{=>}"
	}
	parts := make([]string, len(keys))
	for i := range keys {
		parts[i] = keys[i] + " => " + vals[i]
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// DebugHook renders a nominal value through the Debug impl the program linked
// for its type. It answers owned=false for a value it does not own, and the
// structural rendering then applies. The engine supplies it; rt cannot call
// Nomi code.
type DebugHook func(v any) (text string, owned bool, err error)

// DebugText is the structural `Debug.inspect` of a Value, consulting hook
// first at every level. It renders what the front end's universal Debug and
// std's impls print for scalars, collections, tuples, anonymous records,
// distincts, Bool and the prelude's carrying enums, and refuses by name what
// only a dispatched impl can render: a named struct, a user enum, a Dynamic,
// an opaque value.
func DebugText(v any, hook DebugHook) (string, error) {
	if hook != nil {
		if s, owned, err := hook(v); owned || err != nil {
			return s, err
		}
	}
	switch t := v.(type) {
	case Unit:
		// std/unit.nomi declares `host type Unit` with no explicit Debug, so
		// the synthesized universal Debug prints the bare name.
		return "Unit", nil
	case Type[any]:
		// std/type.nomi declares `host type Type<T>` with no explicit Debug:
		// the same bare-name default, whatever T is.
		return "Type", nil
	case Byte:
		return InspectByte(t), nil
	case Bytes:
		return InspectBytes(t), nil
	case Decimal:
		return DecimalInspect(t), nil
	case int64:
		return FormatInt(t), nil
	case float64:
		return FormatFloat(t), nil
	case bool:
		return FormatBool(t), nil
	case string:
		return DebugString(t), nil
	case *List[any]:
		var renderErr error
		text := FormatListCells[any, List[any]](t, func(element any) string {
			if renderErr != nil {
				return ""
			}
			s, err := DebugText(element, hook)
			renderErr = err
			return s
		})
		return text, renderErr
	case Vector[any]:
		var renderErr error
		text := FormatVector(t, func(element any) string {
			if renderErr != nil {
				return ""
			}
			s, err := DebugText(element, hook)
			renderErr = err
			return s
		})
		return text, renderErr
	case Map[any, any]:
		entries := MapEntries(t)
		keys := make([]string, len(entries))
		vals := make([]string, len(entries))
		for i, e := range entries {
			k, err := DebugText(e.Key, hook)
			if err != nil {
				return "", err
			}
			v, err := DebugText(e.Val, hook)
			if err != nil {
				return "", err
			}
			keys[i], vals[i] = k, v
		}
		return FormatMapPairs(keys, vals), nil
	case *Record:
		return debugRecord(t, hook)
	case HostHandle:
		// A host type's Debug is its own `impl Debug`, which the hook owns
		// (std/regex's renders the typed literal). With none, the universal
		// default is the bare type name, as the front end synthesizes it.
		return ShortTypeName(t.TypeName), nil
	case Context:
		// A Context's Debug is the deterministic placeholder the universal
		// Debug gives every non-structural value kind.
		return ContextInspect(t), nil
	case Seq[any]:
		if t.Src != nil {
			// A view of a source is that source (see Seq).
			return DebugText(t.Src, hook)
		}
		// A lazy Iter, for the same reason, and without running it.
		return InspectSeq(t), nil
	case Closure:
		// A function held in a tuple or record part.
		return FunctionInspectText(), nil
	}
	return "", fmt.Errorf("Debug on an unrepresented receiver (%T)", v)
}

// DebugPreludeVariant reports whether the structural Debug knows this variant
// of a prelude enum by its runtime identity (`maybe.Maybe`, not `Maybe`), and
// if so whether it carries a payload: a carrying variant renders
// `Variant(payload)`, a bare one its name. Anything unknown is a user enum,
// whose Debug is its dispatched impl.
func DebugPreludeVariant(enum, variant string) (carrying, known bool) {
	carrying, known = debugCarrying[enum][variant]
	return carrying, known
}

var debugCarrying = map[string]map[string]bool{
	"maybe.Maybe":       {"Some": true, "None": false},
	"results.Result":    {"Ok": true, "Err": true},
	"literals.Fragment": {"Static": true, "Dynamic": true},
	"tasks.Outcome":     {"Completed": true, "Failed": true, "Cancelled": false},
	"tasks.Failure":     {"Panicked": true, "Errored": true},
}

func debugRecord(r *Record, hook DebugHook) (string, error) {
	switch r.Desc.Kind {
	case KindTuple:
		l := r.Layout()
		parts := make([]string, len(l.Fields))
		for i := range l.Fields {
			s, err := DebugText(r.slot(&l.Fields[i]), hook)
			if err != nil {
				return "", err
			}
			parts[i] = s
		}
		return FormatTuple(parts), nil
	case KindEnum:
		v := r.Variant()
		bare := v.Shape == VariantBare
		carrying, known := DebugPreludeVariant(r.Desc.Name, v.Name)
		if known && !carrying && bare {
			return v.Name, nil
		}
		if known && carrying && !bare {
			payload, err := DebugText(payloadValue(r), hook)
			if err != nil {
				return "", err
			}
			return FormatVariant(v.Name, payload), nil
		}
		// A Bool, and only a Bool, prints its variant's name; the engine's
		// Bool is a Go bool, so this arm serves a Bool built as a record.
		if r.Desc.Short != "Bool" || !bare {
			return "", fmt.Errorf("Debug on an unrepresented receiver (%s.%s)", r.Desc.Short, v.Name)
		}
		return v.Name, nil
	case KindDistinct:
		if r.NumFields() == 0 {
			return r.Desc.Short, nil
		}
		inner, err := DebugText(r.Field(0), hook)
		if err != nil {
			return "", err
		}
		return FormatVariant(r.Desc.Short, inner), nil
	}
	if r.Desc.Name == "sets.Set" {
		return debugSet(r, hook)
	}
	if r.Desc.Kind == KindAnon || r.Desc.Name == "" {
		// An anonymous record's field order is its sorted names, which is
		// its descriptor's layout order.
		parts, err := sortedDebugParts(r, hook)
		if err != nil {
			return "", err
		}
		return "{" + strings.Join(parts, ", ") + "}", nil
	}
	return "", fmt.Errorf("Debug on an unrepresented receiver (%s): a struct's "+
		"Debug renders its fields in DECLARATION order through its dispatched impl, "+
		"and the structural renderer prints only sorted orders", r.Desc.Short)
}

func sortedDebugParts(r *Record, hook DebugHook) ([]string, error) {
	l := r.Layout()
	idx := make([]int, len(l.Fields))
	for i := range idx {
		idx[i] = i
	}
	for i := 1; i < len(idx); i++ {
		for j := i; j > 0 && l.Fields[idx[j]].Name < l.Fields[idx[j-1]].Name; j-- {
			idx[j], idx[j-1] = idx[j-1], idx[j]
		}
	}
	parts := make([]string, len(idx))
	for i, fi := range idx {
		s, err := DebugText(r.slot(&l.Fields[fi]), hook)
		if err != nil {
			return nil, err
		}
		parts[i] = l.Fields[fi].Name + ": " + s
	}
	return parts, nil
}

// debugSet is std's Set Debug: `{a, b}` over the elements in insertion order,
// each through the structural Debug without the caller's hook, which is how
// the VM renders a set.
func debugSet(r *Record, hook DebugHook) (string, error) {
	keys, ok := setMembers(r)
	if !ok {
		raw, _ := r.FieldNamed("items")
		return "", fmt.Errorf("vm: Set.items is %T, want Map", raw)
	}
	var renderErr error
	text := FormatSetElems(keys, func(element any) string {
		if renderErr != nil {
			return ""
		}
		s, err := DebugText(element, hook)
		renderErr = err
		return s
	})
	return text, renderErr
}

// DebugString is std's `impl Debug for String`, transcribed:
//
//	Iter.map(s, |g| if g == "\\" { "\\\\" } else if g == "\"" { "\\\"" } else { g })
//	|> String.join()  and then wrapped in quotes
//
// Two escapes and no more: a newline and a tab pass through raw, as std's
// impl above does. strconv.Quote would escape both and be wrong.
//
// Over grapheme clusters, which is `Iter.map(s, …)`'s own unit: a backslash
// followed by a combining mark is one cluster, so it passes through unescaped,
// where a byte-wise replace would escape the backslash and split the
// character.
func DebugString(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	EachGraphemeCluster(s, func(cluster string) bool {
		switch cluster {
		case `\`:
			b.WriteString(`\\`)
		case `"`:
			b.WriteString(`\"`)
		default:
			b.WriteString(cluster)
		}
		return true
	})
	b.WriteByte('"')
	return b.String()
}
