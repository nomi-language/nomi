package irbuild

import "testing"

// A field accessor `.name` lowers to a closure reading the field chain, in
// every position the checker accepts it: a generic argument pinned by an
// earlier one, a pipe stage's argument, a trailing argument routed past a
// default, an annotated binding, a return value, a generic struct, a generic
// body, a function-valued field, a tuple index and an anonymous struct.
func TestIRFieldAccessor_RunsWhereTheCheckerAcceptsIt(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

struct Address {
  city: String
}

struct User {
  name: String
  age: Int
  address: Address
  greet: (String) -> String
}

struct Box<T> {
  value: T
}

fn name_of(): (User) -> String {
  .name
}

fn values<T>(boxes: List<Box<T>>): List<T> {
  boxes |> Iter.map(.value) |> Iter.to_list()
}

fn main() {
  users = [
    User{name: "Ann", age: 40, address: Address{city: "Bath"}, greet: |s| "hi ${s}"},
    User{name: "Bob", age: 30, address: Address{city: "York"}, greet: |s| "yo ${s}"},
  ]
  io.print(users |> Iter.map(.name) |> Iter.to_list())
  io.print(Iter.map(users, .age) |> Iter.to_list())
  io.print(users |> Iter.map(.address.city) |> Iter.to_list())
  io.print(Iter.sort_by(users, .age) |> Iter.map(.name) |> Iter.to_list())
  age: (User) -> Int = .age
  io.print(Iter.map(users, age) |> Iter.to_list())
  name = name_of()
  io.print(Iter.map(users, name) |> Iter.to_list())
  boxes = [Box{value: Address{city: "Rye"}}, Box{value: Address{city: "Ely"}}]
  io.print(boxes |> Iter.map(.value.city) |> Iter.to_list())
  io.print(values([Box{value: 1}, Box{value: 2}]))
  greet: (User) -> (String) -> String = .greet
  hello = greet(User{name: "Cy", age: 1, address: Address{city: "Ayr"}, greet: |s| "hey ${s}"})
  io.print(hello("there"))
  io.print([(1, "a"), (2, "b")] |> Iter.map(.1) |> Iter.to_list())
  points = [{x: 1, y: 2}, {x: 3, y: 4}]
  io.print(points |> Iter.map(.y) |> Iter.to_list())
}
`, "[Ann, Bob]\n[40, 30]\n[Bath, York]\n[Bob, Ann]\n[40, 30]\n[Ann, Bob]\n[Rye, Ely]\n[1, 2]\nhey there\n[a, b]\n[2, 4]\n")
}
