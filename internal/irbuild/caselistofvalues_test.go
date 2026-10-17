package irbuild

import "testing"

// A list pattern takes apart a list of any value: Iters, Ranges and Maps,
// at the top level and inside an Iter.map callback, binding heads and a
// rest.
func TestCaseOverListOfValues_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	verifyLambdaProgram(t, `import std/io

fn main() {
    it = [1, 2] |> Iter.map(fn n -> n * 2)
    got = case [it] {
        [p] -> p
        _ -> it
    }
    io.inspect(Iter.to_list(got))
    out = [1, 2]
        |> Iter.map(fn n -> {
            r = 0..n
            held = case [r] {
                [p] -> p
                _ -> r
            }
            Debug.inspect(held)
        })
        |> String.join(",")
    io.print(out)
    counts = [1, 2]
        |> Iter.map(fn n -> {
            each = [n, n] |> Iter.map(fn x -> x + 1)
            case [each, each] {
                [a, ..rest] -> Iter.count(a) + Iter.count(rest)
                _ -> 0
            }
        })
        |> Iter.to_list()
    io.inspect(counts)
    ms = [%{"a" => 1}]
    io.inspect(case ms {
        [m] -> Map.get(m, "a")
        _ -> None
    })
}
`, "[2, 4]\n0..1,0..2\n[3, 3]\nSome(1)\n")
}
