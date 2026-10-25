package irbuild

import "testing"

// A `break`, `continue` or `return` that ends an `if` or `case` arm in value
// position leaves the callback, as one ending a guard does: the region's
// other arms give its value. These cover a block arm, a bare arm, a value
// `if`, a `case` nested in an `if` branch, and a binding, in each callback
// that runs a signal.

// Lambda callbacks of the adapters and reduce: the arm's signal is the
// callback's control answer, and an arm whose region's kind is inferred
// writes nothing to it.
func TestIRSignalArm_AdapterAndReduceCallbacks(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

fn main() {
    xs = [1, 2, 3, 4, 5, 6]
    // map: a block arm and a bare arm that continue.
    a = Iter.map(xs, fn x -> {
        case x % 3 {
            0 -> {
                io.print("skip ${x}")
                continue
            }
            1 -> continue
            _ -> x * 10
        }
    }) |> Iter.to_list()
    io.inspect(a)
    // map: a value 'if' whose branches continue and break.
    b = Iter.map(xs, fn x -> if x == 2 { continue } else if x == 5 { break } else { x }) |> Iter.to_list()
    io.inspect(b)
    // map: a case nested in an if branch, with 'break v'.
    c = Iter.map(xs, fn x -> {
        if x > 2 {
            case x {
                4 -> break 40
                _ -> x
            }
        } else {
            0 - x
        }
    }) |> Iter.to_list()
    io.inspect(c)
    // map: the signal in a binding.
    d = Iter.map(xs, fn x -> {
        y = case x % 2 {
            0 -> continue
            _ -> x + 100
        }
        y * 2
    }) |> Iter.to_list()
    io.inspect(d)
    e = Iter.map(xs, fn x -> {
        y = if x > 3 { break x * 1000 } else { x }
        y + 1
    }) |> Iter.to_list()
    io.inspect(e)
    // filter.
    f = Iter.filter(xs, fn x -> {
        case x {
            2 -> continue
            5 -> break
            _ -> x % 2 == 0 or x == 1
        }
    }) |> Iter.to_list()
    io.inspect(f)
    g = Iter.filter(xs, fn x -> if x == 4 { break True } else { x % 2 == 1 }) |> Iter.to_list()
    io.inspect(g)
    // take_while.
    h = Iter.take_while(xs, fn x -> {
        case x {
            2 -> continue
            _ -> x < 4
        }
    }) |> Iter.to_list()
    io.inspect(h)
    // each.
    Iter.each(xs, fn x -> {
        case x % 2 {
            0 -> continue
            _ -> {
                if x == 5 { break }
                io.print("each ${x}")
            }
        }
    })
    Iter.each(xs, fn x -> {
        case x {
            3 -> break
            _ -> io.print("each2 ${x}")
        }
    })
    // iterate.
    i = Iter.iterate(1, fn n -> {
        case n > 20 {
            True -> break
            False -> n * 3
        }
    }) |> Iter.to_list()
    io.inspect(i)
    j = Iter.iterate(1, fn n -> if n > 5 { break n * 100 } else { n + 2 }) |> Iter.to_list()
    io.inspect(j)
    // reduce.
    k = Iter.reduce(xs, fn acc = 0, x -> {
        case x % 2 {
            0 -> {
                io.print("odd only ${x}")
                continue
            }
            _ -> if x == 5 { break acc * 100 } else { acc + x }
        }
    })
    io.inspect(k)
    l = Iter.reduce(xs, fn acc = 0, x -> {
        step = case x {
            3 -> continue
            6 -> break
            _ -> x
        }
        acc + step
    })
    io.inspect(l)
}
`, "skip 3\nskip 6\n[20, 50]\n[1, 3, 4]\n[-1, -2, 3, 40]\n[202, 206, 210]\n"+
		"[2, 3, 4, 4000]\n[1, 4]\n[1, 3, 4]\n[1, 3]\neach 1\neach 3\neach2 1\neach2 2\n[1, 3, 9]\n[1, 3, 5, 700]\n"+
		"odd only 2\nodd only 4\n400\n12\n")
}

// A named function passed as the callback signals from an arm as a lambda
// does.
func TestIRSignalArm_NamedCallbacks(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

fn tens(n: Int) -> Int {
    case n % 3 {
        0 -> {
            io.print("skip ${n}")
            continue
        }
        _ -> if n > 4 { break n * 100 } else { n * 10 }
    }
}

fn evens_until_five(n: Int) -> Bool {
    keep = case n {
        5 -> break
        _ -> n % 2 == 0
    }
    keep
}

fn small(n: Int) -> Bool {
    if n == 2 { continue } else { n < 4 }
}

fn show(n: Int) -> Unit {
    case n {
        2 -> continue
        4 -> break
        _ -> io.print("show ${n}")
    }
}

fn triple(n: Int) -> Int {
    case n > 20 {
        True -> break
        False -> n * 3
    }
}

fn add_odd(acc: Int, n: Int) -> Int {
    case n % 2 {
        0 -> continue
        _ -> if n == 5 { break acc * 100 } else { acc + n }
    }
}

fn main() {
    xs = [1, 2, 3, 4, 5, 6]
    io.inspect(Iter.map(xs, tens) |> Iter.to_list())
    io.inspect(Iter.filter(xs, evens_until_five) |> Iter.to_list())
    io.inspect(Iter.take_while(xs, small) |> Iter.to_list())
    Iter.each(xs, show)
    io.inspect(Iter.iterate(1, triple) |> Iter.to_list())
    io.inspect(Iter.reduce(xs, add_odd, from: 0))
}
`, "skip 3\n[10, 20, 40, 500]\n[2, 4]\n[1, 3]\nshow 1\nshow 3\n[1, 3, 9]\n400\n")
}

