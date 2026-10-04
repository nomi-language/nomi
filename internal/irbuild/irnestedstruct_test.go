package irbuild

import "testing"

// The Tour's nested patch: a nested spread, Struct.update with a deep patch,
// and a bare patch at a struct field, over a struct-typed field.
func TestIRNestedStruct_TourNestedPatch(t *testing.T) {
	verifyLambdaProgram(t, `struct Address {
  street: String
  city: String
}

struct Person {
  name: String
  address: Address
}

fn main(): Person {
  ada = Person{name: "Ada", address: Address{street: "1 Main", city: "Bath"}}

  // A nested change written as a nested spread.
  dbg {..ada, address: {..ada.address, city: "NYC"}}

  // Struct.update reaches the same field with a patch.
  dbg Struct.update(ada, {address: {city: "NYC"}})

  // A bare `+"`{...}`"+` at a struct field is that patch, written as a spread.
  dbg {..ada, address: {city: "NYC"}}
}
`, "dbg line 15: {..ada, address: {..ada.address, city: \"NYC\"}} = Person{name: \"Ada\", address: Address{street: \"1 Main\", city: \"NYC\"}}\n"+
		"dbg line 18: Struct.update(ada, {address: {city: \"NYC\"}}) = Person{name: \"Ada\", address: Address{street: \"1 Main\", city: \"NYC\"}}\n"+
		"dbg line 21: {..ada, address: {city: \"NYC\"}} = Person{name: \"Ada\", address: Address{street: \"1 Main\", city: \"NYC\"}}\n")
}

// Nested field chains, a function over the nested struct, derived Debug
// through the nested impl, and patches whose values are forced around them.
func TestIRNestedStruct_ProjectionPatchAndDebug(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

struct Address {
  street: String
  city: String
}

struct Person {
  name: String
  age: Int
  address: Address
}

fn relocate(p: Person, city: String): Person {
  {..p, address: {..p.address, city: city}, age: p.age + 1}
}

fn city_of(p: Person): String {
  p.address.city
}

fn shout(s: String): String {
  io.print("shout " + s)
  s + "!"
}

fn main() {
  ada = Person{name: "Ada", age: 36, address: Address{street: "1 Main", city: "Bath"}}
  moved = relocate(ada, "York")
  io.print(city_of(moved))
  io.print(moved.address.street)
  dbg moved
  dbg {..moved, address: {street: shout("2 Elm")}, name: shout("Grace")}
  dbg Struct.update(ada, {address: {street: shout("3 Oak"), city: shout("Leeds")}, age: 40})
  io.print(ada.address.city)
}
`, "York\n1 Main\n"+
		"dbg line 32: moved = Person{name: \"Ada\", age: 37, address: Address{street: \"1 Main\", city: \"York\"}}\n"+
		"shout 2 Elm\nshout Grace\n"+
		"dbg line 33: {..moved, address: {street: shout(\"2 Elm\")}, name: shout(\"Grace\")} = Person{name: \"Grace!\", age: 37, address: Address{street: \"2 Elm!\", city: \"York\"}}\n"+
		"shout 3 Oak\nshout Leeds\n"+
		"dbg line 34: Struct.update(ada, {address: {street: shout(\"3 Oak\"), city: shout(\"Leeds\")}, age: 40})"+
		" = Person{name: \"Ada\", age: 40, address: Address{street: \"3 Oak!\", city: \"Leeds!\"}}\n"+
		"Bath\n")
}

// A bare patch at a record field projects the field from the record base.
func TestIRNestedStruct_RecordPatch(t *testing.T) {
	verifyLambdaProgram(t, `fn main() {
  record = {p: {x: 1, y: 2}, n: 0}
  updated = {..record, p: {x: 5}}
  dbg updated
  return
}
`, "dbg line 4: updated = {n: 0, p: {x: 5, y: 2}}\n")
}
