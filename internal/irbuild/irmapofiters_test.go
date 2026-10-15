package irbuild

import "testing"

// A map holds Iters as values, and maps of them, in a binding and a struct
// field.
func TestIRMapOfIters_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

struct Node {
    f: Map<String, Iter<Int>>
}

fn total(d: Node, key: String): Int {
    case Map.get(d.f, key) {
        Some(it) -> it |> Iter.reduce(|acc = 0, n| acc + n)
        None -> -1
    }
}

fn main() {
    empty: Map<String, Iter<Int>> = Map.empty()
    io.inspect(Map.size(Node{f: empty}.f))
    runs: Map<String, Iter<Int>> = {"a" => [1, 2, 3] |> Iter.map(|x| x), "b" => (1..=4) |> Iter.filter(|x| x > 1)}
    n = Node{f: runs}
    io.inspect(Map.size(n.f))
    io.inspect(total(n, "a"))
    io.inspect(total(n, "b"))
    io.inspect(total(n, "c"))
    nested: Map<String, Map<String, Iter<Int>>> = {"x" => runs}
    io.inspect(Map.keys(nested) |> Iter.to_list())
}
`
	irRunSource(t, src, "0\n2\n6\n9\n-1\n[\"x\"]\n")
}
