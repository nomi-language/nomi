package irbuild

import (
	"testing"
)

func TestIRVector_CompletePrograms(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn append(v: Vector<String>, s: String): Vector<String> { Vector.push(v, s) }
fn main() {
  names = #["Ada", "Grace"]
  more = append(names, "Katherine")
  io.inspect(names)
  io.inspect(more)
  io.print(Vector.length(more))
  io.inspect(Vector.at(more, 1))
  io.inspect(Vector.at(more, -1))
  io.inspect(Vector.at(more, 3))
  io.inspect(Vector.at(more, 4294967296))
  get = |i: Int| Vector.at(more, i)
  io.inspect(get(2))
}
`, "#[\"Ada\", \"Grace\"]\n#[\"Ada\", \"Grace\", \"Katherine\"]\n3\nSome(\"Grace\")\nNone\nNone\nNone\nSome(\"Katherine\")\n")
}

func TestIRVector_EmptyAndConcat(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn empty(): Vector<Int> { #[] }
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
  io.inspect(Vector.at(v, 0))
  io.inspect(#["a\nb", "quoted \"text\""])
}
`, "#[]\n0\n#[1]\n#[1, 2, 3]\n#[1]\n#[1]\nNone\n#[\"a\nb\", \"quoted \\\"text\\\"\"]\n")
}

// A Vector of a wrapping distinct is an ordinary vector value; the
// element-agnostic operations run over it.
func TestIRVector_OfADistinctRuns(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
type Meter Int
fn main() { v = #[Meter(1)] io.print(Vector.length(v)) }
`, "1\n")
}

func TestIRVector_DiscardedBuildPreservesNativeEmission(t *testing.T) {
	for _, src := range []string{
		`struct Point { x: Int }
struct Outer {
  p: List<Point>
  hooks: Map<String, (Int) -> Int> = Map.empty()
}
fn main() { v = #[1, 2] dbg Outer{p: [Point{x: Vector.length(v)}]} return }`,
	} {
		p, err := AnalyzeSource("main.nomi", src)
		if err != nil {
			t.Fatalf("front end rejects boundary control: %v", err)
		}
		got, _, err := GenerateIR(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, mod := range got.IR {
			for _, f := range mod.Funcs() {
				if f.Name() == "main" {
					t.Fatal("unsupported nominal body retained")
				}
			}
		}
	}
}

func TestIRVector_EvaluationOrder(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn mark(n: Int): Int { io.print(n) n }
fn vector(v: Vector<Int>): Vector<Int> { io.print("vector") v }
fn main() {
  a = #[mark(1), mark(2)]
  b = Vector.push(vector(a), mark(3))
  io.inspect(Vector.at(vector(b), mark(1)))
  io.inspect(a)
}
`, "1\n2\nvector\n3\nvector\n1\nSome(2)\n#[1, 2]\n")
}

// A named spread at either end of a vector literal is Vector.concat: the
// spread, then the elements, are evaluated (as a list spread's tail is
// first), and an empty `#[]` spread takes the elements' kind.
func TestIRVector_NamedSpread(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn mark(n: Int): Int { io.print(n) n }
fn vector(v: Vector<Int>): Vector<Int> { io.print("vector") v }
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
  t = Team{names: names}
  io.inspect(#[..t.names, "Cy"])
  io.inspect({..t, names: #["Zed", ..]})
  io.inspect(names)
}
`, "#[\"Ada\", \"Bo\", \"Noah\"]\n#[\"Noah\", \"Ada\", \"Bo\"]\n#[\"Ada\", \"Bo\", \"Noah\", \"Eve\"]\n#[1, 2]\n#[1]\n#[None]\nvector\n2\n#[1, 2]\nvector\n3\n#[3, 4]\n#[\"Ada\", \"Bo\", \"Cy\"]\nTeam{names: #[\"Zed\", \"Ada\", \"Bo\"]}\n#[\"Ada\", \"Bo\"]\n")
}