// In an inline Iter.loop every `break`, `return` and `continue` is the
// loop's: an arm ending in `continue` goes round again with the state
// unchanged, `return v` makes v the next state, and `break v` ends the loop.
// The channel feeds each iteration a new input, so a `continue` makes
// progress. early_state's `return 5` used to leave early_state itself.
func TestIRSignalArm_IterLoop(t *testing.T) {
	verifyLambdaProgram(t, `import std/channels.{Channel, Receiver, Sender}
import std/io

struct Account {
    owner: String
    balance: Int
}

fn feed(items: List<String>) -> Receiver<String> {
    ch = Channel.buffered<String>(10)
    Iter.each(items, fn s -> {
        _ = Sender.send(ch.sender, s)
    })
    Sender.close(ch.sender)
    ch.receiver
}

fn deposit(account: Account, input: Receiver<String>) -> Account {
    Iter.loop(fn acc = account -> {
        if acc.balance > 100 { break acc }
        text = case Receiver.receive(input) {
            Some(t) -> t
            None -> break acc
        }
        case String.to_int(text) {
            Some(n) -> #{..acc, balance: acc.balance + n}
            None -> {
                io.print("not a number: ${text}")
                continue
            }
        }
    })
}

fn bare_arm(input: Receiver<String>) -> Int {
    Iter.loop(fn total = 0 -> {
        case Receiver.receive(input) {
            None -> break total
            Some("skip") -> continue
            Some("stop") -> break total * 10
            Some(t) -> total + String.length(t)
        }
    })
}

fn if_branch(input: Receiver<String>) -> Int {
    Iter.loop(fn n = 0 -> {
        t = case Receiver.receive(input) {
            Some(t) -> t
            None -> break n
        }
        if t == "x" { continue } else if t == "end" { break n + 1000 } else { n + 1 }
    })
}

fn nested(input: Receiver<String>) -> Int {
    Iter.loop(fn n = 0 -> {
        t = Receiver.receive(input)
        if n < 100 {
            case t {
                Some("again") -> continue
                Some(_) -> n + 1
                None -> break n
            }
        } else {
            break n
        }
    })
}

fn bound(input: Receiver<String>) -> Int {
    Iter.loop(fn n = 0 -> {
        step = case Receiver.receive(input) {
            Some("x") -> continue
            Some("next") -> return n + 100
            Some(t) -> String.length(t)
            None -> break n
        }
        n + step
    })
}

fn arm_guard(input: Receiver<String>) -> Int {
    Iter.loop(fn n = 0 -> {
        case Receiver.receive(input) {
            Some(t) -> {
                if t == "x" { continue }
                if t == "big" { return n + 50 }
                n + 1
            }
            None -> break n
        }
    })
}

fn early_state() -> Int {
    n = Iter.loop(fn i = 0 -> {
        case i {
            0 -> return 5
            5 -> break i * 100
            _ -> i + 1
        }
    })
    io.print("after the loop")
    n + 1
}

fn main() {
    a = deposit(#{owner: "ann", balance: 10}, feed(["5", "five", "40", "x", "70", "1"]))
    io.inspect(a)
    io.inspect(bare_arm(feed(["ab", "skip", "cde", "skip"])))
    io.inspect(bare_arm(feed(["ab", "skip", "stop", "zzz"])))
    io.inspect(if_branch(feed(["a", "x", "b", "x"])))
    io.inspect(if_branch(feed(["a", "x", "end", "b"])))
    io.inspect(nested(feed(["a", "again", "b"])))
    io.inspect(bound(feed(["ab", "x", "next", "c"])))
    io.inspect(arm_guard(feed(["a", "x", "big", "b"])))
    io.inspect(early_state())
}
`, "not a number: five\nnot a number: x\n#Account{owner: \"ann\", balance: 125}\n"+
		"5\n20\n2\n1001\n2\n103\n52\nafter the loop\n501\n")
}

// A statement in an Iter.loop body may branch: a statement `if`, a block
// holding a guard, a binding whose arm leaves the iteration from below
// another arm, and a region inside the tail's operand.
func TestIRSignalArm_IterLoopBranchingStatements(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

fn stmt_if() -> Int {
    Iter.loop(fn n = 0 -> {
        if n % 2 == 0 { io.print("even ${n}") } else { io.print("odd ${n}") }
        if n > 2 { break n }
        n + 1
    })
}

fn deep() -> Int {
    Iter.loop(fn n = 0 -> {
        case n {
            0 -> {
                y = case n + 1 {
                    1 -> return 3
                    _ -> 0
                }
                y
            }
            3 -> {
                z = if n > 1 { break n * 7 } else { n }
                z
            }
            _ -> n
        }
    })
}

fn calls(n: Int) -> Int {
    n + 1
}

fn operand() -> Int {
    Iter.loop(fn n = 0 -> {
        if n > 3 { break n }
        calls(case n {
            1 -> return 3
            _ -> n
        })
    })
}

fn main() {
    io.inspect(stmt_if())
    io.inspect(deep())
    io.inspect(operand())
}
`, "even 0\nodd 1\neven 2\nodd 3\n3\n21\n4\n")
	verifyLambdaProgram(t, `import std/io

fn f() -> Int {
    Iter.loop(fn n = 0 -> {
        {
            if n == 1 { return n + 5 }
            io.print("at ${n}")
        }
        {
            if n > 6 { break n }
        }
        n + 1
    })
}

fn main() {
    io.inspect(f())
}
`, "at 0\nat 6\nat 7\n7\n")
}
