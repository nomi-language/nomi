package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// An interface type meets a bound `T: I` only when every function of I can
// be called on a value of it (spec §12 *Interface values*), or the language
// defines I across implementors (Debug, Equatable, Hashable). Comparable's
// `compare` takes two `self` values, so a generic call bounded by
// Comparable at the interface type Comparable, an ordering whose impl's
// `where` needs it, and `Iter.sort` over a List of them are each an error
// at the instantiation. Each case fails if the source starts being
// admitted.
func TestInterfaceValueBound_ComparableIsNotMet(t *testing.T) {
	const lt = "fn lt<T>(a: T, b: T) -> Bool where T: Comparable\n    a < b\nend\n\n"
	const tie = "interface Tie\n    fn tie(a: self, b: self) -> Bool\nend\n\nfn tied<T>(a: T, b: T) -> Bool where T: Tie\n    Tie.tie(a, b)\nend\n\n"
	why := func(held, iface, fn string) string {
		return "`" + held + "` does not meet the bound `" + iface + "`, since `" + iface + "." + fn + "` cannot be called on " +
			analysis.ArticleFor(held) + " `" + held + "` value: its 2 `self` arguments must be one implementing type, and `" + held + "` values may hold different ones"
	}
	cases := []struct {
		name, src, want string
		line, col       int
	}{
		{"a generic call", lt + "fn f(x: Comparable, y: Comparable) -> Bool\n    lt(x, y)\nend\n",
			why("Comparable", "Comparable", "compare"), 6, 7},
		{"a conjunction", lt + "fn f(x: Display and Comparable, y: Display and Comparable) -> Bool\n    lt(x, y)\nend\n",
			why("Comparable and Display", "Comparable", "compare"), 6, 7},
		{"an explicit type argument", lt + "fn f() -> Bool\n    lt<Comparable>(1, 2)\nend\n",
			why("Comparable", "Comparable", "compare"), 6, 19},
		{"a List ordering", "fn f(x: Comparable, y: Comparable) -> Bool\n    [x] < [y]\nend\n",
			why("Comparable", "Comparable", "compare"), 2, 9},
		{"Iter.sort", "fn f(xs: List<Comparable>) -> List<Comparable>\n    Iter.sort(xs) |> Iter.to_list()\nend\n",
			why("Comparable", "Comparable", "compare"), 2, 14},
		{"a program's interface", tie + "fn f(x: Tie, y: Tie) -> Bool\n    tied(x, y)\nend\n",
			why("Tie", "Tie", "tie"), 10, 9},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			errs := checkInterfaceValueOrdering(t, c.src)
			var hit *analysis.TypeError
			for i, e := range errs {
				if e.Message == c.want {
					hit = &errs[i]
				}
			}
			if hit == nil {
				t.Fatalf("want %q; got:\n  %s", c.want, joinMessages(errs))
			}
			if len(errs) != 1 {
				t.Errorf("want the one error; got:\n  %s", joinMessages(errs))
			}
			if hit.Line != c.line || hit.Col != c.col || hit.Code != analysis.InterfaceValueSelfCode {
				t.Errorf("the error is at %d:%d with code %q, want %d:%d with %q", hit.Line, hit.Col, hit.Code, c.line, c.col, analysis.InterfaceValueSelfCode)
			}
			if len(hit.Hints) != 1 || !strings.Contains(hit.Hints[0], "type parameter bounded by") {
				t.Errorf("hints = %q, want one naming a type parameter bounded by the interface", hit.Hints)
			}
		})
	}
}

// The interfaces the language defines across implementors, and those
// whose functions each take one `self`, are met by an interface type at a
// bound: Equatable and Hashable compare and hash the held values by their
// own types, Display's `to_string` takes one `self`, Debug is universal.
func TestInterfaceValueBound_OtherBoundsAreMet(t *testing.T) {
	src := `interface Named
    fn name(x: self) -> String
end

fn eq<T>(a: T, b: T) -> Bool where T: Equatable
    a == b
end

fn show<T>(a: T) -> String where T: Display
    Display.to_string(a)
end

fn named<T>(a: T) -> String where T: Named
    Named.name(a)
end

fn uses(x: Equatable, y: Equatable, d: Display, n: Named, h: Hashable) -> Bool
    m: Map<Hashable, Int> = {h => 1}
    eq(x, y) and [x] == [y] and show(d) != named(n) and Map.size(m) == 1
end
`
	if errs := checkInterfaceValueOrdering(t, src); len(errs) > 0 {
		t.Fatalf("the bounds an interface type meets are refused:\n  %s", joinMessages(errs))
	}
}
