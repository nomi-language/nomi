package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// An embedded type widens into its enum: a Circle is a Shape wherever a
// Shape is expected. The enum never narrows into an embedded type at a
// position: a Shape may hold another variant, so the only way to get a
// Circle from a Shape is a match, including the narrowing of a matched name.
// Every position below once accepted the Shape, and the IR builder then
// declined the program, so the front end accepted a program that could not
// run.
const embedDowncastDecls = `struct Circle {
  radius: Float
}

type Id Int

enum Shape {
  embeds Circle
  Point
}

enum Who {
  embeds Id
  Nobody
}

struct Holder {
  c: Circle
}

interface Area {
  fn area(a: self): Float
}

impl Area for Circle {
  fn area(c: Circle): Float {
    c.radius
  }
}

fn take(c: Circle): Float {
  c.radius
}

fn take_named(c: Circle, n: Int): Float {
  c.radius + Int.to_float(n)
}

fn take_id(i: Id): Id {
  i
}

fn takes(cs: List<Circle>): Int {
  Iter.count(cs)
}

fn wants(s: Shape): Bool {
  s == Shape.Point
}

fn two<T>(a: T, b: T): T {
  if a == b { a } else { b }
}

`

const downcastHint = "\nhelp: Shape may hold a variant other than Circle, and only a match narrows it: `case value { .Circle{} -> ... }`"

// expectExactDiag fails unless one error reads exactly want, message and
// hints.
func expectExactDiag(t *testing.T, errs []analysis.TypeError, want string) {
	t.Helper()
	msgs := make([]string, len(errs))
	for i, e := range errs {
		if diagText(e) == want {
			return
		}
		msgs[i] = diagText(e)
	}
	t.Fatalf("expected the error\n  %s\ngot %d errors:\n  %s", want, len(errs), strings.Join(msgs, "\n  "))
}

