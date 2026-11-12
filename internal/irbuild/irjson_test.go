package irbuild

import (
	"testing"
)

const irJSONTourSrc = `import
  std::json.{FromJson, Json, ToJson}
  std::json.Json.Case.Camel
end

struct User
  first_name: String
  age: Int
end

derive ToJson for User with ToJson.Options{rename_all: Camel}
derive FromJson for User with FromJson.Options{rename_all: Camel}

fn main() -> Result<Unit, Json.ShapeError>
  user = User{first_name: "Ada", age: 36}
  shape = User.to_json(user)

  dbg Json.encode(shape)

  user = User.from_json(shape)?

  user
  |> ToJson.to_json()
  |> Json.encode()
  |> dbg

  Ok(Unit)
end
`

// The Tour's derive-and-encode program (interfaces-and-dispatch.md:L468).
func TestIRJSON_TourDeriveRoundTrip(t *testing.T) {
	verifyLambdaProgram(t, irJSONTourSrc, "dbg line 18: Json.encode(shape) = \"{\\\"firstName\\\":\\\"Ada\\\",\\\"age\\\":36}\"\n"+
		"dbg line 25:\n  user\n  |> ToJson.to_json()\n  |> Json.encode()\n  = \"{\\\"firstName\\\":\\\"Ada\\\",\\\"age\\\":36}\"\n")
}

// Derived ToJson/FromJson over scalar fields, through Json.encode and
// Json.decode, with a missing field and a parse error.
func TestIRJSON_EncodeDecodeRoundTrips(t *testing.T) {
	verifyLambdaProgram(t, irJSONRoundTripSrc, `dbg line 35: text = "{\"x\":3,\"y\":-4}"
dbg line 36: point_of(text) = "point 3,-4"
dbg line 37: point_of("{\"y\": 2, \"x\": 7}") = "point 7,2"
dbg line 38: point_of("{\"x\": 1}") = "shape error: expected Int, got missing"
dbg line 39: point_of("{\"x\": 1,") = "decode error at 1:9: unexpected end of input: incomplete JSON value"
dbg line 42: flag_text = "{\"label\":\"a \\\"quoted\\\" label\",\"ratio\":0.25,\"on\":true}"
dbg line 51: back = Flag{label: "a \"quoted\" label", ratio: 0.25, on: True}
`)
}

const irJSONRoundTripSrc = `import
  std::json.{FromJson, Json, ToJson}
end

struct Point
  x: Int
  y: Int
end

struct Flag
  label: String
  ratio: Float
  on: Bool
end

derive ToJson for Point
derive FromJson for Point
derive ToJson for Flag
derive FromJson for Flag

fn point_of(text: String) -> String
  case Json.decode(text)
    Err(e) -> "decode error at ${e.line}:${e.col}: ${e.message}"
    Ok(shape) ->
      case Point.from_json(shape)
        Ok(p) -> "point ${p.x},${p.y}"
        Err(e) -> "shape error: expected ${e.expected}, got ${e.got}"
      end
  end
end

fn main()
  p = Point{x: 3, y: -4}
  text = Json.encode(Point.to_json(p))
  dbg text
  dbg point_of(text)
  dbg point_of("{\"y\": 2, \"x\": 7}")
  dbg point_of("{\"x\": 1}")
  dbg point_of("{\"x\": 1,")
  flag = Flag{label: "a \"quoted\" label", ratio: 0.25, on: True}
  flag_text = Json.encode(ToJson.to_json(flag))
  dbg flag_text
  back = case Json.decode(flag_text)
    Ok(shape) ->
      case Flag.from_json(shape)
        Ok(decoded) -> decoded
        Err(_) -> flag
      end
    Err(_) -> flag
  end
  dbg back
  Unit
end
`

// A decoded array and object, rendered by std's Debug, which encodes, and
// rebuilt in an attach literal.
func TestIRJSON_DecodedValueAndDebug(t *testing.T) {
	verifyLambdaProgram(t, `import
  std::json.{Json}
end

fn wrap(j: Json) -> Json
  Json.Obj{"items" => j}
end

fn main()
  shape = case Json.decode("[1, {\"ok\": true}, null, 2.5]")
    Ok(j) -> wrap(j)
    Err(_) -> Json.Null
  end
  dbg shape
  Unit
end
`, "dbg line 14: shape = {\"items\":[1,{\"ok\":true},null,2.5]}\n")
}

// A nested pattern inside a Json payload (`Json.Arr([Json.Int(n), ..])`)
// retains through the nested list test and prints the expected output.
func TestIRJSON_NestedPayloadPatternRetains(t *testing.T) {
	verifyLambdaProgram(t, `import
  std::io
  std::json.{Json}
end

fn first(j: Json) -> Int
  case j
    Json.Arr([Json.Int(n), .._rest]) -> n
    _ -> 0
  end
end

fn main()
  io.print(first(Json.Arr([Json.Int(7), Json.Null])))
  io.print(first(Json.Arr([Json.Null])))
end
`, "7\n0\n")
}

// Written std/json constructors outside std, alone, bound and nested, run on
// the VM and print the expected output.
func TestIRJSON_WrittenConstructors(t *testing.T) {
	verifyLambdaProgram(t, `import std::json.{Json}

fn main()
  dbg Json.encode(Json.Int(7))
  j = Json.String("x")
  dbg Json.encode(j)
  dbg Json.encode(Json.Arr([Json.Int(1 + 2), Json.Null]))
  Unit
end
`, "dbg line 4: Json.encode(Json.Int(7)) = \"7\"\n"+
		"dbg line 6: Json.encode(j) = \"\\\"x\\\"\"\n"+
		"dbg line 7: Json.encode(Json.Arr([Json.Int(1 + 2), Json.Null])) = \"[3,null]\"\n")
}

