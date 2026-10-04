package irbuild

import (
	"testing"
)

func TestIRTuplePattern_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"conjunction and fallthrough", `import std/io
fn classify(p: (Int, Int)): String {
  case p {
    (0, 0) -> "origin"
    (0, _) -> "y-axis"
    (_, 0) -> "x-axis"
    _ -> "other"
  }
}

fn main() {
  io.print(classify((0, 0))) io.print(classify((0, 2)))
  io.print(classify((3, 0))) io.print(classify((3, 2)))
}`, "origin\ny-axis\nx-axis\nother\n"},
		{"ordered overlapping arms", `import std/io
fn classify(p: (String, Int, Int)): Int {
  case p {
    ("yes", _, 1) -> { n = 10 n + 1 }
    (_, 2, 1) -> { n = 20 n + 2 }
    ("yes", 2, _) -> 33
    _ -> 44
  }
}
fn main() {
  io.print(classify(("yes", 2, 1))) io.print(classify(("no", 2, 1)))
  io.print(classify(("yes", 2, 0))) io.print(classify(("no", 0, 0)))
}`, "11\n22\n33\n44\n"},
		{"nested arms and multiline patterns", `import std/io
fn main() {
  base = 10
  choose = |p: (Int, String), flag: Bool| {
    case p {
      (
        1,
        "yes"
      ) -> {
        n = base + 1
        if flag { n + 1 } else { n + 2 }
      }
      (_, "yes") -> case p.0 { 2 -> 20 _ -> 30 }
      _ -> 40
    }
  }
  io.print(choose((1, "yes"), True))
  io.print(choose((1, "yes"), False))
  io.print(choose((2, "yes"), False))
  io.print(choose((3, "yes"), False))
  io.print(choose((1, "no"), True))
}`, "12\n13\n20\n30\n40\n"},
		{"computed subject once", `import std/io
fn subject(): (Int, Int) { io.print("subject") return (1, 2) }
fn main() {
  result = case subject() {
    (0, _) -> 10
    (1, 0) -> 20
    (1, 2) -> 30
    _ -> 40
  }
  io.print(result)
}`, "subject\n30\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
