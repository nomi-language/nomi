package irbuild

import (
	"testing"
)

func TestIRPatternIf_ResultValueAndNestedBranches(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn render(r: Result<Int, String>) -> String
  label = if Ok(n) = r then "value ${n}" else "error"
  "result " + label
end
fn classify(m: Maybe<Int>) -> String
  if Some(n) = m then
    if n < 0 then "negative" else "positive"
  else
      "empty"
  end
end
fn main()
  io.print(render(Ok(7)))
  io.print(render(Err("failed")))
  io.print(classify(Some(-1)))
  io.print(classify(Some(2)))
  io.print(classify(None))
end
`, "result value 7\nresult error\nnegative\npositive\nempty\n")
}

func TestIRPatternIf_LambdaBody(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn main()
  choose = fn m: Maybe<Int> -> if Some(n) = m then n else 0
  io.print(choose(Some(7)))
  io.print(choose(None))
end
`, "7\n0\n")
}

// A pattern `if` with no `else` in a Unit position retains: a miss answers
// Unit, as the Boolean form's does.
func TestIRPatternIf_ElselessPatternRetains(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn show(m: Maybe<Int>)
  u = if Some(n) = m then io.print(n)
  io.print(u == Unit)
end
fn main()
  show(Some(7))
  show(None)
end
`, "7\nTrue\nTrue\n")
}

// A list pattern inside a pattern `if` retains through the nested list test.
func TestIRPatternIf_ListPayloadRetains(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn choose(m: Maybe<List<Int>>) -> Int
    if Some([x]) = m then x else 0
end
fn main()
  io.print(choose(Some([7])))
  io.print(choose(Some([7, 8])))
  io.print(choose(None))
end
`, "7\n0\n0\n")
}
