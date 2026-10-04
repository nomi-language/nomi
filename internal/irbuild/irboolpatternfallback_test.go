package irbuild

import (
	"testing"
)

// `Bool.True(x)` binds the embedded singleton as a marker of std/bool's
// `host type True`, not as the Bool: x's impls are True's own, so
// `Debug.inspect(x)` renders the singleton's name and nothing dispatches back
// into Bool's derived impls (whose `True(x) -> Display.to_string(x)` would
// then call itself). The body retains and prints the expected output.
func TestIRBoolPattern_SingletonPayloadBindsAMarker(t *testing.T) {
	src := `import std/io

fn choose(b: Bool): String {
  case b {
    Bool.True(x) -> Debug.inspect(x)
    Bool.False(_) -> "no"
  }
}

fn main() {
  io.print(choose(True))
  io.print(choose(False))
}
`
	if names := irRetainedFuncNames(t, src); !names["choose"] {
		t.Fatal("choose was not retained")
	}
	verifyLambdaProgram(t, src, "True\nno\n")
}
