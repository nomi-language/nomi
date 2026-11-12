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
	verifyLambdaProgram(t, `import std::channels.{Channel, Receiver, Sender}
import std::io

struct Account
    owner: String
    balance: Int
end

fn feed(items: List<String>) -> Receiver<String>
    ch = Channel.buffered<String>(10)
    Iter.each(items, fn s ->
        _ = Sender.send(ch.sender, s)
    end)
    Sender.close(ch.sender)
    ch.receiver
end

fn parse(s: String) -> Result<Int, String>
    case String.to_int(s)
        Some(n) -> Ok(n)
        None -> Err("not a number: ${s}")
    end
end

fn break_or_continue(input: Receiver<String>) -> Int
    Iter.loop(0, fn n ->
        case Receiver.receive(input)
            None -> break n
            _ -> continue
        end
    end)
end

fn break_or_next() -> Int
    Iter.loop(0, fn n ->
        case n > 2
            True -> break n
            False -> return n + 1
        end
    end)
end

fn bare_arms(input: Receiver<String>) -> Int
    Iter.loop(0, fn n ->
        case Receiver.receive(input)
            Some("skip") -> continue
            Some(t) -> return n + String.length(t)
            None -> break n
        end
    end)
end

fn block_arms(input: Receiver<String>) -> Int
    Iter.loop(0, fn n ->
        case Receiver.receive(input)
            Some("skip") ->
                io.print("skip")
                continue
            Some(t) ->
                io.print("got ${t}")
                return n + String.length(t)
            None ->
                io.print("done")
                break n
        end
    end)
end

fn deposit(account: Account, input: Receiver<String>) -> Account
    Iter.loop(account, fn acc ->
        text = case Receiver.receive(input)
            Some(t) -> t
            None -> break acc
        end
        case parse(text)
            Ok(amount) ->
                io.print("deposit ${amount}")
                break {..acc, balance: amount}
            Err(msg) ->
                io.print(msg)
                continue
        end
    end)
end

fn bare_break(input: Receiver<String>)
    _ = Iter.loop(0, fn n ->
        case Receiver.receive(input)
            Some("stop") -> break
            _ -> return n + 1
        end
    end)
    io.print("stopped")
end

fn if_else(input: Receiver<String>) -> Int
    Iter.loop(0, fn n ->
        t = case Receiver.receive(input)
            Some(t) -> t
            None -> break n
        end
        if t == "skip" then
            io.print("skip")
            continue
        else
            return n + String.length(t)
        end
    end)
end

fn if_else_bare(input: Receiver<String>) -> Int
    Iter.loop(0, fn n ->
        if Receiver.receive(input) == None then break n else continue
    end)
end

fn nested(input: Receiver<String>) -> Int
    Iter.loop(0, fn n ->
        case Receiver.receive(input)
            Some(t) ->
                case t == "skip"
                    True -> continue
                    False -> return n + String.length(t)
                end
            None -> break n
        end
    end)
end

fn nested_in_if(input: Receiver<String>) -> Int
    Iter.loop(0, fn n ->
        t = Receiver.receive(input)
        if t == None then
            break n
        else
            case t
                Some("skip") -> continue
                _ -> return n + 1
            end
        end
    end)
end

fn binding(input: Receiver<String>) -> Int
    Iter.loop(0, fn n ->
        y = case Receiver.receive(input)
            Some("skip") -> continue
            Some(t) -> return n + String.length(t)
            None -> break n
        end
        io.print("never")
        y
    end)
end

fn binding_if(input: Receiver<String>) -> Int
    Iter.loop(0, fn n ->
        t = Receiver.receive(input)
        y = if t == None then break n else return n + 1
        io.print("never")
        y
    end)
end

fn main()
    io.inspect(break_or_continue(feed(["a", "b"])))
    io.inspect(break_or_next())
    io.inspect(bare_arms(feed(["ab", "skip", "cde"])))
    io.inspect(block_arms(feed(["ab", "skip", "cde"])))
    io.inspect(deposit({owner: "ann", balance: 1}, feed(["x", "y", "40", "50"])))
    io.inspect(deposit({owner: "bob", balance: 1}, feed(["x"])))
    bare_break(feed(["a", "stop", "b"]))
    io.inspect(if_else(feed(["ab", "skip", "cde"])))
    io.inspect(if_else_bare(feed(["a", "b"])))
    io.inspect(nested(feed(["ab", "skip", "cde"])))
    io.inspect(nested_in_if(feed(["ab", "skip", "cde"])))
    io.inspect(binding(feed(["ab", "skip", "cde"])))
    io.inspect(binding_if(feed(["ab", "skip", "cde"])))
end
`, "0\n3\n5\n"+
		"got ab\nskip\ngot cde\ndone\n5\n"+
		"not a number: x\nnot a number: y\ndeposit 40\nAccount{owner: \"ann\", balance: 40}\n"+
		"not a number: x\nAccount{owner: \"bob\", balance: 1}\n"+
		"stopped\n"+
		"skip\n5\n0\n5\n2\n5\n3\n")
}

