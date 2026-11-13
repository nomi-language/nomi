package irbuild

import (
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/rt"
)

// A std collection converted to wider type arguments is the same value
// (spec §5 *Covariant conversion*): converting a list of 10,000
// elements enters no function but the one converting it, once, reads no
// element, and answers the very list it was given. A per-element
// conversion would enter an adapter per element and build a new list.
func TestRelabel_ConvertsNoElement(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `struct Pt
    x: Int
end

interface Named
    fn name(v: self) -> String
end

impl Named for Pt
    fn name(_p: Pt) -> String
        "pt"
    end
end

fn ints(xs: List<Int>) -> List<Display>
    xs
end

fn pts(xs: List<Pt>) -> List<Named>
    xs
end

fn funcs(fs: List<(Int) -> Int>) -> List<(Int) -> Display>
    fs
end

fn nested(xs: List<List<Int>>) -> List<List<Display>>
    xs
end

fn values(m: Map<String, Int>) -> Map<String, Display>
    m
end

fn keep(x: Int) -> Display
    x
end

fn mapped(xs: List<Int>) -> List<Display>
    Iter.map(xs, keep) |> Iter.to_list()
end

fn main()
    dbg Iter.count(mapped([1]))
    dbg Iter.count(ints([1]))
    dbg Iter.count(pts([Pt{x: 1}]))
    dbg Iter.count(funcs([fn n -> n]))
    dbg Iter.count(nested([[1]]))
    dbg Map.size(values({"a" => 1}))
end
`
	path := filepath.Join(t.TempDir(), "main.nomi")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatalf("the front end rejects the fixture, so nothing below is tested: %v", err)
	}
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	var entry *ir.Module
	for _, m := range res.IR {
		if m.Name() == path {
			entry = m
		}
	}
	if entry == nil {
		t.Fatal("no module for the program")
	}
	const n = 10_000
	list := func(elem func(i int) any) *rt.List[any] {
		var out *rt.List[any]
		for i := n - 1; i >= 0; i-- {
			out = rt.ConsCell[any, rt.List[any]](elem(i), out)
		}
		return out
	}
	pt := rt.NewStructDesc("main.Pt", []rt.FieldSpec{{Name: "x", Type: rt.SlotInt}})
	entries := make([]rt.MapEntry[any, any], n)
	for i := range entries {
		entries[i] = rt.MapEntry[any, any]{Key: string(rune('a'+i%26)) + strconv.Itoa(i), Val: int64(i)}
	}
	m := rt.MapOf(rt.Hash, rt.Equal, entries)
	ints := list(func(i int) any { return int64(i) })
	cases := []struct {
		fn      string
		arg     any
		entries uint64
	}{
		{"ints", ints, 1},
		{"pts", list(func(i int) any { return pt.Make(int64(i)) }), 1},
		{"funcs", list(func(int) any { return nil }), 1},
		{"nested", list(func(i int) any { return rt.ConsCell[any, rt.List[any]](int64(i), nil) }), 1},
		{"values", m, 1},
		// An explicit conversion, for contrast: its function runs once per
		// element, and its list is a new one.
		{"mapped", ints, n + 1},
	}
	all := res.IRModules()
	for _, c := range cases {
		mach := vm.NewProgram(entry, res.IRModules(), io.Discard).WithEntryCounts()
		got, err := mach.Run(c.fn, c.arg)
		if err != nil {
			t.Fatalf("%s: %v", c.fn, err)
		}
		if same := sameValue(got, c.arg); same != (c.entries == 1) {
			t.Errorf("%s answers the value it was given: %v, want %v", c.fn, same, c.entries == 1)
		}
		var entries uint64
		for _, mod := range all {
			for _, f := range mod.Funcs() {
				entries += mach.Entries(f)
			}
		}
		if entries != c.entries {
			t.Errorf("%s over %d elements entered %d function bodies, want %d", c.fn, n, entries, c.entries)
		}
	}
}

// sameValue reports whether a and b are one reference.
func sameValue(a, b any) bool {
	switch x := a.(type) {
	case *rt.List[any]:
		y, ok := b.(*rt.List[any])
		return ok && x == y
	}
	// A Map is a value over a shared trie: one that was not rebuilt holds
	// the same entries.
	return rt.Equal(a, b)
}
