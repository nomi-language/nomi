package irbuild

import "testing"

func TestIRPatternIf_NestedPayloadScopes(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn combine(a: Maybe<Int>, b: Maybe<Int>) -> Int
  if Some(n) = a then
    if Some(m) = b then n + m else n
  else
      0
  end
end
fn main()
  io.print(combine(Some(7), Some(3)))
  io.print(combine(Some(7), None))
  io.print(combine(None, Some(3)))
end
`, "10\n7\n0\n")
}
