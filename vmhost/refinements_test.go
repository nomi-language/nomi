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
    std::duration.{Duration, PositiveDuration}
    std::io
end

subtype Probability NonNegativeFloat when fn p -> p <= 1.0

subtype Port Int when fn port -> port >= 1 and port <= 65535

subtype Capped Float when fn x -> x < 0.9

subtype Half Probability and Capped when fn p -> p <= 0.5

struct Span
    low:  Int
    high: Int
    when fn span -> span.low <= span.high
end

subtype Pair List<Int> when fn xs -> List.length(xs) == 2

subtype Weights Map<String, Float> when fn m -> Map.size(m) > 0

subtype Many<T> List<T> when fn xs -> not Iter.is_empty(xs)

subtype Ascending<T> List<T> where T: Comparable when fn xs -> Iter.zip(xs, Iter.drop(xs, 1)) |> Iter.all_match(fn (a, b) -> a <= b)

subtype Run<T> Many<T> and Ascending<T> where T: Comparable when fn xs -> List.length(xs) <= 3

fn interval(d: PositiveDuration) -> Duration
    d
end

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
			"-0.5 is not a Probability: Probability is a subtype of NonNegativeFloat, and NonNegativeFloat's rule `fn x -> x >= 0.0` is false"},
		{"    n: PositiveInt = -3\n    io.inspect(n)\n",
			"-3 is not a PositiveInt: PositiveInt is a subtype of NonNegativeInt, and NonNegativeInt's rule `fn n -> n >= 0` is false"},
		{"    h: Half = -0.1\n    io.inspect(h)\n",
			"-0.1 is not a Half: Half is a subtype of Probability, which is a subtype of NonNegativeFloat, and NonNegativeFloat's rule `fn x -> x >= 0.0` is false"},
		{"    h: Half = 0.95\n    io.inspect(h)\n",
			"0.95 is not a Half: Half is a subtype of Capped, and Capped's rule `fn x -> x < 0.9` is false"},
		{"    h: Half = 0.7\n    io.inspect(h)\n",
			"0.7 is not a Half: `fn p -> p <= 0.5` is false"},
		{"    d: NonZeroDecimal = 0.00d\n    io.inspect(d)\n",
			"0.00d is not a NonZeroDecimal: `fn d -> d != 0d` is false"},
		{"    d: PositiveDecimal = -1.5d\n    io.inspect(d)\n",
			"-1.5d is not a PositiveDecimal: PositiveDecimal is a subtype of NonNegativeDecimal, and NonNegativeDecimal's rule `fn d -> d >= 0d` is false"},
		{"    s: NonEmptyString = \"\"\n    io.inspect(s)\n",
			"\"\" is not a NonEmptyString: `fn s -> s != \"\"` is false"},
		{"    t: PositiveDuration = Duration\"0s\"\n    io.inspect(t)\n",
			"Duration\"0s\" is not a PositiveDuration: `fn d -> Duration.as_nanos(d) > 0` is false"},
		{"    io.inspect(Int.pow(2, -1))\n",
			"-1 is not a NonNegativeInt: `fn n -> n >= 0` is false"},
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
		{"    m: Many<String> = []\n    io.inspect(m)\n",
			"this Many<String> literal breaks its rule: `fn xs -> not Iter.is_empty(xs)` is false"},
		{"    a: Ascending<Int> = [2, 1]\n    io.inspect(a)\n",
			"this Ascending<Int> literal breaks its rule: `fn xs -> Iter.zip(xs, Iter.drop(xs, 1)) |> Iter.all_match(fn (a, b) -> a <= b)` is false"},
		{"    r: Run<Int> = [3, 1]\n    io.inspect(r)\n",
			"this Run<Int> literal breaks the rule of Ascending<Int>, a supertype of it: `fn xs -> Iter.zip(xs, Iter.drop(xs, 1)) |> Iter.all_match(fn (a, b) -> a <= b)` is false"},
		{"    r: Run<Int> = [1, 2, 3, 4]\n    io.inspect(r)\n",
			"this Run<Int> literal breaks its rule: `fn xs -> List.length(xs) <= 3` is false"},
		{"    ms: List<Many<PositiveInt>> = [[1], [0]]\n    io.inspect(ms)\n",
			"0 is not a PositiveInt: PositiveInt is a subtype of NonZeroInt, and NonZeroInt's rule `fn n -> n != 0` is false"},
		{"    c = Port.try_from(1) == None\n    n: NonNegativeInt = if c then 1 else -1\n    io.inspect(n)\n",
			"-1 is not a NonNegativeInt: `fn n -> n >= 0` is false"},
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
		"subtype Noisy Int when loud\n" +
		"subtype Slow Int when spin\n" +
		"subtype Trapping Int when fn n -> 10 / n > 1\n\n"
	for _, tc := range []struct{ body, want string }{
		{"    io.inspect(Noisy.try_from(1))\n",
			"the rule of Noisy has effects (loud calls io.print): a rule is a pure function of the value it checks"},
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

// A generic refinement's rule is checked for effects at its declaration,
// as a non-generic one's is, whether or not any literal of it is checked
// or its `try_from` called: each is reported once, at its `when`, and a
// literal of it is not reported again. A pure one is no error.
func TestRefinement_AGenericRuleWithEffectsIsAnErrorAtItsDeclaration(t *testing.T) {
	const src = "import std::io\n\n" +
		"subtype Loud<T> List<T> when fn xs ->\n    io.print(\"checking\")\n    not Iter.is_empty(xs)\nend\n\n" +
		"subtype Sorted<T> List<T> where T: Comparable when fn xs ->\n    io.print(\"sorting\")\n    Iter.zip(xs, Iter.drop(xs, 1)) |> Iter.all_match(fn (a, b) -> a <= b)\nend\n\n" +
		"subtype Filled<K, V> Map<K, V> when fn m ->\n    io.print(\"filling\")\n    Map.size(m) > 0\nend\n\n" +
		"subtype Quiet<T> List<T> when fn xs -> not Iter.is_empty(xs)\n\n" +
		"subtype Over<T> Quiet<T> when fn xs ->\n    io.print(\"over\")\n    List.length(xs) > 1\nend\n\n"
	for _, tc := range []struct{ name, body string }{
		{"no literal or call", "    io.print(\"hi\")\n"},
		{"a literal of each", "    a: Loud<Int> = [1]\n    b: Over<Int> = [1, 2]\n    io.inspect((a, b))\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ds := literalDiagnostics(t, src+"fn main()\n"+tc.body+"end\n")
			want := []struct {
				line, col int
				msg       string
			}{
				{3, 25, "the rule of Loud has effects (lambda calls io.print): a rule is a pure function of the value it checks"},
				{8, 47, "the rule of Sorted has effects (lambda calls io.print): a rule is a pure function of the value it checks"},
				{13, 32, "the rule of Filled has effects (lambda calls io.print): a rule is a pure function of the value it checks"},
				{20, 26, "the rule of Over has effects (lambda calls io.print): a rule is a pure function of the value it checks"},
			}
			if len(ds) != len(want) {
				t.Fatalf("diagnostics = %v, want %d", ds, len(want))
			}
			for i, w := range want {
				if ds[i].Line != w.line || ds[i].Col != w.col || ds[i].Message != w.msg || ds[i].Code != InvalidRefinementCode {
					t.Errorf("diagnostic %d = %d:%d %q, want %d:%d %q", i, ds[i].Line, ds[i].Col, ds[i].Message, w.line, w.col, w.msg)
				}
			}
		})
	}
}

