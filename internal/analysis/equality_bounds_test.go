package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// equalityBoundDecls declares a struct with equality and no impl (Point),
// one with a custom Equatable (Tag), and one holding a function (Op).
const equalityBoundDecls = `struct Point
    x: Int
    y: Int
end

struct Tag
    name: String
end

impl Equatable for Tag
    fn equal(a: Tag, b: Tag) -> Bool
        String.lowercase(a.name) == String.lowercase(b.name)
    end
end

struct Op
    f: (Int) -> Int
end

fn same<T>(a: T, b: T) -> Bool where T: Equatable
    a == b
end

fn key<T>(a: T) -> Int where T: Hashable
    Hashable.hash(a)
end

`

// equalityBoundErrors answers every diagnostic of the program, the
// coherence pass's among them, with its help.
func equalityBoundErrors(src string) []string {
	fa, errs := checkSourceWithStdlib(src)
	errs = append(errs, analysis.FinalizeCoherence(fa)...)
	var out []string
	for _, e := range errs {
		out = append(out, strings.Join(append([]string{e.Message}, e.Hints...), " | "))
	}
	return out
}

// A `where T: Equatable` or `where T: Hashable` bound means the type has
// equality (spec §5 *Equality bounds*): a struct, enum or distinct type
// with no impl of its own meets it, and so do the std functions and impls
// bounded by it, List's and Vector's Contains among them.
func TestEqualityBoundAcceptsATypeWithEquality(t *testing.T) {
	for name, body := range map[string]string{
		"a user generic":         "same(Point{x: 1, y: 2}, Point{x: 1, y: 2})",
		"a list's in":            "Point{x: 1, y: 2} in [Point{x: 1, y: 2}]",
		"a vector's in":          "Point{x: 1, y: 2} in Vector[Point{x: 1, y: 2}]",
		"List.contains":          "List.contains([Point{x: 1, y: 2}], Point{x: 1, y: 2})",
		"Iter.contains":          "Iter.contains([Point{x: 1, y: 2}], Point{x: 1, y: 2})",
		"List.starts_with":       "List.starts_with([Point{x: 1, y: 2}], [Point{x: 1, y: 2}])",
		"a custom Equatable":     "same(Tag{name: \"a\"}, Tag{name: \"A\"})",
		"a Hashable bound":       "key(Point{x: 1, y: 2}) == 0",
		"Hashable, custom equal": "key(Tag{name: \"a\"}) == 0",
		"a tuple of one":         "key((1, Point{x: 1, y: 2})) == 0",
		"a container of one":     "key([Point{x: 1, y: 2}]) == 0",
		"an Option of one":       "same(Some(Point{x: 1, y: 2}), None)",
		"an enum with no impls":  "same(Ordering.Less, Ordering.Less)",
	} {
		src := equalityBoundDecls + "fn f() -> Bool\n    " + body + "\nend\n"
		if got := equalityBoundErrors(src); len(got) != 0 {
			t.Errorf("%s: the front end rejects `%s`:\n%s", name, body, strings.Join(got, "\n"))
		}
	}
}

