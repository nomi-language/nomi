package irbuild

import "testing"

func TestIRLambdaDefaults_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"captured condition and list result", `import std/io
fn main() {
 choose = || { io.print("choose") False }
 f = |xs: List<Int> = if choose() { [1] } else { [2, 3] }| xs
 alias = f
 io.inspect(alias())
 io.inspect(f([9]))
}`, "choose\n[2, 3]\n[9]\n"},
		{"conditional suppliers", `import std/io
fn mark(n: Int): Int { io.print(n) n }
fn main() {
 base = 10
 f = |flag: Bool, a: Int = if flag { mark(base) } else { mark(20) }, b: Int = if a == 10 { mark(a + 1) } else { mark(a + 2) }| a + b
 io.print(f(True))
 io.print(f(False))
 io.print(f(False, 30, 40))
}`, "10\n11\n21\n20\n22\n42\n70\n"},
		{"case suppliers and scoped arms", `import std/io
fn mark(n: Int): Int { io.print(n) n }
fn main() {
 base = 5
 f = |n: Int, x: Int = case n {
  0 -> { offset = base + 1 mark(offset) }
  1 -> if n == 1 { mark(7) } else { mark(8) }
  _ -> mark(9)
 }| x
 io.print(f(0))
 io.print(f(1))
 io.print(f(2))
 io.print(f(0, 42))
}`, "6\n6\n7\n7\n9\n9\n42\n"},
		{"tour shape", `import std/io
fn greet(name: String, greeting: String = "Hello"): String { "${greeting}, ${name}!" }
fn main() {
 io.print(greet("World"))
 add = |x: Int, y = 10| x + y
 io.print(add(5))
 io.print(add(5, 2))
}`, "Hello, World!\n15\n7\n"},
		{"dependent defaults execute once", `import std/io
fn mark(n: Int): Int { io.print(n) n }
fn main() {
 f = |a = mark(1), b = mark(a + 1), c = mark(b + 1)| a + b + c
 io.print(f())
 io.print(f(a: 10))
 io.print(f(c: mark(30), a: mark(10)))
 io.print(f(7, 8, 9))
}`, "1\n2\n3\n6\n11\n12\n33\n30\n10\n11\n51\n24\n"},
		{"captures and aliases", `import std/io
fn main() {
 base = 40
 f = |x = base, y = x + 2| y
 alias = f
 io.print(alias())
 io.print(alias(y: 0, x: 1))
 io.print(f(10))
}`, "42\n0\n12\n"},
		{"callable default captures earlier parameter", `import std/io
fn main() {
 apply = |x: Int, f: (Int) -> Int = |n: Int| n + x| f(2)
 io.print(apply(40))
 io.print(apply(100, |n| n * 3))
}`, "42\n6\n"},
		{"explicit ordering and zero values", `import std/io
fn mark(n: Int): Int { io.print(n) n }
fn main() {
 f = |a: Int, b = mark(20), c = mark(30)| a + b + c
 io.print(f(c: mark(9), mark(7)))
 zero = |x = 99| x
 io.print(zero(0))
 bool = |x = True| x
 io.print(bool(False))
 text = |x = "hello"| x
 io.print("[${text("")}]")
}`, "7\n9\n20\n36\n0\nFalse\n[]\n"},
		{"short-circuit defaults", `import std/io
fn mark(): Bool { io.print("unexpected") True }
fn main() {
 f = |a = False and mark(), b = True or mark()| if a { 1 } else if b { 2 } else { 3 }
 io.print(f())
}`, "2\n"},
		{"multiline supplier cursors", `import std/io
fn mark(n: Int): Int { io.print(n) n }
fn main() {
 base = 10
 f = |a: Int =
   mark(base), b: Int =
   mark(a + 1)| {
   a + b
 }
 io.print(f())
}`, "10\n11\n21\n"},
		{"higher order and unused suppliers", `import std/io
fn apply(f: (Int) -> Int): Int { f(2) }
fn mark(n: Int): Int { io.print(n) n }
fn main() {
 io.print(apply(|x = mark(99)| x + 1))
 choose = |x: Int, _label = "z", cb: (Int) -> Int| cb(x) + 1
 io.print(choose(7, |x| x * 2))
}`, "3\n15\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
