package analysis_test

import "testing"

// A generic call's type arguments come from its arguments first; the
// expected type fills only what they leave open, and the call's result
// converts to it (spec §5 *Type arguments from the arguments*).

// The two programs the rule was made for: a lambda's parameter is the
// element the head gives, not the interface the binding names.
func TestArgsFirst_LambdaParameterIsTheArgumentsType(t *testing.T) {
	src := `fn demo() -> Int
    x: Iter<Display> = [1, 2] |> Iter.filter(fn n -> n > 1)
    d: Display = Iter.loop(0, fn i -> if i > 2 then break i else i + 1)
    Iter.count(x)
end`
	expectNoErrorsT(t, checkWithStdlib(src))
}

// The direct-call spelling, and an accumulator the expected type used to
// make a Display.
func TestArgsFirst_DirectCallAndAccumulator(t *testing.T) {
	src := `fn demo() -> Int
    x: Iter<Display> = Iter.filter([1, 2], fn n -> n > 1)
    s: Display = Iter.reduce([1, 2, 3], 0, fn acc, n -> acc + n)
    Iter.count(x)
end`
	expectNoErrorsT(t, checkWithStdlib(src))
}

// What the arguments leave open still comes from the expected type: an
// empty literal's element, a lambda parameter nothing else types, a
// dot-leading variant, and a type argument inside a Maybe, where a value
// converts only in place.
func TestArgsFirst_ExpectedTypeFillsWhatTheArgumentsLeaveOpen(t *testing.T) {
	src := `enum Color
    Red
    Blue
end
fn ident<T>(x: T) -> T
    x
end
fn demo() -> Int
    xs: List<Int> = Iter.to_list([])
    f: (Int) -> Int = ident(fn x -> x + 1)
    c: Maybe<Color> = Some(.Red)
    m: Maybe<Display> = Some([1])
    f(Iter.count(xs))
end`
	expectNoErrorsT(t, checkWithStdlib(src))
}

// Arguments whose type does not convert to the expected one are an error
// at the call, against the type the arguments gave it. The expected type
// used to make `"a"` the mismatch, as `argument 1: expected Int, got String`.
func TestArgsFirst_ArgumentsTypeThatDoesNotConvertIsAnError(t *testing.T) {
	src := `fn demo() -> Int
    n: Int = Iter.loop("a", fn s -> if s == "" then s else break s)
    n
end`
	expectErrorContaining(t, checkWithStdlib(src), "type mismatch: expected Int, got String")
}

// A range literal's endpoints are its arguments: what both leave open, as
// two empty lists do, comes from the expected Range's element.
func TestArgsFirst_RangeEndpointsTakeTheExpectedElement(t *testing.T) {
	src := `fn demo() -> Bool
    c: Range<List<Int>> = []..[]
    Range.contains(c, [1])
end`
	expectNoErrorsT(t, checkWithStdlib(src))
}
