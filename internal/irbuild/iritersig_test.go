package irbuild

import "testing"

// Sequence values across function-value signatures, and the non-list sources
// the builder drives: a user type's own `impl Iter` and a Vector. Each
// program runs on the VM and must print the expected output.
func TestIRIterSig_ProgramsRunOnTheVM(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"sequence results, captures and branch values", `import std/io

fn main() {
  base = [1, 2, 3, 4, 5]
  evens = |xs: List<Int>| { xs |> Iter.filter(|x| x % 2 == 0) }
  s = evens(base)
  scaled = |k: Int| { s |> Iter.map(|x| x * k) }
  scaled(3) |> Iter.to_list() |> io.inspect()
  pick = |flag: Bool| {
    if flag {
      s
    } else {
      base |> Iter.take(1)
    }
  }
  pick(False) |> Iter.to_list() |> io.inspect()
  pick(True) |> Iter.count() |> io.print()
  total = |f: (Int) -> Int| Iter.reduce(scaled(2) |> Iter.map(f), |a = 0, x| a + x)
  total(|x| x + 1) |> io.print()
  chosen = if Iter.count(s) > 1 { scaled(5) } else { s }
  chosen |> Iter.to_list() |> io.inspect()
  s |> Iter.reduce(|a = 0, x| a + x) |> io.print()
}
`, "[6, 12]\n[1]\n2\n14\n[10, 20]\n6\n"},
		{"a user source pushes through its own each_while", `import std/io

struct Span {
  lo: Int
  hi: Int
}

impl Iter for Span {
  fn each_while(s: Span, yield: (Int) -> Bool): Bool {
    Iter.all?(s.lo..s.hi, |x| {
      io.print("visit ${x}")
      yield(x)
    })
  }
}

fn main() {
  span = Span{lo: 1, hi: 4}
  span |> Iter.to_list() |> io.inspect()
  span |> Iter.map(|x| x * 10) |> Iter.take(2) |> Iter.to_list() |> io.inspect()
  Iter.count(span) |> io.print()
  Iter.each_while(span, |x| x < 2) |> io.print()
  Iter.each_while(Span{lo: 3, hi: 3}, |_x| False) |> io.print()
  odd = span |> Iter.filter(|x| x % 2 == 1)
  odd |> Iter.to_list() |> io.inspect()
  odd |> Iter.to_list() |> io.inspect()
}
`, "visit 1\nvisit 2\nvisit 3\n[1, 2, 3]\n" +
			"visit 1\nvisit 2\n[10, 20]\n" +
			"visit 1\nvisit 2\nvisit 3\n3\n" +
			"visit 1\nvisit 2\nFalse\n" +
			"True\n" +
			"visit 1\nvisit 2\nvisit 3\n[1, 3]\n" +
			"visit 1\nvisit 2\nvisit 3\n[1, 3]\n"},
		{"a vector source", `import std/io

fn main() {
  v = #[3, 1, 4, 1, 5]
  v |> Iter.map(|x| x * 2) |> Iter.to_list() |> io.inspect()
  Iter.count(v) |> io.print()
  v |> Iter.sort() |> io.inspect()
  Iter.any?(v, |x| x > 4) |> io.print()
  Iter.each_while(v, |x| x < 4) |> io.print()
  Vector.push(v, 9) |> Iter.filter(|x| x > 3) |> Iter.to_list() |> io.inspect()
}
`, "[6, 2, 8, 2, 10]\n5\n[1, 1, 3, 4, 5]\nTrue\nFalse\n[4, 5, 9]\n"},
		{"each_while over a list", `import std/io

fn main() {
  xs = [1, 2, 3]
  Iter.each_while(xs, |x| x < 5) |> io.print()
  Iter.each_while(xs |> Iter.map(|x| x * 2), |x| x < 3) |> io.print()
}
`, "True\nFalse\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
