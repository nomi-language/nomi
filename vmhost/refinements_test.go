package vmhost

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/irbuild"
)

// Refinement types at load and at run time (refinements.go): a literal is
// checked against each rule of its chain before anything runs, a rule with
// effects is an error at its declaration, and `try_from` checks a value
// when the program runs.

const refinementSrc = `import
    std::duration.{Duration, NonNegativeDuration, PositiveDuration}
    std::io
end

refine Probability NonNegativeFloat when fn p -> p <= 1.0

refine Port Int when fn port -> port >= 1 and port <= 65535

refine Capped Float when fn x -> x < 0.9

refine Half Probability and Capped when fn p -> p <= 0.5

struct Span
    low:  Int
    high: Int
    when fn span -> span.low <= span.high
end

refine Pair List<Int> when fn xs -> List.length(xs) == 2

refine Weights Map<String, Float> when fn m -> Map.size(m) > 0

refine Many<T> List<T> when fn xs -> not Iter.is_empty(xs)

refine Ascending<T> List<T> where T: Comparable when fn xs -> Iter.zip(xs, Iter.drop(xs, 1)) |> Iter.all_match(fn (a, b) -> a <= b)

refine Run<T> Many<T> and Ascending<T> where T: Comparable when fn xs -> List.length(xs) <= 3

fn interval(d: PositiveDuration) -> Duration
    d
end

fn wait(d: NonNegativeDuration) -> Duration
    d
end

fn chance(p: Probability) -> Float
    p * 100.0
end

fn per_slot(total: Int, slots: PositiveInt) -> Int
    total / slots
end

refine Wide Range<Int> when fn r -> Range.contains(r, 0) and Range.contains(r, 5)

fn span(r: Wide) -> Int
    Iter.count(r)
end

`

func TestRefinement_LiteralsAreCheckedWhenTheProgramIsCompiled(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{"    p: Port = 0\n    io.inspect(p)\n",
			"0 is not a Port: `fn port -> port >= 1 and port <= 65535` is false"},
		{"    io.inspect(chance(1.5))\n",
			"1.5 is not a Probability: `fn p -> p <= 1.0` is false"},
		{"    io.inspect(chance(-0.5))\n",
			"-0.5 is not a Probability: Probability is a refinement of NonNegativeFloat, and NonNegativeFloat's rule `fn x -> x >= 0.0` is false"},
		{"    n: PositiveInt = -3\n    io.inspect(n)\n",
			"-3 is not a PositiveInt: PositiveInt is a refinement of NonNegativeInt, and NonNegativeInt's rule `fn n -> n >= 0` is false"},
		{"    h: Half = -0.1\n    io.inspect(h)\n",
			"-0.1 is not a Half: Half is a refinement of Probability, which is a refinement of NonNegativeFloat, and NonNegativeFloat's rule `fn x -> x >= 0.0` is false"},
		{"    h: Half = 0.95\n    io.inspect(h)\n",
			"0.95 is not a Half: Half is a refinement of Capped, and Capped's rule `fn x -> x < 0.9` is false"},
		{"    h: Half = 0.7\n    io.inspect(h)\n",
			"0.7 is not a Half: `fn p -> p <= 0.5` is false"},
		{"    d: NonZeroDecimal = 0.00d\n    io.inspect(d)\n",
			"0.00d is not a NonZeroDecimal: `fn d -> d != 0d` is false"},
		{"    d: PositiveDecimal = -1.5d\n    io.inspect(d)\n",
			"-1.5d is not a PositiveDecimal: PositiveDecimal is a refinement of NonNegativeDecimal, and NonNegativeDecimal's rule `fn d -> d >= 0d` is false"},
		{"    s: NonEmptyString = \"\"\n    io.inspect(s)\n",
			"\"\" is not a NonEmptyString: `fn s -> s != \"\"` is false"},
		{"    t: PositiveDuration = Duration\"0s\"\n    io.inspect(t)\n",
			"Duration\"0s\" is not a PositiveDuration: PositiveDuration is a refinement of NonZeroDuration, and NonZeroDuration's rule `fn d -> Duration.as_nanos(d) != 0` is false"},
		{"    io.inspect(wait(Duration\"-1s\"))\n",
			"Duration\"-1s\" is not a NonNegativeDuration: `fn d -> Duration.as_nanos(d) >= 0` is false"},
		{"    io.inspect(String.repeat(\"ab\", -2))\n",
			"-2 is not a NonNegativeInt: `fn n -> n >= 0` is false"},
		{"    io.inspect(span(1..3))\n",
			"this Wide literal breaks its rule: `fn r -> Range.contains(r, 0) and Range.contains(r, 5)` is false"},
		{"    io.inspect(Iter.chunks([1, 2], 0) |> Iter.to_list())\n",
			"0 is not a PositiveInt: PositiveInt is a refinement of NonZeroInt, and NonZeroInt's rule `fn n -> n != 0` is false"},
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
		{"    w: Option<Weights> = Some({})\n    io.inspect(w)\n",
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
			"0 is not a PositiveInt: PositiveInt is a refinement of NonZeroInt, and NonZeroInt's rule `fn n -> n != 0` is false"},
		{"    c = Port.try_from(1) == None\n    n: NonNegativeInt = if c then 1 else -1\n    io.inspect(n)\n",
			"-1 is not a NonNegativeInt: `fn n -> n >= 0` is false"},
	} {
		ds := literalDiagnostics(t, refinementSrc+"fn main()\n"+tc.body+"end\n")
		if len(ds) != 1 || ds[0].Message != tc.want || ds[0].Code != InvalidRefinementCode {
			t.Errorf("%s: diagnostics = %v, want one reading %q", strings.TrimSpace(tc.body), ds, tc.want)
		}
	}
}

