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

// A `.Variant` whose enum is a std declaration the calling file never names:
// the expected type comes from the std callee's parameter. Every shape the
// checker resolves this way lowers: a pipe and an owner call, a defaulted
// parameter, a payload variant, a struct-shaped variant, a lambda's result,
// and a list element. The file's own `Direction` is a different enum with the
// same variant names, so a resolution by name would build the wrong one.
func TestIRDotVariant_StdEnumFromTheCallee(t *testing.T) {
	verifyLambdaProgram(t, `import {
  std/io
  std/json.Json
}
enum Direction { Ascending; Descending }
derive Display for Direction
fn mine(d: Direction): Direction { d }
fn main() {
  xs = [3, 1, 2]
  xs |> Iter.sort(.Descending) |> io.print()
  Iter.sort(xs, .Ascending) |> io.print()
  Iter.sort_by(xs, .Descending, |n| n) |> io.print()
  xs |> Iter.sort_with(|a, b| if a < b { .Greater } else if a > b { .Less } else { .Equal }) |> io.print()
  Decimal.round(Decimal.from_int(5), 1, .HalfEven) |> io.print()
  String.normalize("e\u{301}", .NFC) |> String.length() |> io.print()
  Json.encode(.Arr([.Int(1), .Null])) |> io.print()
  os: List<Ordering> = [.Less, .Greater]
  io.print(os)
  io.print(mine(.Descending))
}
`, "[3, 2, 1]\n[1, 2, 3]\n[3, 2, 1]\n[3, 2, 1]\n5.0\n1\n[1,null]\n[Less, Greater]\nDescending\n")
}

func TestIRDotVariant_StdStructVariantFromTheCallee(t *testing.T) {
	verifyLambdaProgram(t, `import {
  std/io
  std/supervisors.Supervisor
}
struct App {
  context: Context
  workers: Supervisor
}
fn boot(): App {
  App{
    context: Context.root(),
    workers: Supervisor.new(
      max_running: 2,
      restart: .Transient,
      backoff: .Exponential{max_restarts: 2},
      on_give_up: .Exit,
    ),
  }
}
fn main() {
  io.print("booted")
}
`, "booted\n")
}
