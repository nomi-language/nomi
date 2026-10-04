package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

const prepareRenameMain = `import models/users
import models/users.User
import std/io

fn double(n: Int): Int { n * 2 }

interface Named {
    fn label(value: self): String
}

impl Named for User {
    fn label(value: User): String { value.name }
}

impl Display for User {
    fn to_string(u: User): String { u.name }
}

fn main() {
    total = double(21)
    p = User{name: String.trim(" a ")}
    n = Iter.map([1, 2], |x| x + total) |> Iter.count()
    io.print(Int.to_string(users.make() + total + n) + p.name)
}
`

func prepareRenameServer(t *testing.T) (*Server, string) {
	t.Helper()
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
	write("models/users.nomi", "pub fn make(): Int { 1 }\n\npub struct User {\n    name: String\n}\n")
	write("main.nomi", prepareRenameMain)
	s := NewServer()
	s.docs.SetWorkspaceRoot(dir)
	uri := pathToURI(filepath.Join(dir, "main.nomi"))
	snap := s.docs.Open(uri, prepareRenameMain)
	if snap.Analysis == nil || len(snap.Analysis.TypeErrors) > 0 {
		t.Fatalf("main.nomi does not check: %v", snap.Analysis.TypeErrors)
	}
	return s, uri
}

// occ finds the n-th (0-based) occurrence of needle in src and returns its
// position plus offset bytes (ASCII source, so bytes are UTF-16 units).
func occ(src, needle string, n, offset int) protocol.Position {
	i := -1
	for k := 0; k <= n; k++ {
		j := strings.Index(src[i+1:], needle)
		if j < 0 {
			panic("no " + needle)
		}
		i += 1 + j
	}
	i += offset
	line := strings.Count(src[:i], "\n")
	col := i - (strings.LastIndex(src[:i], "\n") + 1)
	return protocol.Position{Line: uint32(line), Character: uint32(col)}
}

func TestPrepareRename(t *testing.T) {
	s, uri := prepareRenameServer(t)
	src := prepareRenameMain
	accept := []struct {
		what   string
		pos    protocol.Position
		name   string
		atName protocol.Position
	}{
		{"function declaration", occ(src, "double", 0, 2), "double", occ(src, "double", 0, 0)},
		{"function call", occ(src, "double", 1, 0), "double", occ(src, "double", 1, 0)},
		{"cursor just past a name", occ(src, "double", 1, 6), "double", occ(src, "double", 1, 0)},
		{"parameter", occ(src, "n:", 0, 0), "n", occ(src, "n:", 0, 0)},
		{"local binding", occ(src, "total", 2, 1), "total", occ(src, "total", 2, 0)},
		{"lambda parameter", occ(src, "|x|", 0, 1), "x", occ(src, "|x|", 0, 1)},
		{"project function in another file", occ(src, "make", 0, 1), "make", occ(src, "make", 0, 0)},
		{"selectively imported type", occ(src, "User{", 0, 0), "User", occ(src, "User{", 0, 0)},
		{"project struct field", occ(src, "p.name", 0, 3), "name", occ(src, "p.name", 0, 2)},
	}
	for _, c := range accept {
		res, err := s.textDocumentPrepareRename(nil, &protocol.PrepareRenameParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)}, Position: c.pos}})
		if err != nil {
			t.Errorf("%s: refused: %v", c.what, err)
			continue
		}
		rp, ok := res.(protocol.RangeWithPlaceholder)
		if !ok {
			t.Errorf("%s: got %#v", c.what, res)
			continue
		}
		want := protocol.Range{Start: c.atName, End: protocol.Position{Line: c.atName.Line, Character: c.atName.Character + uint32(len(c.name))}}
		if rp.Placeholder != c.name || rp.Range != want {
			t.Errorf("%s: got %q at %s, want %q at %s", c.what, rp.Placeholder, fmtRange(rp.Range), c.name, fmtRange(want))
		}
	}

	refuse := []struct {
		what string
		pos  protocol.Position
		msg  string
	}{
		{"keyword", occ(src, "fn double", 0, 0), "nothing to rename"},
		{"integer literal", occ(src, "21", 0, 0), "nothing to rename"},
		{"string literal", occ(src, `" a "`, 0, 2), "nothing to rename"},
		{"primitive type", occ(src, "Int)", 0, 0), "standard library"},
		{"prelude type", occ(src, "Iter.map", 0, 1), "standard library"},
		{"stdlib function", occ(src, "Int.to_string", 0, 4), "standard library"},
		{"stdlib file qualifier", occ(src, "io.print", 0, 0), "import path"},
		{"stdlib owner", occ(src, "String.trim", 0, 0), "standard library"},
		{"project file qualifier", occ(src, "users.make", 0, 0), "import path"},
		{"interface method", occ(src, "label(value: self)", 0, 0), "interface method"},
		{"implementation of a project interface", occ(src, "label(value: User)", 0, 0), "implements Named.label"},
		{"implementation of a stdlib interface", occ(src, "to_string(u", 0, 0), "implements Display.to_string"},
		{"import path directory", occ(src, "models/users\n", 0, 0), "import path"},
	}
	for _, c := range refuse {
		params := protocol.TextDocumentPositionParams{TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)}, Position: c.pos}
		res, err := s.textDocumentPrepareRename(nil, &protocol.PrepareRenameParams{TextDocumentPositionParams: params})
		if err == nil || !strings.Contains(err.Error(), c.msg) {
			t.Errorf("%s: got %#v, %v; want an error containing %q", c.what, res, err, c.msg)
		}
		// Rename refuses the same position.
		if _, err := s.textDocumentRename(nil, &protocol.RenameParams{TextDocumentPositionParams: params, NewName: "renamed"}); err == nil {
			t.Errorf("%s: rename accepted what prepareRename refuses", c.what)
		}
	}
}

// Rename refuses a new name that is not one identifier.
func TestRename_RefusesInvalidName(t *testing.T) {
	s, uri := prepareRenameServer(t)
	params := protocol.TextDocumentPositionParams{TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)}, Position: occ(prepareRenameMain, "total", 0, 0)}
	for _, name := range []string{"fn", "two words", "1x", ""} {
		if _, err := s.textDocumentRename(nil, &protocol.RenameParams{TextDocumentPositionParams: params, NewName: name}); err == nil {
			t.Errorf("rename to %q accepted", name)
		}
	}
	if we, err := s.textDocumentRename(nil, &protocol.RenameParams{TextDocumentPositionParams: params, NewName: "sum"}); err != nil || we == nil {
		t.Errorf("rename to sum: %v, %v", we, err)
	}
}

func TestPrepareRename_Advertised(t *testing.T) {
	res, err := NewServer().initialize(nil, &protocol.InitializeParams{})
	if err != nil {
		t.Fatal(err)
	}
	opts, ok := res.(initializeResult).Capabilities.RenameProvider.(protocol.RenameOptions)
	if !ok || opts.PrepareProvider == nil || !*opts.PrepareProvider {
		t.Fatalf("renameProvider = %#v, want prepareProvider", res.(initializeResult).Capabilities.RenameProvider)
	}
}
