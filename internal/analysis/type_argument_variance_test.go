package analysis_test

import "testing"

// The declarations the variance cases share: a struct and an interface it
// implements, a generic struct and enum, and functions of each shape.
const varianceDecls = `struct Pt {
  x: Int
}

interface Named {
  fn name(v: self) -> String
}

impl Named for Pt {
  fn name(_p: Pt) -> String { "pt" }
}

struct Box<T> {
  v: T
}

enum Opt<T> {
  Has(T)
  Nothing
}

fn number(i: Int) -> Int { i }

fn shown(x: Display) -> String { Display.to_string(x) }

fn float_str(f: Float) -> String { Float.to_string(f) }
`

// A std collection's type arguments convert where each element already is
// a value of the wider type (spec §5 *Type argument conversion*): a List,
// Vector, Set, Map (keys and values), Maybe and Result, nested, through a
// tuple's elements and a record's fields, and a function whose result
// widens or whose parameter narrows, at an argument, an annotation, a
// result and a field.
func TestTypeArgumentVariance_CovariantConverts(t *testing.T) {
	for name, body := range map[string]string{
		"List":              `xs: List<Int> = [1]; ys: List<Display> = xs; dbg Iter.count(ys)`,
		"Vector":            `xs: Vector<Int> = #[1]; ys: Vector<Display> = xs; dbg Iter.count(ys)`,
		"Set":               `xs: Set<Int> = %[1]; ys: Set<Hashable> = xs; dbg Iter.count(ys)`,
		"Map values":        `m = %{"a" => Pt{x: 1}}; n: Map<String, Named> = m; dbg Map.size(n)`,
		"Map keys":          `m = %{1 => "a"}; n: Map<Hashable, String> = m; dbg Map.size(n)`,
		"Maybe":             `x: Maybe<Int> = Some(1); y: Maybe<Display> = x; dbg Maybe.is_some(y)`,
		"Result":            `x: Result<Int, String> = Ok(1); y: Result<Display, String> = x; dbg Result.is_ok(y)`,
		"nested":            `xs = [[1]]; ys: List<List<Display>> = xs; dbg Iter.count(ys)`,
		"tuple element":     `xs = [("a", Pt{x: 1})]; ys: List<(String, Named)> = xs; dbg Iter.count(ys)`,
		"record field":      `xs = [.{n: 1}]; ys: List<.{n: Display}> = xs; dbg Iter.count(ys)`,
		"function result":   `fs = [number]; gs: List<(Int) -> Display> = fs; dbg Iter.count(gs)`,
		"function argument": `fs = [shown]; gs: List<(Int) -> String> = fs; dbg Iter.count(gs)`,
		"argument":          `xs = [1]; dbg take(xs)`,
		"named argument":    `xs = [1]; dbg take(xs: xs)`,
		"pipe":              `xs = [1]; dbg xs |> take()`,
		"field":             `xs = [Pt{x: 1}]; g = Gallery{items: xs}; dbg Iter.count(g.items)`,
		"result":            `dbg Iter.count(widen([1]))`,
		"branches":          `a = [1]; b = ["x"]; c: List<Display> = if True { a } else { b }; dbg Iter.count(c)`,
		"case branches": `a = [1]; b = ["x"]; c: List<Display> = case 1 { 1 -> a
    _ -> b }; dbg Iter.count(c)`,
	} {
		t.Run(name, func(t *testing.T) {
			src := varianceDecls + `
struct Gallery {
  items: List<Named>
}

fn take(xs: List<Display>) -> Int { Iter.count(xs) }

fn widen(xs: List<Int>) -> List<Display> { xs }

fn main() {
  ` + body + `
}
`
			expectNoErrorsT(t, checkWithStdlib(src))
		})
	}
}

// A conversion that would take work per element is refused, and the help
// names the explicit form: an element that enters its interface boxed (a
// Maybe, a type parameter), and a function that would need a wrapper.
func TestTypeArgumentVariance_PerElementWorkIsRefused(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"a boxed element", `xs = [Some(1)]; ys: List<Display> = xs; dbg ys`,
			"type mismatch: expected List<Display>, got List<Maybe<Int>>\nhelp: List<Maybe<Int>> does not convert to List<Display>: Maybe<Int> becomes Display boxed, so the conversion would build a new List element by element; write it where that is meant: `Iter.map(xs, fn x -> x) |> Iter.to_list()`"},
		{"a boxed Maybe payload", `x = Some([1]); y: Maybe<Display> = x; dbg y`,
			"type mismatch: expected Maybe<Display>, got Maybe<List<Int>>\nhelp: Maybe<List<Int>> does not convert to Maybe<Display>: List<Int> becomes Display boxed, so the conversion would build a new Maybe element by element; write it where that is meant: `Maybe.map(m, fn x -> x)`"},
		{"a wrapper per function", `fs = [shown]; gs: List<(Maybe<Int>) -> String> = fs; dbg Iter.count(gs)`,
			"type mismatch: expected List<(Maybe<Int>) -> String>, got List<(Display) -> String>\nhelp: List<(Display) -> String> does not convert to List<(Maybe<Int>) -> String>: each (Display) -> String would need a wrapper to become (Maybe<Int>) -> String, one per element; wrap them where that is meant: `Iter.map(fs, fn f -> fn x -> f(x)) |> Iter.to_list()`"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := varianceDecls + "\nfn main() {\n  " + tc.body + "\n}\n"
			expectErrorContaining(t, checkWithStdlib(src), tc.want)
		})
	}
	t.Run("a type parameter", func(t *testing.T) {
		src := "fn wide<T>(xs: List<T>) -> List<Display> where T: Display {\n  xs\n}\n\nfn main() {\n  dbg Iter.count(wide([1]))\n}\n"
		expectErrorContaining(t, checkWithStdlib(src), "T may be a type that becomes Display boxed")
	})
}

