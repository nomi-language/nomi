package irbuild

import "testing"

// irPatternThroughTypeVarSource matches every pattern shape inside a payload
// whose type the checker reaches as a type variable: `pick<T>` returns its
// argument type, so the scrutinee is `Opt<T>` with T bound to the payload's
// concrete type. The checker follows the binding before each pattern arm
// inspects the type.
const irPatternThroughTypeVarSource = `import std/io

enum Opt<T> {
  Nothing
  Has(T)
}

struct Pt {
  x: Int
  y: Int
}

enum Shape {
  Circle(Int)
  Rect{w: Int, h: Int}
}

type Id Int
type Kvs Map<String, Int>

fn pick<T>(c: Bool, a: T, b: T): T {
  if c { b } else { a }
}

fn main() {
  case pick(True, Opt.Nothing, Opt.Has((1, "x"))) {
    .Has((s, t)) -> io.print("tuple ${s} ${t}")
    .Nothing -> io.print("none")
  }
  case pick(True, Opt.Nothing, Opt.Has(Pt{x: 3, y: 4})) {
    .Has(Pt{x, y}) -> io.print("struct ${x} ${y}")
    .Nothing -> io.print("none")
  }
  case pick(True, Opt.Nothing, Opt.Has([1, 2, 3])) {
    .Has([a, ..rest]) -> io.print("list ${a} ${rest}")
    .Has([]) -> io.print("empty")
    .Nothing -> io.print("none")
  }
  case pick(True, Opt.Nothing, Opt.Has(Shape.Rect{w: 5, h: 6})) {
    .Has(.Rect{w, h}) -> io.print("struct variant ${w} ${h}")
    .Has(.Circle(r)) -> io.print("circle ${r}")
    .Nothing -> io.print("none")
  }
  case pick(True, Opt.Nothing, Opt.Has(Opt.Has(7))) {
    .Has(.Has(n)) -> io.print("variant ${n}")
    .Has(.Nothing) -> io.print("inner none")
    .Nothing -> io.print("none")
  }
  case pick(True, Opt.Nothing, Opt.Has({"k" => 8})) {
    .Has({"k" => v}) -> io.print("map ${v}")
    .Has(_) -> io.print("no k")
    .Nothing -> io.print("none")
  }
  case pick(True, Opt.Nothing, Opt.Has(Id(9))) {
    .Has(Id(i)) -> io.print("distinct ${i}")
    .Nothing -> io.print("none")
  }
  case pick(True, Opt.Nothing, Opt.Has(Kvs{"a" => 10})) {
    .Has(Kvs{"a" => v}) -> io.print("map distinct ${v}")
    .Has(_) -> io.print("no a")
    .Nothing -> io.print("none")
  }
}
`

func TestIRPattern_EveryShapeThroughATypeVariable(t *testing.T) {
	names := irRetainedFuncNames(t, irPatternThroughTypeVarSource)
	if !names["main"] {
		t.Fatal("main was not retained")
	}
	verifyLambdaProgram(t, irPatternThroughTypeVarSource,
		"tuple 1 x\nstruct 3 4\nlist 1 [2, 3]\nstruct variant 5 6\nvariant 7\nmap 8\ndistinct 9\nmap distinct 10\n")
}
