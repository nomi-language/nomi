package rt_test

import (
	"math"
	"testing"

	"github.com/nomi-language/nomi/rt"
)

// TestConsSharesItsTail is the property the cons cell was chosen for, so it is
// asserted rather than assumed: prepending must be O(1) and must not copy, or
// the representation has no advantage over a slice and the reasoning in list.go
// is wrong.
//
// Identity, not equality: `xs.Tail` must be the very pointer that was passed in.
func TestConsSharesItsTail(t *testing.T) {
	tail := rt.Cons(int64(2), rt.Cons(int64(3), nil))
	xs := rt.Cons(int64(1), tail)
	if xs.Tail != tail {
		t.Fatal("Cons copied its tail instead of sharing it")
	}
	if xs.Len != 3 || tail.Len != 2 {
		t.Fatalf("Len = %d / %d, want 3 / 2", xs.Len, tail.Len)
	}
	// The shared tail is still its own value, so the original is unchanged.
	other := rt.Cons(int64(9), tail)
	if other.Tail != tail || tail.Head != 2 {
		t.Fatal("a second Cons onto one tail disturbed it")
	}
}

// TestEmptyListIsNil pins nil as the empty list rather than a distinguished
// allocated cell. It is what makes a Go zero value correct for a list — the
// opposite of the enum tag, where the zero value had to be made detectably
// invalid — so a `var xs *rt.List[int64]` needs no initialization.
func TestEmptyListIsNil(t *testing.T) {
	var xs *rt.List[int64]
	if rt.FormatList(xs, rt.FormatInt) != "[]" {
		t.Fatal("a nil list does not render as the empty list")
	}
	if !rt.ListEqual(xs, nil, rt.Eq[int64]) {
		t.Fatal("a nil list is not equal to the empty list")
	}
	if rt.Cons(int64(1), xs).Len != 1 {
		t.Fatal("consing onto nil did not produce a one-element list")
	}
}

func TestListEqual(t *testing.T) {
	ints := func(vs ...int64) *rt.List[int64] {
		var out *rt.List[int64]
		for i := len(vs) - 1; i >= 0; i-- {
			out = rt.Cons(vs[i], out)
		}
		return out
	}
	cases := []struct {
		name string
		a, b *rt.List[int64]
		want bool
	}{
		{"equal", ints(1, 2, 3), ints(1, 2, 3), true},
		{"different element", ints(1, 2, 3), ints(1, 9, 3), false},
		{"a proper prefix is not equal", ints(1, 2), ints(1, 2, 3), false},
		{"the longer side is not equal either", ints(1, 2, 3), ints(1, 2), false},
		{"empty equals empty", nil, nil, true},
		{"empty is not equal to non-empty", nil, ints(1), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rt.ListEqual(tc.a, tc.b, rt.Eq[int64]); got != tc.want {
				t.Fatalf("ListEqual = %v, want %v", got, tc.want)
			}
		})
	}
	// Nested, so the comparator parameter is exercised as more than a formality:
	// a list of lists cannot bottom out at Go's `==`, which would compare cells
	// by address.
	nested := func(rows ...*rt.List[int64]) *rt.List[*rt.List[int64]] {
		var out *rt.List[*rt.List[int64]]
		for i := len(rows) - 1; i >= 0; i-- {
			out = rt.Cons(rows[i], out)
		}
		return out
	}
	elem := func(a, b *rt.List[int64]) bool { return rt.ListEqual(a, b, rt.Eq[int64]) }
	if !rt.ListEqual(nested(ints(1), ints(2)), nested(ints(1), ints(2)), elem) {
		t.Fatal("two structurally equal lists of lists compared unequal")
	}
	if rt.ListEqual(nested(ints(1)), nested(ints(2)), elem) {
		t.Fatal("two different lists of lists compared equal")
	}
}

// TestEqFloatIsReflexiveForNaN pins the reason EqFloat exists beside Eq.
//
// Nomi's `==` on Float is reflexive: `nan == nan` is True, which is what makes a
// NaN map key retrievable from the map it was inserted into. Go's is IEEE. The
// `comparable` constraint on Eq admits float64, so the wrong one compiles.
// The second half of this test pins that they disagree, so nobody
// "simplifies" EqFloat away.
func TestEqFloatIsReflexiveForNaN(t *testing.T) {
	nan := math.NaN()
	if !rt.EqFloat(nan, nan) {
		t.Fatal("EqFloat is not reflexive for NaN")
	}
	if rt.Eq(nan, nan) {
		t.Fatal("Eq became reflexive for NaN, so EqFloat is no longer distinguishable")
	}
	if !rt.EqFloat(0.0, math.Copysign(0, -1)) {
		t.Fatal("EqFloat separated 0.0 from -0.0, where Nomi and Go agree")
	}
	if rt.EqFloat(nan, 1.0) || rt.EqFloat(1.0, nan) {
		t.Fatal("EqFloat equated NaN with a number")
	}
	if !rt.EqFloat(1.5, 1.5) || rt.EqFloat(1.5, 2.5) {
		t.Fatal("EqFloat is wrong on ordinary values")
	}
	if !rt.ListEqual(rt.Cons(nan, nil), rt.Cons(nan, nil), rt.EqFloat) {
		t.Fatal("a list of NaN compared unequal to itself")
	}
}

// TestFormatListMatchesTheDisplayImpl pins the rule from `impl Display for
// List<T>` (std/lists.nomi) and not the `values:` row rendering.
//
// The two differ on exactly one thing and it is observable: the row renders
// elements with Inspect, which quotes strings, while the Display impl renders
// them with Display.to_string, which does not. `${["file.txt"]}` is
// `[file.txt]`, so that is the answer to reproduce.
func TestFormatListMatchesTheDisplayImpl(t *testing.T) {
	words := rt.Cons("file.txt", rt.Cons("other", nil))
	if got := rt.FormatList(words, rt.FormatString); got != "[file.txt, other]" {
		t.Fatalf("FormatList = %q, want unquoted elements", got)
	}
	if got := rt.FormatList(rt.Cons(int64(1), nil), rt.FormatInt); got != "[1]" {
		t.Fatalf("FormatList = %q", got)
	}
	var empty *rt.List[int64]
	if got := rt.FormatList(empty, rt.FormatInt); got != "[]" {
		t.Fatalf("FormatList of the empty list = %q", got)
	}
	nested := rt.Cons(rt.Cons(int64(1), rt.Cons(int64(2), nil)), rt.Cons(rt.Cons(int64(3), nil), nil))
	inner := func(v *rt.List[int64]) string { return rt.FormatList(v, rt.FormatInt) }
	if got := rt.FormatList(nested, inner); got != "[[1, 2], [3]]" {
		t.Fatalf("nested FormatList = %q", got)
	}
}

// BenchmarkConsPrepend measures the claim list.go makes about why a cons cell
// beats a slice for Nomi's costs: prepending n elements is n allocations and
// linear time, where an immutable slice would have to copy on every prepend and
// be quadratic.
func BenchmarkConsPrepend(b *testing.B) {
	for b.Loop() {
		var xs *rt.List[int64]
		for i := range 1000 {
			xs = rt.Cons(int64(i), xs)
		}
		if xs.Len != 1000 {
			b.Fatal("wrong length")
		}
	}
}
