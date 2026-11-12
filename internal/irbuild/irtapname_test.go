package irbuild

import (
	"testing"
)

// A `tap` stage that names its function calls it with the piped value and
// answers the value: a plain function, a qualified one, a generic one, one
// whose defaults fill the rest, a local binding, and a List passed where an
// Iter is declared. The piped value is computed once.
func TestIRTapName_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"plain and qualified", `import std::io
fn log_it(n: Int)
    io.print("log ${n}")
end
fn main()
  x = 5 |> tap log_it |> tap io.print
  io.print(x + 1)
end`, "log 5\n5\n6\n"},
		{"generic and defaults", `import std::io
fn show<T>(v: T) where T: Display
    io.print("show ${v}")
end
fn labeled(n: Int, label: String = "n")
    io.print("${label} = ${n}")
end
fn main()
  x = 3 |> tap show |> tap labeled
  io.print("ab" |> tap show |> String.length())
  io.print(x)
end`, "show 3\nn = 3\nshow ab\n2\n3\n"},
		{"local binding and an Iter", `import std::io
fn total(xs: Iter<Int>)
    io.print(Iter.sum(xs))
end
fn mark(n: Int) -> Int
    io.print("mark")
    return n
end
fn main()
  g = fn n: Int -> io.print("g ${n}")
  x = mark(4) |> tap g
  io.inspect([1, 2] |> tap total)
  io.print(x)
end`, "mark\ng 4\n3\n[1, 2]\n4\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
