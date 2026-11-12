package irbuild

import "testing"

func TestIRPipeKeyword_CompletePrograms(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn mark(s: String) -> String
    io.print(s)
    return s
end
fn main()
  label = mark("Ada")
    |> if String.contains("A") then
      "initialed"
    else
      "plain"
    end
  io.print(label)
  number = Some(mark("value"))
    |> case
      Some(s) -> "some ${s}"
      None -> "empty"
    end
  io.print(number)
end
`, "Ada\ninitialed\nvalue\nsome value\n")
}

func TestIRPipeKeyword_BareStagesAndFollowingCalls(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn choose(b: Bool) -> String
  b |> if then
      "yes"
  else
      "no"
  end |> String.lowercase()
end
fn describe(m: Maybe<Int>) -> Int
  m |> case
      Some(n) -> n + 1
      None -> 0
  end
end
fn main()
  io.print(choose(True))
  io.print(choose(False))
  io.print(describe(Some(41)))
  io.print(describe(None))
end
`, "yes\nno\n42\n0\n")
}
