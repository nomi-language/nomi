package rt

import "testing"

func boxedListFixture() (*List[any], *Record) {
	l := Cons[any](int64(1), Cons[any](int64(2), nil))
	return l, Box(BoxDesc("List<Int>"), l)
}

// A box is its value to every kernel but dispatch and equality's type
// identity: two boxes of one static type compare and hash as the values they
// hold, and every rendering and the debugger's tree show the value. Boxes of
// two static types, or a box and a value that is none, held two types where
// they became interface values, so they are unequal (`List<Int>`'s `[]` is
// not `List<String>`'s).
func TestBox_IsItsValueEverywhereButDispatch(t *testing.T) {
	l, b := boxedListFixture()
	other := Box(BoxDesc("List<Int>"), Cons[any](int64(1), Cons[any](int64(2), nil)))
	if !Equal(b, other) || !Equal(other, b) {
		t.Fatal("boxes of one type and equal values compare unequal")
	}
	if Equal(b, Box(BoxDesc("List<Int>"), Cons[any](int64(3), nil))) {
		t.Fatal("boxes of unequal values compare equal")
	}
	if Equal(b, l) || Equal(l, b) {
		t.Fatal("a box compares equal to a value that is no box")
	}
	var empty *List[any]
	if Equal(Box(BoxDesc("List<Int>"), empty), Box(BoxDesc("List<String>"), empty)) {
		t.Fatal("boxes of two static types compare equal")
	}
	if Hash(b) != Hash(l) || Hash(b) != Hash(other) {
		t.Fatal("a box hashes apart from its value")
	}
	if !Equal(Cons[any](b, nil), Cons[any](other, nil)) {
		t.Fatal("lists of equal boxes compare unequal")
	}
	for name, got := range map[string]string{
		"RowText":     RowText(b),
		"DisplayText": DisplayText(b),
		"Inspect":     Inspect(b).Text(),
	} {
		if got != "[1, 2]" {
			t.Errorf("%s = %q, want [1, 2]", name, got)
		}
	}
	if s, err := DebugText(b, nil); err != nil || s != "[1, 2]" {
		t.Errorf("DebugText = %q, %v", s, err)
	}
	if ty := Inspect(b).Type(); ty != "List" {
		t.Errorf("Inspect type = %q, want List", ty)
	}
	nested := Cons[any](b, nil)
	if got := RowText(nested); got != "[[1, 2]]" {
		t.Errorf("a box nested in a list renders %q", got)
	}
	if kids := Inspect(nested).Children(0, 1); len(kids) != 1 || kids[0].Value.Text() != "[1, 2]" {
		t.Errorf("a nested box's child = %+v", kids)
	}
	if BoxDesc("List<Int>") != b.Desc || b.Desc.Kind != KindBoxed {
		t.Error("BoxDesc is not one descriptor per name")
	}
}

// Seeing through a box costs a value that is no box nothing: unboxing,
// comparing and hashing a plain existential (a struct, a scalar) allocate
// exactly what they did.
func TestBox_APlainExistentialAllocatesNothingExtra(t *testing.T) {
	p := particleDesc().Make(int64(1), int64(2), int64(3), int64(4))
	q := particleDesc().Make(int64(1), int64(2), int64(3), int64(4))
	var sink any
	var ok bool
	var h uint64
	for name, f := range map[string]func(){
		"Unbox(struct)": func() { sink = Unbox(p) },
		"Unbox(Int)":    func() { sink = Unbox(int64(7)) },
		"Equal(struct)": func() { ok = Equal(p, q) },
		"Hash(struct)":  func() { h = Hash(p) },
		"Boxed(struct)": func() { ok = Boxed(p) },
		"Unbox(boxed)":  func() { _, b := boxedListFixtureOnce(); sink = Unbox(b) },
		"Equal(boxed)":  func() { _, b := boxedListFixtureOnce(); ok = Equal(b, b) },
	} {
		if allocs := testing.AllocsPerRun(1000, f); allocs != 0 {
			t.Errorf("%s allocates %v times, want 0", name, allocs)
		}
	}
	_, _, _ = sink, ok, h
}

var onceList, onceBox = boxedListFixture()

func boxedListFixtureOnce() (*List[any], *Record) { return onceList, onceBox }

// boxHooks owns the static type "main.Shape": its values are equal when
// their Ints have one parity, and hash by that parity.
type boxHooks struct{}

func (boxHooks) EqualRecord(a, b *Record) (bool, bool) {
	if a.Desc.Name != "main.Shape" {
		return false, false
	}
	return Unbox(a).(int64)%2 == Unbox(b).(int64)%2, true
}

func (boxHooks) HashRecord(r *Record) (uint64, bool) {
	if r.Desc.Name != "main.Shape" {
		return 0, false
	}
	return HashInt(Unbox(r).(int64) % 2), true
}

// A box's static type answers for the values inside: the hooks are asked
// about the box, by its name, before the values are compared or hashed as
// themselves, and a box they do not own is its value.
func TestBox_KeyHooksAnswerForTheBoxedType(t *testing.T) {
	k := &Keys{Hooks: boxHooks{}}
	shape := BoxDesc("main.Shape")
	one, three, two := Box(shape, int64(1)), Box(shape, int64(3)), Box(shape, int64(2))
	if !k.Equal(one, three) || k.Equal(one, two) {
		t.Fatal("the boxed type's own equality was not used")
	}
	if k.Hash(one) != k.Hash(three) || k.Hash(one) != HashInt(1) {
		t.Fatal("the boxed type's own hash was not used")
	}
	plain := BoxDesc("List<Int>")
	if !k.Equal(Box(plain, int64(1)), Box(plain, int64(1))) || k.Equal(Box(plain, int64(1)), Box(plain, int64(3))) {
		t.Fatal("a box the hooks do not own does not compare as its value")
	}
	if k.Equal(one, Box(plain, int64(1))) {
		t.Fatal("boxes of two static types compare equal")
	}
}
