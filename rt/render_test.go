package rt

import (
	"strings"
	"testing"
)

// The six measured Row/Debug disagreements internal/ir/render.go records,
// pinned on rt's renderers. Each must keep disagreeing exactly as written.
func TestRender_TheSixRowDebugDisagreements(t *testing.T) {
	dec, err := ParseDecimal("1.50")
	if err != nil {
		t.Fatal(err)
	}
	point := NewStructDesc("shapes.Point", []FieldSpec{{"y", SlotInt}, {"x", SlotInt}}).Make(int64(2), int64(1))
	record := AnonDesc([]FieldSpec{{"s", SlotString}, {"d", SlotRef}}).Make(dec, `a"b`)

	type want struct{ row, display, debug, debugErr string }
	cases := []struct {
		name string
		v    any
		hook DebugHook
		want want
	}{
		{"Decimal", dec, nil, want{row: "1.50", display: "1.50", debug: "1.50d"}},
		{"Dynamic", Dynamic{Inner: "hello"}, nil, want{row: "<dynamic: string>", display: "<dynamic: string>",
			debugErr: "Debug on an unrepresented receiver (rt.Dynamic)"}},
		{"String", `f"\.txt`, nil, want{row: `"f"\.txt"`, display: `f"\.txt`, debug: `"f\"\\.txt"`}},
		{"struct", point, nil, want{row: "Point{x: 1, y: 2}", display: "Point{x: 1, y: 2}",
			debugErr: "Debug on an unrepresented receiver (Point)"}},
		{"record", record, nil, want{row: `{d: 1.50, s: "a"b"}`, display: `{d: 1.50, s: "a"b"}`,
			debug: `{d: 1.50d, s: "a\"b"}`}},
		{"hand impl", NewDistinctDesc("ids.Id", ptr(SlotInt)).Make(int64(5)),
			func(v any) (string, bool, error) {
				if r, ok := v.(*Record); ok && r.Desc.Name == "ids.Id" {
					return "#5", true, nil
				}
				return "", false, nil
			}, want{row: "Id(5)", display: "Id(5)", debug: "#5"}},
	}
	for _, c := range cases {
		if got := RowText(c.v); got != c.want.row {
			t.Errorf("%s: Row %q, want %q", c.name, got, c.want.row)
		}
		if got := DisplayText(c.v); got != c.want.display {
			t.Errorf("%s: Display %q, want %q", c.name, got, c.want.display)
		}
		got, err := DebugText(c.v, c.hook)
		if c.want.debugErr != "" {
			if err == nil || !strings.HasPrefix(err.Error(), c.want.debugErr) {
				t.Errorf("%s: Debug %q, %v; want error %q", c.name, got, err, c.want.debugErr)
			}
			continue
		}
		if err != nil || got != c.want.debug {
			t.Errorf("%s: Debug %q, %v; want %q", c.name, got, err, c.want.debug)
		}
	}
}

func ptr[T any](v T) *T { return &v }

// A host handle's Debug is the impl the hook owns for its TypeName, at the top
// and nested; with no impl it is the bare type name, which is what the front
// end's synthesized host-type Debug renders.
func TestRender_HostHandleDebug(t *testing.T) {
	re := HostHandle{TypeName: "regex.Regex", Value: new(int)}
	handle := HostHandle{TypeName: "shapes.Handle", Value: new(int)}
	hook := func(v any) (string, bool, error) {
		if h, ok := v.(HostHandle); ok && h.TypeName == "regex.Regex" {
			return "Regex`a+`", true, nil
		}
		return "", false, nil
	}
	list := ConsCell[any, List[any]](re, ConsCell[any, List[any]](handle, nil))
	for _, c := range []struct {
		v    any
		hook DebugHook
		want string
	}{
		{re, hook, "Regex`a+`"},
		{re, nil, "Regex"},
		{handle, nil, "Handle"},
		{list, hook, "[Regex`a+`, Handle]"},
	} {
		got, err := DebugText(c.v, c.hook)
		if err != nil || got != c.want {
			t.Errorf("DebugText %q, %v; want %q", got, err, c.want)
		}
	}
}

