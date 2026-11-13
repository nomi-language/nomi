package analysis_test

import "testing"

// The declarations the variance cases share: a struct and an interface it
// implements, a generic struct and enum, and functions of each shape.
const varianceDecls = `struct Pt
  x: Int
end

interface Named
  fn name(v: self) -> String
end

impl Named for Pt
  fn name(_p: Pt) -> String
      "pt"
  end
end

struct Box<T>
  v: T
end

struct Sink<T>
  take: (T) -> String
end

struct Cell<T>
  get: () -> T
  put: (T) -> Unit
end

struct Circle
  r: Int
end

type Ids<T> List<T>

type Handler<T> (T) -> T

type Taker<T> (T) -> String

enum Shape
  embeds Circle
  Dot
end

fn radius(s: Shape) -> String
  case s
      .Circle{r:} -> Int.to_string(r)
      .Dot -> "0"
  end
end

enum Opt<T>
  Has(T)
  Nothing
end

fn number(i: Int) -> Int
    i
end

fn shown(x: Display) -> String
    Display.to_string(x)
end

fn float_str(f: Float) -> String
    Float.to_string(f)
end
`

// A type argument converts by its parameter's variance (spec §5 *Covariant
// conversion*): a std collection's (List, Vector, Set, Map keys and values,
// Maybe, Result), nested, through a tuple's elements and a record's fields,
// a user struct's or enum's covariant parameter, a function's result and,
// the other way, its parameter, at an argument, an annotation, a result and
// a field. An element that enters its interface boxed, a function that
// needs a wrapper and a type parameter convert too.
func TestTypeArgumentVariance_CovariantConverts(t *testing.T) {
	for name, body := range map[string]string{
		"List":                         `xs: List<Int> = [1]; ys: List<Display> = xs; dbg Iter.count(ys)`,
		"Vector":                       `xs: Vector<Int> = Vector[1]; ys: Vector<Display> = xs; dbg Iter.count(ys)`,
		"Set":                          `xs: Set<Int> = Set[1]; ys: Set<Hashable> = xs; dbg Iter.count(ys)`,
		"Map values":                   `m = {"a" => Pt{x: 1}}; n: Map<String, Named> = m; dbg Map.size(n)`,
		"Map keys":                     `m = {1 => "a"}; n: Map<Hashable, String> = m; dbg Map.size(n)`,
		"boxed Map keys":               `m = {Some(1) => "a"}; n: Map<Hashable, String> = m; dbg Map.size(n)`,
		"Maybe":                        `x: Maybe<Int> = Some(1); y: Maybe<Display> = x; dbg Maybe.is_some(y)`,
		"Result":                       `x: Result<Int, String> = Ok(1); y: Result<Display, String> = x; dbg Result.is_ok(y)`,
		"Result error":                 `x: Result<Int, Pt> = Err(Pt{x: 1}); y: Result<Int, Named> = x; dbg Result.is_ok(y)`,
		"nested":                       `xs = [[1]]; ys: List<List<Display>> = xs; dbg Iter.count(ys)`,
		"tuple element":                `xs = [("a", Pt{x: 1})]; ys: List<(String, Named)> = xs; dbg Iter.count(ys)`,
		"record field":                 `xs = [{n: 1}]; ys: List<{n: Display}> = xs; dbg Iter.count(ys)`,
		"function result":              `fs = [number]; gs: List<(Int) -> Display> = fs; dbg Iter.count(gs)`,
		"function argument":            `fs = [shown]; gs: List<(Int) -> String> = fs; dbg Iter.count(gs)`,
		"boxed element":                `xs = [Some(1)]; ys: List<Display> = xs; dbg Iter.count(ys)`,
		"boxed Maybe payload":          `x = Some([1]); y: Maybe<Display> = x; dbg Maybe.is_some(y)`,
		"wrapped function":             `fs = [shown]; gs: List<(Maybe<Int>) -> String> = fs; dbg Iter.count(gs)`,
		"nested function":              `f = fn -> [1]; g: () -> List<Display> = f; dbg Iter.count(g())`,
		"user struct":                  `b = Box{v: [1]}; c: Box<Display> = b; dbg Display.to_string(c.v)`,
		"user enum":                    `o = Opt.Has(Some(1)); p: Opt<Display> = o; dbg Iter.count([p])`,
		"user struct in a List":        `bs = [Box{v: 1}]; cs: List<Box<Display>> = bs; dbg Iter.count(cs)`,
		"contravariant field":          `s = Sink{take: shown}; u: Sink<Int> = s; dbg u.take(1)`,
		"distinct type, covariant":     `ids = Ids([Some(1)]); wide: Ids<Display> = ids; dbg wide`,
		"distinct type, contravariant": `t = Taker(shown); u: Taker<Int> = t; dbg u`,
		"embedded type, covariant":     `b = Box{v: Circle{r: 1}}; c: Box<Shape> = b; dbg radius(c.v)`,
		"embedded type, contravariant": `s = Sink{take: radius}; u: Sink<Circle> = s; dbg u.take(Circle{r: 1})`,
		"argument":                     `xs = [1]; dbg take(xs)`,
		"named argument":               `xs = [1]; dbg take(xs:)`,
		"pipe":                         `xs = [1]; dbg xs |> take()`,
		"field":                        `xs = [Pt{x: 1}]; g = Gallery{items: xs}; dbg Iter.count(g.items)`,
		"result":                       `dbg Iter.count(widen([1]))`,
		"branches":                     `a = [1]; b = ["x"]; c: List<Display> = if True then a else b; dbg Iter.count(c)`,
		"case branches": `a = [1]; b = ["x"]; c: List<Display> = case 1
    1 -> a
    _ -> b
end; dbg Iter.count(c)`,
	} {
		t.Run(name, func(t *testing.T) {
			src := varianceDecls + `
struct Gallery
  items: List<Named>
end

fn take(xs: List<Display>) -> Int
  Iter.count(xs)
end

fn widen(xs: List<Int>) -> List<Display>
  xs
end

fn main()
  ` + body + `
end
`
			expectNoErrorsT(t, checkWithStdlib(src))
		})
	}
	t.Run("a type parameter at an invariant position is refused", func(t *testing.T) {
		src := varianceDecls + "fn wide<T>(c: Cell<T>) -> Cell<Display> where T: Display\n  c\nend\n\nfn main()\n  dbg wide(Cell{get: fn -> 1, put: fn _ -> Unit}).get()\nend\n"
		expectErrorContaining(t, checkWithStdlib(src), "expected Cell<Display>, got Cell<T>")
	})
	t.Run("a type parameter", func(t *testing.T) {
		src := "fn wide<T>(xs: List<T>) -> List<Display> where T: Display\n  xs\nend\n\nfn main()\n  dbg Iter.count(wide([1]))\nend\n"
		expectNoErrorsT(t, checkWithStdlib(src))
	})
}

