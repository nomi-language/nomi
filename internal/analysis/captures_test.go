package analysis_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

const captureDecls = `struct Point {
    x: Int
    y: Int
}

struct Box<T> {
    v: T
}

struct Rule {
    keep: (Int) -> Bool
}

enum Shape {
    Circle {radius: Float}
    Dot
}

fn add(a: Int, b: Int) -> Int {
    a + b
}

fn divide(x: Int, y: Int) -> Int {
    x / y
}

fn apply2(f: (Int, Int) -> Int) -> Int {
    f(3, 4)
}

`

// captureTypes is the type the checker gave each capture of src, sorted.
// The imports src starts with go before captureDecls.
func captureTypes(t *testing.T, src string) []string {
	t.Helper()
	imports := ""
	for strings.HasPrefix(src, "import ") {
		line, rest, _ := strings.Cut(src, "\n")
		imports += line + "\n"
		src = rest
	}
	fa, errs := checkSourceWithStdlib(imports + captureDecls + src)
	expectNoStdlibErrors(t, errs)
	var out []string
	for n, ty := range fa.ExprTypes {
		if _, ok := n.(*ast.Capture); ok && ty != nil {
			out = append(out, ty.String())
		}
	}
	sort.Strings(out)
	return out
}

// A capture is `&` and a call, a parenthesized expression or a literal;
// the function type its position expects fixes its arity and its
// parameters' types.
func TestCapture_EachBodyIsAFunction(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"comparison", "fn f() -> (Int) -> Bool { &(&1 > 2) }\n", "(Int) -> Bool"},
		{"comparison, the parameter on the right", "fn f() -> (Int) -> Bool { &(0 < &1) }\n", "(Int) -> Bool"},
		{"equality", "fn f() -> (String) -> Bool { &(&1 == \"a\") }\n", "(String) -> Bool"},
		{"two parameters", "fn f() -> (Int, Int) -> Int { &(&1 + &2) }\n", "(Int, Int) -> Int"},
		{"two steps", "fn f() -> (Int) -> Int { &(&1 * 2 + 1) }\n", "(Int) -> Int"},
		{"remainder and equality", "fn f() -> (Int) -> Bool { &(&1 % 2 == 0) }\n", "(Int) -> Bool"},
		{"list concatenation", "fn f() -> (List<Int>) -> List<Int> { &(&1 + [0]) }\n", "(List<Int>) -> List<Int>"},
		{"half-open range", "fn f() -> (Int) -> Range<Int> { &(0..&1) }\n", "(Int) -> Range<Int>"},
		{"negation", "fn f() -> (Int) -> Int { &(-&1) }\n", "(Int) -> Int"},
		{"not", "fn f() -> (Bool) -> Bool { &(!&1) }\n", "(Bool) -> Bool"},
		{"a field read", "fn f() -> (Point) -> Bool { &(&1.x > 2) }\n", "(Point) -> Bool"},
		{"a parenthesized parameter", "fn f() -> (Int, Int) -> Int { &(&2) }\n", "(Int, Int) -> Int"},
		{"a call", "fn f() -> (Int) -> Int { &Int.max(&1, 0) }\n", "(Int) -> Int"},
		{"a call reordering its parameters", "fn f() -> (Int, Int) -> Int { &divide(&2, &1) }\n", "(Int, Int) -> Int"},
		{"a call reusing its parameter", "fn f() -> (Int) -> Int { &add(&1, &1) }\n", "(Int) -> Int"},
		{"a call with a deeper parameter", "fn f() -> (Int) -> Maybe<Int> { &Some(&1 + 1) }\n", "(Int) -> Maybe<Int>"},
		{"a call of a type parameter's function", "import std/json.{Json, FromJson}\n\nfn f<T>(xs: List<Json>) -> List<Result<T, Json.ShapeError>> where T: FromJson {\n    Iter.map(xs, &T.from_json(&1)) |> Iter.to_list()\n}\n", "(Json) -> Result<T, Json.ShapeError>"},
		{"named struct", "fn f() -> (Int) -> Point { &#Point{x: &1, y: 0} }\n", "(Int) -> Point"},
		{"named struct, two parameters", "fn f() -> (Int, Int) -> Point { &#Point{y: &1, x: &2} }\n", "(Int, Int) -> Point"},
		{"anonymous struct where a Point is expected", "fn f() -> (Int) -> Point { &#{x: &1, y: 0} }\n", "(Int) -> Point"},
		{"generic struct", "fn f() -> (String) -> Box<String> { &#Box{v: &1} }\n", "(String) -> Box<String>"},
		{"struct-shaped variant", "fn f() -> (Float) -> Shape { &#Shape.Circle{radius: &1} }\n", "(Float) -> Shape"},
		{"dot struct-shaped variant", "fn f() -> (Float) -> Shape { &#.Circle{radius: &1} }\n", "(Float) -> Shape"},
		{"tuple", "fn f() -> (Int) -> #(Int, Int) { &#(&1, 0) }\n", "(Int) -> #(Int, Int)"},
		{"list", "fn f() -> (Int) -> List<Int> { &[&1, 0] }\n", "(Int) -> List<Int>"},
		{"vector", "fn f() -> (Int) -> Vector<Int> { &#[&1] }\n", "(Int) -> Vector<Int>"},
		{"set", "fn f() -> (Int) -> Set<Int> { &%[&1] }\n", "(Int) -> Set<Int>"},
		{"map", "fn f() -> (String) -> Map<String, Int> { &%{&1 => 0} }\n", "(String) -> Map<String, Int>"},
		{"a generic callback", "fn f() -> List<Int> { [1, 2] |> Iter.map(&(&1 * 2)) |> Iter.to_list() }\n", "(Int) -> Int"},
		{"an annotated binding", "fn f() -> Bool {\n    big: (Int) -> Bool = &(&1 > 2)\n    big(3)\n}\n", "(Int) -> Bool"},
		{"a struct field of function type", "fn f() -> Rule { #Rule{keep: &(&1 > 2)} }\n", "(Int) -> Bool"},
		{"the argument of Some", "fn f() -> Maybe<(Int) -> Int> { Some(&(&1 + 1)) }\n", "(Int) -> Int"},
		{"an ignored parameter", "fn f() -> Int { apply2(&(&2 * 10)) }\n", "(Int, Int) -> Int"},
		{"a partial ignoring a parameter", "fn f() -> Int { apply2(&add(&1, 100)) }\n", "(Int, Int) -> Int"},
		{"a then stage", "fn f() -> Int { 10 |> then &divide(100, &1) }\n", "(Int) -> Int"},
		{"a then stage with no partial", "fn f() -> Int { 10 |> then &(&1 + 1) }\n", "(Int) -> Int"},
		{"a tap stage", "import std/io\n\nfn f() -> Int { 10 |> tap &io.print(Int.to_string(&1)) }\n", "(Int) -> Unit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := captureTypes(t, tc.body)
			if len(got) != 1 || got[0] != tc.want {
				t.Fatalf("capture types = %q, want [%q]", got, tc.want)
			}
		})
	}
}

