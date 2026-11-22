package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// Generic refinements (spec §4 *Generic subtypes*): a refinement's type
// parameters, used in its parents, with bounds its rule needs; its
// conversions follow its parents' variance, and joins carry its type
// arguments.

const genericRefinementDecls = `subtype Many<T> List<T> when fn xs -> not Iter.is_empty(xs)

subtype Ascending<T> List<T> where T: Comparable when fn xs -> Iter.zip(xs, Iter.drop(xs, 1)) |> Iter.all_match(fn (a, b) -> a <= b)

subtype Run<T> Many<T> and Ascending<T> where T: Comparable when fn _ -> True

subtype IntPair List<Int> when fn xs -> List.length(xs) == 2

subtype Takers<T> List<(T) -> Int> when fn fs -> not Iter.is_empty(fs)

interface Shape
    fn area(shape: self) -> Float
end

struct Circle
    r: Float
end

impl Shape for Circle
    fn area(c: Circle) -> Float
        c.r * c.r * 3.0
    end
end

fn shapes(xs: Many<Shape>) -> Int
    List.length(xs)
end

fn plain(xs: List<Shape>) -> Int
    List.length(xs)
end

fn many(xs: Many<Int>) -> Many<Int>
    xs
end

`

func TestGenericRefinement_Accepted(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"a literal at its type arguments", "fn main()\n    xs: Many<Int> = [1, 2]\n    _ = xs\nend\n"},
		{"try_from solves its type argument", "fn f(xs: List<String>) -> Maybe<Many<String>>\n    Many.try_from(xs)\nend\n"},
		{"unchecked in the declaring file", "fn f(xs: List<Int>) -> Many<Int>\n    Many.unchecked(xs)\nend\n"},
		{"a bounded rule's try_from", "fn f(xs: List<Int>) -> Maybe<Ascending<Int>>\n    Ascending.try_from(xs)\nend\n"},
		{"up to its base", "fn f(xs: Many<Int>) -> List<Int>\n    xs\nend\n"},
		{"up to its base, converting the elements", "fn f(xs: Many<Circle>) -> Int\n    plain(xs)\nend\n"},
		{"to itself at a wider argument", "fn f(xs: Many<Circle>) -> Int\n    shapes(xs)\nend\n"},
		{"a refinement argument widens", "fn f(xs: Many<PositiveInt>) -> Many<Int>\n    xs\nend\n"},
		{"a contravariant argument narrows", "fn f(t: Takers<Int>) -> Takers<NonNegativeInt>\n    t\nend\n"},
		{"several parents with type arguments", "fn f(r: Run<Int>) -> Many<Int>\n    r\nend\n"},
		{"the other parent", "fn f(r: Run<Int>) -> Ascending<Int>\n    r\nend\n"},
		{"a join at the nearest shared ancestor", "fn f(c: Bool, r: Run<Int>, a: Ascending<Int>) -> Ascending<Int>\n    if c then r else a\nend\n"},
		{"a join with the base", "fn f(c: Bool, r: Many<Int>, xs: List<Int>) -> List<Int>\n    if c then r else xs\nend\n"},
		{"a literal argument solves the parameter", "fn main()\n    _ = many([1, 2])\nend\n"},
		{"a fixed argument", "fn main()\n    p: IntPair = [1, 2]\n    _ = p\nend\n"},
		{"an inherent function of its own type parameters", "impl Many<T>\n    fn head<T>(xs: Many<T>) -> T\n        List.first(xs)!\n    end\nend\n"},
		{"a std NonEmptyList", "fn f(xs: NonEmptyList<Int>) -> Int\n    NonEmptyList.first(xs) + NonEmptyList.max(xs)\nend\n"},
		{"a std NonEmptyList passes as a List", "fn f(xs: NonEmptyList<Circle>) -> Int\n    plain(xs)\nend\n"},
		{"a std NonEmptyMap's values", "fn f(m: NonEmptyMap<String, Int>) -> Int\n    NonEmptyList.max(NonEmptyMap.values(m))\nend\n"},
		{"a comparison with a literal of computed elements", "fn f(xs: List<NonEmptyList<Int>>, n: Int) -> Bool\n    xs == [[n], [1, n]]\nend\n"},
		{"a std group is a NonEmptyList", "fn f(xs: List<String>) -> Map<Int, String>\n    Iter.group_by(xs, String.length) |> Map.map_values(&NonEmptyList.first(&1))\nend\n"},
		{"a std chunk is a NonEmptyList", "fn f(xs: List<Int>) -> List<Int>\n    Iter.chunks(xs, 2) |> Iter.map(fn c -> NonEmptyList.last(c)) |> Iter.to_list()\nend\n"},
		{"a std non-empty list literal of computed elements", "fn f(a: Int, b: Int) -> NonEmptyList<Int>\n    [a, b]\nend\n"},
		{"a std non-empty vector literal headed by its name", "fn f(a: Int) -> NonEmptyVector<Int>\n    Vector[a + 1]\nend\n"},
		{"a std non-empty set literal headed by its name", "fn f(a: Int) -> NonEmptySet<Int>\n    Set[a]\nend\n"},
		{"a std non-empty map literal of computed entries", "fn f(k: String, v: Int) -> NonEmptyMap<String, Int>\n    {k => v}\nend\n"},
		{"a std non-empty literal with a spread and an element", "fn f(xs: List<Int>, y: Int) -> NonEmptyList<Int>\n    [..xs, y]\nend\n"},
		{"a std non-empty literal spreading a non-empty list", "fn f(xs: NonEmptyList<Int>, ys: List<Int>) -> NonEmptyList<Int>\n    [..ys, ..xs]\nend\n"},
		{"a std non-empty literal spreading a subtype of one", "subtype Short<T> NonEmptyList<T> when fn xs -> List.length(xs) < 3\n\nfn f(xs: Short<Int>, ys: List<Int>) -> NonEmptyList<Int>\n    [..xs, ..ys]\nend\n"},
		{"a std non-empty literal checks its elements", "fn f(n: PositiveInt) -> NonEmptyList<NonNegativeInt>\n    [n, 0]\nend\n"},
		{"a function over one as a value", "fn greet(names: NonEmptyList<String>) -> String\n    NonEmptyList.first(names)\nend\n\nfn f(m: Maybe<NonEmptyList<String>>) -> Maybe<String>\n    Maybe.map(m, greet)\nend\n"},
		{"a function over a generic distinct as a value", "type Ids<T> List<T>\n\nfn count(Ids(xs): Ids<Int>) -> Int\n    List.length(xs)\nend\n\nfn f(m: Maybe<Ids<Int>>) -> Maybe<Int>\n    Maybe.map(m, count)\nend\n"},
		{"a map refinement", "subtype Filled<K, V> Map<K, V> when fn m -> Map.size(m) > 0\n\nfn main()\n    m: Filled<String, Int> = {\"a\" => 1}\n    _ = m\nend\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if errs := checkRefinements(t, genericRefinementDecls+tc.body); len(errs) != 0 {
				var msgs []string
				for _, e := range errs {
					msgs = append(msgs, diagText(e))
				}
				t.Fatalf("the checker rejects this: %q", msgs)
			}
		})
	}
}

