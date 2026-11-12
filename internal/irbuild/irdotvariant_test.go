package irbuild

import "testing"

func TestIRDotVariant_BareAndPositionalValues(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
enum Direction
    North
    South
end
enum Signal
    Value Int
    Idle
end
fn direction() -> Direction
    .North
end
fn payload() -> Int
    io.print("payload")
    7
end
fn describe(d: Direction) -> String
  case d
    .North -> "up"
    .South -> "down"
  end
end
fn value(s: Signal) -> Int
  case s
    .Value(n) -> n
    .Idle -> 0
  end
end
fn main()
  io.print(describe(direction()))
  io.print(describe(.South))
  io.print(value(.Value(payload())))
  idle: Signal = .Idle
  io.print(value(idle))
end
`, "up\ndown\npayload\n7\n0\n")
}

func TestIRDotVariant_ImportedDeclarationIdentity(t *testing.T) {
	verifyLambdaProgram(t, `import
    std::io shapes
end
fn choose() -> shapes.Direction
    .North
end
fn describe(d: shapes.Direction) -> String
  case d
    .North -> "up"
    .South -> "down"
  end
end
fn main()
  io.print(describe(choose()))
  io.print(describe(.South))
end
`, "up\ndown\n", map[string]string{
		"shapes.nomi": "pub enum Direction\n    North\n    South\nend",
	})
}

// A `.Variant` whose enum is a std declaration the calling file never names:
// the expected type comes from the std callee's parameter. Every shape the
// checker resolves this way lowers: a pipe and an owner call, a defaulted
// parameter, a payload variant, a struct-shaped variant, a lambda's result,
// and a list element. The file's own `Direction` is a different enum with the
// same variant names, so a resolution by name would build the wrong one.
func TestIRDotVariant_StdEnumFromTheCallee(t *testing.T) {
	verifyLambdaProgram(t, `import
  std::io
  std::json.Json
end
enum Direction
    Ascending
    Descending
end
derive Display for Direction
fn mine(d: Direction) -> Direction
    d
end
fn main()
  xs = [3, 1, 2]
  xs |> Iter.sort(.Descending) |> io.print()
  Iter.sort(xs, .Ascending) |> io.print()
  Iter.sort_by(xs, .Descending, fn n -> n) |> io.print()
  xs |> Iter.sort_with(fn a, b -> if a < b then .Greater else if a > b then .Less else .Equal) |> io.print()
  Decimal.round(Int.to_decimal(5), 1, .HalfEven) |> io.print()
  String.normalize("e\u{301}", .NFC) |> String.length() |> io.print()
  Json.encode(.Arr([.Int(1), .Null])) |> io.print()
  os: List<Ordering> = [.Less, .Greater]
  io.print(os)
  io.print(mine(.Descending))
end
`, "[3, 2, 1]\n[1, 2, 3]\n[3, 2, 1]\n[3, 2, 1]\n5.0\n1\n[1,null]\n[Less, Greater]\nDescending\n")
}

func TestIRDotVariant_StdStructVariantFromTheCallee(t *testing.T) {
	verifyLambdaProgram(t, `import
  std::io
  std::supervisors.Supervisor
end
struct App
  context: Context
  workers: Supervisor
end
fn boot() -> Result<App, String>
  Ok(App{
    context: Context.root(),
    workers: Supervisor.new(
      max_running: 2,
      restart: .Transient,
      backoff: .Exponential{max_restarts: 2},
      on_give_up: .Exit,
    ),
  })
end
fn main()
  io.print("booted")
end
`, "booted\n")
}