// A partial capture, whose body is a call taking each `&N` as one of its
// own arguments in order, takes its parameters' types, names and defaults
// from the callee, so it needs no expected type.
func TestCapture_APartialNeedsNoExpectedType(t *testing.T) {
	for name, body := range map[string]string{
		"no expected type":   "fn f() -> Int {\n    inc = &add(1, &1)\n    inc(2)\n}\n",
		"called by name":     "fn f() -> Int {\n    inc = &add(&1, b: &2)\n    inc(b: 1, a: 2)\n}\n",
		"called directly":    "fn f() -> Int { (&add(2, &1))(4) }\n",
		"inside a tuple":     "fn f() -> Int {\n    t = #(&Int.max(&1, 0), 1)\n    t.0(-3)\n}\n",
		"a variant":          "fn f() -> Maybe<Int> {\n    wrap = &Some(&1)\n    wrap(1)\n}\n",
		"partial of partial": "fn f() -> Int {\n    g = &add(&1, b: &2)\n    h = &g(1, b: &1)\n    h(2)\n}\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(captureDecls + body)
			expectNoStdlibErrors(t, errs)
		})
	}
}

// A struct literal whose `&N`s are its whole fields takes their names, by
// which a call of the function bound to a name passes them.
func TestCapture_AStructIsCalledByItsFields(t *testing.T) {
	_, errs := checkSourceWithStdlib(captureDecls + "fn f() -> Point {\n    mk: (Int) -> Point = &#Point{x: &1, y: 0}\n    mk(x: 3)\n}\n")
	expectNoStdlibErrors(t, errs)
	_, errs = checkSourceWithStdlib(captureDecls + "fn f() -> Bool {\n    big: (Int) -> Bool = &(&1 > 2)\n    big(lhs: 3)\n}\n")
	wantOnlyError(t, errs, "no parameter named 'lhs'")
}

