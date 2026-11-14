package vmhost

import (
	"context"
	"strings"
	"testing"
)

// Refinement types at load and at run time (refinements.go): a literal is
// checked against each rule of its chain before anything runs, a rule with
// effects is an error at its declaration, and `try_from` checks a value
// when the program runs.

const refinementSrc = `import
    std::float.NonNegativeFloat
    std::io
    std::int.PositiveInt
end

type Probability NonNegativeFloat when fn p -> p <= 1.0

type Port Int when fn port -> port >= 1 and port <= 65535

struct Span
    low:  Int
    high: Int
end when fn span -> span.low <= span.high

type Pair List<Int> when fn xs -> List.length(xs) == 2

type Weights Map<String, Float> when fn m -> Map.size(m) > 0

fn chance(p: Probability) -> Float
    p * 100.0
end

fn per_slot(total: Int, slots: PositiveInt) -> Int
    total / slots
end

`

func TestRefinement_LiteralsAreCheckedWhenTheProgramIsCompiled(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{"    p: Port = 0\n    io.inspect(p)\n",
			"0 is not a Port: `fn port -> port >= 1 and port <= 65535` is false"},
		{"    io.inspect(chance(1.5))\n",
			"1.5 is not a Probability: `fn p -> p <= 1.0` is false"},
		{"    io.inspect(chance(-0.5))\n",
			"-0.5 is not a Probability: Probability refines NonNegativeFloat, and NonNegativeFloat's rule `fn x -> x >= 0.0` is false"},
		{"    n: PositiveInt = -3\n    io.inspect(n)\n",
			"-3 is not a PositiveInt: `fn n -> n > 0` is false"},
		{"    io.inspect(Port(70000))\n",
			"70000 is not a Port: `fn port -> port >= 1 and port <= 65535` is false"},
		{"    io.inspect(Span{low: 5, high: 1})\n",
			"this Span literal breaks its rule: `fn span -> span.low <= span.high` is false"},
		{"    io.inspect(Float.equal_within(1.0, 1.0, -1.0))\n",
			"-1.0 is not a NonNegativeFloat: `fn x -> x >= 0.0` is false"},
		{"    p: Pair = [1, 2, 3]\n    io.inspect(p)\n",
			"this Pair literal breaks its rule: `fn xs -> List.length(xs) == 2` is false"},
		{"    ps: List<Pair> = [[1, 2], [3]]\n    io.inspect(ps)\n",
			"this Pair literal breaks its rule: `fn xs -> List.length(xs) == 2` is false"},
		{"    w: Maybe<Weights> = Some({})\n    io.inspect(w)\n",
			"this Weights literal breaks its rule: `fn m -> Map.size(m) > 0` is false"},
	} {
		ds := literalDiagnostics(t, refinementSrc+"fn main()\n"+tc.body+"end\n")
		if len(ds) != 1 || ds[0].Message != tc.want || ds[0].Code != InvalidRefinementCode {
			t.Errorf("%s: diagnostics = %v, want one reading %q", strings.TrimSpace(tc.body), ds, tc.want)
		}
	}
}

func TestRefinement_ARuleThatCannotBeEvaluated(t *testing.T) {
	const decls = "import std::io\n\n" +
		"fn loud(n: Int) -> Bool\n    io.print(\"checking\")\n    n > 0\nend\n\n" +
		"fn spin(n: Int) -> Bool\n    if n > 100000000 then True else spin(n + 1)\nend\n\n" +
		"type Noisy Int when loud\n" +
		"type Slow Int when spin\n" +
		"type Trapping Int when fn n -> 10 / n > 1\n\n"
	for _, tc := range []struct{ body, want string }{
		{"    io.inspect(Noisy.try_from(1))\n",
			"the rule of the refinement Noisy has effects (loud calls io.print): a rule is a pure function of the value it checks"},
		{"    s: Slow = 1\n    io.inspect(s)\n",
			"1 cannot be checked as a Slow when the program is compiled: its rule `spin` did not finish within 1000000 steps"},
		{"    t: Trapping = 0\n    io.inspect(t)\n",
			"0 is not a Trapping: Trapping's rule `fn n -> 10 / n > 1` failed: "},
	} {
		ds := literalDiagnostics(t, decls+"fn main()\n"+tc.body+"end\n")
		found := false
		for _, d := range ds {
			if strings.HasPrefix(d.Message, tc.want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: diagnostics = %v, want one reading %q", strings.TrimSpace(tc.body), ds, tc.want)
		}
	}
}

func TestRefinement_TryFromChecksTheChainWhenTheProgramRuns(t *testing.T) {
	p, err := LoadSource("main", refinementSrc+`fn main()
    io.inspect(Probability.try_from(0.25))
    io.inspect(Probability.try_from(1.25))
    io.inspect(Probability.try_from(-0.25))
    io.inspect(Port.try_from(8080))
    io.inspect(PositiveInt.try_from(0))
    io.inspect(per_slot(10, 3))
    io.inspect(Span.try_from({low: 2, high: 1}))
    io.inspect(Span.try_from({low: 1, high: 2}))
    q: Probability = 0.5
    io.inspect(chance(q))
    xs: List<Probability> = [0.1, 0.2]
    io.inspect(Iter.sum(xs))
    pair: Pair = [1, 2]
    io.inspect(pair)
    weights: Weights = {"a" => 0.5}
    io.inspect(weights)
end
`)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatal(err)
	}
	const want = "Some(0.25)\nNone\nNone\nSome(8080)\nNone\n3\nNone\nSome(Span{low: 1, high: 2})\n50.0\n0.30000000000000004\n[1, 2]\n{\"a\" => 0.5}\n"
	if out.String() != want {
		t.Errorf("output = %q, want %q", out.String(), want)
	}
}

// TestRefinement_ARuleThatTrapsIsFalseWhenTheProgramRuns: `try_from` is
// None for a value whose rule traps, up the chain too, and the program goes
// on; a trap outside a rule still stops it.
func TestRefinement_ARuleThatTrapsIsFalseWhenTheProgramRuns(t *testing.T) {
	p, err := LoadSource("main", `import std::io

type Ratio Int when fn n -> 10 / n > 1

type SmallRatio Ratio when fn n -> n < 5

struct Fraction
    top:    Int
    bottom: Int
end when fn f -> f.top / f.bottom < 1

fn main()
    io.inspect(Ratio.try_from(0))
    io.inspect(Ratio.try_from(2))
    io.inspect(SmallRatio.try_from(0))
    io.inspect(SmallRatio.try_from(3))
    io.inspect(Fraction.try_from({top: 1, bottom: 0}))
    zero = Iter.sum([0])
    io.inspect(10 / zero)
end
`)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	err = p.Run(context.Background(), &out, nil, false)
	const want = "None\nSome(2)\nNone\nSome(3)\nNone\n"
	if out.String() != want {
		t.Errorf("output = %q, want %q", out.String(), want)
	}
	if err == nil || !strings.Contains(err.Error(), "div") {
		t.Errorf("the division outside a rule = %v, want it to stop the program", err)
	}
}
