package irbuild

import "testing"

// `Iter.sum` is a std generic body that calls `T.empty_sum()`, which takes
// no value of T, and adds with `+` at T. Its instances reach three places a
// std instance had not reached before: a std type's receiverless impl that
// iter.nomi's scope does not name (Decimal, Duration, Bytes), and a
// program type's `impl Summable` and `impl Add` declared in the calling file
// or in a sibling file.
func TestIterSum_AtStdTypesTheModuleDoesNotName(t *testing.T) {
	verifyLambdaProgram(t, `import {
    std/duration.Duration
    std/io
}

fn main() {
    io.print(Iter.sum([1.25d, 2d]))
    io.print(Iter.sum([Duration.seconds(1), Duration.seconds(2)]) == Duration.seconds(3))
    io.print(Bytes.length(Iter.sum([Bytes.from_list([]), Bytes.from_list([])])))
}
`, "3.25\nTrue\n0\n")
}

func TestIterSum_AtProgramTypes(t *testing.T) {
	verifyLambdaProgram(t, `import {
    money.{Coins, Point}
    std/io
}

type Cents Int

impl Add<Cents, Cents> for Cents {
    fn add(Cents(a), Cents(b)): Cents {
        Cents(a + b)
    }
}

impl Summable for Cents {
    fn empty_sum(): Cents {
        Cents(0)
    }
}

fn main() {
    Cents(c) = Iter.sum([Cents(2), Cents(3)])
    io.print(c)
    Coins(n) = Iter.sum([Coins(4), Coins(5)])
    io.print(n)
    io.print(Iter.sum([Point{x: 1}, Point{x: 2}]).x)
    none: List<Point> = []
    io.print(Iter.sum(none).x)
}
`, "5\n9\n3\n0\n", map[string]string{"money.nomi": `pub type Coins Int

impl Add<Coins, Coins> for Coins {
    fn add(Coins(a), Coins(b)): Coins {
        Coins(a + b)
    }
}

impl Summable for Coins {
    fn empty_sum(): Coins {
        Coins(0)
    }
}

pub struct Point {
    x: Int
}

impl Add<Point, Point> for Point {
    fn add(lhs: Point, rhs: Point): Point {
        Point{x: lhs.x + rhs.x}
    }
}

impl Summable for Point {
    fn empty_sum(): Point {
        Point{x: 0}
    }
}
`})
}

// A sum of empty lists is checked at `Iter<List<?>>`, the element of the
// inner lists left open. No element is built or observed, so the hole is
// filled as an empty list's is.
func TestIterSum_OfEmptyLists(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

fn main() {
    io.print(Iter.sum([[]]) == [])
    io.print(Iter.sum([[], []]) == [])
    io.print(Iter.sum([[[]], []]) == [[]])
    io.print(Iter.count(Iter.sum([[]])))
}
`, "True\nTrue\nTrue\n0\n")
}
