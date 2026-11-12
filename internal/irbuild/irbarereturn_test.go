package irbuild

import "testing"

func TestIRBareReturn_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"named sequential guards", `import std::io
fn check(n: Int) -> Bool
    io.print("check")
    n > 0
end
fn visit(n: Int)
  if n < 0 then
    message = "negative"
    io.print(message)
    return
  end

  if n > 0 and check(n) then return
  io.print("zero")
end
fn main()
  visit(-1)
  visit(0)
  visit(1)
  io.print("done")
end`, "negative\nzero\ncheck\ndone\n"},
		{"captured lambda activation", `import std::io
fn main()
  message = "tail"
  visit = fn stop: Bool ->
    if stop then
      io.print("early")
      return
    end

    io.print(message)
  end
  visit(True)
  visit(False)
  io.print("main")
end`, "early\ntail\nmain\n"},
		{"nested lambda activation", `import std::io
fn main()
  outer = fn stop: Bool ->
    inner = fn ->
      if stop then return
      io.print("inner")
    end
    inner()
    io.print("outer")
  end
  outer(True)
  outer(False)
  io.print("main")
end`, "outer\ninner\nouter\nmain\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
