package irbuild

import (
	"testing"
)

// A String host call's result builds a struct whose field holds a Map of
// function values.
func TestIRStringHost_BesideAFunctionMapFieldRuns(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
struct Point { x: String }
struct Outer {
  p: List<Point>
  hooks: Map<String, (Int) -> Int> = %{}
}
fn main() { s = String.trim(" x ") io.inspect(#Outer{p: [#Point{x: s}]}) }
`, "#Outer{p: [#Point{x: \"x\"}], hooks: %{}}\n")
}

func TestIRStringHost_PipelineConstructors(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
type Meter Int
enum Signal {
  Ready Int
  Waiting
}
fn number(Meter(n)) -> Int { n }
fn signal(s: Signal) -> Int { case s { Signal.Ready(n) -> n Signal.Waiting -> 0 } }
fn main() {
  io.inspect(1 |> Some())
  io.print(2 |> Meter() |> number())
  io.print(3 |> Signal.Ready() |> signal())
}
`, "Some(1)\n2\n3\n")
}

func TestIRStringHost_CompletePrograms(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn parse(s: String) -> Maybe<Int> {
  s |> String.trim() |> String.to_int()? |> Some()
}
fn main() {
  io.print(String.lowercase(String.trim("  JANE  ")))
  io.print(String.uppercase("Ada lovelace"))
  io.inspect(String.contains("Ada Lovelace", "Ada"))
  io.inspect(parse("  42  "))
  io.inspect(parse("no"))
  io.inspect(parse("9223372036854775808"))
  io.inspect(parse("0x10"))
  io.inspect(parse("-9223372036854775808"))
}
`, "jane\nADA LOVELACE\nTrue\nSome(42)\nNone\nSome(9223372036854775808)\nNone\nSome(-9223372036854775808)\n")
}

func TestIRStringHost_EffectsAndSiblingCaller(t *testing.T) {
	verifyLambdaProgram(t, `import { std/io lib }
fn mark(s: String) -> String { io.print(s) return s }
fn main() {
  io.inspect(String.contains(
    mark("Ada"),
    mark("A") + mark("da")
  ))
  io.inspect(lib.parse("7"))
}
`, "Ada\nA\nda\nTrue\nSome(7)\n", map[string]string{
		"lib.nomi": "pub fn parse(s: String) -> Maybe<Int> { String.to_int(s) }",
	})
}

func TestIRStringHost_CustomDistinctEquality(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
type Slug String
impl Equatable for Slug {
  fn equal(a: Slug, b: Slug) -> Bool {
    Slug(x) = a
    Slug(y) = b
    String.lowercase(x) == String.lowercase(y)
  }
}
fn main() {
  io.inspect(Slug.equal(Slug("Home"), Slug("home")))
  io.inspect(Slug.equal(Slug("home"), Slug("away")))
}
`, "True\nFalse\n")
}