func TestEmbedDowncast_EveryPositionRefusesTheEnum(t *testing.T) {
	for _, tc := range []struct {
		name, decls, body, want string
	}{
		{"an argument", "", "  _ = take(s)\n",
			"argument 1: expected Circle, got Shape" + downcastHint},
		{"a named argument", "", "  _ = take_named(n: 1, c: s)\n",
			"argument 'c': expected Circle, got Shape" + downcastHint},
		{"a pipe argument", "", "  _ = s |> take()\n",
			"pipe argument type mismatch: expected Circle, got Shape" + downcastHint},
		{"an interface function's self", "", "  _ = Circle.area(s)\n",
			"argument 1: expected Circle, got Shape" + downcastHint},
		{"a lambda's parameter", "", "  f = |c: Circle| c.radius\n  _ = f(s)\n",
			"argument 1: expected Circle, got Shape" + downcastHint},
		{"an annotated binding", "", "  c: Circle = s\n  _ = c\n",
			"type mismatch: expected Circle, got Shape" + downcastHint},
		{"a struct field", "", "  h = Holder{c: s}\n  _ = h\n",
			"field 'c' of Holder: expected Circle, got Shape" + downcastHint},
		{"a list element", "", "  cs: List<Circle> = [s]\n  _ = cs\n",
			"list element type mismatch: expected Circle, got Shape" + downcastHint},
		{"a map value", "", "  m: Map<Int, Circle> = {1 => s}\n  _ = m\n",
			"map value type mismatch: expected Circle, got Shape" + downcastHint},
		{"a set element", "", "  xs: Set<Circle> = #{s}\n  _ = xs\n",
			"set element type mismatch: expected Circle, got Shape" + downcastHint},
		{"a tuple element", "", "  p: (Circle, Int) = (s, 1)\n  _ = p\n",
			"type mismatch: expected (Circle, Int), got (Shape, Int)" + downcastHint},
		{"a record field", "", "  r: {c: Circle} = {c: s}\n  _ = r\n",
			"type mismatch: expected {c: Circle}, got {c: Shape}" + downcastHint},
		{"a type argument", "", "  m: Maybe<Circle> = Some(s)\n  _ = m\n",
			"type mismatch: expected Maybe<Circle>, got Maybe<Shape>" + downcastHint},
		{"a list argument", "", "  ss: List<Shape> = [s]\n  _ = takes(ss)\n",
			"argument 1: expected List<Circle>, got List<Shape>" + downcastHint},
		{"a function's result", "", "  f: () -> Circle = || s\n  _ = f\n",
			"type mismatch: expected () -> Circle, got () -> Shape" + downcastHint},
		{"a generic call's result", "", "  x: Circle = two(Circle{radius: 1.0}, s)\n  _ = x\n",
			"type mismatch: expected Circle, got Shape" + downcastHint},
		{"a generic call's result as an argument", "", "  _ = take(two(Circle{radius: 1.0}, s))\n",
			"argument 1: expected Circle, got Shape" + downcastHint},
		{"an if join", "", "  x = if wants(s) { Circle{radius: 1.0} } else { s }\n  _ = take(x)\n",
			"argument 1: expected Circle, got Shape" + downcastHint},
		{"a case join", "", "  x = case s {\n    .Point -> Circle{radius: 1.0}\n    _ -> s\n  }\n  _ = take(x)\n",
			"argument 1: expected Circle, got Shape" + downcastHint},
		{"a nested list join", "", "  xs = [(1, Circle{radius: 1.0}), (2, s)]\n  ys: List<(Int, Circle)> = xs\n  _ = ys\n",
			"type mismatch: expected List<(Int, Circle)>, got List<(Int, Shape)>" + downcastHint},
		{"a list of lists join", "", "  xs = [[Circle{radius: 1.0}], [s]]\n  ys: List<List<Circle>> = xs\n  _ = ys\n",
			"type mismatch: expected List<List<Circle>>, got List<List<Shape>>" + downcastHint},
		{"a map key join", "", "  m = {Circle{radius: 1.0} => 1, s => 2}\n  n: Map<Circle, Int> = m\n  _ = n\n",
			"type mismatch: expected Map<Circle, Int>, got Map<Shape, Int>" + downcastHint},
		{"a nested if join", "", "  x = if wants(s) { (1, Circle{radius: 1.0}) } else { (2, s) }\n  _ = take(x.1)\n",
			"argument 1: expected Circle, got Shape" + downcastHint},
		{"elements no side of which takes the other", "", "  _ = [(Circle{radius: 1.0}, s), (s, Circle{radius: 1.0})]\n",
			"list element type mismatch: expected (Circle, Shape), got (Shape, Circle)" + downcastHint},
		{"a binding's fallback", "", "  m: Maybe<Circle> = None\n  Some(c) = m else { s }\n  _ = c\n",
			"the fallback stands in for Some's payload of type Circle, got Shape" + downcastHint},
		{"a Struct.update patch", "", "  h = Holder{c: Circle{radius: 1.0}}\n  _ = Struct.update(h, {c: s})\n",
			"argument 2: expected Partial<Holder>, got {c: Shape}"},
		{"an embedded distinct", "", "  w = Who.Nobody\n  _ = take_id(w)\n",
			"argument 1: expected Id, got Who\nhelp: Who may hold a variant other than Id, and only a match narrows it: `case value { .Id(_) -> ... }`"},
		{"a parameter default", "fn d(c: Circle = Shape.Point): Float {\n  c.radius\n}\n\n", "  _ = d()\n",
			"default value for parameter 'c' is Shape, expected Circle"},
		{"a field default", "struct H {\n  c: Circle = Shape.Point\n}\n\n", "  _ = H{}\n",
			"field 'c' of H: expected Circle, got Shape" + downcastHint},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := embedDowncastDecls + tc.decls + "fn main() {\n  s = Shape.Point\n" + tc.body + "}\n"
			_, errs := checkSourceWithStdlib(src)
			expectExactDiag(t, errs, tc.want)
		})
	}
}

