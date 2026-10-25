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

// A type parameter only the result names is solved through `try` under an
// annotated binding, where the expected type is `Result<Int, ?E>`, and
// through an operator, where the call has no expected type at all. The
// checker records the call at those solutions, so the builder instantiates
// the callee rather than declining it.
func TestReturnOnlyParam_SolvedThroughTryAndOperators(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

interface FromInt {
    fn from_int(n: Int) -> self
}

impl FromInt for Int {
    fn from_int(n: Int) -> Int {
        n * 10
    }
}

type Row

impl Row {
    pub fn get<T>(n: Int) -> Result<T, String> where T: FromInt {
        Ok(T.from_int(n))
    }
}

fn annotated() -> Result<Int, String> {
    n: Int = try Row.get(3)
    Ok(n + 1)
}

fn operand() -> Result<Int, String> {
    Ok(1 + try Row.get(4))
}

fn main() {
    io.inspect(annotated())
    io.inspect(operand())
}
`, "Ok(31)\nOk(41)\n")
}