// The prelude's range refinements check a range literal's bounds against each
// other when the program is compiled: their rules read the range's parts
// (Range.start, Range.stop, Range.is_inclusive), which lower for any
// Comparable element, so clamp's ClosedRange and random's BoundedRange are
// checked at every element type they take.
func TestRefinement_RangeRefinementsCheckBoundsThatMustAgree(t *testing.T) {
	nonEmpty := func(elem string) string {
		return "breaks the rule of NonEmptyRange<" + elem + ">, a supertype of it: `fn r -> Range.contains(r, Range.start(r))` is false"
	}
	for _, tc := range []struct{ src, want string }{
		{"fn main()\n    dbg Int.clamp(5, 10..=1)\nend\n",
			"this ClosedRange<Int> literal " + nonEmpty("Int")},
		{"fn main()\n    dbg Comparable.clamp(5, 10..=1)\nend\n",
			"this ClosedRange<Int> literal " + nonEmpty("Int")},
		{"fn main()\n    dbg String.clamp(\"b\", \"z\"..=\"a\")\nend\n",
			"this ClosedRange<String> literal " + nonEmpty("String")},
		{"fn main()\n    dbg Float.clamp(0.5, 0.0..1.0)\nend\n",
			"this ClosedRange<Float> literal breaks its rule: `fn r -> Range.is_inclusive(r) or not Range.is_bounded(r)` is false"},
		{"import std::random.Generator\n\nfn main()\n    _ = Generator.int(6..=1)\nend\n",
			"this BoundedRange<Int> literal " + nonEmpty("Int")},
		{"import std::random.Generator\n\nfn main()\n    _ = Generator.int(4..4)\nend\n",
			"this BoundedRange<Int> literal " + nonEmpty("Int")},
		{"import std::random.Generator\n\nfn main()\n    _ = Generator.float(2.5..1.5)\nend\n",
			"this FiniteRange literal " + nonEmpty("Float")},
		{"fn main()\n    r: NonEmptyRange<Int> = 3..3\n    dbg r\nend\n",
			"this NonEmptyRange<Int> literal breaks its rule: `fn r -> Range.contains(r, Range.start(r))` is false"},
	} {
		ds := literalDiagnostics(t, tc.src)
		if len(ds) != 1 || ds[0].Message != tc.want || ds[0].Code != InvalidRefinementCode {
			t.Errorf("diagnostics = %v, want one reading %q\n%s", ds, tc.want, tc.src)
		}
	}
}

// A range's parts read the same for every element type, a range computed
// when the program runs goes through try_from, and clamp and random take
// the range refinements: an unbounded range clamps only below, and a range
// with a NaN end is no ClosedRange.
func TestRefinement_RangeRefinementsRunOverTheirParts(t *testing.T) {
	p, err := LoadSource("main", `import
    std::io
    std::random.{Generator, Seed}
end

struct Version
    major: Int
end

derive Comparable for Version

fn main()
    io.inspect((Range.start(2..5), Range.stop(2..5), Range.is_inclusive(2..5)))
    io.inspect((Range.start("a"..="c"), Range.stop(Range.from(4)), Range.is_inclusive(Range.from(4))))
    v1 = Version{major: 1}
    v3 = Version{major: 3}
    io.inspect(Range.stop(v1..=v3))
    io.inspect(Version.clamp(Version{major: 9}, ClosedRange.try_from(v1..=v3)!))
    io.inspect(ClosedRange.try_from(v3..=v1))
    io.inspect((Int.clamp(-4, Range.from(0)), Int.clamp(40, Range.from(0)), Int.clamp(40, 0..=11)))
    io.inspect(ClosedRange.try_from(0.0..=Float.nan()))
    io.inspect(BoundedRange.try_from(0.0..1.0))
    io.inspect(BoundedRange.try_from(Range.from(1)))
    n = 3
    io.inspect(BoundedRange.try_from(n..n))
    (x, _) = Generator.step(Generator.int(BoundedRange.try_from(n..=n)!), Seed.from_int(1))
    io.inspect(x)
    io.inspect(Iter.map([1..=2, 5..9], Range.start) |> Iter.to_list())
end
`)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatal(err)
	}
	const want = "(2, Some(5), False)\n(\"a\", None, False)\nSome(Version{major: 3})\nVersion{major: 3}\nNone\n(0, 40, 11)\nNone\nSome(0.0..1.0)\nNone\nNone\n3\n[1, 5]\n"
	if out.String() != want {
		t.Errorf("output = %q, want %q", out.String(), want)
	}
}

