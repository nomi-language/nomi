package vmhost_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/vmhost"
)

// loadFiles writes files into a fresh directory and loads its main.nomi.
func loadFiles(t *testing.T, files map[string]string) (*vmhost.Program, string, error) {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p, err := vmhost.Load(filepath.Join(dir, "main.nomi"))
	return p, dir, err
}

const exportGame = `struct Game {
    word: String
    guesses: List<String>
}

derive ToJson for Game
derive FromJson for Game

export fn new_game(word: String): Game {
    Game{word, guesses: []}
}

export fn guess(game: Game, letter: String): Game {
    {..game, guesses: [letter, ..game.guesses]}
}

export fn reset(_: Game): Unit {
}
`

// A program whose only entry points are exported functions checks and
// loads with no main, refuses `nomi run` and a native build by pointing at
// `nomi build --web`, and builds for the web.
func TestExport_AProgramWithNoMainBuildsForTheWeb(t *testing.T) {
	p, _, err := loadFiles(t, map[string]string{"main.nomi": "import std/json.{ToJson, FromJson}\n\n" + exportGame})
	if err != nil {
		t.Fatalf("the front end rejects a program of exported functions: %v", err)
	}
	if p.HasMain() || !p.HasExports() {
		t.Fatalf("HasMain=%v HasExports=%v, want false and true", p.HasMain(), p.HasExports())
	}
	if err := p.Unsupported(); err != nil {
		t.Fatalf("an exported function does not lower: %v", err)
	}
	const pointer = "its exported functions are called by a host, so build it for a page with `nomi build --web`, or as a WASI reactor with `nomi build --target wasip1/wasm`"
	if err := p.RequireMain("run"); err == nil || !strings.Contains(err.Error(), "so there is no program to run; "+pointer) {
		t.Errorf("RequireMain(run) = %v, want it to point at `nomi build --web`", err)
	}
	if _, _, err := p.BuildImage(); err == nil || !strings.Contains(err.Error(), "so there is no program to build; "+pointer) {
		t.Errorf("BuildImage = %v, want it to point at `nomi build --web`", err)
	}
	if img, _, err := p.BuildExportsImage(); err != nil || len(img) == 0 {
		t.Errorf("BuildExportsImage = %d bytes, %v; want an image", len(img), err)
	}
}

// An exported function main never calls is still lowered, so `nomi check`
// reports a body the VM cannot run.
func TestExport_AnUnreachedExportedFunctionIsChecked(t *testing.T) {
	src := `import std/io

fn main() {
    io.print("hi")
}

export fn short(x: Int): Bool {
    x > 0 and case x {
        1 -> return True
        _ -> return False
    }
}
`
	p, _, err := loadFiles(t, map[string]string{"main.nomi": src})
	if err != nil {
		t.Fatalf("the front end rejects this, so it no longer exercises lowering: %v", err)
	}
	err = p.Unsupported()
	var ds frontend.Diagnostics
	if !errors.As(err, &ds) || len(ds) != 1 || ds[0].Line != 8 || !strings.Contains(ds[0].Message, "so `fn short` cannot run") {
		t.Fatalf("Unsupported = %v, want one diagnostic at line 8 naming `fn short`", err)
	}
	if _, _, err := p.BuildExportsImage(); err == nil {
		t.Error("BuildExportsImage builds a program whose exported function cannot run")
	}
	if _, _, err := p.BuildImage(); err != nil {
		t.Errorf("BuildImage refuses a program whose main runs: %v", err)
	}
}

func TestExport_SignatureRefusals(t *testing.T) {
	header := "import std/json.{ToJson, FromJson}\n\n" + exportGame + `
enum Outcome {
    Won
    Lost
}

derive ToJson for Outcome

struct Plain {
    x: Int
}

`
	base := strings.Count(header, "\n") + 1
	cases := []struct {
		name, decl, want, hint string
		col                    int
	}{
		{"generic", "export fn ident<T>(x: T): T {\n    x\n}\n",
			"exported function `ident` cannot be generic: a host calls it with JSON values of one known type",
			"declare it with concrete types, as a wrapper over the generic function", 17},
		{"interface parameter", "export fn shown(d: Display): String {\n    Display.to_string(d)\n}\n",
			"parameter `d` of exported function `shown` has type `Display`, which is not concrete: a host passes JSON values of one known type",
			"", 20},
		{"struct without FromJson", "export fn sum(p: Plain): Int {\n    p.x\n}\n",
			"parameter `p` of exported function `sum` cannot cross as JSON: `Plain` does not implement `FromJson`",
			"add `derive FromJson for Plain`", 18},
		{"struct in a list", "export fn count(ps: List<Plain>): Int {\n    List.count(ps)\n}\n",
			"parameter `ps` of exported function `count` cannot cross as JSON: `Plain`, in `List<Plain>`, does not implement `FromJson`",
			"add `derive FromJson for Plain`", 21},
		{"result without ToJson", "export fn make(x: Int): Plain {\n    Plain{x}\n}\n",
			"the result of exported function `make` cannot cross as JSON: `Plain` does not implement `ToJson`",
			"add `derive ToJson for Plain`", 25},
		{"enum parameter", "export fn won(o: Outcome): Bool {\n    o == Won\n}\n",
			"parameter `o` of exported function `won` cannot cross as JSON: `Outcome` does not implement `FromJson`",
			"add `derive FromJson for Outcome`", 18},
		{"tuple", "export fn add(p: (Int, Int)): Int {\n    p.0 + p.1\n}\n",
			"parameter `p` of exported function `add` cannot cross as JSON: `(Int, Int)` does not implement `FromJson`",
			"a parameter's type must implement `FromJson` and a result's `ToJson`: std implements both for Int, Float, String, Bool, List, Vector, Set, Maybe and Map with String keys, and a struct or enum the program declares can derive them", 18},
		{"map with Int keys", "export fn size(m: Map<Int, String>): Int {\n    Map.size(m)\n}\n",
			"parameter `m` of exported function `size` cannot cross as JSON: `Map<Int, String>` does not implement `FromJson`",
			"a parameter's type must implement `FromJson` and a result's `ToJson`: std implements both for Int, Float, String, Bool, List, Vector, Set, Maybe and Map with String keys, and a struct or enum the program declares can derive them", 19},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := loadFiles(t, map[string]string{"main.nomi": header + c.decl})
			if err == nil {
				t.Fatalf("the front end accepts:\n%s", c.decl)
			}
			var ds frontend.Diagnostics
			if !errors.As(err, &ds) {
				t.Fatalf("got %T %v, want diagnostics", err, err)
			}
			for _, d := range ds {
				if d.Message != c.want {
					continue
				}
				if d.Line != base || d.Col != c.col {
					t.Errorf("at %d:%d, want %d:%d", d.Line, d.Col, base, c.col)
				}
				if c.hint != "" && (len(d.Hints) != 1 || d.Hints[0] != c.hint) {
					t.Errorf("hints %q, want %q", d.Hints, c.hint)
				}
				return
			}
			t.Fatalf("no diagnostic %q in:\n%v", c.want, err)
		})
	}
}

