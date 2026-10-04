package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// highlightsAt drives the real documentHighlight entry point over src
// written as a project's main.nomi, and renders each highlight as
// "line:col-end kind".
func highlightsAt(t *testing.T, src string, line, char uint32) []string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "main.nomi")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	uri := pathToURI(path)
	s := NewServer()
	s.docs.Open(uri, src)
	hs, err := s.textDocumentDocumentHighlight(nil, &protocol.DocumentHighlightParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
			Position:     protocol.Position{Line: line, Character: char},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, h := range hs {
		kind := "?"
		if h.Kind != nil {
			kind = map[protocol.DocumentHighlightKind]string{protocol.DocumentHighlightKindRead: "read", protocol.DocumentHighlightKindWrite: "write", protocol.DocumentHighlightKindText: "text"}[*h.Kind]
		}
		out = append(out, itoa(h.Range.Start.Line)+":"+itoa(h.Range.Start.Character)+"-"+itoa(h.Range.End.Character)+" "+kind)
	}
	return out
}

const highlightSrc = `struct Point {
    x: Int
    y: Int
}

fn shift(p: Point, by: Int): Point {
    x = p.x + by
    y = p.y + by
    Point{x, y: y}
}

fn main() {
    total = shift(Point{x: 1, y: 2}, 3)
    total = shift(total, 4)
    w = ("é", total.x)
    w
}
`

func TestDocumentHighlight(t *testing.T) {
	cases := []struct {
		name       string
		line, char uint32
		want       []string
	}{
		// A parameter: its definition writes, each use reads.
		{"parameter", 5, 9, []string{"5:9-10 write", "6:8-9 read", "7:8-9 read"}},
		{"parameter from a use", 6, 15, []string{"5:19-21 write", "6:14-16 read", "7:14-16 read"}},
		// A local read by a punned field label.
		{"punned local", 6, 4, []string{"6:4-5 write", "8:10-11 read"}},
		// A function: its declaration and its calls.
		{"function", 12, 13, []string{"5:3-8 write", "12:12-17 read", "13:12-17 read"}},
		// A shadowing binding is its own symbol: the second `total`
		// reads the first and defines another.
		{"first binding", 12, 4, []string{"12:4-9 write", "13:18-23 read"}},
		{"shadowing binding", 13, 4, []string{"13:4-9 write", "14:14-19 read"}},
		// A field: its declaration and its accesses in this file.
		{"field", 1, 4, []string{"1:4-5 write", "6:10-11 read", "8:10-11 read", "12:24-25 read", "14:20-21 read"}},
		// Columns are UTF-16 after a non-ASCII character.
		{"after non-ASCII", 14, 15, []string{"13:4-9 write", "14:14-19 read"}},
		// A keyword has no symbol.
		{"keyword", 5, 0, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := highlightsAt(t, highlightSrc, c.line, c.char)
			if strings.Join(got, ", ") != strings.Join(c.want, ", ") {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}
