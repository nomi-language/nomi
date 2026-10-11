package rt

import (
	"strconv"
)

// Inspect is a value as a debugger's variable tree shows it: a one-line text,
// a type name, and children read on demand, a page at a time. It is built
// from the value's representation and its TypeDesc alone and runs no Nomi
// code, so it can render a value whose type has no Debug impl the program
// can reach, and it cannot change what the program does.
//
// Children: a struct's or record's fields in declaration order, a tuple's
// components (`0`, `1`), a variant's payload (its fields by name, or `0` for
// a positional one), a distinct's inner value (`0`), a List's or Vector's
// elements (`[0]`, `[1]`), a Set's members (`[0]`), a Map's entries (by the
// key's text when the key is a scalar, otherwise `[i]` holding `key` and
// `value`). A lazy Iter has none: a debugger never runs one.
type Inspected struct {
	value any
	// entry marks a Map entry whose key is not a scalar: its children are
	// the key and the value.
	entry *MapEntry[any, any]
}

// Inspect wraps v for a debugger.
func Inspect(v any) Inspected { return Inspected{value: v} }

// InspectChild is one child: its name in the tree and the child itself.
type InspectChild struct {
	Name  string
	Value Inspected
}

// inspectTextBudget is how many elements a value's one-line text may hold,
// counting nested ones, before the text summarizes instead. A debugger
// renders every variable it lists, and a million-element List must not cost
// a million renderings to show its first page.
const inspectTextBudget = 200

// Value is the value inspected.
func (n Inspected) Value() any {
	if n.entry != nil {
		return *n.entry
	}
	return n.value
}

// Type is the value's runtime type name: `Int`, `String`, `List`, `Map`, a
// declared type's short name (`Point`), `Tuple`, `Record`, or `Entry` for a
// Map entry.
func (n Inspected) Type() string {
	if n.entry != nil {
		return "Entry"
	}
	switch x := n.value.(type) {
	case int64:
		return "Int"
	case float64:
		return "Float"
	case bool:
		return "Bool"
	case string:
		return "String"
	case Decimal:
		return "Decimal"
	case Byte:
		return "Byte"
	case Bytes:
		return "Bytes"
	case Unit:
		return "Unit"
	case Dynamic:
		return "Dynamic"
	case *List[any]:
		return "List"
	case Vector[any]:
		return "Vector"
	case Map[any, any]:
		return "Map"
	case Seq[any]:
		if x.Src != nil {
			return Inspect(x.Src).Type()
		}
		return "Iter"
	case HostHandle:
		return ShortTypeName(x.TypeName)
	case *Record:
		if x == nil {
			return "Unit"
		}
		switch x.Desc.Kind {
		case KindTuple:
			return "Tuple"
		case KindAnon:
			return "Record"
		}
		return x.Desc.Short
	case Closure:
		return "Function"
	}
	return "Value"
}

// Text is the value's one-line rendering: its `values:` row text, which
// quotes a String and renders a struct's fields structurally (a variant with
// named fields as `Circle{radius: 5}`), or for a value past
// inspectTextBudget elements, its type and size.
func (n Inspected) Text() string {
	if n.entry != nil {
		return inspectText(n.entry.Key) + ": " + Inspect(n.entry.Val).Text()
	}
	budget := inspectTextBudget
	if !withinBudget(n.value, &budget) {
		count := n.NumChildren()
		return n.Type() + " (" + strconv.Itoa(count) + " " + plural(count, "item") + ")"
	}
	return inspectText(n.value)
}