// A value with no equality does not meet the bound, with the message `==`'s
// rule gives it.
func TestEqualityBoundRefusesATypeWithoutEquality(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"a user generic": {"same(Op{f: fn x -> x}, Op{f: fn x -> x})",
			"Op does not implement Equatable (required by `where T: Equatable`) | `Op` holds `(Int) -> Int`, and a function value has no equality"},
		"a Hashable bound": {"key(Op{f: fn x -> x}) == 0",
			"Op does not implement Hashable (required by `where T: Hashable`) | `Op` holds `(Int) -> Int`, and a function value has no equality"},
		"Hashable.hash of a list": {"Hashable.hash([fn x: Int -> x]) == 0",
			"List<(Int) -> Int> does not implement Hashable (required by `Hashable.hash`) | `List<(Int) -> Int>` holds `(Int) -> Int`, and a function value has no equality"},
		"Hashable.hash of an Option": {"Hashable.hash(Some(fn x: Int -> x)) == 0",
			"Option<(Int) -> Int> does not implement Hashable (required by `Hashable.hash`) | `Option<(Int) -> Int>` holds `(Int) -> Int`, and a function value has no equality"},
		"Hashable.hash of an Option of a declared type": {"Hashable.hash(Some(Op{f: fn x -> x})) == 0",
			"no impl of `Hashable` for `Op` (required by the call `Hashable.hash`) | `Op` holds a function, an Iter or a handle to running state, which has no equality; write an `impl Hashable for Op` block that leaves it out"},
		"Hashable.hash of a tuple": {"Hashable.hash((1, fn x: Int -> x)) == 0",
			"(Int, (Int) -> Int) does not implement Hashable (required by `Hashable.hash`) | `(Int, (Int) -> Int)` holds `(Int) -> Int`, and a function value has no equality"},
		"Equatable.equal of a list": {"Equatable.equal([fn x: Int -> x], [])",
			"List<(Int) -> Int> does not implement Equatable (required by `Equatable.equal`) | `List<(Int) -> Int>` holds `(Int) -> Int`, and a function value has no equality"},
		"a list's in": {"Op{f: fn x -> x} in [Op{f: fn x -> x}]",
			"no impl of `Equatable` for `Op` (required via membership test `in`) | `List<T>`'s `Contains` impl requires `T: Equatable` | `Op` holds a function, an Iter or a handle to running state, which has no equality; write an `impl Equatable for Op` block that leaves it out"},
	} {
		src := equalityBoundDecls + "fn f() -> Bool\n    " + tc.body + "\nend\n"
		got := equalityBoundErrors(src)
		if len(got) == 0 {
			t.Errorf("%s: the front end accepts `%s`; want %q", name, tc.body, tc.want)
			continue
		}
		if len(got) != 1 || got[0] != tc.want {
			t.Errorf("%s: want the one error %q, got:\n%s", name, tc.want, strings.Join(got, "\n"))
		}
	}
}

