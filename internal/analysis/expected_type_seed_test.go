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

const seedEnums = `pub enum Direction {
  North
  East
}
pub enum Place {
  Cave
  Hall
}
`

func TestExpectedTypeSeed_StructField(t *testing.T) {
	src := seedEnums + `struct Room {
  exits: Map<Direction, Place>
}
fn demo(): Room {
  Room{exits: Iter.to_map([(.North, .Cave), (.East, .Hall)])}
}`
	expectNoErrorsT(t, checkWithStdlib(src))
}

func TestExpectedTypeSeed_ArgumentOfNonGenericCallee(t *testing.T) {
	src := seedEnums + `fn take(m: Map<Direction, Place>): Int { Map.size(m) }
fn demo(): Int {
  take(Iter.to_map([(.North, .Cave)]))
}`
	expectNoErrorsT(t, checkWithStdlib(src))
}

func TestExpectedTypeSeed_ReturnTail(t *testing.T) {
	src := seedEnums + `fn exits(): Map<Direction, Place> {
  Iter.to_map([(.North, .Cave)])
}`
	expectNoErrorsT(t, checkWithStdlib(src))
}

func TestExpectedTypeSeed_ReturnStatement(t *testing.T) {
	src := seedEnums + `fn exits(early: Bool): Map<Direction, Place> {
  if early {
    return Iter.to_map([(.East, .Hall)])
  }
  Iter.to_map([(.North, .Cave)])
}`
	expectNoErrorsT(t, checkWithStdlib(src))
}

func TestExpectedTypeSeed_AnnotatedBinding(t *testing.T) {
	src := seedEnums + `fn demo(): Int {
  exits: Map<Direction, Place> = Iter.to_map([(.North, .Cave)])
  Map.size(exits)
}`
	expectNoErrorsT(t, checkWithStdlib(src))
}

func TestExpectedTypeSeed_ListElement(t *testing.T) {
	src := seedEnums + `fn demo(): List<Map<Direction, Place>> {
  [Iter.to_map([(.North, .Cave)]), Iter.to_map([(.East, .Hall)])]
}`
	expectNoErrorsT(t, checkWithStdlib(src))
}

func TestExpectedTypeSeed_MapValue(t *testing.T) {
	src := seedEnums + `fn demo(): Map<String, Map<Direction, Place>> {
  {"hall" => Iter.to_map([(.North, .Cave)])}
}`
	expectNoErrorsT(t, checkWithStdlib(src))
}

func TestExpectedTypeSeed_TupleElement(t *testing.T) {
	src := seedEnums + `fn demo(): (Map<Direction, Place>, Int) {
  (Iter.to_map([(.North, .Cave)]), 1)
}`
	expectNoErrorsT(t, checkWithStdlib(src))
}

func TestExpectedTypeSeed_CaseArm(t *testing.T) {
	src := seedEnums + `fn exits(d: Direction): Map<Direction, Place> {
  case d {
    .North -> Iter.to_map([(.North, .Cave)])
    .East -> Iter.to_map([(.East, .Hall)])
  }
}`
	expectNoErrorsT(t, checkWithStdlib(src))
}

func TestExpectedTypeSeed_PipeStage(t *testing.T) {
	src := seedEnums + `fn exits(): Map<Direction, Place> {
  [(.North, .Cave)] |> Iter.to_map()
}`
	expectNoErrorsT(t, checkWithStdlib(src))
}

func TestExpectedTypeSeed_UserGenericFunction(t *testing.T) {
	src := seedEnums + `fn pairs<K, V>(xs: List<(K, V)>): Map<K, V> { Iter.to_map(xs) }
fn exits(): Map<Direction, Place> {
  pairs([(.North, .Cave)])
}`
	expectNoErrorsT(t, checkWithStdlib(src))
}

func TestExpectedTypeSeed_NestedConstructor(t *testing.T) {
	src := seedEnums + `fn demo(): List<Maybe<Direction>> {
  [Some(.North), None]
}`
	expectNoErrorsT(t, checkWithStdlib(src))
}

// Empty literals: nothing in the argument fixes its element type, so only the
// expected type can.
func TestExpectedTypeSeed_EmptyList_AnnotatedBinding(t *testing.T) {
	src := `fn demo(): Int {
  x: Map<String, List<Int>> = Iter.to_map([])
  Map.size(x)
}`
	expectNoErrorsT(t, checkWithStdlib(src))
}

func TestExpectedTypeSeed_EmptyList_StructField(t *testing.T) {
	src := `struct Index {
  words: Map<String, List<Int>>
}
fn demo(): Index {
  Index{words: Iter.to_map([])}
}`
	expectNoErrorsT(t, checkWithStdlib(src))
}

