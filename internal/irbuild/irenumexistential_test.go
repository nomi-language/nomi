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
	const src = `import std::io

interface Show
    fn show(value: self) -> String
end

enum Coin
    Penny
    Dime
end

enum Note
    Paper Int
    Coin Coin
end

enum Opt<T>
    Has T
    Nothing
end

struct Holder
    s: Show
end

impl Show for Coin
    fn show(c: Coin) -> String
        case c
            .Penny -> "penny"
            .Dime -> "dime"
        end
    end
end

impl Show for Note
    fn show(n: Note) -> String
        case n
            .Paper(v) -> "paper ${v}"
            .Coin(c) -> "coin ${Show.show(c)}"
        end
    end
end

impl Show for Opt<Int>
    fn show(o: Opt<Int>) -> String
        case o
            .Has(v) -> "has ${v}"
            .Nothing -> "nothing"
        end
    end
end

fn shown(s: Show) -> String
    "<" + Show.show(s) + ">"
end

fn made() -> Show
    Note.Paper(5)
end

fn main()
    s: Show = Coin.Dime
    io.print(Show.show(s))
    io.print(shown(Coin.Penny))
    nothing: Opt<Int> = Opt.Nothing
    xs: List<Show> = [Coin.Penny, Note.Coin(Coin.Dime), Opt.Has(3), nothing]
    io.print(xs |> Iter.map(fn x -> Show.show(x)) |> String.join(", "))
    m: Maybe<Show> = Some(Coin.Dime)
    case m
        Some(v) -> io.print(shown(v))
        None -> io.print("none")
    end
    io.print(Show.show(made()))
    h = Holder{s: Note.Coin(Coin.Penny)}
    io.print(Show.show(h.s))
end
`
	irRunSource(t, src, "dime\n<penny>\npenny, coin dime, has 3, nothing\n<dime>\npaper 5\ncoin penny\n")
}
