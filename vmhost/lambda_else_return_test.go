package vmhost_test

import (
	"strings"
	"testing"
)

// A bare `return` in a lambda leaves the lambda wherever it ends a path: the
// else of a binding whose pattern falls back to a payload
// (`Some(v) = m else { return }`, as a block or an arm), an `if` guard, a
// `case` arm. The fallback else is a region of the payload's kind, not of the
// lambda's Unit result, and the builder declined the bare `return` there.
// Each shape runs in an Iter.each, Iter.map, Iter.filter, Iter.loop or
// Maybe.map callback, a nested lambda, or a lambda bound to a name and
// called.
func TestLambdaElseReturn_RunsInEveryCallback(t *testing.T) {
	got := runSourceOutput(t, `import std::io

fn a()
    Iter.each([1.5, -2.0], fn f ->
        Some(d) = Float.to_decimal(f, 1, .HalfUp) else
            return Unit
        end
        io.print("a ${d}")
    end)
end

fn b()
    xs = [Some(1), None, Some(3)]
        |> Iter.map(fn m ->
            Some(v) = m else
                return 0
            end
            v * 10
        end)
        |> Iter.to_list()
    io.print("b ${xs}")
end

fn c()
    Iter.each([1, 2, 3], fn n ->
        if n == 2 then
            io.print("c skip")
            return
        end
        io.print("c ${n}")
    end)
end

fn d()
    Iter.each([[Some(1), None], [Some(2)]], fn row ->
        Iter.each(row, fn m ->
            Some(v) = m else
                return
            end
            io.print("d ${v}")
        end)
    end)
end

fn e()
    show = fn m: Maybe<Int> ->
        Some(v) = m else
            return
        end
        io.print("e ${v}")
    end
    show(Some(7))
    show(None)
end

fn f()
    ys = [Some(1), None, Some(4)]
        |> Iter.filter(fn m ->
            Some(v) = m else
                return False
            end
            v > 2
        end)
        |> Iter.count()
    io.print("f ${ys}")
end

fn g()
    z = Maybe.map(Some(Some(5)), fn m ->
        Some(v) = m else
            return 0
        end
        v + 1
    end)
    io.print("g ${z}")
end

fn h()
    r = Iter.loop(0, fn i ->
        if i >= 4 then
            break i
        end
        Some(v) = (if i % 2 == 0 then Some(i) else None) else
            return i + 1
        end
        io.print("h ${v}")
        i + 1
    end)
    io.print("h done ${r}")
end

fn i()
    Iter.each([Some(1), None], fn m ->
        case m
            Some(v) -> io.print("i ${v}")
            None -> return
        end
        io.print("i after")
    end)
end

fn j()
    Iter.each([Some(1), None], fn m ->
        Some(v) = m else
            io.print("j none")
            return
        end
        io.print("j ${v}")
    end)
end

fn k()
    Iter.each([Ok(1), Err("x")], fn r ->
        Ok(v) = r else
            Err(e) ->
                io.print("k ${e}")
                return
        end
        io.print("k ${v}")
    end)
end

fn main()
    a()
    b()
    c()
    d()
    e()
    f()
    g()
    h()
    i()
    j()
    k()
end
`)
	want := strings.Join([]string{
		"a 1.5", "a -2.0",
		"b [10, 0, 30]",
		"c 1", "c skip", "c 3",
		"d 1", "d 2",
		"e 7",
		"f 1",
		"g Some(6)",
		"h 0", "h 2", "h done 4",
		"i 1", "i after",
		"j 1", "j none",
		"k 1", "k x",
	}, "\n") + "\n"
	if got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}

// The reported program: a Decimal conversion whose else leaves the Iter.each
// callback.
func TestLambdaElseReturn_DecimalFromFloat(t *testing.T) {
	got := runSourceOutput(t, `import std::io

fn main()
    Iter.each([212.0, 33.8, 2.675, -0.05], fn f ->
        Some(d) = Float.to_decimal(f, 1, .HalfUp) else
            return
        end
        io.print("${f} -> ${d}")
    end)
end
`)
	want := "212.0 -> 212.0\n33.8 -> 33.8\n2.675 -> 2.7\n-0.05 -> -0.1\n"
	if got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}
