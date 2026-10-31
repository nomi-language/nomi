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
  f = fn x -> 1
  v = #(fn y -> {
    _ = y
    1
  }, 2).1
  fs = [fn z -> 1]
  ok1 = #(fn a: Int -> a + 1, 2).1
  ok2: (Int) -> Int = fn b -> b + 1
  ok3 = #(fn c = 0 -> c + 1, 3).1
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
		"3:10 cannot infer type for parameter x",
		"4:12 cannot infer type for parameter y",
		"8:12 cannot infer type for parameter z",
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
fn is_leap(year: Int) -> Bool {
  is_divisible = fn n -> year % n == 0
  is_divisible(4) and (not is_divisible(100) or is_divisible(400))
}
`)
	if len(errs) == 0 {
		t.Fatal("the checker admits `fn n -> year % n == 0` bound to a name")
	}
	want := "cannot infer type for parameter n\n" +
		"help: annotate the parameter with its type, as in `fn n: T -> ...`; a later use of the lambda does not give it one"
	if got := diagText(errs[0]); got != want || errs[0].Line != 3 || errs[0].Col != 21 {
		t.Fatalf("first error at %d:%d:\n%s\nwant at 3:21:\n%s", errs[0].Line, errs[0].Col, got, want)
	}
}
