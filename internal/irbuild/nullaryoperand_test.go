package irbuild

import "testing"

// A generic call with no arguments has nothing to solve its type parameters
// but the place its result goes. An operator's operand has no expected type,
// so the checker solves them only when the operator is typed, after the call
// is. The call's instantiation must still reach the builder: these programs
// declined with "a direct callee that is not lowerable" and "a generic
// stdlib call whose type arguments are unsolved".
func TestNullaryGenericCall_InAnOperatorOperandRuns(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

interface Blank {
    fn blank(): self
}

struct Box<T> {
    items: List<T>
}

impl Blank for Box<T> {
    fn blank(): Box<T> {
        Box{items: []}
    }
}

impl Add<Box<T>, Box<T>> for Box<T> {
    fn add(lhs: Box<T>, rhs: Box<T>): Box<T> {
        Box{items: lhs.items + rhs.items}
    }
}

fn none<T>(): List<T> {
    []
}

fn main() {
    io.print([1, 2] + none())
    io.print(none() + ["a"])
    io.print([1] == none())
    io.print((Box{items: [3]} + Box.blank()).items)
    io.print((Box.blank() + Box{items: ["b"]}).items)
}
`, "[1, 2]\n[a]\nFalse\n[3]\n[b]\n")
}