// RowTextWith renders a value the hook owns through the hook at every depth,
// and everything else exactly as RowText does: nested Strings quoted, fields
// sorted.
func TestRender_RowTextWithHonoursTheHookAtEveryDepth(t *testing.T) {
	id := NewDistinctDesc("ids.Id", ptr(SlotInt)).Make(int64(5))
	hook := func(v any) (string, bool, error) {
		if r, ok := v.(*Record); ok && r.Desc.Name == "ids.Id" {
			return "#5", true, nil
		}
		return "", false, nil
	}
	point := NewStructDesc("shapes.Point", []FieldSpec{{"y", SlotRef}, {"x", SlotString}}).Make(id, "a")
	list := ConsCell[any, List[any]](id, ConsCell[any, List[any]](point, nil))
	for _, c := range []struct {
		v    any
		want string
	}{
		{id, "#5"},
		{point, `Point{x: "a", y: #5}`},
		{list, `[#5, Point{x: "a", y: #5}]`},
		{"s", `"s"`},
	} {
		got, err := RowTextWith(c.v, hook)
		if err != nil || got != c.want {
			t.Errorf("RowTextWith %q, %v; want %q", got, err, c.want)
		}
		if plain, _ := RowTextWith(c.v, nil); plain != RowText(c.v) {
			t.Errorf("RowTextWith(nil) %q, RowText %q", plain, RowText(c.v))
		}
	}
}

// A lazy Iter renders as `<iter>` in every rendering, nested or not, and
// rendering it does not run it.
func TestRender_AnIterIsAPlaceholderAndIsNotRun(t *testing.T) {
	ran := 0
	var seq Seq[any]
	seq.Run = func(fr *Frame, yield func(*Frame, any) bool) bool {
		ran++
		return yield(fr, int64(1))
	}
	if got := RowText(seq); got != "<iter>" {
		t.Errorf("row %s", got)
	}
	if got, err := DebugText(seq, nil); err != nil || got != "<iter>" {
		t.Errorf("debug %s %v", got, err)
	}
	list := ConsCell[any, List[any]](seq, nil)
	if got := RowText(list); got != "[<iter>]" {
		t.Errorf("list row %s", got)
	}
	if got, err := DebugText(list, nil); err != nil || got != "[<iter>]" {
		t.Errorf("list debug %s %v", got, err)
	}
	tuple := TupleDesc(SlotRef, SlotInt).Make(seq, int64(1))
	if got := RowText(tuple); got != "(<iter>, 1)" {
		t.Errorf("tuple row %s", got)
	}
	if got, err := DebugText(tuple, nil); err != nil || got != "(<iter>, 1)" {
		t.Errorf("tuple debug %s %v", got, err)
	}
	if ran != 0 {
		t.Errorf("rendering ran the sequence %d times", ran)
	}
}

// A view of a source renders as the source and answers the source's
// known_count; a pipeline's sequence declines both.
func TestRender_AViewIsItsSource(t *testing.T) {
	src := ConsCell[any, List[any]](int64(1), ConsCell[any, List[any]](int64(2), nil))
	view := ListCellSeq[any](src)
	view.Src = src
	view.Count = func(*Frame) Maybe[int64] { return ListCellKnownCount[any](src) }
	if got := RowText(view); got != "[1, 2]" {
		t.Errorf("row %s", got)
	}
	if got, err := DebugText(view, nil); err != nil || got != "[1, 2]" {
		t.Errorf("debug %s %v", got, err)
	}
	if got := SeqKnownCount(nil, view); got.Tag != TagSome || got.Some != 2 {
		t.Errorf("known_count of the view %+v", got)
	}
	mapped := SeqMap(view, func(_ *Frame, x any) any { return x })
	if got := SeqKnownCount(nil, mapped); got.Tag != TagNone {
		t.Errorf("known_count of a pipeline %+v", got)
	}
	if got, err := DebugText(mapped, nil); err != nil || got != "<iter>" {
		t.Errorf("pipeline debug %s %v", got, err)
	}
}