// A function's result and a try's error flow into a declared type, and an
// impl function's signature into its interface's.
func TestEmbedDowncast_DeclarationsRefuseTheEnum(t *testing.T) {
	for _, tc := range []struct {
		name, src, want string
	}{
		{"a function's tail", "fn get(s: Shape): Circle {\n  s\n}\n",
			"return type mismatch: expected Circle, got Shape" + downcastHint},
		{"a return", "fn get(s: Shape): Circle {\n  if wants(s) {\n    return s\n  }\n  Circle{radius: 1.0}\n}\n",
			"return type mismatch: expected Circle, got Shape" + downcastHint},
		{"a result's payload", "fn get(s: Shape): Result<Circle, String> {\n  Ok(s)\n}\n",
			"return type mismatch: expected Result<Circle, String>, got Result<Shape, ?1>" + downcastHint},
		{"a try's error", "fn inner(): Result<Int, Shape> {\n  Err(Shape.Point)\n}\n\nfn outer(): Result<Int, Circle> {\n  n = try inner()\n  Ok(n)\n}\n",
			"try error type mismatch: expected Circle, got Shape (the enclosing function returns Result<Int, Circle>); convert the error at the `try`, for example with Result.map_err" + downcastHint},
		{"an impl parameter", "interface Edge {\n  fn edge(a: self, other: Shape): Float\n}\n\nimpl Edge for Circle {\n  fn edge(c: Circle, other: Circle): Float {\n    c.radius + other.radius\n  }\n}\n",
			"impl function 'edge': parameter 2 has type Circle, but interface 'Edge' declares Shape, and Shape may hold a variant other than Circle\nhelp: interface 'Edge' requires `fn edge(a: Circle, other: Shape): Float`"},
		{"an impl result", "interface Make {\n  fn make(a: self): Circle\n}\n\nimpl Make for Circle {\n  fn make(c: Circle): Shape {\n    c\n  }\n}\n",
			"impl function 'make': return type Shape does not match interface 'Make' return type Circle, and Shape may hold a variant other than Circle\nhelp: interface 'Make' requires `fn make(a: Circle): Circle`"},
		{"an impl's self", "interface Size {\n  fn size(a: self): Float\n}\n\nimpl Size for Circle {\n  fn size(c: Shape): Float {\n    if wants(c) { 1.0 } else { 2.0 }\n  }\n}\n",
			"impl function 'size': parameter 1 has type Shape, but it stands for `self`, which this block implements for Circle\nhelp: interface 'Size' requires `fn size(a: Circle): Float`"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(embedDowncastDecls + tc.src)
			expectExactDiag(t, errs, tc.want)
		})
	}
}

// The mirror: a Circle still enters every Shape position, a join of a Circle
// and a Shape is a Shape, `==` compares a Circle with a Shape as two Shapes,
// and a name matched against `.Circle{}` is a Circle in its arm.
func TestEmbedDowncast_WideningAndNarrowingAreAccepted(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"an argument", "  _ = wants(c)\n"},
		{"an annotated binding", "  x: Shape = c\n  _ = x\n"},
		{"a list element", "  xs: List<Shape> = [c, Shape.Point]\n  _ = xs\n"},
		{"a list argument", "  _ = Iter.count([c, s])\n"},
		{"a type argument", "  m: Maybe<Shape> = Some(c)\n  _ = m\n"},
		{"an if join", "  x = if wants(s) { c } else { s }\n  _ = wants(x)\n"},
		{"a case join", "  x = case s {\n    .Point -> c\n    _ -> s\n  }\n  _ = wants(x)\n"},
		{"a map join", "  m = {1 => c, 2 => s}\n  ss: Map<Int, Shape> = m\n  _ = ss\n"},
		{"a nested list join", "  xs = [(1, c), (2, s)]\n  ys: List<(Int, Shape)> = xs\n  _ = ys\n"},
		{"a nested vector join", "  xs = #[[c], [s]]\n  ys: Vector<List<Shape>> = xs\n  _ = ys\n"},
		{"a nested map join", "  m = {1 => (1, s), 2 => (2, c)}\n  n: Map<Int, (Int, Shape)> = m\n  _ = n\n"},
		{"a generic call", "  _ = wants(two(c, s))\n  _ = wants(two(s, c))\n"},
		{"equality", "  _ = c == s\n  _ = s == c\n"},
		{"a narrowed arm", "  _ = case s {\n    .Circle{radius: _} -> take(s)\n    .Point -> 0.0\n  }\n"},
		{"a narrowed if", "  _ = if .Circle{radius: _} = s {\n    take(s)\n  } else {\n    0.0\n  }\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := embedDowncastDecls + "fn main() {\n  s = Shape.Point\n  c = Circle{radius: 1.0}\n" + tc.body + "}\n"
			_, errs := checkSourceWithStdlib(src)
			expectNoStdlibErrors(t, errs)
		})
	}
	_, errs := checkSourceWithStdlib(embedDowncastDecls + "fn back(c: Circle): Shape {\n  c\n}\n\nfn back_return(c: Circle): Shape {\n  return c\n}\n")
	expectNoStdlibErrors(t, errs)
}

