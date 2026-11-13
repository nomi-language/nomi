package irbuild

import "testing"

// An `Iter.loop` exit's value enters the state type: a `break v`, a guard's
// `return v` and the tail, each a concrete value where the state is an
// interface. A bare `return` in a loop of Unit state goes round again with
// Unit, from a guard, a pattern binding's else and a `case` arm.
func TestIRLoopExitEntersTheState_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io

fn main()
    d: Display = 1
    a = Iter.loop(d, fn x ->
        if Display.to_string(x) == "2" then
            break "two"
        end
        if Display.to_string(x) == "1" then
            return 2
        end
        3
    end)
    io.print(Display.to_string(a))
    n = Iter.loop(0, fn i ->
        if i > 3 then
            break i
        end
        i + 1
    end)
    Iter.loop(Unit, fn _ ->
        if n > 5 then
            return
        end
        io.print("guard")
        break
    end)
    m: Maybe<Int> = Some(n)
    Iter.loop(Unit, fn _ ->
        Some(v) = m else
            return
        end
        k = case v
            4 -> break
            5 -> return
            _ -> 1
        end
        io.inspect(k)
    end)
    io.print("done")
end
`
	irRunSource(t, src, "two\nguard\ndone\n")
}

// A binding else in an `Iter.loop` body falls back with Unit where the
// payload is Unit, from a straight value and from an `if` region.
func TestIRLoopElseFallsBackWithUnit_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io

fn main()
    r: Result<Unit, String> = Err("x")
    n = Iter.loop(0, fn i ->
        if i > 1 then
            break i
        end
        Ok(v) = r else
            io.print("fallback")
            Unit
        end
        io.inspect(v)
        i + 1
    end)
    io.inspect(n)
    m = Iter.loop(0, fn i ->
        if i > 0 then
            break i
        end
        Ok(v) = r else
            if i > 5 then
                break 9
            else
                Unit
            end
        end
        io.inspect(v)
        i + 1
    end)
    io.inspect(m)
end
`
	irRunSource(t, src, "fallback\nUnit\nfallback\nUnit\n2\nUnit\n1\n")
}
