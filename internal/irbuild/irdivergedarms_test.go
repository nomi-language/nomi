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
// arms all leave, which ends the callback. The loops read a channel so a
// `continue` makes progress.
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
        _y = case Receiver.receive(input)
            Some("skip") -> continue
            Some(t) -> return n + String.length(t)
            None -> break n
        end
    end)
end

fn binding_if(input: Receiver<String>) -> Int
    Iter.loop(0, fn n ->
        t = Receiver.receive(input)
        _y = if t == None then break n else return n + 1
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
        _y = case x
            1 -> continue
            _ -> break 7
        end
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
        _y = if x == 1 then continue else break True
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
        _y = case x
            0 -> return 1
            _ -> return 2
        end
    end
    io.inspect(h(0))
    io.inspect(h(5))
end
`, "6\n[4, 4]\n[1, 0]\n1\n2\n")
}

// A binding whose arms all return, ending a named function.
func TestIRDivergedArms_NamedFunctionBinding(t *testing.T) {
	verifyLambdaProgram(t, `import std::io

fn f(x: Int) -> Int
    _y = case x
        0 -> return 1
        _ -> return 2
    end
end

fn g(x: Int) -> Int
    _y = if x > 0 then return 1 else return 2
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
    _y = calls(case x
        0 -> return 70
        _ -> return 80
    end)
end

fn statement(x: Int) -> Int
    io.inspect(noisy(5) + case x
        0 -> return 90
        _ -> return 91
    end)
end

fn discarded(x: Int)
    _ = calls(case x
        0 ->
            io.print("left zero")
            return
        _ -> return
    end)
end

fn parse(s: String) -> Result<Int, String>
    case String.to_int(s)
        Some(n) -> Ok(n)
        None -> Err("bad ${s}")
    end
end

fn tried(x: Int) -> Result<Int, String>
    _n = parse(case x
        0 -> return Ok(100)
        _ -> return Err("no")
    end)?
end

fn tried_arm(s: String) -> Result<Int, String>
    _n = parse(case s
        "" -> return Err("empty")
        _ ->
            m = parse(s)?
            return Ok(m * 2)
    end)?
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

// An `if` or `case` whose every arm returns is lifted out of every operand
// that always evaluates it, after the operands before it: a string's hole,
// `dbg`, a `defer`'s argument, a destructure's value, a pattern binding's, a
// `return`'s, a map, range, vector and set literal's, a spread and an
// element beside one (spreads first), a struct update's field, `is`, `as`, a
// named argument, a pipe stage's argument and a field's object.
func TestIRDivergedArms_EveryOperandPosition(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io

struct Pt
    x: Int
    y: Int
end

fn n(i: Int) -> Int
    io.print("n${i}")
    i
end

fn note(a: Int, b: Int) -> Unit
    io.print("note ${a} ${b}")
end

fn pair(a: Int, b: Int) -> (Int, Int)
    (a, b)
end

fn wrap(a: Int) -> Option<Int>
    Some(a)
end

fn more(a: Int) -> List<Int>
    [a]
end

fn base(a: Int) -> Pt
    Pt{x: a, y: a}
end

fn add(a: Int, b: Int) -> Int
    a + b
end

fn interp(c: Bool) -> Int
    _s = "a ${n(1)} ${if c then return 10 else return 20} ${n(2)}"
end

fn debug(c: Bool) -> Int
    dbg(if c then return 11 else return 21)
end

fn deferred(c: Bool) -> Int
    defer note(n(1), if c then return 12 else return 22)
end


fn tuple(c: Bool) -> Int
    (_a, _b) = pair(n(1), if c then return 14 else return 24)
end

fn pattern(c: Bool) -> Int
    Some(_v) = wrap(if c then return 15 else return 25) else return 0
end

fn returned(c: Bool) -> Int
    return n(1) + (if c then return 16 else return 26)
end

fn map_lit(c: Bool) -> Int
    _m = {n(1) => n(2), n(3) => if c then return 17 else return 27}
end

fn range_lit(c: Bool) -> Int
    _r = n(1)..(if c then return 18 else return 28)
end

fn vector_lit(c: Bool) -> Int
    _v = Vector[n(1), if c then return 19 else return 29, n(2)]
end

fn set_lit(c: Bool) -> Int
    _v = Set[n(1), if c then return 30 else return 40]
end

fn spread_item(c: Bool) -> Int
    _v = [n(1), ..more(n(2)), if c then return 31 else return 41]
end

fn spread_head(c: Bool) -> Int
    _v = [n(1), ..more(if c then return 32 else return 42), n(2)]
end

fn spread_order() -> Int
    v = [n(1), ..more(n(2)), n(3)]
    List.length(v)
end

fn struct_spread(c: Bool) -> Int
    _p = {..base(n(1)), y: if c then return 33 else return 43}
end

fn is_test(c: Bool) -> Int
    _b = wrap(if c then return 34 else return 44) is Some(_)
end

fn ascribed(c: Bool) -> Int
    _v = add(n(1), (if c then return 35 else return 45) as Int)
end

fn named(c: Bool) -> Int
    add(b: n(1), a: if c then return 36 else return 46)
end

fn piped(c: Bool) -> Int
    n(1) |> add(if c then return 37 else return 47)
end

fn unary(c: Bool) -> Int
    -add(n(1), if c then return 38 else return 48)
end

fn field(c: Bool) -> Int
    base(if c then return 39 else return 49).x
end

fn nested(c: Bool) -> Int
    _s = "a ${add(n(1), dbg(if c then return 50 else return 60))}"
end

fn case_region(k: Int) -> Int
    _s = "${case k
        0 -> return 51
        _ -> return 61
    end}"
end

fn main()
    io.print(interp(True))
    io.print(interp(False))
    io.print(debug(True))
    io.print(deferred(False))
    io.print(tuple(True))
    io.print(pattern(False))
    io.print(returned(True))
    io.print(map_lit(True))
    io.print(range_lit(False))
    io.print(vector_lit(True))
    io.print(set_lit(True))
    io.print(spread_item(True))
    io.print(spread_head(True))
    io.print(spread_order())
    io.print(struct_spread(True))
    io.print(is_test(True))
    io.print(ascribed(True))
    io.print(named(True))
    io.print(piped(True))
    io.print(unary(True))
    io.print(field(True))
    io.print(nested(True))
    io.print(case_region(0))
    io.print(case_region(1))
end
`
	irRunSource(t, src, "n1\n"+
		"10\n"+
		"n1\n"+
		"20\n"+
		"11\n"+
		"n1\n"+
		"22\n"+
		"n1\n"+
		"14\n"+
		"25\n"+
		"n1\n"+
		"16\n"+
		"n1\n"+
		"n2\n"+
		"n3\n"+
		"17\n"+
		"n1\n"+
		"28\n"+
		"n1\n"+
		"19\n"+
		"n1\n"+
		"30\n"+
		"n2\n"+
		"n1\n"+
		"31\n"+
		"32\n"+
		"n2\n"+
		"n1\n"+
		"n3\n"+
		"3\n"+
		"n1\n"+
		"33\n"+
		"34\n"+
		"n1\n"+
		"35\n"+
		"n1\n"+
		"36\n"+
		"n1\n"+
		"37\n"+
		"n1\n"+
		"38\n"+
		"39\n"+
		"n1\n"+
		"50\n"+
		"51\n"+
		"61\n")
}

// A `with` whose value holds an `if` every arm of which returns overrides
// nothing: the function returns from the arm.
func TestIRDivergedArms_WithValue(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io

struct App
    greeting: String
end

fn boot() -> Result<App, String>
    Ok(App{greeting: "hello"})
end

fn pick(c: Bool) -> String
    with App.greeting = "x ${if c then return "left" else return "right"}"
end

fn main()
    io.print(pick(True))
    io.print(pick(False))
end
`
	irRunSource(t, src, "left\n"+
		"right\n")
}

