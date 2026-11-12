package analysis_test

import (
	"strings"
	"testing"
)

// A generic call in a position with a known expected type solves its type
// parameters from that type before checking its arguments, so a dot-leading
// variant or an empty literal nested inside an argument is checked against
// the concrete parameter type: `Iter.to_map([(.North, .Cave)])` expected to
// be `Map<Direction, Place>` checks its list against
// `Iter<(Direction, Place)>`, and a collection literal checked against
// `Iter<T>` checks each item against T.

const seedEnums = `pub enum Direction
  North
  East
end
pub enum Place
  Cave
  Hall
end
`

func TestExpectedTypeSeed_StructField(t *testing.T) {
	src := seedEnums + `struct Room
  exits: Map<Direction, Place>
end
fn demo() -> Room
  Room{exits: Iter.to_map([(.North, .Cave), (.East, .Hall)])}
end`
	expectNoErrorsT(t, checkWithStdlib(src))
}

func TestExpectedTypeSeed_ArgumentOfNonGenericCallee(t *testing.T) {
	src := seedEnums + `fn take(m: Map<Direction, Place>) -> Int
    Map.size(m)
end
fn demo() -> Int
  take(Iter.to_map([(.North, .Cave)]))
end`
	expectNoErrorsT(t, checkWithStdlib(src))
}

func TestExpectedTypeSeed_ReturnTail(t *testing.T) {
	src := seedEnums + `fn exits() -> Map<Direction, Place>
  Iter.to_map([(.North, .Cave)])
end`
	expectNoErrorsT(t, checkWithStdlib(src))
}

func TestExpectedTypeSeed_ReturnStatement(t *testing.T) {
	src := seedEnums + `fn exits(early: Bool) -> Map<Direction, Place>
  if early then
    return Iter.to_map([(.East, .Hall)])
  end
  Iter.to_map([(.North, .Cave)])
end`
	expectNoErrorsT(t, checkWithStdlib(src))
}

func TestExpectedTypeSeed_AnnotatedBinding(t *testing.T) {
	src := seedEnums + `fn demo() -> Int
  exits: Map<Direction, Place> = Iter.to_map([(.North, .Cave)])
  Map.size(exits)
end`
	expectNoErrorsT(t, checkWithStdlib(src))
}

func TestExpectedTypeSeed_ListElement(t *testing.T) {
	src := seedEnums + `fn demo() -> List<Map<Direction, Place>>
  [Iter.to_map([(.North, .Cave)]), Iter.to_map([(.East, .Hall)])]
end`
	expectNoErrorsT(t, checkWithStdlib(src))
}

func TestExpectedTypeSeed_MapValue(t *testing.T) {
	src := seedEnums + `fn demo() -> Map<String, Map<Direction, Place>>
  {"hall" => Iter.to_map([(.North, .Cave)])}
end`
	expectNoErrorsT(t, checkWithStdlib(src))
}

func TestExpectedTypeSeed_TupleElement(t *testing.T) {
	src := seedEnums + `fn demo() -> (Map<Direction, Place>, Int)
  (Iter.to_map([(.North, .Cave)]), 1)
end`
	expectNoErrorsT(t, checkWithStdlib(src))
}

func TestExpectedTypeSeed_CaseArm(t *testing.T) {
	src := seedEnums + `fn exits(d: Direction) -> Map<Direction, Place>
  case d
    .North -> Iter.to_map([(.North, .Cave)])
    .East -> Iter.to_map([(.East, .Hall)])
  end
end`
	expectNoErrorsT(t, checkWithStdlib(src))
}

func TestExpectedTypeSeed_PipeStage(t *testing.T) {
	src := seedEnums + `fn exits() -> Map<Direction, Place>
  [(.North, .Cave)] |> Iter.to_map()
end`
	expectNoErrorsT(t, checkWithStdlib(src))
}

func TestExpectedTypeSeed_UserGenericFunction(t *testing.T) {
	src := seedEnums + `fn pairs<K, V>(xs: List<(K, V)>) -> Map<K, V>
    Iter.to_map(xs)
end
fn exits() -> Map<Direction, Place>
  pairs([(.North, .Cave)])
end`
	expectNoErrorsT(t, checkWithStdlib(src))
}

