package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// typesFixture is a module whose models.nomi and api/ declare types that
// cross as JSON, from the repository root, where TestMain runs the tests.
var typesFixture = filepath.Join("cmd", "nomi", "testdata", "types", "shop")

// typesGolden is what `nomi types models.nomi api` writes in typesFixture:
// structs, enums with and without payloads, a generic type, Option, lists,
// sets, vectors, maps, a distinct type, a type alias, std types, a
// reference across files, two Users of different files, and a hand-written
// ToJson. NOMI_REGENERATE_EXPECTATIONS=1 writes it.
var typesGolden = filepath.Join("cmd", "nomi", "testdata", "types", "shop.d.ts.golden")

// requestsGolden is what `nomi types requests.nomi` writes in typesFixture:
// types that derive FromJson and have no ToJson, whose Option fields and
// fields with a default are optional properties, Presence fields in each
// kind of type, a Presence outside a field, and generic types whose field
// of a type parameter some instantiation, directly or through another
// type's parameter, puts a Presence at.
var requestsGolden = filepath.Join("cmd", "nomi", "testdata", "types", "requests.d.ts.golden")

func TestTypesCommand_WritesTheFixturesDeclarations(t *testing.T) {
	checkTypesGolden(t, typesGolden, "models.nomi", "api")
}

func TestTypesCommand_ATypeOnlyReadIsWhatFromJsonReads(t *testing.T) {
	checkTypesGolden(t, requestsGolden, "requests.nomi")
}

// checkTypesGolden runs `nomi types paths...` in typesFixture and holds its
// output, and what -o writes, to golden. NOMI_REGENERATE_EXPECTATIONS=1
// writes golden.
func checkTypesGolden(t *testing.T, golden string, paths ...string) {
	t.Helper()
	dir, err := filepath.Abs(typesFixture)
	if err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := runNomiSplit(t, t.TempDir(), dir, append([]string{"types"}, paths...)...)
	if err != nil || stderr != "" {
		t.Fatalf("nomi types: %v\n%s%s", err, stdout, stderr)
	}
	if os.Getenv("NOMI_REGENERATE_EXPECTATIONS") == "1" {
		if err := os.WriteFile(golden, []byte(stdout), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if stdout != string(want) {
		t.Errorf("nomi types wrote:\n%s\nwant (%s):\n%s", stdout, golden, want)
	}

	// -o writes the same text to a file, and nothing to stdout.
	out := filepath.Join(t.TempDir(), "api.ts")
	stdout, stderr, err = runNomiSplit(t, t.TempDir(), dir, append(append([]string{"types"}, paths...), "-o", out)...)
	if err != nil || stdout != "" || stderr != "" {
		t.Fatalf("nomi types -o: %v\n%s%s", err, stdout, stderr)
	}
	if got, _ := os.ReadFile(out); string(got) != string(want) {
		t.Errorf("-o wrote:\n%s", got)
	}

	// The output is a valid TypeScript module, when a tsc is at hand.
	if tsc, err := exec.LookPath("tsc"); err == nil {
		if got := runNode(t, os.Environ(), "", "", tsc, "--noEmit", "--strict", out); got.exit != 0 {
			t.Errorf("tsc --strict: exit %d\n%s", got.exit, got.transcript())
		}
	} else {
		t.Log("no tsc on PATH: the output is not type-checked")
	}
}

// A named path whose files declare no type that crosses as JSON, or only
// private ones, is an error, and nothing is written.
func TestTypesCommand_APathWithNoJSONTypesIsAnError(t *testing.T) {
	dir, err := filepath.Abs(typesFixture)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"views/plain.nomi"}, "nomi types: views/plain.nomi declares no type that crosses as JSON\nhelp: derive ToJson or FromJson"},
		{[]string{"views/hidden.nomi"}, "nomi types: views/hidden.nomi declares no type that crosses as JSON"},
		{[]string{"models.nomi", "views"}, "nomi types: no file under views declares a type that crosses as JSON"},
		{[]string{"nomi.toml"}, "nomi types: nomi.toml is not a .nomi file or a directory"},
		{[]string{"missing"}, "nomi types: missing: no such file or directory"},
		{nil, "usage: nomi types <paths...> [-o <file>]"},
	} {
		stdout, stderr, err := runNomiSplit(t, t.TempDir(), dir, append([]string{"types"}, c.args...)...)
		if err == nil || stdout != "" || !strings.Contains(stderr, c.want) {
			t.Errorf("nomi types %v: %v\nstdout:\n%s\nstderr:\n%s\nwant stderr to hold %q", c.args, err, stdout, stderr, c.want)
		}
	}
}

