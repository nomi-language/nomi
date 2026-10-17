package irbuild

import "testing"

// A dotted generic struct's literal (`Tree.Node{v: 1}`) builds the instance
// its fields solve, as an undotted one's does, and a value of it passes to
// a generic function.
func TestDottedGenericStruct_LiteralRuns(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

struct Tree.Node<T> {
    v: T
}

fn val<T>(n: Tree.Node<T>) -> T {
    n.v
}

fn main() {
    n = Tree.Node{v: 1}
    io.print(n.v)
    io.inspect(Tree.Node{v: "a"})
    m: Tree.Node<String> = Tree.Node{v: "b"}
    io.print(val(m))
}
`, "1\nTree.Node{v: \"a\"}\nb\n")
}
