package lsp

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

const paramTypeDecls = "struct Game {\n    turn: Int\n}\n\nenum Command {\n    Look\n    Quit\n}\n\nstruct UserId {\n    n: Int\n}\n\nstruct Box<T> {\n    v: T\n}\n\ninterface Describe {\n    fn describe(self): String\n}\n\n"

// wantParamItem asserts the completion at src offers exactly one item,
// labeled label, whose edit inserts text.
func wantParamItem(t *testing.T, items []protocol.CompletionItem, label, text string) protocol.CompletionItem {
	t.Helper()
	if len(items) != 1 {
		t.Fatalf("got %d items %v, want only %q", len(items), itemLabels(items), label)
	}
	it := mustItem(t, items, label)
	if te := textEditOf(t, it); te.NewText != text {
		t.Fatalf("insert = %q, want %q", te.NewText, text)
	}
	return it
}

func TestCompletion_ParamTypeFromName(t *testing.T) {
	tests := []struct {
		name, src, label, text string
	}{
		{"module function", "fn slay(game" + cursorMark + ") {}\n", "game: Game", "game: Game"},
		{"module function, unclosed", "fn slay(game" + cursorMark, "game: Game", "game: Game"},
		{"second parameter, unclosed", "fn slay(game: Game, command" + cursorMark + "\n", "command: Command", "command: Command"},
		{"after a typed parameter", "fn slay(x: Map<Int, String>, game" + cursorMark + "\n", "game: Game", "game: Game"},
		{"multiword name", "fn find(user_id" + cursorMark + ") {}\n", "user_id: UserId", "user_id: UserId"},
		{"generic function", "fn run<T>(game" + cursorMark + "\n", "game: Game", "game: Game"},
		{"inherent impl", "impl Game {\n    fn next(game" + cursorMark + "\n}\n", "game: Game", "game: Game"},
		{"interface impl", "impl Describe for Command {\n    fn describe(command" + cursorMark + "): String {\n        \"\"\n    }\n}\n", "command: Command", "command: Command"},
		{"interface declaration", "interface Play {\n    fn play(self, game" + cursorMark + "): Int\n}\n", "game: Game", "game: Game"},
		{"interface declaration, unclosed", "interface Play {\n    fn play(game" + cursorMark + "\n}\n", "game: Game", "game: Game"},
		{"prelude type", "fn total(string" + cursorMark + ") {}\n", "string: String", "string: String"},
		{"parameter on its own line", "fn slay(\n    game: Game,\n    command" + cursorMark + "\n", "command: Command", "command: Command"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items := complete(t, paramTypeDecls+tt.src)
			it := wantParamItem(t, items, tt.label, tt.text)
			if name, _, _ := strings.Cut(tt.text, ":"); it.FilterText == nil || *it.FilterText != name {
				t.Fatalf("filterText = %v, want the parameter's name", it.FilterText)
			}
		})
	}
}

// A client asks once, at the name's first letter, and filters that list
// itself as the name grows, so a prefix must already hold every type whose
// snake_case spelling it starts, and the type the whole name spells ranks
// first.
func TestCompletion_ParamTypeFromAPrefix(t *testing.T) {
	decls := paramTypeDecls + "struct GameState {\n    game: Game\n}\n\n"
	tests := []struct {
		name, src string
		want      []string // in order, at the front of the list
	}{
		{"first letter", "fn slay(g" + cursorMark + ") {}\n", []string{"game: Game", "game_state: GameState"}},
		{"part of a word", "impl Game {\n    fn next(gam" + cursorMark + "\n}\n", []string{"game: Game", "game_state: GameState"}},
		{"the whole name ranks its type first", "fn slay(game" + cursorMark + ") {}\n", []string{"game: Game"}},
		{"a later word", "fn find(user_i" + cursorMark + ") {}\n", []string{"user_id: UserId"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items := complete(t, decls+tt.src)
			labels := itemLabels(items)
			if len(labels) < len(tt.want) {
				t.Fatalf("got %v, want %v first", labels, tt.want)
			}
			for i, want := range tt.want {
				if labels[i] != want {
					t.Fatalf("got %v, want %v first", labels, tt.want)
				}
			}
			for _, it := range items {
				te := textEditOf(t, it)
				if name, _, _ := strings.Cut(te.NewText, ":"); it.FilterText == nil || *it.FilterText != name {
					t.Fatalf("%q: filterText = %v, want %q", it.Label, it.FilterText, name)
				}
			}
		})
	}
}

