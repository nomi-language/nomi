package irbuild

import (
	"testing"
)

func TestIRPreludeEquality_CompletePrograms(t *testing.T) {
	verifyLambdaProgram(t, `import
    std::io std::literals.Fragment
end
fn same(a: Maybe<String>, b: Maybe<String>) -> Bool
    a == b
end
fn different(a: Result<Int, String>, b: Result<Int, String>) -> Bool
    a != b
end
fn main()
  io.print(same(Some("Ada"), Some("Ada")))
  io.print(same(Some("Ada"), Some("Grace")))
  io.print(same(Some("Ada"), None))
  io.print(same(None, None))
  io.print(different(Ok(42), Ok(42)))
  io.print(different(Ok(42), Ok(43)))
  io.print(different(Ok(42), Err("bad")))
  io.print(different(Err("bad"), Err("bad")))
  io.print(Fragment.Dynamic(7) == Fragment.Static<Int>("seven"))
  io.print(Some(0.0 / 0.0) == Some(0.0 / 0.0))
  io.print(Some(0.0 / 0.0) != Some(0.0 / 0.0))
end
`, "True\nFalse\nFalse\nTrue\nFalse\nTrue\nTrue\nFalse\nFalse\nTrue\nFalse\n")
}

func TestIRPreludeEquality_LambdaPipelineEffects(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn mark(s: String) -> Maybe<String>
    io.print(s)
    Some(s)
end
fn main()
  expected = Some("left")
  answer = mark("left")
    |> then fn name -> name == expected
  io.print(answer)
  io.print(mark("first") != mark("second"))
  io.print(40 |> then fn n -> n + 2)
end
`, "left\nTrue\nfirst\nsecond\nTrue\n42\n")
}

// `==` on an enum with a hand-written Equatable calls the impl; `!=` negates
// its answer.
func TestIRPreludeEquality_NominalDispatchCallsTheImpl(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
enum Answer
  Value Int
end
impl Equatable for Answer
  fn equal(_a: Answer, _b: Answer) -> Bool
      True
  end
end
fn main()
  io.print(Answer.Value(1) == Answer.Value(2))
  io.print(Answer.Value(1) != Answer.Value(2))
end
`, "True\nFalse\n")
}
