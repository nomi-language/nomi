package vmhost_test

import (
	"strings"
	"testing"
)

// An unannotated collection literal that mixes an enum with values of a type
// it embeds is a collection of the enum, at the top or nested, and each
// embedded value is widened where it sits. The checker typed the top-level
// joins and the IR builder declined every one but the list ("this map
// literal is not supported yet"); a nested join (`[(1, c), (2, s)]`) the
// checker typed by its first element, `List<(Int, Circle)>`, though the
// second holds a Dot.
func TestEmbedsJoin_CollectionLiteralsWidenTheEmbeddedValues(t *testing.T) {
	got := runSourceOutput(t, `import std/io

struct Circle {
  r: Int
}

enum Shape {
  embeds Circle
  Dot
}

derive Equatable for Circle
derive Hashable for Circle
derive Equatable for Shape
derive Hashable for Shape

fn radius(s: Shape): Int {
  case s {
    .Circle{r: r} -> r
    .Dot -> 0
  }
}

fn main() {
  c = Circle{r: 1}
  s: Shape = .Dot
  m = {1 => c, 2 => s}
  io.inspect(m)
  io.inspect({1 => s, 2 => c})
  io.inspect({c => 1, s => 2})
  io.inspect([c, s])
  io.inspect(#[c, s])
  io.inspect(#{c, s})
  io.inspect([(1, c), (2, s)])
  io.inspect([[c], [s]])
  io.inspect([#{c}, #{s}])
  io.inspect([#[c], #[s]])
  io.inspect([{1 => c}, {2 => s}])
  io.inspect({1 => (1, c), 2 => (2, s)})
  io.inspect(#[(1, c), (2, s)])
  io.inspect(#{(1, c), (2, s)})
  ta: List<(Int, Shape)> = [(1, c), (2, s)]
  io.inspect(ta)
  na: List<List<Shape>> = [[c], [s]]
  io.inspect(na)
  io.inspect(Map.values(m) |> Iter.map(radius) |> Iter.to_list())
}
`)
	want := strings.Join([]string{
		"{1 => Circle{r: 1}, 2 => Dot}",
		"{1 => Dot, 2 => Circle{r: 1}}",
		"{Circle{r: 1} => 1, Dot => 2}",
		"[Circle{r: 1}, Dot]",
		"#[Circle{r: 1}, Dot]",
		"#{Circle{r: 1}, Dot}",
		"[(1, Circle{r: 1}), (2, Dot)]",
		"[[Circle{r: 1}], [Dot]]",
		"[#{Circle{r: 1}}, #{Dot}]",
		"[#[Circle{r: 1}], #[Dot]]",
		"[{1 => Circle{r: 1}}, {2 => Dot}]",
		"{1 => (1, Circle{r: 1}), 2 => (2, Dot)}",
		"#[(1, Circle{r: 1}), (2, Dot)]",
		"#{(1, Circle{r: 1}), (2, Dot)}",
		"[(1, Circle{r: 1}), (2, Dot)]",
		"[[Circle{r: 1}], [Dot]]",
		"[1, 0]",
		"",
	}, "\n")
	if got != want {
		t.Errorf("output\n%s\nwant\n%s", got, want)
	}
}
