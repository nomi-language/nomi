package irbuild

import (
	"strings"
	"testing"
)

// The `impl List<T>` method surface, end to end.
//
// # The pinned text is the guard, and why an agreement check is not
//
// `List.equal?` and `==` share one lowering (collections.go's listEqual), which
// is the right structure — it makes the two spellings unable to disagree — and
// it also makes an AGREEMENT assertion between them blind to the failure that
// matters most. Replacing `rt.ListEqual(a, b, eq)` with a pointer comparison
// makes both spellings answer False together on `lit-lit`, `nan` and `nested`:
// they agree perfectly, on the wrong answer. A text pinned absolutely is what
// catches that.
//
// The corpus sweep does not: every `List.equal?` site in
// tests/ is in a file still refused for other reasons, so no corpus
// program exercises this code at all. That is why the fixture is not optional.

// TestListMethods_PinnedText pins the VM's text for the fixture absolutely, so
// a wrong answer that every spelling agrees on still fails.
//
// The rows that carry weight, and what moves each:
//
//	lit-built   a DIFFERENT cons chain with equal elements — the address trap
//	nan         reflexive NaN, which only rt.EqFloat gives
//	nested      the comparator recursing into an inner rt.ListEqual
//	tail-1      Some([]) for a one-element list, distinct from tail-0's None

func TestListMethods_PinnedText(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	want := strings.Join([]string{
		"lit-lit   True True",
		"lit-built True True",
		"differing False False",
		"shorter   False False",
		"empty     False False",
		"nan       True True",
		"nan-mixed True",
		"float     True True",
		"neg-zero  True True",
		"nested    True True",
		"nested-no False",
		"strings   True True",
		"tuples    True True",
		"tup-float True",
		"bools     True",
		"concat    [1, 2, 3, 4]",
		"concat-l0 [3, 4]",
		"concat-r0 [1, 2]",
		"concat-sh [1, 2, 3, 1, 2, 3]",
		"head      Some(1)",
		"head-0    None",
		"tail      Some([2, 3])",
		"tail-1    Some([])",
		"tail-0    None",
		"",
	}, "\n")
	if got := vmReference(fixture("list_methods.nomi")); got.stdout != want {
		t.Fatalf("the VM does not produce the text this fixture pins\n--- want ---\n%s\n--- got ---\n%s",
			want, got.stdout)
	}
}

// TestIterToMap_PinnedText pins the two rules a to_map over a List of pairs can
// get individually wrong.
//
// `dup {x => 9}` is last-write-wins on the VALUE. `dup3 {x => 9, y => 2}` is the
// separable half: the winning entry inherits the earlier one's POSITION, so x
// stays first. An implementation that rebuilt the map by folding the list in
// reverse would produce `{x => 1}` for the first and `{y => 2, x => 9}` for the
// second — one wrong answer and one wrong order, and only the second row sees
// it.
//
// `tupkey`, `boolkey` and `listkey` are keys Go's `comparable` would reject
// outright; they are here because a `map[K]V` representation could not express
// them at all.
func TestIterToMap_PinnedText(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	want := strings.Join([]string{
		"pairs   {a => 1, b => 2}",
		"dup     {x => 9}",
		"dup3    {x => 9, y => 2}",
		"order   {z => 1, a => 2, m => 3}",
		"one     {1 => 10}",
		"intkeys {3 => c, 1 => a, 2 => b}",
		"boolkey {True => 1, False => 0}",
		"tupkey  {(1, 2) => a, (3, 4) => b}",
		"listkey True",
		"floatk  {1.5 => a}",
		"nankey  True",
		"size    2",
		"get     True",
		"miss    False",
		"empty   True",
		"",
	}, "\n")
	got := vmReference(fixture("iter_to_map.nomi"))
	vmSkipIfKnownBlocked(t, got)
	if got.stdout != want {
		t.Fatalf("the VM does not produce the text this fixture pins\n--- want ---\n%s\n--- got ---\n%s",
			want, got.stdout)
	}
}

// --- List order -----------------------------------------------------------
//
// `List.compare` and `<` / `>` / `<=` / `>=` on two Lists, which are one
// operation with two spellings (listCompare emits both).

// TestListCompare_PinnedText pins the reference bytes absolutely.
//
// Every row is a rule with a wrong implementation attached to it:
//
//	equal      two SEPARATELY built equal chains — rt.ListCompare's pointer
//	           short-circuit does not fire, so the element walk must answer
//	Less/Greater (rows 2-3)  a proper PREFIX is Less, both directions
//	Greater/Less (rows 4-5)  `[2]` vs `[1, 9]`: the SHORTER list is Greater, so
//	                         consulting the cached Len before walking is a wrong
//	                         answer and not a fast path
//	Greater (row 6)          `[1, 9, 1]` vs `[1, 2, 9]`: the FIRST difference
//	                         decides. The operands are chosen so the two rules
//	                         DISAGREE — first difference says Greater, last
//	                         says Less — because the obvious `[1, 9, 9]` vs
//	                         `[1, 2, 2]` answers Greater under either and
//	                         asserts nothing
//	Less/Greater             empty against non-empty, both directions
//	T F T T T T              all four operators, through rt.OrderingRank
//	Equal (nan vs nan)       std/float's compare is a TOTAL order, not IEEE —
//	Greater (nan vs +inf)    an element comparator built from Go's `<` answers
//	Less (+inf vs nan)       differently on exactly these three rows
func TestListCompare_PinnedText(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	want := strings.Join([]string{
		"equal",
		"Less", "Greater",
		"Greater", "Less",
		"Greater",
		"Less", "Greater",
		"T", "F", "T", "T", "T", "T",
		"Less",
		"Equal", "Greater", "Less",
		"T", "T",
		"",
	}, "\n")
	if got := vmReference(fixture("list_compare.nomi")); got.stdout != want || got.exit != 0 {
		t.Fatalf("the VM does not produce the text this fixture pins: %s\n--- want ---\n%s",
			got, want)
	}
}
