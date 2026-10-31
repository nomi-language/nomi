package irbuild

import "testing"

// A receiver implementing one generic interface at several type arguments:
// each call form reaches the impl the front end chose (instimpl.go).
func TestInstImpl_EveryCallFormRunsTheChosenImpl(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	irRunSource(t, `import std/io

interface Holds<T> {
    fn holds(b: self, x: T) -> Bool

    fn holds_none(b: self, xs: List<T>) -> Bool {
        Iter.all(xs, fn x -> not Holds.holds(b, x))
    }
}

struct Box {
    n: Int
    s: String
}

impl Holds<Int> for Box {
    fn holds(b: Box, x: Int) -> Bool {
        b.n == x
    }
}

impl Holds<String> for Box {
    fn holds(b: Box, x: String) -> Bool {
        b.s == x
    }
}

fn via<C, T>(c: C, x: T) -> Bool where C: Holds<T> {
    C.holds(c, x)
}

fn main() {
    b = #Box{n: 1, s: "a"}
    io.print(Bool.to_string(Box.holds(b, 1)))
    io.print(Bool.to_string(Holds.holds(b, "b")))
    io.print(Bool.to_string(b |> Box.holds("a")))
    io.print(Bool.to_string(via(b, 2)))
    io.print(Bool.to_string(Box.holds_none(b, ["x"])))
    picks = Iter.filter([1, 2], &Holds.holds(b, &1)) |> Iter.to_list()
    io.print(Debug.inspect(picks))
}
`, "True\nFalse\nTrue\nFalse\nTrue\n[1]\n")
}

// A type argument only the result names is chosen by the expected type, for
// a call, a bound and an operator.
func TestInstImpl_TheExpectedTypeChoosesTheImpl(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	irRunSource(t, `import std/io

interface Make<T> {
    fn make(seed: self) -> T
}

struct Factory {
    n: Int
}

impl Make<Int> for Factory {
    fn make(seed: Factory) -> Int {
        seed.n
    }
}

impl Make<String> for Factory {
    fn make(seed: Factory) -> String {
        "f${seed.n}"
    }
}

fn made<C, T>(c: C) -> T where C: Make<T> {
    C.make(c)
}

type Day Int
type Days Int

fn day(d: Day) -> Int {
    Day(n) = d
    n
}

impl Add<Days, Day> for Day {
    fn add(lhs: Day, rhs: Days) -> Day {
        Days(n) = rhs
        Day(day(lhs) + n)
    }
}

impl Add<Days, Days> for Day {
    fn add(lhs: Day, rhs: Days) -> Days {
        Days(n) = rhs
        Days(day(lhs) + n + 100)
    }
}

fn main() {
    f = #Factory{n: 3}
    i: Int = Factory.make(f)
    s: String = Make.make(f)
    j: Int = made(f)
    later: Day = Day(1) + Days(2)
    gap: Days = Day(1) + Days(2)
    Days(g) = gap
    io.print(Int.to_string(i + j))
    io.print(s)
    io.print(Int.to_string(day(later)))
    io.print(Int.to_string(g))
}
`, "6\nf3\n3\n103\n")
}

// A value held as an instantiation of a generic interface dispatches to the
// impl at its own arguments, alone and in a list, and a type implementing
// two instantiations is held as either.
func TestInstImpl_AValueHeldAsAnInstantiationDispatches(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	irRunSource(t, `import std/io

interface Gets<T> {
    fn got(h: self) -> T
}

struct Bag {
    s: String
    n: Int
}

struct Cup {
    s: String
}

impl Gets<String> for Bag {
    fn got(h: Bag) -> String {
        h.s
    }
}

impl Gets<Int> for Bag {
    fn got(h: Bag) -> Int {
        h.n
    }
}

impl Gets<String> for Cup {
    fn got(h: Cup) -> String {
        "cup " + h.s
    }
}

fn text(h: Gets<String>) -> String {
    Gets.got(h)
}

fn number(h: Gets<Int>) -> Int {
    Gets.got(h)
}

fn main() {
    b = #Bag{s: "bag", n: 3}
    io.print(text(b))
    io.print(text(#Cup{s: "c"}))
    io.print(Int.to_string(number(b)))
    texts: List<Gets<String>> = [b, #Cup{s: "d"}]
    io.print(Iter.map(texts, fn h -> Gets.got(h)) |> String.join(", "))
    nums: List<Gets<Int>> = [b, b]
    io.print(Int.to_string(Iter.map(nums, fn h -> Gets.got(h)) |> Iter.sum()))
}
`, "bag\ncup c\n3\nbag, cup d\n6\n")
}

// A function with no receiver, whose only self is its result, at several
// type arguments: `Num.parse(3)`, `Parse.parse(5)` where a Num is expected,
// and a bound that solves both.
func TestInstImpl_AReceiverlessFunctionAtSeveralArguments(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	irRunSource(t, `import std/io

interface Parse<T> {
    fn parse(x: T) -> self
}

struct Num {
    n: Int
}

impl Parse<Int> for Num {
    fn parse(x: Int) -> Num {
        #Num{n: x}
    }
}

impl Parse<String> for Num {
    fn parse(x: String) -> Num {
        #Num{n: String.length(x)}
    }
}

fn parsed<N, T>(x: T) -> N where N: Parse<T> {
    N.parse(x)
}

fn main() {
    a = Num.parse(3)
    b = Num.parse("four")
    c: Num = Parse.parse(5)
    d: Num = parsed("xy")
    io.print(Int.to_string(a.n + b.n + c.n + d.n))
}
`, "14\n")
}
