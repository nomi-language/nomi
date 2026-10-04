package irbuild

import "testing"

func TestIRPipeKeyword_CompletePrograms(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn mark(s: String): String { io.print(s) return s }
fn main() {
  label = mark("Ada")
    |> if String.contains?("A") {
      "initialed"
    } else {
      "plain"
    }
  io.print(label)
  number = Some(mark("value"))
    |> case {
      Some(s) -> "some ${s}"
      None -> "empty"
    }
  io.print(number)
}
`, "Ada\ninitialed\nvalue\nsome value\n")
}

func TestIRPipeKeyword_BareStagesAndFollowingCalls(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn choose(b: Bool): String {
  b |> if { "yes" } else { "no" } |> String.to_lower()
}
fn describe(m: Maybe<Int>): Int {
  m |> case { Some(n) -> n + 1 None -> 0 }
}
fn main() {
  io.print(choose(True))
  io.print(choose(False))
  io.print(describe(Some(41)))
  io.print(describe(None))
}
`, "yes\nno\n42\n0\n")
}