// A callee that is an expression is evaluated before a leaving argument,
// for its effects, and a leaving region inside it is lifted as one in an
// argument is: a call of a call, of a field of a call and of a grouped
// call. A callee that is a name evaluates nothing.
func TestIRDivergedArms_CalleeBeforeALeavingArgument(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io

struct Table
    f: (Int) -> Int
end

fn n(i: Int) -> Int
    io.print("n${i}")
    i
end

fn pick(i: Int) -> (Int) -> Int
    io.print("pick ${i}")
    fn x -> x + i
end

fn table(i: Int) -> Table
    io.print("table ${i}")
    Table{f: fn x -> x * i}
end

fn computed(c: Bool) -> Int
    pick(n(1))(if c then return 70 else return 80)
end

fn in_callee(c: Bool) -> Int
    pick(if c then return 71 else return 81)(n(2))
end

fn field(c: Bool) -> Int
    table(3).f(if c then return 72 else return 82)
end

fn grouped(c: Bool) -> Int
    (pick(4))(if c then return 73 else return 83)
end

fn named(c: Bool) -> Int
    n(if c then return 74 else return 84)
end


fn main()
    io.print(computed(True))
    io.print(in_callee(False))
    io.print(field(True))
    io.print(named(False) + grouped(True))
    io.print(pick(1)(2))
    io.print(table(3).f(4))
end
`
	irRunSource(t, src, "n1\n"+
		"pick 1\n"+
		"70\n"+
		"81\n"+
		"table 3\n"+
		"72\n"+
		"pick 4\n"+
		"157\n"+
		"pick 1\n"+
		"3\n"+
		"table 3\n"+
		"12\n")
}

// A body whose last statement leaves through an operand needs no result
// after it: a `defer`, a call, a binding and a destructure with an operand
// every arm of which returns. The operands before it run, a `defer`
// registered earlier runs as the function leaves, and the `defer` whose
// argument left registers nothing.
func TestIRDivergedArms_AnOperandOfTheLastStatement(t *testing.T) {
	verifyLambdaProgram(t, `import std::io

fn note(n: Int)
    io.print("note ${n}")
end

fn n(k: Int) -> Int
    io.print("n ${k}")
    k
end

fn pair(a: Int, b: Int) -> (Int, Int)
    (a, b)
end

fn deferred(c: Bool) -> Int
    defer note(if c then return 1 else return 2)
end

fn deferred_case(c: Bool) -> Int
    defer note(case c
        True -> return 3
        False -> return 4
    end)
end

fn after_a_defer(c: Bool) -> Int
    defer note(100)
    defer note(n(1) + if c then return 5 else return 6)
end

fn called(c: Bool) -> Int
    note(if c then return 7 else return 8)
end

fn bound(c: Bool) -> Int
    _x = n(if c then return 9 else return 10)
end

fn destructured(c: Bool) -> Int
    (_a, _b) = pair(n(2), if c then return 11 else return 12)
end

fn in_a_branch(c: Bool) -> Int
    if c then
        note(if c then return 13 else return 14)
    else
        return 15
    end
end

fn main()
    io.print(deferred(True))
    io.print(deferred(False))
    io.print(deferred_case(True))
    io.print(after_a_defer(False))
    io.print(called(True))
    io.print(bound(False))
    io.print(destructured(True))
    io.print(in_a_branch(True))
    io.print(in_a_branch(False))
end
`, "1\n2\n3\nn 1\nnote 100\n6\n7\n10\nn 2\n11\n13\n15\n")
}

// A branch or an arm whose last statement leaves through an operand it
// always evaluates leaves, so an `if` or a `case` of such branches is a
// region that leaves: as a statement, as a binding's value, and with the
// operand as its own condition. The checker's answer (analysis, leaves) is
// the one the builder reads.
func TestIRDivergedArms_BranchEndingInALeavingOperand(t *testing.T) {
	verifyLambdaProgram(t, `import std::io

fn close(n: Int)
    io.print("close ${n}")
end

fn branch(c: Bool) -> Int
    if c then
        close(if c then return 3 else return 30)
    else
        return 4
    end
end

fn arm(n: Int) -> Int
    case n
        0 -> close(if n == 0 then return 5 else return 50)
        _ ->
            defer close(if n > 1 then return 6 else return 60)
    end
end

fn bound(c: Bool) -> Int
    _y = if c then
        close(if c then return 7 else return 70)
    else
        return 8
    end
end

fn cond(c: Bool) -> Int
    if (if c then return 9 else return 90) then
        1
    else
        2
    end
end

fn main()
    io.print(branch(True))
    io.print(branch(False))
    io.print(arm(0))
    io.print(arm(1))
    io.print(arm(2))
    io.print(bound(True))
    io.print(bound(False))
    io.print(cond(True))
    io.print(cond(False))
end
`, "3\n4\n5\n60\n6\n7\n8\n9\n90\n")
}
