package irbuild

import (
	"testing"
)

func TestIRRecord_UnsupportedBodiesDecline(t *testing.T) {
	for _, src := range []string{
		// A struct that reaches itself through a field is outside the
		// retained domain.
		"struct Point { x: Int }\nstruct Outer { p: List<Point>\n hooks: Map<String, (Int) -> Int> = Map.empty() }\nfn main() { record = {outer: Outer{p: [Point{x: 1}]}} dbg record return }",
		// A nested patch over a struct outside the retained domain keeps
		// native lowering.
		"struct Point { x: Int }\nstruct Outer { ps: List<Point>\n n: Int\n hooks: Map<String, (Int) -> Int> = Map.empty() }\nfn main() { o = Outer{ps: [Point{x: 1}], n: 1} updated = {..o, n: 2} record = {o: o} dbg {..record, o: {n: 3}} dbg updated return }",
	} {
		p, err := AnalyzeSource("main.nomi", src)
		if err != nil {
			t.Fatal(err)
		}
		res, _, err := GenerateIR(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, mod := range res.IR {
			for _, fn := range mod.Funcs() {
				if fn.Name() == "main" {
					t.Fatal("unsupported record body retained")
				}
			}
		}
	}
}

func TestIRRecord_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"point", `fn main() {
  point = {x: 10, y: 20}
  dbg point
  dbg point.x + point.y
}`, "dbg line 3: point = {x: 10, y: 20}\ndbg line 4: point.x + point.y = 30\n"},
		{"source order", `import std/io
fn mark(n: Int): Int { io.print(n) return n }
fn main() {
  point = {z: mark(1), a: mark(2), m: mark(3)}
  io.inspect(point)
}`, "1\n2\n3\n{a: 2, m: 3, z: 1}\n"},
		{"structural fields", `import std/io
fn main() {
  record = {z: (1, "a\nb"), a: {y: True, x: [1, 2]}}
  io.inspect(record)
  inner = record.a
  io.inspect(inner.x)
}`, "{a: {x: [1, 2], y: True}, z: (1, \"a\nb\")}\n[1, 2]\n"},
		{"parameter and result", `import std/io
fn record(n: Int): {x: Int, y: Int} { {y: n + 1, x: n} }
fn sum(p: {x: Int, y: Int}): Int { p.x + p.y }
fn main() {
  p = record(20)
  io.inspect(p)
  io.print(sum(p))
}`, "{x: 20, y: 21}\n41\n"},
		{"sort names before rendering", `import std/io
fn main() {
  io.inspect({a0: "zero", a: "first", a_1: "last"})
}`, "{a: \"first\", a0: \"zero\", a_1: \"last\"}\n"},
		{"debug escaping", `import std/io
fn main() {
  io.inspect({quote: "a\"b", slash: "a\\b"})
}`, "{quote: \"a\\\"b\", slash: \"a\\\\b\"}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
