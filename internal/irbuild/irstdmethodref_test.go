package irbuild

import "testing"

func TestIRStdMethodRef_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"construct and unwrap", `import { std/io std/duration.Duration }
fn main() {
  make = Duration.nanoseconds
  unwrap = Duration.as_nanos
  io.print(unwrap(make(42)))
}`, "42\n"},
		{"higher order", `import { std/io std/duration.Duration }
fn apply(f: (Int) -> Duration, n: Int): Duration { f(n) }
fn main() {
  unwrap = Duration.as_nanos
  io.print(unwrap(apply(Duration.microseconds, 3)))
}`, "3000\n"},
		{"captured callable and operator", `import { std/io std/duration.Duration }
fn main() {
  make = Duration.nanoseconds
  unwrap = Duration.as_nanos
  twice = |n: Int| make(n) + make(n)
  io.print(unwrap(twice(21)))
}`, "42\n"},
		{"branch selected method and operand effects", `import { std/io std/duration.Duration }
fn input(): Int { io.print("input") 2 }
fn main() {
  make = if True { Duration.microseconds } else { Duration.nanoseconds }
  unwrap = Duration.as_nanos
  io.print(unwrap(make(
    input()
  )))
}`, "input\n2000\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
