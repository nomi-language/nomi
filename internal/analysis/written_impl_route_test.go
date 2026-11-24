package analysis_test

import (
	"slices"
	"strings"
	"testing"
)

// writtenImplRouteDecls declares Measure<P>, whose written Equatable,
// Hashable, Comparable, Display and Debug each ask only `P: Unit2`, and
// helpers that reach them through a bound and an interface-typed
// parameter. Feet implements Unit2; Bare implements nothing.
const writtenImplRouteDecls = `interface Unit2
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

impl Equatable for Measure<P> where P: Unit2
    fn equal(a: Measure<P>, b: Measure<P>) -> Bool
        a.amount == b.amount
    end
end

impl Hashable for Measure<P> where P: Unit2
    fn hash(m: Measure<P>) -> Int
        m.amount
    end
end

impl Comparable for Measure<P> where P: Unit2
    fn compare(a: Measure<P>, b: Measure<P>) -> Ordering
        Comparable.compare(a.amount, b.amount)
    end
end

impl Display for Measure<P> where P: Unit2
    fn to_string(m: Measure<P>) -> String
        "${m.amount} ${Unit2.symbol(m.unit)}"
    end
end

impl Debug for Measure<P> where P: Unit2
    fn inspect(m: Measure<P>) -> String
        "Measure(${m.amount})"
    end
end

fn same<T>(x: T) -> Bool where T: Equatable
    x == x
end

fn shown(x: Display) -> String
    Display.to_string(x)
end

fn equals(x: Equatable) -> Bool
    x == x
end

`

