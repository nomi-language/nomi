package analysis_test

import "testing"

// A list, vector, set or map literal takes spreads anywhere, any number of
// them. A map spread has the literal's type; a list, vector or set spread is
// any Iter of the literal's elements. The elements have its element type.

func TestCheck_CollectionSpreads_Accepts(t *testing.T) {
	cases := map[string]string{
		"list, spread first":           "  xs = [1]\n  _a: List<Int> = [..xs, 2]\n",
		"list, spread between":         "  xs = [1]\n  _a: List<Int> = [0, ..xs, 2]\n",
		"list, two spreads":            "  xs = [1]\n  _a: List<Int> = [..xs, ..xs]\n",
		"list, empty spread":           "  _a: List<Int> = [..[], 1, ..[2]]\n",
		"vector, between":              "  v = Vector[1]\n  _a: Vector<Int> = Vector[0, ..v, 2]\n",
		"vector, two spreads":          "  v = Vector[1]\n  _a: Vector<Int> = Vector[..v, 2, ..v]\n",
		"set, spread last":             "  s = Set[1]\n  _a: Set<Int> = Set[0, ..s]\n",
		"set, two spreads":             "  s = Set[1]\n  _a: Set<Int> = Set[..s, ..Set[2]]\n",
		"list, spread of an Option":    "  m = Some(1)\n  _a: List<Int> = [0, ..m, ..None]\n",
		"list, spread of an Iter":      "  _a: List<Int> = [0, ..[1, 2] |> Iter.map(fn x -> x * 2)]\n",
		"list, spread of a set":        "  _a = [0, ..Set[1]]\n",
		"vector, spread of a list":     "  _a: Vector<Int> = Vector[1, ..[2], 3]\n",
		"vector, only an Iter spread":  "  _a = Vector[..Some(1)]\n",
		"set, spread of a list":        "  _a: Set<Int> = Set[1, ..[2]]\n",
		"set, an Iter spread then set": "  _a = Set[..Some(1), ..Set[2]]\n",
		"map, spread last":             "  m = {\"a\" => 1}\n  _a: Map<String, Int> = {\"a\" => 0, ..m}\n",
		"map, two spreads":             "  m = {\"a\" => 1}\n  _a: Map<String, Int> = {..m, ..{\"b\" => 2}}\n",
		"map, open first":              "  m = {\"a\" => 1}\n  _a: Map<String, Int> = {..{}, ..m}\n",
		"map, update after spreads": "  m = {\"a\" => {\"x\" => 1}}\n" +
			"  _a: Map<String, Map<String, Int>> = {\"b\" => {}, ..m, \"a\" => {.., \"y\" => 2}}\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib("fn main()\n" + body + "end\n")
			if len(errs) != 0 {
				t.Fatalf("errors = %v, want none", errs)
			}
		})
	}
}

// Each refusal is one error, with its exact message and position. An
// error about a spread is at its `..` in every kind of literal.
func TestCheck_CollectionSpreads_Refuses(t *testing.T) {
	cases := map[string]struct {
		body      string
		want      string
		line, col int
	}{
		"a list spread of a literal of another element": {"  xs = [1]\n  _a = [..xs, ..[\"b\"]]\n",
			"list element type mismatch: expected Int, got String", 3, 18},
		"a list spread of a list of another element": {"  xs = [1]\n  ys = [\"b\"]\n  _a = [..xs, ..ys]\n",
			"list spread element type mismatch: expected Int, got String", 4, 15},
		"a list spread that is no Iter": {"  n = 1\n  _a = [0, ..n]\n",
			"list spread `..` requires an Iter of the list's elements, got Int", 3, 12},
		"an element beside a spread of an Iter of another element": {"  _a = [0, ..Some(\"b\")]\n",
			"list element type mismatch: expected String, got Int", 2, 9},
		"a vector spread that is no Iter": {"  v = Vector[1]\n  _a = Vector[1, ..v, ..2]\n",
			"vector spread `..` requires an Iter of the vector's elements, got Int", 3, 23},
		"a vector spread of a literal of another element": {"  v = Vector[1]\n  _a = Vector[1, ..v, ..[\"b\"]]\n",
			"vector element type mismatch: expected Int, got String", 3, 26},
		"a vector spread of a list of another element": {"  v = Vector[1]\n  xs = [\"b\"]\n  _a = Vector[1, ..v, ..xs]\n",
			"vector spread element type mismatch: expected Int, got String", 4, 23},
		"a set spread after an element, that is no Iter": {"  n = 1\n  _a = Set[1, ..n]\n",
			"set spread `..` requires an Iter of the set's elements, got Int", 3, 15},
		"two map spreads of different types": {"  m = {\"a\" => 1}\n  o = {1 => 2}\n  _a = {..m, ..o}\n",
			"map spread type mismatch: expected Map<String, Int>, got Map<Int, Int>", 4, 14},
		"a map entry before every spread has no current value": {"  m = {\"a\" => {\"x\" => 1}}\n  _a = {\"a\" => {.., \"y\" => 2}, ..m}\n",
			"a bare `..` has no value to update here", 3, 17},
		"one set spread and no element": {"  s = Set[1]\n  _a = Set[..s]\n",
			"a set spread with no elements is its base value; write `s`", 3, 12},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib("fn main()\n" + c.body + "end\n")
			if len(errs) != 1 {
				t.Fatalf("errors = %v, want exactly %q", errs, c.want)
			}
			e := errs[0]
			if e.Message != c.want || e.Line != c.line || e.Col != c.col {
				t.Errorf("error = %d:%d %q, want %d:%d %q", e.Line, e.Col, e.Message, c.line, c.col, c.want)
			}
		})
	}
}

