package lsp

import (
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/internal/fileuri"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

const pointDecls = `struct Point
    x: Int
    y: Int
end

impl Point
    pub fn origin() -> Point
        Point{x: 0, y: 0}
    end
end

impl Display for Point
    fn to_string(p: Point) -> String
        "${p.x},${p.y}"
    end
end

enum Color
    Red
    Green
end
`

func TestCompletion_OwnerQualified(t *testing.T) {
	tests := []struct {
		name      string
		src       string
		want, not []string
	}{
		{
			"stdlib owner",
			"fn f(s: String) -> String\n    String." + cursorMark + "\nend\n",
			[]string{"trim", "uppercase", "length", "contains", "to_string"},
			[]string{"x", "if"},
		},
		{
			"same-file owner: inherent and interface functions",
			pointDecls + "fn f() -> Point\n    Point." + cursorMark + "\nend\n",
			[]string{"origin", "to_string"},
			[]string{"x", "y"},
		},
		{
			"enum owner offers its variants",
			pointDecls + "fn f() -> Color\n    Color." + cursorMark + "\nend\n",
			[]string{"Red", "Green"},
			nil,
		},
		{
			"interface owner",
			"fn f(n: Int) -> String\n    Display." + cursorMark + "\nend\n",
			[]string{"to_string"},
			[]string{"trim"},
		},
		{
			"interface with owner functions",
			"fn f(xs: List<Int>) -> Int\n    Iter." + cursorMark + "\nend\n",
			[]string{"map", "filter", "count", "each_while"},
			nil,
		},
		{
			"bounded type parameter",
			"fn show<T>(x: T) -> String where T: Display\n    T." + cursorMark + "\nend\n",
			[]string{"to_string"},
			nil,
		},
		{
			"typed prefix",
			"fn f(s: String) -> String\n    String.tr" + cursorMark + "\nend\n",
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
	items := complete(t, "fn f(s: String) -> String\n    String.tri"+cursorMark+"\nend\n")
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
			pointDecls + "fn f(p: Point) -> Int\n    n = p.x\n    p." + cursorMark + "\nend\n",
			[]string{"x", "y"},
			[]string{"origin", "to_string", "inspect"},
		},
		{
			"struct value in a statement that does not parse",
			pointDecls + "fn f(p: Point) -> Int\n    p." + cursorMark + "\nend\n",
			[]string{"x", "y"},
			[]string{"origin"},
		},
		{
			"anonymous struct",
			"fn f() -> Int\n    cfg = {name: \"a\", port: 1}\n    cfg." + cursorMark + "\nend\n",
			[]string{"name", "port"},
			nil,
		},
		{
			"field chain",
			pointDecls + "struct Line\n    from: Point\n    to: Point\nend\n\nfn f(l: Line) -> Int\n    l.from." + cursorMark + "\nend\n",
			[]string{"x", "y"},
			[]string{"from", "to"},
		},
		{
			"String value has no members",
			"fn f(s: String) -> Int\n    s." + cursorMark + "\nend\n",
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
		"server.nomi":   "pub struct Config\n    port: Int\nend\n\npub fn start(c: Config) -> Int\n    c.port\nend\n\nfn hidden() -> Int\n    1\nend\n",
		"lib/text.nomi": "pub fn shout(s: String) -> String\n    s\nend\n",
		"main.nomi":     "fn main() end\n",
	})
	s := NewServer()
	uri := fileuri.FromPath(filepath.Join(dir, "main.nomi"))

	items := completeIn(t, s, uri, "import server\n\nfn main()\n    server."+cursorMark+"\nend\n").Items
	labelsInclude(t, items, "start", "Config")
	labelsExclude(t, items, "hidden")

	items = completeIn(t, s, uri, "import "+cursorMark+"\n\nfn main() end\n").Items
	labelsInclude(t, items, "std", "server", "lib")
	labelsExclude(t, items, "main")

	items = completeIn(t, s, uri, "import std::"+cursorMark+"\n\nfn main() end\n").Items
	labelsInclude(t, items, "io", "json", "strings")
	labelsExclude(t, items, "prelude", "server")

	items = completeIn(t, s, uri, "import lib::"+cursorMark+"\n\nfn main() end\n").Items
	labelsInclude(t, items, "text")

	// After a path below a directory import, the file's public names.
	items = completeIn(t, s, uri, "import lib\n\nfn main()\n    lib::text."+cursorMark+"\nend\n").Items
	labelsInclude(t, items, "shout")

	items = completeIn(t, s, uri, "import server.{start, "+cursorMark+"end\n\nfn main()\nend\n").Items
	labelsInclude(t, items, "Config", "self")
	labelsExclude(t, items, "start", "hidden")

	items = completeIn(t, s, uri, "import std::io.{"+cursorMark+"end\n\nfn main()\nend\n").Items
	labelsInclude(t, items, "print", "self")
}

const appDecls = `struct App
    port: Int
    context: Context
end

impl App
    pub fn describe() -> String
        "app"
    end
end

fn boot(_startup: Startup) -> Result<App, String>
    Ok(App{port: 3, context: Context.root()})
end
`

func TestCompletion_ApplicationFields(t *testing.T) {
	items := complete(t, appDecls+"fn main()\n    n = App."+cursorMark+"\nend\n")
	labelsInclude(t, items, "port", "context", "describe")

	items = complete(t, appDecls+"fn main()\n    with App."+cursorMark+"\nend\n")
	labelsInclude(t, items, "port", "context")
	labelsExclude(t, items, "describe")
}

func TestCompletion_DotVariantOffersVariantNames(t *testing.T) {
	items := complete(t, pointDecls+"fn f() -> Color\n    ."+cursorMark+"\nend\n")
	labelsInclude(t, items, "Red", "Green")
	labelsExclude(t, items, "Color", "f", "origin")
}
