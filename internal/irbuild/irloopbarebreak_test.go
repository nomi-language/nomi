package irbuild

import "testing"

// A bare `break` in an Iter.loop callback stops the loop with the current
// state, the value the callback's parameter was bound to on that iteration,
// as a bare `break` in Iter.reduce stops with the accumulator so far.

// Each position a bare `break` can take in a stated loop, with Int and
// struct states, and in a stateless loop, whose state is Unit.
func TestIRLoop_BareBreakAnswersTheCurrentState(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

struct Tally {
    label: String
    count: Int
}

fn first_even(xs: List<Int>) -> Maybe<Int> {
    Iter.find(xs, fn x -> x % 2 == 0)
}

fn settle(start: Int) -> Int {
    Iter.loop(fn n = start -> {
        if n % 5 == 0 { break }
        n + 1
    })
}

fn main() {
    // A guard.
    a = Iter.loop(fn n = 0 -> {
        if n * n > 20 { break }
        n + 1
    })
    io.inspect(a)
    // An else binding: the pattern fails once five elements are dropped.
    b = Iter.loop(fn t = Tally{label: "t", count: 0} -> {
        Some(_) = first_even(Iter.drop([1, 3, 2, 5, 4], t.count) |> Iter.to_list()) else { break }
        {..t, count: t.count + 1}
    })
    io.inspect(b)
    // A case arm, bare and in a block.
    c = Iter.loop(fn n = 10 -> {
        case n % 7 {
            0 -> break
            _ -> n + 1
        }
    })
    io.inspect(c)
    d = Iter.loop(fn n = 1 -> {
        case n {
            8 -> {
                io.print("stop at ${n}")
                break
            }
            _ -> n * 2
        }
    })
    io.inspect(d)
    // A guard nested in an arm's block, and a value 'if' tail.
    e = Iter.loop(fn t = Tally{label: "e", count: 0} -> {
        case t.count % 2 {
            0 -> {
                if t.count >= 4 { break }
                {..t, count: t.count + 1}
            }
            _ -> {..t, count: t.count + 1}
        }
    })
    io.inspect(e)
    f = Iter.loop(fn n = 3 -> if n > 40 { break } else { n * 3 })
    io.inspect(f)
    // The state, not a binding that shadows the parameter.
    g = Iter.loop(fn n = 0 -> {
        n = n + 100
        if n > 102 { break }
        n - 99
    })
    io.inspect(g)
    // A loop inside a lambda, and one that is a named function's result.
    climb = fn start: Int -> Iter.loop(fn n = start -> {
        if n >= 10 { break }
        n + 3
    })
    io.inspect(climb(1))
    io.inspect(climb(20))
    io.inspect(settle(7))
    // A discarded loop.
    _ = Iter.loop(fn n = 0 -> {
        io.print("discarded ${n}")
        if n == 2 { break }
        n + 1
    })
    // A stateless loop's state is Unit, as a statement and as a value.
    Iter.loop(fn -> {
        io.print("statement round")
        break
    })
    u = Iter.loop(fn -> {
        io.print("value round")
        break
    })
    io.inspect(u)
}
`, `5
Tally{label: "t", count: 5}
14
stop at 8
8
Tally{label: "e", count: 4}
81
3
10
20
10
discarded 0
discarded 1
discarded 2
statement round
value round
Unit
`)
}

// The program that read an opening balance: the end of the input stops the
// loop with the account as the last failed parse left it.
func TestIRLoop_BareBreakInAnElseKeepsTheStructState(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

struct Account {
    name: String = "x"
    balance: Int = 0
}

fn parse_amount(s: String) -> Result<Int, String> {
    case String.to_int(s) {
        Some(n) -> Ok(n)
        None -> Err("not a number: ${s}")
    }
}

fn open(inputs: List<String>) -> Account {
    #(_, account) = Iter.loop(fn state = #(inputs, Account{}) -> {
        #(rest, account) = state
        Some(input) = List.head(rest) else { break }
        case parse_amount(input) {
            Ok(amount) -> break #(rest, {..account, balance: amount})
            Err(msg) -> {
                io.print(msg)
                #(Iter.drop(rest, 1) |> Iter.to_list(), {..account, name: "${account.name}!"})
            }
        }
    })
    account
}

fn main() {
    io.inspect(open(["abc", "12"]))
    io.inspect(open(["abc", "def"]))
}
`, `not a number: abc
Account{name: "x!", balance: 12}
not a number: abc
not a number: def
Account{name: "x!!", balance: 0}
`)
}
