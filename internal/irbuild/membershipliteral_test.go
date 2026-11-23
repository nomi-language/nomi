package irbuild

import "testing"

// A membership test whose collection is a literal that leaves part of its
// element type open (`[Ok(25)]` is a List<Result<Int, ?E>>, `[]` a
// List<?T>) takes it from the value it looks for, as the impl of Contains
// it reaches is matched with the value's type first: `in` over a List, a
// Vector, a Set and a Map's keys, with elements of Result, Option<Result>,
// Result<Option>, a tuple holding a Result and a user enum with a generic
// payload, and the same calls written out. List's and Vector's Contains
// hold where the element is Equatable, which Result<Int, ?E> alone cannot
// be checked at.
func TestIRMembershipLiteralTakesTheValuesType_Run(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io

enum Tagged<T>
    Tag(T)
    Blank
end

derive Equatable for Tagged<T>

derive Hashable for Tagged<T>

fn main()
    a: Result<Int, String> = Ok(8)
    io.inspect(a in [Ok(25), Ok(8)])
    io.inspect(not a in [Ok(25), Ok(40)])
    io.inspect(a in Vector[Ok(25), Ok(8)])
    io.inspect(not a in Vector[Ok(25)])
    io.inspect(a in Set[Ok(8)])
    io.inspect(a in {Ok(8) => 1})
    io.inspect(a in [])
    io.inspect(a in Vector[])
    b: Option<Result<Int, String>> = Some(Err("x"))
    io.inspect(b in [Some(Ok(1)), Some(Err("x"))])
    io.inspect(b in Vector[Some(Ok(1)), None])
    io.inspect(b in Set[None])
    c: Result<Option<Int>, String> = Ok(None)
    io.inspect(c in [Ok(Some(1)), Ok(None)])
    io.inspect(c in Vector[Err("no")])
    io.inspect(c in {Ok(None) => 0})
    d: (Int, Result<Int, String>) = (1, Ok(2))
    io.inspect(d in [(1, Ok(2))])
    io.inspect(d in Vector[(1, Ok(3))])
    io.inspect(d in Set[(1, Ok(2))])
    e: Tagged<Result<Int, String>> = Tagged.Tag(Ok(3))
    io.inspect(e in [Tagged.Tag(Ok(3)), Tagged.Blank])
    io.inspect(e in Vector[Tagged.Blank])
    io.inspect(e in {Tagged.Blank => 0})
    o: Option<String> = Some("x")
    io.inspect(o in [None, None])
    io.inspect(Contains.contains([Ok(25), Ok(8)], a))
    io.inspect(List.contains([Ok(25)], a))
    io.inspect(Vector.contains(Vector[Ok(8)], a))
    io.inspect(Iter.contains([Ok(25), Ok(8)], a))
    io.inspect(a == Ok(8))
    io.inspect(Err("x") != a)
end
`
	const want = "True\nTrue\nTrue\nTrue\nTrue\nTrue\nFalse\nFalse\n" +
		"True\nFalse\nFalse\n" +
		"True\nFalse\nTrue\n" +
		"True\nFalse\nTrue\n" +
		"True\nFalse\nFalse\n" +
		"False\n" +
		"True\nFalse\nTrue\nTrue\nTrue\nTrue\n"
	irRunSource(t, src, want)
}
