package irbuild

import (
	"testing"
)

func TestIRLambda_NestedCases(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"case in both Boolean arms", `import std/io
fn main() {
 base = 10
 choose = |a: Bool, x: Int| {
  if a {
   amount = base + 1
   case x {
    0 -> { answer = amount + 1; io.print("zero"); answer }
    1 -> amount + 2
    _ -> amount + 3
   }
  } else {
   case x { 0 -> base; _ -> base + 9 }
  }
 }
 io.print(choose(True, 0))
 io.print(choose(True, 1))
 io.print(choose(True, 9))
 io.print(choose(False, 0))
 io.print(choose(False, 9))
}
`, "zero\n12\n13\n14\n10\n19\n"},
		{"Boolean and case inside case", `import std/io
fn main() {
 choose = |x: Int, text: String, flag: Bool| {
  case x {
   0 -> if flag { 1 } else { 2 }
   _ -> case text {
    "a" -> if flag { 3 } else { 4 }
    _ -> { io.print("wild"); 5 }
   }
  }
 }
 io.print(choose(0, "a", True))
 io.print(choose(0, "a", False))
 io.print(choose(1, "a", True))
 io.print(choose(1, "a", False))
 io.print(choose(1, "z", True))
}
`, "1\n2\n3\n4\nwild\n5\n"},
		{"nested cases return closures", `import std/io
fn main() {
 choose = |x: Int, y: Int| {
  case x {
   0 -> case y {
    0 -> { amount = 1; |n: Int| n + amount }
    _ -> { amount = 2; |n: Int| n + amount }
   }
   _ -> case y {
    0 -> { amount = 3; |n: Int| n + amount }
    _ -> { amount = 4; |n: Int| n + amount }
   }
  }
 }
 a = choose(0, 0)
 b = choose(0, 1)
 c = choose(1, 0)
 d = choose(1, 1)
 io.print(a(10))
 io.print(b(10))
 io.print(c(10))
 io.print(d(10))
 io.print(a(20))
}
`, "11\n12\n13\n14\n21\n"},
		{"nested Unit cases", `import std/io
fn main() {
 emit = |x: Int, y: Int| {
  case x {
   0 -> case y { 0 -> io.print("both"); _ -> io.print("first") }
   _ -> case y { 0 -> io.print("second"); _ -> io.print("neither") }
  }
 }
 emit(0, 0)
 emit(0, 1)
 emit(1, 0)
 emit(1, 1)
}
`, "both\nfirst\nsecond\nneither\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
