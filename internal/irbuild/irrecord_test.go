package irbuild

import (
	"testing"
)

// A record holding a struct whose field holds a Map of function values, and
// a nested patch over one, run.
func TestIRRecord_OverAFunctionMapFieldRuns(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
struct Point { x: Int }
struct Outer { p: List<Point>
 hooks: Map<String, (Int) -> Int> = %{} }
fn main() { record = #{outer: #Outer{p: [#Point{x: 1}]}} io.inspect(record) }
`, "#{outer: #Outer{p: [#Point{x: 1}], hooks: %{}}}\n")
	verifyLambdaProgram(t, `import std/io
struct Point { x: Int }
struct Outer { ps: List<Point>
 n: Int
 hooks: Map<String, (Int) -> Int> = %{} }
fn main() { o = #Outer{ps: [#Point{x: 1}], n: 1} updated = #{..o, n: 2} record = #{o: o} io.inspect(#{..record, o: #{.., n: 3}}) io.inspect(updated) }
`, "#{o: #Outer{ps: [#Point{x: 1}], n: 3, hooks: %{}}}\n#Outer{ps: [#Point{x: 1}], n: 2, hooks: %{}}\n")
}

func TestIRRecord_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"point", `fn main() {
  point = #{x: 10, y: 20}
  dbg point
  dbg point.x + point.y
}`, "dbg line 3: point = #{x: 10, y: 20}\ndbg line 4: point.x + point.y = 30\n"},
		{"source order", `import std/io
fn mark(n: Int) -> Int { io.print(n) return n }
fn main() {
  point = #{z: mark(1), a: mark(2), m: mark(3)}
  io.inspect(point)
}`, "1\n2\n3\n#{a: 2, m: 3, z: 1}\n"},
		{"structural fields", `import std/io
fn main() {
  record = #{z: #(1, "a\nb"), a: #{y: True, x: [1, 2]}}
  io.inspect(record)
  inner = record.a
  io.inspect(inner.x)
}`, "#{a: #{x: [1, 2], y: True}, z: #(1, \"a\\nb\")}\n[1, 2]\n"},
		{"parameter and result", `import std/io
fn record(n: Int) -> #{x: Int, y: Int} { #{y: n + 1, x: n} }
fn sum(p: #{x: Int, y: Int}) -> Int { p.x + p.y }
fn main() {
  p = record(20)
  io.inspect(p)
  io.print(sum(p))
}`, "#{x: 20, y: 21}\n41\n"},
		{"sort names before rendering", `import std/io
fn main() {
  io.inspect(#{a0: "zero", a: "first", a_1: "last"})
}`, "#{a: \"first\", a0: \"zero\", a_1: \"last\"}\n"},
		{"debug escaping", `import std/io
fn main() {
  io.inspect(#{quote: "a\"b", slash: "a\\b"})
}`, "#{quote: \"a\\\"b\", slash: \"a\\\\b\"}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