func TestExpectedTypeSeed_NestedConstructor(t *testing.T) {
	src := seedEnums + `fn demo() -> List<Maybe<Direction>>
  [Some(.North), None]
end`
	expectNoErrorsT(t, checkWithStdlib(src))
}

// Empty literals: nothing in the argument fixes its element type, so only the
// expected type can.
func TestExpectedTypeSeed_EmptyList_AnnotatedBinding(t *testing.T) {
	src := `fn demo() -> Int
  x: Map<String, List<Int>> = Iter.to_map([])
  Map.size(x)
end`
	expectNoErrorsT(t, checkWithStdlib(src))
}

func TestExpectedTypeSeed_EmptyList_StructField(t *testing.T) {
	src := `struct Index
  words: Map<String, List<Int>>
end
fn demo() -> Index
  Index{words: Iter.to_map([])}
end`
	expectNoErrorsT(t, checkWithStdlib(src))
}

func TestExpectedTypeSeed_EmptyList_PipeStage(t *testing.T) {
	src := `fn demo() -> Map<String, Int>
  [] |> Iter.to_map()
end`
	expectNoErrorsT(t, checkWithStdlib(src))
}

func TestExpectedTypeSeed_NoneArgument(t *testing.T) {
	src := seedEnums + `fn demo() -> Map<String, Maybe<Direction>>
  Iter.to_map([("a", None), ("b", Some(.East))])
end`
	expectNoErrorsT(t, checkWithStdlib(src))
}

// A real argument mismatch is still reported, against the element type the
// expected type solved.
func TestExpectedTypeSeed_ArgumentMismatchStillReported(t *testing.T) {
	src := seedEnums + `fn exits() -> Map<Direction, Place>
  Iter.to_map([(.North, 5)])
end`
	expectErrorContaining(t, checkWithStdlib(src),
		"list element type mismatch: expected (Direction, Place), got (Direction, Int)")
}

// An expected type the call's result cannot have does not seed anything: the
// arguments are checked as they would be without it, and the mismatch is
// reported at the binding.
func TestExpectedTypeSeed_UnrelatedExpectedTypeIsNotSeeded(t *testing.T) {
	src := `fn demo() -> Int
  x: Int = Iter.to_map([(1, "one")])
  x
end`
	expectErrorContaining(t, checkWithStdlib(src), "expected Int, got Map<Int, String>")
}

// A collection literal checked against an expected `Iter<T>` checks each item
// against T, at any depth, for a list, a vector and a set, called directly or
// as a pipe head.
const seedPayloads = seedEnums + `pub enum Exit
  Door(Place)
  Gap
end
`

func TestExpectedTypeSeed_IterParam_TupleItems(t *testing.T) {
	src := seedEnums + `fn exits() -> Map<Direction, Place>
  Iter.to_map([(.North, .Cave), (.East, .Hall)])
end`
	expectNoErrorsT(t, checkWithStdlib(src))
}

func TestExpectedTypeSeed_IterParam_PipeHead(t *testing.T) {
	src := seedEnums + `fn exits() -> Map<Direction, Place>
  [(.North, .Cave), (.East, .Hall)] |> Iter.to_map()
end`
	expectNoErrorsT(t, checkWithStdlib(src))
}

func TestExpectedTypeSeed_IterParam_NestedPayload(t *testing.T) {
	src := seedPayloads + `fn exits() -> Map<Direction, Maybe<Exit>>
  Iter.to_map([(.North, Some(.Door(.Cave))), (.East, None), (.East, Some(.Gap))])
end
fn piped() -> Map<Direction, Maybe<Exit>>
  [(.North, Some(.Door(.Hall)))] |> Iter.to_map()
end`
	expectNoErrorsT(t, checkWithStdlib(src))
}

func TestExpectedTypeSeed_IterParam_VectorAndSetLiterals(t *testing.T) {
	src := seedEnums + `fn v() -> Vector<(Direction, Place)>
  Iter.to_vector(Vector[(.North, .Cave)])
end
fn s() -> Set<Direction>
  Set[.North, .East] |> Iter.to_set()
end
fn l() -> List<Direction>
  Iter.to_list([.North])
end`
	expectNoErrorsT(t, checkWithStdlib(src))
}

