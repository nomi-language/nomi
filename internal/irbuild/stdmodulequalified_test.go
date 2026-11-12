package irbuild

import "testing"

// Std types reached only through a whole-module import's qualifier, in a file
// that declares its own `Task`, `Channel`, `Supervisor` and `Error`:
// `tasks.Task.spawn` and `tasks.Task<Int>`, `channels.Channel.buffered<T>`,
// `supervisors.Supervisor.new` with named and defaulted arguments in boot, an
// app field of type `supervisors.Supervisor`, `random.Error.OsEntropy{...}`
// built and matched, and `json.Json.Int(3)` and `json.Json.Null`. The
// intrinsic arms recognise std's type by the declaration the owner resolves
// to (stdIntrinsicOwner), so the local types keep their own calls.
func TestStdModuleQualified_TypesResolveByDeclaration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const want = "" +
		"task mine\n" +
		"3\n" +
		"local\n" +
		"my error\n" +
		"42\n" +
		"Some(hi)\n" +
		"supervised\n" +
		"entropy: x\n" +
		"weight: 0.5\n" +
		"other\n" +
		"OS entropy unavailable: a\n" +
		"[3,null]\n"
	got := vmReference(fixture("std_module_qualified.nomi"))
	if got.stdout != want || got.exit != 0 || got.stderr != "" {
		t.Fatalf("std types named through their module's qualifier:\nwant stdout=%q\ngot  %s", want, got)
	}
}

// A literal whose prefix names a std type through its module builds as the
// same literal under the bare name does: a struct literal of a dotted
// struct (`json.Json.ShapeError{...}`), and a map or list literal attached
// to an enum's variant (`json.Json.Obj{...}`, `json.Json.Arr[...]`).
func TestStdModuleQualified_LiteralPrefixes(t *testing.T) {
	verifyLambdaProgram(t, `import
  std::io
  std::json
end
fn main()
  e = json.Json.ShapeError{path: ["a"], expected: "Int", got: "missing"}
  io.print(e.expected)
  j = json.Json.Obj{"n" => json.Json.Int(1), "l" => json.Json.Arr[json.Json.Null]}
  io.print(json.Json.encode(j))
end`, "Int\n{\"n\":1,\"l\":[null]}\n")
}

// A std interface named through its module is the interface the bare name
// is: a call dispatches on the operand, a bounded generic's call on its
// instance, an existential's on the value it holds, and the function as a
// value is instantiated at the type its position expects.
func TestStdModuleQualified_InterfaceCalls(t *testing.T) {
	verifyLambdaProgram(t, `import
  std::display
  std::io
end
struct Point
  x: Int
end
impl Display for Point
  fn to_string(p: Point) -> String
    "P${p.x}"
  end
end
fn show<T>(x: T) -> String where T: Display
  display.Display.to_string(x)
end
fn main()
  io.print(display.Display.to_string(3))
  io.print(display.Display.to_string(Point{x: 4}))
  d: Display = Point{x: 5}
  io.print(display.Display.to_string(d))
  io.print(show(6))
  [1, 2] |> Iter.map(display.Display.to_string) |> String.join(",") |> io.print()
end`, "3\nP4\nP5\n6\n1,2\n")
}

// A std interface named through its module in a file that declares its own
// type of the interface's name is still the module's interface: the local
// `struct DateParts` (or `ToJson`, `FromJson`) takes the bare name, and its
// impl of the interface is one implementor among others. A call dispatches
// on the operand (std's impl for a `Date`, this file's for the local type,
// another file's, a derived one) or on the type its result is
// (`json.FromJson.from_json`), a bounded generic's call on its instance, and
// the function as a value is instantiated at the type its position expects.
func TestStdModuleQualified_InterfaceBesideALocalTypeOfItsName(t *testing.T) {
	verifyLambdaProgram(t, `import
  std::calendar
  std::calendar.Date
  std::io
  std::json
  std::json.Json
  stamp
end
struct DateParts
  x: Int
end
impl calendar.DateParts for DateParts
  fn year(p: DateParts) -> Int
    p.x
  end
  fn month(p: DateParts) -> Int
    p.x
  end
  fn day(p: DateParts) -> Int
    p.x
  end
end
impl json.FromJson for DateParts
  fn from_json(json: Json) -> Result<DateParts, Json.ShapeError>
    case json
      Json.Int(n) -> Result.Ok(DateParts{x: n})
      _ -> Result.Ok(DateParts{x: 0})
    end
  end
end
struct ToJson
  x: Int
end
struct FromJson
  x: Int
end
fn years<T>(x: T) -> Int where T: calendar.DateParts
  calendar.DateParts.year(x)
end
fn main()
  d = Date`+"`2024-03-05`"+`
  io.print(Int.to_string(calendar.DateParts.year(d)))
  io.print(Int.to_string(calendar.DateParts.year(DateParts{x: 7})))
  io.print(Int.to_string(calendar.DateParts.year(stamp.Stamp{y: 1999})))
  io.print(Int.to_string(years(d)))
  io.print(Int.to_string(years(DateParts{x: 8})))
  [DateParts{x: 5}] |> Iter.map(calendar.DateParts.year) |> Iter.map(Int.to_string) |> String.join(",") |> io.print()
  [d] |> Iter.map(calendar.DateParts.year) |> Iter.map(Int.to_string) |> String.join(",") |> io.print()
  io.print(json.Json.encode(json.ToJson.to_json(stamp.Stamp{y: 3})))
  io.print(json.Json.encode(json.ToJson.to_json(4)))
  r: Result<DateParts, Json.ShapeError> = json.FromJson.from_json(Json.Int(9))
  io.print(Int.to_string(Result.with_default(r, DateParts{x: 0}).x))
  io.print(Int.to_string(ToJson{x: 6}.x + FromJson{x: 4}.x))
end`, "2024\n7\n1999\n2024\n8\n5\n2024\n{\"y\":3}\n4\n9\n10\n", map[string]string{
		"stamp.nomi": `import
  std::calendar
  std::json.ToJson
end
pub struct Stamp
  y: Int
end
impl calendar.DateParts for Stamp
  fn year(s: Stamp) -> Int
    s.y
  end
  fn month(s: Stamp) -> Int
    s.y
  end
  fn day(s: Stamp) -> Int
    s.y
  end
end
derive ToJson for Stamp
`,
	})
}
