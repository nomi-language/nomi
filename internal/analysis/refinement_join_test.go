package analysis_test

import "testing"

// A join (the branches of an `if` or `case`, the elements of a collection
// literal, a lambda's results) of a refinement and an ancestor of it, or of
// two refinements that share an ancestor, is the nearest ancestor the two
// share (spec §4 *Chains and conversion*), at the top and nested. Each
// case passes the join where that ancestor is expected, so the program
// checks only when the join is it or below it.
func TestRefinementJoin_TheNearestSharedAncestor(t *testing.T) {
	const decls = `fn int(n: Int) -> Int
    n
end

fn nonzero(n: NonZeroInt) -> NonZeroInt
    n
end

fn ints(xs: List<Int>) -> List<Int>
    xs
end

fn maybe_ints(xs: List<Maybe<Int>>) -> Int
    List.length(xs)
end

fn counts(m: Map<String, Int>) -> Int
    Map.size(m)
end

fn pick<T>(c: Bool, a: T, b: T) -> T
    if c then a else b
end

`
	for _, tc := range []struct{ name, body string }{
		{"an if of a refinement and its base", "    _ = int(if c then List.length(xs) else -1)"},
		{"a binding of the join", "    n = if c then List.length(xs) else -1\n    _ = int(n + 1)"},
		{"a case", "    _ = int(case c\n        True -> List.length(xs)\n        False -> -1\n    end)"},
		{"a refinement and its parent", "    _ = nonzero(if c then p else nz)"},
		{"the parent first", "    _ = nonzero(if c then nz else p)"},
		{"siblings at the base", "    _ = int(if c then nn else nz)"},
		{"a list literal", "    _ = ints([List.length(xs), -1])"},
		{"a list literal of options", "    _ = maybe_ints([Some(List.length(xs)), Some(-1), None])"},
		{"a map literal's values", "    _ = counts({\"a\" => List.length(xs), \"b\" => -1})"},
		{"lists in branches", "    _ = ints(if c then [nn] else [-1])"},
		{"options in branches", "    _ = maybe_ints([if c then Some(nn) else Some(-1)])"},
		{"two arguments of one type parameter", "    _ = int(pick(c, List.length(xs), -1))"},
		{"the ancestor first", "    _ = int(pick(c, -1, List.length(xs)))"},
		{"lists of one type parameter", "    _ = ints(pick(c, [nn], [-1]))"},
		{"a lambda's results", "    f = fn k: Int ->\n        if k > 0 then\n            return nn\n        end\n        k\n    end\n    _ = int(f(1))"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := decls + "fn main()\n    xs = [1, 2]\n    c = List.length(xs) > 1\n    nn: NonNegativeInt = 1\n    nz: NonZeroInt = -1\n    p: PositiveInt = 2\n" + tc.body + "\nend\n"
			_, errs := checkSourceWithStdlib(src)
			expectNoStdlibErrors(t, errs)
		})
	}
}

