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

// The error's hint shows the annotation that fixes it, with the parameter's
// own name: a lambda bound to a name does not take its parameter's type from
// its body or a later call (spec, "A later use never types a parameter").
func TestLambdaParamUninferable_HintShowsTheAnnotation(t *testing.T) {
	errs := checkWithStdlib(`
fn leap?(year: Int): Bool {
  divisible? = |n| year % n == 0
  divisible?(4) and (!divisible?(100) or divisible?(400))
}
`)
	if len(errs) == 0 {
		t.Fatal("the checker admits `|n| year % n == 0` bound to a name")
	}
	want := "cannot infer type for parameter n\n" +
		"help: annotate the parameter with its type, as in `|n: T|`; a later use of the lambda does not give it one"
	if got := diagText(errs[0]); got != want || errs[0].Line != 3 || errs[0].Col != 17 {
		t.Fatalf("first error at %d:%d:\n%s\nwant at 3:17:\n%s", errs[0].Line, errs[0].Col, got, want)
	}
}
