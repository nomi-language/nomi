package irbuild

import "testing"

func TestIRBlockBinding_CompletePrograms(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn main() {
 total = { a = 10 b = 20 a + b }
 dbg total
 first = { item = 3 io.print(item) item + 1 }
 second = { item = 8 nested = { value = 2 value * 3 } item + nested }
 item = 100
 io.print(first)
 io.print(second)
 io.print(item)
 base = 5
 calculate = |n: Int| { answer = { offset = base + n offset * 2 } answer + 1 }
	io.print(calculate(3))
}`, "dbg line 4: total = 30\n3\n4\n14\n100\n17\n")
}

func TestIRBlockBinding_EscapingCallable(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn main() {
 f = { base = 4 |n: Int| base + n }
 g = { base = 10 |n: Int| base * n }
 io.print(f(3))
 io.print(g(3))
 io.print(f(5))
}`, "7\n30\n9\n")
}
