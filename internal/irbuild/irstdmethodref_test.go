package irbuild

import "testing"

func TestIRStdMethodRef_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"construct and unwrap", `import
    std::io std::duration.Duration
end
fn main()
  make = Duration.nanoseconds
  unwrap = Duration.as_nanos
  io.print(unwrap(make(42)))
end`, "42\n"},
		{"higher order", `import
    std::io std::duration.Duration
end
fn apply(f: (Int) -> Duration, n: Int) -> Duration
    f(n)
end
fn main()
  unwrap = Duration.as_nanos
  io.print(unwrap(apply(Duration.microseconds, 3)))
end`, "3000\n"},
		{"captured callable and operator", `import
    std::io std::duration.Duration
end
fn main()
  make = Duration.nanoseconds
  unwrap = Duration.as_nanos
  twice = fn n: Int -> make(n) + make(n)
  io.print(unwrap(twice(21)))
end`, "42\n"},
		{"branch selected method and operand effects", `import
    std::io std::duration.Duration
end
fn input() -> Int
    io.print("input")
    2
end
fn main()
  make = if True then Duration.microseconds else Duration.nanoseconds
  unwrap = Duration.as_nanos
  io.print(unwrap(make(
    input()
  )))
end`, "input\n2000\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
