package irbuild

import "testing"

func TestIRCaseGuard_CompleteProgram(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn classify(n: Int): String {
  case n {
    0 -> "zero"
    n when n < 0 -> "negative"
    n when n > 100 -> "huge"
    _ -> "regular"
  }
}
fn main() {
  io.print(classify(0))
  io.print(classify(-5))
  io.print(classify(500))
  io.print(classify(42))
}
`, "zero\nnegative\nhuge\nregular\n")
}

// A final unguarded identifier arm binds the subject, for Int and String
// subjects, and its body may shadow and read it.
func TestIRCaseGuard_IdentifierFallback(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn describe(s: String): String {
  case s {
    "q" -> "quit"
    other -> {
      n = String.length(other)
      "${other}:${n}"
    }
  }
}
fn bump(n: Int): Int {
  case n + 1 {
    1 -> 100
    m -> m * 2
  }
}
fn main() {
  io.print(describe("q"))
  io.print(describe("abc"))
  io.print(bump(0))
  io.print(bump(4))
}
`, "quit\nabc:3\n100\n10\n")
}
