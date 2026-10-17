package analysis_test

import (
	"fmt"
	"strings"
	"testing"
)

// A variant with no payload is a value, so calling it is an error at the
// callee in every spelling: qualified, dot-leading, a bare prelude variant,
// and with an empty argument list. Before, all but the dot-leading form were
// accepted and the IR builder declined the call.
func TestBareVariantCall_IsAnErrorAtTheCallee(t *testing.T) {
	errs := checkWithStdlib(`
enum Shape {
  Circle
  Square Int
  Dot
}

fn dot() -> Shape {
  .Circle(0)
}

fn main() {
  _ = Shape.Circle(0)
  _ = Shape.Dot()
  m: Maybe<Int> = None(1)
  _ = m
  _ = dot()
  _ = Shape.Square(1)
  _ = Shape.Circle
  sq: (Int) -> Shape = Shape.Square
  _ = sq
}
`)
	var got []string
	for _, e := range errs {
		got = append(got, fmt.Sprintf("%d:%d %s", e.Line, e.Col, e.Message))
	}
	want := []string{
		"9:3 variant .Circle carries no data; write .Circle without parentheses",
		"13:7 variant Shape.Circle carries no data; write Shape.Circle without parentheses",
		"14:7 variant Shape.Dot carries no data; write Shape.Dot without parentheses",
		"15:19 variant None carries no data; write None without parentheses",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("errors:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A variant whose payload type does not resolve reports that type once; its
// patterns and constructor calls add no "carries no data" error, since the
// variant does carry data.
func TestUnresolvedVariantPayload_ReportsOnlyTheType(t *testing.T) {
	errs := checkWithStdlib(`
enum Shape {
  Circle Squre
  Dot
}

fn area(s: Shape) -> Int {
  case s {
    .Circle(_) -> 1
    .Dot -> 0
  }
}

fn main() {
  _ = area(Shape.Circle(0))
}
`)
	var got []string
	for _, e := range errs {
		got = append(got, fmt.Sprintf("%d:%d %s", e.Line, e.Col, e.Message))
	}
	want := []string{`3:10 unknown type "Squre"`}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("errors:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
