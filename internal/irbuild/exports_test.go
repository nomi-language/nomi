package irbuild

import (
	"os"
	"path/filepath"
	"testing"
)

// Result.Exports lists each `export fn` with its lowered body and its
// signature's Nomi types, in file and declaration order, whether or not
// main reaches it: what the image's table of exported functions is built
// from.
func TestExports_ListEachExportedFunctionWithItsBodyAndTypes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.nomi")
	src := `import std/json.{ToJson, FromJson}

struct Game {
    word: String
}

derive ToJson for Game
derive FromJson for Game

fn main() {
}

export fn new_game(word: String) -> Game {
    Game{word}
}

fn helper() -> Int {
    1
}

export fn tries(game: Game, limit: Int) -> Unit {
    _ = #(game, limit)
}
`
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatalf("the front end rejects the fixture, so nothing below is tested: %v", err)
	}
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		name   string
		params []string
		result string
		line   int
	}{
		{"new_game", []string{"word: String"}, "Game", 13},
		{"tries", []string{"game: Game", "limit: Int"}, "Unit", 21},
	}
	if len(res.Exports) != len(want) {
		t.Fatalf("got %d exports, want %d: %+v", len(res.Exports), len(want), res.Exports)
	}
	for i, w := range want {
		e := res.Exports[i]
		if e.Name != w.name || e.Path != path || e.Line != w.line || e.Col != 11 {
			t.Errorf("export %d is %s at %s:%d:%d, want %s at %s:%d:11", i, e.Name, e.Path, e.Line, e.Col, w.name, path, w.line)
		}
		if e.Func == nil || e.Func.Name() != w.name || e.Module == nil || e.Module.FuncFor(e.Func.Sym()) != e.Func {
			t.Errorf("%s: no lowered body in its module (Func %v)", w.name, e.Func)
		}
		if len(e.Params) != len(w.params) {
			t.Fatalf("%s: %d params, want %d", w.name, len(e.Params), len(w.params))
		}
		for j, prm := range e.Params {
			if got := prm.Name + ": " + prm.Type.String(); got != w.params[j] {
				t.Errorf("%s param %d is %q, want %q", w.name, j, got, w.params[j])
			}
		}
		if e.Result == nil || e.Result.String() != w.result {
			t.Errorf("%s result %v, want %s", w.name, e.Result, w.result)
		}
	}
}
