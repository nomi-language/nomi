package irbuild

import "testing"

// An ordering operator on a bounded T inside a std generic instance reaches
// the PROGRAM's `impl Comparable` for the type the instance is built at.
// `Iter.min` and its siblings compare with `<` and `>` (std/iter.nomi), and
// each instance below is at a type std cannot see: a derived enum and a
// hand-written impl declared in main.nomi, and a derived enum, a hand-written
// impl and a derived generic struct declared in a sibling file, which the
// caller holds as mirrors. The instance's own impl index is empty for all of
// them, so the operator used to decline the instance ("the comparison
// operator `<`"); rankedCompare now asks callerImplPlan, as an interface call
// `Comparable.compare(a, b)` in the same body does.
func TestStdInstCompare_OrderingOperatorsReachTheProgramsImpl(t *testing.T) {
	ranks := map[string]string{"ranks.nomi": `pub enum Suit {
    Clubs
    Spades
}

derive Comparable for Suit

pub struct Hand {
    size: Int
}

impl Comparable for Hand {
    fn compare(x: Hand, y: Hand) -> Ordering {
        Int.compare(x.size, y.size)
    }
}

pub struct Pair<T> {
    a: T
    b: T
}

derive Comparable for Pair
`}
	src := `import std/io
import ranks.Suit
import ranks.Hand
import ranks.Pair

enum Level {
    Low
    Mid
    High
}

derive Comparable for Level

struct Card {
    rank: Int
    label: String
}

impl Comparable for Card {
    fn compare(a: Card, b: Card) -> Ordering {
        Int.compare(a.rank, b.rank)
    }
}

fn lesser<T>(a: T, b: T) -> T where T: Comparable {
    if a <= b { a } else { b }
}

fn main() {
    io.inspect(Iter.min([Level.Mid, Level.Low, Level.High]))
    io.inspect(Iter.max([Level.Mid, Level.Low, Level.High]))
    io.inspect(Iter.min([#Card{rank: 2, label: "a"}, #Card{rank: 1, label: "b"}, #Card{rank: 1, label: "c"}]))
    io.inspect(Iter.max([#Card{rank: 2, label: "a"}, #Card{rank: 1, label: "b"}, #Card{rank: 2, label: "c"}]))
    io.inspect(Iter.min_by([#Card{rank: 1, label: "x"}, #Card{rank: 0, label: "y"}], fn c -> if c.rank == 0 { Level.High } else { Level.Low }))
    io.inspect(Iter.max_by(["b", "a"], fn s -> if s == "a" { Level.High } else { Level.Low }))
    io.inspect(Iter.min([Suit.Spades, Suit.Clubs]))
    io.inspect(Iter.max([#Hand{size: 3}, #Hand{size: 5}]))
    io.inspect(Iter.min([#Pair{a: 2, b: 1}, #Pair{a: 1, b: 9}]))
    io.inspect(lesser(Suit.Spades, Suit.Clubs))
    io.inspect(lesser(Level.High, Level.Mid))
}
`
	verifyLambdaProgram(t, src, "Some(Low)\nSome(High)\n"+
		"Some(#Card{rank: 1, label: \"b\"})\nSome(#Card{rank: 2, label: \"a\"})\n"+
		"Some(#Card{rank: 1, label: \"x\"})\nSome(\"a\")\n"+
		"Some(Clubs)\nSome(#Hand{size: 5})\nSome(#Pair{a: 1, b: 9})\n"+
		"Clubs\nMid\n", ranks)
}
