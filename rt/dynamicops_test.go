package rt

import (
	"math"
	"testing"
)

// The two absolute pins over std/dynamic's observable strings.
//
// # Why these are absolute
//
// Every rule in dynamicops.go has one implementation, and a comparison between
// two callers of one implementation cannot see a bug in it. So an absolute pin
// is not belt-and-braces here; it is the whole guard.

// TestDynamicInspectRenderings pins `impl Debug for Dynamic`'s output.
//
// The `dict` row supplies twelve keys in reverse. With two keys a random Go map
// iteration comes out sorted half the time, so a dynamicRender missing its
// `sort.Strings` would pass by chance. A random permutation of twelve is sorted
// with probability 1/12!, so a missing sort fails every run.
//
// `nested` crosses list-into-dict-into-dict, so a recursion that handled only one
// level passes every other row.
func TestDynamicInspectRenderings(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"nil", nil, "null"},
		{"true", true, "true"},
		{"false", false, "false"},
		{"int64", int64(42), "42"},
		{"float", 3.5, "3.5"},
		{"string", "hi", `"hi"`},
		{"list", []any{int64(1), "x", true}, `[1, "x", true]`},
		// Twelve keys, supplied in reverse. See the header: with two keys a
		// dynamicRender missing its sort would pass half the time.
		{"dict", map[string]any{
			"l": int64(12), "k": int64(11), "j": int64(10), "i": int64(9),
			"h": int64(8), "g": int64(7), "f": int64(6), "e": int64(5),
			"d": int64(4), "c": int64(3), "b": int64(2), "a": int64(1),
		}, `{"a": 1, "b": 2, "c": 3, "d": 4, "e": 5, "f": 6, "g": 7, "h": 8, "i": 9, "j": 10, "k": 11, "l": 12}`},
		{"nested", map[string]any{
			"users": []any{
				map[string]any{"name": "Ada"},
			},
		}, `{"users": [{"name": "Ada"}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DynamicInspect(Dynamic{Inner: tc.in}); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestDynamicTypeNameVocabulary pins the words a DecodeError's `got` field uses.
//
// The vocabulary is a Nomi-observable API:
// a program may compare `e.got` against a literal, so widening or renaming a
// word here is a breaking change and not a wording preference. Both integer
// widths and both float widths are present because the switch groups them, and a
// grouping that lost a case would still answer for the representative.
func TestDynamicTypeNameVocabulary(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{nil, "Null"},
		{true, "Bool"},
		{int64(1), "Int"},
		{int(1), "Int"},
		{uint(1), "Int"},
		{float64(1.0), "Float"},
		{float32(1.0), "Float"},
		{"x", "String"},
		{[]any{}, "List"},
		{map[string]any{}, "Dict"},
	}
	for _, tc := range cases {
		if got := dynamicTypeName(tc.in); got != tc.want {
			t.Errorf("dynamicTypeName(%#v) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// The fallback carries the real Go type, deliberately: `<*rt.Json>` in a
	// decode error is worth more to an embedder than "Unknown". Asserted because
	// it is the one arm a reader would be tempted to simplify.
	if got := dynamicTypeName(struct{ X int }{}); got != "<struct { X int }>" {
		t.Errorf("unknown Go type rendered %q; the fallback must name the real type", got)
	}
}

// TestDynamicNumericBoundaries pins the two range rules whose correct form is
// not the obvious one.
//
// These are the arms where a plausible simplification produces a wrong answer
// rather than a refusal, so they get their own test.
//
//	float64(2^63)   math.MaxInt64 has no exact float64 and rounds up to 2^63, so
//	                `n > math.MaxInt64` is false here and the int64 cast
//	                overflows. `>= float64(1<<63)` is what rejects it.
//	uint64 > MaxInt64  the top bit becomes the sign bit under an int64 cast, so
//	                an unguarded conversion answers a negative number for a
//	                positive input.
//
// Each has its in-range neighbour beside it, because a guard that rejects
// everything passes a test that only checks rejection.
func TestDynamicNumericBoundaries(t *testing.T) {
	if got := DynamicAsInt(Dynamic{Inner: float64(1 << 63)}); got.Tag != TagErr {
		t.Errorf("float64(2^63) produced Ok(%d); it overflows int64", got.Ok)
	} else if got.Err.Got != "Float (out of int64 range)" {
		t.Errorf("float64(2^63) got %q", got.Err.Got)
	}
	if got := DynamicAsInt(Dynamic{Inner: float64(1 << 62)}); got.Tag != TagOk || got.Ok != 1<<62 {
		t.Errorf("float64(2^62) is in range; got %+v", got)
	}
	if got := DynamicAsInt(Dynamic{Inner: uint64(math.MaxInt64) + 1}); got.Tag != TagErr {
		t.Errorf("uint64(2^63) produced Ok(%d); the top bit is int64's sign bit", got.Ok)
	} else if got.Err.Got != "Uint64 (out of int64 range)" {
		t.Errorf("uint64(2^63) got %q", got.Err.Got)
	}
	if got := DynamicAsInt(Dynamic{Inner: uint64(math.MaxInt64)}); got.Tag != TagOk || got.Ok != math.MaxInt64 {
		t.Errorf("uint64(MaxInt64) is in range; got %+v", got)
	}
	// A fractional float is refused, not truncated. The reason for accepting
	// float64 at all is JSON's `36` arriving as 36.0, and accepting 3.14 too
	// would lose data silently.
	if got := DynamicAsInt(Dynamic{Inner: 3.14}); got.Tag != TagErr ||
		got.Err.Got != "Float with fractional part" {
		t.Errorf("3.14 must refuse as a fractional Float; got %+v", got)
	}
}

// TestDynamicPathSegmentTagsMatchDeclarationOrder pins the two tag numbers
// against the names, in the file that constructs them.
//
// A layout contract rather than a unit test: internal/irbuild's stdEnumSpec for
// `std/dynamic.PathSegment` names these variants in declaration order and the
// builder assigns 1 and 2 from that order, so a swap here would make a
// program read a `Field` as an `Index` — a wrong answer with no build error
// anywhere. The constants exist so that contract has one statement; this asserts
// the statement is the one std wrote.
func TestDynamicPathSegmentTagsMatchDeclarationOrder(t *testing.T) {
	if TagPathSegmentField != 1 || TagPathSegmentIndex != 2 {
		t.Fatalf("PathSegment tags are Field=%d Index=%d; std/dynamic.nomi declares "+
			"`Field String` first and `Index Int` second, and the IR builder numbers from "+
			"that order", TagPathSegmentField, TagPathSegmentIndex)
	}
	// And the constructors put the payload in the field that belongs to the tag.
	// Two payload fields of different Go types share no slot, so a crossed pair
	// would be silently readable as an empty value of the other variant.
	if f := DynamicField(Dynamic{Inner: int64(1)}, "k"); f.Tag != TagErr ||
		f.Err.Path == nil || f.Err.Path.Head.Tag != TagPathSegmentField ||
		f.Err.Path.Head.Field != "k" {
		t.Fatalf("a field navigation must carry Field(%q); got %+v", "k", f.Err.Path)
	}
	if f := DynamicIndex(Dynamic{Inner: int64(1)}, 7); f.Tag != TagErr ||
		f.Err.Path == nil || f.Err.Path.Head.Tag != TagPathSegmentIndex ||
		f.Err.Path.Head.Index != 7 {
		t.Fatalf("an index navigation must carry Index(7); got %+v", f.Err.Path)
	}
}

// TestDynamicAsDictKeysAreSorted asserts the order, not just the contents.
//
// Separate from the rendering pin because it is a different mechanism: the
// rendering sorts at print time, and this sorts at insertion time so that
// `rt.MapEntries` — which reports insertion order — gives a stable walk. A
// dropped sort here is invisible to the rendering pin and shows up as a Nomi
// `Map` that renders differently between runs of one program.
//
// Twelve keys supplied in reverse, because Go's map iteration is randomized but
// not adversarial: with two or three keys an unsorted walk often comes out
// sorted by chance, and a flaky guard reads as a passing one.
func TestDynamicAsDictKeysAreSorted(t *testing.T) {
	raw := map[string]any{}
	want := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l"}
	for i := len(want) - 1; i >= 0; i-- {
		raw[want[i]] = int64(i)
	}
	got := DynamicAsDict(Dynamic{Inner: raw})
	if got.Tag != TagOk {
		t.Fatalf("as_dict refused a map[string]any: %+v", got.Err)
	}
	entries := MapEntries(got.Ok)
	if len(entries) != len(want) {
		t.Fatalf("got %d entries, want %d", len(entries), len(want))
	}
	for i, e := range entries {
		if e.Key != want[i] {
			t.Fatalf("entry %d is %q, want %q — insertion order is not sorted, so "+
				"MapEntries gives an unstable walk", i, e.Key, want[i])
		}
	}
}

// TestDynamicAsListPreservesOrderAndLen pins the cons-cell invariant.
//
// `Len` is not decoration: rt.List caches it so a whole list knows its own
// length in O(1), and a list built front-to-back with Cons would carry the wrong
// Len at every cell while still traversing correctly — so a length-only check
// passes and a Nomi `Iter.count` answers wrong.
func TestDynamicAsListPreservesOrderAndLen(t *testing.T) {
	got := DynamicAsList(Dynamic{Inner: []any{"a", int64(2), true}})
	if got.Tag != TagOk {
		t.Fatalf("as_list refused a []any: %+v", got.Err)
	}
	if got.Ok == nil || got.Ok.Len != 3 {
		t.Fatalf("head cell Len is wrong: %+v", got.Ok)
	}
	var seen []string
	for node := got.Ok; node != nil; node = node.Tail {
		seen = append(seen, DynamicInspect(node.Head))
	}
	if len(seen) != 3 || seen[0] != `"a"` || seen[1] != "2" || seen[2] != "true" {
		t.Fatalf("elements out of order or unwrapped: %v", seen)
	}
	// Every cell's Len counts to the end, which is what makes the cache sound
	// under tail sharing.
	if got.Ok.Tail.Len != 2 || got.Ok.Tail.Tail.Len != 1 {
		t.Fatalf("Len is not the distance to the end at every cell: %+v", got.Ok)
	}
}

// TestJsonToDynamicIsJsonToAnyWrapped asserts the bridge is a wrapper and not a
// second walk.
//
// The claim is that `JsonToDynamic` adds the Nomi type and nothing else, so the
// tree conversion has one implementation. Asserted by rendering both sides:
// anything JsonToDynamic did differently from JsonToAny would show as a
// different rendering.
func TestJsonToDynamicIsJsonToAnyWrapped(t *testing.T) {
	jv := Json{Tag: TagJsonArr, Arr: Cons(
		Json{Tag: TagJsonString, String: "x"},
		Cons(Json{Tag: TagJsonInt, Int: 7}, nil))}
	if got, want := DynamicInspect(JsonToDynamic(jv)), dynamicRender(JsonToAny(jv)); got != want {
		t.Fatalf("JsonToDynamic rendered %q but JsonToAny rendered %q; the bridge is "+
			"supposed to add the type and nothing else", got, want)
	}
	if got := DynamicInspect(JsonToDynamic(Json{Tag: TagJsonString, String: "hi"})); got != `"hi"` {
		t.Fatalf("a Json string projected to %q", got)
	}
}