// A function's parameter is contravariant: a list of functions that take a
// Float is no list of functions that may be passed any Display.
func TestTypeArgumentVariance_ContravariantIsRefused(t *testing.T) {
	src := varianceDecls + "\nfn main()\n  fs = [float_str]\n  gs: List<(Display) -> String> = fs\n  dbg Iter.count(gs)\nend\n"
	expectErrorContaining(t, checkWithStdlib(src),
		"type mismatch: expected List<(Display) -> String>, got List<(Float) -> String>\nhelp: List<(Float) -> String> does not convert to List<(Display) -> String>: each function takes only Float, and one of type (Display) -> String may be passed any Display")
}

// A user type's parameter that its fields only take converts only the other
// way, one they both hold and take compares exactly, and a std struct's or
// host type's arguments compare exactly: each refusal names the reason.
func TestTypeArgumentVariance_UnsoundConversionsAreRefused(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"contravariant": {`s = Sink{take: float_str}; u: Sink<Display> = s; dbg u.take(1.5)`,
			"type mismatch: expected Sink<Display>, got Sink<Float>\nhelp: Sink<Float> does not convert to Sink<Display>: Sink's type parameter T is contravariant, since Sink's fields only take a T, so it converts only where Display converts to Float"},
		"invariant": {`c = Cell{get: fn -> 1, put: fn _ -> Unit}; d: Cell<Display> = c; dbg d.get()`,
			"type mismatch: expected Cell<Display>, got Cell<Int>\nhelp: Cell<Int> does not convert to Cell<Display>: Cell's type parameter T is invariant, since Cell's fields both hold a T and take one; build a new Cell<Display> from the value's parts"},
		"embedded type, invariant": {`c = Cell{get: fn -> Circle{r: 1}, put: fn _ -> Unit}; d: Cell<Shape> = c; dbg d.get()`,
			"type mismatch: expected Cell<Shape>, got Cell<Circle>\nhelp: Cell<Circle> does not convert to Cell<Shape>: Cell's type parameter T is invariant, since Cell's fields both hold a T and take one; build a new Cell<Shape> from the value's parts"},
		"embedded type, contravariant the wrong way": {`s = Sink{take: fn c: Circle -> Int.to_string(c.r)}; u: Sink<Shape> = s; dbg u.take(.Dot)`,
			"type mismatch: expected Sink<Shape>, got Sink<Circle>\nhelp: Sink<Circle> does not convert to Sink<Shape>: Sink's type parameter T is contravariant, since Sink's fields only take a T, so it converts only where Shape converts to Circle"},
		"distinct type, invariant": {`h = Handler(fn x: Int -> x); d: Handler<Display> = h; dbg d`,
			"type mismatch: expected Handler<Display>, got Handler<Int>\nhelp: Handler<Int> does not convert to Handler<Display>: Handler's type parameter T is invariant, since Handler's inner type both holds a T and takes one; build a new Handler<Display> from the value's parts"},
		"a collection as an Iter, invariant": {`c = Cell{get: fn -> [1], put: fn _ -> Unit}; d: Cell<Iter<Int>> = c; dbg d.get()`,
			"type mismatch: expected Cell<Iter<Int>>, got Cell<List<Int>>\nhelp: Cell<List<Int>> does not convert to Cell<Iter<Int>>: Cell's type parameter T is invariant"},
		"invariant in a List": {`cs = [Cell{get: fn -> 1, put: fn _ -> Unit}]; ds: List<Cell<Display>> = cs; dbg Iter.count(ds)`,
			"expected List<Cell<Display>>, got List<Cell<Int>>"},
		"an element that does not implement it": {`xs = [1.5]; ys: List<Named> = xs; dbg Iter.count(ys)`,
			"expected List<Named>, got List<Float>"},
	} {
		t.Run(name, func(t *testing.T) {
			src := varianceDecls + "\nfn main()\n  " + tc.body + "\nend\n"
			expectErrorContaining(t, checkWithStdlib(src), tc.want)
		})
	}
}

