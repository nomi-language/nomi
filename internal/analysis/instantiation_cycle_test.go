package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// polymorphicRecursions is the polymorphic-recursion errors among errs.
func polymorphicRecursions(errs []analysis.TypeError) []analysis.TypeError {
	var out []analysis.TypeError
	for _, e := range errs {
		if e.Code == analysis.PolymorphicRecursionCode {
			out = append(out, e)
		}
	}
	return out
}

// Polymorphic recursion is a compile error: a generic function that reaches
// itself at a type built from its own parameter needs infinitely many
// instances. Recursion at the same parameters, at a concrete type, or with
// parameters swapped needs finitely many and is accepted.
func TestInstantiationCycle(t *testing.T) {
	const header = "struct Box<T>\n  inner: T\nend\n\n"
	rejected := map[string]string{
		"direct, through a struct": `fn grow<T>(x: T, n: Int) -> Int
  if n == 0 then 0 else grow(Box{inner: x}, n - 1)
end`,
		"direct, through a tuple": `fn pair<T>(x: T, n: Int) -> Int
  if n == 0 then 0 else pair((x, x), n - 1)
end`,
		"direct, through an anonymous struct": `fn nest<T>(x: T, n: Int) -> Int
  if n == 0 then 0 else nest({v: x}, n - 1)
end`,
		"an anonymous struct parameter": `fn deeper<T>(r: {v: T}, n: Int) -> Int
  if n == 0 then 0 else deeper({v: {v: r.v}}, n - 1)
end`,
		"a vector parameter": `fn stack<T>(r: Vector<T>, n: Int) -> Int
  if n == 0 then 0 else stack(Vector[r], n - 1)
end`,
		"mutual, through a list": `fn ping<T>(x: T, n: Int) -> Int
  if n == 0 then 0 else pong([x], n - 1)
end

fn pong<U>(y: U, n: Int) -> Int
  ping(y, n)
end`,
	}
	for name, src := range rejected {
		errs := polymorphicRecursions(checkUniversalDebugProject(t, header+src+"\n\nfn main()\n  _ = 1\nend\n"))
		if len(errs) != 1 || !strings.Contains(errs[0].Message, "grows on every recursive call") {
			t.Errorf("%s: want one polymorphic-recursion error, got %v", name, errs)
		}
	}
	accepted := map[string]string{
		"same parameter": `fn count<T>(x: T, n: Int) -> Int
  if n == 0 then 0 else count(x, n - 1)
end`,
		"concrete argument": `fn once<T>(x: T, n: Int) -> Int
  if n == 0 then 0 else once(1, n - 1)
end`,
		"swapped parameters": `fn swap<T, U>(a: T, b: U, n: Int) -> Int
  if n == 0 then 0 else swap(b, a, n - 1)
end`,
		"growing but not recursive": `fn wrap<T>(x: T) -> Box<T>
  Box{inner: x}
end

fn twice<T>(x: T) -> Box<Box<T>>
  wrap(wrap(x))
end`,
		"growing through an anonymous struct but not recursive": `fn field<T>(x: T) -> {v: T}
  {v: x}
end

fn fields<T>(x: T) -> {v: {v: T}}
  field(field(x))
end`,
	}
	for name, src := range accepted {
		if errs := polymorphicRecursions(checkUniversalDebugProject(t, header+src+"\n\nfn main()\n  _ = 1\nend\n")); len(errs) > 0 {
			t.Errorf("%s: rejected: %v", name, errs)
		}
	}
}