// A refinement a listed std module declares (std::process's ExitCode) is lowered
// with the listed modules, which name its `try_from` by its impl block, as a
// program's own are (irbuild's implFuncLink): its literals are checked as a
// shared module's are, in a field and an argument alike.
func TestRefinement_AListedStdModulesRefinementChecksItsLiterals(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"import std::process.Exit\n\nfn main() -> Result<Unit, Exit>\n    Err(Exit{code: 300})\nend\n",
			"300 is not an ExitCode: `fn code -> code >= 0 and code <= 255` is false"},
		{"import std::process.ExitCode\n\nfn main()\n    c: ExitCode = -1\n    dbg c\nend\n",
			"-1 is not an ExitCode: `fn code -> code >= 0 and code <= 255` is false"},
		{"import std::tasks.Task\n\nfn main()\n    xs = concurrent\n        Task.spawn_all([1], &(&1 * 2), max_running: 0) |> Task.await_all()\n    end\n    dbg xs\nend\n",
			"0 is not a PositiveInt: PositiveInt is a refinement of NonZeroInt, and NonZeroInt's rule `fn n -> n != 0` is false"},
	} {
		ds := literalDiagnostics(t, tc.src)
		if len(ds) != 1 || ds[0].Message != tc.want || ds[0].Code != InvalidRefinementCode {
			t.Errorf("diagnostics = %v, want one reading %q\n%s", ds, tc.want, tc.src)
		}
	}
	if _, err := LoadSource("main", "import std::process.Exit\n\nfn main() -> Result<Unit, Exit>\n    Err(Exit{code: 255})\nend\n"); err != nil {
		t.Errorf("Exit{code: 255} does not load: %v", err)
	}
}

// A literal of a generic refinement another file declares is checked through
// that declaration's `try_from`, whatever the literal's file names: a file
// that imports only a function taking the refinement checks its literal
// arguments, an import of the refinement's name alone for them is unused, and
// a file declaring its own type of the same name still checks against the
// other file's rule.
func TestRefinement_AGenericRefinementsLiteralNeedsNoNameInScope(t *testing.T) {
	const lib = "pub refine Many<T> List<T> when fn xs -> List.length(xs) > 1\n\n" +
		"pub refine Asc<T> List<T> where T: Comparable when fn xs -> Iter.zip(xs, Iter.drop(xs, 1)) |> Iter.all_match(fn (a, b) -> a <= b)\n\n" +
		"pub fn first(xs: Many<Int>) -> Int\n    Iter.first(xs)!\nend\n\n" +
		"pub fn top<T>(xs: Asc<T>) -> Option<T> where T: Comparable\n    Iter.last(xs)\nend\n"
	got := runProject(t, map[string]string{"lib.nomi": lib, "main.nomi": "import\n    std::io\n    lib.{first, top}\nend\n\n" +
		"fn main()\n    io.inspect(first([1, 2]))\n    io.inspect(top([1, 3]))\nend\n"})
	if want := "1\nSome(3)\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
	for _, tc := range []struct{ name, main, want string }{
		{"a broken rule", "import lib.first\n\nfn main()\n    dbg first([1])\nend\n",
			"this Many<Int> literal breaks its rule: `fn xs -> List.length(xs) > 1` is false"},
		{"a bounded rule", "import lib.top\n\nfn main()\n    dbg top([3, 1])\nend\n",
			"this Asc<Int> literal breaks its rule: `fn xs -> Iter.zip(xs, Iter.drop(xs, 1)) |> Iter.all_match(fn (a, b) -> a <= b)` is false"},
		{"a name of its own", "import lib.first\n\nrefine Many<T> List<T> when fn xs -> List.length(xs) > 5\n\n" +
			"fn main()\n    m: Many<Int> = [1, 2, 3, 4, 5, 6]\n    dbg m\n    dbg first([1])\nend\n",
			"this Many<Int> literal breaks its rule: `fn xs -> List.length(xs) > 1` is false"},
		{"an import used by nothing", "import lib.{Many, first}\n\nfn main()\n    dbg first([1, 2])\nend\n",
			"imported name 'Many' is unused — remove it from the import"},
		{"a broken bound", "import lib.Asc\n\nstruct Circle\n    r: Int\nend\n\nfn main()\n    u: Asc<Circle> = []\n    dbg u\nend\n",
			"Circle does not implement Comparable (required by `where T: Comparable`)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, src := range map[string]string{"lib.nomi": lib, "main.nomi": tc.main} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			_, err := Load(filepath.Join(dir, "main.nomi"))
			if err == nil {
				t.Fatalf("the program loads; want %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("load error = %v, want one reading %q", err, tc.want)
			}
		})
	}
}

