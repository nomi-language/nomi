package vmhost

import (
	"context"
	"strings"
	"testing"
)

// subtypeMeetSrc binds values of the std non-empty and numeric
// subtypes, and of their bases, for a program to meet one with the
// other.
const subtypeMeetSrc = `import std::io

fn pick<T>(c: Bool, a: T, b: T) -> T
    if c then a else b
end

fn main()
    c = String.length("ab") > 5
    xs: NonEmptyList<String> = ["a"]
    vs: NonEmptyVector<Int> = Vector[1]
    ms: NonEmptyMap<String, Int> = {"a" => 1}
    s: NonEmptyString = "a"
    p: PositiveInt = 2
    ps: List<PositiveInt> = [1, 2]
    mps: Map<String, PositiveInt> = {"a" => 1}
    ints: List<Int> = [0]
    mints: Map<String, Int> = {"z" => 0}
    xss: List<List<NonNegativeInt>> = [[1, 2], [3]]
`

// Where a value of a subtype meets a value or a literal of its base, the
// program runs at their join, in either order (spec §4 *Chains and
// conversion*): no literal there is held to the subtype's rule, and
// each value is the one written.
func TestSubtypeMeet_ProgramsRunAtTheJoin(t *testing.T) {
	p, err := LoadSource("main", subtypeMeetSrc+`    io.inspect((xs == [], [] == xs, xs != [], Some(xs) == Some([]), (xs, 1) == ([], 1)))
    io.inspect((if c then [] else xs, if c then xs else []))
    io.inspect((if c then Vector[] else vs, if c then {} else ms, if c then "" else s))
    io.inspect((pick(c, [], xs), pick(c, xs, [])))
    io.inspect((Option.with_default(Some(xs), []), Option.otherwise(Some(xs), Some([]))))
    io.inspect(Map.put({"k" => xs}, "j", []))
    io.inspect(Iter.group_by([1, 2], fn n -> n % 2) |> Map.get(5) |> Option.with_default([]))
    io.inspect(([..ps, 0], [0, ..ps], [..ps, -1]))
    io.inspect(({..mints, ..mps}, {..mps, ..mints}, {..mps, "b" => 0}))
    io.inspect((vs + [3], [3] + vs, ints + ps, ps + ints, s + ""))
    io.inspect((Iter.map(xss, Iter.sum) |> Iter.to_list(), xss |> Iter.map(Iter.product) |> Iter.to_list()))
    io.inspect((pick(c, p, 0), pick(c, 0, p), if c then -1 else p))
end
`)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatal(err)
	}
	const want = `(False, False, True, False, False)
(["a"], [])
(Vector[1], {"a" => 1}, "a")
(["a"], [])
(["a"], Some(["a"]))
{"k" => ["a"], "j" => []}
[]
([1, 2, 0], [0, 1, 2], [1, 2, -1])
({"z" => 0, "a" => 1}, {"a" => 1, "z" => 0}, {"a" => 1, "b" => 0})
(Vector[1, 3], Vector[3, 1], [0, 1, 2], [1, 2, 0], "a")
([3, 3], [2, 3])
(0, 2, 2)
`
	if out.String() != want {
		t.Errorf("output =\n%s\nwant\n%s", out.String(), want)
	}
}

// Where the program writes the subtype for the value, a literal that
// breaks its rule is still the error, joined with a value of a subtype or not:
// the annotation decides the type, and each literal is checked against it.
// A generic call's arguments solve its parameters before the type its
// result is expected to have, so there the call's result is the mismatch,
// as it is for a numeric subtype.
func TestSubtypeMeet_AWrittenSubtypeStillChecksTheLiteral(t *testing.T) {
	const notPositive = "0 is not a PositiveInt: PositiveInt is a subtype of NonZeroInt, and NonZeroInt's rule `fn n -> n != 0` is false"
	for _, tc := range []struct{ body, want, code string }{
		{"    v: NonEmptyList<String> = if c then [] else xs\n    io.inspect(v)\n",
			"this NonEmptyList<String> literal breaks its rule: `fn list -> not Iter.is_empty(list)` is false", InvalidSubtypeCode},
		{"    v: NonEmptyList<String> = case c\n        True -> xs\n        False -> []\n    end\n    io.inspect(v)\n",
			"this NonEmptyList<String> literal breaks its rule: `fn list -> not Iter.is_empty(list)` is false", InvalidSubtypeCode},
		{"    v: List<PositiveInt> = [..ps, 0]\n    io.inspect(v)\n", notPositive, InvalidSubtypeCode},
		{"    v: List<PositiveInt> = [0, ..ps]\n    io.inspect(v)\n", notPositive, InvalidSubtypeCode},
		{"    v: Map<String, PositiveInt> = {..mps, \"b\" => 0}\n    io.inspect(v)\n", notPositive, InvalidSubtypeCode},
		{"    v: NonEmptyList<String> = Option.with_default(Some(xs), [])\n    io.inspect(v)\n",
			"type mismatch: expected NonEmptyList<String>, got List<String>", ""},
		{"    v: PositiveInt = pick(c, p, 0)\n    io.inspect(v)\n",
			"type mismatch: expected PositiveInt, got NonNegativeInt", ""},
	} {
		src := subtypeMeetSrc + "    io.inspect((c, xs, vs, ms, s, p, ps, mps, ints, mints, xss))\n" + tc.body + "end\n"
		ds := literalDiagnostics(t, src)
		if len(ds) != 1 || ds[0].Message != tc.want || ds[0].Code != tc.code {
			t.Errorf("%s: diagnostics = %v, want one reading %q with code %q", strings.TrimSpace(tc.body), ds, tc.want, tc.code)
		}
	}
}
