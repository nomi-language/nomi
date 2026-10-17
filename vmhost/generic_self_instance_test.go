package vmhost_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// genericSelfShapes are types that hold their own instance: a user generic
// enum whose payload holds `Tree<T>` through a List, a Maybe, a tuple, a Map,
// a Set, a struct-shaped variant's field or an Iter; a user generic struct
// holding `Rose<T>` through a List or a Maybe; a generic struct and enum
// holding each other; two generic enums holding each other; and the same Set
// and Maybe payloads without generics. Deciding whether the enum lowers used
// to decide the instance again without the walk's stack, until the
// compiler's stack overflowed; the Maybe, struct-shaped Maybe and Iter
// payloads declined.
var genericSelfShapes = map[string]string{
	"list": `enum Tree<T> {
  Leaf(T)
  Node(List<Tree<T>>)
}

fn main() {
  io.inspect(Tree.Node([Tree.Leaf(1), Tree.Node([])]))
}
`,
	"maybe": `enum Tree<T> {
  Leaf(T)
  Node(Maybe<Tree<T>>)
}

fn main() {
  io.inspect(Tree.Node(Some(Tree.Leaf(1))))
}
`,
	"tuple": `enum Tree<T> {
  Leaf(T)
  Node(#(Tree<T>, Tree<T>))
}

fn main() {
  io.inspect(Tree.Node(#(Tree.Leaf(1), Tree.Leaf(2))))
}
`,
	"map": `enum Tree<T> {
  Leaf(T)
  Node(Map<String, Tree<T>>)
}

fn main() {
  io.inspect(Tree.Node(%{"a" => Tree.Leaf(1)}))
}
`,
	"set": `enum Tree<T> {
  Leaf(T)
  Node(Set<Tree<T>>)
}

fn main() {
  io.inspect(Tree.Node(#{Tree.Leaf(1)}))
}
`,
	"struct-shaped variant": `enum Tree<T> {
  Leaf(T)
  Node {kids: List<Tree<T>>, label: String}
}

fn main() {
  io.inspect(Tree.Node{kids: [Tree.Leaf(1)], label: "x"})
}
`,
	"struct-shaped maybe": `enum Tree<T> {
  Leaf(T)
  Node {next: Maybe<Tree<T>>}
}

fn main() {
  io.inspect(Tree.Node{next: Some(Tree.Leaf(1))})
}
`,
	"iter": `enum Tree<T> {
  Leaf(T)
  Node(Iter<Tree<T>>)
}

fn count(t: Tree<Int>) -> Int {
  case t {
    Tree.Leaf(_) -> 1
    Tree.Node(xs) -> xs |> Iter.map(fn k -> count(k)) |> Iter.reduce(fn acc = 0, n -> acc + n)
  }
}

fn main() {
  io.print("${count(Tree.Node([Tree.Leaf(1), Tree.Leaf(2)] |> Iter.map(fn x -> x)))}")
}
`,
	"generic struct list": `struct Rose<T> {
  value: T
  kids: List<Rose<T>>
}

fn main() {
  io.inspect(Rose{value: 1, kids: [Rose{value: 2, kids: []}]})
}
`,
	"generic struct maybe": `struct Link<T> {
  value: T
  next: Maybe<Link<T>>
}

fn main() {
  io.inspect(Link{value: 1, next: Some(Link{value: 2, next: None})})
}
`,
	"generic struct and enum": `struct Node<T> {
  value: T
  kids: List<Tree<T>>
}

enum Tree<T> {
  N(Node<T>)
  Leaf
}

fn main() {
  io.inspect(Tree.N(Node{value: 1, kids: [Tree.Leaf]}))
}
`,
	"mutual": `enum Even<T> {
  Zero(T)
  E(List<Odd<T>>)
}

enum Odd<T> {
  O(Even<T>)
}

fn main() {
  io.inspect(Even.E([Odd.O(Even.Zero(1))]))
}
`,
	"non-generic set": `enum Tree {
  Leaf(Int)
  Node(Set<Tree>)
}

fn main() {
  io.inspect(Tree.Node(#{Tree.Leaf(1)}))
}
`,
	"non-generic maybe": `enum Tree {
  Leaf(Int)
  Node(Maybe<Tree>)
}

fn main() {
  io.inspect(Tree.Node(Some(Tree.Leaf(1))))
}
`,
}

