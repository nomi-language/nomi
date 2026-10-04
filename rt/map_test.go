package rt

import (
	"context"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// Everything in this file is an ABSOLUTE assertion, and that is deliberate.
//
// A golden file records whatever the implementation printed, so a map that was
// wrong in a consistent way would be recorded wrong. Every expectation below
// is therefore spelled out, and the ones taken from observed program behaviour
// name the `nomi run` output they were taken from.

// stringKeys is the key pair for a `Map<String, _>`.
func stringKeys() (func(string) uint64, func(string, string) bool) {
	return HashString, Eq[string]
}

func intKeys() (func(int64) uint64, func(int64, int64) bool) {
	return HashInt, Eq[int64]
}

// listKeys is the key pair for a `Map<List<Int>, _>` — the case a Go
// `map` cannot express, because a `*List[int64]` compares by ADDRESS under Go's
// own `==`.
func listKeys() (func(*List[int64]) uint64, func(*List[int64], *List[int64]) bool) {
	return func(xs *List[int64]) uint64 { return HashList(xs, HashInt) },
		func(a, b *List[int64]) bool { return ListEqual(a, b, Eq[int64]) }
}

func list(xs ...int64) *List[int64] {
	var out *List[int64]
	for i := len(xs) - 1; i >= 0; i-- {
		out = Cons(xs[i], out)
	}
	return out
}

func TestMapGetPutSize(t *testing.T) {
	h, eq := stringKeys()
	var m Map[string, int64]
	if got := MapSize(m); got != 0 {
		t.Fatalf("empty size = %d, want 0", got)
	}
	if _, ok := MapLookup(m, h, eq, "a"); ok {
		t.Fatal("empty map reported a key")
	}
	m = MapPut(m, h, eq, "a", 1)
	m = MapPut(m, h, eq, "b", 2)
	if got := MapSize(m); got != 2 {
		t.Fatalf("size = %d, want 2", got)
	}
	if v, ok := MapLookup(m, h, eq, "a"); !ok || v != 1 {
		t.Fatalf(`get "a" = (%d, %v), want (1, true)`, v, ok)
	}
	if v, ok := MapLookup(m, h, eq, "b"); !ok || v != 2 {
		t.Fatalf(`get "b" = (%d, %v), want (2, true)`, v, ok)
	}
	if _, ok := MapLookup(m, h, eq, "z"); ok {
		t.Fatal(`get "z" found a key that was never put`)
	}
	// Overwrite: last write wins, size unchanged.
	m2 := MapPut(m, h, eq, "a", 9)
	if v, _ := MapLookup(m2, h, eq, "a"); v != 9 {
		t.Fatalf(`overwritten "a" = %d, want 9`, v)
	}
	if got := MapSize(m2); got != 2 {
		t.Fatalf("size after overwrite = %d, want 2", got)
	}
	// And the ORIGINAL is untouched, which is the whole point of persistence.
	if v, _ := MapLookup(m, h, eq, "a"); v != 1 {
		t.Fatalf(`put mutated its argument: original "a" = %d, want 1`, v)
	}
}

// TestMapPutDoesNotMutateAnySharedVersion keeps every intermediate map alive
// and re-reads all of them at the end.
//
// The failure this catches is the one a persistent structure dies of: an insert
// that writes through into a node an older version still points at. It only
// shows up when the older version is read AFTER the newer one was built, and
// only at a size that forces node splitting — hence 4096 keys, which is deep
// enough to need several trie levels.
func TestMapPutDoesNotMutateAnySharedVersion(t *testing.T) {
	h, eq := intKeys()
	const n = 4096
	versions := make([]Map[int64, int64], n+1)
	for i := range int64(n) {
		versions[i+1] = MapPut(versions[i], h, eq, i, i*10)
	}
	for i := range n + 1 {
		if got := MapSize(versions[i]); got != int64(i) {
			t.Fatalf("version %d size = %d, want %d", i, got, i)
		}
		for k := range int64(i) {
			v, ok := MapLookup(versions[i], h, eq, k)
			if !ok || v != k*10 {
				t.Fatalf("version %d: key %d = (%d, %v), want (%d, true)", i, k, v, ok, k*10)
			}
		}
		if _, ok := MapLookup(versions[i], h, eq, int64(i)); ok {
			t.Fatalf("version %d contains key %d, which was inserted later", i, i)
		}
	}
}

// TestMapNonComparableKey is the fixture the whole representation exists for.
//
// A `*List[int64]` key is Go-comparable and compares by ADDRESS, so a Go
// `map[*List[int64]]V` would accept these keys, compile clean, and then fail to
// find any of them from a freshly built equal list. Every lookup below uses a
// NEW cons chain, never the one that was inserted.
func TestMapNonComparableKey(t *testing.T) {
	h, eq := listKeys()
	var m Map[*List[int64], string]
	m = MapPut(m, h, eq, list(1, 2), "x")
	m = MapPut(m, h, eq, list(3), "y")
	m = MapPut(m, h, eq, list(), "empty")

	for _, tc := range []struct {
		key  *List[int64]
		want string
	}{
		{list(1, 2), "x"},
		{list(3), "y"},
		{list(), "empty"},
	} {
		got, ok := MapLookup(m, h, eq, tc.key)
		if !ok || got != tc.want {
			t.Errorf("get %v = (%q, %v), want (%q, true) — a fresh but EQUAL list key must find its entry",
				FormatList(tc.key, FormatInt), got, ok, tc.want)
		}
	}
	if _, ok := MapLookup(m, h, eq, list(1, 2, 3)); ok {
		t.Error("get [1, 2, 3] found an entry; only [1, 2], [3] and [] were inserted")
	}
	// Overwriting through a different-but-equal key must hit the SAME entry,
	// not add a second one. An address-keyed map would grow to four.
	m = MapPut(m, h, eq, list(1, 2), "x2")
	if got := MapSize(m); got != 3 {
		t.Fatalf("size after overwriting [1, 2] = %d, want 3 — a pointer-keyed map would say 4", got)
	}
	if got, _ := MapLookup(m, h, eq, list(1, 2)); got != "x2" {
		t.Fatalf("overwritten [1, 2] = %q, want %q", got, "x2")
	}
	// Removal through an equal-but-distinct key, likewise.
	m = MapRemove(m, h, eq, list(3))
	if got := MapSize(m); got != 2 {
		t.Fatalf("size after removing [3] = %d, want 2", got)
	}
	if _, ok := MapLookup(m, h, eq, list(3)); ok {
		t.Error("removed [3] is still present")
	}
}

// TestMapTupleKey covers the other shape the corpus already uses —
// 08-pattern-matching/map_patterns_test.nomi's `{(1, 2) => "a"}`. A tuple lowers
// to an anonymous Go struct, so this key IS Go-comparable and correct under Go's
// `==`; the point of the test is that the structural path agrees.
func TestMapTupleKey(t *testing.T) {
	type pair = struct {
		F0 int64
		F1 int64
	}
	h := func(p pair) uint64 {
		return HashMix(HashMix(HashTupleSeed, HashInt(p.F0)), HashInt(p.F1))
	}
	eq := Eq[pair]
	var m Map[pair, string]
	m = MapPut(m, h, eq, pair{1, 2}, "a")
	m = MapPut(m, h, eq, pair{3, 4}, "b")
	if got, ok := MapLookup(m, h, eq, pair{3, 4}); !ok || got != "b" {
		t.Fatalf("get (3, 4) = (%q, %v), want (b, true)", got, ok)
	}
	if _, ok := MapLookup(m, h, eq, pair{4, 3}); ok {
		t.Fatal("get (4, 3) found an entry; tuple keys are ordered")
	}
}

// TestMapInsertionOrder pins the ordering rules against output read off
// `nomi run` rather than guessed:
//
//	{"a" => 1, "b" => 2}                              -> {a => 1, b => 2}
//	Map.put(m, "a", 9)                                -> {a => 9, b => 2}
//	Map.put(Map.remove(m, "a"), "a", 7)               -> {b => 2, a => 7}
func TestMapInsertionOrder(t *testing.T) {
	h, eq := stringKeys()
	base := MapOf(h, eq, []MapEntry[string, int64]{{"a", 1}, {"b", 2}})

	if got := keyList(base); got != "a,b" {
		t.Errorf("literal order = %q, want %q", got, "a,b")
	}
	// put over an existing key keeps that key's POSITION.
	if got := keyList(MapPut(base, h, eq, "a", 9)); got != "a,b" {
		t.Errorf("order after overwriting a = %q, want %q", got, "a,b")
	}
	// remove then put moves it to the END: the sequence counter does not roll
	// back. This is the observable consequence.
	moved := MapPut(MapRemove(base, h, eq, "a"), h, eq, "a", 7)
	if got := keyList(moved); got != "b,a" {
		t.Errorf("order after remove+put of a = %q, want %q", got, "b,a")
	}
	if got := FormatMap(moved, FormatString, FormatInt); got != "{b => 2, a => 7}" {
		t.Errorf("render after remove+put = %q, want %q", got, "{b => 2, a => 7}")
	}
	// And the base is unchanged by either.
	if got := FormatMap(base, FormatString, FormatInt); got != "{a => 1, b => 2}" {
		t.Errorf("base was mutated: %q", got)
	}
}

// TestMapOrderSurvivesRemovalAtScale exercises the SORTING path in MapEntries.
// The fast path is taken only while nothing has been removed, so a test that
// never removes anything never runs the sort at all.
func TestMapOrderSurvivesRemovalAtScale(t *testing.T) {
	h, eq := intKeys()
	const n = 2000
	m := Map[int64, int64]{}
	for i := int64(0); i < n; i++ {
		m = MapPut(m, h, eq, i, i)
	}
	// Drop every third key, then append new ones. The survivors must keep
	// their original relative order and the new keys must follow all of them.
	for i := int64(0); i < n; i += 3 {
		m = MapRemove(m, h, eq, i)
	}
	for i := int64(n); i < n+10; i++ {
		m = MapPut(m, h, eq, i, i)
	}
	ents := MapEntries(m)
	if int64(len(ents)) != MapSize(m) {
		t.Fatalf("MapEntries returned %d entries, size says %d", len(ents), MapSize(m))
	}
	prev := int64(-1)
	for _, e := range ents {
		if e.Key <= prev {
			t.Fatalf("order broke: %d follows %d", e.Key, prev)
		}
		if e.Key < n && e.Key%3 == 0 {
			t.Fatalf("removed key %d is still present", e.Key)
		}
		prev = e.Key
	}
	if prev != n+9 {
		t.Fatalf("last key = %d, want %d", prev, n+9)
	}
}

// TestMapCollisionBucket forces the path a 64-bit structural-hash collision
// takes: the trie runs out of bits and stores both keys in one bucket.
//
// Unreachable with a real hasher at any size a test can build, so the hasher is
// the thing under test's control here — which is the only way to see the
// collision code run at all. Without it, `mapSplit`'s exhaustion arm and
// `mapInsert`/`mapDelete`'s bucket arms are dead in every test.
func TestMapCollisionBucket(t *testing.T) {
	// Every key hashes to the same value, so the trie can never separate them.
	h := func(string) uint64 { return 0xdeadbeefcafe1234 }
	eq := Eq[string]
	var m Map[string, int64]
	keys := []string{"a", "b", "c", "d"}
	for i, k := range keys {
		m = MapPut(m, h, eq, k, int64(i))
	}
	if got := MapSize(m); got != 4 {
		t.Fatalf("size = %d, want 4 — colliding keys must not overwrite one another", got)
	}
	for i, k := range keys {
		if v, ok := MapLookup(m, h, eq, k); !ok || v != int64(i) {
			t.Fatalf("get %q = (%d, %v), want (%d, true)", k, v, ok, i)
		}
	}
	if _, ok := MapLookup(m, h, eq, "z"); ok {
		t.Fatal(`get "z" found an entry in the collision bucket; the hash matches but the key does not`)
	}
	if got := keyList(m); got != "a,b,c,d" {
		t.Fatalf("collision-bucket order = %q, want %q", got, "a,b,c,d")
	}
	// Overwrite inside the bucket keeps position and size.
	m = MapPut(m, h, eq, "b", 99)
	if v, _ := MapLookup(m, h, eq, "b"); v != 99 {
		t.Fatalf(`overwritten "b" = %d, want 99`, v)
	}
	if got := MapSize(m); got != 4 || keyList(m) != "a,b,c,d" {
		t.Fatalf("after overwrite: size %d order %q, want 4 and a,b,c,d", MapSize(m), keyList(m))
	}
	// Remove from the middle of the bucket, then from the ends.
	m = MapRemove(m, h, eq, "b")
	if got := MapSize(m); got != 3 || keyList(m) != "a,c,d" {
		t.Fatalf("after removing b: size %d order %q, want 3 and a,c,d", MapSize(m), keyList(m))
	}
	for _, k := range []string{"a", "c", "d"} {
		m = MapRemove(m, h, eq, k)
	}
	if got := MapSize(m); got != 0 {
		t.Fatalf("size after draining the bucket = %d, want 0", got)
	}
	if got := FormatMap(m, FormatString, FormatInt); got != "{=>}" {
		t.Fatalf("drained map renders %q, want %q", got, "{=>}")
	}
}

// TestMapCollisionsCoexistWithBranches puts colliding and non-colliding keys in
// one map: the bucket must live inside the trie rather than replacing it.
func TestMapCollisionsCoexistWithBranches(t *testing.T) {
	// Two keys share a hash; the rest get distinct ones.
	h := func(s string) uint64 {
		if s == "x" || s == "y" {
			return 7
		}
		return HashString(s)
	}
	eq := Eq[string]
	var m Map[string, int64]
	for i, k := range []string{"x", "alpha", "y", "beta", "gamma"} {
		m = MapPut(m, h, eq, k, int64(i))
	}
	if got := MapSize(m); got != 5 {
		t.Fatalf("size = %d, want 5", got)
	}
	for i, k := range []string{"x", "alpha", "y", "beta", "gamma"} {
		if v, ok := MapLookup(m, h, eq, k); !ok || v != int64(i) {
			t.Fatalf("get %q = (%d, %v), want (%d, true)", k, v, ok, i)
		}
	}
	if got := keyList(m); got != "x,alpha,y,beta,gamma" {
		t.Fatalf("order = %q, want %q", got, "x,alpha,y,beta,gamma")
	}
}

func TestMapRemove(t *testing.T) {
	h, eq := stringKeys()
	m := MapOf(h, eq, []MapEntry[string, int64]{{"a", 1}, {"b", 2}})
	// Removing an absent key is a no-op and returns the SAME map.
	same := MapRemove(m, h, eq, "z")
	if MapSize(same) != 2 {
		t.Fatalf("removing an absent key changed the size to %d", MapSize(same))
	}
	got := MapRemove(m, h, eq, "a")
	if s := MapSize(got); s != 1 {
		t.Fatalf("size after remove = %d, want 1", s)
	}
	if _, ok := MapLookup(got, h, eq, "a"); ok {
		t.Fatal(`"a" survived its removal`)
	}
	if v, ok := MapLookup(got, h, eq, "b"); !ok || v != 2 {
		t.Fatalf(`"b" = (%d, %v) after removing "a", want (2, true)`, v, ok)
	}
	// The original still has both, which is what remove being persistent means.
	if MapSize(m) != 2 {
		t.Fatalf("remove mutated its argument: size %d", MapSize(m))
	}
	drained := MapRemove(got, h, eq, "b")
	if MapSize(drained) != 0 || drained.root != nil {
		t.Fatalf("draining left size %d root %v, want 0 and nil", MapSize(drained), drained.root)
	}
}

// TestMapMerge pins `Map.merge` against `nomi run`'s output:
// `Map.merge({"a" => 1}, {"b" => 2, "a" => 9})` prints `{a => 9, b => 2}`.
func TestMapMerge(t *testing.T) {
	h, eq := stringKeys()
	a := MapOf(h, eq, []MapEntry[string, int64]{{"a", 1}})
	b := MapOf(h, eq, []MapEntry[string, int64]{{"b", 2}, {"a", 9}})
	if got := FormatMap(MapMerge(a, b, h, eq), FormatString, FormatInt); got != "{a => 9, b => 2}" {
		t.Errorf("merge = %q, want %q", got, "{a => 9, b => 2}")
	}
	if got := FormatMap(a, FormatString, FormatInt); got != "{a => 1}" {
		t.Errorf("merge mutated a: %q", got)
	}
	if got := FormatMap(b, FormatString, FormatInt); got != "{b => 2, a => 9}" {
		t.Errorf("merge mutated b: %q", got)
	}
}

// TestMapOfLastWriteWinsKeepingPosition pins the rule
// `{"a" => 1, "a" => 9}` and `Iter.to_map([("a", 1), ("b", 2), ("a", 9)])`
// both depend on. `nomi run` prints `{a => 9, b => 2}` for the latter.
func TestMapOfLastWriteWinsKeepingPosition(t *testing.T) {
	h, eq := stringKeys()
	m := MapOf(h, eq, []MapEntry[string, int64]{{"a", 1}, {"b", 2}, {"a", 9}})
	if got := FormatMap(m, FormatString, FormatInt); got != "{a => 9, b => 2}" {
		t.Errorf("MapOf with a duplicate key = %q, want %q", got, "{a => 9, b => 2}")
	}
	if got := MapSize(m); got != 2 {
		t.Errorf("size = %d, want 2", got)
	}
}

// TestMapEqualIgnoresOrder pins Nomi's answer:
// `{"a" => 1, "b" => 2} == {"b" => 2, "a" => 1}` is True.
func TestMapEqualIgnoresOrder(t *testing.T) {
	h, eq := stringKeys()
	ab := MapOf(h, eq, []MapEntry[string, int64]{{"a", 1}, {"b", 2}})
	ba := MapOf(h, eq, []MapEntry[string, int64]{{"b", 2}, {"a", 1}})
	if !MapEqual(ab, ba, h, eq, Eq[int64]) {
		t.Error("maps with the same entries in a different order compared unequal")
	}
	if keyList(ab) == keyList(ba) {
		t.Fatal("the two maps were built in the same order; the test proves nothing")
	}
	if MapEqual(ab, MapPut(ab, h, eq, "b", 3), h, eq, Eq[int64]) {
		t.Error("a differing value compared equal")
	}
	if MapEqual(ab, MapRemove(ab, h, eq, "a"), h, eq, Eq[int64]) {
		t.Error("a differing size compared equal")
	}
	var empty Map[string, int64]
	if !MapEqual(empty, Map[string, int64]{}, h, eq, Eq[int64]) {
		t.Error("two empty maps compared unequal")
	}
}

// TestMapEqualComparesValuesStructurally: a `Map<String, List<Int>>` holds
// values Go's `==` would compare by address.
func TestMapEqualComparesValuesStructurally(t *testing.T) {
	h, eq := stringKeys()
	valEq := func(a, b *List[int64]) bool { return ListEqual(a, b, Eq[int64]) }
	a := MapOf(h, eq, []MapEntry[string, *List[int64]]{{"k", list(1, 2)}})
	b := MapOf(h, eq, []MapEntry[string, *List[int64]]{{"k", list(1, 2)}})
	if !MapEqual(a, b, h, eq, valEq) {
		t.Error("equal list values in two maps compared unequal")
	}
	if a.root.leaf[0].Val == b.root.leaf[0].Val {
		t.Fatal("the two lists are the same pointer; the test proves nothing")
	}
}

// TestFormatMapMatchesTheDisplayImpl pins the rendering against output read
// off `nomi run`: `${{"a" => 1, "b" => 2}}` prints `{a => 1, b => 2}` — unquoted, because
// `impl Display for Map<K, V>` renders with Display.to_string, not Inspect.
func TestFormatMapMatchesTheDisplayImpl(t *testing.T) {
	h, eq := stringKeys()
	cases := []struct {
		m    Map[string, int64]
		want string
	}{
		{Map[string, int64]{}, "{=>}"},
		{MapOf(h, eq, []MapEntry[string, int64]{{"a", 1}}), "{a => 1}"},
		{MapOf(h, eq, []MapEntry[string, int64]{{"a", 1}, {"b", 2}}), "{a => 1, b => 2}"},
	}
	for _, tc := range cases {
		if got := FormatMap(tc.m, FormatString, FormatInt); got != tc.want {
			t.Errorf("FormatMap = %q, want %q", got, tc.want)
		}
	}
	// A List key renders per `impl Display for List<T>`, which is also
	// unquoted: `nomi run` prints `{[1, 2] => x, [3] => y}`.
	lh, leq := listKeys()
	lm := MapOf(lh, leq, []MapEntry[*List[int64], string]{{list(1, 2), "x"}, {list(3), "y"}})
	want := "{[1, 2] => x, [3] => y}"
	got := FormatMap(lm, func(xs *List[int64]) string { return FormatList(xs, FormatInt) }, FormatString)
	if got != want {
		t.Errorf("FormatMap over List keys = %q, want %q", got, want)
	}
}

// TestMapNaNAndSignedZeroKeys is the hash/equality LAW at the two places it is
// easiest to break, and both are keys a program can lose.
//
// `nan == nan` is True in Nomi (EqFloat), so a NaN key must be retrievable —
// verified with `nomi run`: `Map.get({0.0 / 0.0 => 1}, 0.0 / 0.0)` is
// `Some(1)`. And `-0.0 == 0.0`, so they must be ONE key:
// `Map.get({0.0 => 1}, -0.0)` is `Some(1)`.
func TestMapNaNAndSignedZeroKeys(t *testing.T) {
	h, eq := HashFloat, EqFloat
	var m Map[float64, int64]
	nan := math.NaN()
	m = MapPut(m, h, eq, nan, 1)
	if v, ok := MapLookup(m, h, eq, math.NaN()); !ok || v != 1 {
		t.Errorf("a NaN key was not retrievable: got (%d, %v), want (1, true)", v, ok)
	}
	// A DIFFERENT NaN encoding must land in the same bucket.
	other := math.Float64frombits(math.Float64bits(nan) | 0x3)
	if !math.IsNaN(other) {
		t.Fatal("constructed a non-NaN; the test needs a second NaN encoding")
	}
	if v, ok := MapLookup(m, h, eq, other); !ok || v != 1 {
		t.Errorf("a second NaN encoding missed the entry: got (%d, %v), want (1, true)", v, ok)
	}
	m = MapPut(m, h, eq, 0.0, 2)
	if got := MapSize(m); got != 2 {
		t.Fatalf("size = %d, want 2", got)
	}
	if v, ok := MapLookup(m, h, eq, math.Copysign(0, -1)); !ok || v != 2 {
		t.Errorf("-0.0 missed the 0.0 entry: got (%d, %v), want (2, true)", v, ok)
	}
	// And putting -0.0 must overwrite rather than add.
	m = MapPut(m, h, eq, math.Copysign(0, -1), 3)
	if got := MapSize(m); got != 2 {
		t.Fatalf("size after putting -0.0 = %d, want 2 — signed zeros are one key", got)
	}
	if v, _ := MapLookup(m, h, eq, 0.0); v != 3 {
		t.Errorf("0.0 = %d after putting -0.0, want 3", v)
	}
}

// TestMapAtScale is the correctness-under-depth check: 20000 keys forces the
// trie several levels deep, and every one of them must be findable, countable
// and orderable.
func TestMapAtScale(t *testing.T) {
	h, eq := stringKeys()
	const n = 20000
	m := Map[string, int64]{}
	for i := range n {
		m = MapPut(m, h, eq, "k"+strconv.Itoa(i), int64(i))
	}
	if got := MapSize(m); got != n {
		t.Fatalf("size = %d, want %d", got, n)
	}
	for i := range n {
		v, ok := MapLookup(m, h, eq, "k"+strconv.Itoa(i))
		if !ok || v != int64(i) {
			t.Fatalf("key k%d = (%d, %v), want (%d, true)", i, v, ok, i)
		}
	}
	ents := MapEntries(m)
	if len(ents) != n {
		t.Fatalf("MapEntries returned %d, want %d", len(ents), n)
	}
	for i, e := range ents {
		if e.Val != int64(i) {
			t.Fatalf("entry %d has value %d; insertion order was lost", i, e.Val)
		}
	}
	// Remove half, in an order unrelated to insertion.
	for i := n - 1; i >= 0; i -= 2 {
		m = MapRemove(m, h, eq, "k"+strconv.Itoa(i))
	}
	if got := MapSize(m); got != n/2 {
		t.Fatalf("size after removing half = %d, want %d", got, n/2)
	}
	for i := range n {
		_, ok := MapLookup(m, h, eq, "k"+strconv.Itoa(i))
		if want := i%2 == 0; ok != want {
			t.Fatalf("after removal, key k%d present = %v, want %v", i, ok, want)
		}
	}
}

// keyList is the insertion-ordered keys, comma-joined — a compact way to assert
// an exact order rather than a property of it.
func keyList[V any](m Map[string, V]) string {
	ents := MapEntries(m)
	parts := make([]string, len(ents))
	for i, e := range ents {
		parts[i] = e.Key
	}
	return strings.Join(parts, ",")
}

// --- the cost table, asserted by counting ----------------------------------

// TestMeasureMapCosts asserts map.go's cost table for get and size by counting
// the work MapLookup's own loop does. Nothing here reads a clock, so machine
// load cannot change the result.
//
// The counts come from mapCountNode and mapCountProbe, which MapLookup calls at
// each node it visits and each entry whose hash it compares. They are compiled
// out of a normal build (mapcount_off.go), so under a plain `go test` this test
// re-runs itself with `-tags rtmapcount` and fails if the child fails.
//
// The bound, derived from map.go. A branch node spends mapBits = 5 bits of hash
// per level, and an entry sits as a leaf at the first level where no other key
// shares its slot. HashInt is the identity, so the keys 0..n-1 share their low
// 5d bits in groups of ceil(n / 32^d), and a key becomes a leaf at the first
// depth D with 32^D >= n. So for these keys get visits at most
// log32ceil(n) nodes, some key visits exactly that many, and a hit compares
// exactly one entry. The bound has no slack: one extra node per level, or a
// scan of a level's entries, breaks it. For keys in general the ceiling is
// mapLastShift/mapBits + 2 = 14 nodes: branch levels at shifts 0..60, then a
// collision bucket, which the last block of the test reaches.
func TestMeasureMapCosts(t *testing.T) {
	if testing.Short() {
		t.Skip("builds maps up to 2^20 entries; -short")
	}
	if !mapCounting {
		runMapCountingBuild(t)
		return
	}

	h, eq := intKeys()
	for _, n := range []int{1 << 4, 1 << 8, 1 << 12, 1 << 16, 1 << 20} {
		m := Map[int64, int64]{}
		for k := range int64(n) {
			m = MapPut(m, h, eq, k, k)
		}
		bound := log32ceil(n)

		var hashes, eqs, worst, hitNodes int
		ch := func(k int64) uint64 { hashes++; return h(k) }
		ceq := func(a, b int64) bool { eqs++; return eq(a, b) }
		// Keys n..2n-1 are misses. They walk the same subtrees, so they are
		// held to the same node bound and to at most one probe.
		for k := range int64(2 * n) {
			hashes, eqs = 0, 0
			mapCountReset()
			_, ok := MapLookup(m, ch, ceq, k)
			c := mapCountRead()
			worst = max(worst, c.nodes)
			if hit := k < int64(n); ok != hit {
				t.Fatalf("n = %d: get(%d) found = %v, want %v", n, k, ok, hit)
			}
			if ok {
				hitNodes += c.nodes
			}
			if c.nodes > bound {
				t.Fatalf("n = %d: get(%d) visited %d nodes; the bound is log32ceil(n) = %d", n, k, c.nodes, bound)
			}
			if ok && c.probes != 1 || c.probes > 1 {
				t.Fatalf("n = %d: get(%d) (found %v) compared %d entries; a branch slot holds one entry", n, k, ok, c.probes)
			}
			if hashes != 1 || eqs > 1 {
				t.Fatalf("n = %d: get(%d) made %d hash and %d eq calls; the cost table says one and at most one", n, k, hashes, eqs)
			}
		}
		// A counter that never fires passes every <= check above. Reaching
		// the bound exactly shows it fires once per level.
		if worst != bound {
			t.Errorf("n = %d: deepest get visited %d nodes, the trie over 0..n-1 is %d levels deep", n, worst, bound)
		}

		mapCountReset()
		MapSize(m)
		if c := mapCountRead(); c != (mapCost{}) {
			t.Errorf("n = %d: MapSize visited %d nodes and compared %d entries; it must be a field read", n, c.nodes, c.probes)
		}
		t.Logf("n = %7d   bound %d   worst %d nodes   %.2f nodes per hit   size 0 nodes",
			n, bound, worst, float64(hitNodes)/float64(n))
	}

	// MapSize is a field read and not a walk: over a nil root it still answers
	// the count. A walk would return 0 here.
	if got := MapSize(Map[int64, int64]{n: 12345}); got != 12345 {
		t.Errorf("MapSize over a nil root returned %d, want 12345; it must be a field read, not a walk", got)
	}

	// The structural ceiling, and a check that the probe counter sees a scan.
	// Every key hashes to 0, so mapSplit descends through shifts 5..60 and
	// puts all eight keys in one collision bucket at the 14th node, in
	// insertion order. get(7) walks all 14 nodes and compares all 8 entries.
	deg := Map[int64, int64]{}
	zero := func(int64) uint64 { return 0 }
	for k := range int64(8) {
		deg = MapPut(deg, zero, eq, k, k)
	}
	mapCountReset()
	if _, ok := MapLookup(deg, zero, eq, 7); !ok {
		t.Fatal("collision bucket: get(7) missed")
	}
	if c, want := mapCountRead(), (mapCost{nodes: mapLastShift/mapBits + 2, probes: 8}); c != want {
		t.Errorf("collision bucket: get(7) visited %d nodes and compared %d entries, want %d and %d",
			c.nodes, c.probes, want.nodes, want.probes)
	}
}

// log32ceil is the least D >= 1 with 32^D >= n: the depth of a trie holding
// the identity-hashed keys 0..n-1.
func log32ceil(n int) int {
	d := 1
	for c := 32; c < n; c *= 32 {
		d++
	}
	return d
}

// runMapCountingBuild runs TestMeasureMapCosts in a child `go test` built with
// the rtmapcount tag, where MapLookup's counters are live. It requires the
// child to report a PASS for this test by name, because `go test -run` that
// matches nothing also exits 0.
func runMapCountingBuild(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("the counting build needs the go command: %v", err)
	}
	out, err := exec.Command(goBin, "test", "-tags=rtmapcount", "-run=^TestMeasureMapCosts$", "-count=1", "-v", ".").CombinedOutput()
	t.Logf("go test -tags=rtmapcount -run=^TestMeasureMapCosts$ .\n%s", out)
	if err != nil {
		t.Fatalf("counting build failed: %v", err)
	}
	if !strings.Contains(string(out), "--- PASS: TestMeasureMapCosts") {
		t.Fatal("counting build did not report TestMeasureMapCosts as passed")
	}
}

