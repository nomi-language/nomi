package irbuild

import (
	"os"
	"path/filepath"
	"testing"
)

// A derived FromJson decodes a field of a generic std container through
// `json$.FromJson.from_json<T>(j)`, std's interface named through the
// hidden module import, with the field's type as the turbofish: a Map, a
// List and an alias of a Map. The type argument selects the impl, as for
// `FromJson.from_json<T>`, so each call reaches the container's impl the
// checker records. The golden set holds the program, so the callee
// verifier sees a derived decode of each container.
func TestDerivedJsonContainerFields_RoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	src := `import std::io
import std::json.{FromJson, Json, ToJson}

typealias Key String

typealias Counts Map<Key, Int>

struct Tally
    counts: Counts
    by_name: Map<String, Int>
    names: List<String>
end

derive Equatable for Tally
derive ToJson for Tally
derive FromJson for Tally

fn main()
    t = Tally{counts: {"a" => 1}, by_name: {"b" => 2}, names: ["c"]}
    io.print(Json.to_string(ToJson.to_json(t)))
    io.print("${FromJson.from_json<Tally>(ToJson.to_json(t)) == Ok(t)}")
end
`
	path := filepath.Join(t.TempDir(), "main.nomi")
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Analyze(path); err != nil {
		t.Fatalf("the front end rejects this, so nothing below is tested: %v", err)
	}
	const want = "{\"counts\":{\"a\":1},\"by_name\":{\"b\":2},\"names\":[\"c\"]}\nTrue\n"
	got := vmReference(path)
	if got.stdout+got.stderr != want || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s--- want ---\n%s", got.exit, got.stdout, got.stderr, want)
	}
	if golden := goldenReference(t, path); golden.stdout+golden.stderr != want || golden.exit != 0 {
		t.Errorf("golden record (exit %d):\n%s%s", golden.exit, golden.stdout, golden.stderr)
	}
}
