package irbuild

import "testing"

// A partial of a call through a bounded type parameter, `T.decode(&1)`,
// runs inside a generic impl, at each instance, with a receiver and
// without. The partial takes its parameter kinds from the reference the
// analysis records at `decode`, which a block-level `where T: Decode`
// must give the bound's method; std/json's container impls write
// `T.from_json(&1)` the same way.
func TestVM_PartialThroughABoundTypeParameter(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io

interface Decode
    fn decode(n: Int) -> self
end

impl Decode for Int
    fn decode(n: Int) -> Int
        n
    end
end

impl Decode for String
    fn decode(n: Int) -> String
        "#${Int.to_string(n)}"
    end
end

struct Pair<T>
    a: T
    b: T
end

fn both<T>(n: Int, f: (Int) -> T) -> Pair<T>
    Pair{a: f(n), b: f(n + 1)}
end

impl Decode for Pair<T> where T: Decode
    fn decode(n: Int) -> Pair<T>
        both(n, &T.decode(&1))
    end
end

interface Show
    fn show(value: self) -> String
end

impl Show for Int
    fn show(value: Int) -> String
        "i${Int.to_string(value)}"
    end
end

impl Show for Pair<T> where T: Show
    fn show(value: Pair<T>) -> String
        Iter.map([value.a, value.b], &T.show(&1)) |> String.join("+")
    end
end

fn main()
    io.print(Show.show(Pair{a: 1, b: 2}))
    p: Pair<String> = Decode.decode(3)
    io.inspect(p)
    q: Pair<Int> = Decode.decode(3)
    io.inspect(q)
end
`
	irRunSource(t, src, `i1+i2
Pair{a: "#3", b: "#4"}
Pair{a: 3, b: 4}
`)
}
