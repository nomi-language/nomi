package vmhost_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// An impl function's parameter may destructure its argument, as a file-level
// function's may: a distinct (`Balance(n)`), a struct (`Point{x, y}`) and a
// tuple (`#(a, b): #(Int, Int)`), in an inherent block, an interface impl, a
// generic block, and an interface default. Each used to be refused as
// "destructuring parameter", so every call to one stopped the program with
// "this call to `Balance.cents` is not supported yet".
func TestImplDestructuringParams_Run(t *testing.T) {
	got := runSourceOutput(t, `import std/io

type Balance Int

impl Balance {
    pub fn cents(Balance(n)) -> Int { n * 100 }
    pub fn add(Balance(a), Balance(b)) -> Balance { Balance(a + b) }
}

type Slug String

impl Equatable for Slug {
    fn equal?(Slug(a), Slug(b)) -> Bool { a == b }
}

struct Point {
    x: Int
    y: Int
}

impl Point {
    fn sum(Point{x, y}) -> Int { x + y }
    fn swap(#(a, b): #(Int, Int)) -> #(Int, Int) { #(b, a) }
    fn shifted(Point{x, y} as p, #(dx, dy): #(Int, Int)) -> String {
        "${Point.sum(p)} -> ${x + dx},${y + dy}"
    }
}

interface Named {
    fn name(value: self) -> String
    fn wrapped(value: self, #(open, close): #(String, String)) -> String {
        "${open}${Named.name(value)}${close}"
    }
    fn plus(value: self, Point{x, y}) -> String { "${Named.name(value)}+${x + y}" }
}

impl Named for Point {
    fn name(Point{x, y}) -> String { "p${x}${y}" }
}

struct Box<T> {
    value: T
    count: Int
}

impl Box<T> {
    fn get(Box{value}: Box<T>) -> T { value }
}

impl Display for Box<T> where T: Display {
    fn to_string(Box{value, count}: Box<T>) -> String { "${T.to_string(value)} x${count}" }
}

interface Pick<T> {
    fn pick(value: self, pair: #(T, T)) -> T
}

impl Pick<String> for Point {
    fn pick(Point{x, y}, #(a, b): #(String, String)) -> String {
        if x > y { a } else { b }
    }
}

fn main() {
    io.print(Balance.cents(Balance.add(Balance(1), Balance(2))))
    io.print(Slug("a") == Slug("a"))
    io.print(Slug("a") == Slug("b"))
    io.print(Point.sum(Point{x: 1, y: 2}))
    io.print(Point.swap(#(1, 2)))
    io.print(Point.shifted(Point{x: 1, y: 2}, #(10, 20)))
    io.print(Named.wrapped(Point{x: 1, y: 2}, #("<", ">")))
    io.print(Point.plus(Point{x: 1, y: 2}, Point{x: 3, y: 4}))
    named: List<Named> = [Point{x: 5, y: 6}]
    io.print(named |> Iter.map(fn n -> Named.wrapped(n, #("[", "]"))) |> Iter.to_list())
    io.print(Box.get(Box{value: "v", count: 1}))
    io.print(Display.to_string(Box{value: 7, count: 3}))
    io.print(Pick.pick(Point{x: 2, y: 1}, #("first", "second")))
}
`)
	want := `300
True
False
3
#(2, 1)
3 -> 11,22
<p12>
p12+7
[[p56]]
v
7 x3
first
`
	if got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}

// The same functions declared in one file and called from another, with an
// interface default that destructures inherited by an impl in the calling
// file.
func TestImplDestructuringParams_RunFromAnotherFile(t *testing.T) {
	entry, name, files, manifest, err := vmhost.SplitMultiFile(`// FILE: money.nomi
pub type Balance Int

impl Balance {
    pub fn cents(Balance(n)) -> Int { n * 100 }
}

pub struct Pair {
    left: Int
    right: Int
}

impl Pair {
    pub fn total(Pair{left, right}) -> Int { left + right }
}

pub interface Shape {
    fn area(shape: self) -> Int
    fn describe(shape: self, #(label, unit): #(String, String)) -> String {
        "${label} ${Shape.area(shape)} ${unit}"
    }
}

impl Shape for Pair {
    fn area(Pair{left, right}) -> Int { left * right }
}

// FILE: main.nomi
import {
    std/io
    money.Balance
    money.Pair
    money.Shape
}

struct Square {
    side: Int
}

impl Shape for Square {
    fn area(Square{side}) -> Int { side * side }
}

fn main() {
    io.print(Balance.cents(Balance(3)))
    io.print(Pair.total(Pair{left: 3, right: 4}))
    io.print(Shape.describe(Pair{left: 3, right: 4}, #("area", "m2")))
    io.print(Shape.describe(Square{side: 3}, #("square", "m2")))
}
`)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	p, err := vmhost.LoadSource(name, entry, vmhost.WithVirtualFiles(files), vmhost.WithVirtualManifest(manifest))
	if err != nil {
		t.Fatalf("the front end rejects the program: %v", err)
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatalf("run: %v\noutput so far:\n%s", err, out.String())
	}
	want := `300
7
area 12 m2
square 9 m2
`
	if got := out.String(); got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}
