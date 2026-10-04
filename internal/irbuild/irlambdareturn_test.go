package irbuild

import "testing"

func TestIRLambdaReturn_CompletePrograms(t *testing.T) {
	const src = `import std/io
fn main() {
 base = 10
 choose = |n: Int| {
  if n < 0 {
   amount = base - n
   io.print("early")
   return amount
  }
  if n == 0 { return base }
  io.print("tail")
  n + base
 }
 io.print(choose(-2))
 io.print(choose(0))
 io.print(choose(3))
}`
	verifyLambdaProgram(t, src, "early\n12\n10\ntail\n13\n")
}

func TestIRLambdaReturn_ListCoercionAndTailBranches(t *testing.T) {
	const src = `import std/io
fn main() {
 choose = |n: Int| {
  if n < 0 { return [n] }
  if n == 0 { return [] }
  if n == 1 { [10] } else { [20] }
 }
 io.inspect(choose(-2))
 io.inspect(choose(0))
 io.inspect(choose(1))
 io.inspect(choose(2))
}`
	verifyLambdaProgram(t, src, "[-2]\n[]\n[10]\n[20]\n")
}

func TestIRLambdaReturn_ActivationAndShortCircuit(t *testing.T) {
	const src = `import std/io
fn check(n: Int): Bool { io.print("check") n > 0 }
fn main() {
 outer = |n: Int| {
  inner = |x: Int| {
   if x > 0 and check(x) { return x + n }
   0
  }
  answer = inner(n)
  io.print("outer")
  answer + 1
 }
 io.print(outer(-1))
 io.print(outer(2))
 io.print("main")
}`
	verifyLambdaProgram(t, src, "outer\n1\ncheck\nouter\n5\nmain\n")
}
