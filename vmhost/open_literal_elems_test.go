package vmhost_test

import (
	"strings"
	"testing"
)

// A map literal whose keys or values are still open in a type variable on
// one side (`None`, `[]`, `Ok(1)`) takes the other entries' type, annotated
// or not, and runs. The checker rejected each map below ("map key type
// mismatch: expected Maybe<Int>, got Maybe<?1>"), and the builder declined
// `%{1 => [], 2 => [1]}`, whose value kinds join only through the checked
// type.
func TestOpenLiteralElems_MapsRun(t *testing.T) {
	src := `import std/io

fn main() {
    a = %{Some(1) => 1, None => 2}
    b: Map<Maybe<Int>, Int> = %{Some(1) => 1, None => 2}
    c = %{None => 1, Some(2) => 3}
    d = %{1 => None, 2 => Some(1)}
    e = %{1 => [], 2 => [1]}
    f = %{[] => 1, [1] => 2}
    g = %{Ok(1) => 1, Err("e") => 2}
    h = %{1 => %{None => [Some(1)]}, 2 => %{Some(2) => [None]}}
    i: Map<Int, Maybe<Int>> = %{1 => None, 2 => Some(3)}
    l = [None, Some(1)]
    io.print(Debug.inspect(#(a, b, c, d)))
    io.print(Debug.inspect(#(e, f, g)))
    io.print(Debug.inspect(#(h, i, l)))
    io.print("${Map.get(a, None)} ${Map.get(e, 1)}")
}
`
	want := strings.Join([]string{
		"#(%{Some(1) => 1, None => 2}, %{Some(1) => 1, None => 2}, %{None => 1, Some(2) => 3}, %{1 => None, 2 => Some(1)})",
		`#(%{1 => [], 2 => [1]}, %{[] => 1, [1] => 2}, %{Ok(1) => 1, Err("e") => 2})`,
		"#(%{1 => %{None => [Some(1)]}, 2 => %{Some(2) => [None]}}, %{1 => None, 2 => Some(3)}, [None, Some(1)])",
		"Some(2) Some([])",
		"",
	}, "\n")
	if got := runSourceOutput(t, src); got != want {
		t.Errorf("output\n%s\nwant\n%s", got, want)
	}
}

// A set or vector literal whose items differ by an empty collection
// (`%[[], [1]]`, `#[#[], #[1]]`) takes the checked element type, annotated
// or not, and runs; so does one whose items are all empty where the checked
// type gives the element (`%[[]]` inside `%[%[[]], %[[1]]]`). The checker
// accepted each, and the IR builder declined it: the item kinds (an empty
// list beside a List<Int>) have no embeds join. An embedded value beside
// an empty collection still widens into its enum.
func TestOpenLiteralElems_SetsAndVectorsRun(t *testing.T) {
	src := `import std/io

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

fn main() {
    a = %[[], [1]]
    b = #[[1], []]
    c: Set<List<Int>> = %[[], [1]]
    d: Vector<List<Int>> = #[[], [1]]
    e = %[%[], %[1]]
    f = #[#[], #[1]]
    g: Set<Set<Int>> = %[%[], %[1]]
    h = #[%{}, %{1 => 2}]
    i = %[%[[]], %[[1]]]
    j = #[#[[]], #[[1]]]
    k: Set<List<Int>> = %[[]]
    l = %{1 => %[[]], 2 => %[[1]]}
    m = %[None, Some(1)]
    n = #[Ok(1), Err("e")]
    o = #[None, Some([]), Some([1])]
    ci = #Circle{r: 1}
    s: Shape = .Dot
    p = #[[ci], [], [s]]
    q = %[%[ci], %[], %[s]]
    io.print(Debug.inspect(#(a, b, c, d)))
    io.print(Debug.inspect(#(e, f, g, h)))
    io.print(Debug.inspect(#(i, j, k, l)))
    io.print(Debug.inspect(#(m, n, o)))
    io.print(Debug.inspect(#(p, q)))
    io.print("${Set.contains(a, [])} ${Set.contains(i, %[[1]])} ${Vector.get(b, 1)}")
}
`
	want := strings.Join([]string{
		"#(%[[], [1]], #[[1], []], %[[], [1]], #[[], [1]])",
		"#(%[%[], %[1]], #[#[], #[1]], %[%[], %[1]], #[%{}, %{1 => 2}])",
		"#(%[%[[]], %[[1]]], #[#[[]], #[[1]]], %[[]], %{1 => %[[]], 2 => %[[1]]})",
		`#(%[None, Some(1)], #[Ok(1), Err("e")], #[None, Some([]), Some([1])])`,
		"#(#[[#Circle{r: 1}], [], [Dot]], %[%[#Circle{r: 1}], %[], %[Dot]])",
		"True True Some([])",
		"",
	}, "\n")
	if got := runSourceOutput(t, src); got != want {
		t.Errorf("output\n%s\nwant\n%s", got, want)
	}
}

