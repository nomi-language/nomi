package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

func TestDocumentLinks(t *testing.T) {
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
	write("nomi.toml", "[module]\nname = \"app\"\n")
	write("models/users.nomi", "pub struct User {\n    name: String\n}\n")
	write("game.nomi", "pub struct Game {\n    turn: Int\n}\n")
	src := `import {
    std/iter
    std/maybe.Maybe.{None}
    models/users
    app/models/users.User
    models/users.User.{self}
    game.Game
    missing/thing
}
import std/io as console
`
	write("main.nomi", src)
	uri := pathToURI(filepath.Join(dir, "main.nomi"))
	s := NewServer()
	s.docs.SetWorkspaceRoot(dir)
	s.docs.Open(uri, src)
	links, err := s.textDocumentDocumentLink(nil, &protocol.DocumentLinkParams{TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)}})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, l := range links {
		if l.Target == nil {
			t.Fatalf("link at %s has no target", fmtRange(l.Range))
		}
		target := uriToPath(string(*l.Target))
		if _, err := os.Stat(target); err != nil {
			t.Errorf("link at %s targets a missing file: %v", fmtRange(l.Range), err)
		}
		if rel, err := filepath.Rel(dir, target); err == nil && !strings.HasPrefix(rel, "..") {
			target = rel
		} else {
			target = "std:" + filepath.Base(target)
		}
		got = append(got, fmtRange(l.Range)+" "+target)
	}
	want := []string{
		"1:4-1:12 std:iter.nomi",
		"2:4-2:13 std:maybe.nomi",
		"3:4-3:16 models/users.nomi",
		"4:4-4:20 models/users.nomi",
		"5:4-5:16 models/users.nomi",
		"6:4-6:8 game.nomi",
		"9:7-9:13 std:io.nomi",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("links:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