func TestExpectedTypeSeed_IterParam_EmptyLiteral(t *testing.T) {
	src := `fn demo() -> Map<String, List<Int>>
  Iter.to_map([])
end`
	expectNoErrorsT(t, checkWithStdlib(src))
}

// An item that does not fit T is reported once, against T, and a `.Variant`
// is not also reported against the call's own expected type.
func TestExpectedTypeSeed_IterParam_ItemMismatch(t *testing.T) {
	errs := checkWithStdlib(seedEnums + `fn exits() -> Map<Direction, Place>
  Iter.to_map([(.North, 5)])
end`)
	expectErrorContaining(t, errs,
		"list element type mismatch: expected (Direction, Place), got (Direction, Int)")
	for _, e := range errs {
		if strings.Contains(e.Message, "resolves only to enum variants") {
			t.Fatalf("a variant was checked against the call's expected type: %q", e.Message)
		}
	}
}

// A bare `None` beside typed items takes their type in a vector or set
// literal, as it does in a list literal, in either order.
func TestCollectionLiteral_BareNoneTakesTheItemsType(t *testing.T) {
	src := `fn v() -> Vector<Maybe<Int>>
  x = Vector[Some(1), None]
  x
end
fn w() -> Vector<Maybe<String>>
  y = Vector[None, Some("a")]
  y
end
fn s() -> Set<Maybe<Int>>
  z = Set[None, Some(2)]
  z
end`
	expectNoErrorsT(t, checkWithStdlib(src))
	expectErrorContaining(t, checkWithStdlib(`fn main()
  _ = Vector[Some(1), Some("a")]
end`), "vector element type mismatch: expected Maybe<Int>, got Maybe<String>")
}

// A function named as a value and a list literal of several items take
// their type from a generic call's expected type, as a lambda does: the
// seed solves Some's T, so `Display.to_string` and `ident` are instantiated
// at `(Int) -> String` and `(Int) -> Int` (§7), and each of the list's items
// widens to Display. Without the seed the reference had nothing to solve its
// type parameter from, and the list took its first item's type.
func TestExpectedTypeSeed_FunctionValueAndWideningList(t *testing.T) {
	src := `fn ident<T>(x: T) -> T
  x
end
fn demo() -> Int
  a: Maybe<(Int) -> String> = Some(Display.to_string)
  b: Maybe<List<Display>> = Some([1, "a"])
  c: Maybe<(Int) -> Int> = Some(ident)
  d: Maybe<(Int) -> Int> = Some(fn x -> x * 3)
  _ = (a, b, c, d)
  1
end`
	expectNoErrorsT(t, checkWithStdlib(src))
	expectErrorContaining(t, checkWithStdlib(`fn demo() -> Maybe<List<Int>>
  Some([1, "a"])
end`), "list element type mismatch: expected Int, got String")
	expectErrorContaining(t, checkWithStdlib(`fn demo() -> Int
  f: Maybe<(Int) -> Int> = Some(Display.to_string)
  _ = f
  1
end`), "argument 1: expected (Int) -> Int, got (Int) -> String")
}

// A dot-leading struct variant inside a constructor's argument takes the
// enum from the call's expected type, as the call form `.Tagged(1)` does:
// `Ok(.Square{side:})` in a function returning `Result<Shape, String>`.
func TestExpectedTypeSeed_DotStructVariantInAConstructor(t *testing.T) {
	src := `pub enum Shape
  Circle {radius: Int}
  Square {side: Int}
end
pub enum Holder
  Holds(Shape)
end
fn ok(side: Int) -> Result<Shape, String>
  Ok(.Square{side:})
end
fn err() -> Result<Int, Shape>
  Err(.Circle{radius: 1})
end
fn some() -> Maybe<Shape>
  Some(.Square{side: 1})
end
fn held() -> Holder
  .Holds(.Circle{radius: 2})
end`
	expectNoErrorsT(t, checkWithStdlib(src))
}