// A list, vector or set literal with spreads where its collection of an
// element type is expected is that collection, whatever its spreads hold:
// each spread is an Iter of elements that convert to the element type, and
// each element converts to it. The three collections are checked by one
// function (checkSpreadLitExpecting), so each row holds for all three.
func TestCheck_CollectionSpreads_AtAnExpectedElementType(t *testing.T) {
	const decls = "struct Circle\n  r: Int\nend\n\nstruct Dot\n  at: Int\nend\n\nenum Shape\n  embeds Circle\n  embeds Dot\nend\n\n"
	kinds := []struct{ name, ty, lit string }{
		{"list", "List", ""},
		{"vector", "Vector", "Vector"},
		{"set", "Set", "Set"},
	}
	rows := []struct{ name, elem, base, lit string }{
		{"an interface, beside an element of another type", "Display", "[1, 2]", "[..base, \"a\"]"},
		{"an interface, the element first", "Display", "[1, 2]", "[\"a\", ..base]"},
		{"an interface, the spread alone", "Display", "[1, 2]", "[..base]"},
		{"an enum, beside another embedded type", "Shape", "[Circle{r: 1}]", "[..base, Dot{at: 1}]"},
		{"an enum, the spread alone", "Shape", "[Circle{r: 1}]", "[..base]"},
		{"a refinement's base", "Int", "[List.length([1])]", "[..base, -1]"},
		{"a refinement's base, the spread alone", "Int", "[List.length([1])]", "[..base]"},
	}
	for _, k := range kinds {
		for _, r := range rows {
			t.Run(k.name+", "+r.name, func(t *testing.T) {
				src := decls + "fn main()\n  base = " + k.lit + r.base + "\n  _a: " + k.ty + "<" + r.elem + "> = " + r.lit + "\nend\n"
				_, errs := checkSourceWithStdlib(src)
				if len(errs) != 0 {
					t.Fatalf("errors = %v, want none\n%s", errs, src)
				}
			})
		}
	}
}

// A spread or an element that does not convert to the expected element
// type is refused in each collection, over the spread from its `..` or
// over the element.
func TestCheck_CollectionSpreads_AtAnExpectedElementTypeRefuses(t *testing.T) {
	for _, tc := range []struct{ name, body, want, at string }{
		{"a list spread", "  base = [\"a\"]\n  _a: List<Int> = [..base, 1]\n", "list spread element type mismatch: expected Int, got String", "..base"},
		{"a vector spread", "  base = Vector[\"a\"]\n  _a: Vector<Int> = [..base, 1]\n", "vector spread element type mismatch: expected Int, got String", "..base"},
		{"a set spread", "  base = Set[\"a\"]\n  _a: Set<Int> = [..base, 1]\n", "set spread element type mismatch: expected Int, got String", "..base"},
		{"a list element", "  base = [1]\n  _a: List<Int> = [..base, \"a\"]\n", "list element type mismatch: expected Int, got String", "\"a\""},
		{"a vector element", "  base = Vector[1]\n  _a: Vector<Int> = [..base, \"a\"]\n", "vector element type mismatch: expected Int, got String", "\"a\""},
		{"a set element", "  base = Set[1]\n  _a: Set<Int> = [..base, \"a\"]\n", "set element type mismatch: expected Int, got String", "\"a\""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "fn main()\n" + tc.body + "end\n"
			_, errs := checkSourceWithStdlib(src)
			expectDiagAt(t, src, errs, tc.want, tc.at)
		})
	}
}

