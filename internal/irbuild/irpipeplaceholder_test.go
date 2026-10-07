package irbuild

import (
	"testing"
)

func TestIRPipePlaceholder_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"multiline effects", `import std/io
fn mark(n: Int): Int { io.print(n) return n }
fn sub(a: Int, b: Int): Int { a - b }
fn main() {
  answer = mark(3)
    |> sub(mark(10), _)
  io.print(answer)
}`, "3\n10\n7\n"},
		{"defaults and scope", `import std/io
fn add(a: Int, b: Int, c: Int = a + b): Int { a + b + c }
fn main() {
  x = 5
  answer = x |> add(10, _)
  io.print(answer)
  io.print(x)
}`, "30\n5\n"},
		{"nonfirst slot", `fn divide(x: Int, y: Int): Int { x / y }
fn main() { 10 |> divide(100, _) |> dbg }`, "dbg line 2: 10 |> divide(100, _) = 10\n"},
		{"repeated effectful value", `import std/io
fn mark(n: Int): Int { io.print(n) return n }
fn combine(a: Int, b: Int, c: Int): Int { a * 100 + b * 10 + c }
fn main() { io.print(mark(1) |> combine(_, mark(2), _)) }`, "1\n2\n121\n"},
		{"pure value and later binding", `import std/io
fn add(a: Int, b: Int): Int { a + b }
fn main() {
  a = 7 |> add(_, _)
  b = a + 1
  io.print(b)
}`, "15\n"},
		{"nested pipelines", `import std/io
fn sub(a: Int, b: Int): Int { a - b }
fn main() { io.print((3 |> sub(10, _)) |> sub(_, 2)) }`, "5\n"},
		{"generic call", `import std/io
fn pair<T, U>(a: T, b: U): (T, U) { (a, b) }
fn main() { io.inspect(42 |> pair("answer", _)) }`, "(\"answer\", 42)\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