// A literal whose items differ by an empty collection below their top runs:
// one inside a tuple (`#[#(1, []), #(2, [3])]`), a variant payload
// (`[Some([]), Some([1])]`), a record or struct field, or a nested
// collection (`[[], [[]], [[1]]]`), in a list, set, vector or map. The
// checker accepted each, and the IR builder declined it: an item such as
// `#(1, [])` was lowered without the literal's checked element type, so its
// `[]` kept the empty-list kind and the items did not join. Each item is now
// lowered against the checked element type, so the empty collection is
// typed where it sits.
func TestOpenLiteralElems_NestedEmptyCollectionsRun(t *testing.T) {
	src := `import std/io

struct P {
  a: List<Int>
}

struct Box<T> {
  v: T
}

fn main() {
    a = #[#(1, []), #(2, [3])]
    b = [[], [[]], [[1]]]
    c = %{1 => #(None, []), 2 => #(Some(1), [2])}
    d = [Some([]), Some([1])]
    e = [#P{a: []}, #P{a: [1]}]
    f = [#Box{v: []}, #Box{v: [1]}]
    g = [#{a: [], b: 1}, #{a: [2], b: 3}]
    h = %[#(1, []), #(2, [3])]
    i = %[%[[]], %[[1]]]
    j = #[[%[]], [%[1]]]
    k = #[Some(#[]), Some(#[1])]
    l = %{1 => [[]], 2 => [[1]]}
    m = %{#(1, []) => 1, #(2, [3]) => 2}
    n = [#(1, %{}), #(2, %{"a" => 1})]
    o = [#([], 1), #([2], 3)]
    io.print(Debug.inspect(#(a, b, c)))
    io.print(Debug.inspect(#(d, e, f, g)))
    io.print(Debug.inspect(#(h, i, j, k)))
    io.print(Debug.inspect(#(l, m, n, o)))
    io.print("${a == #[#(1, []), #(2, [3])]} ${Set.contains(h, #(1, []))} ${Map.get(m, #(2, [3]))}")
    case b {
        [[], [[]], [[x]]] -> io.print("${x}")
        _ -> io.print("no")
    }
}
`
	want := strings.Join([]string{
		`#(#[#(1, []), #(2, [3])], [[], [[]], [[1]]], %{1 => #(None, []), 2 => #(Some(1), [2])})`,
		`#([Some([]), Some([1])], [#P{a: []}, #P{a: [1]}], [#Box{v: []}, #Box{v: [1]}], [#{a: [], b: 1}, #{a: [2], b: 3}])`,
		`#(%[#(1, []), #(2, [3])], %[%[[]], %[[1]]], #[[%[]], [%[1]]], #[Some(#[]), Some(#[1])])`,
		`#(%{1 => [[]], 2 => [[1]]}, %{#(1, []) => 1, #(2, [3]) => 2}, [#(1, %{}), #(2, %{"a" => 1})], [#([], 1), #([2], 3)])`,
		"True True Some(2)",
		"1",
		"",
	}, "\n")
	if got := runSourceOutput(t, src); got != want {
		t.Errorf("output\n%s\nwant\n%s", got, want)
	}
}
