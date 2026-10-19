package irbuild

import "testing"

// An `if` or `case` every arm of which leaves the iteration, the callback or
// the function writes no value, and no path reaches its join. The join must
// read nothing: these ran into an `ir.Lint` failure ("nothing in this
// function defines t6") that `nomi run` and `nomi check` reported as an
// internal compiler error.

// An Iter.loop whose tail `case` or `if` leaves the iteration in every arm,
// by `break`, `break v`, `continue` and `return v` in any mix, in bare arms,
// block arms, an `if`/`else`, a `case` nested in an arm, and a binding whose
// arms all leave followed by statements that never run. The loops read a
// channel so a `continue` makes progress.
func TestIRDivergedArms_IterLoopTail(t *testing.T) {
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

fn parse(s: String) -> Result<Int, String> {
    case String.to_int(s) {
        Some(n) -> Ok(n)
        None -> Err("not a number: ${s}")
    }
}

fn break_or_continue(input: Receiver<String>) -> Int {
    Iter.loop(fn n = 0 -> {
        case Receiver.receive(input) {
            None -> break n
            _ -> continue
        }
    })
}

fn break_or_next() -> Int {
    Iter.loop(fn n = 0 -> {
        case n > 2 {
            True -> break n
            False -> return n + 1
        }
    })
}

fn bare_arms(input: Receiver<String>) -> Int {
    Iter.loop(fn n = 0 -> {
        case Receiver.receive(input) {
            Some("skip") -> continue
            Some(t) -> return n + String.length(t)
            None -> break n
        }
    })
}

fn block_arms(input: Receiver<String>) -> Int {
    Iter.loop(fn n = 0 -> {
        case Receiver.receive(input) {
            Some("skip") -> {
                io.print("skip")
                continue
            }
            Some(t) -> {
                io.print("got ${t}")
                return n + String.length(t)
            }
            None -> {
                io.print("done")
                break n
            }
        }
    })
}

fn deposit(account: Account, input: Receiver<String>) -> Account {
    Iter.loop(fn acc = account -> {
        text = case Receiver.receive(input) {
            Some(t) -> t
            None -> break acc
        }
        case parse(text) {
            Ok(amount) -> {
                io.print("deposit ${amount}")
                break #{..acc, balance: amount}
            }
            Err(msg) -> {
                io.print(msg)
                continue
            }
        }
    })
}

fn bare_break(input: Receiver<String>) {
    _ = Iter.loop(fn n = 0 -> {
        case Receiver.receive(input) {
            Some("stop") -> break
            _ -> return n + 1
        }
    })
    io.print("stopped")
}

fn if_else(input: Receiver<String>) -> Int {
    Iter.loop(fn n = 0 -> {
        t = case Receiver.receive(input) {
            Some(t) -> t
            None -> break n
        }
        if t == "skip" {
            io.print("skip")
            continue
        } else {
            return n + String.length(t)
        }
    })
}

fn if_else_bare(input: Receiver<String>) -> Int {
    Iter.loop(fn n = 0 -> {
        if Receiver.receive(input) == None { break n } else { continue }
    })
}

fn nested(input: Receiver<String>) -> Int {
    Iter.loop(fn n = 0 -> {
        case Receiver.receive(input) {
            Some(t) -> {
                case t == "skip" {
                    True -> continue
                    False -> return n + String.length(t)
                }
            }
            None -> break n
        }
    })
}

fn nested_in_if(input: Receiver<String>) -> Int {
    Iter.loop(fn n = 0 -> {
        t = Receiver.receive(input)
        if t == None {
            break n
        } else {
            case t {
                Some("skip") -> continue
                _ -> return n + 1
            }
        }
    })
}

fn binding(input: Receiver<String>) -> Int {
    Iter.loop(fn n = 0 -> {
        y = case Receiver.receive(input) {
            Some("skip") -> continue
            Some(t) -> return n + String.length(t)
            None -> break n
        }
        io.print("never")
        y
    })
}

fn binding_if(input: Receiver<String>) -> Int {
    Iter.loop(fn n = 0 -> {
        t = Receiver.receive(input)
        y = if t == None { break n } else { return n + 1 }
        io.print("never")
        y
    })
}

fn main() {
    io.inspect(break_or_continue(feed(["a", "b"])))
    io.inspect(break_or_next())
    io.inspect(bare_arms(feed(["ab", "skip", "cde"])))
    io.inspect(block_arms(feed(["ab", "skip", "cde"])))
    io.inspect(deposit(#{owner: "ann", balance: 1}, feed(["x", "y", "40", "50"])))
    io.inspect(deposit(#{owner: "bob", balance: 1}, feed(["x"])))
    bare_break(feed(["a", "stop", "b"]))
    io.inspect(if_else(feed(["ab", "skip", "cde"])))
    io.inspect(if_else_bare(feed(["a", "b"])))
    io.inspect(nested(feed(["ab", "skip", "cde"])))
    io.inspect(nested_in_if(feed(["ab", "skip", "cde"])))
    io.inspect(binding(feed(["ab", "skip", "cde"])))
    io.inspect(binding_if(feed(["ab", "skip", "cde"])))
}
`, "0\n3\n5\n"+
		"got ab\nskip\ngot cde\ndone\n5\n"+
		"not a number: x\nnot a number: y\ndeposit 40\n#Account{owner: \"ann\", balance: 40}\n"+
		"not a number: x\n#Account{owner: \"bob\", balance: 1}\n"+
		"stopped\n"+
		"skip\n5\n0\n5\n2\n5\n3\n")
}

// The same shapes in the callbacks that signal: Iter.map, filter, each and
// reduce, where `continue` skips, `break` stops and `break v` answers v and
// stops. A callback none of whose arms gives a value takes its result type
// from the callee's parameter.
func TestIRDivergedArms_Callbacks(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

fn main() {
    xs = [1, 2, 3, 4, 5]
    a = Iter.map(xs, fn x -> {
        case x {
            1 -> continue
            _ -> break 99
        }
    }) |> Iter.to_list()
    io.inspect(a)
    b = Iter.map(xs, fn x -> {
        case x {
            1 -> return 50
            2 -> {
                io.print("skip 2")
                continue
            }
            _ -> {
                case x > 3 {
                    True -> break x * 100
                    False -> continue
                }
            }
        }
    }) |> Iter.to_list()
    io.inspect(b)
    c = Iter.map(xs, fn x -> if x < 4 { continue } else { break x }) |> Iter.to_list()
    io.inspect(c)
    d = Iter.map(xs, fn x -> {
        if x < 2 {
            io.print("small ${x}")
            continue
        } else {
            io.print("big ${x}")
            break 0
        }
    }) |> Iter.to_list()
    io.inspect(d)
    e = Iter.map(xs, fn x -> {
        y = case x {
            1 -> continue
            _ -> break 7
        }
        io.inspect(y)
        x
    }) |> Iter.to_list()
    io.inspect(e)
    f = Iter.filter(xs, fn x -> {
        case x {
            1 -> {
                io.print("one")
                continue
            }
            _ -> {
                io.print("stop")
                break
            }
        }
    }) |> Iter.to_list()
    io.inspect(f)
    g = Iter.filter(xs, fn x -> {
        y = if x == 1 { continue } else { break True }
        y
    }) |> Iter.to_list()
    io.inspect(g)
    Iter.each(xs, fn x -> {
        case x {
            1 -> {
                io.print("each ${x}")
                continue
            }
            _ -> break
        }
    })
    Iter.each(xs, fn x -> {
        if x < 3 { continue } else { break }
    })
    h = Iter.reduce(xs, fn acc = 0, x -> {
        case x {
            1 -> continue
            _ -> break acc + 100
        }
    })
    io.inspect(h)
    i = Iter.reduce(xs, fn acc = 0, x -> if x < 3 { return acc + x } else { break acc })
    io.inspect(i)
}
`, "[99]\nskip 2\n[50, 400]\n[4]\nsmall 1\nbig 2\n[0]\n[7]\none\nstop\n[]\n[2]\neach 1\n100\n3\n")
}

// A lambda whose tail leaves in every arm, and an Iter.loop inside a lambda
// whose tail does.
func TestIRDivergedArms_Lambdas(t *testing.T) {
	verifyLambdaProgram(t, `import std/channels.{Channel, Receiver, Sender}
import std/io

fn feed(items: List<String>) -> Receiver<String> {
    ch = Channel.buffered<String>(10)
    Iter.each(items, fn s -> {
        _ = Sender.send(ch.sender, s)
    })
    Sender.close(ch.sender)
    ch.receiver
}

fn main() {
    f = fn start: Int -> {
        r = Iter.loop(fn n = start -> {
            case n > 2 {
                True -> break n
                False -> return n + 1
            }
        })
        r * 2
    }
    io.inspect(f(0))
    g = Iter.map([1, 2], fn x -> {
        Iter.loop(fn n = x -> {
            if n > 3 { break n } else { return n + 1 }
        })
    }) |> Iter.to_list()
    io.inspect(g)
    totals = Iter.map([["a", "1", "b"], ["c"]], fn items -> {
        input = feed(items)
        Iter.loop(fn n = 0 -> {
            case Receiver.receive(input) {
                Some(t) -> {
                    case String.to_int(t) {
                        Some(v) -> return n + v
                        None -> continue
                    }
                }
                None -> break n
            }
        })
    }) |> Iter.to_list()
    io.inspect(totals)
    h = fn x: Int -> {
        y = case x {
            0 -> return 1
            _ -> return 2
        }
        io.inspect(y)
        3
    }
    io.inspect(h(0))
    io.inspect(h(5))
}
`, "6\n[4, 4]\n[1, 0]\n1\n2\n")
}

// A binding whose arms all return, in a named function, followed by
// statements that never run.
func TestIRDivergedArms_NamedFunctionBinding(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

fn f(x: Int) -> Int {
    y = case x {
        0 -> return 1
        _ -> return 2
    }
    io.print("never")
    y
}

fn g(x: Int) -> Int {
    y = if x > 0 { return 1 } else { return 2 }
    io.inspect(y)
    3
}

fn main() {
    io.inspect(f(0))
    io.inspect(f(5))
    io.inspect(g(5))
    io.inspect(g(0))
}
`, "1\n2\n1\n2\n")
}

// An `if` or `case` every arm of which leaves, used as an operand: an
// argument, an operand of `+`, a struct field, a list item, a pipe's source,
// a `try`'s operand, an `if` condition, a `case` subject, an arm's value
// and an Iter.loop or callback tail. The
// construct that would read it never runs; the operands evaluated before it
// do, in order, and nothing after it does.
func TestIRDivergedArms_Operands(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

struct Point {
    x: Int
    y: Int
}

fn calls(n: Int) -> Int {
    n + 1
}

fn noisy(n: Int) -> Int {
    io.print("noisy ${n}")
    n
}

fn pair(a: Int, b: Int) -> Int {
    a + b
}

fn arg(x: Int) -> Int {
    calls(case x {
        0 -> return 10
        _ -> return 20
    })
}

fn second_arg(x: Int) -> Int {
    pair(noisy(1), if x == 0 { return 11 } else { return 21 })
}

fn plus(x: Int) -> Int {
    noisy(2) + if x == 0 { return 30 } else { return 40 }
}

fn field(x: Int) -> Point {
    #Point{x: noisy(3), y: case x {
        0 -> return #Point{x: 0, y: 0}
        _ -> return #Point{x: 1, y: 1}
    }}
}

fn item(x: Int) -> List<Int> {
    [noisy(4), case x {
        0 -> return [0]
        _ -> return [1]
    }]
}

fn piped(x: Int) -> Int {
    case x {
        0 -> return 50
        _ -> return 60
    } |> calls()
}

fn bound(x: Int) -> Int {
    y = calls(case x {
        0 -> return 70
        _ -> return 80
    })
    io.print("never")
    y
}

fn statement(x: Int) -> Int {
    io.inspect(noisy(5) + case x {
        0 -> return 90
        _ -> return 91
    })
    io.print("never")
    0
}

fn discarded(x: Int) {
    _ = calls(case x {
        0 -> {
            io.print("left zero")
            return
        }
        _ -> return
    })
    io.print("never")
}

fn parse(s: String) -> Result<Int, String> {
    case String.to_int(s) {
        Some(n) -> Ok(n)
        None -> Err("bad ${s}")
    }
}

fn tried(x: Int) -> Result<Int, String> {
    n = try parse(case x {
        0 -> return Ok(100)
        _ -> return Err("no")
    })
    Ok(n)
}

fn tried_arm(s: String) -> Result<Int, String> {
    n = try parse(case s {
        "" -> return Err("empty")
        _ -> {
            m = try parse(s)
            return Ok(m * 2)
        }
    })
    Ok(n)
}

fn in_arm(x: Int, y: Int) -> Int {
    case x {
        0 -> calls(case y {
            0 -> return 110
            _ -> return 120
        })
        _ -> x
    }
}

fn loop_state(limit: Int) -> Int {
    Iter.loop(fn n = 0 -> {
        calls(case n >= limit {
            True -> break n
            False -> return n + 1
        })
    })
}

fn condition(x: Int) -> Int {
    if case x { 0 -> return 130  _ -> return 140 } { 1 } else { 2 }
}

fn subject(x: Int) -> Int {
    case calls(if x > 0 { return 150 } else { return 160 }) {
        0 -> 1
        _ -> 2
    }
}

fn lambda_return(x: Int) -> Int {
    f = fn v: Int -> calls(if v > 0 { return v * 2 } else { return 0 })
    f(x)
}

fn main() {
    io.inspect(arg(0))
    io.inspect(second_arg(1))
    io.inspect(plus(1))
    io.inspect(field(0))
    io.inspect(item(1))
    io.inspect(piped(0))
    io.inspect(bound(1))
    io.inspect(statement(0))
    discarded(0)
    io.inspect(tried(0))
    io.inspect(tried(1))
    io.inspect(tried_arm(""))
    io.inspect(tried_arm("21"))
    io.inspect(tried_arm("x"))
    io.inspect(in_arm(0, 1))
    io.inspect(in_arm(5, 1))
    io.inspect(loop_state(3))
    io.inspect(condition(1))
    io.inspect(subject(0))
    io.inspect(lambda_return(4))
    mapped = Iter.map([0, 1, 5, 6], fn x -> calls(case x {
        0 -> continue
        1 -> continue
        _ -> break x
    }))
        |> Iter.to_list()
    io.inspect(mapped)
}
`, "10\n"+
		"noisy 1\n21\n"+
		"noisy 2\n40\n"+
		"noisy 3\n#Point{x: 0, y: 0}\n"+
		"noisy 4\n[1]\n"+
		"50\n80\n"+
		"noisy 5\n90\n"+
		"left zero\n"+
		"Ok(100)\nErr(\"no\")\n"+
		"Err(\"empty\")\nOk(42)\nErr(\"bad x\")\n"+
		"120\n5\n3\n140\n160\n8\n[5]\n")
}
