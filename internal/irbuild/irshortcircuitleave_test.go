package irbuild

import "testing"

// A right operand of `and` or `or` that leaves runs as the `if` the
// operator is: `ready? or { return Err(..) }` in a Result function, `x and {
// return 1 }`, a `case` whose arms all leave, the same `case` as a call's
// argument, in parentheses and under `!`, a chain `a or b or { return c }`, a `try` that fails, and
// `continue` and `break` in an `Iter.map` callback. Where the operand does
// not run, the value is the left operand's.
func TestIRShortCircuitLeavingOperand_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

fn check(ready?: Bool) -> Result<Int, String> {
    ok = ready? or {
        return Err("x")
    }
    Ok(if ok { 1 } else { 0 })
}

fn early(x: Bool) -> Int {
    y = x and {
        return 1
    }
    if y { 3 } else { 2 }
}

fn both(x: Int) -> Bool {
    x > 0 and case x {
        1 -> return True
        _ -> return False
    }
}

fn id(b: Bool) -> Bool {
    b
}

fn wrapped(x: Int) -> Bool {
    x > 0 and id(case x {
        1 -> return True
        _ -> return False
    })
}

fn grouped(x: Int) -> Bool {
    x > 0 and (case x {
        1 -> return True
        _ -> return False
    })
}

fn negated(x: Int) -> Bool {
    x > 0 and !case x {
        1 -> return True
        _ -> return False
    }
}

fn either(x: Int) -> Int {
    b = x > 0 or (if x == 0 { return 7 } else { return 8 })
    if b { 1 } else { 2 }
}

fn chain(a: Bool, b: Bool) -> String {
    some = a or b or {
        return "neither"
    }
    "some ${some}"
}

fn half(n: Int) -> Result<Int, String> {
    if n % 2 == 0 { Ok(n / 2) } else { Err("odd ${n}") }
}

fn halves(n: Int) -> Result<Bool, String> {
    ok = n > 0 and try half(n) > 0
    Ok(ok)
}

fn main() {
    io.inspect(check(True))
    io.inspect(check(False))
    io.inspect(early(True))
    io.inspect(early(False))
    io.inspect(both(1))
    io.inspect(both(2))
    io.inspect(both(0))
    io.inspect(wrapped(1))
    io.inspect(wrapped(0))
    io.inspect(grouped(1))
    io.inspect(negated(2))
    io.inspect(either(0))
    io.inspect(either(-1))
    io.inspect(either(3))
    io.inspect(chain(False, True))
    io.inspect(chain(False, False))
    io.inspect(halves(0))
    io.inspect(halves(3))
    kept = Iter.map([1, 2, 3, 4, 5], fn n -> {
        _ = n != 2 or {
            continue
        }
        _ = n < 4 or {
            break
        }
        n * 10
    }) |> Iter.to_list()
    io.inspect(kept)
}
`
	const want = "Ok(1)\n" +
		"Err(\"x\")\n" +
		"1\n" +
		"2\n" +
		"True\n" +
		"False\n" +
		"False\n" +
		"True\n" +
		"False\n" +
		"True\n" +
		"False\n" +
		"7\n" +
		"8\n" +
		"1\n" +
		"\"some True\"\n" +
		"\"neither\"\n" +
		"Ok(False)\n" +
		"Err(\"odd 3\")\n" +
		"[10, 30]\n"
	irRunSource(t, src, want)
}
