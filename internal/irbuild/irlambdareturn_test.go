package irbuild

import "testing"

func TestIRLambdaReturn_CompletePrograms(t *testing.T) {
	const src = `import std::io
fn main()
 base = 10
 choose = fn n: Int ->
  if n < 0 then
   amount = base - n
   io.print("early")
   return amount
  end
  if n == 0 then return base
  io.print("tail")
  n + base
 end
 io.print(choose(-2))
 io.print(choose(0))
 io.print(choose(3))
end`
	verifyLambdaProgram(t, src, "early\n12\n10\ntail\n13\n")
}

func TestIRLambdaReturn_ListCoercionAndTailBranches(t *testing.T) {
	const src = `import std::io
fn main()
 choose = fn n: Int ->
  if n < 0 then return [n]
  if n == 0 then return []
  if n == 1 then [10] else [20]
 end
 io.inspect(choose(-2))
 io.inspect(choose(0))
 io.inspect(choose(1))
 io.inspect(choose(2))
end`
	verifyLambdaProgram(t, src, "[-2]\n[]\n[10]\n[20]\n")
}

func TestIRLambdaReturn_ActivationAndShortCircuit(t *testing.T) {
	const src = `import std::io
fn check(n: Int) -> Bool
    io.print("check")
    n > 0
end
fn main()
 outer = fn n: Int ->
  inner = fn x: Int ->
   if x > 0 and check(x) then return x + n
   0
  end
  answer = inner(n)
  io.print("outer")
  answer + 1
 end
 io.print(outer(-1))
 io.print(outer(2))
 io.print("main")
end`
	verifyLambdaProgram(t, src, "outer\n1\ncheck\nouter\n5\nmain\n")
}