// inspectText is RowText with a variant that has named fields written as its
// Debug writes it, `Circle{radius: 5}`, at any depth: the row text names the
// variant twice.
func inspectText(v any) string {
	switch x := v.(type) {
	case string:
		return InspectString(x)
	case *Record:
		if x != nil && x.Desc.Kind == KindEnum && x.Variant().Shape == VariantFields {
			return InspectStruct(ShortTypeName(x.Variant().Name), namedParts(x, inspectText))
		}
		return DisplayTextWith(x, inspectText)
	case *List[any], Vector[any], Map[any, any]:
		return DisplayTextWith(x, inspectText)
	}
	return DisplayText(v)
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// withinBudget spends one unit of budget per element and field reachable from
// v, and reports whether it lasted.
func withinBudget(v any, budget *int) bool {
	*budget--
	if *budget < 0 {
		return false
	}
	n := Inspect(v)
	count := n.NumChildren()
	if count == 0 {
		return true
	}
	if count > *budget {
		return false
	}
	for _, c := range n.Children(0, count) {
		if !withinBudget(c.Value.Value(), budget) {
			return false
		}
	}
	return true
}

// NumChildren is how many children the value has.
func (n Inspected) NumChildren() int {
	if n.entry != nil {
		return 2
	}
	switch x := n.value.(type) {
	case *List[any]:
		if x == nil {
			return 0
		}
		return x.Len
	case Vector[any]:
		return x.Len
	case Map[any, any]:
		return x.n
	case Seq[any]:
		if x.Src != nil {
			return Inspect(x.Src).NumChildren()
		}
	case *Record:
		if x == nil {
			return 0
		}
		if members, isSet := setMembers(x); isSet && x.Desc.Name == "sets.Set" {
			return len(members)
		}
		if x.Desc.Kind == KindEnum {
			v := x.Variant()
			if v.Shape == VariantFields {
				return len(v.Fields)
			}
			if v.Shape == VariantBare {
				return 0
			}
			return 1
		}
		return x.NumFields()
	}
	return 0
}

// Children are count children from start, fewer at the end.
func (n Inspected) Children(start, count int) []InspectChild {
	total := n.NumChildren()
	if start < 0 {
		start = 0
	}
	if start >= total || count <= 0 {
		return nil
	}
	end := min(total, start+count)
	out := make([]InspectChild, 0, end-start)
	index := func(i int) string { return "[" + strconv.Itoa(i) + "]" }
	if n.entry != nil {
		kids := []InspectChild{{"key", Inspect(n.entry.Key)}, {"value", Inspect(n.entry.Val)}}
		return kids[start:end]
	}
	switch x := n.value.(type) {
	case *List[any]:
		i := 0
		for c := x; c != nil && i < end; c, i = c.Tail, i+1 {
			if i >= start {
				out = append(out, InspectChild{index(i), Inspect(c.Head)})
			}
		}
	case Vector[any]:
		for i := start; i < end; i++ {
			out = append(out, InspectChild{index(i), Inspect(x.Items[x.Start+i])})
		}
	case Map[any, any]:
		entries := MapEntries(x)
		for i := start; i < end; i++ {
			e := entries[i]
			if scalarKey(e.Key) {
				out = append(out, InspectChild{RowText(e.Key), Inspect(e.Val)})
				continue
			}
			out = append(out, InspectChild{index(i), Inspected{entry: &e}})
		}
	case Seq[any]:
		return Inspect(x.Src).Children(start, count)
	case *Record:
		if members, isSet := setMembers(x); isSet && x.Desc.Name == "sets.Set" {
			for i := start; i < end; i++ {
				out = append(out, InspectChild{index(i), Inspect(members[i])})
			}
			return out
		}
		if x.Desc.Kind == KindEnum {
			v := x.Variant()
			if v.Shape != VariantFields {
				return []InspectChild{{"0", Inspect(x.Field(0))}}
			}
			for i := start; i < end; i++ {
				out = append(out, InspectChild{v.Fields[i].Name, Inspect(x.slot(&v.Fields[i]))})
			}
			return out
		}
		l := x.Layout()
		for i := start; i < end; i++ {
			name := l.Fields[i].Name
			if x.Desc.Kind == KindTuple || x.Desc.Kind == KindDistinct || name == "" {
				name = strconv.Itoa(i)
			}
			out = append(out, InspectChild{name, Inspect(x.slot(&l.Fields[i]))})
		}
	}
	return out
}

// scalarKey reports whether a Map key reads well as a child's name.
func scalarKey(k any) bool {
	switch k.(type) {
	case int64, float64, bool, string, Decimal, Byte:
		return true
	}
	return false
}