// wantOnlyError fails unless errs, unused bindings aside, is one error that
// reads want.
func wantOnlyError(t *testing.T, errs []analysis.TypeError, want string) {
	t.Helper()
	errs = withoutUnusedBindingErrors(errs)
	if len(errs) != 1 || !strings.HasPrefix(diagText(errs[0]), want) {
		var msgs []string
		for _, e := range errs {
			msgs = append(msgs, diagText(e))
		}
		t.Fatalf("errors = %q, want exactly one reading %q", msgs, want)
	}
}

// The errors around captures: each fails when the program starts being
// accepted, and checks its message.
func TestCapture_Rejections(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"no expected type", "fn f() -> Bool {\n    big = &(&1 > 2)\n    big(3)\n}\n",
			"cannot infer type for `&1` in `&(&1 > 2)`\nhelp: annotate the binding with the function's type, as in `f: (Int) -> Bool = &(&1 > 2)`, or write a lambda with an annotated parameter, as in `fn x: Int -> x > 2`; a later use of the function does not give it one"},
		{"no expected type, a struct", "fn f() -> Point {\n    mk = &#Point{x: &1, y: 0}\n    mk(1)\n}\n",
			"cannot infer type for `&1` in `&#Point{x: &1, y: 0}`\nhelp: annotate the binding with the function's type, as in `f: (Int) -> Point = &#Point{x: &1, y: 0}`, or write a lambda with an annotated parameter, as in `fn x: Int -> #Point{x, y: 0}`; a later use of the function does not give it one"},
		{"no expected type, a reused call", "fn f() -> Int {\n    twice = &add(&1, &1)\n    twice(2)\n}\n",
			"cannot infer type for `&1` in `&add(&1, &1)`"},
		{"no expected type, a reordered call", "fn f() -> Int {\n    flip = &divide(&2, &1)\n    flip(2, 10)\n}\n",
			"cannot infer type for `&2` in `&divide(&2, &1)`"},
		{"a capture in a construction's argument", "fn f() -> List<Maybe<Int>> { [1] |> Iter.map(Some(&(&1 + 1))) |> Iter.to_list() }\n",
			"cannot infer type for `&1` in `&(&1 + 1)`\nhelp: for a function of the whole call, write `&Some(&1 + 1)`"},
		{"past the expected arity", "fn f() -> List<Int> { [1] |> Iter.map(&(&2 + 1)) |> Iter.to_list() }\n",
			"`Iter.map` passes one argument; `&2` names a second\nhelp: `&N` is the capture's parameter N, and the function expected where the capture stands fixes how many it has"},
		{"past the arity of an annotation", "fn f() -> Int {\n    g: (Int) -> Int = &(&1 + &3)\n    g(1)\n}\n",
			"the function expected here takes one argument; `&3` names a third"},
		{"a partial past the expected arity", "fn f() -> List<Int> { [1] |> Iter.map(&add(&1, &2)) |> Iter.to_list() }\n",
			"`Iter.map` passes one argument; `&2` names a second"},
		{"a then stage passes one argument", "fn f() -> Int { 10 |> then &add(&1, &2) }\n",
			"the function expected here takes one argument; `&2` names a second"},
		{"no parameter", "fn f() -> List<Int> { [1] |> Iter.map(&add(1, 2)) |> Iter.to_list() }\n",
			"a capture uses at least `&1`\nhelp: `&` makes a function of the `&1`, `&2`, ... in its body; for a function of no arguments, write `fn -> add(1, 2)`"},
		{"captures do not nest", "fn f() -> List<Int> { [1] |> Iter.map(&add(&1, &Int.max(&1, 0))) |> Iter.to_list() }\n",
			"captures do not nest; write the inner one as `fn x -> ...`\nhelp: each `&N` belongs to the one capture around it; inside `&add(&1, &Int.max(&1, 0))`, write `&Int.max(&1, 0)` as a lambda"},
		{"a capture is no pipe stage", "fn f() -> Int { 10 |> &add(&1, 1) }\n",
			"a capture is not a pipe stage: write `then &add(&1, 1)`\nhelp: `then` applies a capture or a lambda to the piped value"},
		{"a parameter of the wrong type", "fn f() -> (String) -> Point { &#Point{x: &1, y: 0} }\n",
			"field 'x' of Point: expected Int, got String"},
		{"a reduction's callback", "fn f() -> Int { [1, 2] |> Iter.reduce(&(&1 + &2)) }\n",
			"`Iter.reduce` needs a starting value\nhelp: pass a lambda whose accumulator has a starting value: `fn acc = <start>, x -> ...`"},
		{"an operand of and", "fn f(ready: Bool) -> (Int) -> Bool { &(&1 > 2) and ready }\n",
			"cannot infer type for `&1` in `&(&1 > 2)`"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(captureDecls + tc.body)
			var msgs []string
			found := false
			for _, e := range withoutUnusedBindingErrors(errs) {
				msgs = append(msgs, diagText(e))
				if strings.HasPrefix(diagText(e), tc.want) {
					found = true
				}
			}
			if len(msgs) == 0 {
				t.Fatalf("the program is accepted; want the error %q", tc.want)
			}
			if !found {
				t.Fatalf("errors = %q, want one to read %q", msgs, tc.want)
			}
		})
	}
}

