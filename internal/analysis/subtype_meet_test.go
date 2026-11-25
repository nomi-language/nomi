package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// subtypeMeetDecls binds a value of each std non-empty subtype and of
// the numeric ones, and values of their bases, so a case meets one with the
// other.
const subtypeMeetDecls = `fn pick<T>(c: Bool, a: T, b: T) -> T
    if c then a else b
end

fn main()
    c = String.length("ab") > 1
    xs: NonEmptyList<String> = ["a"]
    vs: NonEmptyVector<Int> = Vector[1]
    ss: NonEmptySet<Int> = Set[1]
    ms: NonEmptyMap<String, Int> = {"a" => 1}
    s: NonEmptyString = "a"
    p: PositiveInt = 2
    ps: List<PositiveInt> = [1, 2]
    vps: Vector<PositiveInt> = Vector[1]
    sps: Set<PositiveInt> = Set[1]
    mps: Map<String, PositiveInt> = {"a" => 1}
    ints: List<Int> = [0]
    mints: Map<String, Int> = {"z" => 0}
    nps: NonEmptyList<PositiveInt> = [1]
    xss: List<List<NonNegativeInt>> = [[1, 2]]
    _ = (c, xs, vs, ss, ms, s, p, ps, vps, sps, mps, ints, mints, nps, xss)
`

// Where a value of a subtype meets a value or a literal of its base,
// the result is their join, the nearest type both are values of (spec §4
// *Chains and conversion*), whatever the order: the branches of an `if`
// or `case`, two arguments of one type parameter, the elements and spreads
// of a collection literal, the operands of an operator. A literal beside
// a std numeric subtype it satisfies is that subtype. Each case is
// written in both orders, and each order gives the same type.
func TestSubtypeMeet_TheJoinInEitherOrder(t *testing.T) {
	for _, tc := range []struct{ name, a, b, want string }{
		{"an if of an empty list and a NonEmptyList", "if c then [] else xs", "if c then xs else []", "List<String>"},
		{"a case of an empty list and a NonEmptyList", "case c\n        True -> []\n        False -> xs\n    end", "case c\n        True -> xs\n        False -> []\n    end", "List<String>"},
		{"an if of an empty vector and a NonEmptyVector", "if c then Vector[] else vs", "if c then vs else Vector[]", "Vector<Int>"},
		{"an if of an empty set and a NonEmptySet", "if c then Set[] else ss", "if c then ss else Set[]", "Set<Int>"},
		{"an if of an empty map and a NonEmptyMap", "if c then {} else ms", "if c then ms else {}", "Map<String, Int>"},
		{"an if of an empty string and a NonEmptyString", "if c then \"\" else s", "if c then s else \"\"", "String"},
		{"an if of a literal a numeric subtype takes", "if c then 0 else p", "if c then p else 0", "NonNegativeInt"},
		{"an if of a literal a parent subtype takes", "if c then -1 else p", "if c then p else -1", "NonZeroInt"},
		{"an if of a literal no numeric subtype takes", "if c then -1 else List.length(ints)", "if c then List.length(ints) else -1", "Int"},
		{"an if of subtypes nested in lists", "if c then nps else ints", "if c then ints else nps", "List<Int>"},
		{"two arguments of one type parameter", "pick(c, [], xs)", "pick(c, xs, [])", "List<String>"},
		{"two numeric arguments of one type parameter", "pick(c, p, -1)", "pick(c, -1, p)", "NonZeroInt"},
		{"a default for a NonEmptyList", "Option.with_default(Some(xs), [])", "Option.with_default(Some([]), xs)", "List<String>"},
		{"an Option of a NonEmptyList beside one of an empty list", "Option.otherwise(Some(xs), Some([]))", "Option.otherwise(Some([]), Some(xs))", "Option<List<String>>"},
		{"a value put into a map of NonEmptyLists", "Map.put({\"k\" => xs}, \"j\", [])", "Map.put({\"j\" => []}, \"k\", xs)", "NonEmptyMap<String, List<String>>"},
		{"a piped default for a group", "Iter.group_by([1, 2], fn n -> n % 2) |> Map.get(5) |> Option.with_default([])", "Iter.group_by([1, 2], fn n -> n % 2) |> Map.get(5) |> Option.with_default([1])", "List<Int>"},
		{"a literal the spreads' subtype refuses", "[..ps, 0]", "[0, ..ps]", "List<NonNegativeInt>"},
		{"a literal the spreads' subtype takes", "[..ps, 1]", "[1, ..ps]", "List<PositiveInt>"},
		{"a literal a parent subtype takes", "[..ps, -1]", "[-1, ..ps]", "List<NonZeroInt>"},
		{"a literal no numeric subtype takes", "[..Iter.map(xss, List.length), -1]", "[-1, ..Iter.map(xss, List.length)]", "List<Int>"},
		{"a vector spread and a literal", "Vector[..vps, 0]", "Vector[0, ..vps]", "Vector<NonNegativeInt>"},
		{"a set spread and a literal", "Set[..sps, 0]", "Set[0, ..sps]", "Set<NonNegativeInt>"},
		{"a map spread and a literal value", "{..mps, \"b\" => 0}", "{\"b\" => 0, ..mps}", "Map<String, NonNegativeInt>"},
		{"two map spreads", "{..mints, ..mps}", "{..mps, ..mints}", "Map<String, Int>"},
		{"two list spreads", "[..ints, ..ps]", "[..ps, ..ints]", "List<Int>"},
		{"a NonEmptyVector plus a list literal", "vs + [3]", "[3] + vs", "Vector<Int>"},
		{"a NonEmptyVector plus an empty literal", "vs + []", "[] + vs", "Vector<Int>"},
		{"lists of a subtype and of its base", "ints + ps", "ps + ints", "List<Int>"},
		{"a NonEmptyString plus an empty string", "s + \"\"", "\"\" + s", "String"},
		{"a function value whose bound makes a value", "Iter.map(xss, Iter.sum) |> Iter.to_list()", "xss |> Iter.map(Iter.sum) |> Iter.to_list()", "List<Int>"},
		{"a product as a function value", "Iter.map(xss, Iter.product) |> Iter.to_list()", "Iter.windows([1, 2, 3], 2) |> Iter.map(Iter.product) |> Iter.to_list()", "List<Int>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, expr := range []string{tc.a, tc.b} {
				src := subtypeMeetDecls + "    v = " + expr + "\n    _ = v\nend\n"
				fa, errs := checkSourceWithStdlib(src)
				expectNoStdlibErrors(t, errs)
				if got := bindingType(t, fa, "v"); got != tc.want {
					t.Errorf("%s: got %s, want %s", expr, got, tc.want)
				}
				expectNoStdNonEmptyLiteral(t, fa, expr)
			}
		})
	}
}

