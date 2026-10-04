package irbuild

import "testing"

// TestIRBacklogCorpus_MixedShapesRunOnTheVM runs, on the VM, one program over
// several retained shapes, against its expected output: an ad-hoc `case`
// whose condition short-circuits (and its effect order), Float sorting through
// std's retained compare with a negative zero (rt.NegFloat), Bool, String and
// List ordering operators, distincts over a tuple, a map and a function, an
// interface default with a defaulted parameter called through an existential
// and read through a `field` requirement, Float literal patterns and Decimal
// set elements.
func TestIRBacklogCorpus_MixedShapesRunOnTheVM(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

type Coord (Int, Int)

type Kvs Map<String, Int>

type Callback (String) -> String

interface Named {
  field name: String
  fn greet(value: self, loud: Bool = False): String {
    if loud { "HI " + value.name } else { "hi " + value.name }
  }
}

struct Dog {
  name: String
}

impl Named for Dog {}

fn say(n: Named): String {
  Named.greet(n) + "/" + n.name
}

fn pick(a: Bool, b: Bool): String {
  case {
    a and b -> "both"
    a or b -> "one"
    _ -> "none"
  }
}

fn noisy(label: String, v: Bool): Bool {
  io.print(label)
  v
}

fn classify(f: Float): String {
  case f {
    -2.5 -> "neg"
    0.0 -> "zero"
    _ -> "other"
  }
}

fn main() {
  io.inspect(pick(True, True))
  io.inspect(pick(False, True))
  io.inspect(pick(False, False))
  io.inspect(case { noisy("a", False) and noisy("b", True) -> 1 _ -> 0 })
  io.inspect(Iter.sort([2.0, 1.0, 3.0, -0.0, 0.0]))
  io.inspect([False < True, True <= False, True >= True, "a" < "b", [1, 2] < [1, 3], [2] > [1, 9]])
  c = Coord(3, 4)
  io.inspect(c)
  io.inspect(c == Coord(3, 4))
  io.inspect(c == Coord((3, 5)))
  k = Kvs{"a" => 1}
  io.inspect(k)
  cb = Callback(|s| s + "!")
  io.print(run(cb, "go"))
  io.print(say(Dog{name: "Rex"}))
  io.print(Named.greet(Dog{name: "Rex"}, True))
  io.inspect(classify(-2.5))
  io.inspect(classify(-0.0))
  io.inspect(Set.size(#{1.50d, 1.5d, 2.0d}))
}

fn run(cb: Callback, s: String): String {
  cb(s)
}
`, "\"both\"\n\"one\"\n\"none\"\na\n0\n[-0.0, 0.0, 1.0, 2.0, 3.0]\n[True, False, True, True, True, True]\nCoord(3, 4)\nTrue\nFalse\nKvs({\"a\" => 1})\ngo!\nhi Rex/Rex\nHI Rex\n\"neg\"\n\"zero\"\n2\n")
}
