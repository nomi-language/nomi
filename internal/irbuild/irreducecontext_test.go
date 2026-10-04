package irbuild

import "testing"

func TestIRReduceContext_EarlyBareVariant(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn reduce(xs: List<Int>): Maybe<Int> {
 Iter.reduce(xs, |_acc: Maybe<Int> = None, x: Int| {
  if x == 0 { return None }
  Some(x)
 })
}
fn reverse(xs: List<Int>): Maybe<Int> {
 Iter.reduce(xs, |_acc: Maybe<Int> = None, x: Int| {
  if x != 0 { return Some(x) }
  None
 })
}
fn main() {
 io.inspect(reduce([1, 0]))
 io.inspect(reduce([0, 2]))
 io.inspect(reduce([]))
 io.inspect(reverse([1, 0]))
 io.inspect(reverse([0, 2]))
 io.inspect(reverse([]))
}
`, "None\nSome(2)\nNone\nNone\nSome(2)\nNone\n")
}
