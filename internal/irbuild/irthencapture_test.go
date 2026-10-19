package irbuild

import (
	"testing"
)

// A `then` stage applies its capture to the piped value: the value is computed
// once, before the capture's other arguments, and fills each `&1`.
func TestIRThenCapture_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"multiline effects", `import std/io
fn mark(n: Int) -> Int { io.print(n) return n }
fn sub(a: Int, b: Int) -> Int { a - b }
fn main() {
  answer = mark(3)
    |> then &sub(mark(10), &1)
  io.print(answer)
}`, "3\n10\n7\n"},
		{"defaults and scope", `import std/io
fn add(a: Int, b: Int, c: Int = a + b) -> Int { a + b + c }
fn main() {
  x = 5
  answer = x |> then &add(10, &1)
  io.print(answer)
  io.print(x)
}`, "30\n5\n"},
		{"nonfirst slot", `fn divide(x: Int, y: Int) -> Int { x / y }
fn main() { 10 |> then &divide(100, &1) |> dbg }`, "dbg line 2: 10 |> then &divide(100, &1) = 10\n"},
		{"repeated effectful value", `import std/io
fn mark(n: Int) -> Int { io.print(n) return n }
fn combine(a: Int, b: Int, c: Int) -> Int { a * 100 + b * 10 + c }
fn main() { io.print(mark(1) |> then &combine(&1, mark(2), &1)) }`, "1\n2\n121\n"},
		{"pure value and later binding", `import std/io
fn add(a: Int, b: Int) -> Int { a + b }
fn main() {
  a = 7 |> then &add(&1, &1)
  b = a + 1
  io.print(b)
}`, "15\n"},
		{"nested pipelines", `import std/io
fn sub(a: Int, b: Int) -> Int { a - b }
fn main() { io.print((3 |> then &sub(10, &1)) |> then &sub(&1, 2)) }`, "5\n"},
		{"generic call", `import std/io
fn pair<T, U>(a: T, b: U) -> #(T, U) { #(a, b) }
fn main() { io.inspect(42 |> then &pair("answer", &1)) }`, "#(\"answer\", 42)\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