// A literal of another file's type is checked by that type's own rule in a
// file that declares a type of the same name with another rule: a generic
// refinement passed to a function, named through an alias, or written in a
// test block, and a validated type, generic or not, built through an
// alias. Each main file's own type has a rule the literal breaks where the
// other file's holds, or holds where the other file's breaks, so a check
// run through the local type's `try_from` fails the case.
func TestRefinement_ALiteralKeepsItsTypesRuleBesideALocalTypeOfItsName(t *testing.T) {
	const many = "pub refine Many<T> List<T> when fn xs -> List.length(xs) > 1\n\n" +
		"pub fn first(xs: Many<Int>) -> Int\n    Iter.first(xs)!\nend\n"
	const code = "pub type Code Int when fn n -> n > 1\n\npub fn f(c: Code) -> Code\n    c\nend\n"
	const pair = "pub type Pair<T> List<T> when fn xs -> List.length(xs) == 2\n\n" +
		"pub fn f(p: Pair<Int>) -> Pair<Int>\n    p\nend\n"
	for _, tc := range []struct{ name, lib, main, out, err string }{
		{"a generic refinement", many,
			"import lib.first\n\nrefine Many<T> List<T> when fn xs -> List.length(xs) > 5\n\n" +
				"fn main()\n    m: Many<Int> = [1, 2, 3, 4, 5, 6]\n    dbg m\n    dbg first([1, 2])\nend\n",
			"dbg line 7: m = [1, 2, 3, 4, 5, 6]\ndbg line 8: first([1, 2]) = 1\n", ""},
		{"a generic refinement it breaks", many,
			"import lib.first\n\nrefine Many<T> List<T> when fn xs -> List.length(xs) > 0\n\nfn main()\n    dbg first([1])\nend\n",
			"", "this Many<Int> literal breaks its rule: `fn xs -> List.length(xs) > 1` is false"},
		{"a generic refinement under an alias", many,
			"import lib.Many as M\n\nrefine Many<T> List<T> when fn xs -> List.length(xs) > 5\n\n" +
				"fn main()\n    k: M<Int> = [1, 2]\n    dbg k\nend\n",
			"dbg line 7: k = [1, 2]\n", ""},
		{"a generic refinement under an alias it breaks", many,
			"import lib.Many as M\n\nrefine Many<T> List<T> when fn xs -> List.length(xs) > 0\n\n" +
				"fn main()\n    k: M<Int> = [1]\n    dbg k\nend\n",
			"", "this Many<Int> literal breaks its rule: `fn xs -> List.length(xs) > 1` is false"},
		{"a generic refinement in a test", many,
			"import lib.first\n\nrefine Many<T> List<T> when fn xs -> List.length(xs) > 0\n\n" +
				"test \"first\"\n    assert first([1]) == 1\nend\n",
			"", "this Many<Int> literal breaks its rule: `fn xs -> List.length(xs) > 1` is false"},
		{"a validated type", code,
			"import lib.{Code as C, f}\n\ntype Code Int when fn n -> n > 5\n\nfn main()\n    dbg f(C(2))\nend\n",
			"dbg line 6: f(C(2)) = Code(2)\n", ""},
		{"a validated type it breaks", code,
			"import lib.{Code as C, f}\n\ntype Code Int when fn n -> n > 0\n\nfn main()\n    dbg f(C(1))\nend\n",
			"", "1 is not a Code: `fn n -> n > 1` is false"},
		{"a generic validated type", pair,
			"import lib.{Pair as P, f}\n\ntype Pair<T> List<T> when fn xs -> List.length(xs) == 3\n\n" +
				"fn main()\n    q: P<Int> = P([1, 2])\n    dbg f(q)\nend\n",
			"dbg line 7: f(q) = Pair([1, 2])\n", ""},
		{"a generic validated type it breaks", pair,
			"import lib.{Pair as P, f}\n\ntype Pair<T> List<T> when fn xs -> List.length(xs) > 0\n\n" +
				"fn main()\n    q: P<Int> = P([1])\n    dbg f(q)\nend\n",
			"", "this Pair<Int> literal breaks its rule: `fn xs -> List.length(xs) == 2` is false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{"lib.nomi": tc.lib, "main.nomi": tc.main}
			if tc.err == "" {
				if got := runProject(t, files); got != tc.out {
					t.Errorf("output = %q, want %q", got, tc.out)
				}
				return
			}
			dir := t.TempDir()
			for name, src := range files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			_, err := Load(filepath.Join(dir, "main.nomi"))
			if err == nil {
				t.Fatalf("the program loads; want %q", tc.err)
			}
			if !strings.Contains(err.Error(), tc.err) {
				t.Errorf("load error = %v, want one reading %q", err, tc.err)
			}
		})
	}
}

