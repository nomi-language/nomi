package irbuild

import (
	"testing"
)

func TestIRVector_CompletePrograms(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn append(v: Vector<String>, s: String) -> Vector<String>
    Vector.push(v, s)
end
fn main()
  names = Vector["Ada", "Grace"]
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
end
`, "Vector[\"Ada\", \"Grace\"]\nVector[\"Ada\", \"Grace\", \"Katherine\"]\n3\nSome(\"Grace\")\nNone\nNone\nNone\nSome(\"Katherine\")\n")
}

func TestIRVector_EmptyAndConcat(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn empty() -> Vector<Int>
    Vector[]
end
fn main()
  v: Vector<Int> = Vector[]
  io.inspect(v)
  io.print(Vector.length(empty()))
  a = Vector.push(v, 1)
  b = Vector.concat(a, Vector[2, 3])
  io.inspect(a)
  io.inspect(b)
  io.inspect(Vector.concat(Vector[], a))
  io.inspect(Vector.concat(a, Vector[]))
  io.inspect(Vector.get(v, 0))
  io.inspect(Vector["a\nb", "quoted \"text\""])
end
`, "Vector[]\n0\nVector[1]\nVector[1, 2, 3]\nVector[1]\nVector[1]\nNone\nVector[\"a\\nb\", \"quoted \\\"text\\\"\"]\n")
}

// A Vector of a wrapping distinct is an ordinary vector value; the
// element-agnostic operations run over it.
func TestIRVector_OfADistinctRuns(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
type Meter Int
fn main()
    v = Vector[Meter(1)]
    io.print(Vector.length(v))
end
`, "1\n")
}

// A Vector of function values is built from a literal of lambdas and read
// back by the element-agnostic operations.
func TestIRVector_OfFunctionValuesRuns(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn main()
  v = Vector[fn x: Int -> x + 1, fn x: Int -> x * 2]
  io.print(Vector.length(v))
  case Vector.get(v, 1)
    Some(f) -> io.print(f(5))
    None -> io.print(0)
  end
end
`, "2\n10\n")
}

func TestIRVector_EvaluationOrder(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn mark(n: Int) -> Int
    io.print(n)
    n
end
fn vector(v: Vector<Int>) -> Vector<Int>
    io.print("vector")
    v
end
fn main()
  a = Vector[mark(1), mark(2)]
  b = Vector.push(vector(a), mark(3))
  io.inspect(Vector.get(vector(b), mark(1)))
  io.inspect(a)
end
`, "1\n2\nvector\n3\nvector\n1\nSome(2)\nVector[1, 2]\n")
}

// A named spread at either end of a vector literal is Vector.concat: the
// spread, then the elements, are evaluated (as a list spread's tail is
// first), and an empty `[]` spread takes the elements' kind.
func TestIRVector_NamedSpread(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn mark(n: Int) -> Int
    io.print(n)
    n
end
fn vector(v: Vector<Int>) -> Vector<Int>
    io.print("vector")
    v
end
struct Team
    names: Vector<String>
end
fn main()
  names = Vector["Ada", "Bo"]
  io.inspect(Vector[..names, "Iris"])
  io.inspect(Vector["Iris", ..names])
  io.inspect(Vector[..names, "Iris", "Eve"])
  io.inspect(Vector[..Vector[], 1, 2])
  io.inspect(Vector[1, ..Vector[]])
  maybes: Vector<Maybe<Int>> = Vector[..Vector[], None]
  io.inspect(maybes)
  io.inspect(Vector[..vector(Vector[1]), mark(2)])
  io.inspect(Vector[mark(3), ..vector(Vector[4])])
  t = Team{names:}
  io.inspect(Vector[..t.names, "Cy"])
  io.inspect({..t, names: Vector["Zed", ..]})
  io.inspect(names)
end
`, "Vector[\"Ada\", \"Bo\", \"Iris\"]\nVector[\"Iris\", \"Ada\", \"Bo\"]\nVector[\"Ada\", \"Bo\", \"Iris\", \"Eve\"]\nVector[1, 2]\nVector[1]\nVector[None]\nvector\n2\nVector[1, 2]\nvector\n3\nVector[3, 4]\nVector[\"Ada\", \"Bo\", \"Cy\"]\nTeam{names: Vector[\"Zed\", \"Ada\", \"Bo\"]}\nVector[\"Ada\", \"Bo\"]\n")
}

// A Vector of an interface's existential holds each item as the value it
// erases, as a List does: a declared `Vector<Display>`, a vector literal
// passed where an `Iter<Display>` is declared, and the element-agnostic
// operations over one.
func TestIRVector_OfExistential(t *testing.T) {
	verifyLambdaProgram(t, `import std::io

struct Point
    x: Int
end

impl Display for Point
    fn to_string(p: Point) -> String
        "P${p.x}"
    end
end

fn show(xs: Iter<Display>) -> String
    xs |> Iter.map(Display.to_string) |> String.join(",")
end

fn main()
    v: Vector<Display> = Vector[1, 2]
    io.print(Int.to_string(Iter.count(v)))
    io.print(show(v))
    io.print(show(Vector[3, 4]))
    w: Vector<Display> = Vector[Point{x: 5}, "s", 1.5]
    io.print(show(w))
    io.print(Int.to_string(Vector.length(Vector.push(w, 7))))
    io.print(show(Vector.push(w, 7)))
    io.inspect(w)
    io.print(Debug.inspect(Vector.get(v, 0)))
end`, "2\n1,2\n3,4\nP5,s,1.5\n4\nP5,s,1.5,7\nVector[Point{x: 5}, \"s\", 1.5]\nSome(1)\n")
}
