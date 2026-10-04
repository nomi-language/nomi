package lsp

import (
	"path/filepath"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

const pointDecls = `struct Point {
    x: Int
    y: Int
}

impl Point {
    pub fn origin(): Point {
        Point{x: 0, y: 0}
    }
}

impl Display for Point {
    fn to_string(p: Point): String {
        "${p.x},${p.y}"
    }
}

enum Color {
    Red
    Green
}
`

func TestCompletion_OwnerQualified(t *testing.T) {
	tests := []struct {
		name      string
		src       string
		want, not []string
	}{
		{
			"stdlib owner",
			"fn f(s: String): String {\n    String." + cursorMark + "\n}\n",
			[]string{"trim", "to_upper", "length", "contains?", "to_string"},
			[]string{"x", "if"},
		},
		{
			"same-file owner: inherent and interface functions",
			pointDecls + "fn f(): Point {\n    Point." + cursorMark + "\n}\n",
			[]string{"origin", "to_string"},
			[]string{"x", "y"},
		},
		{
			"enum owner offers its variants",
			pointDecls + "fn f(): Color {\n    Color." + cursorMark + "\n}\n",
			[]string{"Red", "Green"},
			nil,
		},
		{
			"interface owner",
			"fn f(n: Int): String {\n    Display." + cursorMark + "\n}\n",
			[]string{"to_string"},
			[]string{"trim"},
		},
		{
			"interface with owner functions",
			"fn f(xs: List<Int>): Int {\n    Iter." + cursorMark + "\n}\n",
			[]string{"map", "filter", "count", "each_while"},
			nil,
		},
		{
			"bounded type parameter",
			"fn show<T>(x: T): String where T: Display {\n    T." + cursorMark + "\n}\n",
			[]string{"to_string"},
			nil,
		},
		{
			"typed prefix",
			"fn f(s: String): String {\n    String.tr" + cursorMark + "\n}\n",
			[]string{"trim"},
			[]string{"length"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items := complete(t, tt.src)
			labelsInclude(t, items, tt.want...)
			labelsExclude(t, items, tt.not...)
		})
	}
}

func TestCompletion_OwnerFunctionsAreMethods(t *testing.T) {
	items := complete(t, "fn f(s: String): String {\n    String.tri"+cursorMark+"\n}\n")
	it := findItem(items, "trim")
	if it == nil || it.Kind == nil || *it.Kind != protocol.CompletionItemKindMethod {
		t.Fatalf("String.trim item = %+v, want a Method", it)
	}
}

func TestCompletion_ValueMembersAreFieldsOnly(t *testing.T) {
	tests := []struct {
		name      string
		src       string
		want, not []string
	}{
		{
			"struct value",
			pointDecls + "fn f(p: Point): Int {\n    n = p.x\n    p." + cursorMark + "\n}\n",
			[]string{"x", "y"},
			[]string{"origin", "to_string", "inspect"},
		},
		{
			"struct value in a statement that does not parse",
			pointDecls + "fn f(p: Point): Int {\n    p." + cursorMark + "\n}\n",
			[]string{"x", "y"},
			[]string{"origin"},
		},
		{
			"anonymous struct",
			"fn f(): Int {\n    cfg = {name: \"a\", port: 1}\n    cfg." + cursorMark + "\n}\n",
			[]string{"name", "port"},
			nil,
		},
		{
			"field chain",
			pointDecls + "struct Line {\n    from: Point\n    to: Point\n}\n\nfn f(l: Line): Int {\n    l.from." + cursorMark + "\n}\n",
			[]string{"x", "y"},
			[]string{"from", "to"},
		},
		{
			"String value has no members",
			"fn f(s: String): Int {\n    s." + cursorMark + "\n}\n",
			nil,
			[]string{"trim", "length"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items := complete(t, tt.src)
			labelsInclude(t, items, tt.want...)
			labelsExclude(t, items, tt.not...)
		})
	}
}

func TestCompletion_UserFileMembersAndImports(t *testing.T) {
	dir := writeProject(t, map[string]string{
		"server.nomi":   "pub struct Config {\n    port: Int\n}\n\npub fn start(c: Config): Int {\n    c.port\n}\n\nfn hidden(): Int {\n    1\n}\n",
		"lib/text.nomi": "pub fn shout(s: String): String {\n    s\n}\n",
		"main.nomi":     "fn main() {\n}\n",
	})
	s := NewServer()
	uri := "file://" + filepath.Join(dir, "main.nomi")

	items := completeIn(t, s, uri, "import server\n\nfn main() {\n    server."+cursorMark+"\n}\n").Items
	labelsInclude(t, items, "start", "Config")
	labelsExclude(t, items, "hidden")

	items = completeIn(t, s, uri, "import "+cursorMark+"\n\nfn main() {\n}\n").Items
	labelsInclude(t, items, "std", "server", "lib")
	labelsExclude(t, items, "main")

	items = completeIn(t, s, uri, "import std/"+cursorMark+"\n\nfn main() {\n}\n").Items
	labelsInclude(t, items, "io", "json", "strings")
	labelsExclude(t, items, "prelude", "server")

	items = completeIn(t, s, uri, "import lib/"+cursorMark+"\n\nfn main() {\n}\n").Items
	labelsInclude(t, items, "text")

	items = completeIn(t, s, uri, "import server.{start, "+cursorMark+"}\n\nfn main() {\n}\n").Items
	labelsInclude(t, items, "Config", "self")
	labelsExclude(t, items, "start", "hidden")

	items = completeIn(t, s, uri, "import std/io.{"+cursorMark+"}\n\nfn main() {\n}\n").Items
	labelsInclude(t, items, "print", "self")
}

const appDecls = `struct App {
    port: Int
    context: Context
}

impl App {
    pub fn describe(): String {
        "app"
    }
}

fn boot(_startup: Startup): App {
    App{port: 3, context: Context.root()}
}
`

func TestCompletion_ApplicationFields(t *testing.T) {
	items := complete(t, appDecls+"fn main() {\n    n = App."+cursorMark+"\n}\n")
	labelsInclude(t, items, "port", "context", "describe")

	items = complete(t, appDecls+"fn main() {\n    with App."+cursorMark+"\n}\n")
	labelsInclude(t, items, "port", "context")
	labelsExclude(t, items, "describe")
}

func TestCompletion_DotVariantOffersVariantNames(t *testing.T) {
	items := complete(t, pointDecls+"fn f(): Color {\n    ."+cursorMark+"\n}\n")
	labelsInclude(t, items, "Red", "Green")
	labelsExclude(t, items, "Color", "f", "origin")
}
