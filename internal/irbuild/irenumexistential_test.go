package irbuild

import "testing"

// A user enum implementing a program interface is held as that interface
// wherever one stands: a binding, a parameter, a list element, a Maybe's
// payload, a function's result and a struct field. A dispatch selects the
// enum's impl by the enum record's own type, as it does a struct's; an
// instance of a generic enum is held the same way.
func TestIREnumHeldAsAnInterface_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

interface Show {
    fn show(value: self) -> String
}

enum Coin {
    Penny
    Dime
}

enum Note {
    Paper Int
    Coin Coin
}

enum Opt<T> {
    Has T
    Nothing
}

struct Holder {
    s: Show
}

impl Show for Coin {
    fn show(c: Coin) -> String {
        case c {
            .Penny -> "penny"
            .Dime -> "dime"
        }
    }
}

impl Show for Note {
    fn show(n: Note) -> String {
        case n {
            .Paper(v) -> "paper ${v}"
            .Coin(c) -> "coin ${Show.show(c)}"
        }
    }
}

impl Show for Opt<Int> {
    fn show(o: Opt<Int>) -> String {
        case o {
            .Has(v) -> "has ${v}"
            .Nothing -> "nothing"
        }
    }
}

fn shown(s: Show) -> String {
    "<" + Show.show(s) + ">"
}

fn made() -> Show {
    Note.Paper(5)
}

fn main() {
    s: Show = Coin.Dime
    io.print(Show.show(s))
    io.print(shown(Coin.Penny))
    nothing: Opt<Int> = Opt.Nothing
    xs: List<Show> = [Coin.Penny, Note.Coin(Coin.Dime), Opt.Has(3), nothing]
    io.print(xs |> Iter.map(fn x -> Show.show(x)) |> String.join(", "))
    m: Maybe<Show> = Some(Coin.Dime)
    case m {
        Some(v) -> io.print(shown(v))
        None -> io.print("none")
    }
    io.print(Show.show(made()))
    h = #Holder{s: Note.Coin(Coin.Penny)}
    io.print(Show.show(h.s))
}
`
	irRunSource(t, src, "dime\n<penny>\npenny, coin dime, has 3, nothing\n<dime>\npaper 5\ncoin penny\n")
}
