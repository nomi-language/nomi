package irbuild

import "testing"

func TestIRPatternIf_NestedPayloadScopes(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn combine(a: Maybe<Int>, b: Maybe<Int>): Int {
  if Some(n) = a {
    if Some(m) = b { n + m } else { n }
  } else { 0 }
}
fn main() {
  io.print(combine(Some(7), Some(3)))
  io.print(combine(Some(7), None))
  io.print(combine(None, Some(3)))
}
`, "10\n7\n0\n")
}
