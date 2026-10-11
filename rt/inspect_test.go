package rt

import (
	"strconv"
	"strings"
	"testing"
)

func childNames(cs []InspectChild) string {
	names := make([]string, len(cs))
	for i, c := range cs {
		names[i] = c.Name + "=" + c.Value.Text()
	}
	return strings.Join(names, " ")
}

func TestInspect_AStructShowsItsFieldsInDeclarationOrderWithNoDebugImpl(t *testing.T) {
	d := NewStructDesc("geo.Point", []FieldSpec{{"y", SlotInt}, {"x", SlotInt}, {"label", SlotString}})
	n := Inspect(d.Make(int64(2), int64(1), "origin"))
	if n.Type() != "Point" || n.NumChildren() != 3 {
		t.Fatalf("type %s with %d children", n.Type(), n.NumChildren())
	}
	if got := childNames(n.Children(0, 10)); got != `y=2 x=1 label="origin"` {
		t.Fatalf("children: %s", got)
	}
}

func TestInspect_VariantsTuplesAndDistincts(t *testing.T) {
	shape := NewEnumDesc("shapes.Shape", []VariantSpec{
		{Name: "Circle", Shape: VariantPositional, Fields: []FieldSpec{{Type: SlotInt}}},
		{Name: "Rect", Shape: VariantFields, Fields: []FieldSpec{{"w", SlotInt}, {"h", SlotInt}}},
		{Name: "Dot"},
	})
	if got := childNames(Inspect(shape.MakeVariant(0, int64(3))).Children(0, 5)); got != "0=3" {
		t.Errorf("Circle(3): %s", got)
	}
	rect := shape.MakeVariant(1, int64(4), int64(5))
	if got := childNames(Inspect(rect).Children(0, 5)); got != "w=4 h=5" {
		t.Errorf("Rect: %s", got)
	}
	if got := Inspect(rect).Text(); got != "Rect{h: 5, w: 4}" {
		t.Errorf("Rect's text: %s", got)
	}
	if got := Inspect(Cons[any](rect, nil)).Text(); got != "[Rect{h: 5, w: 4}]" {
		t.Errorf("a list of Rect's text: %s", got)
	}
	if n := Inspect(shape.NewVariant(2)); n.NumChildren() != 0 || n.Text() != "Dot" {
		t.Errorf("Dot: %d children, %s", n.NumChildren(), n.Text())
	}
	pair := TupleDesc(SlotInt, SlotString).Make(int64(1), "a")
	if got := childNames(Inspect(pair).Children(0, 5)); got != `0=1 1="a"` {
		t.Errorf("tuple: %s", got)
	}
}

func TestInspect_ALargeListPagesAndSummarizes(t *testing.T) {
	var xs *List[any]
	for i := 999; i >= 0; i-- {
		xs = Cons[any](int64(i), xs)
	}
	n := Inspect(xs)
	if n.NumChildren() != 1000 {
		t.Fatalf("%d children", n.NumChildren())
	}
	page := n.Children(500, 3)
	if got := childNames(page); got != "[500]=500 [501]=501 [502]=502" {
		t.Fatalf("page: %s", got)
	}
	if n.Text() != "List (1000 items)" {
		t.Fatalf("a list past the budget reads %q", n.Text())
	}
	small := Cons[any](int64(1), Cons[any](int64(2), nil))
	if Inspect(small).Text() != "[1, 2]" {
		t.Fatalf("a small list reads %q", Inspect(small).Text())
	}
	if got := Inspect(small).Children(5, 3); got != nil {
		t.Fatalf("a page past the end: %v", got)
	}
}

func TestInspect_MapEntries(t *testing.T) {
	var m Map[any, any]
	m = MapPut(m, Hash, Equal, any("b"), any(int64(2)))
	m = MapPut(m, Hash, Equal, any("a"), any(int64(1)))
	if got := childNames(Inspect(m).Children(0, 5)); got != `"b"=2 "a"=1` {
		t.Errorf("string keys: %s", got)
	}
	key := TupleDesc(SlotInt, SlotInt).Make(int64(1), int64(2))
	var byPair Map[any, any]
	byPair = MapPut(byPair, Hash, Equal, any(key), any("x"))
	entry := Inspect(byPair).Children(0, 1)[0]
	if entry.Name != "[0]" || childNames(entry.Value.Children(0, 2)) != `key=(1, 2) value="x"` {
		t.Errorf("a tuple key: %s %s", entry.Name, childNames(entry.Value.Children(0, 2)))
	}
}

func TestInspect_VectorPages(t *testing.T) {
	elems := make([]any, 10)
	for i := range elems {
		elems[i] = strconv.Itoa(i)
	}
	v := VectorTail(VectorOf(elems))
	if got := childNames(Inspect(v).Children(0, 2)); got != `[0]="1" [1]="2"` {
		t.Errorf("vector: %s", got)
	}
}
