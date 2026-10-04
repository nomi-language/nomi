package irbuild

import "testing"

func TestIRTupleFallback_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"multiline fallback bindings", `import std/io
fn main() {
  choose = |p: (Int, Int)| {
    case p {
      (0, _) -> 10
      (
        x,
        y
      ) -> {
        io.print(x)
        x + y
      }
    }
  }
  io.print(choose((2, 3)))
}`, "2\n5\n"},
		{"final binding pattern", `import std/io
fn sum(p: (Int, Int)): Int {
  case p {
    (0, y) -> y + 100
    (x, y) -> x + y
  }
}
fn main() {
  io.print(sum((0, 2)))
  io.print(sum((3, 4)))
}`, "102\n7\n"},
		{"wildcards and captured fallback", `import std/io
fn main() {
  n = 100
  choose = |p: (Int, Int)| {
    case p {
      (0, _) -> |x: Int| x
      (_, n) -> |x: Int| n + x
    }
  }
  first = choose((0, 7))
  other = choose((1, 9))
  io.print(first(10)) io.print(other(10)) io.print(n)
}`, "10\n19\n100\n"},
		{"all wildcard tuple", `import std/io
fn choose(p: (Int, String)): Int {
  case p {
    (1, "yes") -> 10
    (_, _) -> 20
  }
}
fn main() {
  io.print(choose((1, "yes")))
  io.print(choose((1, "no")))
}`, "10\n20\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
