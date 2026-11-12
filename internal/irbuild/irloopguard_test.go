package irbuild

import "testing"

// A guard's condition, and a `break` or `return` value, may branch: `and`
// and `or` short-circuit through blocks of their own, and the guard tests
// the value where they join.
func TestIRLoop_GuardConditionsThatBranch(t *testing.T) {
	verifyLambdaProgram(t, `import
    std::channels.{Channel, Receiver, Sender}
    std::io
end

fn fill(xs: List<Int>) -> Receiver<Int>
    ch: Channel<Int> = Channel.buffered(10)
    Iter.each(xs, fn x ->
        _ = Sender.send(ch.sender, x)
    end)
    Sender.close(ch.sender)
    ch.receiver
end

fn small(n: Int) -> Bool
    n < 3
end

fn newton(n: Float) -> Float
    (_, guess) = Iter.loop((n / 2.0, 0.0), fn state ->
        (guess, prev) = state
        diff = guess - prev
        if diff > -0.0001 and diff < 0.0001 then
            break state
        end
        next = (guess + n / guess) / 2.0
        (next, guess)
    end)
    guess
end

fn main()
    io.inspect(newton(2.0))
    // or
    a = Iter.loop(0, fn i ->
        if i > 100 or i * i > 30 then break i
        i + 1
    end)
    io.inspect(a)
    // not, and nested parentheses
    b = Iter.loop(0, fn i ->
        if not (i < 4 or (i == 5 and not (i > 9))) then break i
        i + 1
    end)
    io.inspect(b)
    // a call in the condition, beside and
    c = Iter.loop(10, fn i ->
        if small(i) and i != 2 then break i
        i - 1
    end)
    io.inspect(c)
    // return guards with branching conditions, and a
    // statement in the guard's arm that branches
    d = Iter.loop(0, fn i ->
        if i == 2 or i == 4 then
            even = i > 0 and i % 2 == 0
            io.print("skip ${i} ${even}")
            return i + 1
        end
        if i > 6 and small(0) then break i * 10
        if not small(i) and i == 5 then return i + 1
        i + 1
    end)
    io.inspect(d)
    // a Bool state whose break value branches
    e = Iter.loop(False, fn done ->
        if not done then return True
        break done and small(1)
    end)
    io.inspect(e)
    // in a lambda
    f = fn limit: Int -> Iter.loop(0, fn i ->
        if (i >= limit) or (small(i) and i == limit) then break i
        i + 1
    end)
    io.inspect(f(4))
    // a continue guard with a branching condition
    inbox = fill([1, 2, 3, 4])
    Iter.loop(fn ->
        Some(n) = Receiver.receive(inbox) else
            break
        end
        if n == 1 or (n == 3 and not small(n)) then continue
        io.print("kept ${n}")
    end)
end
`, "1.4142156862745097\n6\n4\n1\nskip 2 True\nskip 4 True\n70\nTrue\n4\nkept 2\nkept 4\n")
}
