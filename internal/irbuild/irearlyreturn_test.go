package irbuild

import "testing"

func TestIREarlyReturn_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"empty result coercion", `import std::io
fn choose(flag: Bool) -> List<Int>
 if flag then return []
 [1]
end
fn main()
 io.inspect(choose(True))
 io.inspect(choose(False))
end`, "[]\n[1]\n"},
		{"short-circuit condition and multiline return", `import std::io
fn check(n: Int) -> Bool
    io.print("check")
    n > 0
end
fn mark(n: Int) -> Int
    io.print("return")
    n
end
fn choose(n: Int) -> Int
 if n > 0 and check(n) then
  return mark(
   n,
  )
 end
 0
end
fn main()
 io.print(choose(-1))
 io.print(choose(4))
end`, "0\ncheck\nreturn\n4\n"},
		{"absolute value", `import std::io
fn abs(x: Int) -> Int
 if x < 0 then return -x
 x
end
fn main()
 io.print(abs(-9))
 io.print(abs(0))
 io.print(abs(7))
end`, "9\n0\n7\n"},
		{"sequential guards and scoped effects", `import std::io
fn mark(n: Int) -> Int
    io.print(n)
    n
end
fn choose(n: Int) -> Int
 if n < 0 then
  amount = -n
  io.print("negative")
  return mark(amount)
 end
 if n == 0 then return mark(0)
 io.print("tail")
 mark(n)
end
fn main()
 io.print(choose(-3))
 io.print(choose(0))
 io.print(choose(8))
end`, "negative\n3\n3\n0\n0\ntail\n8\n8\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
