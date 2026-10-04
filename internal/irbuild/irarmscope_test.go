package irbuild

import "testing"

func TestIRArmScope_NamedFunctions(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"sibling names and effects", `import std/io
fn choose(flag: Bool): Int {
  if flag { n = 10 io.print("then") n + 1 }
  else { n = 20 io.print("else") n + 2 }
}
fn main() { io.print(choose(True)) io.print(choose(False)) }
`, "then\n11\nelse\n22\n"},
		{"else if and nested bindings", `import std/io
fn choose(a: Bool, b: Bool): Int {
  if a { n = 3 if b { n + 1 } else { n + 2 } }
  else if b { n = 10 n + 1 } else { n = 20 n + 2 }
}
fn main() {
  io.print(choose(True, True)) io.print(choose(True, False))
  io.print(choose(False, True)) io.print(choose(False, False))
}`, "4\n5\n11\n22\n"},
		{"case arm scopes", `import std/io
fn choose(n: Int): Int {
  case n {
    1 -> { x = 10 io.print("one") x + 1 }
    2 -> { x = 20 io.print("two") x + 2 }
    _ -> { x = 30 io.print("other") x + 3 }
  }
}
fn main() { io.print(choose(1)) io.print(choose(2)) io.print(choose(3)) }
`, "one\n11\ntwo\n22\nother\n33\n"},
		{"escaping arm capture", `import std/io
fn choose(flag: Bool): (Int) -> Int {
  if flag { base = 10 |n: Int| base + n }
  else { base = 20 |n: Int| base + n }
}
fn main() { a = choose(True) b = choose(False) io.print(a(1)) io.print(b(2)) }
`, "11\n22\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
