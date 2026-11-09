package vmhost_test

import "testing"

// A tuple or anonymous record held as std's Hashable, Equatable or Display
// runs as the same value does held as itself: its hash through the
// interface is the derive mix a direct `Hashable.hash` answers, two equal
// tuples compare equal, and it renders as `"${t}"` renders it, nested in a
// collection of interface values too.
func TestStructuralExistentials_HashCompareAndRenderAsTheValueItself(t *testing.T) {
	got := runSourceOutput(t, `import std::io

struct P {
    a: Int
}

derive Equatable for P
derive Hashable for P

fn hh(h: Hashable) -> Int {
    Hashable.hash(h)
}

fn eq(a: Equatable, b: Equatable) -> Bool {
    a == b
}

fn show(d: Display) -> String {
    "${d}"
}

fn main() {
    io.print(hh((1, 2)) == Hashable.hash((1, 2)))
    io.print(hh((1, 2)))
    io.print(hh((P{a: 1}, "x")) == Hashable.hash((P{a: 1}, "x")))
    io.print(hh((1, (2, 3))) == Hashable.hash((1, (2, 3))))
    io.print(hh(.{a: 1, b: 2}) == Hashable.hash(.{a: 1, b: 2}))
    io.print(eq((1, 2), (1, 2)))
    io.print(eq((1, 2), (1, 3)))
    io.print(eq((1, 2), 3))
    io.print(eq(.{a: 1}, .{a: 1}))
    io.print(show((1, "a")))
    io.print(show(.{a: 1, b: (2.5, "z")}))
    xs: List<Hashable> = [(1, 2), 3]
    io.print(xs |> Iter.map(fn x -> Hashable.hash(x)) |> Iter.to_list())
    ds: List<Display> = [(1, "x"), .{b: 2.5}, 3]
    io.print(ds |> Iter.map(fn d -> "${d}") |> Iter.to_list())
    m: Map<Hashable, Int> = %{(1, 2) => 1, 3 => 4}
    io.print(Map.size(m))
}
`)
	want := `True
33
True
True
True
True
False
False
True
(1, a)
.{a: 1, b: (2.5, z)}
[33, 3]
[(1, x), .{b: 2.5}, 3]
2
`
	if got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}
