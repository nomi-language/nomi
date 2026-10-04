package vmhost_test

import "testing"

// Programs that reach std bodies the stdlib lowering used to decline, and
// the equality and hashing rules for keys. Each runs on the VM and prints
// what the language says it prints.
func TestRun_ReachableStdBodiesRun(t *testing.T) {
	cases := []struct {
		name, src, want string
	}{
		{"Backoff's Debug and its defaulted variant", `import std/io
import std/duration.{Duration}
import std/supervisors.{Backoff}

fn main() {
  a: Backoff = Backoff.Exponential{}
  b: Backoff = Backoff.Exponential{max_restarts: 3, max_elapsed: Duration.minutes(1)}
  io.inspect(a)
  io.print(Debug.inspect(b))
}
`, "Exponential{max_restarts: 10, max_elapsed: 15m}\nExponential{max_restarts: 3, max_elapsed: 1m}\n"},
		{"Range.from and Range.naturals as function values", `import std/io

fn main() {
  f = Range.from
  io.inspect(f(2))
  [1, 2] |> Iter.map(Range.from) |> Iter.to_list() |> io.inspect()
  g = Range.naturals
  g() |> Iter.take(2) |> Iter.to_list() |> io.inspect()
}
`, "2..\n[1.., 2..]\n[0, 1]\n"},
		{"Bytes.hash and a Set of Bytes", `import std/io

fn main() {
  a = String.to_bytes("ab")
  io.print(Hashable.hash(a) == Hashable.hash(String.to_bytes("ab")))
  io.print(Hashable.hash(a) == Hashable.hash(String.to_bytes("ba")))
  io.print(Set.size(#{a, String.to_bytes("ab")}))
}
`, "True\nFalse\n1\n"},
		{"OffsetDateTime keys by instant", `import std/io
import std/calendar.{OffsetDateTime}

fn main() {
  a = OffsetDateTime"2024-01-01T12:00:00+00:00"
  b = OffsetDateTime"2024-01-01T13:00:00+01:00"
  io.print(a == b)
  io.print(Set.size(#{a, b}))
  io.print(Map.get({a => 1}, b))
}
`, "True\n1\nSome(1)\n"},
		{"Decimal sorts and fills a Vector", `import std/io

fn main() {
  io.inspect(Iter.sort([2.50d, 1.5d, 10d]))
  io.inspect(Vector.at(#[1.5d, 2.5d], 1))
}
`, "[1.5d, 2.50d, 10d]\nSome(2.5d)\n"},
		{"Hashable.hash on a tuple is the derive mix", `import std/io

struct H {
  k: Int
  note: String
}

impl Hashable for H {
  fn hash(h: H): Int {
    h.k * 1000
  }
}

enum E {
  Pair (Int, Int)
}

derive Hashable for E

fn main() {
  io.print(Hashable.hash((1, 2)))
  io.print(Hashable.hash((1, 2, 3)))
  io.print(Hashable.hash((H{k: 2, note: "x"}, 3)))
  io.print(Hashable.hash(E.Pair((1, 2))) == Hashable.hash((1, 2)))
}
`, "33\n1026\n62003\nTrue\n"},
		{"a struct-shaped variant's positional binding is its record", `import std/io

enum Wrap {
  One {y: Int}
  Two {a: Int, b: String}
  Zero
}

fn main() {
  for_each = [Wrap.One({y: 1}), Wrap.Two({a: 2, b: "s"}), Wrap.Zero]
  for_each |> Iter.each(|w| case w {
    .One(p) -> io.inspect(p)
    .Two(q) -> io.print(q == {a: 2, b: "s"})
    .Zero -> io.print("zero")
  })
}
`, "{y: 1}\nTrue\nzero\n"},
		{"a tuple slot of a hand-written Equatable compares through it", `import std/io

struct Tag {
  name: String
  note: String
}

impl Equatable for Tag {
  fn equal?(a: Tag, b: Tag): Bool {
    a.name == b.name
  }
}

fn main() {
  io.print((Tag{name: "x", note: "1"}, 1) == (Tag{name: "x", note: "2"}, 1))
  io.print(Some(Tag{name: "x", note: "1"}) == Some(Tag{name: "x", note: "2"}))
  io.print([Tag{name: "x", note: "1"}] == [Tag{name: "x", note: "2"}])
}
`, "True\nTrue\nTrue\n"},
		{"a hand-written Equatable with no Hashable keys by equal?", `import std/io

struct Loose {
  name: String
  note: String
}

impl Equatable for Loose {
  fn equal?(a: Loose, b: Loose): Bool {
    a.name == b.name
  }
}

fn main() {
  s = #{Loose{name: "x", note: "1"}, Loose{name: "x", note: "2"}, Loose{name: "y", note: "3"}}
  io.print(Set.size(s))
  io.print(Map.get({Loose{name: "y", note: "a"} => 7}, Loose{name: "y", note: "b"}))
}
`, "2\nSome(7)\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := runVM(t, c.src)
			if err != nil || out != c.want {
				t.Fatalf("output %q, error %v; want %q", out, err, c.want)
			}
		})
	}
}
