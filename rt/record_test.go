package rt

import (
	"math"
	"strings"
	"testing"
)

func particleDesc() *TypeDesc {
	return NewStructDesc("engines.Particle", []FieldSpec{
		{"x", SlotInt}, {"y", SlotInt}, {"dx", SlotInt}, {"dy", SlotInt},
	})
}

func TestRecord_LayoutSplitsBanksInFieldOrder(t *testing.T) {
	d := NewStructDesc("m.Mixed", []FieldSpec{
		{"a", SlotInt}, {"s", SlotString}, {"f", SlotFloat}, {"l", SlotRef},
		{"b", SlotBool}, {"bs", SlotBytes}, {"y", SlotByte},
	})
	if d.NW != 4 || d.NS != 2 || d.NR != 1 {
		t.Fatalf("banks = %d/%d/%d, want 4/2/1", d.NW, d.NS, d.NR)
	}
	want := []int{0, 0, 1, 0, 2, 1, 3}
	for i, f := range d.Fields {
		if f.Slot != want[i] {
			t.Errorf("field %s slot %d, want %d", f.Name, f.Slot, want[i])
		}
	}
	if d.Short != "Mixed" {
		t.Errorf("Short = %q", d.Short)
	}
	r := d.Make(int64(-7), "s", math.Inf(-1), []int{1}, true, Bytes("\x00"), Byte(200))
	got := []any{r.Field(0), r.Field(1), r.Field(2), r.Field(4), r.Field(5), r.Field(6)}
	wantVals := []any{int64(-7), "s", math.Inf(-1), true, Bytes("\x00"), Byte(200)}
	for i := range got {
		if got[i] != wantVals[i] {
			t.Errorf("field %d = %#v, want %#v", i, got[i], wantVals[i])
		}
	}
	if v, ok := r.FieldNamed("l"); !ok || v.([]int)[0] != 1 {
		t.Errorf("FieldNamed(l) = %v, %v", v, ok)
	}
	if _, ok := r.FieldNamed("zz"); ok {
		t.Error("FieldNamed found a field that does not exist")
	}
}

func TestRecord_MakeRefusesAValueThatDoesNotFitItsSlot(t *testing.T) {
	defer func() {
		msg, _ := recover().(string)
		if !strings.Contains(msg, `field "x" is a Int slot and got string`) {
			t.Fatalf("panic = %q", msg)
		}
	}()
	particleDesc().Make("1", int64(2), int64(3), int64(4))
}

func TestRecord_WithCopiesAndLeavesTheReceiverUnchanged(t *testing.T) {
	d := particleDesc()
	p := d.Make(int64(1), int64(2), int64(3), int64(4))
	q := p.With([]int{0, 3}, []any{int64(10), int64(40)})
	if RowText(p) != "Particle{dx: 3, dy: 4, x: 1, y: 2}" {
		t.Errorf("receiver changed: %s", RowText(p))
	}
	if RowText(q) != "Particle{dx: 3, dy: 40, x: 10, y: 2}" {
		t.Errorf("update = %s", RowText(q))
	}
	if &p.W[0] == &q.W[0] {
		t.Error("With shares the word bank")
	}
}

func TestRecord_PayloadFreeValuesAreSharedAndAllocateNothing(t *testing.T) {
	shape := NewEnumDesc("shapes.Shape", []VariantSpec{
		{Name: "Circle", Shape: VariantPositional, Fields: []FieldSpec{{Type: SlotInt}}},
		{Name: "Dot"},
	})
	if shape.NewVariant(1) != shape.NewVariant(1) {
		t.Error("two Dots are two allocations")
	}
	if n := testing.AllocsPerRun(100, func() { _ = shape.NewVariant(1) }); n != 0 {
		t.Errorf("Shape.Dot allocates %v", n)
	}
	marker := NewDistinctDesc("state.Expired", nil)
	if marker.New() != marker.New() || marker.New().Clone() != marker.New() {
		t.Error("a marker is not a singleton")
	}
	c := shape.MakeVariant(0, int64(3))
	if RowText(c) != "Circle(3)" || RowText(shape.NewVariant(1)) != "Dot" {
		t.Errorf("rendered %s / %s", RowText(c), RowText(shape.NewVariant(1)))
	}
}

func TestEqualHash_FloatFieldsFollowEqFloatNotTheirBits(t *testing.T) {
	d := NewStructDesc("m.F", []FieldSpec{{"v", SlotFloat}})
	nan1 := math.Float64frombits(0x7ff8000000000001)
	nan2 := math.Float64frombits(0x7ff8000000000abc)
	pairs := [][2]float64{{nan1, nan2}, {0, math.Copysign(0, -1)}}
	for _, p := range pairs {
		a, b := d.Make(p[0]), d.Make(p[1])
		if !Equal(a, b) || Hash(a) != Hash(b) {
			t.Errorf("%v vs %v: Equal %v, hashes %x %x", p[0], p[1], Equal(a, b), Hash(a), Hash(b))
		}
	}
}