// A list, vector or set literal of one spread of the literal's own type
// and nothing else is that spread, with an expected type as without one,
// and so is one whose spread is a refinement of it. One that converts its
// spread is not: a list spread where a vector is expected, elements of a
// refinement widened to their base, and a distinct type built from its inner
// type.
func TestCheck_CollectionSpreads_ALoneSpreadOfTheExpectedType(t *testing.T) {
	const decls = "type Ids List<Int>\n\ntype Vs Vector<Int>\n\n"
	for _, tc := range []struct{ name, body, want string }{
		{"a vector", "  base = Vector[1]\n  _a: Vector<Int> = [..base]\n", "a vector spread with no elements is its base value; write `base`"},
		{"a set", "  base = Set[1]\n  _a: Set<Int> = [..base]\n", "a set spread with no elements is its base value; write `base`"},
		{"a list", "  base = [1]\n  _a: List<Int> = [..base]\n", "a list spread with no elements is its base value; write `base`"},
		{"a list, no expected type", "  base = [1]\n  _a = [..base]\n", "a list spread with no elements is its base value; write `base`"},
		{"a vector, no expected type", "  base = Vector[1]\n  _a = Vector[..base]\n", "a vector spread with no elements is its base value; write `base`"},
		{"a refinement of the list", "  base: NonEmptyList<Int> = [1]\n  _a: List<Int> = [..base]\n", "a list spread with no elements is its base value; write `base`"},
		{"the refinement itself", "  base: NonEmptyList<Int> = [1]\n  _a: NonEmptyList<Int> = [..base]\n", "a list spread with no elements is its base value; write `base`"},
		{"a list as a vector", "  base = [1]\n  _a: Vector<Int> = [..base]\n", ""},
		{"a vector as a list", "  base = Vector[1]\n  _a: List<Int> = [..base]\n", ""},
		{"a set as a list", "  base = Set[1]\n  _a: List<Int> = [..base]\n", ""},
		{"refinement elements widened", "  base: List<PositiveInt> = [1]\n  _a: List<Int> = [..base]\n", ""},
		{"a distinct list built", "  base = [1]\n  _a: Ids = [..base]\n", ""},
		{"a distinct vector built", "  base = Vector[1]\n  _a: Vs = [..base]\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(decls + "fn main()\n" + tc.body + "end\n")
			if tc.want == "" {
				expectNoStdlibErrors(t, errs)
				return
			}
			if len(errs) != 1 || errs[0].Message != tc.want {
				t.Fatalf("errors = %v, want exactly %q", errs, tc.want)
			}
		})
	}
}

// A spread that is no Iter is the same error at the same place, its `..`
// through the end of its head, in a list, a vector and a set literal,
// whether or not the literal's position expects a type. Each source is
// refused: the test fails if one is admitted.
func TestCheck_CollectionSpreads_ASpreadThatIsNoIterIsAtItsDots(t *testing.T) {
	for _, k := range []struct{ kind, lit, ty string }{
		{"list", "[", "List<Int>"},
		{"vector", "Vector[", "Vector<Int>"},
		{"set", "Set[", "Set<Int>"},
	} {
		want := k.kind + " spread `..` requires an Iter of the " + k.kind + "'s elements, got Int"
		for name, binding := range map[string]string{"no expected type": "_a", "an expected type": "_a: " + k.ty} {
			t.Run(k.kind+", "+name, func(t *testing.T) {
				src := "fn main()\n  n = 1\n  " + binding + " = " + k.lit + "0, ..n]\nend\n"
				_, errs := checkSourceWithStdlib(src)
				if len(errs) != 1 {
					t.Fatalf("errors = %v, want exactly %q", errs, want)
				}
				expectDiagAt(t, src, errs, want, "..n")
			})
		}
	}
}

// spreadDecidesSource is a program whose `r` is the literal lit, beside
// bindings of the collection kind ("List", "Vector", "Set") to spread.
func spreadDecidesSource(kind, lit string) string {
	return "enum Color\n  Red\n  Green\nend\n\nfn main()\n" +
		"  xs: " + kind + "<Int> = [1]\n" +
		"  words: " + kind + "<String> = [\"w\"]\n" +
		"  ps: " + kind + "<PositiveInt> = [1]\n" +
		"  bytes: " + kind + "<Byte> = [65]\n" +
		"  cs: " + kind + "<Color> = [.Red]\n" +
		"  n = -1\n" +
		"  p: PositiveInt = 1\n" +
		"  r = " + lit + "\n" +
		"  _ = (r, xs, words, ps, bytes, cs, n, p)\n" +
		"end\n"
}

