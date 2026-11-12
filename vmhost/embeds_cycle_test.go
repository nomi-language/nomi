package vmhost_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// embedCycleShapes are enums that embed a struct, or a distinct over one,
// whose fields reach the enum back: directly, through a List, a Map value, a
// tuple, a second struct, a distinct, and two enums embedding each other's
// structs. The last is the same tuple shape without `embeds`. Deciding
// whether the enum lowers used to decide the struct, then the enum again,
// until the compiler's stack overflowed; then it declined.
var embedCycleShapes = map[string]string{
	"direct": `struct Circle
  r: Int
  inner: Maybe<Shape>
end

enum Shape
  embeds Circle
  Dot
end

fn main()
  s: Shape = Circle{r: 2, inner: Some(Circle{r: 1, inner: Some(Shape.Dot)})}
  io.inspect(s)
end
`,
	"list": `struct Circle
  r: Int
  kids: List<Shape>
end

enum Shape
  embeds Circle
  Dot
end

fn main()
  s: Shape = Circle{r: 2, kids: [Shape.Dot, Circle{r: 1, kids: []}]}
  io.inspect(s)
end
`,
	"map value": `struct Circle
  r: Int
  kids: Map<String, Shape>
end

enum Shape
  embeds Circle
  Dot
end

fn main()
  s: Shape = Circle{r: 2, kids: {"a" => Shape.Dot}}
  io.inspect(s)
end
`,
	"tuple": `struct Circle
  r: Int
  pair: (Int, Maybe<Shape>)
end

enum Shape
  embeds Circle
  Dot
end

fn main()
  s: Shape = Circle{r: 2, pair: (1, Some(Shape.Dot))}
  io.inspect(s)
end
`,
	"two steps": `struct Circle
  r: Int
  b: Box
end

struct Box
  inner: Maybe<Shape>
end

enum Shape
  embeds Circle
  Dot
end

fn main()
  s: Shape = Circle{r: 2, b: Box{inner: Some(Shape.Dot)}}
  io.inspect(s)
end
`,
	"distinct": `struct Circle
  r: Int
  inner: Maybe<Shape>
end

type Wrapped Circle

enum Shape
  embeds Wrapped
  Dot
end

fn main()
  s: Shape = Wrapped(Circle{r: 2, inner: Some(Shape.Dot)})
  io.inspect(s)
end
`,
	"mutual": `struct Circle
  r: Int
  other: Maybe<Fruit>
end

struct Apple
  w: Int
  other: Maybe<Shape>
end

enum Shape
  embeds Circle
  Dot
end

enum Fruit
  embeds Apple
  Pip
end

fn main()
  s: Shape = Circle{r: 2, other: Some(Apple{w: 3, other: Some(Shape.Dot)})}
  io.inspect(s)
end
`,
	"tuple without embeds": `struct Node
  r: Int
  pair: (Int, Maybe<Tree>)
end

enum Tree
  N(Node)
  Leaf
end

fn main()
  io.inspect(Tree.N(Node{r: 2, pair: (1, Some(Tree.Leaf))}))
end
`,
}

// `nomi check` on each cyclic shape terminates and finds nothing it cannot
// run, and each runs.
func TestCheck_EmbeddedStructReachingItsEnumBackLowers(t *testing.T) {
	want := map[string]string{
		"direct":               "Circle{r: 2, inner: Some(Circle{r: 1, inner: Some(Dot)})}\n",
		"list":                 "Circle{r: 2, kids: [Dot, Circle{r: 1, kids: []}]}\n",
		"map value":            "Circle{r: 2, kids: {\"a\" => Dot}}\n",
		"tuple":                "Circle{r: 2, pair: (1, Some(Dot))}\n",
		"two steps":            "Circle{r: 2, b: Box{inner: Some(Dot)}}\n",
		"distinct":             "Wrapped(Circle{r: 2, inner: Some(Dot)})\n",
		"mutual":               "Circle{r: 2, other: Some(Apple{w: 3, other: Some(Dot)})}\n",
		"tuple without embeds": "N(Node{r: 2, pair: (1, Some(Leaf))})\n",
	}
	for name, body := range embedCycleShapes {
		t.Run(name, func(t *testing.T) {
			src := "import std::io\n\n" + body
			if err := vmhost.Check(writeProgram(t, src)); err != nil {
				t.Fatalf("check: %v", err)
			}
			out, err := runVM(t, src)
			if err != nil || out != want[name] {
				t.Fatalf("output %q, error %v; want %q", out, err, want[name])
			}
		})
	}
}

