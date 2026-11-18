package analysis_test

import "testing"

// The programs here were rejected while a walk over a type's structure
// was written out by hand and did not look inside every kind of type
// (type_walk.go). Each is held to being accepted, beside the form that
// already was.

// A comparison compares its operands as the types they are at run time, a
// refinement as its base, wherever in the operand's type the refinement
// is: in an anonymous struct's field as in a tuple's element.
func TestComparisonErasesARefinementInsideAnAnonymousStruct(t *testing.T) {
	head := "fn f(a: NonNegativeInt, b: Int) -> Bool\n  "
	cases := map[string]string{
		"tuple (already accepted)": "(a, 1) == (b, 1)",
		"anonymous struct":         "{n: a} == {n: b}",
		"ancestor on the left":     "{n: b} != {n: a}",
		"in a list":                "[{n: a}] == [{n: b}]",
		"in a tuple":               "(1, {n: a}) == (1, {n: b})",
		"nested":                   "{p: {n: a}} == {p: {n: b}}",
	}
	for name, expr := range cases {
		t.Run(name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(head + expr + "\nend\n")
			expectNoStdlibErrors(t, errs)
		})
	}
}

// A function's type parameters are the ones its signature names, in
// order, wherever it names them: one only a conjunction's member names
// takes an explicit type argument, as one a single interface names does.
func TestTypeParameterInAConjunctionTakesAnExplicitArgument(t *testing.T) {
	decls := "interface Source<T>\n  fn pull(s: self) -> Maybe<T>\nend\n\n" +
		"interface Named\n  fn name(s: self) -> String\nend\n\n" +
		"struct Ints\n  n: Int\nend\n\n" +
		"impl Source<Int> for Ints\n  fn pull(s: Ints) -> Maybe<Int>\n    Some(s.n)\n  end\nend\n\n" +
		"impl Named for Ints\n  fn name(s: Ints) -> String\n    \"${s.n}\"\n  end\nend\n\n" +
		"fn one<T>(s: Source<T>) -> Bool\n  Source.pull(s) |> Maybe.is_some()\nend\n\n" +
		"fn both<T>(s: Source<T> and Named) -> Bool\n  Source.pull(s) |> Maybe.is_some()\nend\n\n"
	cases := map[string]string{
		"interface (already accepted)": "fn f() -> Bool\n  one<Int>(Ints{n: 1})\nend\n",
		"conjunction":                  "fn f() -> Bool\n  both<Int>(Ints{n: 1})\nend\n",
		"conjunction, inferred":        "fn f() -> Bool\n  both(Ints{n: 1})\nend\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(decls + body)
			expectNoStdlibErrors(t, errs)
		})
	}
	// The argument is checked as the parameter's: one the impl does not
	// meet is still refused.
	_, errs := checkSourceWithStdlib(decls + "fn f() -> Bool\n  both<String>(Ints{n: 1})\nend\n")
	if len(errs) == 0 {
		t.Fatal("both<String>(Ints{n: 1}) is accepted, though Ints is a Source<Int>")
	}
	if want := "argument 1: expected Named and Source<String>, got Ints"; errs[0].Message != want {
		t.Fatalf("both<String>(Ints{n: 1}) reports %q, want %q", errs[0].Message, want)
	}
}

// A `Partial<T>` parameter handed on to a function that takes a
// `Partial<T>` is a patch of T, with T a struct and with T the enclosing
// function's own type parameter. The second was accepted only because a
// Partial of any type parameter was taken to hold a callee's
// (containsTypeParamExcept), which skipped the check the first failed.
func TestAPartialParameterIsHandedOn(t *testing.T) {
	decls := "struct Point\n  x: Int\n  y: Int\nend\n\n"
	cases := map[string]string{
		"a struct":              "fn patch(base: Point, p: Partial<Point>) -> Point\n  Struct.update(base, p)\nend\n",
		"a type parameter":      "fn patch<T>(base: T, p: Partial<T>) -> T where T: Struct\n  T.update(base, p)\nend\n",
		"a literal (as it was)": "fn patch(base: Point) -> Point\n  Struct.update(base, {x: 5})\nend\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(decls + body)
			expectNoStdlibErrors(t, errs)
		})
	}
	// A patch of another struct is still refused.
	_, errs := checkSourceWithStdlib(decls + "struct Size\n  w: Int\nend\n\nfn patch(base: Point, p: Partial<Size>) -> Point\n  Struct.update(base, p)\nend\n")
	if len(errs) == 0 {
		t.Fatal("a Partial<Size> is accepted as a patch of a Point")
	}
	if want := "argument 2: expected Partial<Point>, got Partial<Size>"; errs[0].Message != want {
		t.Fatalf("a Partial<Size> for a Point reports %q, want %q", errs[0].Message, want)
	}
}

// A lambda after an element that is a function returning an enum is
// checked against that function type, so a dot-leading variant in its body
// resolves, as one in a later tuple or constructor does.
func TestEarlierFunctionElementSetsALambdasEnum(t *testing.T) {
	decls := "enum Color\n  Red\n  Green\nend\n\nfn red() -> Color\n  Color.Red\nend\n\n"
	cases := map[string]string{
		"tuple (already accepted)": "fn f() -> Int\n  xs = [(Color.Red, 1), (.Green, 2)]\n  List.length(xs)\nend\n",
		"list":                     "fn f() -> Int\n  xs = [red, fn -> .Green]\n  List.length(xs)\nend\n",
		"map value":                "fn f() -> Int\n  m = {1 => red, 2 => fn -> .Green}\n  Map.size(m)\nend\n",
		"if branches":              "fn f(first: Bool) -> Color\n  pick = if first then red else fn -> .Green\n  pick()\nend\n",
		"case arms":                "fn f(n: Int) -> Color\n  pick = case n\n    0 -> red\n    _ -> fn -> .Green\n  end\n  pick()\nend\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(decls + body)
			expectNoStdlibErrors(t, errs)
		})
	}
}
