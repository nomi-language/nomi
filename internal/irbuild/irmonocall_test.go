package irbuild

import (
	"testing"
)

func TestIRMonoCall_DeclinedRetentionDoesNotChangeEmission(t *testing.T) {
	const src = `import std/io
struct Point { x: Int }
struct Outer {
  p: List<Point>
  hooks: Map<String, (Int) -> Int> = Map.empty()
}
fn pair<T, U>(a: T, b: U): (T, U) { (a, b) }
fn main() {
  io.inspect(pair(1, "a"))
  dbg Outer{p: [Point{x: 2}]}
  return
}`
	p, err := AnalyzeSource("main.nomi", src)
	if err != nil {
		t.Fatal(err)
	}
	retained, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, mod := range retained.IR {
		for _, fn := range mod.Funcs() {
			if fn.Name() == "main" {
				t.Fatal("nominal Debug must decline after instance resolution")
			}
		}
	}
}

func TestIRMonoCall_MissingCheckedSignatureKeepsNativeFallback(t *testing.T) {
	p, err := AnalyzeSource("main.nomi", "import std/io\nfn pair<T, U>(a: T, b: U): (T, U) { (a, b) }\nfn main() { io.inspect(pair(42, \"x\")) }")
	if err != nil {
		t.Fatal(err)
	}
	cleared := 0
	for _, sym := range p.Entry().FA.References {
		if sym.Name == "pair" && sym.CallType != nil {
			sym.CallType = nil
			cleared++
		}
	}
	if cleared != 1 {
		t.Fatalf("cleared %d checked signatures, want 1", cleared)
	}
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, mod := range res.IR {
		for _, fn := range mod.Funcs() {
			if fn.Name() == "main" {
				t.Fatal("call without a checked signature retained")
			}
		}
	}
}

func TestIRMonoCall_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"pair", `fn pair<T, U>(first: T, second: U): (T, U) { (first, second) }
fn main() { dbg pair("age", 30) }`, "dbg line 2: pair(\"age\", 30) = (\"age\", 30)\n"},
		{"distinct and reused instances", `import std/io
fn sum<T>(pair: (T, T)): T where T: Add<T, T> { pair.0 + pair.1 }
fn main() {
  io.print(sum((20, 22)))
  io.print(sum(("a", "b")))
  io.print(sum((3, 4)))
}`, "42\nab\n7\n"},
		{"effects once", `import std/io
fn mark(n: Int): Int { io.print(n) return n }
fn pair<T, U>(a: T, b: U): (T, U) { (a, b) }
fn main() { io.inspect(pair(mark(1), mark(2))) }`, "1\n2\n(1, 2)\n"},
		{"nested generic calls", `import std/io
fn pair<T, U>(a: T, b: U): (T, U) { (a, b) }
fn twice<T>(x: T): (T, T) { pair(x, x) }
fn main() {
  io.inspect(twice(21))
  io.inspect(twice("x"))
}`, "(21, 21)\n(\"x\", \"x\")\n"},
		{"scalar lists", `import std/io
fn wrap<T>(x: T): List<T> { [x] }
fn main() {
  io.inspect(wrap(42))
  io.inspect(wrap("a"))
}`, "[42]\n[\"a\"]\n"},
		{"explicit type arguments", `import std/io
fn pair<T, U>(a: T, b: U): (T, U) { (a, b) }
fn main() { io.inspect(pair<Int, String>(42, "x")) }`, "(42, \"x\")\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
