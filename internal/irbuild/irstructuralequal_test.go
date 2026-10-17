package irbuild

import "testing"

// `Hashable.hash` over a record is the derive mix over its fields in name
// order, and `Equatable.equal?` over two tuples or two records is `==`,
// called directly and through a generic bound.
func TestIRStructuralHashAndEqual_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

fn same<T>(a: T, b: T) -> Bool where T: Equatable {
    Equatable.equal?(a, b)
}

fn h<T>(a: T) -> Int where T: Hashable {
    Hashable.hash(a)
}

fn main() {
    io.inspect(Hashable.hash({name: "r", n: 1}) == Hashable.hash({n: 1, name: "r"}))
    io.inspect(Hashable.hash({name: "r", n: 1}) == Hashable.hash({n: 2, name: "r"}))
    io.inspect(Hashable.hash({name: "r", n: 1}) == Hashable.hash(1) * 31 + Hashable.hash("r"))
    io.inspect(h({name: "r", n: 1}) == Hashable.hash({name: "r", n: 1}))
    io.inspect(same(#(1, 2), #(1, 2)))
    io.inspect(same(#(1, 2), #(1, 3)))
    io.inspect(same({a: 1}, {a: 1}))
    io.inspect(Equatable.equal?(#(1, "x"), #(1, "x")))
    io.inspect(Equatable.equal?({a: 1, b: #(2, 3)}, {a: 1, b: #(2, 4)}))
}
`
	irRunSource(t, src, "True\nFalse\nTrue\nTrue\nTrue\nFalse\nTrue\nTrue\nFalse\n")
}

// `Maybe.equal?` is `==` over a payload whose own impl decides (a struct
// with a derived Equatable) and over two instances an embedded value tells
// apart.
func TestIRMaybeEqualAsTheOperator_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

struct Inner {
    a: Int
}

derive Equatable for Inner

struct Circle {
    r: Int
}

enum Shape {
    embeds Circle
    Dot
}

derive Equatable for Circle
derive Equatable for Shape

fn main() {
    c = Circle{r: 1}
    s: Shape = Shape.Dot
    io.print(Maybe.equal?(Some(Inner{a: 1}), None))
    io.print(Maybe.equal?(Some(Inner{a: 1}), Some(Inner{a: 1})))
    io.print(Maybe.equal?(Some(c), Some(s)))
    io.print(Maybe.equal?(Some(c), Some(Shape.Circle{r: 1})))
    r: Result<Inner, String> = Ok(Inner{a: 2})
    io.print(Result.equal?(r, Ok(Inner{a: 2})))
}
`
	irRunSource(t, src, "False\nTrue\nFalse\nTrue\nTrue\n")
}
