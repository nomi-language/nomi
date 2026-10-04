package irbuild

import "testing"

// Every derivable interface run from a file that imports only ToJson and
// FromJson. The bodies `derive ToJson` and `derive FromJson` synthesize build
// `Json.Obj`, `Json.Arr` and `Json.String`, match `Json.Obj(fields)` and build
// `Json.ShapeError`, none of which shapes.nomi binds; the builder anchors those
// names to std's declarations as the analyzer does (synthstd.go).
func TestDeriveInterfaceOnly_RunsWithoutJsonInScope(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const want = "" +
		"False\n" +
		"True\n" +
		"Less\n" +
		"Point{x: 1, y: 2}\n" +
		"{\"x\":1,\"y\":2}\n" +
		"[\"Dot\",{\"Circle\":2},{\"Pair\":[3,4]},{\"Box\":{\"w\":5,\"h\":6}}]\n" +
		"Ok(Point{x: 7, y: 8})\n" +
		"Ok(Tagged{name: \"n\", count: 3})\n" +
		"Err(Json.ShapeError{path: [\"name\"], expected: \"String\", got: \"missing\"})\n" +
		"Err(Json.ShapeError{path: [], expected: \"object\", got: \"int\"})\n" +
		"Ok(Meters(4))\n" +
		"9\n"
	got := vmReference(fixture("derive_interface_only/main.nomi"))
	if got.stdout != want || got.exit != 0 || got.stderr != "" {
		t.Fatalf("derives from a file importing only the interface:\nwant stdout=%q\ngot  %s", want, got)
	}
}
