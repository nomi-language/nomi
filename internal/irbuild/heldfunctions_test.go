package irbuild

import "testing"

// Function values are elements a List, a Vector and a Map hold, and every
// Iter stage that builds a container or a tuple from them runs: `concat`,
// `zip` (on either side), `chunks`, `flatten`, `flat_map`, `to_vector`,
// `to_map`, `group_by`, `repeat` and `iterate`, a vector and a map literal
// of lambdas read back with `Vector.get` and `Map.get`, Debug over each, a
// list spread over a list of functions, and a list pattern over one.
func TestIRHeldFunctionValues_Run(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

fn size(xs: List<(Int) -> Int>) -> Int {
    case xs {
        [] -> 0
        [_, ..rest] -> 1 + size(rest)
    }
}

fn main() {
    fs = [fn x: Int -> x + 1, fn x: Int -> x * 2]
    io.inspect(fs)
    io.inspect(size([fn x: Int -> x, ..fs]))
    io.inspect(Iter.concat(fs, fs) |> Iter.map(fn f -> f(1)) |> Iter.to_list())
    io.inspect(Iter.zip([1, 2], fs) |> Iter.map(fn #(n, f) -> f(n)) |> Iter.to_list())
    io.inspect(Iter.chunks(fs, 1) |> Iter.map(fn c -> Iter.count(c)) |> Iter.to_list())
    io.inspect(Iter.flatten([fs, fs]) |> Iter.map(fn f -> f(3)) |> Iter.to_list())
    io.inspect([1, 2] |> Iter.flat_map(fn n -> [fn y: Int -> y + n]) |> Iter.map(fn f -> f(10)) |> Iter.to_list())
    v = Iter.to_vector(fs)
    io.inspect(v)
    io.inspect(Vector.length(v))
    case Vector.get(v, 1) {
        Some(f) -> io.inspect(f(7))
        None -> io.inspect(0)
    }
    m = Iter.to_map([#(1, fn x: Int -> x), #(2, fn x: Int -> x * 3)])
    io.inspect(m)
    case Map.get(m, 2) {
        Some(f) -> io.inspect(f(7))
        None -> io.inspect(0)
    }
    ops = %{"inc" => fn x: Int -> x + 1}
    case Map.get(ops, "inc") {
        Some(f) -> io.inspect(f(1))
        None -> io.inspect(0)
    }
    g = Iter.group_by(fs, fn f -> f(2))
    io.inspect(Map.keys(g))
    io.inspect(Iter.repeat(fn x: Int -> x + 1) |> Iter.take(3) |> Iter.map(fn f -> f(1)) |> Iter.to_list())
    io.inspect(Iter.iterate(fn x: Int -> x, fn f -> fn x: Int -> f(x) + 1) |> Iter.take(3) |> Iter.map(fn f -> f(0)) |> Iter.to_list())
    vv = #[fn x: Int -> x + 1, fn x: Int -> x * 2]
    io.inspect(vv |> Iter.map(fn f -> f(4)) |> Iter.to_list())
}
`
	const want = "[<function>, <function>]\n" +
		"3\n" +
		"[2, 2, 2, 2]\n" +
		"[2, 4]\n" +
		"[1, 1]\n" +
		"[4, 6, 4, 6]\n" +
		"[11, 12]\n" +
		"#[<function>, <function>]\n" +
		"2\n" +
		"14\n" +
		"%{1 => <function>, 2 => <function>}\n" +
		"21\n" +
		"2\n" +
		"[3, 4]\n" +
		"[2, 2, 2]\n" +
		"[0, 1, 2]\n" +
		"[5, 8]\n"
	irRunSource(t, src, want)
}