func TestGenericRefinement_Rejected(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"down from its base", "fn f(xs: List<Int>) -> Many<Int>\n    xs\nend\n",
			"return type mismatch: expected Many<Int>, got List<Int>\nhelp: Many is a subtype of List<Int>; check the value with `Many.try_from(...)`"},
		{"down from a parent", "fn f(xs: Many<Int>) -> Run<Int>\n    xs\nend\n",
			"return type mismatch: expected Run<Int>, got Many<Int>"},
		{"across siblings", "fn f(xs: Many<Int>) -> Ascending<Int>\n    xs\nend\n",
			"return type mismatch: expected Ascending<Int>, got Many<Int>"},
		{"a narrower covariant argument", "fn f(xs: Many<Int>) -> Many<PositiveInt>\n    xs\nend\n",
			"return type mismatch: expected Many<PositiveInt>, got Many<Int>"},
		{"try_from of a type its bound rules out", "fn f(xs: List<Circle>) -> Maybe<Ascending<Circle>>\n    Ascending.try_from(xs)\nend\n",
			"Circle does not implement Comparable"},
		{"a literal with a computed element", "fn f(n: Int) -> Many<Int>\n    [n]\nend\n",
			"Many is a subtype: a literal of it is checked when the program is compiled, so each of its elements is a literal, and `n` is not\nhelp: build a Many from values known only when the program runs with `Many.try_from(...)`, a `Maybe<Many<Int>>`"},
		{"a std non-empty literal of spreads that may be empty", "fn f(xs: List<Int>) -> NonEmptyList<Int>\n    [..xs]\nend\n",
			"`[..xs]` may be empty, and a NonEmptyList holds something: none of its spreads holds something, and it writes no element\nhelp: write an element beside the spread, spread a NonEmptyList, or check the value with `NonEmptyList.try_from(...)`"},
		{"a std non-empty literal's element of another type", "fn f(n: Int) -> NonEmptyList<PositiveInt>\n    [n]\nend\n",
			"list element type mismatch: expected PositiveInt, got Int"},
		{"a subtype over NonEmptyList keeps its rule", "subtype Short<T> NonEmptyList<T> when fn xs -> List.length(xs) < 3\n\nfn f(n: Int) -> Short<Int>\n    [n]\nend\n",
			"Short is a subtype: a literal of it is checked when the program is compiled, so each of its elements is a literal, and `n` is not"},
		{"a generic struct refinement", "struct Span<T>\n    low:  T\n    high: T\n    when fn _ -> True\nend\n",
			"a validated struct takes no type parameters"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := checkRefinements(t, genericRefinementDecls+tc.src)
			for _, e := range errs {
				if strings.HasPrefix(diagText(e), tc.want) {
					return
				}
			}
			var msgs []string
			for _, e := range errs {
				msgs = append(msgs, diagText(e))
			}
			if len(errs) == 0 {
				t.Fatalf("the checker accepts this; want %q", tc.want)
			}
			t.Fatalf("errors = %q, want one reading %q", msgs, tc.want)
		})
	}
}