// Two files of one program exporting one name is an error at the second,
// naming the first.
func TestExport_NamesAreUniqueAcrossTheProgram(t *testing.T) {
	_, dir, err := loadFiles(t, map[string]string{
		"main.nomi":  "import other.{helper}\n\nexport fn guess(n: Int): Int {\n    helper(n)\n}\n",
		"other.nomi": "pub fn helper(n: Int): Int {\n    n\n}\n\nexport fn guess(n: Int): Int {\n    n\n}\n",
	})
	if err == nil {
		t.Fatal("the front end accepts two files exporting `guess`")
	}
	var ds frontend.Diagnostics
	if !errors.As(err, &ds) || len(ds) != 1 {
		t.Fatalf("got %v, want one diagnostic", err)
	}
	d := ds[0]
	want := "exported function `guess` is already exported by "
	if !strings.HasPrefix(d.Message, want) || !strings.HasSuffix(d.Message, ".nomi: a host calls exported functions by name, so each name is exported once") {
		t.Errorf("message %q", d.Message)
	}
	if len(d.Related) != 1 || d.Related[0].Message != "`guess` is exported here" {
		t.Fatalf("related %+v, want the other declaration", d.Related)
	}
	// The files are built in a fixed order, so one of them is first.
	first, second := filepath.Join(dir, "other.nomi"), filepath.Join(dir, "main.nomi")
	if d.Path == first {
		first, second = second, first
	}
	if d.Path != second || d.Related[0].Path != first || d.Line != 3 && d.Line != 5 || d.Col != 11 {
		t.Errorf("at %s:%d:%d, related %s, want the two files' declarations", d.Path, d.Line, d.Col, d.Related[0].Path)
	}
}

// nomi.d.ts declares a typed `call` per exported function, mapping each
// Nomi type to the TypeScript type of the JSON it crosses as.
func TestExport_WebDeclarations(t *testing.T) {
	src := `import std/json.{Json, ToJson, FromJson}

struct Point {
    x_pos: Int
    label: Maybe<String>
}

derive ToJson for Point with ToJson.Options{rename_all: Json.Case.Camel}
derive FromJson for Point with FromJson.Options{rename_all: Json.Case.Camel}

enum Shape {
    Dot
    Circle {radius: Float}
    Pair (Int, String)
    Tagged Point
}

derive ToJson for Shape
derive FromJson for Shape

struct Opaque {
    n: Int
}

impl ToJson for Opaque {
    fn to_json(value: Opaque): Json {
        Json.Int(value.n)
    }
}

export fn shapes(points: List<Point>, scale: Float): Vector<Shape> {
    Iter.map(points, |p| if scale > 0.0 { Shape.Tagged(p) } else { Shape.Dot }) |> Iter.to_vector()
}

export fn counts(new: Map<String, Int>, seen: Set<String>): Maybe<Bool> {
    Some(Map.size(new) > Set.size(seen))
}

export fn hidden(): Opaque {
    Opaque{n: 1}
}

export fn reset() {
}
`
	p, _, err := loadFiles(t, map[string]string{"main.nomi": src})
	if err != nil {
		t.Fatal(err)
	}
	got := p.WebDeclarations()
	for _, want := range []string{
		"for main.nomi",
		`export interface Point { xPos: number; label: string | null }`,
		`export type Shape = "Dot" | { Circle: { radius: number } } | { Pair: [number, string] } | { Tagged: Point };`,
		`  call(name: "shapes", points: Point[], scale: number): Promise<Shape[]>;`,
		`  call(name: "counts", new_: Record<string, number>, seen: string[]): Promise<boolean | null>;`,
		`  call(name: "hidden"): Promise<unknown>;`,
		`  call(name: "reset"): Promise<void>;`,
		"export function load(url: string | URL, options?: NomiLoadOptions): Promise<NomiProgram>;",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("nomi.d.ts lacks\n%s\nin\n%s", want, got)
		}
	}
}
