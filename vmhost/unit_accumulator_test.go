package vmhost_test

import "testing"

// Unit is a value like any other where an Iter function takes a starting
// value, a seed or an element: a fold over Unit (`try_reduce`, `reduce`,
// `scan`, `map_reduce`, `loop`), a source built from one (`repeat`,
// `iterate`) and a std body instantiated at it (`intersperse`, `contains`).
func TestRun_IterFunctionsTakeUnitAsTheirValue(t *testing.T) {
	out := runOutput(t, `import std::io

fn check(n: Int) -> Result<Unit, String> {
    if n > 5 {
        Err("big ${n}")
    } else {
        Ok(Unit)
    }
}

fn main() {
    io.inspect(Iter.try_reduce([1, 2], Unit, fn _, n -> check(n)))
    io.inspect(Iter.try_reduce([1, 9, 3], Unit, fn _, n -> check(n)))
    io.inspect(Iter.reduce([1, 2], Unit, fn u, n -> {
        io.print("saw ${n}")
        u
    }))
    io.inspect(Iter.reduce([1, 2, 3], Unit, fn u, n -> if n == 2 { break } else { u }))
    io.inspect(Iter.scan([1, 2], Unit, fn u, _ -> u) |> Iter.to_list())
    io.inspect(Iter.map_reduce([1, 2], Unit, fn u, n -> (u, n * 10)))
    io.inspect(Iter.loop(Unit, fn u -> { break u }))
    io.inspect(Iter.repeat(Unit) |> Iter.take(2) |> Iter.to_list())
    io.inspect(Iter.iterate(Unit, fn u -> u) |> Iter.take(3) |> Iter.to_list())
    io.inspect(Iter.intersperse([Unit, Unit], Unit) |> Iter.to_list())
    io.inspect(Iter.contains([Unit], Unit))
}
`)
	const want = `Ok(Unit)
Err("big 9")
saw 1
saw 2
Unit
Unit
[Unit, Unit]
(Unit, [10, 20])
Unit
[Unit, Unit]
[Unit, Unit, Unit]
[Unit, Unit, Unit]
True
`
	if out != want {
		t.Fatalf("output:\n%s\nwant:\n%s", out, want)
	}
}