// A generic type that holds itself, through its parts, at a type argument
// that strictly contains one of its own parameters needs an instance per
// size of that argument, without end, and is an error at the part's type.
// The error names the type, the argument and, through other declarations,
// the path back.
func TestInstantiationCycle_ATypeThatHoldsItselfAtALargerArgument(t *testing.T) {
	type want struct {
		src string
		// line is where the error stands; says are pieces of its message.
		line int
		says []string
	}
	rejected := map[string]want{
		"a struct, through Option and a list": {`struct Nested<T>
  value: T
  inner: Option<Nested<List<T>>>
end`, 3, []string{"`Nested` holds itself at `T = List<T>` in `inner`"}},
		"a struct, through a tuple": {`struct Doubled<T>
  inner: Option<Doubled<(T, T)>>
end`, 2, []string{"`Doubled` holds itself at `T = (T, T)`"}},
		"a struct, through Option as the argument": {`struct Deeper<T>
  inner: List<Deeper<Option<T>>>
end`, 2, []string{"`T = Option<T>`"}},
		"an enum's payload": {`enum Perfect<T>
  Leaf(T)
  Node(Perfect<(T, T)>)
end`, 3, []string{"`Perfect` holds itself at `T = (T, T)` in `Node`"}},
		"an enum's record variant": {`enum Grow<T>
  Stop
  More {next: Grow<List<T>>}
end`, 3, []string{"in `More.next`"}},
		"a function-typed field": {`struct Lazy<T>
  next: Option<(T) -> Lazy<List<T>>>
end`, 2, []string{"`Lazy` holds itself at `T = List<T>`"}},
		"two parameters, one swapped and one grown": {`struct Pair<A, B>
  first: A
  rest: Option<Pair<B, List<A>>>
end`, 3, []string{"`Pair` holds itself at `B = List<A>`"}},
		"through a typealias": {`typealias Inner<T> Option<Node<List<T>>>

struct Node<T>
  value: T
  inner: Inner<T>
end`, 5, []string{"`Node` holds itself at `T = List<T>`"}},
		"mutual, two structs": {`struct Left<T>
  right: Option<Right<T>>
end

struct Right<U>
  left: Option<Left<List<U>>>
end`, 6, []string{"`Right` holds `Left` at `T = List<U>` in `left`", "`Left` holds `Right` again (Right → Left → Right)"}},
		"mutual, through a distinct type": {`type Wrap<T> Option<Holder<T>>

struct Holder<T>
  wrap: Option<Wrap<List<T>>>
end`, 4, []string{"`Holder` holds `Wrap` at `T = List<T>`", "(Holder → Wrap → Holder)"}},
		"mutual, through a subtype": {`struct Holder<T>
  some: Option<Filled<T>>
end

subtype Filled<T> List<Holder<List<T>>> when fn xs -> List.length(xs) > 0`, 5, []string{"`Filled` holds `Holder` at `T = List<T>` in `inner`", "(Filled → Holder → Filled)"}},
	}
	for name, w := range rejected {
		errs := polymorphicRecursions(checkUniversalDebugProject(t, w.src+"\n\nfn main()\n  _ = 1\nend\n"))
		if len(errs) != 1 {
			t.Errorf("%s: want one polymorphic-recursion error, got %v", name, errs)
			continue
		}
		if errs[0].Line != w.line {
			t.Errorf("%s: the error is at line %d, want %d: %v", name, errs[0].Line, w.line, errs[0])
		}
		for _, s := range w.says {
			if !strings.Contains(errs[0].Message, s) {
				t.Errorf("%s: the error does not say %q: %s", name, s, errs[0].Message)
			}
		}
	}
}

// A type declared in a body is held to the rule as a top-level one is.
func TestInstantiationCycle_ATypeDeclaredInABody(t *testing.T) {
	errs := polymorphicRecursions(checkUniversalDebugProject(t, `fn main()
  struct Nested<T>
    value: T
    inner: Option<Nested<List<T>>>
  end
  _ = 1
end
`))
	if len(errs) != 1 || errs[0].Line != 4 {
		t.Fatalf("want one polymorphic-recursion error at line 4, got %v", errs)
	}
}

// A cycle through a type another file declares is seen whole, and each
// file reports the growing part it declares.
func TestInstantiationCycle_ThroughAnotherFile(t *testing.T) {
	out := checkOnceProject(t, map[string]string{
		"main.nomi": `import left.{Left}
import right.{Right}

fn main()
  _l: Option<Left<Int>> = None
  _r: Option<Right<Int>> = None
end
`,
		"right.nomi": `import left.{Left}

pub struct Right<U>
  left: Option<Left<List<U>>>
end
`,
		"left.nomi": `import right.{Right}

pub struct Left<T>
  right: Option<Right<T>>
end
`,
	}, []string{"left", "right"})
	if errs := polymorphicRecursions(out["right.nomi"]); len(errs) != 1 || errs[0].Line != 4 ||
		!strings.Contains(errs[0].Message, "(Right → Left → Right)") {
		t.Errorf("right.nomi: want one polymorphic-recursion error at line 4 naming the path, got %v", out["right.nomi"])
	}
	if errs := polymorphicRecursions(out["left.nomi"]); len(errs) != 0 {
		t.Errorf("left.nomi holds Right at its own parameter, and is reported: %v", errs)
	}
}

// Holding itself at its own parameters, at arguments that do not grow, at
// a concrete type, or a larger instance of a type that does not come back
// takes finitely many instances, and is accepted.
func TestInstantiationCycle_ATypeThatHoldsItselfAtArgumentsThatDoNotGrow(t *testing.T) {
	accepted := map[string]string{
		"a tree": `struct Tree<T>
  value: T
  children: List<Tree<T>>
end`,
		"a linked list": `enum Chain<T>
  End
  Link(T, Chain<T>)
end`,
		"swapped parameters": `struct Pair<A, B>
  first: A
  rest: Option<Pair<B, A>>
end`,
		"a concrete argument": `struct Tagged<T>
  value: T
  tag: Option<Tagged<Int>>
end`,
		"a larger instance of a type that does not come back": `struct Box<T>
  inner: T
end

struct Holder<T>
  boxed: Option<Box<List<T>>>
  next: Option<Holder<T>>
end`,
		"mutual at the same parameters": `struct Left<T>
  right: Option<Right<T>>
end

struct Right<U>
  left: Option<Left<U>>
end`,
	}
	for name, src := range accepted {
		if errs := polymorphicRecursions(checkUniversalDebugProject(t, src+"\n\nfn main()\n  _ = 1\nend\n")); len(errs) > 0 {
			t.Errorf("%s: rejected: %v", name, errs)
		}
	}
}
