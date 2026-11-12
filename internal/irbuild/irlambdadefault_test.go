package irbuild

import "testing"

func TestIRLambdaDefaults_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"captured condition and list result", `import std::io
fn main()
 choose = fn ->
     io.print("choose")
     False
 end
 f = fn xs: List<Int> = (if choose() then [1] else [2, 3]) -> xs
 alias = f
 io.inspect(alias())
 io.inspect(f([9]))
end`, "choose\n[2, 3]\n[9]\n"},
		{"conditional suppliers", `import std::io
fn mark(n: Int) -> Int
    io.print(n)
    n
end
fn main()
 base = 10
 f = fn flag: Bool, a: Int = if flag then mark(base) else mark(20), b: Int = (if a == 10 then mark(a + 1) else mark(a + 2)) -> a + b
 io.print(f(True))
 io.print(f(False))
 io.print(f(False, 30, 40))
end`, "10\n11\n21\n20\n22\n42\n70\n"},
		{"case suppliers and scoped arms", `import std::io
fn mark(n: Int) -> Int
    io.print(n)
    n
end
fn main()
 base = 5
 f = fn n: Int, x: Int = case n
  0 ->
      offset = base + 1
      mark(offset)
  1 -> if n == 1 then mark(7) else mark(8)
  _ -> mark(9)
 end -> x
 io.print(f(0))
 io.print(f(1))
 io.print(f(2))
 io.print(f(0, 42))
end`, "6\n6\n7\n7\n9\n9\n42\n"},
		{"tour shape", `import std::io
fn greet(name: String, greeting: String = "Hello") -> String
    "${greeting}, ${name}!"
end
fn main()
 io.print(greet("World"))
 add = fn x: Int, y = 10 -> x + y
 io.print(add(5))
 io.print(add(5, 2))
end`, "Hello, World!\n15\n7\n"},
		{"dependent defaults execute once", `import std::io
fn mark(n: Int) -> Int
    io.print(n)
    n
end
fn main()
 f = fn a = mark(1), b = mark(a + 1), c = mark(b + 1) -> a + b + c
 io.print(f())
 io.print(f(a: 10))
 io.print(f(c: mark(30), a: mark(10)))
 io.print(f(7, 8, 9))
end`, "1\n2\n3\n6\n11\n12\n33\n30\n10\n11\n51\n24\n"},
		{"captures and aliases", `import std::io
fn main()
 base = 40
 f = fn x = base, y = x + 2 -> y
 alias = f
 io.print(alias())
 io.print(alias(1, 0))
 io.print(f(10))
end`, "42\n0\n12\n"},
		{"callable default captures earlier parameter", `import std::io
fn main()
 apply = fn x: Int, f: (Int) -> Int = fn n: Int -> n + x -> f(2)
 io.print(apply(40))
 io.print(apply(100, fn n -> n * 3))
end`, "42\n6\n"},
		{"explicit ordering and zero values", `import std::io
fn mark(n: Int) -> Int
    io.print(n)
    n
end
fn main()
 f = fn a: Int, b = mark(20), c = mark(30) -> a + b + c
 io.print(f(c: mark(9), mark(7)))
 zero = fn x = 99 -> x
 io.print(zero(0))
 bool = fn x = True -> x
 io.print(bool(False))
 text = fn x = "hello" -> x
 io.print("[${text("")}]")
end`, "7\n9\n20\n36\n0\nFalse\n[]\n"},
		{"short-circuit defaults", `import std::io
fn mark() -> Bool
    io.print("unexpected")
    True
end
fn main()
 f = fn a = False and mark(), b = True or mark() -> if a then 1 else if b then 2 else 3
 io.print(f())
end`, "2\n"},
		{"multiline supplier cursors", `import std::io
fn mark(n: Int) -> Int
    io.print(n)
    n
end
fn main()
 base = 10
 f = fn a: Int =
   mark(base), b: Int =
   mark(a + 1) ->
   a + b
 end
 io.print(f())
end`, "10\n11\n21\n"},
		{"higher order and unused suppliers", `import std::io
fn apply(f: (Int) -> Int) -> Int
    f(2)
end
fn mark(n: Int) -> Int
    io.print(n)
    n
end
fn main()
 io.print(apply(fn x = mark(99) -> x + 1))
 choose = fn x: Int, _label = "z", cb: (Int) -> Int -> cb(x) + 1
 io.print(choose(7, fn x -> x * 2))
end`, "3\n15\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}

// A local name that copies a declared function keeps its defaults, as one
// bound to a lambda does: a module function, a copy of a copy, a pipe, an
// owner function, a nested function and a copy a closure captured. The call
// goes through the function's shorter value, which calls it by name.
func TestIRLambdaDefault_ALocalCopyOfAFunctionKeepsItsDefaults(t *testing.T) {
	verifyLambdaProgram(t, `import std::io

fn parse(s: String, strict: Bool = False) -> Maybe<Int>
  if strict then None else String.to_int(s)
end

struct Parsing
  n: Int
end

impl Parsing
  fn add(a: Int, b: Int = 100) -> Int
    a + b
  end
end

fn main()
  g = parse
  g2 = g
  io.print(Debug.inspect(g("12")))
  io.print(Debug.inspect(g2("13")))
  io.print(Debug.inspect("14" |> g()))
  io.print(Debug.inspect(g("1", True)))
  p = Parsing.add
  io.print(Debug.inspect(p(1)))
  fn local(n: Int, m: Int = 5) -> Int
    n * m
  end
  l = local
  io.print(Debug.inspect(l(3)))
  k = fn s: String -> g(s)
  io.print(Debug.inspect(k("7")))
end
`, "Some(12)\nSome(13)\nSome(14)\nNone\n101\n15\nSome(7)\n")
}

// A lambda with defaulted parameters past the ones a shorter expected
// function type has is that shorter function, whose call fills the rest from
// their defaults (spec §7 *Default Arguments*): as an argument, an annotated
// binding, a field, a list element, a return and an `if` or `case` arm. A
// default may read an earlier parameter.
func TestIRLambdaDefault_ALambdaTakesAShorterExpectedType(t *testing.T) {
	verifyLambdaProgram(t, `import std::io

struct Op
  run: (Int) -> Int
end

fn make() -> (Int) -> Int
  fn x, y = 2 -> x * y
end

fn pick(b: Bool) -> (Int) -> Int
  if b then
      fn x, y = 3 -> x + y
  else
      fn x -> x
  end
end

fn choose(n: Int) -> (Int) -> Int
  case n
    0 -> fn x, y = 4 -> x - y
    _ -> fn x -> x
  end
end

fn main()
  io.print(Debug.inspect(Iter.map([1, 2], fn x, y = 10 -> x + y) |> Iter.to_list()))
  f: (Int) -> Int = fn x, y = 3 -> x - y
  io.print(Debug.inspect(f(10)))
  op = Op{run: fn x, y = 1 -> x + y}
  io.print(Debug.inspect(op.run(5)))
  fs: List<(Int) -> Int> = [fn x, y = 7 -> x + y]
  io.print(Debug.inspect(Iter.map(fs, fn g -> g(1)) |> Iter.to_list()))
  io.print(Debug.inspect(make()(5)))
  io.print(Debug.inspect(pick(True)(5)))
  io.print(Debug.inspect(choose(0)(5)))
  io.print(Debug.inspect(Iter.map([1, 2], fn x, y = x * 10 -> x + y) |> Iter.to_list()))
end
`, "[11, 12]\n7\n6\n[8]\n10\n8\n1\n[11, 22]\n")
}