// An Iter's element converts as a value does at the top, since an Iter only
// hands its elements out: a `List<Int>` passes where an `Iter<Display>` is
// expected. A literal checks each element against the expected type.
func TestTypeArgumentVariance_IterAndLiterals(t *testing.T) {
	expectNoErrorsT(t, checkWithStdlib(`fn count(xs: Iter<Display>) -> Int
    Iter.count(xs)
end
fn main()
  xs: List<Int> = [1, 2]
  dbg count(xs)
  ys: List<Display> = [1, Some(2)]
  dbg Iter.count(ys)
end`))
}

// The declarations the collection-literal operand cases share: an
// interface, a struct that implements it, an alias of a function type that
// returns the interface, as std/http's `Handler` returns `ToResponse`, and
// functions that answer routes of that alias.
const literalOperandDecls = `interface Reply
  fn body(r: self) -> String
end

struct Page
  text: String
end

impl Reply for Page
  fn body(p: Page) -> String
      p.text
  end
end

typealias Handler (Int) -> Reply

fn page(_n: Int) -> Page
    Page{text: "p"}
end

fn other(_s: String) -> Page
    Page{text: "o"}
end

fn routes() -> List<(String, Handler)>
    [("a", page)]
end

fn vroutes() -> Vector<(String, Handler)>
    Vector[("a", page)]
end

fn mroutes() -> Map<String, Handler>
    {"a" => page}
end
`

// A collection literal beside a value of a collection type is built as that
// type, as a literal is where an annotation expects it (spec §8 *Arithmetic
// operators*, §3 *Spreads*): the operand of `+` on either side, and the
// elements of a list literal beside a spread. So a route whose handler
// answers a `Page` joins routes of `Handler`s with no annotation, and the
// result is a list of the alias.
func TestTypeArgumentVariance_LiteralBuiltAsTheOtherOperand(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"literal + list":    {`r = [("b", page)] + routes()`, "List<(String, (Int) -> Reply)>"},
		"list + literal":    {`r = routes() + [("b", page)]`, "List<(String, (Int) -> Reply)>"},
		"element, spread":   {`r = [("b", page), ..routes()]`, "List<(String, (Int) -> Reply)>"},
		"spread, element":   {`r = [..routes(), ("b", page)]`, "List<(String, (Int) -> Reply)>"},
		"vector + literal":  {`r = Vector[("b", page)] + vroutes()`, "Vector<(String, (Int) -> Reply)>"},
		"map + literal":     {`r = {"b" => page} + mroutes()`, "Map<String, (Int) -> Reply>"},
		"interface element": {`ds: List<Display> = ["a"]; r = [1, 2] + ds`, "List<Display>"},
	} {
		t.Run(name, func(t *testing.T) {
			expectBindingType(t, literalOperandDecls+"\nfn main()\n  "+tc.body+"\n  dbg r\nend\n", "r", tc.want)
		})
	}
}

// A literal that cannot be built as the other operand's type keeps its own,
// and the operator reports the two types. Two values that each have a type
// of their own must have one type, since nothing says which way either
// flows (spec §5 *Assignability*).
func TestTypeArgumentVariance_LiteralOperandMismatchIsRefused(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"a narrowing parameter": {`r = [("b", other)] + routes()`,
			"binary + type mismatch: List<(String, (String) -> Page)> vs List<(String, (Int) -> Reply)>"},
		"an element of another type": {`r = [1] + ["a"]`, "binary + type mismatch: List<Int> vs List<String>"},
		"two typed operands": {`mine = [("b", page)]; r = mine + routes()`,
			"binary + type mismatch: List<(String, (Int) -> Page)> vs List<(String, (Int) -> Reply)>"},
		"a spread beside a narrowing element": {`r = [("b", other), ..routes()]`,
			"list spread element type mismatch: (String, (String) -> Page) vs (String, (Int) -> Reply)"},
		"two typed spreads": {`mine = [("b", page)]; r = [..mine, ..routes()]`,
			"list spread element type mismatch: (String, (Int) -> Page) vs (String, (Int) -> Reply)"},
	} {
		t.Run(name, func(t *testing.T) {
			src := literalOperandDecls + "\nfn main()\n  " + tc.body + "\n  dbg r\nend\n"
			expectErrorContaining(t, checkWithStdlib(src), tc.want)
		})
	}
}
