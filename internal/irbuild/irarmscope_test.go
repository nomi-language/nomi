package irbuild

import "testing"

func TestIRArmScope_NamedFunctions(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"sibling names and effects", `import std::io
fn choose(flag: Bool) -> Int
  if flag then
      n = 10
      io.print("then")
      n + 1
  else
      n = 20
      io.print("else")
      n + 2
  end
end
fn main()
    io.print(choose(True))
    io.print(choose(False))
end
`, "then\n11\nelse\n22\n"},
		{"else if and nested bindings", `import std::io
fn choose(a: Bool, b: Bool) -> Int
  if a then
      n = 3
      if b then n + 1 else n + 2
  else if b then
      n = 10
      n + 1
  else
      n = 20
      n + 2
  end
end
fn main()
  io.print(choose(True, True))
    io.print(choose(True, False))
  io.print(choose(False, True))
    io.print(choose(False, False))
end`, "4\n5\n11\n22\n"},
		{"case arm scopes", `import std::io
fn choose(n: Int) -> Int
  case n
    1 ->
        x = 10
        io.print("one")
        x + 1
    2 ->
        x = 20
        io.print("two")
        x + 2
    _ ->
        x = 30
        io.print("other")
        x + 3
  end
end
fn main()
    io.print(choose(1))
    io.print(choose(2))
    io.print(choose(3))
end
`, "one\n11\ntwo\n22\nother\n33\n"},
		{"escaping arm capture", `import std::io
fn choose(flag: Bool) -> (Int) -> Int
  if flag then
      base = 10
      fn n: Int -> base + n
  else
      base = 20
      fn n: Int -> base + n
  end
end
fn main()
    a = choose(True)
    b = choose(False)
    io.print(a(1))
    io.print(b(2))
end
`, "11\n22\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
