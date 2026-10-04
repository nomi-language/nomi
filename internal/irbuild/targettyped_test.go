package irbuild

import "testing"

// A brace literal with no type name at a position expecting a nominal struct
// builds that struct (analysis.FileAnalysis.TargetStructs), and the builder
// lowers it exactly as the named literal: the same nominal value, the same
// Debug and Display, equal to a value built by `Address{...}`.

func TestTargetTyped_EveryPositionBuildsTheNominalStruct(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

struct Address {
  street: String
  city: String
  zip: String = "00000"
}

struct Person {
  name: String
  address: Address
}

once office: Address = {street: "2 Once", city: "Leeds"}

fn home(): Address { {street: "3 Ret", city: "York"} }

fn city(a: Address): String { a.city }

fn pick(n: Int): Address {
  case n {
    0 -> {street: "a", city: "Zero"}
    _ -> {street: "b", city: "Many"}
  }
}

fn main() {
  a: Address = {street: "1 Main", city: "Bath"}
  io.inspect(a)
  io.inspect(Person{name: "Ada", address: {street: "1 Main", city: "Bath"}})
  p: Person = {name: "Bo", address: a}
  io.inspect(p)
  people: List<Person> = [{name: "Cy", address: {street: "4 List", city: "Hull"}}]
  io.inspect(people)
  io.inspect(home())
  io.inspect(office)
  io.print(city({street: "5 Arg", city: "Ely"}))
  io.inspect(pick(0))
  io.inspect(pick(1))
  by_name: Map<String, Address> = {"x" => {street: "6 Map", city: "Wells"}}
  io.inspect(by_name)
  some: Maybe<Address> = Some({street: "7 Some", city: "Ripon"})
  io.inspect(some)
}
`, `Address{street: "1 Main", city: "Bath", zip: "00000"}
Person{name: "Ada", address: Address{street: "1 Main", city: "Bath", zip: "00000"}}
Person{name: "Bo", address: Address{street: "1 Main", city: "Bath", zip: "00000"}}
[Person{name: "Cy", address: Address{street: "4 List", city: "Hull", zip: "00000"}}]
Address{street: "3 Ret", city: "York", zip: "00000"}
Address{street: "2 Once", city: "Leeds", zip: "00000"}
Ely
Address{street: "a", city: "Zero", zip: "00000"}
Address{street: "b", city: "Many", zip: "00000"}
{"x" => Address{street: "6 Map", city: "Wells", zip: "00000"}}
Some(Address{street: "7 Some", city: "Ripon", zip: "00000"})
`)
}

func TestTargetTyped_EqualToTheNamedLiteral(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

struct Point {
  x: Int
  y: Int
}

derive Display for Point

fn main() {
  a: Point = {x: 1, y: 2}
  b = Point{x: 1, y: 2}
  io.print(a == b)
  io.print(a)
  io.print(b)
  points: Set<Point> = #{{x: 1, y: 2}, Point{x: 1, y: 2}}
  io.print(Set.size(points))
}
`, "True\nPoint{x: 1, y: 2}\nPoint{x: 1, y: 2}\n1\n")
}

func TestTargetTyped_GenericStruct(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

struct Box<T> {
  item: T
}

struct Pair {
  a: Int
  b: Int
}

fn unbox<T>(b: Box<T>): T { b.item }

fn main() {
  n: Box<Int> = {item: 3}
  io.inspect(n)
  io.print(unbox({item: "s"}))
  p: Box<Pair> = {item: {a: 1, b: 2}}
  io.inspect(p)
}
`, "Box{item: 3}\ns\nBox{item: Pair{a: 1, b: 2}}\n")
}

// Under a spread a bare brace at a struct-typed field stays a patch of the
// base's value; outside one it builds the field's struct.
func TestTargetTyped_SpreadPatchStillPatches(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

struct Address {
  street: String
  city: String
}

struct Person {
  name: String
  address: Address
}

fn main() {
  ada: Person = {name: "Ada", address: {street: "1 Main", city: "Bath"}}
  moved = {..ada, address: {city: "NYC"}}
  io.inspect(moved)
  rebuilt = Person{name: "Bo", address: {street: "2 Main", city: "York"}}
  io.inspect(rebuilt)
}
`, `Person{name: "Ada", address: Address{street: "1 Main", city: "NYC"}}
Person{name: "Bo", address: Address{street: "2 Main", city: "York"}}
`)
}