// A function value passed to a generic callee takes the values the callee
// gives it, so a function over an embedded type does not fit where a
// function over the enum is expected: `take` would be called with a Shape
// that may be a Point. The unification failed on that parameter, nothing
// was reported, and the error the program got was "the type argument U of
// `Iter.map` is not determined".
func TestEmbedDowncast_AFunctionOverTheEmbeddedTypeIsRefusedAsACallback(t *testing.T) {
	takeHint := "\nhelp: pass a function that takes a Shape and matches it first: `|value| case value { .Circle{} -> take(value) ... }`"
	for _, tc := range []struct {
		name, body, want string
	}{
		{"Iter.map", "  _ = Iter.map(ss, take) |> Iter.count()\n",
			"argument 2: `take` takes a Circle, but `Iter.map` calls it with a Shape, and a Shape may hold a variant other than Circle" + takeHint},
		{"a binding of the result", "  xs = Iter.map(ss, take) |> Iter.to_list()\n  _ = xs\n",
			"argument 2: `take` takes a Circle, but `Iter.map` calls it with a Shape, and a Shape may hold a variant other than Circle" + takeHint},
		{"a pipe stage", "  _ = ss |> Iter.map(take) |> Iter.count()\n",
			"argument 2: `take` takes a Circle, but `Iter.map` calls it with a Shape, and a Shape may hold a variant other than Circle" + takeHint},
		{"Maybe.map", "  _ = Maybe.map(Some(s), take)\n",
			"argument 2: `take` takes a Circle, but `Maybe.map` calls it with a Shape, and a Shape may hold a variant other than Circle" + takeHint},
		{"a lambda", "  _ = Iter.map(ss, |c: Circle| c.radius) |> Iter.count()\n",
			"argument 2: the lambda takes a Circle, but `Iter.map` calls it with a Shape, and a Shape may hold a variant other than Circle\nhelp: pass a function that takes a Shape and matches it first: `|value| case value { .Circle{} -> ... }`"},
		{"a nested parameter", "  nested: List<List<Shape>> = [[s]]\n  _ = Iter.map(nested, takes) |> Iter.count()\n",
			"argument 2: `takes` takes a List<Circle>, but `Iter.map` calls it with a List<Shape>, and a Shape may hold a variant other than Circle" + downcastHint},
		{"an embedded distinct", "  _ = Iter.map([Who.Nobody], take_id) |> Iter.count()\n",
			"argument 2: `take_id` takes an Id, but `Iter.map` calls it with a Who, and a Who may hold a variant other than Id\nhelp: pass a function that takes a Who and matches it first: `|value| case value { .Id(_) -> take_id(value) ... }`"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := embedDowncastDecls + "fn main() {\n  s = Shape.Point\n  ss: List<Shape> = [s]\n  _ = ss\n" + tc.body + "}\n"
			_, errs := checkSourceWithStdlib(src)
			expectExactDiag(t, errs, tc.want)
			if len(errs) != 1 {
				t.Errorf("want only that error, got %d: %v", len(errs), errs)
			}
		})
	}
}

// The mirror: a function over the enum fits where a function over an
// embedded type is expected, since every Circle is a Shape.
func TestEmbedDowncast_AFunctionOverTheEnumIsAcceptedAsACallback(t *testing.T) {
	src := embedDowncastDecls + "fn main() {\n  cs = [Circle{radius: 1.0}]\n  _ = Iter.map(cs, wants) |> Iter.to_list()\n  _ = cs |> Iter.map(wants) |> Iter.count()\n  _ = Maybe.map(Some(Circle{radius: 1.0}), wants)\n}\n"
	_, errs := checkSourceWithStdlib(src)
	expectNoStdlibErrors(t, errs)
}
