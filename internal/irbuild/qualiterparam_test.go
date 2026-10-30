package irbuild

import "testing"

// A type-qualified call to an impl function whose parameter is `Iter<T>`
// views a List, Set or Vector operand as that sequence, as a direct call
// does, and passes an Iter operand unchanged.
func TestIRQualifiedCallIterParameter_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

struct Box {
    n: Int
}

impl Box {
    fn total(boxes: Iter<Box>) -> Int {
        Iter.reduce(boxes, fn acc = 0, b -> acc + b.n)
    }
}

fn main() {
    boxes = [#Box{n: 1}, #Box{n: 2}]
    io.inspect(Box.total(boxes))
    io.inspect(Box.total([#Box{n: 4}]))
    io.inspect(Box.total([]))
    io.inspect(Box.total(#[#Box{n: 5}]))
    io.inspect([3, 4] |> Iter.map(fn n -> #Box{n}) |> Box.total())
}
`
	irRunSource(t, src, "3\n4\n0\n5\n7\n")
}

// The same call to another file's impl function, monomorphic or generic,
// as std/sql's `Statement.join` and `Statement.values` are called.
func TestIRQualifiedCallIterParameter_CrossFile(t *testing.T) {
	irBacklogProgramVM(t, map[string]string{
		"boxes.nomi": `pub struct Box {
    n: Int
}

impl Box {
    pub fn total(boxes: Iter<Box>) -> Int {
        Iter.reduce(boxes, fn acc = 0, b -> acc + b.n)
    }

    pub fn count_of<T>(items: Iter<T>) -> Int {
        Iter.count(items)
    }
}
`,
		"main_test.nomi": `import boxes.Box

test "a list into another file's Iter parameter" {
    boxes = [#Box{n: 1}, #Box{n: 2}]
    assert Box.total(boxes) == 3
    assert Box.total(%[#Box{n: 4}]) == 4
    assert [5] |> Iter.map(fn n -> #Box{n}) |> Box.total() == 5
}

test "a list into another file's generic Iter parameter" {
    assert Box.count_of([1, 2, 3]) == 3
    assert Box.count_of(%["a"]) == 1
    assert [1, 2, 3, 4] |> Iter.filter(fn n -> n > 2) |> Box.count_of() == 2
}
`,
	}, "main_test.nomi", 2)
}
