package analysis_test

import (
	"slices"
	"testing"
)

// writtenImplBoundDecls declares Measure<P>, whose written Display and
// Comparable ask only `P: Unit2`, and Box<T>, whose derived Display writes
// no function. Feet implements Unit2 and nothing else; Bare implements
// nothing.
const writtenImplBoundDecls = `interface Unit2
    fn symbol(unit: self) -> String
end

struct Feet
    n: Int
end

impl Unit2 for Feet
    fn symbol(_unit: Feet) -> String
        "ft"
    end
end

struct Bare
    n: Int
end

struct Measure<P>
    amount: Int
    unit: P
end

impl Display for Measure<P> where P: Unit2
    fn to_string(m: Measure<P>) -> String
        "${m.amount} ${Unit2.symbol(m.unit)}"
    end
end

impl Equatable for Measure<P> where P: Unit2
    fn equal(a: Measure<P>, b: Measure<P>) -> Bool
        a.amount == b.amount
    end
end

impl Comparable for Measure<P> where P: Unit2
    fn compare(a: Measure<P>, b: Measure<P>) -> Ordering
        Comparable.compare(a.amount, b.amount)
    end
end

struct Box<T>
    value: T
end

derive Display for Box<T>

fn shown<T>(x: T) -> String where T: Display
    Display.to_string(x)
end

`

// The demand a use records of a type with a written impl stops at that
// impl, whose where bounds are its demands of the type arguments
// (analysis.ImplAsksParts): a Measure<Feet> is Display and Comparable at
// every route with no Display or Comparable of Feet, and a Measure<Bare>
// is one error, its Bare's missing Unit2. A derived Display that writes no
// function still demands Display of its part.
func TestWrittenImplBound_DemandsStopAtTheWrittenImpl(t *testing.T) {
	ok := []string{
		`Display.to_string(Measure{amount: 1, unit: Feet{n: 0}})`,
		`"${Measure{amount: 1, unit: Feet{n: 0}}}"`,
		`shown(Measure{amount: 1, unit: Feet{n: 0}})`,
		`Display.to_string(Some(Measure{amount: 1, unit: Feet{n: 0}}))`,
		`"${[Measure{amount: 1, unit: Feet{n: 0}}]}"`,
		`Display.to_string(Box{value: Measure{amount: 1, unit: Feet{n: 0}}})`,
		`Measure{amount: 1, unit: Feet{n: 0}} < Measure{amount: 2, unit: Feet{n: 0}}`,
		`Comparable.compare([Measure{amount: 1, unit: Feet{n: 0}}], [])`,
		`Iter.sort([Measure{amount: 1, unit: Feet{n: 0}}])`,
	}
	for _, expr := range ok {
		t.Run(expr, func(t *testing.T) {
			if errs := equalityBoundErrors(writtenImplBoundDecls + "fn main()\n    _ = " + expr + "\nend\n"); len(errs) != 0 {
				t.Fatalf("want no error, got %q", errs)
			}
		})
	}
	refused := map[string]string{
		`Display.to_string(Measure{amount: 1, unit: Bare{n: 0}})`:                     "no impl of `Unit2` for `Bare` (required by the call `Display.to_string`) | `Measure<P>`'s `Display` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block",
		`"${[Measure{amount: 1, unit: Bare{n: 0}}]}"`:                                 "no impl of `Unit2` for `Bare` (required to interpolate it) | `Measure<P>`'s `Display` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block",
		`Measure{amount: 1, unit: Bare{n: 0}} < Measure{amount: 2, unit: Bare{n: 0}}`: "no impl of `Unit2` for `Bare` (required via ordering operator `<`) | `Measure<P>`'s `Comparable` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block",
		`Display.to_string(Box{value: Feet{n: 0}})`:                                   "no impl of `Display` for `Feet` (required by the call `Display.to_string`) | add `derive Display` to `Feet` or write an `impl Display for Feet` block",
	}
	for expr, want := range refused {
		t.Run(expr, func(t *testing.T) {
			errs := equalityBoundErrors(writtenImplBoundDecls + "fn main()\n    _ = " + expr + "\nend\n")
			if len(errs) == 0 {
				t.Fatalf("the program is admitted; want %q", want)
			}
			if !slices.Equal(errs, []string{want}) {
				t.Fatalf("got %q\nwant [%q]", errs, want)
			}
		})
	}
}
