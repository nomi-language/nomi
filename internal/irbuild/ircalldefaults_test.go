package irbuild

import "testing"

func TestIRCallDefaults_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"named scalar defaults", `import std/io
fn connect(host: String, port: Int = 8080, timeout: Int = 30): String {
 "${host}:${port}:${timeout}"
}
fn main() {
 io.print(connect("localhost"))
 io.print(connect("localhost", timeout: 10))
 io.print(connect(timeout: 5, host: "api", port: 443))
}`, "localhost:8080:30\nlocalhost:8080:10\napi:443:5\n"},
		{"trailing callback", `import std/io
fn transform(x: Int, factor: Int = 1, f: (Int) -> Int): Int { f(x * factor) }
fn double(n: Int): Int { n * 2 }
fn main() {
 io.print(transform(5, |x| x + 1))
 io.print(transform(5, factor: 3, |x| x + 1))
 io.print(transform(5, factor: 3, double))
}`, "6\n16\n30\n"},
		{"callee scope and earlier parameters", `import std/io
fn bump(n: Int): Int { io.print(n) n + 1 }
fn total(a: Int, b: Int = bump(a), c: Int = bump(b)): Int { a + b + c }
fn main() {
 a = 90
 bump = |x: Int| x + 1000
 io.print(total(1))
 io.print(total(2, c: bump(1)))
 io.print(total(c: 9, b: 8, a: 7))
 io.print(a)
}`, "1\n2\n6\n2\n1006\n24\n90\n"},
		{"callable defaults and lambda callers", `import std/io
fn apply(n: Int, f: (Int) -> Int = |x: Int| x + n): Int { f(2) }
fn main() {
 base = 10
 caller = |n: Int| apply(n + base)
 io.print(caller(3))
 io.print(apply(100, |x| x * 4))
}`, "15\n8\n"},

		{"explicit operand order", `import std/io
fn mark(n: Int): Int { io.print(n) n }
fn sum(a: Int, b: Int = mark(20), c: Int = mark(30)): Int { a + b + c }
fn main() {
 io.print(sum(mark(1), c: mark(3)))
 io.print(sum(c: mark(6), a: mark(4), b: mark(5)))
 io.print(sum(c: mark(9), mark(7)))
}`, "1\n3\n20\n24\n6\n4\n5\n15\n7\n9\n20\n36\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
