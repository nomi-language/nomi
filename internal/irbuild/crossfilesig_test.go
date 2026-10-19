package irbuild

import "testing"

// crossFileShapes declares, in a file other than the caller's, functions and
// a struct whose signatures and fields hold a record, `Infallible` and
// `Iter<Int>`. A caller re-derives each of those kinds in its own unit
// (importKind).
const crossFileShapes = `pub struct Box {
  at: #{x: Int, y: Int}
  size: Int = 1
}

pub fn make(x: Int) -> Box {
  #Box{at: #{x, y: x * 2}}
}

pub fn width(b: Box) -> Int {
  b.at.x + b.size
}

pub fn corner(b: Box) -> #{x: Int, y: Int} {
  b.at
}

pub fn px(p: #{x: Int, y: Int}) -> Int {
  p.x * 10 + p.y
}

pub fn ok_only(n: Int) -> Result<Int, Infallible> {
  Ok(n)
}

pub fn evens(n: Int) -> Iter<Int> {
  0..n |> Iter.map(fn x -> x * 2)
}

pub fn sum_of(xs: Iter<Int>) -> Int {
  Iter.reduce(xs, fn acc = 0, x -> acc + x)
}
`

// A struct with a record field, and a record or `Infallible` in a
// signature, declared in another file.
func TestCrossFileSignature_RecordsAndInfallible(t *testing.T) {
	irBacklogProgramVM(t, map[string]string{
		"shapes.nomi": crossFileShapes,
		"main_test.nomi": `import shapes.{self, Box}

test "a struct with a record field from another file" {
  b = shapes.make(3)
  assert shapes.width(b) == 4
  assert b.at.y == 6
  assert shapes.width(#Box{at: #{x: 1, y: 0}, size: 5}) == 6
}

test "records in another file's signatures" {
  assert shapes.corner(shapes.make(2)) == #{x: 2, y: 4}
  assert shapes.px(#{x: 1, y: 2}) == 12
  assert shapes.px(shapes.corner(shapes.make(1))) == 12
}

test "Infallible in another file's signature" {
  v = case shapes.ok_only(3) {
    Ok(n) -> n
    Err(_) -> 0
  }
  assert v == 3
}
`,
	}, "main_test.nomi", 3)
}

// A qualified call to another file's function lowers each argument at its
// parameter's kind, as a bare call to it does: a list enters an `Iter<Int>`
// parameter, qualified, bare and piped.
func TestCrossFileSignature_IterParameters(t *testing.T) {
	irBacklogProgramVM(t, map[string]string{
		"shapes.nomi": crossFileShapes,
		"main_test.nomi": `import shapes.{self, sum_of}

test "a list into another file's Iter parameter" {
  assert shapes.sum_of([1, 2, 3]) == 6
  assert sum_of([4, 5]) == 9
  assert [1, 2] |> shapes.sum_of() == 3
}

test "an Iter from another file" {
  assert shapes.sum_of(shapes.evens(4)) == 12
  assert shapes.evens(3) |> Iter.to_list() == [0, 2, 4]
}
`,
	}, "main_test.nomi", 2)
}
