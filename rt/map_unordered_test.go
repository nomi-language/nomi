package rt

import "testing"

func TestMapStructuralOperationsDoNotMaterializeOrder(t *testing.T) {
	var a, b Map[int64, int64]
	for i := int64(0); i < 128; i++ {
		a = MapPut(a, HashInt, Eq[int64], i, i*2)
		b = MapPut(b, HashInt, Eq[int64], 127-i, (127-i)*2)
	}
	// Removal forces ordered iteration to sort. Neither operation below
	// needs that order, so allocating its slice is a performance regression.
	a = MapRemove(a, HashInt, Eq[int64], 64)
	b = MapRemove(b, HashInt, Eq[int64], 64)
	if !MapEqual(a, b, HashInt, Eq[int64], Eq[int64]) {
		t.Fatal("equal contents differ")
	}
	if MapHash(a, HashInt, HashInt) != MapHash(b, HashInt, HashInt) {
		t.Fatal("hash depends on order")
	}
	var hash uint64
	if n := testing.AllocsPerRun(100, func() { hash = MapHash(a, HashInt, HashInt) }); n != 0 {
		t.Fatalf("hash allocated %g times", n)
	}
	// Sum of i*(FNV prime + 2) over [0,128), excluding the removed 64.
	if hash != uint64(8064)*(0x100000001b3+2) {
		t.Fatal("hash fold differs from the arithmetic model")
	}
	var equal bool
	if n := testing.AllocsPerRun(100, func() { equal = MapEqual(a, b, HashInt, Eq[int64], Eq[int64]) }); n != 0 {
		t.Fatalf("equality allocated %g times", n)
	}
	if !equal {
		t.Fatal("measured equality returned false")
	}
	b = MapPut(b, HashInt, Eq[int64], 2, 99)
	if MapEqual(a, b, HashInt, Eq[int64], Eq[int64]) {
		t.Fatal("different contents compare equal")
	}
}