// A generic refinement's rule reaches, through its bounds, every impl of
// their interfaces the program has, each type of the program's that could
// be its argument: an effect in one is an error at the rule's `when` that
// names the impl, whether or not anything instantiates the refinement at
// that type. A rule whose bounds only a type of the program's own meets is
// checked as one a primitive meets is.
func TestRefinement_AGenericRuleIsCheckedThroughEveryImplOfItsBounds(t *testing.T) {
	const decls = "import std::io\n\n" +
		"interface Named\n    fn name(n: self) -> String\nend\n\n" +
		"struct Pet\n    called: String\nend\n\n" +
		"impl Named for Pet\n    fn name(p: Pet) -> String\n        p.called\n    end\nend\n\n"
	const loud = "struct Loud\n    n: Int\nend\n\n" +
		"impl Display for Loud\n    fn to_string(l: Loud) -> String\n        io.print(\"shown\")\n        \"loud ${l.n}\"\n    end\nend\n\n" +
		"impl Comparable for Loud\n    fn compare(a: Loud, b: Loud) -> Ordering\n        io.print(\"compared\")\n        Int.compare(a.n, b.n)\n    end\nend\n\n" +
		"impl Named for Loud\n    fn name(l: Loud) -> String\n        io.print(\"named\")\n        \"${l.n}\"\n    end\nend\n\n"
	const box = "struct Box<T>\n    v: T\nend\n\n" +
		"impl<T> Display for Box<T> where T: Display\n    fn to_string(b: Box<T>) -> String\n        io.print(\"boxed\")\n        \"box ${b.v}\"\n    end\nend\n\n"
	const rules = "subtype Shown<T> List<T> where T: Display when fn xs -> Iter.all_match(xs, fn x -> Display.to_string(x) != \"\")\n\n" +
		"subtype Sorted<T> List<T> where T: Comparable when fn xs -> Iter.zip(xs, Iter.drop(xs, 1)) |> Iter.all_match(fn (a, b) -> a <= b)\n\n" +
		"subtype Tagged<T> List<T> where T: Named when fn xs -> Iter.all_match(xs, fn x -> Named.name(x) != \"\")\n\n"
	const main = "fn main()\n    io.print(\"hi\")\nend\n"
	msg := func(name, effect string) string {
		return "the rule of " + name + " has effects (" + effect + "): a rule is a pure function of the value it checks"
	}
	type want struct {
		line, col int
		msg       string
	}
	for _, tc := range []struct {
		name, src string
		want      []want
	}{
		{"an impl of a type the program declares", decls + loud + rules + main, []want{
			{42, 43, msg("Shown", "Loud.to_string calls io.print")},
			{44, 47, msg("Sorted", "Loud.compare calls io.print")},
			{46, 42, msg("Tagged", "Loud.name calls io.print")},
		}},
		{"an instance of a generic impl", decls + box + rules + "fn main()\n    io.print(Box{v: 1})\nend\n", []want{
			{28, 43, msg("Shown", "Display.to_string calls io.print")},
		}},
		{"a rule only the program's own type meets the bounds of", decls +
			"subtype Listed<T> List<T> where T: Named when fn xs ->\n    io.print(\"listing\")\n    Iter.all_match(xs, fn x -> Named.name(x) != \"\")\nend\n\n" + main, []want{
			{17, 42, msg("Listed", "lambda calls io.print")},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ds := literalDiagnostics(t, tc.src)
			if len(ds) != len(tc.want) {
				t.Fatalf("diagnostics = %v, want %d", ds, len(tc.want))
			}
			for i, w := range tc.want {
				if ds[i].Line != w.line || ds[i].Col != w.col || ds[i].Message != w.msg || ds[i].Code != InvalidRefinementCode {
					t.Errorf("diagnostic %d = %d:%d %q, want %d:%d %q", i, ds[i].Line, ds[i].Col, ds[i].Message, w.line, w.col, w.msg)
				}
			}
		})
	}
}

