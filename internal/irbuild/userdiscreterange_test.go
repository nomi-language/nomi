package irbuild

import "testing"

// A Range over a user distinct type walks and counts by the program's own
// `impl Discrete` and `Comparable`: as a pipeline's source, zip's right
// side, a declared `Iter<T>` argument, `Iter.known_count` and `Iter.count`.
// An impl that leaves `steps_between` to the interface's default has no
// count (`None`) and still walks.
func TestIRRangeOverUserDiscrete_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

type Id Int

derive Equatable for Id
derive Comparable for Id

impl Discrete for Id {
    fn next(Id(n)): Maybe<Id> {
        Some(Id(n + 1))
    }

    fn steps_between(Id(a), Id(b)): Maybe<Int> {
        Some(b - a)
    }
}

type Lap Int

derive Equatable for Lap
derive Comparable for Lap

impl Discrete for Lap {
    fn next(Lap(n)): Maybe<Lap> {
        if n >= 5 { None } else { Some(Lap(n + 1)) }
    }
}

fn val(Id(n)): Int {
    n
}

fn total(xs: Iter<Id>): Int {
    Iter.count(xs)
}

fn laps(xs: Iter<Lap>): (Int, Maybe<Int>) {
    (Iter.count(xs), Iter.known_count(xs))
}

fn main() {
    io.inspect(Id(1)..Id(4) |> Iter.map(|i| val(i)) |> Iter.to_list())
    io.inspect(Iter.zip([1, 2], Id(1)..Id(4)) |> Iter.count())
    io.inspect(Iter.known_count(Id(1)..=Id(4)))
    io.inspect(Iter.count(Id(1)..=Id(4)))
    io.inspect(total(Id(2)..Id(4)))
    io.inspect(Lap(1)..Lap(9) |> Iter.to_list())
    io.inspect(Lap(1)..Lap(3) |> Iter.count())
    io.inspect(Iter.known_count(Lap(1)..Lap(3)))
    io.inspect(laps(Lap(2)..=Lap(4)))
}
`
	const want = "[1, 2, 3]\n" +
		"2\n" +
		"Some(4)\n" +
		"4\n" +
		"2\n" +
		"[Lap(1), Lap(2), Lap(3), Lap(4), Lap(5)]\n" +
		"2\n" +
		"None\n" +
		"(3, None)\n"
	irRunSource(t, src, want)
}

// A Range over a type another file declares walks and counts by that
// file's `impl Discrete`.
func TestIRRangeOverSiblingDiscrete_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const ids = `pub type Id Int

derive Equatable for Id
derive Comparable for Id
derive Debug for Id

impl Discrete for Id {
    fn next(Id(n)): Maybe<Id> {
        Some(Id(n + 1))
    }

    fn steps_between(Id(a), Id(b)): Maybe<Int> {
        Some(b - a)
    }
}
`
	verifyLambdaProgram(t, `import {
    std/io
    ids.Id
}

fn total(xs: Iter<Id>): Int {
    Iter.count(xs)
}

fn main() {
    io.inspect(Id(1)..Id(4) |> Iter.to_list())
    io.inspect(Iter.known_count(Id(1)..=Id(4)))
    io.inspect(total(Id(2)..Id(4)))
}
`, "[Id(1), Id(2), Id(3)]\nSome(4)\n2\n", map[string]string{"ids.nomi": ids})
}
