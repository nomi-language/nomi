package lsp

import (
	"path/filepath"
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

const shapeDecls = `struct Config {
    name: String
    port: Int = 80
    host: String
}

enum Shape {
    Dot
    Circle(Float)
    Rect{w: Float, h: Float}
}
`

func TestCompletion_StructLiteralOffersUnwrittenFields(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string // in order
	}{
		{"named literal", "fn f(): Config {\n    Config{name: \"a\", " + cursorMark + "}\n}\n", []string{"host", "port"}},
		{"unclosed literal", "fn f(): Config {\n    Config{name: \"a\", " + cursorMark + "\n}\n", []string{"host", "port"}},
		{"empty literal: required fields first", "fn f(): Config {\n    Config{" + cursorMark + "}\n}\n", []string{"name", "host", "port"}},
		{"struct update", "fn f(base: Config): Config {\n    {..base, host: \"h\", " + cursorMark + "}\n}\n", []string{"name", "port"}},
		{"struct variant shorthand", "fn f(): Shape {\n    .Rect{" + cursorMark + "}\n}\n", []string{"w", "h"}},
		{"anonymous literal against an expected type", "fn take(c: Config): Int {\n    c.port\n}\n\nfn f(): Int {\n    take({name: \"a\", " + cursorMark + "})\n}\n", []string{"host", "port"}},
		{"target-typed literal at an annotated binding", "fn f(): Int {\n    c: Config = {name: \"a\", " + cursorMark + "}\n    c.port\n}\n", []string{"host", "port"}},
		{"target-typed literal at a struct field", "struct Server {\n    config: Config\n}\n\nfn f(): Server {\n    Server{config: {name: \"a\", " + cursorMark + "}}\n}\n", []string{"host", "port"}},
		{"target-typed literal at a return", "fn f(): Config {\n    {host: \"h\", " + cursorMark + "}\n}\n", []string{"name", "port"}},
		{"target-typed literal in a typed list", "fn f(): List<Config> {\n    [{name: \"a\", " + cursorMark + "}]\n}\n", []string{"host", "port"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items := complete(t, shapeDecls+tt.src)
			if got := strings.Join(itemLabels(items), ","); got != strings.Join(tt.want, ",") {
				t.Fatalf("fields = %s, want %s", got, strings.Join(tt.want, ","))
			}
		})
	}
	it := mustItem(t, complete(t, shapeDecls+"fn f(): Config {\n    Config{name: \"a\", "+cursorMark+"}\n}\n"), "port")
	if it.Detail == nil || *it.Detail != "port: Int" {
		t.Fatalf("detail = %v, want port: Int", it.Detail)
	}
	if te := textEditOf(t, it); te.NewText != "port: " {
		t.Fatalf("insert = %q, want `port: `", te.NewText)
	}
}

func TestCompletion_CaseFillsMissingArms(t *testing.T) {
	src := shapeDecls + "fn area(s: Shape): Float {\n    case s {\n        .Dot -> 0.0\n        " + cursorMark + "\n    }\n}\n"

	items := completeWith(t, true, src)
	fill := mustItem(t, items, "all missing arms")
	if items[0].Label != "all missing arms" {
		t.Fatalf("the fill item is not first: %v", itemLabels(items))
	}
	want := ".Circle(${1:value}) -> ${2}\n.Rect{${3:w}, ${4:h}} -> ${5}"
	if te := textEditOf(t, fill); te.NewText != want {
		t.Fatalf("fill snippet =\n%s\nwant\n%s", te.NewText, want)
	}
	if fill.InsertTextMode == nil || *fill.InsertTextMode != protocol.InsertTextModeAdjustIndentation {
		t.Fatal("the fill snippet's later arms are relative to the cursor's line; the client must adjust them")
	}
	labelsInclude(t, items, ".Circle", ".Rect")
	labelsExclude(t, items, ".Dot")

	plain := mustItem(t, completeWith(t, false, src), "all missing arms")
	if te := textEditOf(t, plain); te.NewText != ".Circle(value) -> \n        .Rect{w, h} -> " {
		t.Fatalf("plain fill = %q", te.NewText)
	}
}

func TestCompletion_CaseWithCatchAllHasNothingMissing(t *testing.T) {
	src := shapeDecls + "fn area(s: Shape): Float {\n    case s {\n        .Dot -> 0.0\n        _ -> 1.0\n        " + cursorMark + "\n    }\n}\n"
	if items := complete(t, src); len(items) != 0 {
		t.Fatalf("a case with a catch-all arm offers %v", itemLabels(items))
	}
}

func TestCompletion_TestGroupBootOffersEntryBoots(t *testing.T) {
	dir := writeProject(t, map[string]string{
		"server.nomi": "struct App {\n    port: Int\n}\n\nfn boot(_startup: Startup): App {\n    App{port: 1}\n}\n\nfn main() {\n}\n",
		"helper.nomi": "pub fn startup(): Startup {\n    Startup{env: Map.empty(), args: []}\n}\n",
	})
	s := NewServer()
	s.snippetSupport = true
	uri := "file://" + filepath.Join(dir, "server_test.nomi")
	items := completeIn(t, s, uri, "import server\n\ntests \"g\" {\n    boot "+cursorMark+"\n}\n").Items
	if len(items) == 0 || items[0].Label != "server.boot" {
		t.Fatalf("want server.boot first, got %v", itemLabels(items))
	}
	if te := textEditOf(t, items[0]); te.NewText != "server.boot(${1:_startup})$0" {
		t.Fatalf("insert = %q", te.NewText)
	}
}

func TestCompletion_TestGroupLineSnippets(t *testing.T) {
	src := "tests \"g\" {\n    boot server.boot(s)\n    " + cursorMark + "\n}\n"
	items := completeWith(t, true, src)
	if got := strings.Join(itemLabels(items), ","); got != "clock,setup,test" {
		t.Fatalf("group lines = %s", got)
	}
	if te := textEditOf(t, mustItem(t, items, "test")); te.NewText != "test \"${1:name}\" {\n    assert $0\n}" {
		t.Fatalf("test snippet = %q", te.NewText)
	}
	if te := textEditOf(t, mustItem(t, items, "setup")); te.NewText != "setup {\n    $0\n}" {
		t.Fatalf("setup snippet = %q", te.NewText)
	}
}

// TestCompletion_TestGroupLinePlainNamesTheTest: without snippets the test
// line inserts a placeholder name, as the snippet does, since an empty test
// name is a checker error.
func TestCompletion_TestGroupLinePlainNamesTheTest(t *testing.T) {
	src := "tests \"g\" {\n    boot server.boot(s)\n    " + cursorMark + "\n}\n"
	items := completeWith(t, false, src)
	if te := textEditOf(t, mustItem(t, items, "test")); te.NewText != "test \"name\" {\n        \n    }" {
		t.Fatalf("test line = %q", te.NewText)
	}
}
