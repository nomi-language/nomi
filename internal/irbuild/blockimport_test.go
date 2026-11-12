package irbuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A std file imported at the top of a block binds its qualifier there only;
// a type named through that qualifier (a variant's owner, a struct-shaped
// variant's owner, an annotation) resolves as through a file-level import.
func TestBlockImport_TypesThroughTheQualifier(t *testing.T) {
	verifyLambdaProgram(t, `import std::io

fn encoded() -> String
    import std::json
    v: json.Json = json.Json.Int(3)
    Debug.inspect(v)
end

fn main()
    import std::json
    io.inspect(json.Json.Null)
    io.print(encoded())
    io.inspect(json.Json.decode("[1]"))
    io.inspect(json.Json.Arr([json.Json.Bool(True)]))
end
`, "null\n3\nOk([1])\n[true]\n")
}

// One qualifier imported at the top of several blocks names, in each, the
// file that block imports: a function's, another function's, a lambda's
// enclosing block and a binding's block.
func TestBlockImport_OneQualifierOfTwoFiles(t *testing.T) {
	verifyLambdaProgram(t, `import std::io

fn a() -> Int
    import std::duration as d
    d.Duration.as_micros(d.Duration.seconds(1))
end

fn b() -> String
    import std::json as d
    d.Json.encode(d.Json.Null)
end

fn c() -> List<String>
    import std::json as d
    [1, 2] |> Iter.map(fn _ -> d.Json.encode(d.Json.Bool(True))) |> Iter.to_list()
end

fn main()
    io.print(Int.to_string(a()))
    io.print(b())
    io.inspect(c())
    w = begin
        import std::duration as d
        d.Duration.as_millis(d.Duration.seconds(2))
    end
    io.print(Int.to_string(w))
end
`, "1000000\nnull\n[\"true\", \"true\"]\n2000\n")
}

// A project file imported only at the top of a block is part of the program
// as a file-level import's is: discovery follows the block's import, and
// the call through its qualifier runs, from the entry and from a file the
// entry reaches only that way.
func TestBlockImport_ProjectFileRuns(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	dir := t.TempDir()
	files := map[string]string{
		"main.nomi": `import std::io

fn main()
    import shapes
    io.inspect(shapes.area(2))
end
`,
		"shapes.nomi": `pub fn area(r: Int) -> Int
    import units
    units.scale(r * 3)
end
`,
		"units.nomi": `pub fn scale(n: Int) -> Int
    n * 10
end
`,
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0600); err != nil {
			t.Fatal(err)
		}
	}
	entry := filepath.Join(dir, "main.nomi")
	if _, err := Analyze(entry); err != nil {
		t.Fatalf("the front end rejects this, so nothing below is tested: %v", err)
	}
	got := vmReference(entry)
	if got.stdout != "60\n" || got.stderr != "" || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s", got.exit, got.stdout, got.stderr)
	}
}

// A block-local struct may name a type an import at the top of its block
// binds, as an expression in the block may.
func TestBlockImport_ABlockLocalTypeNamesIt(t *testing.T) {
	verifyLambdaProgram(t, `import std::io

fn main()
    import b.B
    struct Local
        x: B
    end
    io.inspect(Local{x: B{v: 3}})
end
`, "Local{x: B{v: 3}}\n", map[string]string{"b.nomi": `pub struct B
    v: Int
end
`})
}

// The same, where the imported file names a type of the importing one: the
// two files' types refer to each other only through the block's import, so
// the import graph and the type groups must count it (nomiImportGraph,
// typeDeclEdges), or the field's type is refused as a sibling type cycle.
func TestBlockImport_ClosesATypeCycle(t *testing.T) {
	verifyLambdaProgram(t, `import std::io

pub struct A
    n: Int
end

fn main()
    import b.B
    struct Local
        x: B
    end
    io.inspect(Local{x: B{a: A{n: 1}}})
end
`, "Local{x: B{a: A{n: 1}}}\n", map[string]string{"b.nomi": `import main.A

pub struct B
    a: A
end
`})
}

// An interface an import at the top of a test body binds, which the file
// itself does not import, resolves there as a type: in a `//!` prompt and in
// a `test` declaration, a sibling file's interface and a std one, as an
// annotation and as a call's type argument (bindBlockImports'
// importedIfaces). The name-keyed lookups read the module scope, which the
// body's import does not reach, so each such body declined. std's `//!`
// prompts reach it the same way: std/literals_testing's `build_each<Display,
// Url>(...)` names `Display`, which that module does not import.
func TestBlockImport_AnInterfaceOnlyATestImports(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	dir := t.TempDir()
	files := map[string]string{
		"main.nomi": `/// Doubles.
//! import shapes.{Shape, Square}
//! import std::html.{Html, ToHtml}
//!
//! s: Shape = Square{side: 3}
//! assert Shape.area(s) == 9
//! assert Iter.map(held<Shape>([Square{side: 2}]), Shape.area) |> Iter.to_list() == [4]
//! h: ToHtml = "<b>"
//! assert Html.to_string(ToHtml.to_html(h)) == "&lt;b&gt;"
//
pub fn double(n: Int) -> Int
    n * 2
end

pub fn held<I>(values: List<I>) -> List<I>
    values
end

test "an interface the test imports"
    import shapes.{Shape, Square}
    import std::html.{Html, ToHtml}

    s: Shape = Square{side: 3}
    assert Shape.area(s) == double(4) + 1
    assert Iter.map(held<Shape>([Square{side: 2}]), Shape.area) |> Iter.to_list() == [4]
    h: ToHtml = 7
    assert Html.to_string(ToHtml.to_html(h)) == "7"
end
`,
		"shapes.nomi": `pub interface Shape
    fn area(shape: self) -> Int
end

pub struct Square
    side: Int
end

impl Shape for Square
    fn area(s: Square) -> Int
        s.side * s.side
    end
end
`,
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0600); err != nil {
			t.Fatal(err)
		}
	}
	entry := filepath.Join(dir, "main.nomi")
	if _, err := Analyze(entry); err != nil {
		t.Fatalf("the front end rejects this, so nothing below is tested: %v", err)
	}
	got := vmReference(entry)
	if got.exit != 0 || !strings.Contains(got.stdout, "2 passed, 0 failed") {
		t.Fatalf("VM test run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s", got.exit, got.stdout, got.stderr)
	}
}