// The same shapes in the callbacks that signal: Iter.map, filter, each and
// reduce, where `continue` skips, `break` stops and `break v` answers v and
// stops. A callback none of whose arms gives a value takes its result type
// from the callee's parameter.
func TestIRDivergedArms_Callbacks(t *testing.T) {
	verifyLambdaProgram(t, `import std::io

fn main()
    xs = [1, 2, 3, 4, 5]
    a = Iter.map(xs, fn x ->
        case x
            1 -> continue
            _ -> break 99
        end
    end) |> Iter.to_list()
    io.inspect(a)
    b = Iter.map(xs, fn x ->
        case x
            1 -> return 50
            2 ->
                io.print("skip 2")
                continue
            _ ->
                case x > 3
                    True -> break x * 100
                    False -> continue
                end
        end
    end) |> Iter.to_list()
    io.inspect(b)
    c = Iter.map(xs, fn x -> if x < 4 then continue else break x) |> Iter.to_list()
    io.inspect(c)
    d = Iter.map(xs, fn x ->
        if x < 2 then
            io.print("small ${x}")
            continue
        else
            io.print("big ${x}")
            break 0
        end
    end) |> Iter.to_list()
    io.inspect(d)
    e = Iter.map(xs, fn x ->
        y = case x
            1 -> continue
            _ -> break 7
        end
        io.inspect(y)
        x
    end) |> Iter.to_list()
    io.inspect(e)
    f = Iter.filter(xs, fn x ->
        case x
            1 ->
                io.print("one")
                continue
            _ ->
                io.print("stop")
                break
        end
    end) |> Iter.to_list()
    io.inspect(f)
    g = Iter.filter(xs, fn x ->
        y = if x == 1 then continue else break True
        y
    end) |> Iter.to_list()
    io.inspect(g)
    Iter.each(xs, fn x ->
        case x
            1 ->
                io.print("each ${x}")
                continue
            _ -> break
        end
    end)
    Iter.each(xs, fn x ->
        if x < 3 then continue else break
    end)
    h = Iter.reduce(xs, 0, fn acc, x ->
        case x
            1 -> continue
            _ -> break acc + 100
        end
    end)
    io.inspect(h)
    i = Iter.reduce(xs, 0, fn acc, x -> if x < 3 then return acc + x else break acc)
    io.inspect(i)
end
`, "[99]\nskip 2\n[50, 400]\n[4]\nsmall 1\nbig 2\n[0]\n[7]\none\nstop\n[]\n[2]\neach 1\n100\n3\n")
}

// A lambda whose tail leaves in every arm, and an Iter.loop inside a lambda
// whose tail does.
func TestIRDivergedArms_Lambdas(t *testing.T) {
	verifyLambdaProgram(t, `import std::channels.{Channel, Receiver, Sender}
import std::io

fn feed(items: List<String>) -> Receiver<String>
    ch = Channel.buffered<String>(10)
    Iter.each(items, fn s ->
        _ = Sender.send(ch.sender, s)
    end)
    Sender.close(ch.sender)
    ch.receiver
end

fn main()
    f = fn start: Int ->
        r = Iter.loop(start, fn n ->
            case n > 2
                True -> break n
                False -> return n + 1
            end
        end)
        r * 2
    end
    io.inspect(f(0))
    g = Iter.map([1, 2], fn x ->
        Iter.loop(x, fn n ->
            if n > 3 then break n else return n + 1
        end)
    end) |> Iter.to_list()
    io.inspect(g)
    totals = Iter.map([["a", "1", "b"], ["c"]], fn items ->
        input = feed(items)
        Iter.loop(0, fn n ->
            case Receiver.receive(input)
                Some(t) ->
                    case String.to_int(t)
                        Some(v) -> return n + v
                        None -> continue
                    end
                None -> break n
            end
        end)
    end) |> Iter.to_list()
    io.inspect(totals)
    h = fn x: Int ->
        y = case x
            0 -> return 1
            _ -> return 2
        end
        io.inspect(y)
        3
    end
    io.inspect(h(0))
    io.inspect(h(5))
end
`, "6\n[4, 4]\n[1, 0]\n1\n2\n")
}

