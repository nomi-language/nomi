package irbuild

import "testing"

func TestIRCaseDiscard_EffectsWithoutResultStorage(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn source(): Int { io.print("subject") 2 }
fn main() {
  case source() {
    1 -> io.print("one")
    _ -> io.print("other")
  }
  io.print("after")
}
`, "subject\nother\nafter\n")
}

func TestIRCaseDiscard_NestedGuardedScopes(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn main() {
  name = "outer"
  case 2 {
    n when n > 1 -> {
      name = "inner"
      case n {
        2 -> io.print(name)
        _ -> io.print("missing")
      }
    }
    _ -> io.print("fallback")
  }
  io.print(name)
  case {
    False -> io.print("no")
    _ -> io.print("done")
  }
  io.print("after")
}
`, "inner\nouter\ndone\nafter\n")
}

func TestIRCaseDiscard_OperandKeepsResultBeforeSubject(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn source(): Int { io.print("subject") 2 }
fn main() {
  _ = case source() {
    1 -> "one"
    _ -> "other"
  }
  io.print("after")
}
`, "subject\nafter\n")
}

func TestIRCaseDiscard_ListPayloadThenClosure(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn main() {
  nested = [[1, 2], [3]]
  case List.head(nested) {
    Some(xs) -> io.inspect(List.tail(xs))
    None -> io.print("missing")
  }
  read = || List.head(nested)
  io.inspect(read())
}
`, "Some([2])\nSome([1, 2])\n")
}