// `equal` and `hash` called qualified, through the interface or through the
// type, over a value that holds a function at any depth are refused, for
// every container, wrapper and structural type, whether std writes the
// impl (List's, Vector's, Map's and Set's, whose Equatable names no bound
// on the element) or derives it (Option's, Result's). Each is one error
// whose help names what the value holds, as `==` on it is.
func TestQualifiedEqualityOverAValueWithNoEqualityIsRefused(t *testing.T) {
	const decls = "fn inc(n: Int) -> Int\n    n + 1\nend\n\n"
	unmet := func(typ, iface, by string) string {
		return typ + " does not implement " + iface + " (required by `" + by + "`) | `" + typ + "` holds `(Int) -> Int`, and a function value has no equality"
	}
	const res = "(Ok(inc) as Result<(Int) -> Int, Int>)"
	for _, tc := range []struct{ params, body, want string }{
		{"", "List.equal([inc], [inc])", unmet("List<(Int) -> Int>", "Equatable", "List.equal")},
		{"", "Equatable.equal([inc], [inc])", unmet("List<(Int) -> Int>", "Equatable", "Equatable.equal")},
		{"", "Hashable.hash([inc]) == 0", unmet("List<(Int) -> Int>", "Hashable", "Hashable.hash")},
		{"", "List.equal([[inc]], [[inc]])", unmet("List<List<(Int) -> Int>>", "Equatable", "List.equal")},
		{"", "List.equal([(1, inc)], [(1, inc)])", unmet("List<(Int, (Int) -> Int)>", "Equatable", "List.equal")},
		{"", "List.equal([{a: inc}], [{a: inc}])", unmet("List<{a: (Int) -> Int}>", "Equatable", "List.equal")},
		{"", "Vector.equal(Vector[inc], Vector[inc])", unmet("Vector<(Int) -> Int>", "Equatable", "Vector.equal")},
		{"", "Equatable.equal(Vector[inc], Vector[inc])", unmet("Vector<(Int) -> Int>", "Equatable", "Equatable.equal")},
		{"", "Hashable.hash(Vector[inc]) == 0", unmet("Vector<(Int) -> Int>", "Hashable", "Hashable.hash")},
		{"", "Map.equal({1 => inc}, {1 => inc})", unmet("Map<Int, (Int) -> Int>", "Equatable", "Map.equal")},
		{"", "Equatable.equal({1 => inc}, {1 => inc})", unmet("Map<Int, (Int) -> Int>", "Equatable", "Equatable.equal")},
		{"", "Hashable.hash({1 => inc}) == 0", unmet("Map<Int, (Int) -> Int>", "Hashable", "Hashable.hash")},
		{"s: Set<(Int) -> Int>", "Set.equal(s, s)", unmet("Set<(Int) -> Int>", "Equatable", "Set.equal")},
		{"s: Set<(Int) -> Int>", "Equatable.equal(s, s)", unmet("Set<(Int) -> Int>", "Equatable", "Equatable.equal")},
		{"s: Set<(Int) -> Int>", "Hashable.hash(s) == 0", unmet("Set<(Int) -> Int>", "Hashable", "Hashable.hash")},
		{"", "Option.equal(Some(inc), None)", unmet("Option<(Int) -> Int>", "Equatable", "Option.equal")},
		{"", "Option.hash(Some(inc)) == 0", unmet("Option<(Int) -> Int>", "Hashable", "Option.hash")},
		{"", "Option.equal(Some([inc]), None)", unmet("Option<List<(Int) -> Int>>", "Equatable", "Option.equal")},
		{"", "Result.equal(" + res + ", " + res + ")", unmet("Result<(Int) -> Int, Int>", "Equatable", "Result.equal")},
		{"", "Result.hash(" + res + ") == 0", unmet("Result<(Int) -> Int, Int>", "Hashable", "Result.hash")},
		{"", "Equatable.equal((1, inc), (1, inc))", unmet("(Int, (Int) -> Int)", "Equatable", "Equatable.equal")},
		{"", "Hashable.hash((1, inc)) == 0", unmet("(Int, (Int) -> Int)", "Hashable", "Hashable.hash")},
		{"", "Equatable.equal({a: inc}, {a: inc})", unmet("{a: (Int) -> Int}", "Equatable", "Equatable.equal")},
		{"", "Hashable.hash({a: [inc]}) == 0", unmet("{a: List<(Int) -> Int>}", "Hashable", "Hashable.hash")},
	} {
		src := decls + "fn f(" + tc.params + ") -> Bool\n    " + tc.body + "\nend\n"
		got := equalityBoundErrors(src)
		if len(got) == 0 {
			t.Errorf("the front end accepts `%s`; want %q", tc.body, tc.want)
			continue
		}
		if len(got) != 1 || got[0] != tc.want {
			t.Errorf("%s: want the one error %q, got:\n%s", tc.body, tc.want, strings.Join(got, "\n"))
		}
	}
}

// An interface value holds an implementor, so a position of type Equatable
// or Hashable still takes only a type with an impl: the bound's rule is a
// bound's.
func TestEqualityInterfaceValueStillNeedsAnImpl(t *testing.T) {
	src := equalityBoundDecls + "fn hold(h: Hashable) -> Int\n    Hashable.hash(h)\nend\n\nfn f() -> Int\n    hold(Point{x: 1, y: 2})\nend\n"
	got := equalityBoundErrors(src)
	want := "argument 1: expected Hashable, got Point"
	if len(got) == 0 || !strings.HasPrefix(got[0], want) {
		t.Fatalf("want %q, got:\n%s", want, strings.Join(got, "\n"))
	}
}
