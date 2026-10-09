package vmhost_test

import (
	"strings"
	"testing"
)

// A function over an enum is passed where a function over one of its
// embedded types is expected, since every Circle is a Shape: the callee
// calls it with Circles. The mirror, a function over Circle where one over
// Shape is expected, is a checker error (TestEmbedDowncast_
// AFunctionOverTheEmbeddedTypeIsRefusedAsACallback); the lambda that
// matches first, which that error's hint names, runs.
func TestEmbedsCallback_AFunctionOverTheEnumTakesTheEmbeddedValues(t *testing.T) {
	got := runSourceOutput(t, `import std/io

struct Circle {
  r: Int
}

enum Shape {
  embeds Circle
  Dot
}

fn area(s: Shape): Int {
  case s {
    .Circle{r: r} -> r
    .Dot -> 0
  }
}

fn take(c: Circle): Int {
  c.r
}

fn main() {
  circles = [Circle{r: 2}, Circle{r: 3}]
  io.inspect(Iter.map(circles, area) |> Iter.to_list())
  io.inspect(circles |> Iter.map(area) |> Iter.to_list())
  io.inspect(Maybe.map(Some(Circle{r: 4}), area))
  shapes: List<Shape> = [.Dot, Circle{r: 5}]
  matched = Iter.map(shapes, |value| case value {
    .Circle{} -> take(value)
    .Dot -> 0
  })
  io.inspect(Iter.to_list(matched))
}
`)
	want := strings.Join([]string{
		"[2, 3]",
		"[2, 3]",
		"Some(4)",
		"[0, 5]",
		"",
	}, "\n")
	if got != want {
		t.Errorf("output\n%s\nwant\n%s", got, want)
	}
}
