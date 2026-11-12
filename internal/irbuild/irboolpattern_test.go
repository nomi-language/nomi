package irbuild

import "testing"

func TestIRBoolPattern_CaseBranches(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn label(b: Bool) -> String
  case b
    True -> "yes"
    False -> "no"
  end
end
fn main()
  io.print(label(True))
  io.print(label(False))
end
`, "yes\nno\n")
}

func TestIRBoolPattern_QualifiedFallbackAndClosure(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
fn source(b: Bool) -> Bool
    io.print("subject")
    b
end
fn label(b: Bool) -> String
  case source(b)
    Bool.False -> "false"
    _ -> "otherwise"
  end
end
fn main()
  io.print(label(False))
  io.print(label(True))
  b = False
  read = fn -> case b
    Bool.True -> "yes"
    Bool.False -> "no"
  end
  io.print(read())
  case (True)
    True -> io.print("matched")
    False -> io.print("wrong")
  end
  io.print("after")
end
`, "subject\nfalse\nsubject\notherwise\nno\nmatched\nafter\n")
}
