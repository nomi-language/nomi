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

// A bare `.Leaf` given to a slot of a generic construction whose type the
// construction's other values solve builds the solved instance: a field of a
// struct-shaped variant (`#Tree.Node{left: .Leaf, value: 4}`), one nested in
// a dot literal, a positional payload, a generic struct's field, a generic
// function's argument, and each wrapped in `Maybe.Some`. The checker
// recorded the dot at the declaration's own `Tree<T>`, since the slot is
// checked before T is solved, and the builder declined it as "a DotVariant".
func TestDotVariant_GenericSlotSolvedByItsSiblings(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	verifyLambdaProgram(t, `import std/io

enum Tree<T> {
    Leaf
    Node {
        left: Tree<T>
        value: T
    }
}

enum Pos<T> {
    Leaf
    Node #(Pos<T>, T)
}

enum Slot<T> {
    Empty
    Full T
}

struct Box<T> {
    inner: Tree<T>
    v: T
}

fn pair<T>(t: Tree<T>, v: T) -> Tree<T> {
    #.Node{left: t, value: v}
}

fn both<T>(a: Slot<T>, b: Slot<T>) -> List<Slot<T>> {
    [a, b]
}

fn main() {
    io.inspect(#Tree.Node{left: .Leaf, value: 4})
    io.inspect(#Tree.Node{left: #.Node{left: .Leaf, value: 3}, value: 4})
    io.inspect(Pos.Node(#(.Node(#(.Leaf, 1)), 4)))
    io.inspect(#Box{inner: .Leaf, v: "b"})
    io.inspect(pair(.Leaf, 2.5))
    io.inspect(pair(#.Node{left: .Leaf, value: 1}, 2))
    io.inspect(both(.Empty, .Full("x")))
    io.inspect(Maybe.Some(#Tree.Node{left: .Leaf, value: True}))
}
`, `#Node{left: Leaf, value: 4}
#Node{left: #Node{left: Leaf, value: 3}, value: 4}
Node(Node(Leaf, 1), 4)
#Box{inner: Leaf, v: "b"}
#Node{left: Leaf, value: 2.5}
#Node{left: #Node{left: Leaf, value: 1}, value: 2}
[Empty, Full("x")]
Some(#Node{left: Leaf, value: True})
`)
}
