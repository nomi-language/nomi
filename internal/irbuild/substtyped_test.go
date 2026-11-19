package irbuild

import (
	"strings"
	"testing"
)

// verifyTypedInstances runs src as verifyLambdaProgram does and fails when
// an instance is entered with a type parameter that has no checker type
// beside its kind (substFrame, substCensus), naming each. Under test
// pushSubst holds each checker type to its kind (verifySubst), so a type
// that disagrees fails the run.
func verifyTypedInstances(t *testing.T, src, want string, extraFiles ...map[string]string) {
	t.Helper()
	census := openSubstCensus()
	verifyLambdaProgram(t, src, want, extraFiles...)
	for e, names := range census() {
		t.Errorf("%d type parameters of %s instances are entered with no checker type: %s", len(names), e, strings.Join(names, ", "))
	}
}

// A std instance the builder asks for at an operand's kind reads its
// checker types off the operand: a container's Display for a hole and for
// `io.print`, a std interface default's `self` (`Int.max`), and a
// container's equality.
func TestSubstTyped_StdInstancesAtAnOperand(t *testing.T) {
	verifyTypedInstances(t, `import std::io

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
	verifyTypedInstances(t, `import std::io

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
	verifyTypedInstances(t, `import std::io
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
	verifyTypedInstances(t, `import std::io

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
	verifyTypedInstances(t, src, "1\na\n")
}

// A generic subtype spelled in an annotation (`Many<Int>`) is its base at
// the checker's types for the annotation's arguments.
func TestSubstTyped_AnnotatedGenericRefinement(t *testing.T) {
	verifyTypedInstances(t, `import std::io

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

// A std value held as a std interface is boxed with dispatch entries the
// program builds (stdProgThunk), each a call for the boxed value's type
// (the coercion's analysis.Coercion.Value), so the std instances the
// entries reach (Range.contains, List.hash) are entered with it.
func TestSubstTyped_StdValuesHeldAsStdInterfaces(t *testing.T) {
	verifyTypedInstances(t, `import std::io

fn holders(x: Int, cs: List<Contains<Int>>) -> Int
    cs |> Iter.filter(fn c -> x in c) |> Iter.count()
end

fn main()
    io.print(Int.to_string(holders(3, [[1, 2], 1..5, Set[3]])))
    h: Hashable = [1, 2]
    same: Hashable = [1, 2]
    io.print(Bool.to_string(Hashable.hash(h) == Hashable.hash(same)))
end
`, "2\nTrue\n")
}

// Instances the builder names by a receiver rather than at a call read
// the receiver's checker type where it is asked for: a std default an
// impl block does not write, at the block's receiver (implDef.recvType);
// a sort's comparator, at the view's element (analysis.IterView.Elem), a
// tuple's part and the key's type argument; a renderer's nested Debug, at
// the part's type within its whole (partCheckerType).
func TestSubstTyped_InstancesNamedByTheirReceiver(t *testing.T) {
	verifyTypedInstances(t, `import std::io
import std::json.{Json, ToJson}

struct Away
    n: Int
end

derive ToJson for Away

fn main()
    v: ToJson = Away{n: 1}
    io.print(Json.encode(ToJson.to_json(v)))
    io.print("${[[2, 1], [1]] |> Iter.sort()}")
    io.print("${[(2, [1]), (1, [2])] |> Iter.sort()}")
    io.print("${[[3], [1, 2]] |> Iter.sort_by(fn xs -> [List.length(xs)])}")
    io.print(Debug.inspect([1..3]))
end
`, "{\"n\":1}\n[[1], [2, 1]]\n[(1, [2]), (2, [1])]\n[[3], [1, 2]]\n[1..3]\n")
}