// A binding whose arms all return, in a named function, followed by
// statements that never run.
func TestIRDivergedArms_NamedFunctionBinding(t *testing.T) {
	verifyLambdaProgram(t, `import std::io

fn f(x: Int) -> Int
    y = case x
        0 -> return 1
        _ -> return 2
    end
    io.print("never")
    y
end

fn g(x: Int) -> Int
    y = if x > 0 then return 1 else return 2
    io.inspect(y)
    3
end

fn main()
    io.inspect(f(0))
    io.inspect(f(5))
    io.inspect(g(5))
    io.inspect(g(0))
end
`, "1\n2\n1\n2\n")
}

// An `if` or `case` every arm of which leaves, used as an operand: an
// argument, an operand of `+`, a struct field, a list item, a pipe's source,
// a `?`'s operand, an `if` condition, a `case` subject, an arm's value
// and an Iter.loop or callback tail. The
// construct that would read it never runs; the operands evaluated before it
// do, in order, and nothing after it does.
func TestIRDivergedArms_Operands(t *testing.T) {
	verifyLambdaProgram(t, `import std::io

struct Point
    x: Int
    y: Int
end

fn calls(n: Int) -> Int
    n + 1
end

fn noisy(n: Int) -> Int
    io.print("noisy ${n}")
    n
end

fn pair(a: Int, b: Int) -> Int
    a + b
end

fn arg(x: Int) -> Int
    calls(case x
        0 -> return 10
        _ -> return 20
    end)
end

fn second_arg(x: Int) -> Int
    pair(noisy(1), if x == 0 then return 11 else return 21)
end

fn plus(x: Int) -> Int
    noisy(2) + if x == 0 then return 30 else return 40
end

fn field(x: Int) -> Point
    Point{x: noisy(3), y: case x
        0 -> return Point{x: 0, y: 0}
        _ -> return Point{x: 1, y: 1}
    end}
end

fn item(x: Int) -> List<Int>
    [noisy(4), case x
        0 -> return [0]
        _ -> return [1]
    end]
end

fn piped(x: Int) -> Int
    case x
        0 -> return 50
        _ -> return 60
    end |> calls()
end

fn bound(x: Int) -> Int
    y = calls(case x
        0 -> return 70
        _ -> return 80
    end)
    io.print("never")
    y
end

fn statement(x: Int) -> Int
    io.inspect(noisy(5) + case x
        0 -> return 90
        _ -> return 91
    end)
    io.print("never")
    0
end

fn discarded(x: Int)
    _ = calls(case x
        0 ->
            io.print("left zero")
            return
        _ -> return
    end)
    io.print("never")
end

fn parse(s: String) -> Result<Int, String>
    case String.to_int(s)
        Some(n) -> Ok(n)
        None -> Err("bad ${s}")
    end
end

fn tried(x: Int) -> Result<Int, String>
    n = parse(case x
        0 -> return Ok(100)
        _ -> return Err("no")
    end)?
    Ok(n)
end

fn tried_arm(s: String) -> Result<Int, String>
    n = parse(case s
        "" -> return Err("empty")
        _ ->
            m = parse(s)?
            return Ok(m * 2)
    end)?
    Ok(n)
end

fn in_arm(x: Int, y: Int) -> Int
    case x
        0 -> calls(case y
            0 -> return 110
            _ -> return 120
        end)
        _ -> x
    end
end

fn loop_state(limit: Int) -> Int
    Iter.loop(0, fn n ->
        calls(case n >= limit
            True -> break n
            False -> return n + 1
        end)
    end)
end

fn condition(x: Int) -> Int
    if case x
        0 -> return 130
        _ -> return 140
    end then 1 else 2
end

fn subject(x: Int) -> Int
    case calls(if x > 0 then return 150 else return 160)
        0 -> 1
        _ -> 2
    end
end

fn lambda_return(x: Int) -> Int
    f = fn v: Int -> calls(if v > 0 then return v * 2 else return 0)
    f(x)
end

fn main()
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
    mapped = Iter.map([0, 1, 5, 6], fn x -> calls(case x
        0 -> continue
        1 -> continue
        _ -> break x
    end))
        |> Iter.to_list()
    io.inspect(mapped)
end
`, "10\n"+
		"noisy 1\n21\n"+
		"noisy 2\n40\n"+
		"noisy 3\nPoint{x: 0, y: 0}\n"+
		"noisy 4\n[1]\n"+
		"50\n80\n"+
		"noisy 5\n90\n"+
		"left zero\n"+
		"Ok(100)\nErr(\"no\")\n"+
		"Err(\"empty\")\nOk(42)\nErr(\"bad x\")\n"+
		"120\n5\n3\n140\n160\n8\n[5]\n")
}
