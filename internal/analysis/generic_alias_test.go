package analysis_test

import (
	"strings"
	"testing"
)

// Generic typealiases and aliases of `A and B` (spec §4 *Type Aliases*): an
// alias is its target at the use's type arguments, with no conversion, and
// a use whose argument breaks the alias's bound is an error there.

const aliasDecls = `interface Priced
    fn price(p: self) -> Int
end

interface Costed
    fn cost(p: self) -> Int
end

struct Coffee
    beans: Int
end

impl Priced for Coffee
    fn price(c: Coffee) -> Int
        c.beans * 3
    end
end

impl Costed for Coffee
    fn cost(c: Coffee) -> Int
        c.beans
    end
end

struct Circle
    r: Int
end

typealias Pair<T> (T, T)

typealias Sortable<T> List<T> where T: Comparable

typealias Item Priced and Costed

typealias Tagged<T> (T, Priced and Costed)

typealias Both NonNegativeInt and NonZeroInt

`

func TestGenericAlias_Accepted(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"a use is its target", "fn f(p: Pair<Int>) -> (Int, Int)\n    p\nend\n\nfn g(p: (Int, Int)) -> Pair<Int>\n    p\nend\n"},
		{"a bounded use", "fn f(xs: Sortable<Int>) -> Option<Int>\n    Iter.min(xs)\nend\n"},
		{"an alias of an interface intersection as a parameter", "fn f(i: Item) -> Int\n    Priced.price(i) - Costed.cost(i)\nend\n\nfn g() -> Int\n    f(Coffee{beans: 2})\nend\n"},
		{"a generic alias whose target holds `and`", "fn f(t: Tagged<String>) -> Int\n    (_, item) = t\n    Priced.price(item)\nend\n\nfn g() -> Int\n    f((\"a\", Coffee{beans: 1}))\nend\n"},
		{"an alias of a refinement intersection", "fn f(n: Both) -> NonZeroInt\n    n\nend\n\nfn g() -> Int\n    f(3)\nend\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if errs := checkRefinementDecls(t, aliasDecls+tc.src); len(errs) > 0 {
				t.Fatalf("errors: %v", errs)
			}
		})
	}
}

func TestGenericAlias_Rejected(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"a use that breaks the bound", "fn f(xs: Sortable<Circle>) -> Int\n    List.length(xs)\nend\n",
			"Circle does not implement Comparable (required by `where T: Comparable` on typealias Sortable)"},
		{"a use with no type arguments", "fn f(p: Pair) -> Int\n    0\nend\n",
			"typealias Pair takes 1 type argument: write `Pair<T>`"},
		{"an alias of a mixed intersection", "typealias Bad Int and String\n",
			"`and` joins interfaces or refinements, and Int is neither"},
		{"the same intersection inline", "fn f(x: Int and String) -> Int\n    0\nend\n",
			"`and` joins interfaces or refinements, and Int is neither"},
		{"an alias is no other type", "fn f(p: Pair<Int>) -> (Int, String)\n    p\nend\n",
			"return type mismatch: expected (Int, String), got (Int, Int)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := checkRefinementDecls(t, aliasDecls+tc.src)
			var msgs []string
			for _, e := range errs {
				if strings.HasPrefix(diagText(e), tc.want) {
					return
				}
				msgs = append(msgs, diagText(e))
			}
			if len(errs) == 0 {
				t.Fatalf("the checker accepts this; want %q", tc.want)
			}
			t.Fatalf("errors = %q, want one reading %q", msgs, tc.want)
		})
	}
}
