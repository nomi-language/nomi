package irbuild

import "testing"

func TestIRLambda_BranchBindings(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"Unit binding", `import std/io
fn main() {
 emit = |flag: Bool| {
  done = if flag { io.print("left") } else { io.print("right") }
  done
 }
 emit(True)
 emit(False)
}
`, "left\nright\n"},
		{"named function", `import std/io
fn choose(x: Int): Int {
 first = if x > 0 { x + 1 } else { 3 }
 second = case first { 2 -> 20; _ -> first + 10 }
 second + 1
}
fn main() {
 io.print(choose(1))
 io.print(choose(2))
 io.print(choose(0))
}
`, "21\n14\n14\n"},
		{"sequential bindings", `import std/io
fn main() {
 choose = |x: Int| {
  first = if x > 0 { x + 1 } else { 3 }
  second = case first { 2 -> 20; _ -> first + 10 }
  io.print(first)
  second + 1
 }
 io.print(choose(1))
 io.print(choose(2))
 io.print(choose(0))
}
`, "2\n21\n3\n14\n3\n14\n"},
		{"binding inside arms", `import std/io
fn main() {
 choose = |x: Int, flag: Bool| {
  answer = if flag {
   inner = case x { 0 -> 3; _ -> 4 }
   io.print("left")
   inner + 10
  } else {
   inner = if x > 0 { 5 } else { 6 }
   io.print("right")
   inner + 20
  }
  if answer > 20 { answer + 1 } else { answer + 2 }
 }
 io.print(choose(0, True))
 io.print(choose(1, True))
 io.print(choose(0, False))
 io.print(choose(1, False))
}
`, "left\n15\nleft\n16\nright\n27\nright\n26\n"},
		{"closure binding", `import std/io
fn main() {
 choose = |flag: Bool| {
  add = if flag {
   amount = 2
   |x: Int| x + amount
  } else {
   amount = 5
   |x: Int| x + amount
  }
  io.print(add(10))
  add
 }
 a = choose(True)
 b = choose(False)
 io.print(a(20))
 io.print(b(20))
}
`, "12\n15\n22\n25\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