// The join is no nearer than the nearest shared ancestor: a refinement
// and its base join as the base, not the refinement, and two siblings as
// their parent or base. An expected type still wins: each branch is
// checked against it. A type parameter widens to the ancestor only where
// every argument it was solved from converts to it, so not past a
// function argument that takes it.
func TestRefinementJoin_NoNearerThanTheSharedAncestor(t *testing.T) {
	const decls = "fn int_of(c: Bool) -> Int\n    if c then 1 else -1\nend\n\nfn count(n: NonNegativeInt) -> Int\n    n\nend\n\nfn nonzero(n: NonZeroInt) -> Int\n    n\nend\n\nfn apply<T>(f: (T) -> Int, x: T) -> Int\n    f(x)\nend\n\n"
	for _, tc := range []struct{ name, body, want, at string }{
		{"a refinement and its base", "    j = if c then nn else i\n    _ = (count(j), nz)",
			"argument 1: expected NonNegativeInt, got Int\nhelp: NonNegativeInt is a subtype of Int; check the value with `NonNegativeInt.try_from(...)`, which is `Some` when it satisfies `fn n -> n >= 0` and `None` when not", "j"},
		{"siblings", "    _ = (nonzero(if c then nn else nz), i)",
			"argument 1: expected NonZeroInt, got Int\nhelp: NonZeroInt is a subtype of Int; check the value with `NonZeroInt.try_from(...)`, which is `Some` when it satisfies `fn n -> n != 0` and `None` when not", "if c then nn else nz"},
		{"the expected type checks each branch", "    n: NonNegativeInt = if c then nn else i\n    _ = (n, nz)",
			"type mismatch: expected NonNegativeInt, got Int\nhelp: NonNegativeInt is a subtype of Int; check the value with `NonNegativeInt.try_from(...)`, which is `Some` when it satisfies `fn n -> n >= 0` and `None` when not", "if c then nn else i"},
		{"a parameter a function argument takes", "    _ = (apply(count, i), nz)",
			"argument 2: expected NonNegativeInt, got Int\nhelp: NonNegativeInt is a subtype of Int; check the value with `NonNegativeInt.try_from(...)`, which is `Some` when it satisfies `fn n -> n >= 0` and `None` when not", "i"},
		{"different bases", "    s: NonEmptyString = \"a\"\n    v = if c then nn else s\n    _ = (v, nz, i)",
			"if/else branch type mismatch: then is NonNegativeInt, else is NonEmptyString", "s"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := decls + "fn main()\n    c = True\n    nn: NonNegativeInt = 1\n    nz: NonZeroInt = -1\n    i = int_of(c)\n" + tc.body + "\nend\n"
			_, errs := checkSourceWithStdlib(src)
			expectDiagAt(t, src, errs, tc.want, tc.at)
		})
	}
}

// Std's lengths, sizes, counts and indices are NonNegativeInts (spec §4
// *Literals, `try_from` and `unchecked`*), so each passes where one is
// expected, and a literal or a lambda's computed result beside one is an
// Int: a later argument, a range's other endpoint, an accumulator a lambda
// computes.
func TestRefinementJoin_StdCountsAreNonNegative(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"lengths and counts", "    _ = (Int.pow(2, List.length(xs)), Int.pow(2, String.length(\"ab\")), Int.pow(2, Iter.count(xs)), Int.pow(2, Map.size({1 => 2})), Int.pow(2, Iter.count_by(xs, &(&1 > 1))))"},
		{"an index", "    _ = xs |> Iter.with_index() |> Iter.map(fn (i, x) -> Int.pow(x, i)) |> Iter.sum()"},
		{"a found index", "    _ = Iter.find_index(xs, &(&1 > 1)) |> Maybe.map(&Int.pow(2, &1))"},
		{"abs and digits", "    _ = Int.to_digits(Int.abs(-12)) |> Iter.map(&Int.pow(2, &1)) |> Iter.to_list()"},
		{"a default after an index", "    n: Int = Maybe.with_default(Iter.find_index(xs, &(&1 > 9)), -1)\n    _ = n"},
		{"a piped default", "    _ = xs |> Iter.find_index(&(&1 > 9)) |> Maybe.with_default(-1)"},
		{"a range to a length", "    r: Range<Int> = 0..List.length(xs)\n    _ = r"},
		{"an accumulator a lambda computes", "    _ = Iter.reduce(xs, List.length(xs), fn acc, x -> acc - x)"},
		{"a piped accumulator", "    _ = List.length(xs) |> Iter.iterate(fn n -> n - 2) |> Iter.take(3) |> Iter.to_list()"},
		{"a map value a lambda computes", "    m = {\"a\" => List.length(xs)}\n    _ = Map.update(m, \"a\", 0, fn v -> v - 7)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "fn main()\n    xs = [1, 2, 3]\n" + tc.body + "\nend\n"
			_, errs := checkSourceWithStdlib(src)
			expectNoStdlibErrors(t, errs)
		})
	}
}
