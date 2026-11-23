package vmhost_test

import "testing"

// A recursive type whose field reaches it through a type that can be empty
// or absent builds values and runs: the forms the value cycle check
// (analysis valueCycle) leaves legal.
func TestRun_RecursionWithAWayOutRuns(t *testing.T) {
	out := runOutput(t, `import std::io

struct Node
    next: Option<Node>
    kids: List<Node>
    vec: Vector<Node>
    map: Map<String, Node>
    make: (Int) -> Node
    result: Result<Node, String>
end

struct Chain<T>
    v: T
    next: Option<Chain<T>>
end

enum Tree
    Branch(Tree, Tree)
    Leaf
end

struct Defaulted
    again: Option<Defaulted> = None
end

enum Rec
    Cons {head: Int, tail: Rec}
    Nil
end

fn depth(t: Tree) -> Int
    case t
        .Branch(l, r) -> 1 + Int.max(depth(l), depth(r))
        .Leaf         -> 0
    end
end

fn sum(r: Rec) -> Int
    case r
        .Cons{head:, tail:} -> head + sum(tail)
        .Nil                -> 0
    end
end

fn main()
    leaf = Node{next: None, kids: [], vec: [], map: {}, make: fn _ -> todo, result: Err("none")}
    root = {..leaf, next: Some(leaf), kids: [leaf, leaf]}
    struct Local
        next: Option<Local>
    end
    local = Local{next: Some(Local{next: None})}
    io.print(List.length(root.kids))
    io.print(Chain{v: 1, next: Some(Chain{v: 2, next: None})}.v)
    io.print(depth(Tree.Branch(Tree.Leaf, Tree.Branch(Tree.Leaf, Tree.Leaf))))
    io.print(Defaulted{}.again == None)
    io.print(sum(Rec.Cons{head: 1, tail: Rec.Cons{head: 2, tail: Rec.Nil}}))
    io.print(local.next is Some(_))
end
`)
	if want := "2\n1\n2\nTrue\n3\nTrue\n"; out != want {
		t.Errorf("output %q, want %q", out, want)
	}
}
