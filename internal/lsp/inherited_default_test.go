package lsp

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/fileuri"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// inheritedDefaultProject writes a project whose shape.nomi declares an
// interface with a default, square.nomi implements it for Square writing
// only the required function, and main.nomi, written as main, imports
// Square alone; it opens main.nomi in a server.
func inheritedDefaultProject(t *testing.T, main string) (*Server, string, string) {
	t.Helper()
	dir := writeProject(t, map[string]string{
		"nomi.toml": "[module]\nname = \"app\"\nentry_points = [\"main\"]\n",
		"shape.nomi": "pub interface Shape\n    fn area(s: self) -> Int\n\n" +
			"    /// Says the shape's area.\n    fn describe(s: self) -> String\n        \"area ${area(s)}\"\n    end\nend\n",
		"square.nomi": "import shape.Shape\n\npub struct Square\n    side: Int\nend\n\n" +
			"impl Shape for Square\n    fn area(s: Square) -> Int\n        s.side * s.side\n    end\nend\n",
		"main.nomi": main,
	})
	s := NewServer()
	uri := fileuri.FromPath(filepath.Join(dir, "main.nomi"))
	s.docs.Open(uri, main)
	return s, uri, dir
}

// A default Square inherits from Shape is Square's member in an editor as
// a function its impl writes is, in a file that does not import Shape:
// completion after `Square.` offers it, and hover and go-to-definition on
// `Square.describe` reach the interface's default.
func TestInheritedDefaultThroughTheTypeInTheEditor(t *testing.T) {
	main := "import square.Square\n\nfn main()\n    dbg Square.describe(Square{side: 3})\n    _ = Square.\nend\n"
	s, uri, dir := inheritedDefaultProject(t, main)

	got := completionLabels(t, s, uri, 4, 15)
	if !got["describe"] || !got["area"] {
		t.Errorf("after `Square.`: %v, want describe and area", got)
	}

	at := protocol.Position{Line: 3, Character: 16} // `describe`
	loc := definitionAt(t, s, uri, at)
	shapeURI := protocol.DocumentUri(fileuri.FromPath(filepath.Join(dir, "shape.nomi")))
	if loc.URI != shapeURI || loc.Range.Start.Line != 4 {
		t.Errorf("definition of describe = %v, want shape.nomi line 5", loc)
	}

	res, err := s.textDocumentHover(nil, &protocol.HoverParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
			Position:     at,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res == nil {
		t.Fatal("no hover on describe")
	}
	text := res.Contents.(protocol.MarkupContent).Value
	if !strings.Contains(text, "fn describe(Square) -> String") {
		t.Errorf("hover on describe = %q, want the default's signature at Square", text)
	}
}

// Hover on an interface's function shows its `///` doc beside the
// signature, as a hover on a function an impl writes does: interface
// qualified (`Shape.describe`, `Comparable.min`) and as a default a type
// inherits (`Square.describe`, `Int.min`), a program's interface and std's.
func TestHoverShowsAnInterfaceFunctionsDoc(t *testing.T) {
	main := "import shape.Shape\nimport square.Square\n\nfn main()\n" +
		"    dbg Square.describe(Square{side: 3})\n" +
		"    dbg Shape.describe(Square{side: 3})\n" +
		"    dbg Int.min(3, 4)\n" +
		"    dbg Comparable.min(3, 4)\nend\n"
	s, uri, _ := inheritedDefaultProject(t, main)
	for _, c := range []struct {
		name      string
		line, col uint32
		want      string
	}{
		{"Square.describe", 4, 16, "Says the shape's area."},
		{"Shape.describe", 5, 15, "Says the shape's area."},
		{"Int.min", 6, 13, "The lesser of `a` and `b` by `compare`"},
		{"Comparable.min", 7, 20, "The lesser of `a` and `b` by `compare`"},
	} {
		res, err := s.textDocumentHover(nil, &protocol.HoverParams{
			TextDocumentPositionParams: protocol.TextDocumentPositionParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
				Position:     protocol.Position{Line: c.line, Character: c.col},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if res == nil {
			t.Errorf("%s: no hover", c.name)
			continue
		}
		text := res.Contents.(protocol.MarkupContent).Value
		if !strings.Contains(text, "fn ") || !strings.Contains(text, c.want) {
			t.Errorf("hover on %s = %q, want its signature and %q", c.name, text, c.want)
		}
	}
}
