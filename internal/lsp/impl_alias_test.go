package lsp

import (
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// An impl whose header names a program's interface through an import
// alias answers the editor as an impl of that interface does: from the
// alias at a call, go-to-implementation lists the impl, go-to-definition
// on the call's function lands on the impl's function, and hovering it
// names the interface it implements.
func TestImplAlias_TheEditorReachesTheImplThroughTheAlias(t *testing.T) {
	main := "import\n    shapes.{Shape as Figure}\nend\n" + // lines 0-2
		"\n" +
		"struct Sq\n    side: Int\nend\n" + // 4-6
		"\n" +
		"impl Figure for Sq\n" + // 8
		"    fn area(s: Sq) -> Int\n        s.side\n    end\n" + // 9
		"end\n" +
		"\n" +
		"fn main()\n" +
		"    _ = Figure.area(Sq{side: 1})\n" +
		"end\n"
	s, uri := openProject(t, map[string]string{
		"nomi.toml":   "[package]\nname = \"app\"\nentry_points = [\"main\"]\n",
		"shapes.nomi": "pub interface Shape\n    fn area(value: self) -> Int\nend\n",
		"main.nomi":   main,
	}, "main.nomi")
	call := strings.Index(main, "Figure.area(")

	result, err := s.textDocumentImplementation(nil, &protocol.ImplementationParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
			Position:     offsetPosition(main, call),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if locs, _ := result.([]protocol.Location); len(locs) != 1 || locs[0].Range.Start.Line != 9 {
		t.Errorf("implementations of Figure: want the one impl at line 9, got %+v", result)
	}

	if loc := definitionAt(t, s, uri, offsetPosition(main, call+len("Figure."))); loc.Range.Start.Line != 9 || !strings.HasSuffix(string(loc.URI), "main.nomi") {
		t.Errorf("definition of Figure.area at Sq: want main.nomi line 9, got %+v", loc)
	}

	hover, err := s.textDocumentHover(nil, &protocol.HoverParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
			Position:     offsetPosition(main, strings.Index(main, "fn area")+len("fn ")),
		},
	})
	if err != nil || hover == nil {
		t.Fatalf("no hover on the impl's function: %v", err)
	}
	text := hover.Contents.(protocol.MarkupContent).Value
	if !strings.Contains(text, "Shape") {
		t.Errorf("the impl's function's hover does not name Shape:\n%s", text)
	}
}