func TestEqualHash_IdentityIsTheShortNameAndFieldsCompareByName(t *testing.T) {
	a := NewStructDesc("shapes.Point", []FieldSpec{{"x", SlotInt}, {"y", SlotInt}}).Make(int64(1), int64(2))
	// Same short name, other qualifier, other declaration order, a boxed slot.
	b := NewStructDesc("Point", []FieldSpec{{"y", SlotRef}, {"x", SlotInt}}).Make(int64(2), int64(1))
	if !Equal(a, b) || Hash(a) != Hash(b) {
		t.Fatalf("Equal %v, hashes %x %x", Equal(a, b), Hash(a), Hash(b))
	}
	anon := AnonDesc([]FieldSpec{{"y", SlotInt}, {"x", SlotInt}}).Make(int64(1), int64(2))
	if Equal(a, anon) {
		t.Error("a named struct equals an anonymous record")
	}
	if RowText(anon) != "{x: 1, y: 2}" {
		t.Errorf("anon row = %s", RowText(anon))
	}
}

func TestEqualHash_AnEmbeddedVariantIsItsPayload(t *testing.T) {
	circle := NewStructDesc("shapes.Circle", []FieldSpec{{"r", SlotInt}}).Make(int64(2))
	shape := NewEnumDesc("shapes.Shape", []VariantSpec{
		{Name: "Circle", Shape: VariantEmbedded, Fields: []FieldSpec{{Type: SlotRef}}},
	})
	widened := shape.MakeVariant(0, circle)
	if !Equal(widened, circle) || !Equal(circle, widened) || Hash(widened) != Hash(circle) {
		t.Fatal("an embedded variant is not its payload")
	}
	if RowText(widened) != "Circle{r: 2}" {
		t.Errorf("row = %s", RowText(widened))
	}
	if _, err := DebugText(widened, nil); err == nil ||
		err.Error() != "Debug on an unrepresented receiver (Shape.Circle)" {
		t.Errorf("Debug = %v", err)
	}
}

func TestEqualHash_VariantShapesAcrossDescriptors(t *testing.T) {
	fields := NewEnumDesc("s.E", []VariantSpec{
		{Name: "Rect", Shape: VariantFields, Fields: []FieldSpec{{"w", SlotInt}, {"h", SlotInt}}},
	})
	positional := NewEnumDesc("E", []VariantSpec{
		{Name: "Rect", Shape: VariantPositional, Fields: []FieldSpec{{Type: SlotRef}}},
	})
	a := fields.MakeVariant(0, int64(1), int64(2))
	payload := NewStructDesc("Rect", []FieldSpec{{"h", SlotInt}, {"w", SlotInt}}).Make(int64(2), int64(1))
	b := positional.MakeVariant(0, payload)
	if !Equal(a, b) || Hash(a) != Hash(b) {
		t.Fatalf("Equal %v, hashes %x %x", Equal(a, b), Hash(a), Hash(b))
	}
	if RowText(a) != "Rect(Rect{h: 2, w: 1})" || RowText(a) != RowText(b) {
		t.Errorf("rows %s / %s", RowText(a), RowText(b))
	}
}

func TestEqualHash_MapKeysOverRecordsAndScalars(t *testing.T) {
	pt := NewStructDesc("shapes.Point", []FieldSpec{{"x", SlotInt}})
	m := MapOf(Hash, Equal, []MapEntry[any, any]{
		{Key: pt.Make(int64(1)), Val: "one"},
		{Key: math.NaN(), Val: "nan"},
		{Key: 0.0, Val: "zero"},
		{Key: true, Val: "t"},
	})
	if v, ok := MapLookup(m, Hash, Equal, any(NewStructDesc("Point", []FieldSpec{{"x", SlotRef}}).Make(int64(1)))); !ok || v != "one" {
		t.Errorf("record key lookup = %v, %v", v, ok)
	}
	if v, ok := MapLookup(m, Hash, Equal, any(math.Float64frombits(0x7ff8000000000abc))); !ok || v != "nan" {
		t.Errorf("NaN key lookup = %v, %v", v, ok)
	}
	if v, ok := MapLookup(m, Hash, Equal, any(math.Copysign(0, -1))); !ok || v != "zero" {
		t.Errorf("-0.0 key lookup = %v, %v", v, ok)
	}
	if _, ok := MapLookup(m, Hash, Equal, any(int64(1))); ok {
		t.Error("an Int key found a record")
	}
}

type testClosure struct{}

func (testClosure) OpaqueText() string { return "<function>" }
func (testClosure) NomiClosure()       {}

func TestEqualHash_OpaqueAndHandles(t *testing.T) {
	var f Closure = testClosure{}
	if Equal(f, f) || Hash(f) != 0 {
		t.Error("an opaque value compares or hashes")
	}
	for _, s := range []string{RowText(f), DisplayText(f)} {
		if s != "<function>" {
			t.Errorf("opaque renders %q", s)
		}
	}
	x := new(int)
	a, b := HostHandle{"regex.Regex", x}, HostHandle{"Regex", x}
	if !Equal(a, b) || Equal(a, HostHandle{"Regex", new(int)}) || Equal(a, HostHandle{"Other", x}) {
		t.Error("handle identity")
	}
	if RowText(a) != "<Regex>" {
		t.Errorf("handle row %q", RowText(a))
	}
}
