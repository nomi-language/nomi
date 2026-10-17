package irbuild

import "testing"

// A generic function whose type parameter only its result names, called
// through a no-self interface function (`T.variants()`), runs at the type
// the call is expected to have: an annotated binding, an argument slot, a
// function's result, and a turbofish.
func TestReturnOnlyParam_RunsAtTheExpectedType(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

enum Coin {
    Heads
    Tails
}

derive Variants for Coin
derive Display for Coin

fn first<T>() -> Maybe<T> where T: Variants {
    T.variants() |> List.head()
}

fn all<T>() -> List<T> where T: Variants {
    T.variants()
}

fn show(m: Maybe<Coin>) -> String {
    case m {
        Some(c) -> "${c}"
        None -> "none"
    }
}

fn pick() -> Maybe<Coin> {
    first()
}

fn main() {
    c: Maybe<Coin> = first()
    io.inspect(c)
    io.print(show(first()))
    io.inspect(pick())
    xs: List<Coin> = all()
    io.inspect(xs)
    io.print(Iter.count(all<Coin>()))
}
`, "Some(Heads)\nHeads\nSome(Heads)\n[Heads, Tails]\n2\n")
}
