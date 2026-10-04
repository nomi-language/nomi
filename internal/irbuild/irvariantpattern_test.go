package irbuild

import "testing"

func TestIRVariantPattern_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"string payload and wildcard", `import std/io
enum Message {
  Empty
  Text String
}
fn label(m: Message): String {
  case m {
    Message.Text("hello") -> "greeting"
    Message.Text(_) -> "text"
    _ -> "empty"
  }
}
fn main() {
  io.print(label(Message.Text("hello")))
  io.print(label(Message.Text("other")))
  io.print(label(Message.Empty))
}`, "greeting\ntext\nempty\n"},
		{"bare and bound payload", `import std/io
enum Signal {
  Idle
  Value Int
}
fn number(s: Signal): Int {
  case s {
    Signal.Value(n) -> n + 1
    _ -> 0
  }
}
fn main() {
  io.print(number(Signal.Value(41)))
  io.print(number(Signal.Idle))
}`, "42\n0\n"},
		{"literal payload fallthrough", `import std/io
enum Signal {
  Idle
  Value Int
}
fn label(s: Signal): String {
  case s {
    Signal.Value(0) -> "zero"
    Signal.Value(n) -> if n > 0 { "positive" } else { "negative" }
    _ -> "idle"
  }
}
fn main() {
  io.print(label(Signal.Value(0)))
  io.print(label(Signal.Value(2)))
  io.print(label(Signal.Value(-2)))
  io.print(label(Signal.Idle))
}`, "zero\npositive\nnegative\nidle\n"},
		{"computed subject once", `import std/io
enum Signal {
  Idle
  Value Int
}
fn subject(): Signal { io.print("subject") Signal.Value(3) }
fn main() {
  n = case subject() {
    Signal.Idle -> 0
    Signal.Value(0) -> 10
    Signal.Value(n) -> n
    _ -> 20
  }
  io.print(n)
}`, "subject\n3\n"},
		{"shadowed payload and escaping capture", `import std/io
enum Signal {
  Idle
  Value Int
}
fn main() {
  n = 100
  choose = |s: Signal| {
    case s {
      Signal.Value(n) -> |x: Int| n + x
      _ -> |x: Int| n - x
    }
  }
  first = choose(Signal.Value(3))
  other = choose(Signal.Idle)
  io.print(first(10)) io.print(other(10)) io.print(n)
}`, "13\n90\n100\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