func BenchmarkMapPut(b *testing.B) {
	h, eq := intKeys()
	for _, n := range []int{16, 1024, 65536} {
		m := Map[int64, int64]{}
		for k := int64(0); k < int64(n); k++ {
			m = MapPut(m, h, eq, k, k)
		}
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; b.Loop(); i++ {
				MapPut(m, h, eq, int64(n+i), 1)
			}
		})
	}
}

func BenchmarkMapEntries(b *testing.B) {
	h, eq := intKeys()
	for _, removed := range []bool{false, true} {
		m := Map[int64, int64]{}
		for k := int64(0); k < 4096; k++ {
			m = MapPut(m, h, eq, k, k)
		}
		name := "dense"
		if removed {
			m = MapRemove(m, h, eq, 0)
			name = "afterRemoval"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				MapEntries(m)
			}
		})
	}
}

// TestStructuralHashLaw is the contract every key hasher has to satisfy:
// `eq(a, b)` implies `hash(a) == hash(b)`. It is the ONLY property the hash owes
// — a degenerate hash is a performance bug and not a wrong answer, which is why
// mutating a list hasher to a constant leaves every map test passing.
//
// The law is what a Go `map` cannot give a Nomi key, and the list cases are the
// point: two structurally equal cons chains are two allocations, so anything
// deriving the hash from identity breaks it.
func TestStructuralHashLaw(t *testing.T) {
	lh, leq := listKeys()
	pairs := []struct {
		name string
		a, b *List[int64]
	}{
		{"empty", list(), list()},
		{"one element", list(7), list(7)},
		{"several", list(1, 2, 3), list(1, 2, 3)},
		{"shared tail vs fresh", Cons(0, list(1, 2)), list(0, 1, 2)},
	}
	for _, p := range pairs {
		if !leq(p.a, p.b) {
			t.Fatalf("%s: the two lists are not equal; the case tests nothing", p.name)
		}
		if ha, hb := lh(p.a), lh(p.b); ha != hb {
			t.Errorf("%s: equal lists hashed %d and %d — equal values must hash equally",
				p.name, ha, hb)
		}
	}
	// Distinct values SHOULD usually differ, and a hash that never does is
	// legal but useless. Asserted as a spread rather than as inequality per
	// pair, so the test states a quality expectation without pretending it is
	// a correctness one.
	seen := map[uint64]bool{}
	for i := int64(0); i < 64; i++ {
		seen[lh(list(i, i+1))] = true
	}
	if len(seen) < 60 {
		t.Errorf("64 distinct list keys produced only %d distinct hashes; the trie "+
			"would degenerate into one collision bucket", len(seen))
	}
	// And the two scalar normalizations, at the level of the law rather than of
	// the bit pattern: EqFloat says these pairs are equal, so they must hash
	// equally or the key becomes unfindable.
	for _, p := range []struct{ a, b float64 }{
		{math.NaN(), math.Float64frombits(math.Float64bits(math.NaN()) | 0x7)},
		{0.0, math.Copysign(0, -1)},
	} {
		if !EqFloat(p.a, p.b) {
			t.Fatalf("%v and %v are not EqFloat; the case tests nothing", p.a, p.b)
		}
		if ha, hb := HashFloat(p.a), HashFloat(p.b); ha != hb {
			t.Errorf("EqFloat(%v, %v) but hashes %d != %d", p.a, p.b, ha, hb)
		}
	}
}

