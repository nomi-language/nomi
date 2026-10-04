package irbuild

import "testing"

func TestIRBareReturn_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"named sequential guards", `import std/io
fn check(n: Int): Bool { io.print("check") n > 0 }
fn visit(n: Int) {
  if n < 0 {
    message = "negative"
    io.print(message)
    return
  }

  if n > 0 and check(n) { return }
  io.print("zero")
}
fn main() {
  visit(-1)
  visit(0)
  visit(1)
  io.print("done")
}`, "negative\nzero\ncheck\ndone\n"},
		{"captured lambda activation", `import std/io
fn main() {
  message = "tail"
  visit = |stop: Bool| {
    if stop {
      io.print("early")
      return
    }

    io.print(message)
  }
  visit(True)
  visit(False)
  io.print("main")
}`, "early\ntail\nmain\n"},
		{"nested lambda activation", `import std/io
fn main() {
  outer = |stop: Bool| {
    inner = || {
      if stop { return }
      io.print("inner")
    }
    inner()
    io.print("outer")
  }
  outer(True)
  outer(False)
  io.print("main")
}`, "outer\ninner\nouter\nmain\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
