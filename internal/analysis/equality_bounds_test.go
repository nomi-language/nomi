package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// equalityBoundDecls declares a struct with equality and no impl (Point),
// one with a custom Equatable (Tag), and one holding a function (Op).
const equalityBoundDecls = `struct Point {
    x: Int
    y: Int
}

struct Tag {
    name: String
}

impl Equatable for Tag {
    fn equal(a: Tag, b: Tag) -> Bool {
        String.lowercase(a.name) == String.lowercase(b.name)
    }
}

struct Op {
    f: (Int) -> Int
}

fn same<T>(a: T, b: T) -> Bool where T: Equatable {
    a == b
}

fn key<T>(a: T) -> Int where T: Hashable {
    Hashable.hash(a)
}

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
		"a vector's in":          "Point{x: 1, y: 2} in #[Point{x: 1, y: 2}]",
		"List.contains":          "List.contains([Point{x: 1, y: 2}], Point{x: 1, y: 2})",
		"Iter.contains":          "Iter.contains([Point{x: 1, y: 2}], Point{x: 1, y: 2})",
		"List.starts_with":       "List.starts_with([Point{x: 1, y: 2}], [Point{x: 1, y: 2}])",
		"a custom Equatable":     "same(Tag{name: \"a\"}, Tag{name: \"A\"})",
		"a Hashable bound":       "key(Point{x: 1, y: 2}) == 0",
		"Hashable, custom equal": "key(Tag{name: \"a\"}) == 0",
		"a tuple of one":         "key((1, Point{x: 1, y: 2})) == 0",
		"a container of one":     "key([Point{x: 1, y: 2}]) == 0",
		"a Maybe of one":         "same(Some(Point{x: 1, y: 2}), None)",
		"an enum with no impls":  "same(Ordering.Less, Ordering.Less)",
	} {
		src := equalityBoundDecls + "fn f() -> Bool {\n    " + body + "\n}\n"
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
		"a list's in": {"Op{f: fn x -> x} in [Op{f: fn x -> x}]",
			"no impl of `Equatable` for `Op` (required via membership test `in`) | `Op` holds a function, an Iter or a handle to running state, which has no equality; write an `impl Equatable for Op` block that leaves it out"},
	} {
		src := equalityBoundDecls + "fn f() -> Bool {\n    " + tc.body + "\n}\n"
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

// An interface value holds an implementor, so a position of type Equatable
// or Hashable still takes only a type with an impl: the bound's rule is a
// bound's.
func TestEqualityInterfaceValueStillNeedsAnImpl(t *testing.T) {
	src := equalityBoundDecls + "fn hold(h: Hashable) -> Int {\n    Hashable.hash(h)\n}\n\nfn f() -> Int {\n    hold(Point{x: 1, y: 2})\n}\n"
	got := equalityBoundErrors(src)
	want := "argument 1: expected Hashable, got Point"
	if len(got) == 0 || !strings.HasPrefix(got[0], want) {
		t.Fatalf("want %q, got:\n%s", want, strings.Join(got, "\n"))
	}
}
