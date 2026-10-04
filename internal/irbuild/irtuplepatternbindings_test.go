package irbuild

import "testing"

func TestIRTuplePatternBindings_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"structural components and discarded names", `import std/io
fn choose(p: (Int, List<Int>, String)): List<Int> {
  case p {
    (1, xs, _ignored) -> xs
    (2, xs, "yes") -> xs
    _ -> []
  }
}
fn main() {
  io.inspect(choose((1, [10, 20], "anything")))
  io.inspect(choose((2, [30], "yes")))
  io.inspect(choose((2, [40], "no")))
}`, "[10, 20]\n[30]\n[]\n"},
		{"bindings before and after tests", `import std/io
fn choose(p: (Int, String, Int)): Int {
  case p {
    (n, "yes", 2) -> n + 10
    (n, "no", _) -> n + 20
    (0, _, n) -> n + 30
    _ -> 40
  }
}
fn main() {
  io.print(choose((3, "yes", 2)))
  io.print(choose((4, "no", 9)))
  io.print(choose((0, "yes", 7)))
  io.print(choose((8, "yes", 7)))
}`, "13\n24\n37\n40\n"},
		{"shadowed names and captured results", `import std/io
fn main() {
  n = 100
  choose = |p: (Int, Int)| {
    case p {
      (n, 1) -> |x: Int| n + x
      (2, n) -> |x: Int| n * x
      _ -> |x: Int| n - x
    }
  }
  first = choose((3, 1))
  second = choose((2, 4))
  other = choose((8, 9))
  io.print(first(10)) io.print(second(10)) io.print(other(10))
  io.print(n)
}`, "13\n40\n90\n100\n"},
		{"multiline binding positions and nested bodies", `import std/io
fn choose(p: (Int, String, Int)): Int {
  case p {
    (
      n,
      "yes",
      other
    ) -> {
      io.print(n)
      if other > 0 { n + other } else { n - other }
    }
    _ -> 0
  }
}
fn main() {
  io.print(choose((10, "yes", 2)))
  io.print(choose((20, "yes", -2)))
  io.print(choose((30, "no", 2)))
}`, "10\n12\n20\n22\n0\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