var spreadDecidesKinds = []struct{ kind, name, head string }{
	{"List", "list", ""},
	{"Vector", "vector", "Vector"},
	{"Set", "set", "Set"},
}

// With no expected type, a literal's spreads decide its element type
// wherever they are written, the same way in a list, a vector and a set:
// a loose element that does not fit is the error at it, and a later spread
// of other elements the error at its `..`, each with the first spread's
// element type as the expected one. Each source is refused: the test fails
// if one is admitted.
func TestCheck_CollectionSpreads_TheSpreadDecidesTheElementType(t *testing.T) {
	for _, k := range spreadDecidesKinds {
		for _, tc := range []struct{ lit, want, at string }{
			{"[..xs, \"s\"]", " element type mismatch: expected Int, got String", "\"s\""},
			{"[\"s\", ..xs]", " element type mismatch: expected Int, got String", "\"s\""},
			{"[..xs, ..words]", " spread element type mismatch: expected Int, got String", "..words"},
			{"[..words, ..xs]", " spread element type mismatch: expected String, got Int", "..xs"},
			{"[..xs, 1, ..words]", " spread element type mismatch: expected Int, got String", "..words"},
		} {
			lit := k.head + tc.lit
			t.Run(k.name+" "+lit, func(t *testing.T) {
				src := spreadDecidesSource(k.kind, lit)
				_, errs := checkSourceWithStdlib(src)
				if len(errs) != 1 {
					t.Fatalf("errors = %v, want exactly one", errs)
				}
				expectDiagAt(t, src, errs, k.name+tc.want, tc.at)
			})
		}
	}
}

// What a spread's element type admits beside it with no expected type,
// the same in a list, a vector and a set: a refinement beside its base, in
// either order, among elements or among spreads, joins as the base; a
// numeric literal adapts to the spread's element type; a bare variant
// resolves against it.
func TestCheck_CollectionSpreads_WhatTheSpreadsElementTypeAdmits(t *testing.T) {
	for _, k := range spreadDecidesKinds {
		for _, tc := range []struct{ lit, elem string }{
			{"[..ps, n]", "Int"},
			{"[n, ..ps]", "Int"},
			{"[..xs, p]", "Int"},
			{"[p, ..xs]", "Int"},
			{"[..ps, ..xs]", "Int"},
			{"[..xs, ..ps]", "Int"},
			{"[..bytes, 66]", "Byte"},
			{"[66, ..bytes]", "Byte"},
			{"[..cs, .Green]", "Color"},
			{"[.Green, ..cs]", "Color"},
		} {
			lit := k.head + tc.lit
			t.Run(k.name+" "+lit, func(t *testing.T) {
				expectBindingType(t, spreadDecidesSource(k.kind, lit), "r", k.kind+"<"+tc.elem+">")
			})
		}
	}
}

// A spread of an interface type decides the element type as any spread
// does: an element of another type that implements it enters it.
func TestCheck_CollectionSpreads_AnInterfaceSpreadDecidesTheElementType(t *testing.T) {
	for _, k := range spreadDecidesKinds[:2] {
		for _, lit := range []string{"[..ds, \"a\"]", "[\"a\", ..ds]"} {
			lit = k.head + lit
			t.Run(k.name+" "+lit, func(t *testing.T) {
				src := "fn main()\n  ds: " + k.kind + "<Display> = [1]\n  r = " + lit + "\n  _ = r\nend\n"
				expectBindingType(t, src, "r", k.kind+"<Display>")
			})
		}
	}
}

// A literal with no spread keeps its first element's type, which a later
// element of another type is the error against.
func TestCheck_CollectionSpreads_WithNoSpreadTheFirstElementDecides(t *testing.T) {
	for _, k := range spreadDecidesKinds {
		lit := k.head + "[1, \"s\"]"
		t.Run(k.name, func(t *testing.T) {
			src := "fn main()\n  r = " + lit + "\n  _ = r\nend\n"
			_, errs := checkSourceWithStdlib(src)
			if len(errs) != 1 {
				t.Fatalf("errors = %v, want exactly one", errs)
			}
			expectDiagAt(t, src, errs, k.name+" element type mismatch: expected Int, got String", "\"s\"")
		})
	}
}
