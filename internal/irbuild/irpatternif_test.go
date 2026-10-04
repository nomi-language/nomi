package irbuild

import "testing"

func TestIRPatternIf_CompleteProgram(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn lookup(id: Int): Maybe<String> {
  io.print(id)
  case id {
    1 -> Some("Alice")
    _ -> None
  }
}
fn label_for(id: Int): String {
  name = "unknown"
  if Some(name) = lookup(id) { name } else { name }
}
fn main() {
  io.print(label_for(1))
  io.print(label_for(9))
}
`, "1\nAlice\n9\nunknown\n")
}
