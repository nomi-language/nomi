package irbuild

import "testing"

// TestIRNestedFn_RunsInEveryExpressionPosition: a block that declares a `fn`
// lowers and runs wherever the block sits. The checker once skipped such a
// fn's body in these positions (its signature walk did not descend into
// them), so the builder met a body with no recorded types and declined it
// ("a TryOp", "a type name in value position: Ok", "a nested fn: a result
// that is not the declared return type").
func TestIRNestedFn_RunsInEveryExpressionPosition(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"list element", `import std/io
fn main() {
  xs = [{
    fn one(): Int {
      1
    }
    one()
  }, 2]
  io.inspect(xs)
}
`, "[1, 2]\n"},
		{"tuple element", `import std/io
fn main() {
  v = ({
    fn name(): String {
      "t"
    }
    name()
  }, 3)
  io.inspect(v)
}
`, "(\"t\", 3)\n"},
		{"struct field", `import std/io
struct Point {
  x: Int
  y: Int
}
fn main() {
  p = Point{x: {
    fn four(): Int {
      4
    }
    four()
  }, y: 5}
  io.inspect(p)
}
`, "Point{x: 4, y: 5}\n"},
		{"operand", `import std/io
fn main() {
  n = 10 + {
    fn six(): Int {
      6
    }
    six()
  }
  io.inspect(n)
}
`, "16\n"},
		{"field receiver", `import std/io
struct Point {
  x: Int
  y: Int
}
fn main() {
  x = {
    fn origin(): Point {
      Point{x: 7, y: 8}
    }
    origin()
  }.x
  io.inspect(x)
}
`, "7\n"},
		{"lambda body in a tuple", `import std/io
fn main() {
  (f, label) = (|n: Int| {
    fn twice(k: Int): Int {
      k * 2
    }
    twice(n)
  }, "twice")
  io.print(label)
  io.inspect(f(6))
}
`, "twice\n12\n"},
		{"then body", `import std/io
fn main() {
  n = 5
    |> then |k| {
      fn inc(j: Int): Int {
        j + 1
      }
      inc(k)
    }
  io.inspect(n)
}
`, "6\n"},
		{"pipe head", `import std/io
fn main() {
  n = {
    fn base(): List<Int> {
      [1, 2, 3]
    }
    base()
  }
    |> Iter.count()
  io.inspect(n)
}
`, "3\n"},
		{"interpolation", `import std/io
fn main() {
  io.print("v=${{
    fn word(): String {
      "w"
    }
    word()
  }}")
}
`, "v=w\n"},
		{"case on the nested fn's call", `import std/io
fn main() {
  xs = [{
    fn pick(): Maybe<Int> {
      Some(9)
    }
    case pick() {
      Some(v) -> v
      None -> 0
    }
  }]
  io.inspect(xs)
}
`, "[9]\n"},
		{"try in the nested fn", `import std/io
fn main() {
  xs = [{
    fn attempt(): Result<Int, String> {
      v = try Ok(11)
      Ok(v)
    }
    case attempt() {
      Ok(v) -> v
      Err(_) -> 0
    }
  }]
  io.inspect(xs)
}
`, "[11]\n"},
		{"capture in an operand", `import std/io
fn main() {
  base = 40
  n = 1 + {
    fn add(k: Int): Int {
      k + base
    }
    add(1)
  }
  io.inspect(n)
}
`, "42\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
