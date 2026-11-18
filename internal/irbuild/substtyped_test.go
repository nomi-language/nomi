package irbuild

import "testing"

// verifyTypedInstances runs src as verifyLambdaProgram does and fails when
// an instance of entry is entered with a type parameter that has no
// checker type beside its kind (substFrame, substCensus). Under test
// pushSubst holds each checker type to its kind (verifySubst), so a type
// that disagrees fails the run.
func verifyTypedInstances(t *testing.T, entry substEntry, src, want string, extraFiles ...map[string]string) {
	t.Helper()
	census := openSubstCensus()
	verifyLambdaProgram(t, src, want, extraFiles...)
	if n := census()[entry]; n != 0 {
		t.Errorf("%d type parameters of %s instances are entered with no checker type", n, entry)
	}
}

// A std instance the builder asks for at an operand's kind reads its
// checker types off the operand: a container's Display for a hole and for
// `io.print`, a std interface default's `self` (`Int.max`), and a
// container's equality.
func TestSubstTyped_StdInstancesAtAnOperand(t *testing.T) {
	verifyTypedInstances(t, substStd, `import std::io

fn main()
    xs = [1, 2, 3]
    io.print("${xs}")
    io.print(Int.max(3, 4))
    r: Result<Int, String> = Ok(1)
    io.print("${r}")
    io.print([Some(1), None])
    io.print([Some(2)] == [Some(2)])
end
`, "[1, 2, 3]\n4\nOk(1)\n[Some(1), None]\nTrue\n")
}

// A program's impl of a std generic interface (`impl Contains<Int> for
// Bag`) binds into the interface's instance at the block's recorded type
// arguments, which carry their checker types (stdProgIfaceForImpl).
func TestSubstTyped_StdGenericInterfaceOfAnImpl(t *testing.T) {
	verifyTypedInstances(t, substIfaceImpl, `import std::io

struct Bag
    items: List<Int>
end

impl Contains<Int> for Bag
    fn contains(b: Bag, x: Int) -> Bool
        List.contains(b.items, x)
    end
end

fn main()
    b = Bag{items: [1, 2]}
    io.print(Contains.contains(b, 2))
    io.print(3 in b)
end
`, "True\nFalse\n")
}

// A generic interface's parameter default, filled in another file, is
// lowered in a frame binding the interface's parameters at the checker's
// types for the call (ifaceDefaultFrameAt), here and in the declaring
// file's accessor.
func TestSubstTyped_GenericInterfaceDefaultInAnotherFile(t *testing.T) {
	verifyTypedInstances(t, substIfaceDefault, `import std::io
import pads.{Pads}

struct Row
    n: Int
end

impl Pads<String> for Row
    fn fill(r: Row) -> String
        "${r.n}"
    end
end

fn main()
    io.print(Pads.pad(Row{n: 1}))
end
`, "[1]\n", map[string]string{"pads.nomi": `pub interface Pads<T>
    fn fill(s: self) -> T

    fn pad(s: self, extra: List<T> = [fill(s)]) -> List<T>
        extra
    end
end
`})
}

// A program's impl over a std container, registered at the receiver an
// operand of the call is (stdRecvImplAt), binds the block's parameters at
// the operand's checker type, matched against the block's receiver
// (analysis.ImplBlockArgsAt).
func TestSubstTyped_ProgramImplOverAStdContainer(t *testing.T) {
	verifyTypedInstances(t, substImpl, `import std::io

interface Show
    fn show(s: self) -> String
end

impl Show for Int
    fn show(n: Int) -> String
        "#${n}"
    end
end

impl Show for Maybe<T> where T: Show
    fn show(m: Maybe<T>) -> String
        case m
            Some(x) -> "some " + T.show(x)
            None -> "none"
        end
    end
end

fn main()
    m: Maybe<Int> = Some(3)
    io.print(Maybe.show(m))
    io.print(Show.show(m))
end
`, "some #3\nsome #3\n")
}

// A generic type's instance spelled in an annotation (`b: Box<Int>`, a
// field's `Pair<String, Int>`, a parameter's `Box<T>` inside a generic
// function) reads its arguments' checker types from the types the checker
// recorded for the annotation's type expressions (typeExprTypes), and the
// impls of that instance carry them.
func TestSubstTyped_AnnotatedGenericTypeInstance(t *testing.T) {
	src := `import std::io

struct Box<T>
    item: T
end

struct Pair<A, B>
    left: A
    right: B
end

struct Holder
    pair: Pair<String, Int>
end

impl Box<T>
    fn get(b: Box<T>) -> T
        b.item
    end
end

fn unbox<T>(b: Box<T>) -> T
    Box.get(b)
end

fn main()
    b: Box<Int> = Box{item: 1}
    h = Holder{pair: Pair{left: "a", right: 2}}
    io.print(unbox(b))
    io.print(h.pair.left)
end
`
	verifyTypedInstances(t, substType, src, "1\na\n")
	verifyTypedInstances(t, substImpl, src, "1\na\n")
}

// A generic subtype spelled in an annotation (`Many<Int>`) is its base at
// the checker's types for the annotation's arguments.
func TestSubstTyped_AnnotatedGenericRefinement(t *testing.T) {
	verifyTypedInstances(t, substRefinement, `import std::io

subtype Many<T> List<T> when fn xs -> List.length(xs) > 1

fn count(m: Many<Int>) -> Int
    List.length(m)
end

fn main()
    m: Many<Int> = [1, 2]
    io.print(count(m))
end
`, "2\n")
}