func TestRender_PreludeEnumsCollectionsAndSets(t *testing.T) {
	maybe := NewEnumDesc("maybe.Maybe", []VariantSpec{
		{Name: "Some", Shape: VariantPositional, Fields: []FieldSpec{{Type: SlotRef}}},
		{Name: "None"},
	})
	list := ConsCell[any, List[any]](maybe.MakeVariant(0, "x"), ConsCell[any, List[any]](maybe.NewVariant(1), nil))
	if got := RowText(list); got != `[Some("x"), None]` {
		t.Errorf("row %s", got)
	}
	if got, err := DebugText(list, nil); err != nil || got != `[Some("x"), None]` {
		t.Errorf("debug %s %v", got, err)
	}
	items := MapOf(Hash, Equal, []MapEntry[any, any]{{Key: int64(2), Val: true}, {Key: int64(1), Val: true}})
	set := NewStructDesc("sets.Set", []FieldSpec{{"items", SlotRef}}).Make(items)
	if got := RowText(set); got != "#{2, 1}" {
		t.Errorf("set row %s", got)
	}
	if got, err := DebugText(set, nil); err != nil || got != "#{2, 1}" {
		t.Errorf("set debug %s %v", got, err)
	}
	// Nested, and with String members, which the row quotes without escaping.
	strs := NewStructDesc("sets.Set", []FieldSpec{{"items", SlotRef}}).Make(
		MapOf(Hash, Equal, []MapEntry[any, any]{{Key: `a"b`, Val: true}}))
	holder := NewStructDesc("m.Holder", []FieldSpec{{"s", SlotRef}}).Make(strs)
	if got := RowText(holder); got != `Holder{s: #{"a"b"}}` {
		t.Errorf("nested set row %s", got)
	}
	if got := RowText(ConsCell[any, List[any]](set, nil)); got != "[#{2, 1}]" {
		t.Errorf("set in list row %s", got)
	}
	tuple := TupleDesc(SlotInt, SlotString, SlotRef).Make(int64(1), `\`, Unit{})
	if RowText(tuple) != `(1, "\", Unit)` {
		t.Errorf("tuple row %s", RowText(tuple))
	}
	if got, _ := DebugText(tuple, nil); got != `(1, "\\", Unit)` {
		t.Errorf("tuple debug %s", got)
	}
	if got := RowText(Map[any, any]{}); got != "{=>}" {
		t.Errorf("empty map %s", got)
	}
}

func TestDebugString_EscapesTwoCharactersOverGraphemes(t *testing.T) {
	cases := map[string]string{
		"a\tb\n":   "\"a\tb\n\"",
		`\`:        `"\\"`,
		"\\́":      "\"\\́\"", // a backslash with a combining mark is one cluster
		`say "hi"`: `"say \"hi\""`,
		"":         `""`,
	}
	for in, want := range cases {
		if got := DebugString(in); got != want {
			t.Errorf("DebugString(%q) = %q, want %q", in, got, want)
		}
	}
}

// A Set's members render through the same hook as a List's: a user enum
// member has no structural Debug, so without the hook it is refused.
func TestRender_SetMembersRenderThroughTheHook(t *testing.T) {
	color := NewEnumDesc("shapes.Color", []VariantSpec{{Name: "Red"}, {Name: "Blue"}})
	items := MapOf(Hash, Equal, []MapEntry[any, any]{{Key: color.NewVariant(0), Val: true}})
	set := NewStructDesc("sets.Set", []FieldSpec{{"items", SlotRef}}).Make(items)
	hook := func(v any) (string, bool, error) {
		if r, ok := v.(*Record); ok && r.Desc == color {
			return "<" + r.Variant().Name + ">", true, nil
		}
		return "", false, nil
	}
	if got, err := DebugText(set, hook); err != nil || got != "#{<Red>}" {
		t.Errorf("set debug %q %v", got, err)
	}
}