// writtenImplRoutes is every route that reaches a written impl of Measure
// at m, a Measure, alone and nested: an operator, a key, a rendering, a
// bound, an interface-typed parameter and a qualified call, each with
// m in a List, an Option, a tuple, a record or a Map where it applies.
// Each refused entry is the message and hints of the one error a
// Measure<Bare> draws there.
var writtenImplRoutes = []struct{ expr, refused string }{
	{"m == m", "no impl of `Unit2` for `Bare` (required by a `where` bound) | `Measure<P>`'s `Equatable` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},
	{"m != m", "no impl of `Unit2` for `Bare` (required by a `where` bound) | `Measure<P>`'s `Equatable` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},
	{"[m] == [m]", "no impl of `Unit2` for `Bare` (required by a `where` bound) | `Measure<P>`'s `Equatable` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},
	{"[[m]] != [[m]]", "no impl of `Unit2` for `Bare` (required by a `where` bound) | `Measure<P>`'s `Equatable` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},
	{"Some(m) == Some(m)", "no impl of `Unit2` for `Bare` (required by a `where` bound) | `Measure<P>`'s `Equatable` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},
	{"(m, 1) == (m, 1)", "no impl of `Unit2` for `Bare` (required by a `where` bound) | `Measure<P>`'s `Equatable` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},
	{"([m], 1) == ([m], 1)", "no impl of `Unit2` for `Bare` (required by a `where` bound) | `Measure<P>`'s `Equatable` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},
	{"{x: m} == {x: m}", "no impl of `Unit2` for `Bare` (required by a `where` bound) | `Measure<P>`'s `Equatable` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},
	{"m in [m]", "no impl of `Unit2` for `Bare` (required via membership test `in`) | `Measure<P>`'s `Equatable` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},
	{"Some(m) in [Some(m)]", "no impl of `Unit2` for `Bare` (required via membership test `in`) | `Measure<P>`'s `Equatable` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},
	{"m in Set[m]", "no impl of `Unit2` for `Bare` (required by a `where` bound) | `Measure<P>`'s `Equatable` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},
	{"m < m", "no impl of `Unit2` for `Bare` (required via ordering operator `<`) | `Measure<P>`'s `Comparable` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},
	{"m >= m", "no impl of `Unit2` for `Bare` (required via ordering operator `>=`) | `Measure<P>`'s `Comparable` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},
	{"[m] < [m]", "no impl of `Unit2` for `Bare` (required via ordering operator `<`) | `Measure<P>`'s `Comparable` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},
	{"Comparable.compare(m, m)", "no impl of `Unit2` for `Bare` (required by the call `Comparable.compare`) | `Measure<P>`'s `Comparable` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},
	{"Hashable.hash(m)", "no impl of `Unit2` for `Bare` (required by the call `Hashable.hash`) | `Measure<P>`'s `Hashable` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},
	{"Hashable.hash([m])", "no impl of `Unit2` for `Bare` (required by the call `Hashable.hash`) | `Measure<P>`'s `Hashable` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},
	{"Equatable.equal(([m], 1), ([m], 1))", "no impl of `Unit2` for `Bare` (required by the call `Equatable.equal`) | `Measure<P>`'s `Equatable` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},
	{"{m => 1}", "no impl of `Unit2` for `Bare` (required by a `where` bound) | `Measure<P>`'s `Equatable` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},
	{"{[m] => 1}", "no impl of `Unit2` for `Bare` (required by a `where` bound) | `Measure<P>`'s `Equatable` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},
	{"{(m, 1) => 1}", "no impl of `Unit2` for `Bare` (required by a `where` bound) | `Measure<P>`'s `Equatable` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},
	{"Set[m]", "no impl of `Unit2` for `Bare` (required by a `where` bound) | `Measure<P>`'s `Equatable` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},
	{"Set[Some(m)]", "no impl of `Unit2` for `Bare` (required by a `where` bound) | `Measure<P>`'s `Equatable` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},
	{"Map.get({m => 1}, m)", "no impl of `Unit2` for `Bare` (required by a `where` bound) | `Measure<P>`'s `Equatable` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},
	{`"${m}"`, "no impl of `Unit2` for `Bare` (required to interpolate it) | `Measure<P>`'s `Display` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},
	{`"${[m]}"`, "no impl of `Unit2` for `Bare` (required to interpolate it) | `Measure<P>`'s `Display` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},
	{`"${(m, 1)}"`, "no impl of `Unit2` for `Bare` (required to interpolate it) | `Measure<P>`'s `Display` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},
	{"dbg m", "no impl of `Unit2` for `Bare` (required via call site) | `Measure<P>`'s `Debug` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},
	{"dbg [m]", "no impl of `Unit2` for `Bare` (required via call site) | `Measure<P>`'s `Debug` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},
	{"dbg Some(m)", "no impl of `Unit2` for `Bare` (required via call site) | `Measure<P>`'s `Debug` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},
	{"dbg {m => 1}", "no impl of `Unit2` for `Bare` (required via call site) | `Measure<P>`'s `Debug` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},
	{"m |> dbg", "no impl of `Unit2` for `Bare` (required via call site) | `Measure<P>`'s `Debug` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},
	{"same(m)", "no impl of `Unit2` for `Bare` (required by a `where` bound) | `Measure<P>`'s `Equatable` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},
	{"same([m])", "no impl of `Unit2` for `Bare` (required by a `where` bound) | `Measure<P>`'s `Equatable` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},
	{"same(([m], 1))", "no impl of `Unit2` for `Bare` (required by a `where` bound) | `Measure<P>`'s `Equatable` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},
	{"shown(m)", "no impl of `Unit2` for `Bare` (required by parameter `x` of `shown`) | `Measure<P>`'s `Display` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},
	{"shown([m])", "no impl of `Unit2` for `Bare` (required by parameter `x` of `shown`) | `Measure<P>`'s `Display` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},
	{"equals(m)", "no impl of `Unit2` for `Bare` (required by parameter `x` of `equals`) | `Measure<P>`'s `Equatable` impl requires `P: Unit2` | write an `impl Unit2 for Bare` block"},
}

// Every route that reaches a written impl asks its where clause, and only
// that: a Measure<Feet> passes each, and a Measure<Bare> is one error at
// each, naming Bare's missing Unit2, which never reaches the IR builder.
func TestWrittenImplRoute_EveryRouteAsksTheWhereClause(t *testing.T) {
	for _, r := range writtenImplRoutes {
		t.Run(r.expr, func(t *testing.T) {
			at := func(unit string) []string {
				src := writtenImplRouteDecls + "fn main()\n    m = Measure{amount: 1, unit: " + unit + "{n: 0}}\n    _ = " + r.expr + "\nend\n"
				return equalityBoundErrors(src)
			}
			if errs := at("Feet"); len(errs) != 0 {
				t.Errorf("at Feet: want no error, got %q", errs)
			}
			errs := at("Bare")
			if len(errs) == 0 {
				t.Fatalf("at Bare: the program is admitted; want %q", r.refused)
			}
			if !slices.Equal(errs, []string{r.refused}) {
				t.Fatalf("at Bare: got %q\nwant [%q]", errs, r.refused)
			}
		})
	}
}

// A key is found by `==` and its hash (spec §5 *Hashing and keys*), so its
// demand asks no impl of the key's type: a type with a written Equatable
// and no Hashable hashes every value alike, and keys a Map though it holds
// a function, as `==` compares it.
func TestWrittenImplRoute_AKeyAsksNoImplOfItsType(t *testing.T) {
	src := strings.Replace(writtenImplRouteDecls, `impl Hashable for Measure<P> where P: Unit2
    fn hash(m: Measure<P>) -> Int
        m.amount
    end
end
`, `struct Op
    run: (Int) -> Int
end

impl Unit2 for Op
    fn symbol(_unit: Op) -> String
        "op"
    end
end
`, 1) + "fn main()\n    m = Measure{amount: 1, unit: Op{run: fn x -> x}}\n    _ = Map.get({m => 1}, m)\n    _ = Set[m, m]\n    _ = m == m\nend\n"
	if !strings.Contains(src, "struct Op") {
		t.Fatal("the declarations no longer hold the written Hashable this test replaces")
	}
	if errs := equalityBoundErrors(src); len(errs) != 0 {
		t.Fatalf("want no error, got %q", errs)
	}
}
