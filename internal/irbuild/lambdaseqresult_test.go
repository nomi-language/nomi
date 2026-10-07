package irbuild

import "testing"

// A lambda whose body is a list, where an `(A) -> Iter<T>` is expected,
// runs: the list enters the declared sequence. The checker solves
// `Maybe.map`'s result as `Iter<Int>` when the call sits under an
// `Iter<Int>` parameter whose element an outer `List<Int>` fixed, so the
// callback `|_| [20]` is wanted at `(Int) -> Iter<Int>`; the same holds for
// an annotated binding, an `if` body and an early `return`.
func TestIRLambdaAtIterResult_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

fn main() {
    fn cb1(xs: List<Int>): List<Int> {
        xs
    }
    io.inspect(cb1(Iter.filter(Maybe.map(Some(Some(0.5)), |_| [20]) |> Maybe.with_default([4]), |y| y == y) |> Iter.to_list()))
    io.inspect(cb1(Iter.to_list(Maybe.with_default(Maybe.map(Some(1), |n| [n, n]), [4]))))
    io.inspect(cb1(Iter.to_list(Maybe.with_default(Maybe.map(None, |n: Int| [n]), [4]))))
    f: (Int) -> Iter<Int> = |_| [20]
    io.inspect(Iter.to_list(f(1)))
    g: (Int) -> Iter<Int> = |n| if n > 0 { [n] } else { [] }
    io.inspect(Iter.to_list(g(0)))
    h: (Int) -> Iter<Int> = |n| {
        if n > 5 {
            return [5]
        }
        [n, n]
    }
    io.inspect(Iter.to_list(h(9)))
    io.inspect(Iter.to_list(h(1)))
}
`
	const want = "[20]\n" +
		"[1, 1]\n" +
		"[4]\n" +
		"[20]\n" +
		"[]\n" +
		"[5]\n" +
		"[1, 1]\n"
	irRunSource(t, src, want)
}
