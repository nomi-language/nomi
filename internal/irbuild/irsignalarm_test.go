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
	verifyLambdaProgram(t, `import std::io

fn main()
    xs = [1, 2, 3, 4, 5, 6]
    // map: a block arm and a bare arm that continue.
    a = Iter.map(xs, fn x ->
        case x % 3
            0 ->
                io.print("skip ${x}")
                continue
            1 -> continue
            _ -> x * 10
        end
    end) |> Iter.to_list()
    io.inspect(a)
    // map: a value 'if' whose branches continue and break.
    b = Iter.map(xs, fn x -> if x == 2 then continue else if x == 5 then break else x) |> Iter.to_list()
    io.inspect(b)
    // map: a case nested in an if branch, with 'break v'.
    c = Iter.map(xs, fn x ->
        if x > 2 then
            case x
                4 -> break 40
                _ -> x
            end
        else
            0 - x
        end
    end) |> Iter.to_list()
    io.inspect(c)
    // map: the signal in a binding.
    d = Iter.map(xs, fn x ->
        y = case x % 2
            0 -> continue
            _ -> x + 100
        end
        y * 2
    end) |> Iter.to_list()
    io.inspect(d)
    e = Iter.map(xs, fn x ->
        y = if x > 3 then break x * 1000 else x
        y + 1
    end) |> Iter.to_list()
    io.inspect(e)
    // filter.
    f = Iter.filter(xs, fn x ->
        case x
            2 -> continue
            5 -> break
            _ -> x % 2 == 0 or x == 1
        end
    end) |> Iter.to_list()
    io.inspect(f)
    g = Iter.filter(xs, fn x -> if x == 4 then break True else x % 2 == 1) |> Iter.to_list()
    io.inspect(g)
    // take_while.
    h = Iter.take_while(xs, fn x ->
        case x
            2 -> continue
            _ -> x < 4
        end
    end) |> Iter.to_list()
    io.inspect(h)
    // each.
    Iter.each(xs, fn x ->
        case x % 2
            0 -> continue
            _ ->
                if x == 5 then break
                io.print("each ${x}")
        end
    end)
    Iter.each(xs, fn x ->
        case x
            3 -> break
            _ -> io.print("each2 ${x}")
        end
    end)
    // iterate.
    i = Iter.iterate(1, fn n ->
        case n > 20
            True -> break
            False -> n * 3
        end
    end) |> Iter.to_list()
    io.inspect(i)
    j = Iter.iterate(1, fn n -> if n > 5 then break n * 100 else n + 2) |> Iter.to_list()
    io.inspect(j)
    // reduce.
    k = Iter.reduce(xs, 0, fn acc, x ->
        case x % 2
            0 ->
                io.print("odd only ${x}")
                continue
            _ -> if x == 5 then break acc * 100 else acc + x
        end
    end)
    io.inspect(k)
    l = Iter.reduce(xs, 0, fn acc, x ->
        step = case x
            3 -> continue
            6 -> break
            _ -> x
        end
        acc + step
    end)
    io.inspect(l)
end
`, "skip 3\nskip 6\n[20, 50]\n[1, 3, 4]\n[-1, -2, 3, 40]\n[202, 206, 210]\n"+
		"[2, 3, 4, 4000]\n[1, 4]\n[1, 3, 4]\n[1, 3]\neach 1\neach 3\neach2 1\neach2 2\n[1, 3, 9]\n[1, 3, 5, 700]\n"+
		"odd only 2\nodd only 4\n400\n12\n")
}

// A named function passed as the callback signals from an arm as a lambda
// does.
func TestIRSignalArm_NamedCallbacks(t *testing.T) {
	verifyLambdaProgram(t, `import std::io

fn tens(n: Int) -> Int
    case n % 3
        0 ->
            io.print("skip ${n}")
            continue
        _ -> if n > 4 then break n * 100 else n * 10
    end
end

fn evens_until_five(n: Int) -> Bool
    keep = case n
        5 -> break
        _ -> n % 2 == 0
    end
    keep
end

fn small(n: Int) -> Bool
    if n == 2 then continue else n < 4
end

fn show(n: Int) -> Unit
    case n
        2 -> continue
        4 -> break
        _ -> io.print("show ${n}")
    end
end

fn triple(n: Int) -> Int
    case n > 20
        True -> break
        False -> n * 3
    end
end

fn add_odd(acc: Int, n: Int) -> Int
    case n % 2
        0 -> continue
        _ -> if n == 5 then break acc * 100 else acc + n
    end
end

fn main()
    xs = [1, 2, 3, 4, 5, 6]
    io.inspect(Iter.map(xs, tens) |> Iter.to_list())
    io.inspect(Iter.filter(xs, evens_until_five) |> Iter.to_list())
    io.inspect(Iter.take_while(xs, small) |> Iter.to_list())
    Iter.each(xs, show)
    io.inspect(Iter.iterate(1, triple) |> Iter.to_list())
    io.inspect(Iter.reduce(xs, 0, add_odd))
end
`, "skip 3\n[10, 20, 40, 500]\n[2, 4]\n[1, 3]\nshow 1\nshow 3\n[1, 3, 9]\n400\n")
}

