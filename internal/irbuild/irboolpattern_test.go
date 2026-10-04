package irbuild

import "testing"

func TestIRBoolPattern_CaseBranches(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn label(b: Bool): String {
  case b {
    True -> "yes"
    False -> "no"
  }
}
fn main() {
  io.print(label(True))
  io.print(label(False))
}
`, "yes\nno\n")
}

func TestIRBoolPattern_QualifiedFallbackAndClosure(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn source(b: Bool): Bool { io.print("subject") b }
fn label(b: Bool): String {
  case source(b) {
    Bool.False -> "false"
    _ -> "otherwise"
  }
}
fn main() {
  io.print(label(False))
  io.print(label(True))
  b = False
  read = || case b {
    Bool.True -> "yes"
    Bool.False -> "no"
  }
  io.print(read())
  case (True) {
    True -> io.print("matched")
    False -> io.print("wrong")
  }
  io.print("after")
}
`, "subject\nfalse\nsubject\notherwise\nno\nmatched\nafter\n")
}
