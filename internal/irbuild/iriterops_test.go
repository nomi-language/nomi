package irbuild

import "testing"

// The positional terminals, `reverse`, the pairing stages, `Iter.from` and
// `known_count`, each through its rt function. Every program runs on the VM
// and must print the expected output.
func TestIRIterOps_ProgramsRunOnTheVM(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"find, first and last answer a Maybe", `import std/io

fn main() {
  xs = [3, 8, 5, 12]
  Iter.find(xs, |x| x > 4) |> io.inspect()
  Iter.find(xs, |x| x > 40) |> io.inspect()
  xs |> Iter.map(|x| x * 2) |> Iter.first() |> io.inspect()
  Iter.last(xs) |> io.inspect()
  [5] |> Iter.filter(|x| x > 10) |> Iter.first() |> io.inspect()
  Iter.last("héllo") |> io.inspect()
  Iter.from(0) |> Iter.find(|x| x * x > 50) |> io.inspect()
}
`, "Some(8)\nNone\nSome(6)\nSome(12)\nNone\nSome(\"o\")\nSome(8)\n"},
		{"reverse, with_index and zip", `import std/io

fn main() {
  xs = [1, 2, 3]
  Iter.reverse(xs) |> io.inspect()
  xs |> Iter.map(|x| x * 10) |> Iter.reverse() |> io.inspect()
  "abc" |> Iter.with_index() |> Iter.map(|p| "${p.0}:${p.1}") |> Iter.to_list() |> io.inspect()
  Iter.zip(xs, ["a", "b"]) |> Iter.map(|p| "${p.1}${p.0}") |> Iter.to_list() |> io.inspect()
  Iter.zip(Iter.from(10), xs) |> Iter.map(|p| p.0 + p.1) |> Iter.to_list() |> io.inspect()
  Iter.from(5) |> Iter.take(3) |> Iter.with_index() |> Iter.map(|p| p.0 * p.1) |> Iter.to_list() |> io.inspect()
}
`, "[3, 2, 1]\n[30, 20, 10]\n[\"0:a\", \"1:b\", \"2:c\"]\n[\"a1\", \"b2\"]\n[11, 13, 15]\n[0, 6, 14]\n"},
		{"known_count per source family", `import std/io

fn main() {
  Iter.known_count([1, 2, 3]) |> io.inspect()
  Iter.known_count("abc") |> io.inspect()
  Iter.known_count([1, 2] |> Iter.map(|x| x)) |> io.inspect()
  Iter.known_count(#[4, 5]) |> io.inspect()
  Iter.known_count(String.to_bytes("ab")) |> io.inspect()
  Iter.known_count(Iter.from(1)) |> io.inspect()
}
`, "Some(3)\nNone\nNone\nSome(2)\nNone\nNone\n"},
		{"at and partition", `import std/io

fn main() {
  xs = [4, 7, 9, 10]
  Iter.at(xs, 2) |> io.inspect()
  Iter.at(xs, 9) |> io.inspect()
  Iter.at(xs, -1) |> io.inspect()
  parts = Iter.partition(xs, |x| x % 2 == 0)
  parts.0 |> io.inspect()
  parts.1 |> io.inspect()
  none = xs |> Iter.partition(|x| x > 100)
  none.0 |> io.inspect()
  none.1 |> Iter.count() |> io.print()
}
`, "Some(9)\nNone\nNone\n[4, 10]\n[7, 9]\n[]\n4\n"},
		{"map and set sources", `import std/io

fn main() {
  ages = {"ann" => 31, "bo" => 22, "cy" => 40}
  ages |> Iter.map(|p| "${p.0}=${p.1}") |> Iter.to_list() |> io.inspect()
  Iter.count(ages) |> io.print()
  Iter.known_count(ages) |> io.inspect()
  ages |> Iter.filter(|p| p.1 > 30) |> Iter.count() |> io.print()
  Iter.any?(ages, |p| p.0 == "bo") |> io.print()
  s = #{5, 3, 5, 9}
  s |> Iter.to_list() |> io.inspect()
  Iter.count(s) |> io.print()
  Iter.known_count(s) |> io.inspect()
  Iter.zip(s, ["x", "y"]) |> Iter.map(|p| "${p.1}${p.0}") |> Iter.to_list() |> io.inspect()
  s |> Iter.reduce(|a = 0, x| a + x) |> io.print()
  Iter.sort(s) |> io.inspect()
}
`, "[\"ann=31\", \"bo=22\", \"cy=40\"]\n3\nSome(3)\n2\nTrue\n[5, 3, 9]\n3\nSome(3)\n[\"x5\", \"y3\"]\n17\n[3, 5, 9]\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
