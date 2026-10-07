package analysis_test

import (
	"fmt"
	"strings"
	"testing"
)

// A lambda whose position expects no type takes no parameter type from
// anywhere, so an unannotated parameter without a default is an error
// wherever that lambda sits: an unannotated binding, a tuple element (the
// projection that drops it was accepted, and the program could not run), a
// list element. One whose position expects a function type, or whose
// parameters are annotated or defaulted, is fine.
func TestLambdaParamUninferable_WhereNoTypeIsExpected(t *testing.T) {
	errs := checkWithStdlib(`
fn main() {
  f = |x| 1
  v = (|y| {
    _ = y
    1
  }, 2).1
  fs = [|z| 1]
  ok1 = (|a: Int| a + 1, 2).1
  ok2: (Int) -> Int = |b| b + 1
  ok3 = (|c = 0| c + 1, 3).1
  _ = f
  _ = v
  _ = fs
  _ = ok1
  _ = ok2
  _ = ok3
}
`)
	var got []string
	for _, e := range errs {
		got = append(got, fmt.Sprintf("%d:%d %s", e.Line, e.Col, e.Message))
	}
	want := []string{
		"3:8 cannot infer type for parameter x",
		"4:9 cannot infer type for parameter y",
		"8:10 cannot infer type for parameter z",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("errors:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
