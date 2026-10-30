package irbuild

import "testing"

// Every derivable interface run from a file that imports only ToJson and
// FromJson. The bodies `derive ToJson` and `derive FromJson` synthesize build
// `Json.Obj`, `Json.Arr` and `Json.String`, match `Json.Obj(fields)` and build
// `Json.ShapeError`, none of which shapes.nomi binds; the derived code names
// them through the hidden `import std/json as json$` the derive adds.
func TestDeriveInterfaceOnly_RunsWithoutJsonInScope(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const want = "" +
		"False\n" +
		"True\n" +
		"Less\n" +
		"#Point{x: 1, y: 2}\n" +
		"{\"x\":1,\"y\":2}\n" +
		"[\"Dot\",{\"Circle\":2},{\"Pair\":[3,4]},{\"Box\":{\"w\":5,\"h\":6}}]\n" +
		"Ok(#Point{x: 7, y: 8})\n" +
		"Ok(#Tagged{name: \"n\", count: 3})\n" +
		"Err(#Json.ShapeError{path: [\"name\"], expected: \"String\", got: \"missing\"})\n" +
		"Err(#Json.ShapeError{path: [], expected: \"object\", got: \"int\"})\n" +
		"Ok(Meters(4))\n" +
		"9\n"
	got := vmReference(fixture("derive_interface_only/main.nomi"))
	if got.stdout != want || got.exit != 0 || got.stderr != "" {
		t.Fatalf("derives from a file importing only the interface:\nwant stdout=%q\ngot  %s", want, got)
	}
}

// A file that declares its own `Json` derives ToJson and FromJson: the
// derived code means std's `Json` through its hidden import, and the file's
// `Json` stays its own.
func TestDeriveJson_BesideAFilesOwnJson(t *testing.T) {
	verifyLambdaProgram(t, `import {
  std/io
  std/json
  std/json.{FromJson, ToJson}
}
struct Json {
  text: String
}
struct Point {
  x: Int
  y: Int
}
derive ToJson for Point
derive FromJson for Point
fn main() {
  io.print(json.Json.encode(ToJson.to_json(#Point{x: 1, y: 2})))
  io.print(Debug.inspect(json.Json.decode_as<Point>("{\"x\": 3, \"y\": 4}")))
  io.print(Debug.inspect(json.Json.decode_as<Point>("{\"x\": 3}")))
  io.print(#Json{text: "mine"}.text)
}`, "{\"x\":1,\"y\":2}\nOk(#Point{x: 3, y: 4})\nErr(Shape(#Json.ShapeError{path: [\"y\"], expected: \"Int\", got: \"missing\"}))\nmine\n")
}