// A derived FromJson and ToJson over fields whose types are instances of the
// program's own generic types: an enum whose FromJson is an owner function
// with a where clause, reached as `Field<String>.from_json(json)` with no
// operand naming the instance, a generic struct, and both nested in a List
// and in each other.
func TestIRJSON_DerivedFieldsOfGenericInstances(t *testing.T) {
	verifyLambdaProgram(t, `import std::json.{FromJson, Json, ToJson}

enum Field<T>
  Missing
  Null
  Given T
end

impl FromJson for Field<T> where T: FromJson
  fn from_json(json: Json) -> Result<Field<T>, Json.ShapeError>
    case json
      .Null -> Ok(Field.Null)
      _ -> T.from_json(json) |> Result.map(Field.Given)
    end
  end
end

impl ToJson for Field<T> where T: ToJson
  fn to_json(value: Field<T>) -> Json
    case value
      .Missing -> Json.Null
      .Null -> Json.Null
      .Given(v) -> ToJson.to_json(v)
    end
  end
end

derive Debug for Field

struct Pair<T>
  left: T
  right: T
end

impl FromJson for Pair<T> where T: FromJson
  fn from_json(json: Json) -> Result<Pair<T>, Json.ShapeError>
    case json
      .Arr([l, r]) -> Ok(Pair{left: T.from_json(l)?, right: T.from_json(r)?})
      _ -> Err(Json.ShapeError{path: [], expected: "a pair", got: "other"})
    end
  end
end

impl ToJson for Pair<T> where T: ToJson
  fn to_json(value: Pair<T>) -> Json
    Json.Arr([ToJson.to_json(value.left), ToJson.to_json(value.right)])
  end
end

derive Debug for Pair

struct Profile
  bio: Field<String>
  age: Field<Int>
  tags: List<Field<String>>
  span: Pair<Int>
  names: Pair<Field<String>>
end

derive FromJson for Profile
derive ToJson for Profile
derive Debug for Profile

fn main() -> Result<Unit, Json.Error>
  p: Profile = Json.decode_as("{\"bio\": null, \"age\": 4, \"tags\": [\"x\", null], \"span\": [1, 2], \"names\": [\"l\", null]}")?
  dbg p
  dbg Json.encode(ToJson.to_json(p))
  Ok(Unit)
end
`, "dbg line 66: p = Profile{bio: Null, age: Given(4), tags: [Given(\"x\"), Null], span: Pair{left: 1, right: 2}, names: Pair{left: Given(\"l\"), right: Null}}\n"+
		"dbg line 67: Json.encode(ToJson.to_json(p)) = \"{\\\"bio\\\":null,\\\"age\\\":4,\\\"tags\\\":[\\\"x\\\",null],\\\"span\\\":[1,2],\\\"names\\\":[\\\"l\\\",null]}\"\n")
}

// A derived FromJson on a generic struct and a generic enum, beside a derived
// ToJson, decodes what the ToJson encodes: at Int, at an instance of the
// enum nested in the struct, and through `FromJson.from_json<Opt<Int>>`,
// whose mismatch is a shape error. The enum's derive names it with its
// parameter (`Opt<T>`) and the struct's bare (`Pair`); both apply the
// header to the declaration's parameters.
func TestIRJSON_DerivedFromJsonOnGenericTypes(t *testing.T) {
	verifyLambdaProgram(t, `import std::json.{FromJson, Json, ToJson}

struct Pair<T>
  a: T
  b: T
end

derive ToJson for Pair
derive FromJson for Pair
derive Debug for Pair

enum Opt<T>
  Nothing
  Some T
end

derive ToJson for Opt
derive FromJson for Opt<T>
derive Debug for Opt

fn main() -> Result<Unit, Json.Error>
  p: Pair<Int> = Json.decode_as("{\"a\": 1, \"b\": 2}")?
  dbg p
  dbg Json.encode(ToJson.to_json(p))
  text = Json.encode(ToJson.to_json(Pair{a: Opt.Some("x"), b: Opt.Nothing}))
  dbg text
  back: Pair<Opt<String>> = Json.decode_as(text)?
  dbg back
  o = FromJson.from_json<Opt<Int>>(Json.Arr([]))
  dbg o
  Ok(Unit)
end
`, "dbg line 23: p = Pair{a: 1, b: 2}\n"+
		"dbg line 24: Json.encode(ToJson.to_json(p)) = \"{\\\"a\\\":1,\\\"b\\\":2}\"\n"+
		"dbg line 26: text = \"{\\\"a\\\":{\\\"Some\\\":\\\"x\\\"},\\\"b\\\":\\\"Nothing\\\"}\"\n"+
		"dbg line 28: back = Pair{a: Some(\"x\"), b: Nothing}\n"+
		"dbg line 30: o = Err(Json.ShapeError{path: [], expected: \"Opt\", got: \"array\"})\n")
}

// A derived FromJson decodes a missing field as
// `FromJson.from_missing<uuid.Uuid>()`, a call with no arguments whose type
// argument is a std type the file names only through its module. No operand
// selects the impl, so the plan for a foreign interface's impl, which selects
// by operands, must not be tried, and the interface's default answers.
func TestIRJSON_DerivedMissingFieldOfAModuleQualifiedStdType(t *testing.T) {
	verifyLambdaProgram(t, `import
  std::io
  std::json.{FromJson, Json}
  std::uuid
end

struct Ticket
  id: uuid.Uuid
end

derive FromJson for Ticket

fn main()
  io.print(Debug.inspect(Result.is_err(FromJson.from_json<Ticket>(Json.Obj({})))))
end
`, "True\n")
}
