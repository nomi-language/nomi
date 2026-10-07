package analysis_test

import (
	"testing"
)

// A function type `(P1) -> R1` is assignable to `(P2) -> R2` when each P2 is
// assignable to its P1 and R1 to R2: parameters are contravariant, results
// covariant, and inside a function type "assignable" is exact, a concrete
// type into its interface, or an embedded type into its enum. Every position
// that compares function types holds to it. Before, two function types
// unified symmetrically, so a function taking a Float passed where one taking
// any Display was expected, and the same sound widening was refused at a
// plain argument, a list element or a field.
const funcAssignDecls = `struct Holder {
  f: (Iter<Int>) -> Int
}

struct Box<T> {
  v: T
}

struct Circle {
  r: Int
}

enum Shape {
  embeds Circle
  Dot
}

fn sum_list(xs: List<Int>): Int {
  Iter.count(xs)
}

fn cnt(xs: Iter<Int>): Int {
  Iter.count(xs)
}

fn shown(x: Display): String {
  Display.to_string(x)
}

fn float_str(f: Float): String {
  Float.to_string(f)
}

fn r_of(c: Circle): Int {
  c.r
}

fn pick<T>(c: Bool, a: T, b: T): T {
  if c { a } else { b }
}

fn use_iter(f: (Iter<Int>) -> Int): Int {
  f([1, 2])
}

fn use_display(f: (Display) -> String): String {
  f("hi")
}

fn use_shape(f: (Shape) -> Int): Int {
  f(Shape.Dot)
}

fn give_iter(f: (Iter<Int>) -> Int): Int {
  f([5, 6])
}

fn higher(g: ((List<Int>) -> Int) -> Int): Int {
  g(sum_list)
}

`

const funcAssignReturns = `fn ret_bad(): (Iter<Int>) -> Int {
  sum_list
}

fn ret_bad_early(b: Bool): (Iter<Int>) -> Int {
  if b {
    return sum_list
  }
  cnt
}

`

func TestFuncTypeAssign_UnsoundIsRejected(t *testing.T) {
	for _, tc := range []struct {
		name, decls, body, want string
	}{
		{"an argument whose parameter is narrower", "", "  _ = use_iter(sum_list)\n",
			"argument 1: expected (Iter<Int>) -> Int, got (List<Int>) -> Int"},
		{"an argument taking a concrete type where an interface is passed", "", "  _ = use_display(float_str)\n",
			"argument 1: expected (Display) -> String, got (Float) -> String"},
		{"an argument taking an embedded type where its enum is passed", "", "  _ = use_shape(r_of)\n",
			"argument 1: expected (Shape) -> Int, got (Circle) -> Int"},
		{"a higher-order argument", "", "  _ = higher(give_iter)\n",
			"argument 1: expected ((List<Int>) -> Int) -> Int, got ((Iter<Int>) -> Int) -> Int"},
		{"a named argument", "", "  _ = use_iter(f: sum_list)\n",
			"argument 'f': expected (Iter<Int>) -> Int, got (List<Int>) -> Int"},
		{"an annotated binding", "", "  f: (Iter<Int>) -> Int = sum_list\n  _ = f([1])\n",
			"type mismatch: expected (Iter<Int>) -> Int, got (List<Int>) -> Int"},
		{"a list element", "", "  fs: List<(Iter<Int>) -> Int> = [sum_list]\n  _ = Iter.count(fs)\n",
			"list element type mismatch: expected (Iter<Int>) -> Int, got (List<Int>) -> Int"},
		{"a struct field", "", "  h = Holder{f: sum_list}\n  _ = h.f([1])\n",
			"field 'f' of Holder: expected (Iter<Int>) -> Int, got (List<Int>) -> Int"},
		{"a generic struct's type argument", "", "  b: Box<(Float) -> String> = Box{v: shown}\n  _ = b.v(1.5)\n",
			"type mismatch: expected Box<(Float) -> String>, got Box<(Display) -> String>"},
		{"a result", funcAssignReturns, "  _ = ret_bad()\n",
			"return type mismatch: expected (Iter<Int>) -> Int, got (List<Int>) -> Int"},
		{"an early return", funcAssignReturns, "  _ = ret_bad_early(True)\n",
			"return type mismatch: expected (Iter<Int>) -> Int, got (List<Int>) -> Int"},
		{"a result where the function returns a narrower one", "", "  n: (Int) -> List<Int> = |x: Int| 0..x\n  _ = n(2)\n",
			"type mismatch: expected (Int) -> List<Int>, got (Int) -> Range<Int>"},
		{"a generic join, wider first", "", "  c = pick(True, shown, float_str)\n  _ = c(\"hi\")\n",
			"argument 3: expected (Display) -> String, got (Float) -> String"},
		{"a generic join, narrower first", "", "  c = pick(True, float_str, shown)\n  _ = c(1.5)\n",
			"argument 3: expected (Float) -> String, got (Display) -> String\nhelp: T is already (Float) -> String from an earlier argument, and a type parameter joins two function types only when they are the same; pass a lambda of that type instead"},
		{"an if join", "", "  g = if True { shown } else { float_str }\n  _ = g(1.5)\n",
			"if/else branch type mismatch: then is (Display) -> String, else is (Float) -> String"},
		{"a case join", "", "  k = case 1 {\n    1 -> shown\n    _ -> float_str\n  }\n  _ = k(1.5)\n",
			"case branch type mismatch: expected (Display) -> String, got (Float) -> String"},
		{"an unannotated list", "", "  l = [shown, float_str]\n  _ = Iter.count(l)\n",
			"list element type mismatch: expected (Display) -> String, got (Float) -> String"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := funcAssignDecls + tc.decls + "fn main() {\n" + tc.body + "}\n"
			_, errs := checkSourceWithStdlib(src)
			expectStdlibError(t, errs, tc.want)
		})
	}
}

// The mirror: the same positions accept a function whose parameters are
// wider and whose result is narrower than the position's type. The VM runs
// each of these (irbuild's TestIRFuncWiderThanExpected_EveryPosition).
func TestFuncTypeAssign_SoundIsAccepted(t *testing.T) {
	for _, body := range []string{
		"  _ = use_display(shown)\n",
		"  _ = higher(|g| g([1]))\n",
		"  _ = use_iter(cnt)\n",
		"  f: (List<Int>) -> Int = cnt\n  _ = f([1])\n",
		"  fs: List<(List<Int>) -> Int> = [cnt, sum_list]\n  _ = Iter.count(fs)\n",
		"  m: (Int) -> Iter<Int> = |n: Int| [n]\n  _ = m(1)\n",
		"  s: (Circle) -> Int = |sh: Shape| case sh {\n    .Circle{r} -> r\n    .Dot -> 0\n  }\n  _ = s(Circle{r: 1})\n",
		"  _ = Iter.map([1, 2], shown) |> Iter.to_list()\n",
		"  _ = Map.map_values({\"a\" => [1]}, cnt)\n",
		"  c = pick(True, shown, shown)\n  _ = c(1)\n",
		"  c = pick(True, float_str, |x| shown(x))\n  _ = c(1.5)\n",
	} {
		src := funcAssignDecls + "fn main() {\n" + body + "}\n"
		_, errs := checkSourceWithStdlib(src)
		expectNoStdlibErrors(t, errs)
	}
}
