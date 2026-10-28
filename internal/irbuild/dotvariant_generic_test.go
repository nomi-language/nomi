package irbuild

import "testing"

// A payload-free variant of a user generic enum written with the leading dot
// (`t: Tree<Int> = .Leaf`) builds the instance the checker typed the dot at:
// an annotated binding, a generic function's result, a case arm, a payload,
// a list element, an argument, and another file's enum. The qualified
// `Tree.Leaf` lowered; the dot declined as "a DotVariant", since the node the
// builder rewrites it to carries no reference to an instance
// (genericDotBare).
func TestDotVariant_GenericEnumBareVariant(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	verifyLambdaProgram(t, `import {
    std/io
    trees.Slot
}

enum Tree<T> {
    Leaf
    Node #(Tree<T>, T, Tree<T>)
}

fn size<T>(t: Tree<T>) -> Int {
    case t {
        .Leaf -> 0
        .Node(#(l, _, r)) -> size(l) + 1 + size(r)
    }
}

fn empty<T>() -> Tree<T> {
    .Leaf
}

fn insert(t: Tree<Int>, n: Int) -> Tree<Int> {
    case t {
        .Leaf -> .Node(#(.Leaf, n, .Leaf))
        .Node(#(l, v, r)) -> {
            if n < v {
                .Node(#(insert(l, n), v, r))
            } else {
                .Node(#(l, v, insert(r, n)))
            }
        }
    }
}

fn count(trees: List<Tree<String>>) -> Int {
    trees |> Iter.map(size) |> Iter.sum()
}

fn slots() -> List<Slot<Int>> {
    [.Empty, .Full(2), .Empty]
}

fn main() {
    t: Tree<Int> = empty()
    io.inspect(size([3, 1, 2] |> Iter.reduce(fn acc = t, n -> insert(acc, n))))
    io.inspect(count([.Leaf, .Node(#(.Leaf, "a", .Leaf))]))
    io.inspect(slots())
    s: Slot<String> = .Empty
    io.inspect(s)
}
`, "3\n1\n[Empty, Full(2), Empty]\nEmpty\n", map[string]string{"trees.nomi": `pub enum Slot<T> {
    Empty
    Full T
}
`})
}
