package irbuild

import (
	"strings"
	"testing"
)

// A generic function that calls itself lowers: the recursive call is an
// instance calling itself, found by its interned key. The checker records
// the self call's instantiation at the caller's own type parameters, which
// is what the builder reads to find the instance.
func TestGenericRecursion_RunsEveryForm(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"direct, not in tail position", `import std/io
fn walk<T>(xs: List<T>): Int {
  case xs {
    [] -> 0
    [_, ..rest] -> 1 + walk(rest)
  }
}
fn main() {
  io.print(walk([1, 2, 3]))
  io.print(walk(["a", "b"]))
  io.print(walk(1..=10000 |> Iter.to_list()))
}`, "3\n2\n10000\n"},
		{"direct, in tail position", `import std/io
fn count<T>(xs: List<T>, acc: Int): Int {
  case xs {
    [] -> acc
    [_, ..rest] -> count(rest, acc + 1)
  }
}
fn spin<T>(x: T, n: Int): T {
  if n == 0 { x } else { spin(x, n - 1) }
}
fn main() {
  io.print(count([1, 2, 3, 4], 0))
  io.print(count(1..=10000 |> Iter.to_list(), 0))
  io.print(spin("s", 1000000))
}`, "4\n10000\ns\n"},
		{"mutual", `import std/io
fn evens<T>(xs: List<T>): List<T> {
  case xs {
    [] -> []
    [x, ..rest] -> [x, ..odds(rest)]
  }
}
fn odds<T>(xs: List<T>): List<T> {
  case xs {
    [] -> []
    [_, ..rest] -> evens(rest)
  }
}
fn ping<T>(x: T, n: Int): T {
  if n == 0 { x } else { pong(x, n - 1) }
}
fn pong<U>(y: U, n: Int): U {
  if n == 0 { y } else { ping(y, n - 1) }
}
fn main() {
  io.inspect(evens([1, 2, 3, 4, 5]))
  io.inspect(evens(["a", "b", "c"]))
  io.print(evens(1..=10000 |> Iter.to_list()) |> Iter.count())
  io.print(ping("p", 1000000))
}`, "[1, 3, 5]\n[\"a\", \"c\"]\n5000\np\n"},
		{"through a lambda", `import std/io
enum Tree<T> {
  Leaf(T)
  Node(List<Tree<T>>)
}
fn depth<T>(t: Tree<T>): Int {
  case t {
    Tree.Leaf(_) -> 1
    Tree.Node(kids) -> 1 + (kids |> Iter.map(|k| depth(k)) |> Iter.reduce(|best = 0, d| if d > best { d } else { best }))
  }
}
fn main() {
  io.print(depth(Tree.Node([Tree.Leaf(1), Tree.Node([Tree.Leaf(2)])])))
  io.print(depth(Tree.Leaf("x")))
}`, "3\n1\n"},
		{"a generic impl function", `import std/io
struct Stack<T> {
  items: List<T>
}
impl Stack<T> {
  fn size(s: Stack<T>): Int {
    case s.items {
      [] -> 0
      [_, ..rest] -> 1 + Stack.size(Stack{items: rest})
    }
  }
}
fn main() {
  io.print(Stack.size(Stack{items: [1, 2, 3]}))
  io.print(Stack.size(Stack{items: 1..=10000 |> Iter.to_list()}))
}`, "3\n10000\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}

// Polymorphic recursion, a generic function reaching itself at a type that
// grows, needs infinitely many instances. The front end rejects it, so the
// builder never instantiates it.
func TestGenericRecursion_PolymorphicIsRejected(t *testing.T) {
	const src = `import std/io
fn f<T>(x: T, n: Int): Int {
  if n == 0 { 0 } else { f((x, x), n - 1) }
}
fn main() { io.print(f(1, 3)) }`
	_, err := AnalyzeSource("main.nomi", src)
	if err == nil {
		t.Fatal("the front end admits polymorphic recursion; the builder would need infinitely many instances")
	}
	if !strings.Contains(err.Error(), "grows on every recursive call") {
		t.Fatalf("rejected for another reason: %v", err)
	}
}

// A lambda parameter whose type is an instance of a generic type over the
// enclosing function's own type parameter lowers: the checker pins the
// accumulator from its default (`acc = t`, `t: Box<T>`), so the instance
// sees `Box<Int>`.
func TestGenericLambda_ParamAtTheEnclosingTypeParameter(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"a generic enum", `import std/io
enum Box<T> {
  One(T)
  Two((Box<T>, Box<T>))
}
fn leaves<T>(b: Box<T>): Int {
  case b {
    Box.One(_) -> 1
    Box.Two((l, r)) -> leaves(l) + leaves(r)
  }
}
fn wrap<T>(t: Box<T>, n: Int): Box<T> {
  1..=n |> Iter.reduce(|acc = t, _| Box.Two((acc, acc)))
}
fn main() {
  io.print(leaves(wrap(Box.One(1), 3)))
  io.print(leaves(wrap(Box.One("a"), 4)))
}`, "8\n16\n"},
		{"a list and a bare parameter", `import std/io
fn grow<T>(t: List<T>, n: Int): List<T> {
  1..=n |> Iter.reduce(|acc = t, _| List.concat(acc, t))
}
fn keep<T>(t: T, n: Int): T {
  1..=n |> Iter.reduce(|acc = t, _| acc)
}
fn main() {
  io.inspect(grow([1, 2], 2))
  io.inspect(keep("k", 3))
}`, "[1, 2, 1, 2, 1, 2]\n\"k\"\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
