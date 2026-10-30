package irbuild

import (
	"testing"
)

func TestIRVector_CompletePrograms(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn append(v: Vector<String>, s: String) -> Vector<String> { Vector.push(v, s) }
fn main() {
  names = #["Ada", "Grace"]
  more = append(names, "Katherine")
  io.inspect(names)
  io.inspect(more)
  io.print(Vector.length(more))
  io.inspect(Vector.get(more, 1))
  io.inspect(Vector.get(more, -1))
  io.inspect(Vector.get(more, 3))
  io.inspect(Vector.get(more, 4294967296))
  get = fn i: Int -> Vector.get(more, i)
  io.inspect(get(2))
}
`, "#[\"Ada\", \"Grace\"]\n#[\"Ada\", \"Grace\", \"Katherine\"]\n3\nSome(\"Grace\")\nNone\nNone\nNone\nSome(\"Katherine\")\n")
}

func TestIRVector_EmptyAndConcat(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn empty() -> Vector<Int> { #[] }
fn main() {
  v: Vector<Int> = #[]
  io.inspect(v)
  io.print(Vector.length(empty()))
  a = Vector.push(v, 1)
  b = Vector.concat(a, #[2, 3])
  io.inspect(a)
  io.inspect(b)
  io.inspect(Vector.concat(#[], a))
  io.inspect(Vector.concat(a, #[]))
  io.inspect(Vector.get(v, 0))
  io.inspect(#["a\nb", "quoted \"text\""])
}
`, "#[]\n0\n#[1]\n#[1, 2, 3]\n#[1]\n#[1]\nNone\n#[\"a\\nb\", \"quoted \\\"text\\\"\"]\n")
}

// A Vector of a wrapping distinct is an ordinary vector value; the
// element-agnostic operations run over it.
func TestIRVector_OfADistinctRuns(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
type Meter Int
fn main() { v = #[Meter(1)] io.print(Vector.length(v)) }
`, "1\n")
}

// A Vector of function values is built from a literal of lambdas and read
// back by the element-agnostic operations.
func TestIRVector_OfFunctionValuesRuns(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn main() {
  v = #[fn x: Int -> x + 1, fn x: Int -> x * 2]
  io.print(Vector.length(v))
  case Vector.get(v, 1) {
    Some(f) -> io.print(f(5))
    None -> io.print(0)
  }
}
`, "2\n10\n")
}

func TestIRVector_EvaluationOrder(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn mark(n: Int) -> Int { io.print(n) n }
fn vector(v: Vector<Int>) -> Vector<Int> { io.print("vector") v }
fn main() {
  a = #[mark(1), mark(2)]
  b = Vector.push(vector(a), mark(3))
  io.inspect(Vector.get(vector(b), mark(1)))
  io.inspect(a)
}
`, "1\n2\nvector\n3\nvector\n1\nSome(2)\n#[1, 2]\n")
}

// A named spread at either end of a vector literal is Vector.concat: the
// spread, then the elements, are evaluated (as a list spread's tail is
// first), and an empty `#[]` spread takes the elements' kind.
func TestIRVector_NamedSpread(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn mark(n: Int) -> Int { io.print(n) n }
fn vector(v: Vector<Int>) -> Vector<Int> { io.print("vector") v }
struct Team { names: Vector<String> }
fn main() {
  names = #["Ada", "Bo"]
  io.inspect(#[..names, "Noah"])
  io.inspect(#["Noah", ..names])
  io.inspect(#[..names, "Noah", "Eve"])
  io.inspect(#[..#[], 1, 2])
  io.inspect(#[1, ..#[]])
  maybes: Vector<Maybe<Int>> = #[..#[], None]
  io.inspect(maybes)
  io.inspect(#[..vector(#[1]), mark(2)])
  io.inspect(#[mark(3), ..vector(#[4])])
  t = #Team{names: names}
  io.inspect(#[..t.names, "Cy"])
  io.inspect(#{..t, names: #["Zed", ..]})
  io.inspect(names)
}
`, "#[\"Ada\", \"Bo\", \"Noah\"]\n#[\"Noah\", \"Ada\", \"Bo\"]\n#[\"Ada\", \"Bo\", \"Noah\", \"Eve\"]\n#[1, 2]\n#[1]\n#[None]\nvector\n2\n#[1, 2]\nvector\n3\n#[3, 4]\n#[\"Ada\", \"Bo\", \"Cy\"]\n#Team{names: #[\"Zed\", \"Ada\", \"Bo\"]}\n#[\"Ada\", \"Bo\"]\n")
}

// A Vector of an interface's existential holds each item as the value it
// erases, as a List does: a declared `Vector<Display>`, a vector literal
// passed where an `Iter<Display>` is declared, and the element-agnostic
// operations over one.
func TestIRVector_OfExistential(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

struct Point {
    x: Int
}

impl Display for Point {
    fn to_string(p: Point) -> String {
        "P${p.x}"
    }
}

fn show(xs: Iter<Display>) -> String {
    xs |> Iter.map(Display.to_string) |> String.join(",")
}

fn main() {
    v: Vector<Display> = #[1, 2]
    io.print(Int.to_string(Iter.count(v)))
    io.print(show(v))
    io.print(show(#[3, 4]))
    w: Vector<Display> = #[#Point{x: 5}, "s", 1.5]
    io.print(show(w))
    io.print(Int.to_string(Vector.length(Vector.push(w, 7))))
    io.print(show(Vector.push(w, 7)))
    io.inspect(w)
    io.print(Debug.inspect(Vector.get(v, 0)))
}`, "2\n1,2\n3,4\nP5,s,1.5\n4\nP5,s,1.5,7\n#[#Point{x: 5}, \"s\", 1.5]\nSome(1)\n")
}
