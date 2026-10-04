package irbuild

import "testing"

func TestIRSortBy_TourUsersByName(t *testing.T) {
	verifyLambdaProgram(t, `struct User {
  name: String
  age: Int
}

fn main(): List<User> {
  users = [User{name: "Cara", age: 35}, User{name: "Ann", age: 41}]
  sorted = users |> Iter.sort_by(|u| u.name)

  dbg sorted
}
`, "dbg line 10: sorted = [User{name: \"Ann\", age: 41}, User{name: \"Cara\", age: 35}]\n")
}

// Keys are projected per comparison through the bound key function; equal keys
// keep source order; a nominal key orders through its Comparable impl.
func TestIRSortBy_KeysStabilityAndNominalKeys(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
struct Rank {
  n: Int
}
derive Comparable for Rank
struct Item {
  label: String
  weight: Int
}
fn main() {
  items = [Item{label: "b", weight: 2}, Item{label: "a", weight: 1}, Item{label: "c", weight: 2}]
  by_weight = items |> Iter.sort_by(|i| i.weight)
  io.inspect(by_weight |> Iter.map(|i| i.label) |> Iter.to_list())
  ranks = [Rank{n: 3}, Rank{n: 1}, Rank{n: 2}]
  io.inspect(Iter.sort_by(ranks, |r| r))
  io.inspect([3, 1, 2] |> Iter.sort_by(|n| 0 - n))
}
`, "[\"a\", \"b\", \"c\"]\n[Rank{n: 1}, Rank{n: 2}, Rank{n: 3}]\n[3, 2, 1]\n")
}
