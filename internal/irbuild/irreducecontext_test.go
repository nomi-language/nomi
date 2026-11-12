package irbuild

import "testing"

func TestIRReduceContext_EarlyBareVariant(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn reduce(xs: List<Int>) -> Maybe<Int>
 Iter.reduce(xs, None, fn _acc: Maybe<Int>, x: Int ->
  if x == 0 then return None
  Some(x)
 end)
end
fn reverse(xs: List<Int>) -> Maybe<Int>
 Iter.reduce(xs, None, fn _acc: Maybe<Int>, x: Int ->
  if x != 0 then return Some(x)
  None
 end)
end
fn main()
 io.inspect(reduce([1, 0]))
 io.inspect(reduce([0, 2]))
 io.inspect(reduce([]))
 io.inspect(reverse([1, 0]))
 io.inspect(reverse([0, 2]))
 io.inspect(reverse([]))
end
`, "None\nSome(2)\nNone\nNone\nSome(2)\nNone\n")
}
