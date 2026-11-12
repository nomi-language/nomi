package irbuild

import "testing"

func TestIRLambda_BranchBindings(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"Unit binding", `import std::io
fn main()
 emit = fn flag: Bool ->
  done = if flag then io.print("left") else io.print("right")
  done
 end
 emit(True)
 emit(False)
end
`, "left\nright\n"},
		{"named function", `import std::io
fn choose(x: Int) -> Int
 first = if x > 0 then x + 1 else 3
 second = case first
     2 -> 20
     _ -> first + 10
 end
 second + 1
end
fn main()
 io.print(choose(1))
 io.print(choose(2))
 io.print(choose(0))
end
`, "21\n14\n14\n"},
		{"sequential bindings", `import std::io
fn main()
 choose = fn x: Int ->
  first = if x > 0 then x + 1 else 3
  second = case first
      2 -> 20
      _ -> first + 10
  end
  io.print(first)
  second + 1
 end
 io.print(choose(1))
 io.print(choose(2))
 io.print(choose(0))
end
`, "2\n21\n3\n14\n3\n14\n"},
		{"binding inside arms", `import std::io
fn main()
 choose = fn x: Int, flag: Bool ->
  answer = if flag then
   inner = case x
       0 -> 3
       _ -> 4
   end
   io.print("left")
   inner + 10
  else
   inner = if x > 0 then 5 else 6
   io.print("right")
   inner + 20
  end
  if answer > 20 then answer + 1 else answer + 2
 end
 io.print(choose(0, True))
 io.print(choose(1, True))
 io.print(choose(0, False))
 io.print(choose(1, False))
end
`, "left\n15\nleft\n16\nright\n27\nright\n26\n"},
		{"closure binding", `import std::io
fn main()
 choose = fn flag: Bool ->
  add = if flag then
   amount = 2
   fn x: Int -> x + amount
  else
   amount = 5
   fn x: Int -> x + amount
  end
  io.print(add(10))
  add
 end
 a = choose(True)
 b = choose(False)
 io.print(a(20))
 io.print(b(20))
end
`, "12\n15\n22\n25\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
