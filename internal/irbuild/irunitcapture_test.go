package irbuild

import "testing"

// A closure that names a Unit binding of its enclosing function makes its
// own Unit: there is nothing to carry. Before, the capture declined, and so
// did `Result.collect()` over results of `Unit`, whose body captures each
// `Ok`'s value in a lambda.
func TestIRUnitCapture_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"a binding", `import std/io
fn main() {
  u = Unit
  f = fn -> [u, u]
  io.print(Iter.count(f()))
}`, "2\n"},
		{"a pattern's binding", `import std/io
fn main() {
  case Some(Unit) {
    Some(v) -> io.print(Iter.count((fn -> [v])()))
    None -> {}
  }
}`, "1\n"},
		{"Result.collect over Unit results", `import std/io
fn write(n: Int) -> Result<Unit, String> {
  if n > 2 { Err("too big: ${n}") } else { Ok(Unit) }
}
fn main() {
  done = [1, 2] |> Iter.map(write) |> Result.collect()
  io.print(Result.ok?(done))
  failed = [1, 3, 4] |> Iter.map(write) |> Result.collect()
  io.print(failed == Err("too big: 3"))
}`, "True\nTrue\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
