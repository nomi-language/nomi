package lsp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/fileuri"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// directoryImportProject writes a project whose main.nomi reaches
// views/admin/users.nomi and views/admin/reports/daily.nomi through a
// directory import, and opens main.nomi, written as main, in a server.
func directoryImportProject(t *testing.T, main string) (*Server, string, string) {
	t.Helper()
	dir := writeProject(t, map[string]string{
		"nomi.toml":                      "[module]\nname = \"app\"\nentry_points = [\"main\"]\n",
		"views/admin/users.nomi":         "pub fn count() -> Int\n    1\nend\n",
		"views/admin/reports/daily.nomi": "pub fn total() -> Int\n    2\nend\n",
		"db/posts.nomi":                  "pub fn count() -> Int\n    3\nend\n",
		"main.nomi":                      main,
	})
	s := NewServer()
	uri := fileuri.FromPath(filepath.Join(dir, "main.nomi"))
	s.docs.Open(uri, main)
	return s, uri, dir
}

const directoryImportMain = "import views::admin\n\nfn main()\n    _ = admin::users.count() + admin::reports::daily.total()\nend\n"

// Go-to-definition on a path's member reaches the declaration, and on the
// path's segments reaches the file.
func TestDirectoryImport_Definition(t *testing.T) {
	s, uri, dir := directoryImportProject(t, directoryImportMain)
	usersURI := protocol.DocumentUri(fileuri.FromPath(filepath.Join(dir, "views", "admin", "users.nomi")))
	dailyURI := protocol.DocumentUri(fileuri.FromPath(filepath.Join(dir, "views", "admin", "reports", "daily.nomi")))

	loc := definitionAt(t, s, uri, protocol.Position{Line: 3, Character: 21}) // `count`
	if loc.URI != usersURI || loc.Range.Start.Line != 0 {
		t.Errorf("definition of count = %v, want views/admin/users.nomi line 1", loc)
	}
	loc = definitionAt(t, s, uri, protocol.Position{Line: 3, Character: 15}) // `users` in `admin::users`
	if loc.URI != usersURI {
		t.Errorf("definition of the users segment = %v, want views/admin/users.nomi", loc)
	}
	loc = definitionAt(t, s, uri, protocol.Position{Line: 3, Character: 50}) // `daily`
	if loc.URI != dailyURI {
		t.Errorf("definition of the daily segment = %v, want views/admin/reports/daily.nomi", loc)
	}
}

// Renaming a function reached through a directory import renames its
// declaration and the call that names it through the path.
func TestDirectoryImport_Rename(t *testing.T) {
	s, uri, dir := directoryImportProject(t, directoryImportMain)
	edit := collectWorkspaceRename(context.Background(), s.docs, uri, analysis.Pos{Line: 4, Col: 22}, "total_users")
	if edit == nil {
		t.Fatal("expected a workspace edit, got nil")
	}
	usersURI := fileuri.FromPath(filepath.Join(dir, "views", "admin", "users.nomi"))
	if got := edit.Changes[usersURI]; len(got) != 1 || got[0].NewText != "total_users" || got[0].Range.Start.Line != 0 {
		t.Errorf("edits in views/admin/users.nomi = %v, want the declaration renamed", got)
	}
	if got := edit.Changes[uri]; len(got) != 1 || got[0].Range.Start.Line != 3 || got[0].Range.Start.Character != 21 {
		t.Errorf("edits in main.nomi = %v, want the call at 4:22", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "views", "admin", "users.nomi")); err != nil {
		t.Fatal(err)
	}
}

// Completion after a directory import's name and `::` offers the files and
// directories under the directory, and one level down after a subdirectory.
func TestDirectoryImport_CompletesPathSegments(t *testing.T) {
	main := "import views::admin\n\nfn main()\n    _ = admin::users.count()\n    _ = admin::\n    _ = admin::reports::\nend\n"
	s, uri, _ := directoryImportProject(t, main)
	got := completionLabels(t, s, uri, 4, 15)
	if !got["users"] || !got["reports"] || got["daily"] {
		t.Errorf("after `admin::`: %v, want users and reports", got)
	}
	got = completionLabels(t, s, uri, 5, 24)
	if !got["daily"] || got["users"] {
		t.Errorf("after `admin::reports::`: %v, want daily", got)
	}
}

// A file import's full path at a use is an error whose quick fix, and
// source.fixAll, write the file's short name.
func TestDirectoryImport_FixAllWritesTheShortName(t *testing.T) {
	main := "import db::posts\n\nfn main()\n    _ = db::posts.count() + posts.count()\nend\n"
	s, uri, _ := directoryImportProject(t, main)
	only := []protocol.CodeActionKind{codeActionKindSourceFixAll}
	result, err := s.textDocumentCodeAction(nil, &protocol.CodeActionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
		Context:      protocol.CodeActionContext{Only: only},
	})
	if err != nil {
		t.Fatal(err)
	}
	actions, _ := result.([]protocol.CodeAction)
	if len(actions) != 1 {
		t.Fatalf("want one fixAll action, got %+v", result)
	}
	want := "import db::posts\n\nfn main()\n    _ = posts.count() + posts.count()\nend\n"
	if got := applyWorkspaceEdit(t, main, actions[0], uri); got != want {
		t.Errorf("after fixAll:\n%s\nwant\n%s", got, want)
	}
}

// Hover on a path's segment names the file it reaches, and on a directory
// import's name in the import, the directory.
func TestDirectoryImport_Hover(t *testing.T) {
	s, uri, _ := directoryImportProject(t, directoryImportMain)
	hover := func(at protocol.Position) string {
		t.Helper()
		res, err := s.textDocumentHover(nil, &protocol.HoverParams{
			TextDocumentPositionParams: protocol.TextDocumentPositionParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
				Position:     at,
			},
		})
		if err != nil || res == nil {
			t.Fatalf("hover at %v: %v %v", at, res, err)
		}
		return res.Contents.(protocol.MarkupContent).Value
	}
	if got := hover(protocol.Position{Line: 3, Character: 15}); !strings.Contains(got, "file views::admin::users") {
		t.Errorf("hover on `users` in `admin::users`:\n%s\nwant the file views::admin::users", got)
	}
	if got := hover(protocol.Position{Line: 0, Character: 15}); !strings.Contains(got, "directory views::admin") {
		t.Errorf("hover on `admin` in the import:\n%s\nwant the directory views::admin", got)
	}
}
