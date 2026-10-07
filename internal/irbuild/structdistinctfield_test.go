package irbuild

import "testing"

// A struct field whose type is a distinct over a tuple, a list or a map
// runs: built, read, destructured, shown, compared, updated and used as a
// map key, as the same distinct does as an enum payload.
func TestIRStructFieldOfCompositeDistinct_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

type Pair (Int, Int)

type Ns List<Int>

type Kv Map<String, Int>

struct H {
    p: Pair
    ns: Ns
    kv: Kv
}

fn main() {
    h = H{p: Pair(1, 2), ns: Ns([3]), kv: Kv({"a" => 4})}
    io.inspect(h)
    io.inspect(h.p)
    H{p: Pair(a, b), ns, kv: _} = h
    io.inspect(a + b)
    io.inspect(ns)
    io.inspect(h == H{p: Pair(1, 2), ns: Ns([3]), kv: Kv({"a" => 4})})
    io.inspect(h == H{p: Pair(1, 3), ns: Ns([3]), kv: Kv({"a" => 4})})
    io.inspect(Struct.update(h, {p: Pair(5, 6)}))
    io.inspect(Map.get({h => 1}, h))
    {
        type Local (Int, Int)
        {
            struct Holder {
                v: Local
            }
            io.inspect(Holder{v: Local(1, 2)})
        }
    }
}
`
	const want = "H{p: Pair(1, 2), ns: Ns([3]), kv: Kv({\"a\" => 4})}\n" +
		"Pair(1, 2)\n" +
		"3\n" +
		"Ns([3])\n" +
		"True\n" +
		"False\n" +
		"H{p: Pair(5, 6), ns: Ns([3]), kv: Kv({\"a\" => 4})}\n" +
		"Some(1)\n" +
		"Holder{v: Local(1, 2)}\n"
	irRunSource(t, src, want)
}
