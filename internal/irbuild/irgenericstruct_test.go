package irbuild

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

func TestIRGenericStruct_MissingFieldMetadataKeepsNativeFallback(t *testing.T) {
	p, err := AnalyzeSource("main.nomi", "import std/io\nstruct Box<T> { value: T }\nfn main() { box = Box{value: 42} io.print(box.value) }")
	if err != nil {
		t.Fatal(err)
	}
	cleared := 0
	for pos, sym := range p.Entry().FA.References {
		if pos.Line == 3 && sym.Kind == analysis.SymbolField && sym.Name == "value" {
			delete(p.Entry().FA.References, pos)
			cleared++
		}
	}
	if cleared == 0 {
		t.Fatal("no field metadata removed")
	}
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, mod := range res.IR {
		for _, fn := range mod.Funcs() {
			if fn.Name() == "main" {
				t.Fatal("missing field metadata retained")
			}
		}
	}
}

func TestIRGenericStruct_DeclinedRetentionDoesNotChangeEmission(t *testing.T) {
	p, err := AnalyzeSource("main.nomi", "import std/io\nstruct Box<T> { value: T }\nstruct Point { x: Int }\nstruct Outer { p: List<Point>\n f: Map<String, (Int) -> Int> }\nfn main() { box = Box{value: 42} io.print(box.value) dbg Outer{p: [Point{x: 2}], f: Map.empty()} return }")
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
				t.Fatal("nominal Debug must decline")
			}
		}
	}
}

func TestIRGenericStruct_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"generic factory and typed parameter", `import std/io
struct Box<T> { value: T }
fn wrap<T>(x: T): Box<T> { Box{value: x} }
fn read(box: Box<Int>): Int { box.value }
fn main() {
  a = wrap(42)
  b = wrap("x")
  io.print(read(a))
  io.print(b.value)
}`, "42\nx\n"},
		{"box", `struct Box<T> { value: T }
fn main(): Int {
  box = Box{value: 42}
  dbg box.value
}`, "dbg line 4: box.value = 42\n"},
		{"distinct instances", `import std/io
struct Box<T> { value: T }
fn main() {
  a = Box{value: 42}
  b = Box{value: "x"}
  c = Box{value: 7}
  io.print(a.value)
  io.print(b.value)
  io.print(c.value)
}`, "42\nx\n7\n"},
		{"source order", `import std/io
struct Pair<T, U> {
  first: T
  second: U
}
fn mark(n: Int): Int { io.print(n) return n }
fn main() {
  pair = Pair{second: mark(2), first: mark(1)}
  io.print(pair.first)
  io.print(pair.second)
}`, "2\n1\n1\n2\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
