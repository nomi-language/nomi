package rt

import (
	"fmt"
	"strings"
)

// Nomi's three renderings of a Value, over the runtime's representations.
//
//	RowText           the assertion report's `values:` row (DisplayText under it)
//	DisplayShapeText  a value's Display by its shape (a derived Display's)
//	DebugText         the structural Debug.inspect
//
// DisplayShapeText and DebugText are one walk of a value's shape
// (shapeText) with Display's leaves and Debug's: a String raw or quoted, a
// Decimal `1.50` or `1.50d`. The row's leaves are Debug's: a String reads as
// its literal, a Decimal as `1.50d` and a Dynamic as the value it holds
// (DecimalInspect and DynamicInspect, the machine's rendering of std's
// written `impl Debug` of each), at any depth. RowText holds that leaf
// rule, and RowTextWith and a debugger's variable (inspectText) render
// their leaves through it. The row and Debug differ only where the spec
// says they do, which internal/ir/render.go records:
//
//	a struct      Row sorted fields      Debug declaration order, as the
//	                                     literal that builds it is written
//	a host value  Row `<Handle>`         Debug `Handle`, when its type has
//	                                     no written `impl Debug`
//	a hand impl   RowText ignores it     RowTextWith and Debug honour it,
//	                                     through the hook
//
// Row recurses through RowText, Debug and Display through shapeText, and
// only shapeText and RowTextWith take a hook. The engine's row hook owns a
// hand-written `impl Debug` of a type whose values are records or host
// handles (a Regex), a std type's as a program's, and nothing else, so a
// derived or universal one never reaches a row. rt/render_test.go pins the
// differences and the agreements, and the golden files in
// testdata/expectations pin what programs print.
//
// They agree on a declared type's call form: each writes a payload-carrying
// variant or a distinct value through callText, so a payload written as a
// tuple of its parts reads as those parts, `Node(Leaf, 1, Leaf)`, in a row,
// a Display, a Debug and a debugger's variable alike.

// RowText is how a Value reads in an assertion's `values:` row: a String is
// its literal (StringLiteral), a Decimal its Debug (DecimalInspect, `1.50d`)
// and everything else its DisplayText. Nested values render by this same
// rule, so a String or a Decimal inside a List reads as at the top. It is
// the row's one leaf rule: RowTextWith's elements and a debugger's variable
// (inspectText) render their leaves through it.
func RowText(v any) string {
	v = Unbox(v)
	switch x := v.(type) {
	case string:
		return StringLiteral(x)
	case Decimal:
		return DecimalInspect(x)
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
		if s, owned, err := hookBoxFirst(x, hook); owned || err != nil {
			if err != nil && first == nil {
				first = err
			}
			return s
		}
		x = Unbox(x)
		switch x.(type) {
		case *List[any], Vector[any], Map[any, any], *Record:
			return DisplayTextWith(x, elem)
		}
		return RowText(x)
	}
	s := elem(v)
	return s, first
}