// A literal compared with a value of a subtype is checked with the
// subtypes erased (spec §4 *Literals, `try_from` and `unchecked`*): it
// is its base's, so no rule is checked on it, on either side and inside
// an Option or a tuple.
func TestSubtypeMeet_AComparedLiteralIsItsBases(t *testing.T) {
	for _, expr := range []string{
		"xs == []", "[] == xs", "xs != []", "Some(xs) == Some([])", "Some([]) == Some(xs)",
		"(xs, 1) == ([], 1)", "([], 1) == (xs, 1)", "vs == Vector[]", "ms == {}", "s == \"\"",
		"ps == [0]", "[0] == ps",
	} {
		src := subtypeMeetDecls + "    v = " + expr + "\n    _ = v\nend\n"
		fa, errs := checkSourceWithStdlib(src)
		expectNoStdlibErrors(t, errs)
		if got := bindingType(t, fa, "v"); got != "Bool" {
			t.Errorf("%s: got %s, want Bool", expr, got)
		}
		for node, rec := range subtypeLiteralsOfTheCase(fa) {
			t.Errorf("%s: %s is checked as %s", expr, analysis.RenderLiteral(node), rec.Type)
		}
	}
}

// subtypeLiteralsOfTheCase is each literal at or after the case's `v =`
// line that fa checks as a subtype.
func subtypeLiteralsOfTheCase(fa *analysis.FileAnalysis) map[ast.Node]*analysis.SubtypeLiteral {
	first := strings.Count(subtypeMeetDecls, "\n") + 1
	out := map[ast.Node]*analysis.SubtypeLiteral{}
	for node, rec := range fa.SubtypeLiterals {
		if line, _ := ast.Position(node); line >= first {
			out[node] = rec
		}
	}
	return out
}

// expectNoStdNonEmptyLiteral fails when a literal in fa is checked as a std
// non-empty subtype: every case's literal meets a value of a subtype with no
// type written for it, so none is held to the rule.
func expectNoStdNonEmptyLiteral(t *testing.T, fa *analysis.FileAnalysis, expr string) {
	t.Helper()
	for node, rec := range subtypeLiteralsOfTheCase(fa) {
		if strings.HasPrefix(rec.Type.String(), "NonEmpty") {
			t.Errorf("%s: the literal %s is checked as %s", expr, analysis.RenderLiteral(node), rec.Type)
		}
	}
}
