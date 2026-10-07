package analysis_test

import (
	"fmt"
	"strings"
	"testing"
)

// Calling a value whose type is data, not a function, is an error at the
// callee. Before, the call was accepted and its result passed as any type.
func TestCallNonFunction_IsAnErrorAtTheCallee(t *testing.T) {
	errs := checkWithStdlib(`
struct P {
  a: Int
}

type Handler (Int) -> Int

fn main() {
  _ = 1()
  _ = ""()
  pair = (1, 2)
  _ = pair(3)
  _ = [1](0)
  p = P{a: 1}
  _ = p()
  f = |x: Int| x + 1
  _ = f(1)
  h = Handler(|x| x)
  _ = h(2)
  _ = Wrap.One(#[1])
}

enum Wrap {
  One Vector<Int>
  Zero
}
`)
	var got []string
	for _, e := range errs {
		got = append(got, fmt.Sprintf("%d:%d %s", e.Line, e.Col, e.Message))
	}
	want := []string{
		"9:7 `Int` is not a function",
		"10:7 `String` is not a function",
		"12:7 `(Int, Int)` is not a function",
		"13:7 `List<Int>` is not a function",
		"15:7 `P` is not a function",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("errors:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