// A type internal to its package is a root, as a pub one is, so an
// application that writes no `pub` gets its types. A private type is no
// root: it is written when a root reaches it (Detail, through the opaque
// Wrapped's field) and not otherwise (Secret).
func TestTypesCommand_InternalTypesAreRootsAndPrivateOnesOnlyReached(t *testing.T) {
	dir := t.TempDir()
	text := `import std::json.ToJson

struct Order
    id: Int
end

derive ToJson for Order

opaque struct Wrapped
    inner: Detail
end

derive ToJson for Wrapped

private struct Detail
    note: String
end

derive ToJson for Detail

private struct Secret
    code: String
end

derive ToJson for Secret

pub struct Shown
    up: Bool
end

derive ToJson for Shown
`
	if err := os.WriteFile(filepath.Join(dir, "models.nomi"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := runNomiSplit(t, t.TempDir(), dir, "types", "models.nomi")
	if err != nil || stderr != "" {
		t.Fatalf("nomi types models.nomi: %v\n%s%s", err, stdout, stderr)
	}
	want := "// Generated by `nomi types models.nomi`. Do not edit.\n" +
		"//\n" +
		"// The JSON each type's ToJson writes, or, for a type with FromJson and no\n" +
		"// ToJson, the JSON its FromJson reads. An Int is a number, exact up to 2^53;\n" +
		"// send one that may be larger as a String.\n\n" +
		"export interface Order {\n  id: number;\n}\n\n" +
		"export interface Wrapped {\n  inner: Detail;\n}\n\n" +
		"export interface Detail {\n  note: string;\n}\n\n" +
		"export interface Shown {\n  up: boolean;\n}\n"
	if stdout != want {
		t.Errorf("nomi types models.nomi wrote:\n%s\nwant:\n%s", stdout, want)
	}
}

// An `impl ToJson` written in another file of the module counts as one in
// the declaring file: Receipt, which derives FromJson, is not a type only
// FromJson reads, and Stamp, which derives nothing, crosses as JSON.
func TestTypesCommand_AToJsonImplInAnotherFileCounts(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"main.nomi": `import
    std::io
    std::json.Json
    json_impls
    models.{Receipt, Stamp}
end

fn main()
    io.print(Json.encode_as(Receipt{total: 3}))
    io.print(Json.encode_as(Stamp{at: 4}))
end
`,
		"models.nomi": `import std::json.FromJson

pub struct Receipt
    total: Int
end

derive FromJson for Receipt

pub struct Stamp
    at: Int
end
`,
		"json_impls.nomi": `import
    std::json.{Json, ToJson}
    models.{Receipt, Stamp}
end

impl ToJson for Receipt
    fn to_json(receipt: Receipt) -> Json
        Json.String("${receipt.total}")
    end
end

impl ToJson for Stamp
    fn to_json(stamp: Stamp) -> Json
        Json.Int(stamp.at)
    end
end
`,
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	stdout, stderr, err := runNomiSplit(t, t.TempDir(), dir, "types", "models.nomi")
	if err != nil || stderr != "" {
		t.Fatalf("nomi types models.nomi: %v\n%s%s", err, stdout, stderr)
	}
	want := "// Generated by `nomi types models.nomi`. Do not edit.\n" +
		"//\n" +
		"// The JSON each type's ToJson writes, or, for a type with FromJson and no\n" +
		"// ToJson, the JSON its FromJson reads. An Int is a number, exact up to 2^53;\n" +
		"// send one that may be larger as a String.\n\n" +
		"/** Receipt's ToJson is written by hand, so its JSON is not known here. */\n" +
		"export type Receipt = unknown;\n\n" +
		"/** Stamp's ToJson is written by hand, so its JSON is not known here. */\n" +
		"export type Stamp = unknown;\n"
	if stdout != want {
		t.Errorf("nomi types models.nomi wrote:\n%s\nwant:\n%s", stdout, want)
	}
}

// A named file that reads an application field is checked under the
// project's entry, whose boot returns the application, as `nomi check .`
// checks it, and only the named file's types are written: main.nomi's
// Status is not.
func TestTypesCommand_ANamedFileIsCheckedUnderItsProjectsBoot(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"main.nomi": `import
    std::io
    std::json.ToJson
    api.{App, show}
end

pub struct Status
    up: Bool
end

derive ToJson for Status

fn boot() -> Result<App, String>
    Ok(App{context: Context.root(), db: "shop.db"})
end

fn main()
    io.print(show(1).source)
end
`,
		"api.nomi": `import std::json.ToJson

pub struct App
    context: Context
    db: String
end

pub struct Item
    id: Int
    source: String
end

derive ToJson for Item

pub fn show(id: Int) -> Item
    Item{id:, source: App.db}
end
`,
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	stdout, stderr, err := runNomiSplit(t, t.TempDir(), dir, "types", "api.nomi")
	if err != nil || stderr != "" {
		t.Fatalf("nomi types api.nomi: %v\n%s%s", err, stdout, stderr)
	}
	want := "// Generated by `nomi types api.nomi`. Do not edit.\n" +
		"//\n" +
		"// The JSON each type's ToJson writes, or, for a type with FromJson and no\n" +
		"// ToJson, the JSON its FromJson reads. An Int is a number, exact up to 2^53;\n" +
		"// send one that may be larger as a String.\n\n" +
		"export interface Item {\n  id: number;\n  source: string;\n}\n"
	if stdout != want {
		t.Errorf("nomi types api.nomi wrote:\n%s\nwant:\n%s", stdout, want)
	}
}