// `&N` outside a capture is an error whose help writes the capture the
// `&N` most likely belongs to: the call taking it as an argument, the
// expression an operator, range, field read or parentheses make of it, or
// the literal it is a field or element of. In a pipe stage, the pipe fills
// the first argument, so the help writes a `then` stage.
func TestCapture_AParameterOutsideACapture(t *testing.T) {
	const msg = "`&1` is a capture's parameter: write the function as `&(...)` or `&f(...)`\nhelp: "
	for _, tc := range []struct{ name, body, help string }{
		{"a call's argument", "fn f() -> List<Int> { [1] |> Iter.map(Int.max(&1, 0)) |> Iter.to_list() }\n", "write `&Int.max(&1, 0)`"},
		{"a named argument", "fn f() -> (Int) -> Int { add(a: 1, b: &1) }\n", "write `&add(a: 1, b: &1)`"},
		{"an operator", "fn f() -> List<Int> { [1] |> Iter.map(&1 * 2 + 1) |> Iter.to_list() }\n", "write `&(&1 * 2 + 1)`"},
		{"an operator in a call's argument", "fn f() -> Maybe<Int> { Some(&1 + 1) }\n", "write `&(&1 + 1)`"},
		{"a struct literal", "fn f() -> (Int) -> Point { #Point{x: &1, y: 0} }\n", "write `&#Point{x: &1, y: 0}`"},
		{"a tuple", "fn f() -> (Int) -> #(Int, Int) { #(&1, 0) }\n", "write `&#(&1, 0)`"},
		{"a list", "fn f() -> (Int) -> List<Int> { [&1, 0] }\n", "write `&[&1, 0]`"},
		{"parentheses", "fn f() -> (Int) -> Int { (&1) + 1 }\n", "write `&((&1) + 1)`"},
		{"a pipe stage", "fn f() -> Int { 10 |> divide(100, &1) }\n", "a pipe fills the first argument; write `|> then &divide(100, &1)`"},
		{"alone", "fn f() -> Int {\n    g = &1\n    g\n}\n", "a capture is `&` and the function's body, as in `&(&1 > 2)` or `&Int.max(&1, 0)`; `&N` alone is no function, so write a lambda"},
		{"inside a lambda in a capture", "fn f() -> (Int) -> Int { &add(&1, (fn y: Int -> y + &1)(1)) }\n", "write `&(y + &1)`"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(captureDecls + tc.body)
			var msgs []string
			hits := 0
			for _, e := range withoutUnusedBindingErrors(errs) {
				msgs = append(msgs, diagText(e))
				if diagText(e) == msg+tc.help {
					hits++
				}
			}
			if hits != 1 {
				t.Fatalf("errors = %q, want exactly one %q", msgs, msg+tc.help)
			}
		})
	}
}

// A capture's `&N`s are its parameters, so each has the type of the
// parameter it names.
func TestCapture_ParametersAreTyped(t *testing.T) {
	fa, errs := checkSourceWithStdlib(captureDecls + "fn f() -> (Int, String) -> #(String, Int) { &#(&2, &1) }\n")
	expectNoStdlibErrors(t, errs)
	var got []string
	for n, ty := range fa.ExprTypes {
		if p, ok := n.(*ast.PlaceholderParam); ok && ty != nil {
			got = append(got, "&"+string(rune('0'+p.N))+":"+ty.String())
		}
	}
	sort.Strings(got)
	if strings.Join(got, ",") != "&1:Int,&2:String" {
		t.Fatalf("parameter types = %q, want &1:Int and &2:String", got)
	}
}