// A generic rule over bounds whose every impl in the program is pure is no
// error, at its declaration or at the program's uses of it.
func TestRefinement_AGenericRuleOverPureImplsIsAccepted(t *testing.T) {
	p, err := LoadSource("main", "import std::io\n\n"+
		"struct Quiet\n    n: Int\nend\n\n"+
		"impl Display for Quiet\n    fn to_string(q: Quiet) -> String\n        \"quiet ${q.n}\"\n    end\nend\n\n"+
		"impl Comparable for Quiet\n    fn compare(a: Quiet, b: Quiet) -> Ordering\n        Int.compare(a.n, b.n)\n    end\nend\n\n"+
		"subtype Shown<T> List<T> where T: Display when fn xs -> Iter.all_match(xs, fn x -> Display.to_string(x) != \"\")\n\n"+
		"subtype Sorted<T> List<T> where T: Comparable when fn xs -> Iter.zip(xs, Iter.drop(xs, 1)) |> Iter.all_match(fn (a, b) -> a <= b)\n\n"+
		"fn main()\n"+
		"    io.inspect(Shown.try_from([Quiet{n: 1}]) |> Maybe.is_some())\n"+
		"    io.inspect(Sorted.try_from([Quiet{n: 2}, Quiet{n: 1}]) |> Maybe.is_some())\n"+
		"    s: Sorted<Int> = [1, 2]\n"+
		"    io.inspect(s)\n"+
		"end\n")
	if err != nil {
		t.Fatalf("the program does not load: %v", err)
	}
	var out strings.Builder
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got, want := out.String(), "True\nFalse\n[1, 2]\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
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

// A join of a refinement and an ancestor of it is that ancestor, and a
// literal in a later branch or element is its base's, not the earlier
// branch's refinement's (spec §4 *Chains and conversion*).
func TestRefinement_JoinsRunAsTheSharedAncestor(t *testing.T) {
	p, err := LoadSource("main", refinementSrc+`fn main()
    xs = [1, 2, 3]
    c = List.length(xs) > 5
    n = if c then List.length(xs) else -1
    io.inspect(n + 1)
    io.inspect([List.length(xs), -1])
    io.inspect(if c then Some(List.length(xs)) else Some(-2))
    io.inspect([Some(List.length(xs)), Some(-3), None])
    io.inspect({"a" => List.length(xs), "b" => -4})
end
`)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatal(err)
	}
	const want = "0\n[3, -1]\nSome(-2)\n[Some(3), Some(-3), None]\n{\"a\" => 3, \"b\" => -4}\n"
	if out.String() != want {
		t.Errorf("output = %q, want %q", out.String(), want)
	}
}

// TestRefinement_ARuleThatTrapsIsFalseWhenTheProgramRuns: `try_from` is
// None for a value whose rule traps, up the chain too, and the program goes
// on; a trap outside a rule still stops it.
func TestRefinement_ARuleThatTrapsIsFalseWhenTheProgramRuns(t *testing.T) {
	p, err := LoadSource("main", `import std::io

subtype Ratio Int when fn n -> 10 / n > 1

subtype SmallRatio Ratio when fn n -> n < 5

struct Fraction
    top:    Int
    bottom: Int
    when fn f -> f.top / f.bottom < 1
end

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