// A generic refinement's declaration types its rule's `try_from` first
// with each parameter a value of its bounds' interface, which meets them
// whichever types implement them (Ordered only Rank does), then at the
// first primitive witness its bounds admit (RefinementRuleChecks).
func TestGenericRefinement_RuleChecksTryTheBoundsFirst(t *testing.T) {
	const decls = `interface Ordered
    fn before(a: self, b: self) -> Bool
end

struct Rank
    n: Int
end

impl Ordered for Rank
    fn before(a: Rank, b: Rank) -> Bool
        a.n < b.n
    end
end

subtype Shown<T> List<T> where T: Display when fn xs -> Iter.all_match(xs, fn x -> Display.to_string(x) != "")

subtype Keyed<T> List<T> where T: Display and Hashable when fn xs -> Iter.all_match(xs, fn x -> Display.to_string(x) != "")

subtype Ascending<T> List<T> where T: Comparable when fn xs -> Iter.zip(xs, Iter.drop(xs, 1)) |> Iter.all_match(fn (a, b) -> a <= b)

subtype InOrder<T> List<T> where T: Ordered when fn xs -> Iter.zip(xs, Iter.drop(xs, 1)) |> Iter.all_match(fn (a, b) -> Ordered.before(a, b))

subtype Pairs<K, V> Map<K, V> when fn m -> Map.size(m) > 0

fn main()
end
`
	src := withStdlibTestImports(decls)
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(src))
	nodes = analysis.SynthesizeUniversalDebug(nodes)
	lib := loadStd()
	fa := analysis.BuildFileWithStdlib(nodes, lib.Primitives, lib.Modules, "", nil)
	analysis.AttachStdlibProjectImpls(fa, lib.Files)
	analysis.BuildTypes(fa, nodes)
	if errs := analysis.CheckTypes(fa, nodes); len(errs) > 0 {
		t.Fatalf("the checker rejects this: %v", errs)
	}
	got := map[string]string{}
	for td, rc := range fa.RefinementRuleChecks {
		var args []string
		for _, try := range rc.Tries {
			args = append(args, try.Arg.String())
		}
		got[td.Name] = strings.Join(args, " ")
	}
	for name, want := range map[string]string{
		"Shown":     "List<Display> List<Int>",
		"Keyed":     "List<Display and Hashable> List<Int>",
		"Ascending": "List<Comparable> List<Int>",
		"InOrder":   "List<Ordered>",
		"Pairs":     "Map<Int, Int>",
	} {
		if got[name] != want {
			t.Errorf("%s's rule is checked at %q, want %q", name, got[name], want)
		}
	}
}
