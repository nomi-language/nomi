package vmhost

import (
	"strings"
	"testing"
)

// TestSharedStdModuleTestsReachTheirOwnFunctions runs tests appended to
// std/hex, a module lowered once for the process, as `nomi test
// std/hex.nomi` runs an edited module: each calls one of the module's own
// functions bare, in an assertion subject, with named arguments and an
// omitted default, through a partial, as a function value and as an
// iteration callback (an iter-sensitive one too, and an Iter.loop step),
// and reads the module's own `once` of type Unit. Each must lower and pass,
// and a wrong assertion must fail.
func TestSharedStdModuleTestsReachTheirOwnFunctions(t *testing.T) {
	t.Setenv("NOMI_DEBUG_LOWERING", "1")
	const add3 = "fn add3(a: Int, b: Int, c: Int = 100) -> Int\n    a + b + c\nend\n\n"
	cases := []struct{ name, src string }{
		{"bare call in an assertion", "test \"matrix\"\n    assert encode(String.to_bytes(\"f\")) == \"66\"\nend\n"},
		{"named arguments and a default", add3 + "test \"matrix\"\n    assert add3(1, b: 2) == 103\nend\n"},
		{"named partial", add3 + "test \"matrix\"\n    g = &add3(1, b: &1)\n    assert g(2) == 103\nend\n"},
		{"function value", "fn twice(n: Int) -> Int\n    n * 2\nend\n\ntest \"matrix\"\n    f = twice\n    assert f(2) == 4\nend\n"},
		{"qualified function value", "fn twice(n: Int) -> Int\n    n * 2\nend\n\ntest \"matrix\"\n    f = hex.twice\n    assert f(2) == 4\nend\n"},
		{"callback", "test \"matrix\"\n    got = [String.to_bytes(\"f\")] |> Iter.map(encode) |> Iter.to_list()\n    assert got == [\"66\"]\nend\n"},
		{"iter-sensitive callback", "fn skip_two(n: Int) -> Int\n    if n == 2 then continue\n    n * 10\nend\n\n" +
			"test \"matrix\"\n    assert [1, 2, 3] |> Iter.map(skip_two) |> Iter.to_list() == [10, 30]\nend\n"},
		{"Iter.loop step", "fn count_up(n: Int) -> Int\n    if n == 1 then return 3\n    if n > 4 then break n * 10\n    n + 1\nend\n\n" +
			"test \"matrix\"\n    assert Iter.loop(0, count_up) == 50\nend\n"},
		{"function value in a body", "fn twice(n: Int) -> Int\n    n * 2\nend\n\nfn apply(n: Int) -> Int\n    f = twice\n    f(n)\nend\n\n" +
			"test \"matrix\"\n    assert apply(2) == 4\nend\n"},
		{"Unit once", "once once1: Unit = Unit\n\ntest \"matrix\"\n    assert Debug.inspect(once1) == \"Unit\"\nend\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			checkStdCaseRuns(t, "hex", c.src)
		})
	}
}

// TestProgramStdModuleDerivesJson runs a struct appended to std/presence,
// which imports `Result` but not its variants, deriving ToJson and FromJson:
// the derived bodies name `Ok` and `Err` bare, which the checker records as
// the prelude's variants (synthPreludeVariant), and the round trip runs.
func TestProgramStdModuleDerivesJson(t *testing.T) {
	t.Setenv("NOMI_DEBUG_LOWERING", "1")
	checkStdCaseRuns(t, "presence", "struct Jq\n    a: Int\nend\n\nderive Equatable for Jq\nderive ToJson for Jq\nderive FromJson for Jq\n\n"+
		"test \"matrix\"\n    assert FromJson.from_json<Jq>(ToJson.to_json(Jq{a: 2})) == Ok(Jq{a: 2})\nend\n")
}

// TestStdModuleVectorAndSetLiterals runs, in std/presence (lowered with each
// program) and std/hex (lowered once), neither of which imports `Vector` or
// `Set`, a `subtests` table whose rows are a Vector and a Set and a bounded
// generic function called at a Vector: the checker types a `[...]` or
// `[...]` literal as std's declaration whatever the file binds, so each
// has a kind.
func TestStdModuleVectorAndSetLiterals(t *testing.T) {
	t.Setenv("NOMI_DEBUG_LOWERING", "1")
	cells := []string{
		"test \"matrix\"\n    subtests [Vector[1, 2]], got\n        assert got == Vector[1, 2]\n    end\nend\n",
		"test \"matrix\"\n    subtests [Set[1, 2]], got\n        assert got == Set[1, 2]\n    end\nend\n",
		"fn has<C, T>(c: C, x: T) -> Bool where C: Contains<T>\n    C.contains(c, x)\nend\n\n" +
			"test \"matrix\"\n    assert has(Vector[1, 2], 2) == True\nend\n",
	}
	for _, module := range []string{"presence", "hex"} {
		for _, cell := range cells {
			checkStdCaseRuns(t, module, cell)
		}
	}
}

// TestProgramStdModuleDerivesComparable runs an enum appended to
// std/presence, which never names `Ordering`, deriving Comparable: the
// derived compare answers std's Ordering, and `<` and `Comparable.compare`
// over it run.
func TestProgramStdModuleDerivesComparable(t *testing.T) {
	t.Setenv("NOMI_DEBUG_LOWERING", "1")
	const coin = "enum Coin\n    Penny\n    Dime\nend\n\nderive Equatable for Coin\nderive Comparable for Coin\n\n"
	checkStdCaseRuns(t, "presence", coin+"test \"matrix\"\n    assert (Coin.Dime < Coin.Penny) == False\nend\n")
	checkStdCaseRuns(t, "presence", coin+"test \"matrix\"\n    o = Comparable.compare(Coin.Penny, Coin.Dime)\n    assert o == Ordering.Less\nend\n")
}

// checkStdCaseRuns appends cell, which declares a test named "matrix" whose
// last `==` is its assertion's, to std/<module> and requires the test to
// lower and pass, and to fail with that assertion negated.
func checkStdCaseRuns(t *testing.T, module, cell string) {
	t.Helper()
	src, from := stdEdited(module, cell)
	lo, k := checkStdLowers(module, src, from)
	if !lo.accepted {
		t.Fatalf("the front end rejects it: %s", lo.rejection)
	}
	if lo.internal != "" || len(lo.declines) > 0 {
		t.Fatalf("it does not lower: %s\n%v", lo.internal, lo.reasons)
	}
	if k.Err != nil {
		t.Fatalf("the test fails: %v\n%s", k.Err, k.Output)
	}
	at := strings.LastIndex(cell, " == ")
	wrong := cell[:at] + " != " + cell[at+len(" == "):]
	src, from = stdEdited(module, wrong)
	if _, k := checkStdLowers(module, src, from); k.Err == nil {
		t.Fatalf("the test passes with its assertion negated:\n%s", wrong)
	}
}