// `nomi check` on each shape terminates and finds nothing it cannot run, and
// each runs.
func TestCheck_GenericTypeHoldingItsOwnInstanceLowers(t *testing.T) {
	want := map[string]string{
		"list":                    "Node([Leaf(1), Node([])])\n",
		"maybe":                   "Node(Some(Leaf(1)))\n",
		"tuple":                   "Node(Leaf(1), Leaf(2))\n",
		"map":                     "Node(%{\"a\" => Leaf(1)})\n",
		"set":                     "Node(#{Leaf(1)})\n",
		"struct-shaped variant":   "Node{kids: [Leaf(1)], label: \"x\"}\n",
		"struct-shaped maybe":     "Node{next: Some(Leaf(1))}\n",
		"iter":                    "2\n",
		"generic struct list":     "Rose{value: 1, kids: [Rose{value: 2, kids: []}]}\n",
		"generic struct maybe":    "Link{value: 1, next: Some(Link{value: 2, next: None})}\n",
		"generic struct and enum": "N(Node{value: 1, kids: [Leaf]})\n",
		"mutual":                  "E([O(Zero(1))])\n",
		"non-generic set":         "Node(#{Leaf(1)})\n",
		"non-generic maybe":       "Node(Some(Leaf(1)))\n",
	}
	for name, body := range genericSelfShapes {
		t.Run(name, func(t *testing.T) {
			src := "import std/io\n\n" + body
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

// `Tree<T>` with a `List<Tree<T>>` payload at Int, String and a struct:
// built, matched (nested list patterns included), compared, printed with
// io.inspect and Display, a set element and a map key, built 1000 deep, and
// passed through generic functions. The compiler decides the type once, not
// once per level.
func TestRun_GenericTreeOfItsOwnInstances(t *testing.T) {
	out, err := runVM(t, `import std/io

struct Point {
    x: Int
    y: Int
}

derive Equatable for Point
derive Hashable for Point
derive Display for Point

enum Tree<T> {
    Leaf T
    Node List<Tree<T>>
}

derive Equatable for Tree<T>
derive Hashable for Tree<T>
derive Display for Tree<T>

fn size(t: Tree<Int>) -> Int {
    case t {
        Tree.Leaf(_) -> 1
        Tree.Node(kids) -> kids |> Iter.map(fn k -> size(k)) |> Iter.reduce(fn acc = 0, n -> acc + n)
    }
}

fn first_leaf(t: Tree<Int>) -> String {
    case t {
        Tree.Node([Tree.Leaf(n), .._]) -> "first ${n}"
        Tree.Node([Tree.Node(_), .._]) -> "nested"
        Tree.Node([]) -> "empty"
        Tree.Leaf(n) -> "leaf ${n}"
    }
}

fn depth(t: Tree<String>) -> Int {
    case t {
        Tree.Leaf(_) -> 0
        Tree.Node([]) -> 1
        Tree.Node([k, .._]) -> 1 + depth(k)
    }
}

fn build(n: Int, acc: Tree<String>) -> Tree<String> {
    if n == 0 { acc } else { build(n - 1, nest(acc)) }
}

fn nest<T>(t: Tree<T>) -> Tree<T> {
    Tree.Node([t])
}

fn same<T>(a: Tree<T>, b: Tree<T>) -> Bool where T: Equatable {
    a == b
}

fn kids<T>(t: Tree<T>) -> List<Tree<T>> {
    case t {
        Tree.Leaf(_) -> []
        Tree.Node(ks) -> ks
    }
}

fn main() {
    a = Tree.Node([Tree.Leaf(1), Tree.Node([Tree.Leaf(2), Tree.Leaf(3)])])
    b = Tree.Node([Tree.Leaf(1), Tree.Node([Tree.Leaf(2), Tree.Leaf(3)])])
    s = Tree.Node([Tree.Leaf("x"), Tree.Leaf("y")])
    p = Tree.Node([Tree.Leaf(Point{x: 1, y: 2})])
    io.inspect(a)
    io.inspect(s)
    io.inspect(p)
    io.print(Display.to_string(a))
    io.print("${s} ${p}")
    io.print(first_leaf(a))
    io.print(first_leaf(Tree.Node([Tree.Node([])])))
    io.print(first_leaf(Tree.Node([])))

    io.print(
        "${a == b} ${a == Tree.Node([Tree.Leaf(1)])} ${s == Tree.Node(
            [Tree.Leaf("x"), Tree.Leaf("y")]
        )}"
    )

    io.print("size ${size(a)}")

    io.print("set ${Set.size(#{a, b, Tree.Leaf(1)})} ${Set.size(#{s, s})} ${Set.size(#{
            p,
            Tree.Node([
                Tree.Leaf(Point{
                    x: 1,
                    y: 2,
                }),
            ]),
        })}")

    m = %{a => "a", Tree.Leaf(9) => "nine"}
    io.inspect(Map.get(m, b))
    io.inspect(Map.get(m, Tree.Leaf(8)))
    deep = build(1000, Tree.Leaf("s"))
    io.print("depth ${depth(deep)}")

    io.print(
        "deep equal ${deep == build(1000, Tree.Leaf("s"))} ${deep == build(999, Tree.Leaf("s"))}"
    )

    io.print("deep set ${Set.size(#{deep, build(1000, Tree.Leaf("s")), s})}")

    io.print(
        "generic ${same(p, Tree.Node([Tree.Leaf(Point{x: 1, y: 2})]))} ${same(a, b)} ${same(
            s,
            Tree.Leaf("x"),
        )}"
    )

    io.inspect(kids(p))
    io.inspect(nest(nest(Tree.Leaf(Point{x: 3, y: 4}))))
}
`)
	want := strings.Join([]string{
		"Node([Leaf(1), Node([Leaf(2), Leaf(3)])])",
		"Node([Leaf(\"x\"), Leaf(\"y\")])",
		"Node([Leaf(Point{x: 1, y: 2})])",
		"Node([Leaf(1), Node([Leaf(2), Leaf(3)])])",
		"Node([Leaf(x), Leaf(y)]) Node([Leaf(Point{x: 1, y: 2})])",
		"first 1",
		"nested",
		"empty",
		"True False True",
		"size 3",
		"set 2 1 1",
		"Some(\"a\")",
		"None",
		"depth 1000",
		"deep equal True False",
		"deep set 2",
		"generic True True False",
		"[Leaf(Point{x: 1, y: 2})]",
		"Node([Node([Leaf(Point{x: 3, y: 4})])])",
	}, "\n") + "\n"
	if err != nil || out != want {
		t.Fatalf("output %q, error %v; want %q", out, err, want)
	}
}

// The other forms: Maybe, tuple and Map payloads, a generic struct holding a
// List of itself, and two generic enums holding each other, each built,
// matched, compared, printed, hashed and built 1000 deep.
func TestRun_GenericSelfInstanceForms(t *testing.T) {
	out, err := runVM(t, `import std/io

struct Point {
    x: Int
    y: Int
}

derive Equatable for Point
derive Hashable for Point
derive Display for Point

enum Chain<T> {
    End T
    Next Maybe<Chain<T>>
}

derive Equatable for Chain<T>
derive Hashable for Chain<T>
derive Display for Chain<T>

enum Pair<T> {
    One T
    Both #(Pair<T>, Pair<T>)
}

derive Equatable for Pair<T>
derive Hashable for Pair<T>
derive Display for Pair<T>

enum Dict<T> {
    Val T
    Obj Map<String, Dict<T>>
}

derive Equatable for Dict<T>
derive Hashable for Dict<T>
derive Display for Dict<T>

struct Rose<T> {
    value: T
    kids: List<Rose<T>>
}

derive Equatable for Rose<T>
derive Hashable for Rose<T>
derive Display for Rose<T>

enum Even<T> {
    Zero T
    E List<Odd<T>>
}

enum Odd<T> {
    O Even<T>
}

derive Equatable for Even<T>
derive Hashable for Even<T>
derive Display for Even<T>

derive Equatable for Odd<T>
derive Hashable for Odd<T>
derive Display for Odd<T>

fn chain_len(c: Chain<Int>) -> Int {
    case c {
        Chain.End(_) -> 0
        Chain.Next(None) -> 1
        Chain.Next(Some(rest)) -> 1 + chain_len(rest)
    }
}

fn build_chain(n: Int, acc: Chain<Int>) -> Chain<Int> {
    if n == 0 {
        acc
    } else {
        build_chain(n - 1, Chain.Next(Some(acc)))
    }
}

fn left(p: Pair<String>) -> String {
    case p {
        Pair.One(s) -> s
        Pair.Both(#(Pair.One(s), _)) -> "left ${s}"
        Pair.Both(#(l, _)) -> left(l)
    }
}

fn build_pair(n: Int, acc: Pair<String>) -> Pair<String> {
    if n == 0 {
        acc
    } else {
        build_pair(n - 1, Pair.Both(#(acc, Pair.One("r"))))
    }
}

fn lookup(d: Dict<Point>, k: String) -> Maybe<Dict<Point>> {
    case d {
        Dict.Obj(m) -> Map.get(m, k)
        Dict.Val(_) -> None
    }
}

fn build_dict(n: Int, acc: Dict<Point>) -> Dict<Point> {
    if n == 0 {
        acc
    } else {
        build_dict(n - 1, Dict.Obj(%{"k" => acc}))
    }
}

fn dict_depth(d: Dict<Point>) -> Int {
    case lookup(d, "k") {
        Some(inner) -> 1 + dict_depth(inner)
        None -> 0
    }
}

fn rose_total(r: Rose<Int>) -> Int {
    r.kids |> Iter.map(fn k -> rose_total(k)) |> Iter.reduce(fn acc = r.value, n -> acc + n)
}

fn build_rose(n: Int, acc: Rose<Int>) -> Rose<Int> {
    if n == 0 {
        acc
    } else {
        build_rose(n - 1, Rose{value: 1, kids: [acc]})
    }
}

fn evens(e: Even<String>) -> Int {
    case e {
        Even.Zero(_) -> 0
        Even.E([Odd.O(inner), .._]) -> 2 + evens(inner)
        Even.E([]) -> 1
    }
}

fn build_even(n: Int, acc: Even<String>) -> Even<String> {
    if n == 0 {
        acc
    } else {
        build_even(n - 1, Even.E([Odd.O(acc)]))
    }
}

fn twice<T>(x: T) -> #(T, T) {
    #(x, x)
}

fn main() {
    c = Chain.Next(Some(Chain.Next(Some(Chain.End(1)))))
    io.inspect(c)

    io.print(
        "${c} ${chain_len(c)} ${c == Chain.Next(
            Some(Chain.Next(Some(Chain.End(1))))
        )} ${c == Chain.Next(None)}"
    )

    io.print(
        "set ${Set.size(#{c, c, Chain.Next(None)})} ${Map.get(
            %{c => 1},
            Chain.Next(Some(Chain.Next(Some(Chain.End(1))))),
        )}"
    )

    io.print(
        "deep ${chain_len(build_chain(1000, Chain.End(0)))} ${build_chain(
            1000,
            Chain.End(0),
        ) == build_chain(1000, Chain.End(0))}"
    )

    p = Pair.Both(#(Pair.One("a"), Pair.Both(#(Pair.One("b"), Pair.One("c")))))
    io.inspect(p)
    io.print("${p} ${left(p)} ${p == p} ${Set.size(#{p, Pair.One("a"), p})}")

    io.print(
        "deep ${left(build_pair(1000, Pair.One("bottom")))} ${build_pair(
            1000,
            Pair.One("z"),
        ) == build_pair(1000, Pair.One("z"))}"
    )

    d = Dict.Obj(%{"a" => Dict.Val(Point{x: 1, y: 2}), "b" => Dict.Val(Point{x: 0, y: 0})})
    io.inspect(d)
    io.print("${d}")
    io.inspect(lookup(d, "a"))

    io.print(
        "${d == Dict.Obj(
            %{"b" => Dict.Val(Point{x: 0, y: 0}), "a" => Dict.Val(Point{x: 1, y: 2})}
        )} ${Set.size(#{d, d})} ${Map.get(%{d => "d"}, d)}"
    )

    io.print("deep ${dict_depth(build_dict(1000, Dict.Val(Point{x: 0, y: 0})))}")

    r = Rose{
        value: 1,
        kids: [
            Rose{
                value: 2,
                kids: [],
            },
            Rose{
                value: 3,
                kids: [
                    Rose{
                        value: 4,
                        kids: [],
                    },
                ],
            },
        ],
    }

    io.inspect(r)

    io.print("${r} ${rose_total(r)} ${r == r} ${Set.size(#{
            r,
            r,
            Rose{
                value: 1,
                kids: [],
            },
        })}")

    io.print("deep ${rose_total(build_rose(1000, Rose{value: 0, kids: []}))}")

    e = Even.E([Odd.O(Even.E([Odd.O(Even.Zero("z"))]))])
    io.inspect(e)
    io.print("${e} ${evens(e)} ${e == e} ${Set.size(#{e, Even.Zero("z"), e})}")
    io.print("deep ${evens(build_even(1000, Even.Zero("z")))}")

    io.inspect(twice(c))
    io.inspect(twice(p))
    io.inspect(twice(r))
}
`)
	want := strings.Join([]string{
		"Next(Some(Next(Some(End(1)))))",
		"Next(Some(Next(Some(End(1))))) 2 True False",
		"set 2 Some(1)",
		"deep 1000 True",
		"Both(One(\"a\"), Both(One(\"b\"), One(\"c\")))",
		"Both(One(a), Both(One(b), One(c))) left a True 2",
		"deep left bottom True",
		"Obj(%{\"a\" => Val(Point{x: 1, y: 2}), \"b\" => Val(Point{x: 0, y: 0})})",
		"Obj(%{a => Val(Point{x: 1, y: 2}), b => Val(Point{x: 0, y: 0})})",
		"Some(Val(Point{x: 1, y: 2}))",
		"True 1 Some(d)",
		"deep 1000",
		"Rose{value: 1, kids: [Rose{value: 2, kids: []}, Rose{value: 3, kids: [Rose{value: 4, kids: []}]}]}",
		"Rose{value: 1, kids: [Rose{value: 2, kids: []}, Rose{value: 3, kids: [Rose{value: 4, kids: []}]}]} 10 True 2",
		"deep 1000",
		"E([O(E([O(Zero(\"z\"))]))])",
		"E([O(E([O(Zero(z))]))]) 4 True 2",
		"deep 2000",
		"#(Next(Some(Next(Some(End(1))))), Next(Some(Next(Some(End(1))))))",
		"#(Both(One(\"a\"), Both(One(\"b\"), One(\"c\"))), Both(One(\"a\"), Both(One(\"b\"), One(\"c\"))))",
		"#(Rose{value: 1, kids: [Rose{value: 2, kids: []}, Rose{value: 3, kids: [Rose{value: 4, kids: []}]}]}, Rose{value: 1, kids: [Rose{value: 2, kids: []}, Rose{value: 3, kids: [Rose{value: 4, kids: []}]}]})",
	}, "\n") + "\n"
	if err != nil || out != want {
		t.Fatalf("output %q, error %v; want %q", out, err, want)
	}
}

// A value of a generic enum that holds its own instance inside a List, a Map
// or a Set, or a second enum that holds it back, used where the same enum at
// an embedding type is expected: it is rebuilt at the wider type, each
// element by the enum's own adapter, which the element adapter takes as a
// capture.
func TestRun_WideningAGenericEnumThatHoldsItselfInACollection(t *testing.T) {
	out, err := runVM(t, `import std/io

struct Circle {
    r: Int
}

enum Shape {
    embeds Circle
    Dot
}

enum Tree<T> {
    Leaf T
    Node List<Tree<T>>
}

enum Dict<T> {
    Val T
    Obj Map<String, Dict<T>>
}

enum Bag<T> {
    Item T
    Many Set<Bag<T>>
}

derive Equatable for Circle
derive Hashable for Circle

derive Equatable for Shape
derive Hashable for Shape

derive Equatable for Bag<T>
derive Hashable for Bag<T>

enum Even<T> {
    Zero T
    E List<Odd<T>>
}

enum Odd<T> {
    O Even<T>
}

fn main() {
    t: Tree<Circle> = Tree.Node([
        Tree.Leaf(Circle{
            r: 1,
        }),
        Tree.Node([
            Tree.Leaf(Circle{
                r: 2,
            }),
        ]),
    ])

    wt: Tree<Shape> = t
    io.inspect(wt)

    d: Dict<Circle> = Dict.Obj(%{
        "a" => Dict.Val(Circle{
            r: 1,
        }),
        "b" => Dict.Obj(%{
            "c" => Dict.Val(Circle{
                r: 2,
            }),
        }),
    })

    wd: Dict<Shape> = d
    io.inspect(wd)

    b: Bag<Circle> = Bag.Many(#{
        Bag.Item(Circle{
            r: 1,
        }),
        Bag.Many(#{
            Bag.Item(Circle{
                r: 2,
            }),
        }),
    })

    wb: Bag<Shape> = b

    io.print("${wb == Bag.Many(#{
            Bag.Many(#{
                Bag.Item(Circle{
                    r: 2,
                }),
            }),
            Bag.Item(Circle{
                r: 1,
            }),
        })}")

    e: Even<Circle> = Even.E([
        Odd.O(Even.E([
            Odd.O(Even.Zero(Circle{
                r: 3,
            })),
        ])),
    ])

    we: Even<Shape> = e
    io.inspect(we)
    m: Maybe<Tree<Circle>> = Some(t)
    wm: Maybe<Tree<Shape>> = m
    io.inspect(wm)
}
`)
	want := strings.Join([]string{
		"Node([Leaf(Circle{r: 1}), Node([Leaf(Circle{r: 2})])])",
		"Obj(%{\"a\" => Val(Circle{r: 1}), \"b\" => Obj(%{\"c\" => Val(Circle{r: 2})})})",
		"True",
		"E([O(E([O(Zero(Circle{r: 3}))]))])",
		"Some(Node([Leaf(Circle{r: 1}), Node([Leaf(Circle{r: 2})])]))",
	}, "\n") + "\n"
	if err != nil || out != want {
		t.Fatalf("output %q, error %v; want %q", out, err, want)
	}
}