func TestSnakeCase(t *testing.T) {
	for typeName, want := range map[string]string{
		"Game": "game", "UserId": "user_id", "HTTPClient": "http_client", "Utf8Text": "utf8_text", "IO": "io",
	} {
		if got := snakeCase(typeName); got != want {
			t.Errorf("snakeCase(%q) = %q, want %q", typeName, got, want)
		}
	}
}

func TestCompletion_ParamTypeImported(t *testing.T) {
	dir := writeProject(t, map[string]string{
		"world.nomi": "pub struct Monster {\n    name: String\n}\n",
		"main.nomi":  "fn main() {\n}\n",
	})
	s := NewServer()
	s.docs.SetWorkspaceRoot(dir)
	s.docs.IndexWorkspace(context.Background())
	uri := "file://" + filepath.Join(dir, "main.nomi")

	items := completeIn(t, s, uri, "import world.Monster\n\nfn slay(monster"+cursorMark+") {}\n\nfn main() {\n}\n").Items
	wantParamItem(t, items, "monster: Monster", "monster: Monster")

	// Not imported: the name names nothing in scope, and nothing is offered.
	items = completeIn(t, s, uri, "fn slay(monster"+cursorMark+") {}\n\nfn main() {\n}\n").Items
	if len(items) != 0 {
		t.Fatalf("an unimported type is offered: %v", itemLabels(items))
	}
}

func TestCompletion_ParamTypeGenericSnippet(t *testing.T) {
	tests := []struct {
		name, src, label string
		snippets         bool
		text             string
	}{
		{"list, snippets", "fn sum(list" + cursorMark + ") {}\n", "list: List", true, "list: List<${1}>"},
		{"list, plain", "fn sum(list" + cursorMark + ") {}\n", "list: List", false, "list: List"},
		{"map, snippets", "fn sum(map" + cursorMark + ") {}\n", "map: Map", true, "map: Map<${1}, ${2}>"},
		{"own generic, snippets", "fn open(box" + cursorMark + ") {}\n", "box: Box", true, "box: Box<${1}>"},
		{"not generic, snippets", "fn slay(game" + cursorMark + ") {}\n", "game: Game", true, "game: Game"},
		{"after the colon, snippets", "fn sum(map: " + cursorMark + ") {}\n", "Map", true, "Map<${1}, ${2}>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items := completeWith(t, tt.snippets, paramTypeDecls+tt.src)
			it := mustItem(t, items, tt.label)
			if te := textEditOf(t, it); te.NewText != tt.text {
				t.Fatalf("insert = %q, want %q", te.NewText, tt.text)
			}
			isSnippet := it.InsertTextFormat != nil && *it.InsertTextFormat == protocol.InsertTextFormatSnippet
			if want := tt.snippets && tt.text != "game: Game"; isSnippet != want {
				t.Fatalf("snippet format = %v, want %v", isSnippet, want)
			}
		})
	}
}

// After `name:` the types are offered as at any annotation, with the type the
// name names first. The unclosed forms are classified as type positions too.
func TestCompletion_ParamTypeAfterColon(t *testing.T) {
	tests := []struct {
		name, src, text string
	}{
		{"colon and space", "fn slay(game: " + cursorMark + ") {}\n", "Game"},
		{"colon and space, unclosed", "fn slay(game: " + cursorMark, "Game"},
		{"colon alone", "fn slay(game:" + cursorMark + ") {}\n", " Game"},
		{"colon alone, unclosed", "fn slay(game:" + cursorMark, " Game"},
		{"typed prefix", "fn slay(game: G" + cursorMark + ") {}\n", "Game"},
		{"interface declaration", "interface Play {\n    fn play(user_id: " + cursorMark + "): Int\n}\n", "UserId"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items := complete(t, paramTypeDecls+tt.src)
			if len(items) < 2 {
				t.Fatalf("got %v, want the type list", itemLabels(items))
			}
			if te := textEditOf(t, items[0]); te.NewText != tt.text {
				t.Fatalf("first item inserts %q, want %q; got %v", te.NewText, tt.text, itemLabels(items))
			}
			if tt.name != "typed prefix" {
				labelsInclude(t, items, "Int", "String", "Command")
			}
			labelsExclude(t, items, "if", "case")
			if n := len(itemsWithLabel(items, items[0].Label)); n != 1 {
				t.Fatalf("%q offered %d times", items[0].Label, n)
			}
		})
	}
}

