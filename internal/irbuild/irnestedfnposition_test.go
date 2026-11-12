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
		{"list element", `import std::io
fn main()
  xs = [begin
    fn one() -> Int
      1
    end
    one()
  end, 2]
  io.inspect(xs)
end
`, "[1, 2]\n"},
		{"tuple element", `import std::io
fn main()
  v = (begin
    fn name() -> String
      "t"
    end
    name()
  end, 3)
  io.inspect(v)
end
`, "(\"t\", 3)\n"},
		{"struct field", `import std::io
struct Point
  x: Int
  y: Int
end
fn main()
  p = Point{x: begin
    fn four() -> Int
      4
    end
    four()
  end, y: 5}
  io.inspect(p)
end
`, "Point{x: 4, y: 5}\n"},
		{"operand", `import std::io
fn main()
  n = 10 + begin
    fn six() -> Int
      6
    end
    six()
  end
  io.inspect(n)
end
`, "16\n"},
		{"field receiver", `import std::io
struct Point
  x: Int
  y: Int
end
fn main()
  x = begin
    fn origin() -> Point
      Point{x: 7, y: 8}
    end
    origin()
  end.x
  io.inspect(x)
end
`, "7\n"},
		{"lambda body in a tuple", `import std::io
fn main()
  (f, label) = (fn n: Int ->
    fn twice(k: Int) -> Int
      k * 2
    end
    twice(n)
  end, "twice")
  io.print(label)
  io.inspect(f(6))
end
`, "twice\n12\n"},
		{"then body", `import std::io
fn main()
  n = 5
    |> then fn k ->
      fn inc(j: Int) -> Int
        j + 1
      end
      inc(k)
    end
  io.inspect(n)
end
`, "6\n"},
		{"pipe head", `import std::io
fn main()
  n = begin
    fn base() -> List<Int>
      [1, 2, 3]
    end
    base()
  end
    |> Iter.count()
  io.inspect(n)
end
`, "3\n"},
		{"interpolation", `import std::io
fn main()
  io.print("v=${begin
    fn word() -> String
      "w"
    end
    word()
  end}")
end
`, "v=w\n"},
		{"case on the nested fn's call", `import std::io
fn main()
  xs = [begin
    fn pick() -> Maybe<Int>
      Some(9)
    end
    case pick()
      Some(v) -> v
      None -> 0
    end
  end]
  io.inspect(xs)
end
`, "[9]\n"},
		{"try in the nested fn", `import std::io
fn main()
  xs = [begin
    fn attempt() -> Result<Int, String>
      v = Ok(11)?
      Ok(v)
    end
    case attempt()
      Ok(v) -> v
      Err(_) -> 0
    end
  end]
  io.inspect(xs)
end
`, "[11]\n"},
		{"capture in an operand", `import std::io
fn main()
  base = 40
  n = 1 + begin
    fn add(k: Int) -> Int
      k + base
    end
    add(1)
  end
  io.inspect(n)
end
`, "42\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
