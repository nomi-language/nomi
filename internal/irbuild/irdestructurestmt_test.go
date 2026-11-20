package irbuild

import (
	"strings"
	"testing"
)

// TestIRDestructureStatement_RunsInEveryPosition runs a tuple and a struct
// destructuring statement in a function body, an `if` arm and a block, over
// a generic struct and an anonymous one, with `_` discards, a rename, and a
// name bound again, through the one lowering a binding of a pattern takes
// (patternBindingStmt).
func TestIRDestructureStatement_RunsInEveryPosition(t *testing.T) {
	verifyLambdaProgram(t, `import std::io

struct Box<T>
    item: T
    label: String
end

fn body(b: Box<Int>) -> Int
    (n, _) = (b.item, "x")
    {item: m, label:} = b
    (n, k) = (n + m, label)
    io.print(k)
    n
end

fn arm(flag: Bool) -> Int
    if flag then
        (a, _, c) = (1, 2, 3)
        {x:, y: z} = {x: a, y: c}
        x + z
    else
        0
    end
end

fn block() -> Int
    total = begin
        {item:} = Box{item: 4, label: "four"}
        (p, q) = (item, item)
        p + q
    end
    total
end

fn main()
    io.print(body(Box{item: 5, label: "five"}))
    io.print(arm(True))
    io.print(arm(False))
    io.print(block())
    f = fn x: Int -> x + 1
    (f, g) = (fn x: Int -> x * 10, f)
    io.print(f(g(1)))
end
`, "five\n10\n4\n0\n8\n20\n")
}

// TestIRDestructureStatement_RunsInATestBody: in a test body, a tuple and a
// struct destructure bind and rebind names the test bound.
func TestIRDestructureStatement_RunsInATestBody(t *testing.T) {
	const src = `struct Pair
    left: Int
    right: String
end

test "destructures in a test body"
    (n, _) = (3, "x")
    {left:, right: name} = Pair{left: n * 2, right: "r"}
    (n, left) = (left, n)
    assert n == 6
    assert left == 3
    assert name == "r"
end
`
	got, exit := irShapesRun(t, src, []string{"destructures in a test body"})
	if want := "ok <path> :: destructures in a test body\n"; !strings.HasPrefix(got, want) || exit != 0 {
		t.Errorf("the VM's report (exit %d):\n%s\nwant it to begin:\n%s", exit, got, want)
	}
}