func TestCompletion_ParamTypeNothing(t *testing.T) {
	// empty marks a parameter-name position, where nothing else is offered
	// either; the others keep their own completion.
	tests := []struct {
		name, src string
		empty     bool
	}{
		{"no type of that name", "fn slay(sword" + cursorMark + ") {}\n", true},
		{"no type of that name, unclosed", "fn slay(game: Game, sword" + cursorMark, true},
		{"type already written", "fn slay(game" + cursorMark + ": Game) {}\n", true},
		{"cursor inside the name", "fn slay(ga" + cursorMark + "me) {}\n", true},
		{"no name yet", "fn slay(" + cursorMark + ") {}\n", true},
		{"lambda", "fn f(): Int {\n    g = |game" + cursorMark + "| 1\n    1\n}\n", false},
		{"lambda, unclosed", "fn f(): Int {\n    g = |game" + cursorMark + "\n}\n", false},
		{"lambda argument", "fn f(xs: List<Game>): Int {\n    xs |> Iter.map(|game" + cursorMark + "| 1)\n    1\n}\n", false},
		{"destructuring parameter", "fn f((game" + cursorMark + ", b)) {}\n", false},
		{"function type", "fn f(g: fn(game" + cursorMark + "\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items := complete(t, paramTypeDecls+tt.src)
			for _, it := range items {
				if len(it.Label) > 2 && it.Label[:2] == ": " {
					t.Fatalf("offers %q", it.Label)
				}
			}
			if tt.empty && len(items) != 0 {
				t.Fatalf("a parameter-name position offers %v", itemLabels(items))
			}
		})
	}
}

// A pattern and a struct field keep their own completion: the name's type is
// not offered there.
func TestCompletion_ParamTypeNotInPatternsOrFields(t *testing.T) {
	items := complete(t, paramTypeDecls+"fn f(c: Command): Int {\n    case c {\n        command"+cursorMark+" -> 1\n    }\n}\n")
	labelsExclude(t, items, "command: Command")
	items = complete(t, paramTypeDecls+"struct Turn {\n    game: "+cursorMark+"\n}\n")
	if len(items) > 0 && items[0].Label == "Game" {
		t.Fatalf("a struct field's type ranks the name's type first: %v", itemLabels(items))
	}
	items = complete(t, paramTypeDecls+"fn f(): Int {\n    g = |game: "+cursorMark+"| 1\n    1\n}\n")
	if len(items) > 0 && items[0].Label == "Game" {
		t.Fatalf("a lambda parameter's type ranks the name's type first: %v", itemLabels(items))
	}
}

// Completion reads the latest text and the last finished analysis; it never
// analyzes. A signature typed since that analysis is still completed, and
// the document's analyzed version does not move.
func TestCompletion_ParamTypeRunsNoAnalysis(t *testing.T) {
	s := NewServer()
	uri := "file:///param_type_stale.nomi"
	s.docs.Open(uri, paramTypeDecls+"fn main() {\n}\n")
	before := s.docs.Snapshot(uri).AnalyzedVersion

	content, pos := splitCursor(t, paramTypeDecls+"fn main() {\n}\n\nfn slay(game, command"+cursorMark)
	s.docs.SetText(uri, content)
	res, err := s.textDocumentCompletion(nil, &protocol.CompletionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
			Position:     pos,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantParamItem(t, completionItemsOf(t, res), "command: Command", "command: Command")
	snap := s.docs.Snapshot(uri)
	if snap.AnalyzedVersion != before || snap.Current() {
		t.Fatalf("completion analyzed the document: analyzed version %d, was %d", snap.AnalyzedVersion, before)
	}
}

// A type position offers the modules that can qualify a type, those the
// file imports, and not the file's own module name or the prelude's:
// `main.Place` and `prelude.Int` name no type.
func TestCompletion_TypePositionOffersOnlyImportedModules(t *testing.T) {
	s := NewServer()
	items := completeIn(t, s, "file:///main.nomi", "import std/calendar\n\nenum Place {\n    Cave\n}\n\nfn f(x: "+cursorMark+") {}\n").Items
	mustItem(t, items, "calendar")
	mustItem(t, items, "Place")
	for _, it := range items {
		if it.Label == "main" || it.Label == "prelude" {
			t.Fatalf("offered module %q, which qualifies no type", it.Label)
		}
	}
}
