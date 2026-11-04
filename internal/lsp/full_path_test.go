package lsp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/fileuri"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// fullPathProject writes a project whose main.nomi reaches db/posts.nomi
// by its full path, and opens main.nomi in a server.
func fullPathProject(t *testing.T) (*Server, string, string) {
	t.Helper()
	dir := writeProject(t, map[string]string{
		"nomi.toml":     "[module]\nname = \"app\"\nentry_points = [\"main\"]\n",
		"db/posts.nomi": "pub fn count() -> Int {\n    1\n}\n",
		"main.nomi":     fullPathMain,
	})
	s := NewServer()
	uri := fileuri.FromPath(filepath.Join(dir, "main.nomi"))
	s.docs.Open(uri, fullPathMain)
	return s, uri, dir
}

const fullPathMain = "import db::posts\n\nfn posts() -> Int {\n    db::posts.count()\n}\n"

// Go-to-definition on a full path's member reaches the declaration, and on
// the path's file segment reaches the file.
func TestFullPath_Definition(t *testing.T) {
	s, uri, dir := fullPathProject(t)
	postsURI := protocol.DocumentUri(fileuri.FromPath(filepath.Join(dir, "db", "posts.nomi")))

	loc := definitionAt(t, s, uri, protocol.Position{Line: 3, Character: 15}) // `count` in `db::posts.count()`
	if loc.URI != postsURI || loc.Range.Start.Line != 0 {
		t.Errorf("definition of count = %v, want db/posts.nomi line 1", loc)
	}
	loc = definitionAt(t, s, uri, protocol.Position{Line: 3, Character: 9}) // `posts` in `db::posts`
	if loc.URI != postsURI {
		t.Errorf("definition of the posts segment = %v, want db/posts.nomi", loc)
	}
}

// Renaming a function reached by full path renames its declaration and the
// call that names it through the path.
func TestFullPath_Rename(t *testing.T) {
	s, uri, dir := fullPathProject(t)
	edit := collectWorkspaceRename(context.Background(), s.docs, uri, analysis.Pos{Line: 4, Col: 15}, "total")
	if edit == nil {
		t.Fatal("expected a workspace edit, got nil")
	}
	postsURI := fileuri.FromPath(filepath.Join(dir, "db", "posts.nomi"))
	if got := edit.Changes[postsURI]; len(got) != 1 || got[0].NewText != "total" || got[0].Range.Start.Line != 0 {
		t.Errorf("edits in db/posts.nomi = %v, want the declaration renamed", got)
	}
	if got := edit.Changes[uri]; len(got) != 1 || got[0].Range.Start.Line != 3 || got[0].Range.Start.Character != 14 {
		t.Errorf("edits in main.nomi = %v, want the call at 4:15", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "db", "posts.nomi")); err != nil {
		t.Fatal(err)
	}
}
