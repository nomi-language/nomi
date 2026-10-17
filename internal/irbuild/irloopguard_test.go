package irbuild

import "testing"

// A guard's condition, and a `break` or `return` value, may branch: `and`
// and `or` short-circuit through blocks of their own, and the guard tests
// the value where they join.
func TestIRLoop_GuardConditionsThatBranch(t *testing.T) {
	verifyLambdaProgram(t, `import {
    std/channels.{Channel, Receiver, Sender}
    std/io
}

fn fill(xs: List<Int>) -> Receiver<Int> {
    ch: Channel<Int> = Channel.buffered(10)
    Iter.each(xs, fn x -> {
        _ = Sender.send(ch.sender, x)
    })
    Sender.close(ch.sender)
    ch.receiver
}

fn small(n: Int) -> Bool {
    n < 3
}

fn newton(n: Float) -> Float {
    #(_, guess) = Iter.loop(fn state = #(n / 2.0, 0.0) -> {
        #(guess, prev) = state
        diff = guess - prev
        if diff > -0.0001 and diff < 0.0001 {
            break state
        }
        next = (guess + n / guess) / 2.0
        #(next, guess)
    })
    guess
}

fn main() {
    io.inspect(newton(2.0))
    // or
    a = Iter.loop(fn i = 0 -> {
        if i > 100 or i * i > 30 { break i }
        i + 1
    })
    io.inspect(a)
    // not, and nested parentheses
    b = Iter.loop(fn i = 0 -> {
        if !(i < 4 or (i == 5 and !(i > 9))) { break i }
        i + 1
    })
    io.inspect(b)
    // a call in the condition, beside and
    c = Iter.loop(fn i = 10 -> {
        if small(i) and i != 2 { break i }
        i - 1
    })
    io.inspect(c)
    // return guards with branching conditions, and a
    // statement in the guard's arm that branches
    d = Iter.loop(fn i = 0 -> {
        if i == 2 or i == 4 {
            even = i > 0 and i % 2 == 0
            io.print("skip ${i} ${even}")
            return i + 1
        }
        if i > 6 and small(0) { break i * 10 }
        if !small(i) and i == 5 { return i + 1 }
        i + 1
    })
    io.inspect(d)
    // a Bool state whose break value branches
    e = Iter.loop(fn done = False -> {
        if !done { return True }
        break done and small(1)
    })
    io.inspect(e)
    // in a lambda
    f = fn limit: Int -> Iter.loop(fn i = 0 -> {
        if (i >= limit) or (small(i) and i == limit) { break i }
        i + 1
    })
    io.inspect(f(4))
    // a continue guard with a branching condition
    inbox = fill([1, 2, 3, 4])
    Iter.loop(fn -> {
        Some(n) = Receiver.receive(inbox) else { break }
        if n == 1 or (n == 3 and !small(n)) { continue }
        io.print("kept ${n}")
    })
}
`, "1.4142156862745097\n6\n4\n1\nskip 2 True\nskip 4 True\n70\nTrue\n4\nkept 2\nkept 4\n")
}
