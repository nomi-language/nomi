package irbuild

import "testing"

func TestIRCaseGuard_EffectsStopAtTheSelectedArm(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn subject(n: Int): Int { io.print("subject") n }
fn check(label: String, matched: Bool): Bool { io.print(label) matched }
fn classify(n: Int): String {
  case subject(n) {
    value when check("negative", value < 0) -> "negative"
    _ when check("large", n > 10) -> "large"
    _ -> "regular"
  }
}
fn main() {
  io.print(classify(-1))
  io.print(classify(20))
  io.print(classify(5))
}
`, "subject\nnegative\nnegative\nsubject\nnegative\nlarge\nlarge\nsubject\nnegative\nlarge\nregular\n")
}

func TestIRCaseGuard_StringBindingsAndShadowing(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn classify(s: String): String {
  result = case s {
    s when String.length(s) == 0 -> "empty"
    s when String.contains?(s, "!") -> "excited"
    _ -> "plain"
  }
  result + ":" + s
}
fn main() {
  io.print(classify(""))
  io.print(classify("hello!"))
  io.print(classify("hello"))
}
`, "empty:\nexcited:hello!\nplain:hello\n")
}
