package irbuild

import (
	"testing"
)

func TestIRListEquality_CompletePrograms(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn left(): List<Int> { io.print("left"); [1, 2] }
fn right(): List<Int> { io.print("right"); [1, 2] }
fn empty(): List<Int> { [] }
fn main() {
 io.print(left() == right())
 io.print([1, 2] != [1, 3])
 io.print([1] == [])
 io.print([] != [1])
 io.print([[1], empty()] == [[1], empty()])
 io.print([0.0 / 0.0] == [0.0 / 0.0])
}
`, "left\nright\nTrue\nTrue\nFalse\nTrue\nTrue\nTrue\n")
}

func TestIRListEquality_FindLinksThroughProcess(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn find(list: List<Int>, target: Int): Maybe<Int> {
 if list == [] { None } else {
  Iter.reduce(list, |acc = None, x|
   case acc {
    Some(v) -> Some(v)
    None -> if x == target { Some(x) } else { None }
   }
  )
 }
}
fn process(list: List<Int>): Maybe<Int> { val = try find(list, 3); Some(val * 10) }
fn main() {
 io.inspect(process([1, 2, 3, 4]))
 io.inspect(process([1, 2, 4, 5]))
 io.inspect(process([]))
}
`, "Some(30)\nNone\nNone\n")
}

// A Vector's and a Map's `==` are structural (std's `impl
// Equatable` for both is `a == b`), and the VM's comparison runs rt.Equal:
// a Map compares order-insensitively, and NaN equals itself.
func TestIRListEquality_VectorsAndMapsCompareStructurally(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn main() {
 io.print(#[1, 2] == #[1, 2])
 io.print(#[1, 2] != #[2, 1])
 io.print(Vector.equal?(#[0.0 / 0.0], #[0.0 / 0.0]))
 io.print({"a" => 1, "b" => 2} == {"b" => 2, "a" => 1})
 io.print({"a" => 1} != {"a" => 2})
 io.print(Map.equal?({1 => "x"}, {1 => "x"}))
}
`, "True\nTrue\nTrue\nTrue\nTrue\nTrue\n")
}

// A list literal of a distinct's values is built (backlog-types), and `==`
// over two such lists compares structurally:
// it never dispatches on an element. TestIRTestBody_BacklogTypes runs it.
func TestIRListEquality_DistinctElementsRetain(t *testing.T) {
	p, err := AnalyzeSource("main.nomi", `import std/io
type Id Int
fn main() { io.print([Id(1)] == [Id(1)]) }`)
	if err != nil {
		t.Fatalf("the front end rejects the fixture, so nothing below is tested: %v", err)
	}
	got, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, mod := range got.IR {
		for _, f := range mod.Funcs() {
			if f.Name() == "main" {
				return
			}
		}
	}
	t.Fatal("main, which compares two lists of a distinct, is not retained")
}