// In an inline Iter.loop every `break`, `return` and `continue` is the
// loop's: an arm ending in `continue` goes round again with the state
// unchanged, `return v` makes v the next state, and `break v` ends the loop.
// The channel feeds each iteration a new input, so a `continue` makes
// progress. early_state's `return 5` used to leave early_state itself.
func TestIRSignalArm_IterLoop(t *testing.T) {
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

fn deposit(account: Account, input: Receiver<String>) -> Account
    Iter.loop(account, fn acc ->
        if acc.balance > 100 then break acc
        text = case Receiver.receive(input)
            Some(t) -> t
            None -> break acc
        end
        case String.to_int(text)
            Some(n) -> {..acc, balance: acc.balance + n}
            None ->
                io.print("not a number: ${text}")
                continue
        end
    end)
end

fn bare_arm(input: Receiver<String>) -> Int
    Iter.loop(0, fn total ->
        case Receiver.receive(input)
            None -> break total
            Some("skip") -> continue
            Some("stop") -> break total * 10
            Some(t) -> total + String.length(t)
        end
    end)
end

fn if_branch(input: Receiver<String>) -> Int
    Iter.loop(0, fn n ->
        t = case Receiver.receive(input)
            Some(t) -> t
            None -> break n
        end
        if t == "x" then continue else if t == "end" then break n + 1000 else n + 1
    end)
end

fn nested(input: Receiver<String>) -> Int
    Iter.loop(0, fn n ->
        t = Receiver.receive(input)
        if n < 100 then
            case t
                Some("again") -> continue
                Some(_) -> n + 1
                None -> break n
            end
        else
            break n
        end
    end)
end

fn bound(input: Receiver<String>) -> Int
    Iter.loop(0, fn n ->
        step = case Receiver.receive(input)
            Some("x") -> continue
            Some("next") -> return n + 100
            Some(t) -> String.length(t)
            None -> break n
        end
        n + step
    end)
end

fn arm_guard(input: Receiver<String>) -> Int
    Iter.loop(0, fn n ->
        case Receiver.receive(input)
            Some(t) ->
                if t == "x" then continue
                if t == "big" then return n + 50
                n + 1
            None -> break n
        end
    end)
end

fn early_state() -> Int
    n = Iter.loop(0, fn i ->
        case i
            0 -> return 5
            5 -> break i * 100
            _ -> i + 1
        end
    end)
    io.print("after the loop")
    n + 1
end

fn main()
    a = deposit({owner: "ann", balance: 10}, feed(["5", "five", "40", "x", "70", "1"]))
    io.inspect(a)
    io.inspect(bare_arm(feed(["ab", "skip", "cde", "skip"])))
    io.inspect(bare_arm(feed(["ab", "skip", "stop", "zzz"])))
    io.inspect(if_branch(feed(["a", "x", "b", "x"])))
    io.inspect(if_branch(feed(["a", "x", "end", "b"])))
    io.inspect(nested(feed(["a", "again", "b"])))
    io.inspect(bound(feed(["ab", "x", "next", "c"])))
    io.inspect(arm_guard(feed(["a", "x", "big", "b"])))
    io.inspect(early_state())
end
`, "not a number: five\nnot a number: x\nAccount{owner: \"ann\", balance: 125}\n"+
		"5\n20\n2\n1001\n2\n103\n52\nafter the loop\n501\n")
}

// A statement in an Iter.loop body may branch: a statement `if`, a block
// holding a guard, a binding whose arm leaves the iteration from below
// another arm, and a region inside the tail's operand.
func TestIRSignalArm_IterLoopBranchingStatements(t *testing.T) {
	verifyLambdaProgram(t, `import std::io

fn stmt_if() -> Int
    Iter.loop(0, fn n ->
        if n % 2 == 0 then io.print("even ${n}") else io.print("odd ${n}")
        if n > 2 then break n
        n + 1
    end)
end

fn deep() -> Int
    Iter.loop(0, fn n ->
        case n
            0 ->
                y = case n + 1
                    1 -> return 3
                    _ -> 0
                end
                y
            3 ->
                z = if n > 1 then break n * 7 else n
                z
            _ -> n
        end
    end)
end

fn calls(n: Int) -> Int
    n + 1
end

fn operand() -> Int
    Iter.loop(0, fn n ->
        if n > 3 then break n
        calls(case n
            1 -> return 3
            _ -> n
        end)
    end)
end

fn main()
    io.inspect(stmt_if())
    io.inspect(deep())
    io.inspect(operand())
end
`, "even 0\nodd 1\neven 2\nodd 3\n3\n21\n4\n")
	verifyLambdaProgram(t, `import std::io

fn f() -> Int
    Iter.loop(0, fn n ->
        begin
            if n == 1 then return n + 5
            io.print("at ${n}")
        end
        begin
            if n > 6 then break n
        end
        n + 1
    end)
end

fn main()
    io.inspect(f())
end
`, "at 0\nat 6\nat 7\n7\n")
}
