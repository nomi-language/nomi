package irbuild

import "testing"

func TestIRStringLength_GraphemesAndEffects(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn mark(s: String): String { io.print("evaluated") s }
fn main() {
  io.print(String.length(""))
  io.print(String.length("hello"))
  io.print(String.length("café"))
  io.print(String.length("é"))
  io.print(String.length("👨‍👩‍👧‍👦"))
  io.print(String.length("🇺🇸"))
  io.print(String.length("\n"))
  io.print(String.length(mark("áb")))
}
`, "0\n5\n4\n1\n1\n1\n1\nevaluated\n2\n")
}
