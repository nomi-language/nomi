package analysis_test

import (
	"strings"
	"testing"
)

// `TryFrom` (spec §12 *TryFrom*): a `type` with a rule in its chain
// implements `TryFrom` of its base, which cannot be written by hand; impls
// from other source types are the program's.

const tryFromDecls = `type Percent Int when fn n -> n >= 0 and n <= 100

type Celsius Float when fn c -> c >= -273.15

type Kelvin Float when fn k -> k >= 0.0

impl TryFrom<Celsius> for Kelvin
    fn try_from(Celsius(c)) -> Maybe<Kelvin>
        Kelvin.try_from(c + 273.15)
    end
end

enum Weekday
    Monday
    Tuesday
end

impl TryFrom<Int> for Weekday
    fn try_from(value: Int) -> Maybe<Weekday>
        case value
            1 -> Some(Weekday.Monday)
            2 -> Some(Weekday.Tuesday)
            _ -> None
        end
    end
end

fn load<T>(n: Int) -> Maybe<T> where T: TryFrom<Int>
    T.try_from(n)
end

`

func TestTryFrom_Accepted(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"a rule's type through a TryFrom bound", "fn f() -> Maybe<Percent>\n    load(7)\nend\n"},
		{"a type over a subtype through a TryFrom bound", "fn f() -> Maybe<UserId>\n    load(7)\nend\n"},
		{"a rule that reads a name `value`", "once value = 5\n\ntype Under Int when fn n -> n < value\n\nfn f() -> Maybe<Under>\n    load(3)\nend\n"},
		{"a generic validated type", "type Short<T> List<T> when fn xs -> List.length(xs) < 3\n\nfn take<T>(xs: List<Int>) -> Maybe<T> where T: TryFrom<List<Int>>\n    T.try_from(xs)\nend\n\nfn f() -> Maybe<Short<Int>>\n    take([1])\nend\n"},
		{"a hand-written impl on an enum", "fn f() -> Maybe<Weekday>\n    load(2)\nend\n"},
		{"try_from resolves by argument type", "fn f(c: Celsius) -> (Maybe<Kelvin>, Maybe<Kelvin>)\n    (Kelvin.try_from(c), Kelvin.try_from(3.0))\nend\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if errs := checkSubtypes(t, tryFromDecls+tc.src); len(errs) > 0 {
				t.Fatalf("errors: %v", errs)
			}
		})
	}
}

func TestTryFrom_Rejected(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"a type with no rule fails the bound", "fn f() -> Maybe<Plain>\n    load(7)\nend\n",
			"Plain does not implement TryFrom<Int> (required by `where T: TryFrom<Int>`)"},
		{"a type over a prelude type that is no subtype has no try_from", "type Ord2 Ordering\n\nfn f() -> Maybe<Ord2>\n    Ord2.try_from(Ordering.Less)\nend\n",
			"type 'Ord2' has no member 'try_from'"},
		{"nor a TryFrom impl", "type Ord2 Ordering\n\nfn take<T>(o: Ordering) -> Maybe<T> where T: TryFrom<Ordering>\n    T.try_from(o)\nend\n\nfn f() -> Maybe<Ord2>\n    take(Ordering.Less)\nend\n",
			"Ord2 does not implement TryFrom<Ordering>"},
		{"the rule's impl written by hand", "impl TryFrom<Int> for Percent\n    fn try_from(_value: Int) -> Maybe<Percent>\n        None\n    end\nend\n",
			"`TryFrom<Int>` for Percent is supplied by its rule and cannot be implemented; every `type` with a rule over Int satisfies `TryFrom<Int>` automatically — remove this `impl TryFrom<Int> for Percent`"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := checkSubtypes(t, tryFromDecls+tc.src)
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
