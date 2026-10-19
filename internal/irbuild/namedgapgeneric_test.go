package irbuild

import "testing"

// A named argument that leaves a generic callee's defaulted slot before it
// empty (`Box.pick(b, n: 2)` over `other: Maybe<T> = None`) fills that slot
// at the call's instantiation.
func TestNamedGapGeneric_FillsTheSlotAtTheInstance(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

struct Box<T> {
    v: T
}

impl Box<T> {
    fn pick<T>(_b: Box<T>, other: Maybe<T> = None, n: Int = 0) -> Int {
        case other {
            Some(_) -> n + 1
            None -> n
        }
    }
}

fn main() {
    io.print(Box.pick(#Box{v: 1}, n: 2))
    io.print(Box.pick(#Box{v: "a"}, n: 5))
    io.print(Box.pick(#Box{v: 1}, other: Some(3), n: 2))
}
`, "2\n5\n3\n")
}
