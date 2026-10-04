package irbuild

import (
	"testing"
)

func TestIRPatternIf_ResultValueAndNestedBranches(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn render(r: Result<Int, String>): String {
  label = if Ok(n) = r { "value ${n}" } else { "error" }
  "result " + label
}
fn classify(m: Maybe<Int>): String {
  if Some(n) = m {
    if n < 0 { "negative" } else { "positive" }
  } else { "empty" }
}
fn main() {
  io.print(render(Ok(7)))
  io.print(render(Err("failed")))
  io.print(classify(Some(-1)))
  io.print(classify(Some(2)))
  io.print(classify(None))
}
`, "result value 7\nresult error\nnegative\npositive\nempty\n")
}

func TestIRPatternIf_LambdaBody(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn main() {
  choose = |m: Maybe<Int>| if Some(n) = m { n } else { 0 }
  io.print(choose(Some(7)))
  io.print(choose(None))
}
`, "7\n0\n")
}

// A pattern `if` with no `else` in a Unit position retains: a miss answers
// Unit, as the Boolean form's does. An identifier or wildcard pattern always
// matches, and its else arm never runs.
func TestIRPatternIf_ElselessAndIrrefutablePatternsRetain(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn show(m: Maybe<Int>) {
  u = if Some(n) = m { io.print(n) }
  io.print(u == Unit)
}
fn whole(n: Int): Int { if k = n + 1 { k } else { 0 } }
fn main() {
  show(Some(7))
  show(None)
  io.print(whole(4))
  io.print(if _ = 3 { "matched" } else { "impossible" })
}
`, "7\nTrue\nTrue\n5\nmatched\n")
}

// A list pattern inside a pattern `if` retains through the nested list test.
func TestIRPatternIf_ListPayloadRetains(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn choose(m: Maybe<List<Int>>): Int { if Some([x]) = m { x } else { 0 } }
fn main() {
  io.print(choose(Some([7])))
  io.print(choose(Some([7, 8])))
  io.print(choose(None))
}
`, "7\n0\n0\n")
}
