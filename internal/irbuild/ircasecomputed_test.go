package irbuild

import "testing"

func TestIRCaseComputed_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"effectful selector evaluated once", `import std/io
fn source(n: Int): Int { io.print(n) n }
fn classify(n: Int): String {
 case source(n) { 0 -> "zero" 1 -> "one" _ -> "other" }
}
fn main() {
 io.print(classify(0))
 io.print(classify(1))
 io.print(classify(3))
 label = case source(2) { 2 -> "two" _ -> "other" }
 io.print(label)
}`, "0\nzero\n1\none\n3\nother\n2\ntwo\n"},
		{"arithmetic and text selectors", `import std/io
fn number(n: Int): String {
 case (
  n +
  1
 ) { 0 -> "zero" 2 -> "two" _ -> "other" }
}
fn text(s: String): Int { case "p" + s { "prefix" -> 1 "post" -> 2 _ -> 3 } }
fn main() {
 io.print(number(-1))
 io.print(number(1))
 io.print(number(3))
 io.print(text("refix"))
 io.print(text("ost"))
 io.print(text("lain"))
}`, "zero\ntwo\nother\n1\n2\n3\n"},
		{"lambda capture and defaulted selector", `import std/io
fn source(n: Int = 4): Int { io.print(n) n }
fn main() {
 base = 1
 choose = |n: Int| case source(n + base) { 5 -> "five" _ -> "other" }
 io.print(choose(4))
 io.print(choose(1))
 label = case source() { 4 -> "four" _ -> "other" }
 io.print(label)
}`, "5\nfive\n2\nother\n4\nfour\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
