package frontend_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/frontend"
)

// A derive in an imported file whose field type has no impl is reported at
// the field, in the file that holds the derive, with that file's source
// line. The error is the program's, found with the entry's, so it was printed
// with the entry's path at the imported file's line.
func TestDeriveMissingImplInAnImportedFileNamesThatFile(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"main.nomi": `import
    std::io
    child.Holder
end

fn main()
    io.inspect(Holder{n: 1, o: Opaque{f: fn x -> x}})
end
`,
		"child.nomi": `pub struct Opaque
    f: (Int) -> Int
end

pub struct Holder
    n: Int
    o: Opaque
end

derive Equatable for Holder
`,
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	entry := filepath.Join(dir, "main.nomi")
	_, err := frontend.New(frontend.Config{}).CheckFileSource(entry, files["main.nomi"], frontend.Mode{})
	ds, ok := err.(frontend.Diagnostics)
	if !ok {
		t.Fatalf("the front end accepts a derive over a field with no Equatable impl: %v", err)
	}
	for _, d := range ds {
		if !strings.Contains(d.Message, "no impl of `Equatable` for `Opaque`") {
			continue
		}
		if filepath.Base(d.Path) != "child.nomi" || d.Line != 7 || d.Col != 5 || strings.TrimSpace(d.SourceLine) != "o: Opaque" {
			t.Fatalf("the missing impl is reported at %s:%d:%d (%q), want child.nomi:7:5 (\"o: Opaque\")", d.Path, d.Line, d.Col, d.SourceLine)
		}
		return
	}
	t.Fatalf("no missing-impl diagnostic for Opaque among %v", ds)
}

// checkOneFile checks src as the entry main.nomi of a directory of its own.
func checkOneFile(t *testing.T, src string) error {
	t.Helper()
	entry := filepath.Join(t.TempDir(), "main.nomi")
	if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := frontend.New(frontend.Config{}).CheckFileSource(entry, src, frontend.Mode{})
	return err
}

const genericFromJsonSrc = `import std::json.{FromJson, Json, ToJson}

struct Plain
    n: Int
end

struct Pair<T>
    a: T
    b: T
end

derive ToJson for Pair
derive FromJson for Pair

enum Opt<T>
    Nothing
    Some T
end

derive ToJson for Opt
derive FromJson for Opt

fn main() -> Result<Unit, Json.Error>
    _p: Pair<Int> = Json.decode_as("{\"a\": 1, \"b\": 2}")?
    _o: Opt<String> = Json.decode_as("{\"Some\": \"x\"}")?
    Ok(Unit)
end
`

// A derived FromJson on a generic struct and enum, named bare in the derive,
// checks as a hand-written `impl FromJson for Pair<T> where T: FromJson`
// does. The header named the bare type, so `self` in `from_json`'s return
// was the unapplied `Pair`, and the derive's own `Result<Pair<T>, ...>`
// matched nothing.
func TestDeriveFromJsonOnGenericTypesChecks(t *testing.T) {
	if err := checkOneFile(t, genericFromJsonSrc); err != nil {
		t.Fatalf("the front end rejects a derived FromJson on generic types: %v", err)
	}
}

// The derived FromJson's implied bound `T: FromJson` is checked where the
// type is used: decoding a Pair<Plain> when Plain has no FromJson is an
// error that names Plain and the interface.
func TestDeriveFromJsonOnGenericTypesNeedsTheBound(t *testing.T) {
	src := strings.Replace(genericFromJsonSrc, "p: Pair<Int>", "p: Pair<Plain>", 1)
	err := checkOneFile(t, src)
	ds, ok := err.(frontend.Diagnostics)
	if !ok {
		t.Fatalf("the front end accepts decoding a Pair<Plain> with no FromJson for Plain: %v", err)
	}
	for _, d := range ds {
		if strings.Contains(d.Message, "no impl of `FromJson` for `Plain`") {
			return
		}
	}
	t.Fatalf("no missing-FromJson diagnostic for Plain among %v", ds)
}