func TestRefinement_ARuleThatCannotBeEvaluated(t *testing.T) {
	const decls = "import std::io\n\n" +
		"fn loud(n: Int) -> Bool\n    io.print(\"checking\")\n    n > 0\nend\n\n" +
		"fn spin(n: Int) -> Bool\n    if n > 100000000 then True else spin(n + 1)\nend\n\n" +
		"refine Noisy Int when loud\n" +
		"refine Slow Int when spin\n" +
		"refine Trapping Int when fn n -> 10 / NonZeroInt.try_from(n)! > 1\n" +
		"refine Padded String when fn s -> String.length(String.repeat(s, 2)) < 10\n\n"
	for _, tc := range []struct{ body, want string }{
		{"    io.inspect(Noisy.try_from(1))\n",
			"the rule of Noisy has effects (loud calls io.print): a rule is a pure function of the value it checks"},
		{"    p: Padded = \"ab\"\n    io.inspect(p)\n",
			"the rule of Padded calls a function whose cost grows with its argument (lambda calls strings.String.repeat): a rule must be bounded"},
		{"    s: Slow = 1\n    io.inspect(s)\n",
			"1 cannot be checked as a Slow when the program is compiled: its rule `spin` did not finish within 1000000 steps"},
		{"    t: Trapping = 0\n    io.inspect(t)\n",
			"0 is not a Trapping: Trapping's rule `fn n -> 10 / NonZeroInt.try_from(n)! > 1` failed: "},
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
		"refine Loud<T> List<T> when fn xs ->\n    io.print(\"checking\")\n    not Iter.is_empty(xs)\nend\n\n" +
		"refine Sorted<T> List<T> where T: Comparable when fn xs ->\n    io.print(\"sorting\")\n    Iter.zip(xs, Iter.drop(xs, 1)) |> Iter.all_match(fn (a, b) -> a <= b)\nend\n\n" +
		"refine Filled<K, V> Map<K, V> when fn m ->\n    io.print(\"filling\")\n    Map.size(m) > 0\nend\n\n" +
		"refine Quiet<T> List<T> when fn xs -> not Iter.is_empty(xs)\n\n" +
		"refine Over<T> Quiet<T> when fn xs ->\n    io.print(\"over\")\n    List.length(xs) > 1\nend\n\n"
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
				{3, 24, "the rule of Loud has effects (lambda calls io.print): a rule is a pure function of the value it checks"},
				{8, 46, "the rule of Sorted has effects (lambda calls io.print): a rule is a pure function of the value it checks"},
				{13, 31, "the rule of Filled has effects (lambda calls io.print): a rule is a pure function of the value it checks"},
				{20, 25, "the rule of Over has effects (lambda calls io.print): a rule is a pure function of the value it checks"},
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
	const rules = "refine Shown<T> List<T> where T: Display when fn xs -> Iter.all_match(xs, fn x -> Display.to_string(x) != \"\")\n\n" +
		"refine Sorted<T> List<T> where T: Comparable when fn xs -> Iter.zip(xs, Iter.drop(xs, 1)) |> Iter.all_match(fn (a, b) -> a <= b)\n\n" +
		"refine Tagged<T> List<T> where T: Named when fn xs -> Iter.all_match(xs, fn x -> Named.name(x) != \"\")\n\n"
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
			{42, 42, msg("Shown", "Display.to_string for Loud calls io.print")},
			{44, 46, msg("Sorted", "Comparable.compare for Loud calls io.print")},
			{46, 41, msg("Tagged", "Named.name for Loud calls io.print")},
		}},
		{"an instance of a generic impl", decls + box + rules + "fn main()\n    io.print(Box{v: 1})\nend\n", []want{
			{28, 42, msg("Shown", "Display.to_string for Box<Int> calls io.print")},
		}},
		{"a rule only the program's own type meets the bounds of", decls +
			"refine Listed<T> List<T> where T: Named when fn xs ->\n    io.print(\"listing\")\n    Iter.all_match(xs, fn x -> Named.name(x) != \"\")\nend\n\n" + main, []want{
			{17, 41, msg("Listed", "lambda calls io.print")},
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
		"refine Shown<T> List<T> where T: Display when fn xs -> Iter.all_match(xs, fn x -> Display.to_string(x) != \"\")\n\n"+
		"refine Sorted<T> List<T> where T: Comparable when fn xs -> Iter.zip(xs, Iter.drop(xs, 1)) |> Iter.all_match(fn (a, b) -> a <= b)\n\n"+
		"fn main()\n"+
		"    io.inspect(Shown.try_from([Quiet{n: 1}]) |> Option.is_some())\n"+
		"    io.inspect(Sorted.try_from([Quiet{n: 2}, Quiet{n: 1}]) |> Option.is_some())\n"+
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

// A rule may call std's pure parsers and checks: a header parser, a
// signature parse and String.is_upper are pure, so each rule runs when the
// program is compiled. A literal that breaks one is an error naming the
// rule, and literals that keep them load and run.
func TestRefinement_ARuleMayCallStdsPureParsers(t *testing.T) {
	const decls = "import\n    std::http.Cookie\n    std::io\n    std::signatures.PublicKey\nend\n\n" +
		"refine CookieHeader String when fn s -> Cookie.parse_header(s) |> Result.is_ok()\n\n" +
		"refine PublicPem String when fn s -> PublicKey.from_pem(s) |> Result.is_ok()\n\n" +
		"refine Shout String when fn s -> String.is_upper(s)\n\n"
	for _, tc := range []struct{ body, want string }{
		{"    c: CookieHeader = \"\"\n    io.inspect(c)\n",
			"\"\" is not a CookieHeader: `fn s -> Cookie.parse_header(s) |> Result.is_ok()` is false"},
		{"    k: PublicPem = \"not a key\"\n    io.inspect(k)\n",
			"\"not a key\" is not a PublicPem: `fn s -> PublicKey.from_pem(s) |> Result.is_ok()` is false"},
		{"    s: Shout = \"quiet\"\n    io.inspect(s)\n",
			"\"quiet\" is not a Shout: `fn s -> String.is_upper(s)` is false"},
	} {
		ds := literalDiagnostics(t, decls+"fn main()\n"+tc.body+"end\n")
		if len(ds) != 1 || ds[0].Message != tc.want || ds[0].Code != InvalidRefinementCode {
			t.Errorf("%s: diagnostics = %v, want one reading %q", strings.TrimSpace(tc.body), ds, tc.want)
		}
	}
	p, err := LoadSource("main", decls+"fn main()\n"+
		"    c: CookieHeader = \"a=1; b=2\"\n"+
		"    k: PublicPem = \"-----BEGIN PUBLIC KEY-----\\nMCowBQYDK2VwAyEAGb9ECWmEzf6FQbrBZ9w7lshQhqowtrbLDFw4rXAxZuE=\\n-----END PUBLIC KEY-----\\n\"\n"+
		"    s: Shout = \"LOUD\"\n"+
		"    io.inspect((c, String.length(k) > 0, s))\n"+
		"end\n")
	if err != nil {
		t.Fatalf("the program does not load: %v", err)
	}
	var out strings.Builder
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got, want := out.String(), "(\"a=1; b=2\", True, \"LOUD\")\n"; got != want {
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

refine Ratio Int when fn n -> 10 / NonZeroInt.try_from(n)! > 1

refine SmallRatio Ratio when fn n -> n < 5

struct Fraction
    top:    Int
    bottom: Int
    when fn f -> f.top / NonZeroInt.try_from(f.bottom)! < 1
end

fn main()
    io.inspect(Ratio.try_from(0))
    io.inspect(Ratio.try_from(2))
    io.inspect(SmallRatio.try_from(0))
    io.inspect(SmallRatio.try_from(3))
    io.inspect(Fraction.try_from({top: 1, bottom: 0}))
    zero = Iter.sum([0])
    io.inspect(10 / NonZeroInt.try_from(zero)!)
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
	if err == nil || !strings.Contains(err.Error(), "found nothing") {
		t.Errorf("the trap outside a rule = %v, want it to stop the program", err)
	}
}

// A literal whose check the builder cannot find is an internal error at the
// literal, naming the refinement and the `try_from` it looked for, never a
// literal let through unchecked: the builder is made to miss the check
// (irbuild.PlantMissingRefinementCheckForTest), as a linkage name built
// wrong did for a listed std module's ExitCode. A rule the program
// declares is reported at its `when` too, since its purity is checked
// through the same function.
func TestRefinement_ALiteralWhoseCheckIsMissingIsAnInternalError(t *testing.T) {
	type want struct {
		line, col int
		msg       string
	}
	for _, tc := range []struct {
		name, plant, src string
		want             []want
	}{
		{"a program's refinement", "Port",
			"refine Port Int when fn p -> p >= 1\n\nfn main()\n    p: Port = 80\n    dbg p\nend\n",
			[]want{
				{1, 17, "internal: the check of Port's rule could not be found (Port.try_from)"},
				{4, 15, "internal: the check for this Port literal could not be found (Port.try_from)"},
			}},
		{"a listed std module's refinement", "ExitCode",
			"import std::process.Exit\n\nfn main() -> Result<Unit, Exit>\n    Err(Exit{code: 3})\nend\n",
			[]want{{4, 20, "internal: the check for this ExitCode literal could not be found (std/process.ExitCode.try_from)"}}},
		{"a parent's rule", "NonNegativeInt",
			"fn main()\n    n: PositiveInt = 5\n    dbg n\nend\n",
			[]want{{2, 22, "internal: the check for this PositiveInt literal could not be found (std/int.NonNegativeInt.try_from)"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			restore := irbuild.PlantMissingRefinementCheckForTest(tc.plant)
			defer restore()
			var ds Diagnostics
			for _, d := range literalDiagnostics(t, tc.src) {
				// A listed std module is lowered with the program, so its
				// own literals and rule are reported too; only main's are
				// this test's.
				if d.Path == "main.nomi" {
					ds = append(ds, d)
				}
			}
			if len(ds) != len(tc.want) {
				t.Fatalf("diagnostics = %v, want %d", ds, len(tc.want))
			}
			for i, w := range tc.want {
				d := ds[i]
				if d.Line != w.line || d.Col != w.col || d.Message != w.msg || d.Code != UncheckedRefinementCode {
					t.Errorf("diagnostic %d = %d:%d %q (%s), want %d:%d %q (%s)", i, d.Line, d.Col, d.Message, d.Code, w.line, w.col, w.msg, UncheckedRefinementCode)
				}
			}
		})
	}
}

// Every literal the checker types as a refinement is checked, whatever its
// form: a codepoint, and a grouped Decimal, typed literal or negated
// number, which the builder makes by lowering the literal.
func TestRefinement_EveryFormOfLiteralIsChecked(t *testing.T) {
	const decls = "import std::duration.Duration\n\n" +
		"refine Vowel Codepoint when fn c -> c == 'a' or c == 'e'\n\n" +
		"refine PosDec Decimal when fn d -> d > 0d\n\n" +
		"refine Pos Int when fn n -> n > 0\n\n" +
		"refine Long Duration when fn d -> Duration.as_nanos(d) > 1_000_000_000\n\n"
	for _, tc := range []struct{ body, want string }{
		{"    v: Vowel = 'b'\n    dbg v\n", "'b' is not a Vowel: `fn c -> c == 'a' or c == 'e'` is false"},
		{"    d: PosDec = (-1.5d)\n    dbg d\n", "(-1.5d) is not a PosDec: `fn d -> d > 0d` is false"},
		{"    d: PosDec = (0d)\n    dbg d\n", "0d is not a PosDec: `fn d -> d > 0d` is false"},
		{"    n: Pos = (-3)\n    dbg n\n", "(-3) is not a Pos: `fn n -> n > 0` is false"},
		{"    l: Long = (Duration\"1s\")\n    dbg l\n", "Duration\"1s\" is not a Long: `fn d -> Duration.as_nanos(d) > 1_000_000_000` is false"},
	} {
		ds := literalDiagnostics(t, decls+"fn main()\n"+tc.body+"end\n")
		if len(ds) != 1 || ds[0].Message != tc.want || ds[0].Code != InvalidRefinementCode {
			t.Errorf("%s: diagnostics = %v, want one reading %q", strings.TrimSpace(tc.body), ds, tc.want)
		}
	}
	if _, err := LoadSource("main", decls+"fn main()\n    v: Vowel = 'a'\n    d: PosDec = (1.5d)\n    l: Long = (Duration\"2s\")\n    dbg (v, d, l)\nend\n"); err != nil {
		t.Errorf("literals that keep their rules do not load: %v", err)
	}
}

// A validated type's rule is checked for effects at its `when` whether or
// not anything builds one, as a refinement's is: its `try_from` is its TryFrom
// impl's, which the builder names with the interface's arguments.
func TestRefinement_AValidatedTypesRuleWithEffectsIsAnError(t *testing.T) {
	const src = "import std::io\n\nfn loud(s: String) -> Bool\n    io.print(\"checking\")\n    String.contains(s, \"@\")\nend\n\n" +
		"type Email String when loud\n\nfn main()\n    io.print(\"hi\")\nend\n"
	ds := literalDiagnostics(t, src)
	const want = "the rule of Email has effects (loud calls io.print): a rule is a pure function of the value it checks"
	if len(ds) != 1 || ds[0].Message != want || ds[0].Line != 8 || ds[0].Code != InvalidRefinementCode {
		t.Errorf("diagnostics = %v, want one at line 8 reading %q", ds, want)
	}
}

// A rule reaches a program's hand-written Equatable only through a value of
// its type: Int64's rule, a std refinement's and a program's own refinement of
// Int compare and pass Ints, so an `equal` that prints does not make their
// literals uncheckable. A rule that compares values of the type, directly
// or as a Set's members, reaches it, and the error names it.
func TestRefinement_ARuleOverIntsReachesNoEquatableOfTheProgram(t *testing.T) {
	const decls = "import\n    std::int.Int64\n    std::io\nend\n\n" +
		"struct Score\n    points: Int64\nend\n\n" +
		"impl Equatable for Score\n    fn equal(a: Score, b: Score) -> Bool\n        io.print(\"compared\")\n        a.points == b.points\n    end\nend\n\n" +
		"refine Small Int when fn n -> n < 10\n\n"
	p, err := LoadSource("main", decls+"fn main()\n"+
		"    a: Int64 = 7\n"+
		"    b: PositiveInt = 3\n"+
		"    c: Small = 9\n"+
		"    io.inspect(Score{points: a} == Score{points: 8})\n"+
		"    io.inspect((a, b, c))\n"+
		"end\n")
	if err != nil {
		t.Fatalf("the program does not load: %v", err)
	}
	var out strings.Builder
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got, want := out.String(), "compared\nFalse\n(7, 3, 9)\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}

	for _, tc := range []struct{ rule, use, want string }{
		{"refine Scored Score when fn s -> s != Score{points: 0}\n\n",
			"    io.inspect(Scored.try_from(Score{points: 1}))\n",
			"the rule of Scored has effects (Equatable.equal for Score calls io.print): a rule is a pure function of the value it checks"},
		{"refine Unique List<Score> when fn xs -> Iter.count(Iter.to_set(xs)) == List.length(xs)\n\n",
			"    io.inspect(Unique.try_from([Score{points: 1}]))\n",
			"the rule of Unique has effects (Equatable.equal for Score calls io.print): a rule is a pure function of the value it checks"},
	} {
		ds := literalDiagnostics(t, decls+tc.rule+"fn main()\n"+tc.use+"end\n")
		if len(ds) != 1 || ds[0].Message != tc.want || ds[0].Code != InvalidRefinementCode {
			t.Errorf("%s: diagnostics = %v, want one reading %q", strings.TrimSpace(tc.rule), ds, tc.want)
		}
	}
}

// A rule reaches a program's Display only through a value of its type: a
// rule that interpolates a Point in a tuple renders Point's Display, so a
// Display of another type that prints does not make the rule effectful. A
// rule that renders a value of the printing type reaches it, and the error
// names it.
func TestRefinement_ARuleReachesOnlyTheDisplayOfTypesItRenders(t *testing.T) {
	const decls = "import std::io\n\n" +
		"struct Loud\n    n: Int\nend\n\n" +
		"impl Display for Loud\n    fn to_string(l: Loud) -> String\n        io.print(\"shown\")\n        \"loud ${l.n}\"\n    end\nend\n\n" +
		"struct Point\n    x: Int\nend\n\n" +
		"derive Display for Point\n\n"
	p, err := LoadSource("main", decls+
		"refine Shown Point when fn p -> \"${(p, 1)}\" != \"\"\n\n"+
		"fn main()\n    io.inspect(Shown.try_from(Point{x: 1}))\nend\n")
	if err != nil {
		t.Fatalf("the program does not load: %v", err)
	}
	var out strings.Builder
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got, want := out.String(), "Some(Point{x: 1})\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}

	ds := literalDiagnostics(t, decls+
		"refine Shown Point when fn p -> \"${(p, Loud{n: p.x})}\" != \"\"\n\n"+
		"fn main()\n    io.inspect(Shown.try_from(Point{x: 1}))\nend\n")
	const want = "the rule of Shown has effects (Display.to_string for Loud calls io.print): a rule is a pure function of the value it checks"
	if len(ds) != 1 || ds[0].Message != want || ds[0].Code != InvalidRefinementCode {
		t.Errorf("a rule that renders a Loud: diagnostics = %v, want one reading %q", ds, want)
	}
}