func TestExpectedTypeSeed_EmptyList_PipeStage(t *testing.T) {
	src := `fn demo(): Map<String, Int> {
  [] |> Iter.to_map()
}`
	expectNoErrorsT(t, checkWithStdlib(src))
}

func TestExpectedTypeSeed_NoneArgument(t *testing.T) {
	src := seedEnums + `fn demo(): Map<String, Maybe<Direction>> {
  Iter.to_map([("a", None), ("b", Some(.East))])
}`
	expectNoErrorsT(t, checkWithStdlib(src))
}

// A real argument mismatch is still reported, against the element type the
// expected type solved.
func TestExpectedTypeSeed_ArgumentMismatchStillReported(t *testing.T) {
	src := seedEnums + `fn exits(): Map<Direction, Place> {
  Iter.to_map([(.North, 5)])
}`
	expectErrorContaining(t, checkWithStdlib(src),
		"list element type mismatch: expected (Direction, Place), got (Direction, Int)")
}

// An expected type the call's result cannot have does not seed anything: the
// arguments are checked as they would be without it, and the mismatch is
// reported at the binding.
func TestExpectedTypeSeed_UnrelatedExpectedTypeIsNotSeeded(t *testing.T) {
	src := `fn demo(): Int {
  x: Int = Iter.to_map([(1, "one")])
  x
}`
	expectErrorContaining(t, checkWithStdlib(src), "expected Int, got Map<Int, String>")
}

// A collection literal checked against an expected `Iter<T>` checks each item
// against T, at any depth, for a list, a vector and a set, called directly or
// as a pipe head.
const seedPayloads = seedEnums + `pub enum Exit {
  Door(Place)
  Gap
}
`

func TestExpectedTypeSeed_IterParam_TupleItems(t *testing.T) {
	src := seedEnums + `fn exits(): Map<Direction, Place> {
  Iter.to_map([(.North, .Cave), (.East, .Hall)])
}`
	expectNoErrorsT(t, checkWithStdlib(src))
}

func TestExpectedTypeSeed_IterParam_PipeHead(t *testing.T) {
	src := seedEnums + `fn exits(): Map<Direction, Place> {
  [(.North, .Cave), (.East, .Hall)] |> Iter.to_map()
}`
	expectNoErrorsT(t, checkWithStdlib(src))
}

func TestExpectedTypeSeed_IterParam_NestedPayload(t *testing.T) {
	src := seedPayloads + `fn exits(): Map<Direction, Maybe<Exit>> {
  Iter.to_map([(.North, Some(.Door(.Cave))), (.East, None), (.East, Some(.Gap))])
}
fn piped(): Map<Direction, Maybe<Exit>> {
  [(.North, Some(.Door(.Hall)))] |> Iter.to_map()
}`
	expectNoErrorsT(t, checkWithStdlib(src))
}

func TestExpectedTypeSeed_IterParam_VectorAndSetLiterals(t *testing.T) {
	src := seedEnums + `fn v(): Vector<(Direction, Place)> {
  Iter.to_vector(#[(.North, .Cave)])
}
fn s(): Set<Direction> {
  #{.North, .East} |> Iter.to_set()
}
fn l(): List<Direction> {
  Iter.to_list([.North])
}`
	expectNoErrorsT(t, checkWithStdlib(src))
}

func TestExpectedTypeSeed_IterParam_EmptyLiteral(t *testing.T) {
	src := `fn demo(): Map<String, List<Int>> {
  Iter.to_map([])
}`
	expectNoErrorsT(t, checkWithStdlib(src))
}

// An item that does not fit T is reported once, against T, and a `.Variant`
// is not also reported against the call's own expected type.
func TestExpectedTypeSeed_IterParam_ItemMismatch(t *testing.T) {
	errs := checkWithStdlib(seedEnums + `fn exits(): Map<Direction, Place> {
  Iter.to_map([(.North, 5)])
}`)
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
	src := `fn v(): Vector<Maybe<Int>> {
  x = #[Some(1), None]
  x
}
fn w(): Vector<Maybe<String>> {
  y = #[None, Some("a")]
  y
}
fn s(): Set<Maybe<Int>> {
  z = #{None, Some(2)}
  z
}`
	expectNoErrorsT(t, checkWithStdlib(src))
	expectErrorContaining(t, checkWithStdlib(`fn main() {
  _ = #[Some(1), Some("a")]
}`), "vector element type mismatch: expected Maybe<Int>, got Maybe<String>")
}