// TestMapProjectionsAreInInsertionOrder is the rt-side guard for MapKeys,
// MapValues and MapMapValues, and it exists because rt's suite was GREEN under
// a MapKeys that consed FORWARD and therefore answered every list backwards.
//
// MEASURED: that mutant was caught only by an internal/irbuild program
// fixture, one module away. Every rt test that could have seen it went through
// FormatMap or MapEqual instead — rendering agrees with a reversed extraction
// because it never calls one, and equality is order-INSENSITIVE by design — so
// the whole existing family was blind to the one property these three functions
// add. That is the same shape as a pin family whose rows are all in one
// direction: the count says nothing about the coverage.
//
// The remove-then-put row is the one a re-numbered or gap-closing walk fails
// alone. `seq` is deliberately not rolled back by MapRemove, so a re-inserted
// key belongs at the END, and that is also the row that forces MapEntries'
// SORTING path rather than its dense fast path.
func TestMapProjectionsAreInInsertionOrder(t *testing.T) {
	h, eq := stringKeys()
	m := MapOf(h, eq, []MapEntry[string, int64]{{"a", 1}, {"b", 2}, {"c", 3}})

	if got := FormatList(MapKeys(m), FormatString); got != "[a, b, c]" {
		t.Errorf("MapKeys = %s, want [a, b, c]", got)
	}
	if got := FormatList(MapValues(m), FormatInt); got != "[1, 2, 3]" {
		t.Errorf("MapValues = %s, want [1, 2, 3]", got)
	}

	// A gap in the sequence: remove then re-insert, so the key moves to the
	// end and MapEntries takes its sorting path.
	moved := MapPut(MapRemove(m, h, eq, "a"), h, eq, "a", 7)
	if got := FormatList(MapKeys(moved), FormatString); got != "[b, c, a]" {
		t.Errorf("MapKeys after remove+put = %s, want [b, c, a] — seq does not roll back", got)
	}
	if got := FormatList(MapValues(moved), FormatInt); got != "[2, 3, 7]" {
		t.Errorf("MapValues after remove+put = %s, want [2, 3, 7]", got)
	}

	// A duplicate inside one MapOf: last value, FIRST position.
	dup := MapOf(h, eq, []MapEntry[string, int64]{{"a", 1}, {"b", 2}, {"a", 9}})
	if got := FormatList(MapKeys(dup), FormatString); got != "[a, b]" {
		t.Errorf("MapKeys over a duplicated key = %s, want [a, b]", got)
	}
	if got := FormatList(MapValues(dup), FormatInt); got != "[9, 2]" {
		t.Errorf("MapValues over a duplicated key = %s, want [9, 2]", got)
	}

	// The empty map answers the empty list, not a fault.
	var empty Map[string, int64]
	if MapKeys(empty) != nil || MapValues(empty) != nil {
		t.Error("MapKeys/MapValues over an empty map must answer the empty list")
	}

	// MapMapValues REBUILDS, so it re-derives the order rather than reading it.
	// Asserted on `moved`, whose order is the one a rebuild loses.
	fr := NewFrame(context.Background())
	tens := MapMapValues(fr, moved, h, eq, func(fr *Frame, v int64) int64 { return v * 10 })
	if got := FormatList(MapKeys(tens), FormatString); got != "[b, c, a]" {
		t.Errorf("MapMapValues lost the insertion order: keys = %s, want [b, c, a]", got)
	}
	if got := FormatList(MapValues(tens), FormatInt); got != "[20, 30, 70]" {
		t.Errorf("MapMapValues = %s, want [20, 30, 70]", got)
	}
	// A result type that is NOT the value type, which is the whole reason
	// map_values carries a third type parameter.
	named := MapMapValues(fr, m, h, eq, func(fr *Frame, v int64) string { return "n" + FormatInt(v) })
	if got := FormatMap(named, FormatString, FormatString); got != "{a => n1, b => n2, c => n3}" {
		t.Errorf("MapMapValues retyping = %s", got)
	}
}
