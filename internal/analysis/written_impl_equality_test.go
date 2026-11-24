package analysis_test

import (
	"slices"
	"testing"
)

// writtenEqualityDecls declares Op, which holds a function and implements
// Unit2; Measure<P>, whose written Equatable and Hashable ask only `P:
// Unit2` and leave P out; Shaped<P>, whose Equatable is a `host fn` and so
// answers by its shape; Box<T>, which derives Equatable; and Bag<T>, which
// has no impl. It ends inside `fn main`, where inc is a function and m a
// Measure<Op>.
const writtenEqualityDecls = `interface Unit2
    fn symbol(unit: self) -> String
end

struct Op
    run: (Int) -> Int
end

impl Unit2 for Op
    fn symbol(_unit: Op) -> String
        "op"
    end
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

struct Shaped<P>
    unit: P
end

impl Equatable for Shaped<P>
    host fn equal(a: Shaped<P>, b: Shaped<P>) -> Bool
end

struct Box<T>
    value: T
end

derive Equatable for Box<T>

struct Bag<T>
    value: T
end

fn same<T>(a: T, b: T) -> Bool where T: Equatable
    a == b
end

fn key<T>(a: T) -> Int where T: Hashable
    Hashable.hash(a)
end

fn main()
    inc: (Int) -> Int = fn x -> x + 1
    m = Measure{amount: 1, unit: Op{run: inc}}
    _ = inc
    _ = m
`

// A hand-written `impl Equatable` or `impl Hashable` decides what is
// compared (analysis.ImplAsksParts), so a Measure<Op> whose Op holds a
// function has equality and a hash at every route: `==`, `in`, a
// container, a derive and a type with no impl over it, a bound and a key.
// A value with no equality outside such an impl stays refused: a function
// in a List, an Option, a derived type or a type with no impl, and under
// an impl that is a `host fn`, which answers by shape. The where clause
// and a derive over a function, which the whole front end reports, are
// vmhost's TestWrittenImplEquality_StillRefuses.
func TestWrittenImplEquality_DecidesWhatIsCompared(t *testing.T) {
	ok := []string{
		`m == m`,
		`[m] == [m]`,
		`m in [m]`,
		`Some(m) == Some(m)`,
		`Equatable.equal(m, m)`,
		`List.equal([m], [m])`,
		`Vector[m] == Vector[m]`,
		`Box{value: m} == Box{value: m}`,
		`Bag{value: m} == Bag{value: m}`,
		`same(m, m)`,
		`same([Some(m)], [])`,
		`Hashable.hash(m)`,
		`key([m])`,
		`Map.get({m => 1}, m)`,
		`Set.size(Set[m, m])`,
	}
	for _, expr := range ok {
		t.Run(expr, func(t *testing.T) {
			if errs := equalityBoundErrors(writtenEqualityDecls + "    _ = " + expr + "\nend\n"); len(errs) != 0 {
				t.Fatalf("want no error, got %q", errs)
			}
		})
	}
	refused := map[string]string{
		`List.equal([inc], [inc])`:                                 "List<(Int) -> Int> does not implement Equatable (required by `List.equal`) | `List<(Int) -> Int>` holds `(Int) -> Int`, and a function value has no equality",
		`[inc] == [inc]`:                                           "`==` cannot compare `List<(Int) -> Int>`: it holds `(Int) -> Int`, and a function value has no equality",
		`inc in [inc]`:                                             "no impl of `Equatable` for `(Int) -> Int` (required via membership test `in`) | a function value has no equality",
		`Some(inc) == Some(inc)`:                                   "`==` cannot compare `Option<(Int) -> Int>`: it holds `(Int) -> Int`, and a function value has no equality",
		`Box{value: inc} == Box{value: inc}`:                       "`==` cannot compare `Box<(Int) -> Int>`: it holds `(Int) -> Int`, and a function value has no equality",
		`Bag{value: inc} == Bag{value: inc}`:                       "`==` cannot compare `Bag<(Int) -> Int>`: it holds `(Int) -> Int`, and a function value has no equality",
		`Shaped{unit: Op{run: inc}} == Shaped{unit: Op{run: inc}}`: "`==` cannot compare `Shaped<Op>`: it holds `(Int) -> Int`, and a function value has no equality",
		`{Shaped{unit: Op{run: inc}} => 1}`:                        "`Shaped<Op>` cannot be a Map key: it holds `(Int) -> Int`, and a function value has no equality or hashing",
	}
	for expr, want := range refused {
		t.Run(expr, func(t *testing.T) {
			errs := equalityBoundErrors(writtenEqualityDecls + "    _ = " + expr + "\nend\n")
			if len(errs) == 0 {
				t.Fatalf("the program is admitted; want %q", want)
			}
			if !slices.Equal(errs, []string{want}) {
				t.Fatalf("got %q\nwant [%q]", errs, want)
			}
		})
	}
}
