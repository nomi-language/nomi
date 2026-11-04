package irbuild

import "testing"

// A value of a bounded type parameter passed where its bound's interface is
// expected runs as an interface value of the type each caller instantiates
// it at: a parameter, an annotated binding, a list element, a result and a
// struct field, for std's Display, Hashable and ToJson and a program's own
// interfaces, under one bound, several, a bound alias, an impl block's
// bound and a generic interface's.
func TestBoundedTypeParamAsInterfaceValue(t *testing.T) {
	verifyLambdaProgram(t, `import {
    std::io
    std::json.{Json, ToJson}
}

interface Shape {
    fn area(s: self) -> Int
}

interface Conv<U> {
    fn conv(v: self) -> U
}

struct Square {
    side: Int
}

impl Shape for Square {
    fn area(s: Square) -> Int {
        s.side * s.side
    }
}

impl Conv<Int> for String {
    fn conv(v: String) -> Int {
        String.length(v)
    }
}

struct Labeled {
    label: Display
}

struct Box<T> {
    item: T
}

typealias Both Display and Hashable

fn take(x: Display) -> String {
    "${x}"
}

fn total(shapes: List<Shape>) -> Int {
    shapes |> Iter.map(fn s -> Shape.area(s)) |> Iter.sum()
}

fn same(a: Hashable, b: Hashable) -> Bool {
    Hashable.hash(a) == Hashable.hash(b)
}

fn as_json(j: ToJson) -> String {
    Json.encode(ToJson.to_json(j))
}

fn use_int(c: Conv<Int>) -> Int {
    Conv.conv(c)
}

fn param<T>(v: T) -> String where T: Display {
    take(v)
}

fn binding<T>(v: T) -> String where T: Display {
    d: Display = v
    "${d}"
}

fn element<T>(v: T, w: T) -> Int where T: Shape {
    total([v, w])
}

fn returned<T>(v: T) -> Display where T: Display {
    v
}

fn field<T>(v: T) -> Labeled where T: Display {
    Labeled{label: v}
}

fn several<T>(v: T) -> String where T: Hashable and Display {
    "${same(v, v)} ${take(v)}"
}

fn aliased<T>(v: T) -> String where T: Both {
    "${same(v, v)} ${take(v)}"
}

fn encoded<T>(v: T) -> String where T: ToJson {
    as_json(v)
}

fn converted<T>(v: T) -> Int where T: Conv<Int> {
    use_int(v)
}

impl Display for Box<T> where T: Display {
    fn to_string(b: Box<T>) -> String {
        "box " + take(b.item)
    }
}

fn main() {
    io.print(param(3))
    io.print(param("s"))
    io.print(binding(2.5))
    io.print("${element(Square{side: 2}, Square{side: 3})}")
    io.print("${returned(7)}")
    io.print("${field(True).label}")
    io.print(several("k"))
    io.print(aliased(4))
    io.print(encoded([1, 2]))
    io.print("${converted("abcd")}")
    io.print(take(Box{item: 9}))
    io.print(take(Box{item: Box{item: "in"}}))
}
`, "3\ns\n2.5\n13\n7\nTrue\nTrue k\nTrue 4\n[1,2]\n4\nbox 9\nbox box in\n")
}
