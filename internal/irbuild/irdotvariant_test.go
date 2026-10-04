package irbuild

import "testing"

func TestIRDotVariant_BareAndPositionalValues(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
enum Direction { North; South }
enum Signal { Value Int; Idle }
fn direction(): Direction { .North }
fn payload(): Int { io.print("payload") 7 }
fn describe(d: Direction): String {
  case d {
    .North -> "up"
    .South -> "down"
  }
}
fn value(s: Signal): Int {
  case s {
    .Value(n) -> n
    .Idle -> 0
  }
}
fn main() {
  io.print(describe(direction()))
  io.print(describe(.South))
  io.print(value(.Value(payload())))
  idle: Signal = .Idle
  io.print(value(idle))
}
`, "up\ndown\npayload\n7\n0\n")
}

func TestIRDotVariant_ImportedDeclarationIdentity(t *testing.T) {
	verifyLambdaProgram(t, `import { std/io shapes }
fn choose(): shapes.Direction { .North }
fn describe(d: shapes.Direction): String {
  case d {
    .North -> "up"
    .South -> "down"
  }
}
fn main() {
  io.print(describe(choose()))
  io.print(describe(.South))
}
`, "up\ndown\n", map[string]string{
		"shapes.nomi": "pub enum Direction { North; South }",
	})
}