// A function's parameter is contravariant: a list of functions that take a
// Float is no list of functions that may be passed any Display.
func TestTypeArgumentVariance_ContravariantIsRefused(t *testing.T) {
	src := varianceDecls + "\nfn main() {\n  fs = [float_str]\n  gs: List<(Display) -> String> = fs\n  dbg Iter.count(gs)\n}\n"
	expectErrorContaining(t, checkWithStdlib(src),
		"type mismatch: expected List<(Display) -> String>, got List<(Float) -> String>\nhelp: List<(Float) -> String> does not convert to List<(Display) -> String>: each function takes only Float, and one of type (Display) -> String may be passed any Display")
}

// A declared generic type's type arguments compare exactly, a struct's, an
// enum's and an interface's, since its values carry them.
func TestTypeArgumentVariance_DeclaredTypesAreInvariant(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"struct": {`b = Box{v: 1}; c: Box<Display> = b; dbg c.v`,
			"type mismatch: expected Box<Display>, got Box<Int>\nhelp: Box<Int> does not convert to Box<Display>: a declared generic type's type arguments compare exactly, since its values carry them; build a new Box<Display> from the value's parts"},
		"enum":      {`o = Opt.Has(1); p: Opt<Display> = o; dbg Iter.count([p])`, "expected Opt<Display>, got Opt<Int>"},
		"in a List": {`bs = [Box{v: 1}]; cs: List<Box<Display>> = bs; dbg Iter.count(cs)`, "expected List<Box<Display>>, got List<Box<Int>>"},
	} {
		t.Run(name, func(t *testing.T) {
			src := varianceDecls + "\nfn main() {\n  " + tc.body + "\n}\n"
			expectErrorContaining(t, checkWithStdlib(src), tc.want)
		})
	}
}

// An Iter's element converts as a value does at the top, since an Iter only
// hands its elements out: a `List<Int>` passes where an `Iter<Display>` is
// expected. A literal checks each element against the expected type.
func TestTypeArgumentVariance_IterAndLiterals(t *testing.T) {
	expectNoErrorsT(t, checkWithStdlib(`fn count(xs: Iter<Display>) -> Int { Iter.count(xs) }
fn main() {
  xs: List<Int> = [1, 2]
  dbg count(xs)
  ys: List<Display> = [1, Some(2)]
  dbg Iter.count(ys)
}`))
}

// The declarations the collection-literal operand cases share: an
// interface, a struct that implements it, an alias of a function type that
// returns the interface, as std/http's `Handler` returns `ToResponse`, and
// functions that answer routes of that alias.
const literalOperandDecls = `interface Reply {
  fn body(r: self) -> String
}

struct Page {
  text: String
}

impl Reply for Page {
  fn body(p: Page) -> String { p.text }
}

typealias Handler (Int) -> Reply

fn page(_n: Int) -> Page { Page{text: "p"} }

fn other(_s: String) -> Page { Page{text: "o"} }

fn routes() -> List<(String, Handler)> { [("a", page)] }

fn vroutes() -> Vector<(String, Handler)> { #[("a", page)] }

fn mroutes() -> Map<String, Handler> { %{"a" => page} }
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
		"vector + literal":  {`r = #[("b", page)] + vroutes()`, "Vector<(String, (Int) -> Reply)>"},
		"map + literal":     {`r = %{"b" => page} + mroutes()`, "Map<String, (Int) -> Reply>"},
		"interface element": {`ds: List<Display> = ["a"]; r = [1, 2] + ds`, "List<Display>"},
	} {
		t.Run(name, func(t *testing.T) {
			expectBindingType(t, literalOperandDecls+"\nfn main() {\n  "+tc.body+"\n  dbg r\n}\n", "r", tc.want)
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
			src := literalOperandDecls + "\nfn main() {\n  " + tc.body + "\n  dbg r\n}\n"
			expectErrorContaining(t, checkWithStdlib(src), tc.want)
		})
	}
}
