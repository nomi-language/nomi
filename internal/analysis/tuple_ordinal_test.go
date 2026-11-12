package analysis

import "testing"

// A tuple and a named tuple read their items by ordinal name, in a field
// access, an accessor and a capture alike; a struct's `first` is its own
// field.
func TestTupleOrdinal_Reads(t *testing.T) {
	_, errs := checkSource(`type Pair (Int, String)

struct Holder
  first: String
end

fn apply<A, B>(x: A, f: (A) -> B) -> B
  f(x)
end

fn main()
  t = (1, "a", True)
  p = Pair(2, "b")
  n: Int = t.first + p.first
  s: String = t.second ++ p.second ++ Holder{first: "c"}.first
  b: Bool = t.third
  xs: String = apply(p, .second)
  ys: Int = apply(t, &(&1.first * 2))
  z: Int = ((1, 2), 3).first.second
  _ = n
  _ = s
  _ = b
  _ = xs
  _ = ys
  _ = z
end
`)
	expectNoErrors(t, errs)
}

// An ordinal past a tuple's last item names the last; a name that is no
// ordinal is no field of a tuple.
func TestTupleOrdinal_PastTheLastItem(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{"  _ = (1, 2).third", "(Int, Int) has 2 items, so it has no `third`; its last is `second`"},
		{"  _ = Pair(1, \"x\").fifth", "(Int, String) has 2 items, so it has no `fifth`; its last is `second`"},
		{"  _ = apply((1, 2), .third)", "(Int, Int) has 2 items, so it has no `third`; its last is `second`"},
		{"  _ = (1, 2).zeroth", "(Int, Int) has no field 'zeroth'"},
	} {
		_, errs := checkSource("type Pair (Int, String)\n\nfn apply<A, B>(x: A, f: (A) -> B) -> B\n  f(x)\nend\n\nfn main()\n" + tc.body + "\nend\n")
		expectError(t, errs, tc.want)
	}
}

// A named tuple's items are its representation: outside the file that
// declares it opaque, reading one is an error.
func TestTupleOrdinal_OpaqueNamedTuple(t *testing.T) {
	modules := map[string]string{"pairs": "pub opaque type Pair (Int, String)\n\npub fn make() -> Pair\n  Pair(1, \"x\")\nend\n"}
	_, errs := checkSourceWithModules(t, "import pairs\n\nfn main()\n  _ = pairs.make().first\nend\n", modules)
	expectError(t, errs, "cannot read item 'first' of opaque type 'Pair' outside its defining module")
}