// DisplayText is the row's text of a Value (RowText) with Display's leaf at
// the top: a top-level String is itself and a top-level Decimal its digits,
// `1.50`. A struct's fields are sorted by name, and a container renders its
// elements by RowText. A value's Display by its shape is DisplayShapeText.
func DisplayText(v any) string {
	v = Unbox(v)
	switch x := v.(type) {
	case int64:
		return FormatInt(x)
	case *BigInt:
		return x.String()
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
	case Type[any]:
		// A `Type<T>` witness renders as its Debug does, `Type<TraceId>`.
		return TypeWitnessInspect(x)
	case Dynamic:
		return DynamicInspect(x)
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
	if name, ok := HostTypeName(v); ok {
		return "<" + name + ">"
	}
	return fmt.Sprintf("<%T>", v)
}

// DisplayTextWith is the row's container rule: DisplayText's shapes, with
// every nested value — a tuple's components, a record's fields, a list's,
// vector's or map's elements, a variant's payload — rendered by elem
// (RowTextWith's, which honours a hand-written `impl Debug`).
func DisplayTextWith(v any, elem func(any) string) string {
	v = Unbox(v)
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
			if p, ok := r.Field(0).(*Record); ok && p != nil && p.Desc.Kind == KindStruct && p.Desc.Short == ShortTypeName(v.Name) {
				// A struct-shaped variant held as its record, `Rect{w: 1}`,
				// reads as the same variant held as its fields does.
				return elem(p)
			}
			return callText(v.Name, r.Field(0), v.Spread, elem)
		}
		return InspectStruct(ShortTypeName(v.Name), namedParts(r, elem))

	case KindDistinct:
		if r.NumFields() == 0 {
			return r.Desc.Short
		}
		return callText(r.Desc.Short, r.Field(0), r.Desc.Spread, elem)
	}
	if r.Desc.Name == "sets.Set" {
		// A Set reads as its members, `[1, 2]`, as its Debug does, never as
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

// callText is a payload-carrying variant or a distinct value as the call
// that builds it, its payload rendered by render: `Some(1)`, `Id(5)`. A
// payload written as a tuple of its parts (spread: VariantDesc.Spread,
// TypeDesc.Spread) is those parts, `Node(Leaf, 1, Leaf)`, `Coord(3, 4)`,
// where a payload of one tuple type is the tuple, `Some((1, 2))`. Every
// rendering of a declared type's call form is this one.
func callText(name string, payload any, spread bool, render func(any) string) string {
	if t, ok := payload.(*Record); spread && ok && t != nil && t.Desc.Kind == KindTuple {
		return name + FormatTuple(recordParts(t, render))
	}
	return FormatVariant(name, render(payload))
}

// FormatMapPairs joins rendered key/value pairs in the order given: `{}`
// when empty, `{k => v, w => x}` otherwise. FormatMap is this over an rt.Map.
func FormatMapPairs(keys, vals []string) string {
	if len(keys) == 0 {
		return "{}"
	}
	parts := make([]string, len(keys))
	for i := range keys {
		parts[i] = keys[i] + " => " + vals[i]
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// DebugHook renders a nominal value through the impl the program linked for
// it, at one position of the value a rendering walks. Render answers
// owned=false for a value it does not own, and the structural rendering
// then applies. Part is the hook for a part of the value at that position
// (step), so an engine that knows the static type at each position answers
// each part by it: a value of an `embeds` enum is its embedded value at run
// time, and only the position says which of the two types' impls renders
// it. The engine supplies it; rt cannot call Nomi code.
type DebugHook interface {
	Render(v any) (text string, owned bool, err error)
	Part(step Step) DebugHook
}

// HookFunc is a DebugHook that answers every position alike.
type HookFunc func(v any) (text string, owned bool, err error)

// Render implements DebugHook.
func (f HookFunc) Render(v any) (string, bool, error) { return f(v) }

// Part implements DebugHook: every part is answered by f too.
func (f HookFunc) Part(Step) DebugHook { return f }

// Step is how a rendering by shape descends from a value to one of its
// parts: an element of a List, a Vector or a Set, a Map's key or value, a
// tuple's slot (Index), a struct's or an anonymous record's field (Name), a
// variant's payload (Name the variant, Field one of its fields or "" for
// its one payload), a distinct's inner value, a view's source.
type Step struct {
	Kind  StepKind
	Name  string
	Field string
	Index int
}

// StepKind is the kind of a Step.
type StepKind uint8

const (
	StepElem StepKind = iota + 1
	StepKey
	StepValue
	StepSlot
	StepField
	StepPayload
	StepInner
	StepSource
)

// hookBoxFirst asks hook to render v. A box is offered as it is first, so
// an impl recorded under the box's name (an `embeds` enum's own Debug, for
// a value widened into one of its variants) renders it; then the value it
// holds is offered.
func hookBoxFirst(v any, hook DebugHook) (string, bool, error) {
	if Boxed(v) {
		if s, owned, err := hook.Render(v); owned || err != nil {
			return s, owned, err
		}
	}
	return hook.Render(Unbox(v))
}

// DebugText is the structural `Debug.inspect` of a Value, consulting hook
// first at every level. It renders a value as the literal that builds it,
// as the spec's universal Debug says (§12 *Display and Debug*): scalars,
// collections, tuples, anonymous records, a named struct with its fields in
// declaration order, an enum's variant by its shape (an `embeds` variant as
// the value it holds), a distinct, and a payload written as a tuple of its
// parts as those parts (VariantDesc.Spread, TypeDesc.Spread), and the
// placeholder of an opaque value (a task, a channel half). An opaque type's
// `<opaque T>` is its synthesized impl's, which the hook owns.
func DebugText(v any, hook DebugHook) (string, error) {
	return shapeText(v, hook, false)
}

// DisplayShapeText is a value's Display by its shape, consulting hook first
// at every level: what a derived Display renders (spec §12 *Derive*). It is
// DebugText's shape with Display's leaves: a String is its raw text and a
// Decimal its digits (`1.50`, where Debug writes `1.50d`). A part whose
// type has a written Display renders through it, which the hook owns, as a
// Bytes value's and a host value's do.
func DisplayShapeText(v any, hook DebugHook) (string, error) {
	return shapeText(v, hook, true)
}

// shapeText renders v as the literal that builds it, each leaf by Debug,
// or by Display when display is set.
func shapeText(v any, hook DebugHook, display bool) (string, error) {
	if hook != nil {
		if s, owned, err := hookBoxFirst(v, hook); owned || err != nil {
			return s, err
		}
	}
	v = Unbox(v)
	// part renders x, which sits at the position steps below v's.
	part := func(x any, steps ...Step) (string, error) {
		h := hook
		for _, s := range steps {
			if h != nil {
				h = h.Part(s)
			}
		}
		return shapeText(x, h, display)
	}
	elem := func(x any) (string, error) { return part(x, Step{Kind: StepElem}) }
	switch t := v.(type) {
	case Unit:
		// std/unit.nomi declares `host type Unit` with no explicit Debug, so
		// the synthesized universal Debug prints the bare name.
		return "Unit", nil
	case Type[any]:
		// A witness renders as the type it names, `Type<TraceId>`.
		return TypeWitnessInspect(t), nil
	case Byte:
		return InspectByte(t), nil
	case Bytes:
		return InspectBytes(t), nil
	case Decimal:
		if display {
			return t.Display(), nil
		}
		return DecimalInspect(t), nil
	case int64:
		return FormatInt(t), nil
	case *BigInt:
		return t.String(), nil
	case float64:
		return FormatFloat(t), nil
	case bool:
		return FormatBool(t), nil
	case string:
		if display {
			return t, nil
		}
		return StringLiteral(t), nil
	case *List[any]:
		var renderErr error
		text := FormatListCells[any, List[any]](t, func(element any) string {
			if renderErr != nil {
				return ""
			}
			s, err := elem(element)
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
			s, err := elem(element)
			renderErr = err
			return s
		})
		return text, renderErr
	case Map[any, any]:
		entries := MapEntries(t)
		keys := make([]string, len(entries))
		vals := make([]string, len(entries))
		for i, e := range entries {
			k, err := part(e.Key, Step{Kind: StepKey})
			if err != nil {
				return "", err
			}
			v, err := part(e.Val, Step{Kind: StepValue})
			if err != nil {
				return "", err
			}
			keys[i], vals[i] = k, v
		}
		return FormatMapPairs(keys, vals), nil
	case *Record:
		return shapeRecord(t, part)
	case HostHandle:
		// A host type's Debug is its own `impl Debug`, which the hook owns
		// (std/regex's renders the typed literal). With none, the universal
		// default is the bare type name, as the front end synthesizes it,
		// and so is a host type's derived Display.
		return ShortTypeName(t.TypeName), nil
	case Dynamic:
		// std/dynamic's `impl Debug for Dynamic` is this host function; a
		// Dynamic inside a tuple, record or container renders through it
		// as a top-level one does.
		return DynamicInspect(t), nil
	case Context:
		// A Context's Debug is the deterministic placeholder the universal
		// Debug gives every non-structural value kind.
		return ContextInspect(t), nil
	case Seq[any]:
		if t.Src != nil {
			// A view of a source is that source (see Seq).
			return part(t.Src, Step{Kind: StepSource})
		}
		// A lazy Iter, for the same reason, and without running it.
		return InspectSeq(t), nil
	case Closure:
		// A function held in a tuple or record part.
		return FunctionInspectText(), nil
	case Opaque:
		// A value kind with no declaration to render: a task (`<task>`), a
		// channel half (`<channel>`), a Supervisor. Its placeholder is the
		// one its values: row prints.
		return t.OpaqueText(), nil
	}
	if name, ok := HostTypeName(v); ok {
		// A std host type's value with no `impl Debug`: the bare name.
		return name, nil
	}
	return "", fmt.Errorf("Debug on an unrepresented receiver (%T)", v)
}

// shapeRecord renders a record as the literal that builds it, each part by
// part, at its step below the record.
func shapeRecord(r *Record, part func(any, ...Step) (string, error)) (string, error) {
	switch r.Desc.Kind {
	case KindTuple:
		l := r.Layout()
		parts := make([]string, len(l.Fields))
		for i := range l.Fields {
			s, err := part(r.slot(&l.Fields[i]), Step{Kind: StepSlot, Index: i})
			if err != nil {
				return "", err
			}
			parts[i] = s
		}
		return FormatTuple(parts), nil
	case KindEnum:
		v := r.Variant()
		payload := Step{Kind: StepPayload, Name: v.Name}
		switch v.Shape {
		case VariantBare:
			return v.Name, nil
		case VariantEmbedded:
			return part(r.Field(0), payload)
		case VariantFields:
			parts, err := declaredParts(r, &v.Layout, func(x any, field string) (string, error) {
				return part(x, Step{Kind: StepPayload, Name: v.Name, Field: field})
			})
			if err != nil {
				return "", err
			}
			return StructOpen(ShortTypeName(v.Name)) + strings.Join(parts, ", ") + "}", nil
		}
		// A positional variant is the call that builds it, a payload of a
		// struct named as the variant among them: `Click(Click{x: 1})`.
		return shapeCall(v.Name, r.Field(0), v.Spread, payload, part)
	case KindDistinct:
		if r.NumFields() == 0 {
			return r.Desc.Short, nil
		}
		return shapeCall(r.Desc.Short, r.Field(0), r.Desc.Spread, Step{Kind: StepInner}, part)
	}
	if r.Desc.Name == "sets.Set" {
		return shapeSet(r, part)
	}
	if r.Desc.Kind == KindAnon || r.Desc.Name == "" {
		// An anonymous record's field order is its sorted names, which is
		// its descriptor's layout order.
		parts, err := sortedParts(r, part)
		if err != nil {
			return "", err
		}
		return "{" + strings.Join(parts, ", ") + "}", nil
	}
	// A named struct reads as the literal that builds it, `Point{x: 1, y:
	// 2}`, its fields in declaration order, which is its layout's.
	parts, err := declaredParts(r, r.Layout(), func(x any, field string) (string, error) {
		return part(x, Step{Kind: StepField, Name: field})
	})
	if err != nil {
		return "", err
	}
	return StructOpen(r.Desc.Short) + strings.Join(parts, ", ") + "}", nil
}

// shapeCall is callText with each part rendered by part at its step below
// the call's value: the payload at step, and a payload written as a tuple
// of its parts each at its slot below that, stopping at the first error.
func shapeCall(name string, payload any, spread bool, step Step, part func(any, ...Step) (string, error)) (string, error) {
	if t, ok := payload.(*Record); spread && ok && t != nil && t.Desc.Kind == KindTuple {
		l := t.Layout()
		parts := make([]string, len(l.Fields))
		for i := range l.Fields {
			s, err := part(t.slot(&l.Fields[i]), step, Step{Kind: StepSlot, Index: i})
			if err != nil {
				return "", err
			}
			parts[i] = s
		}
		return name + FormatTuple(parts), nil
	}
	s, err := part(payload, step)
	if err != nil {
		return "", err
	}
	return FormatVariant(name, s), nil
}

// declaredParts renders the fields l lays out in r, `name: value`, in l's
// order.
func declaredParts(r *Record, l *Layout, part func(x any, field string) (string, error)) ([]string, error) {
	parts := make([]string, len(l.Fields))
	for i := range l.Fields {
		s, err := part(r.slot(&l.Fields[i]), l.Fields[i].Name)
		if err != nil {
			return nil, err
		}
		parts[i] = l.Fields[i].Name + ": " + s
	}
	return parts, nil
}

func sortedParts(r *Record, part func(any, ...Step) (string, error)) ([]string, error) {
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
		s, err := part(r.slot(&l.Fields[fi]), Step{Kind: StepField, Name: l.Fields[fi].Name})
		if err != nil {
			return nil, err
		}
		parts[i] = l.Fields[fi].Name + ": " + s
	}
	return parts, nil
}

// shapeSet is std's Set rendering, `Set[a, b]` over the members in
// insertion order, each by part.
func shapeSet(r *Record, part func(any, ...Step) (string, error)) (string, error) {
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
		s, err := part(element, Step{Kind: StepElem})
		renderErr = err
		return s
	})
	return text, renderErr
}
