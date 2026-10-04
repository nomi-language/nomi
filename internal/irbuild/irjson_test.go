package irbuild

import (
	"testing"
)

const irJSONTourSrc = `import {
  std/json.{FromJson, Json, ToJson}
  std/json.Json.Case.Camel
}

struct User {
  first_name: String
  age: Int
}

derive ToJson for User with ToJson.Options{rename_all: Camel}
derive FromJson for User with FromJson.Options{rename_all: Camel}

fn main(): Result<String, Json.ShapeError> {
  user = User{first_name: "Ada", age: 36}
  shape = User.to_json(user)

  dbg Json.encode(shape)

  user = try User.from_json(shape)

  Ok(
    user
    |> ToJson.to_json()
    |> Json.encode()
    |> dbg
  )
}
`

// The Tour's derive-and-encode program (interfaces-and-dispatch.md:L468).
func TestIRJSON_TourDeriveRoundTrip(t *testing.T) {
	verifyLambdaProgram(t, irJSONTourSrc, "dbg line 18: Json.encode(shape) = \"{\\\"firstName\\\":\\\"Ada\\\",\\\"age\\\":36}\"\n"+
		"dbg line 26:\n  user\n  |> ToJson.to_json()\n  |> Json.encode()\n  = \"{\\\"firstName\\\":\\\"Ada\\\",\\\"age\\\":36}\"\n")
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

const irJSONRoundTripSrc = `import {
  std/json.{FromJson, Json, ToJson}
}

struct Point {
  x: Int
  y: Int
}

struct Flag {
  label: String
  ratio: Float
  on: Bool
}

derive ToJson for Point
derive FromJson for Point
derive ToJson for Flag
derive FromJson for Flag

fn point_of(text: String): String {
  case Json.decode(text) {
    Err(e) -> "decode error at ${e.line}:${e.col}: ${e.message}"
    Ok(shape) ->
      case Point.from_json(shape) {
        Ok(p) -> "point ${p.x},${p.y}"
        Err(e) -> "shape error: expected ${e.expected}, got ${e.got}"
      }
  }
}

fn main() {
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
  back = case Json.decode(flag_text) {
    Ok(shape) ->
      case Flag.from_json(shape) {
        Ok(decoded) -> decoded
        Err(_) -> flag
      }
    Err(_) -> flag
  }
  dbg back
  Unit
}
`

// A decoded array and object, rendered by std's Debug, which encodes, and
// rebuilt in an attach literal.
func TestIRJSON_DecodedValueAndDebug(t *testing.T) {
	verifyLambdaProgram(t, `import {
  std/json.{Json}
}

fn wrap(j: Json): Json {
  Json.Obj{"items" => j}
}

fn main() {
  shape = case Json.decode("[1, {\"ok\": true}, null, 2.5]") {
    Ok(j) -> wrap(j)
    Err(_) -> Json.Null
  }
  dbg shape
  Unit
}
`, "dbg line 14: shape = {\"items\":[1,{\"ok\":true},null,2.5]}\n")
}

// A nested pattern inside a Json payload (`Json.Arr([Json.Int(n), .._])`)
// retains through the nested list test and prints the expected output.
func TestIRJSON_NestedPayloadPatternRetains(t *testing.T) {
	verifyLambdaProgram(t, `import {
  std/io
  std/json.{Json}
}

fn first(j: Json): Int {
  case j {
    Json.Arr([Json.Int(n), .._rest]) -> n
    _ -> 0
  }
}

fn main() {
  io.print(first(Json.Arr([Json.Int(7), Json.Null])))
  io.print(first(Json.Arr([Json.Null])))
}
`, "7\n0\n")
}

// Written std/json constructors outside std, alone, bound and nested, run on
// the VM and print the expected output.
func TestIRJSON_WrittenConstructors(t *testing.T) {
	verifyLambdaProgram(t, `import std/json.{Json}

fn main() {
  dbg Json.encode(Json.Int(7))
  j = Json.String("x")
  dbg Json.encode(j)
  dbg Json.encode(Json.Arr([Json.Int(1 + 2), Json.Null]))
  Unit
}
`, "dbg line 4: Json.encode(Json.Int(7)) = \"7\"\n"+
		"dbg line 6: Json.encode(j) = \"\\\"x\\\"\"\n"+
		"dbg line 7: Json.encode(Json.Arr([Json.Int(1 + 2), Json.Null])) = \"[3,null]\"\n")
}
