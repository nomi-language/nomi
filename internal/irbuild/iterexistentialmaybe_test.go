package irbuild

import "testing"

// The Iter terminals that answer `Maybe<T>` (`find`, `first`, `last`, `get`)
// over a list whose element is an interface value: the Maybe is instantiated
// at the existential element, as `Map.get` and a hand-written `Maybe<Named>`
// already were, and the found value dispatches to its own impl. The stages
// beside them (`filter`, `any`, `all`, `count`, `map` with a capturing
// closure) are here so a change to the element's kind cannot break them
// unnoticed.
func TestIterMaybeTerminalsOverInterfaceElements(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	irRunSource(t, `import std::io

interface Named {
    fn name(n: self) -> String
}

struct A {
    s: String
}

impl Named for A {
    fn name(n: A) -> String {
        n.s
    }
}

struct B {
    k: Int
}

impl Named for B {
    fn name(n: B) -> String {
        "b${n.k}"
    }
}

fn shown(m: Maybe<Named>) -> String {
    m |> Maybe.map(&Named.name(&1)) |> Maybe.with_default("none")
}

fn main() {
    xs: List<Named> = [A{s: "x"}, B{k: 2}, A{s: "y"}]
    io.print(shown(xs |> Iter.find(fn n -> Named.name(n) == "b2")))
    io.print(shown(xs |> Iter.find(fn n -> Named.name(n) == "zz")))
    io.print(shown(xs |> Iter.first()))
    io.print(shown(xs |> Iter.last()))
    io.print(shown(xs |> Iter.get(1)))
    io.print(shown(xs |> Iter.get(9)))
    empty: List<Named> = []
    io.print(shown(empty |> Iter.first()))
    io.print(xs |> Iter.filter(fn n -> Named.name(n) != "x") |> Iter.map(&Named.name(&1)) |> String.join(","))
    io.print(Bool.to_string(xs |> Iter.any(fn n -> Named.name(n) == "y")))
    io.print(Bool.to_string(xs |> Iter.all(fn n -> Named.name(n) == "y")))
    io.print(Int.to_string(xs |> Iter.count()))
    p = "<"
    io.print(xs |> Iter.map(fn n -> p + Named.name(n)) |> String.join(""))
}
`, "b2\nnone\nx\ny\nb2\nnone\nnone\nb2,y\nTrue\nFalse\n3\n<x<b2<y\n")
}

// The same terminals over a list of functions that name a struct: the
// element is in the callable domain but not the retained one, so `Iter.get`
// declined here too (it was pinned in vmhost as
// gapIterGetOfAFunctionOverAStruct).
func TestIterMaybeTerminalsOverFunctionElements(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	irRunSource(t, `import std::io

struct Foo {
    n: Int
}

fn at(m: Maybe<(Foo) -> Foo>) -> Int {
    m |> Maybe.map(fn h -> h(Foo{n: 4}).n) |> Maybe.with_default(-1)
}

fn main() {
    hs: List<(Foo) -> Foo> = [fn f -> Foo{n: f.n + 1}, fn f -> Foo{n: f.n * 10}]
    io.print(Int.to_string(at(Iter.get(hs, 1))))
    io.print(Int.to_string(at(Iter.get(hs, 5))))
    io.print(Int.to_string(at(Iter.first(hs))))
    io.print(Int.to_string(at(Iter.last(hs))))
    io.print(Int.to_string(at(Iter.find(hs, fn h -> h(Foo{n: 1}).n == 2))))
}
`, "40\n-1\n5\n40\n5\n")
}