// An enum embedding a struct whose fields reach the enum back is built,
// matched (nested payload patterns included), compared, printed, hashed as a
// set element and a map key, and built 1000 deep, which the compiler decides
// once per type and not once per level.
func TestRun_EmbeddedStructReachingItsEnumBack(t *testing.T) {
	out, err := runVM(t, `import std::io

struct Circle
  r: Int
  inner: Maybe<Shape>
end

derive Equatable for Circle
derive Hashable for Circle
derive Display for Circle

enum Shape
  embeds Circle
  Dot
end

derive Equatable for Shape
derive Hashable for Shape
derive Display for Shape

fn depth(s: Shape) -> Int
  case s
    Shape.Circle({r: _, inner: Some(next)}) -> 1 + depth(next)
    Shape.Circle({r: _, inner: None}) -> 1
    Shape.Dot -> 0
  end
end

fn nested_two(s: Shape) -> String
  case s
    Shape.Circle({r:, inner: Some(Shape.Circle({r: r2, inner: _}))}) -> "two ${r} ${r2}"
    Shape.Circle({r:, inner: Some(Shape.Dot)}) -> "dot under ${r}"
    Shape.Circle(c) -> "one ${c.r}"
    Shape.Dot -> "dot"
  end
end

fn build(n: Int, acc: Shape) -> Shape
  if n == 0 then
    acc
  else
    build(n - 1, Circle{r: n, inner: Some(acc)})
  end
end

fn main()
  a: Shape = Circle{r: 1, inner: Some(Circle{r: 2, inner: Some(Shape.Dot)})}
  b: Shape = Circle{r: 1, inner: Some(Circle{r: 2, inner: Some(Shape.Dot)})}
  c: Shape = Circle{r: 1, inner: None}
  io.inspect(a)
  io.print(Display.to_string(c))
  io.print(nested_two(a))
  io.print(nested_two(Circle{r: 5, inner: Some(Shape.Dot)}))
  io.print(nested_two(c))
  io.print(nested_two(Shape.Dot))
  io.print("${a == b} ${a == c}")
  io.print("set ${Set.size(Set[a, b, c, Shape.Dot])}")
  m = {a => "a", c => "c"}
  io.inspect(Map.get(m, b))
  deep = build(1000, Shape.Dot)
  io.print("depth ${depth(deep)}")
  io.print("deep equal ${deep == build(1000, Shape.Dot)}")
  io.print("deep set ${Set.size(Set[deep, build(1000, Shape.Dot), a])}")
end
`)
	want := strings.Join([]string{
		"Circle{r: 1, inner: Some(Circle{r: 2, inner: Some(Dot)})}",
		"Circle{r: 1, inner: None}",
		"two 1 2",
		"dot under 5",
		"one 1",
		"dot",
		"True False",
		"set 3",
		`Some("a")`,
		"depth 1000",
		"deep equal True",
		"deep set 2",
	}, "\n") + "\n"
	if err != nil || out != want {
		t.Fatalf("output %q, error %v; want %q", out, err, want)
	}
}

// The distinct form (`type Wrapped Circle`, Circle holding a List<Shape>)
// and two enums embedding each other's structs: a payload pattern through the
// distinct, nested patterns across both enums, equality and hashing.
func TestRun_EmbeddedDistinctAndMutualEmbedsReachingBack(t *testing.T) {
	out, err := runVM(t, `import std::io

struct Circle
  r: Int
  kids: List<Shape>
end

type Wrapped Circle

enum Shape
  embeds Wrapped
  Dot
end

fn total(s: Shape) -> Int
  case s
    Shape.Wrapped({r:, kids:}) -> kids |> Iter.map(fn k -> total(k)) |> Iter.reduce(r, fn acc, n -> acc + n)
    Shape.Dot -> 0
  end
end

struct Ring
  r: Int
  other: Maybe<Fruit>
end

struct Apple
  w: Int
  other: Maybe<Loop>
end

enum Loop
  embeds Ring
  Stop
end

enum Fruit
  embeds Apple
  Pip
end

fn weigh(s: Loop) -> Int
  case s
    Loop.Ring({r:, other: Some(Fruit.Apple({w:, other: Some(inner)}))}) -> r + w + weigh(inner)
    Loop.Ring({r:, other: Some(Fruit.Apple({w:, other: None}))}) -> r + w
    Loop.Ring({r:, other: _}) -> r
    Loop.Stop -> 0
  end
end

fn main()
  s: Shape = Wrapped(Circle{r: 2, kids: [Shape.Dot, Wrapped(Circle{r: 5, kids: []})]})
  io.inspect(s)
  io.print("total ${total(s)}")
  io.print("${s == Wrapped(Circle{r: 2, kids: [Shape.Dot, Wrapped(Circle{r: 5, kids: []})]})}")
  io.print("set ${Set.size(Set[s, Shape.Dot, s])}")
  l: Loop = Ring{r: 2, other: Some(Apple{w: 3, other: Some(Ring{r: 4, other: Some(Fruit.Pip)})})}
  io.inspect(l)
  io.print("weight ${weigh(l)}")
  io.print("set ${Set.size(Set[l, l, Loop.Stop])}")
end
`)
	want := strings.Join([]string{
		"Wrapped(Circle{r: 2, kids: [Dot, Wrapped(Circle{r: 5, kids: []})]})",
		"total 7",
		"True",
		"set 2",
		"Ring{r: 2, other: Some(Apple{w: 3, other: Some(Ring{r: 4, other: Some(Pip)})})}",
		"weight 9",
		"set 2",
	}, "\n") + "\n"
	if err != nil || out != want {
		t.Fatalf("output %q, error %v; want %q", out, err, want)
	}
}

// A recursive enum without `embeds` is a set element and a map key: the
// structural hash and equality assume the type they are already expanding.
func TestRun_RecursiveEnumIsAKey(t *testing.T) {
	out, err := runVM(t, `import std::io

enum Tree
  N(List<Tree>)
  Leaf
end

fn main()
  t = Set[Tree.N([Tree.Leaf]), Tree.Leaf, Tree.N([Tree.Leaf])]
  io.print("${Set.size(t)}")
  m = {Tree.N([Tree.Leaf, Tree.Leaf]) => 2}
  io.inspect(Map.get(m, Tree.N([Tree.Leaf, Tree.Leaf])))
  io.inspect(Map.get(m, Tree.N([Tree.Leaf])))
end
`)
	want := "2\nSome(2)\nNone\n"
	if err != nil || out != want {
		t.Fatalf("output %q, error %v; want %q", out, err, want)
	}
}
