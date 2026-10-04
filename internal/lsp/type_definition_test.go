package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

const typeDefMain = `import models/users

struct Point {
    x: Int
}

fn main() {
    u = users.make()
    m = users.maybe()
    p = Point{x: 1}
    xs = [p]
    pair = (p, u)
    f = make_point
    io.inspect((u.name, p.x, xs, m, pair, f(), make_point()))
}

fn make_point(): Point { Point{x: 2} }
`

func TestTypeDefinition(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, src string) {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("nomi.toml", "[module]\nname = \"app\"\nentry_points = [\"main\"]\n")
	write("models/users.nomi", "pub struct User {\n    name: String\n}\n\npub fn make(): User { User{name: \"a\"} }\n\npub fn maybe(): Maybe<User> { None }\n")
	src := strings.Replace(typeDefMain, "import models/users\n", "import models/users\nimport std/io\n", 1)
	write("main.nomi", src)
	s := NewServer()
	s.docs.SetWorkspaceRoot(dir)
	uri := pathToURI(filepath.Join(dir, "main.nomi"))
	if snap := s.docs.Open(uri, src); snap.Analysis == nil || len(snap.Analysis.TypeErrors) > 0 {
		t.Fatalf("main.nomi does not check: %v", snap.Analysis.TypeErrors)
	}

	cases := []struct {
		what   string
		pos    protocol.Position
		file   string // the target file's base name, "" for no answer
		name   string // the text at the target range
		atLine int    // 0-based, -1 to skip
	}{
		{"binding of a type from another file", occ(src, "u = ", 0, 0), "users.nomi", "User", 0},
		{"generic instantiation lands on the generic type", occ(src, "m = ", 0, 0), "maybe.nomi", "Maybe", -1},
		{"binding of this file's struct", occ(src, "p = ", 0, 0), "main.nomi", "Point", 3},
		{"use of a binding", occ(src, "p.x,", 0, 0), "main.nomi", "Point", 3},
		{"list", occ(src, "xs = ", 0, 0), "lists.nomi", "List", -1},
		{"field access", occ(src, "u.name", 0, 2), "strings.nomi", "String", -1},
		{"field declaration", occ(src, "x: Int", 0, 0), "int.nomi", "Int", -1},
		{"call of another file's function", occ(src, "make()", 0, 0), "users.nomi", "User", 0},
		{"call of this file's function", occ(src, "make_point()", 0, 0), "main.nomi", "Point", 3},
		{"type name", occ(src, "Point{x: 1}", 0, 0), "main.nomi", "Point", 3},
		{"tuple", occ(src, "pair = ", 0, 0), "", "", -1},
		{"function value", occ(src, "f = ", 0, 0), "", "", -1},
		{"file qualifier", occ(src, "users.make", 0, 0), "", "", -1},
		{"keyword", occ(src, "fn main", 0, 0), "", "", -1},
	}
	for _, c := range cases {
		res, err := s.textDocumentTypeDefinition(nil, &protocol.TypeDefinitionParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)}, Position: c.pos}})
		if err != nil {
			t.Fatalf("%s: %v", c.what, err)
		}
		loc, _ := res.(*protocol.Location)
		if c.file == "" {
			if res != nil {
				t.Errorf("%s: got %#v, want nothing", c.what, res)
			}
			continue
		}
		if loc == nil {
			t.Errorf("%s: no location", c.what)
			continue
		}
		path := uriToPath(string(loc.URI))
		if filepath.Base(path) != c.file {
			t.Errorf("%s: landed in %s, want %s", c.what, path, c.file)
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v", c.what, err)
		}
		if got := textAt(string(data), loc.Range); got != c.name {
			t.Errorf("%s: range %s in %s holds %q, want %q", c.what, fmtRange(loc.Range), c.file, got, c.name)
		}
		if c.atLine >= 0 && int(loc.Range.Start.Line) != c.atLine {
			t.Errorf("%s: line %d, want %d", c.what, loc.Range.Start.Line, c.atLine)
		}
	}
}
