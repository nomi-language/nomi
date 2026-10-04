package lsp

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// renameProject writes a project whose module is named app, with
// models/users.nomi and models/roles.nomi, and files importing them:
// main.nomi, opened in the server, and report.nomi and audit.nomi, left
// closed on disk.
func renameProject(t *testing.T) (*Server, string) {
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
	write("nomi.toml", "[module]\nname = \"app\"\n")
	write("models/users.nomi", "pub fn make(): Int { 1 }\n\npub struct User {\n    name: String\n}\n")
	write("models/roles.nomi", "pub fn admin(): Int { 2 }\n")
	main := `import models/users
import models/roles

fn main() {
    x = users.make() + roles.admin()
    y = users.make()
}
`
	write("main.nomi", main)
	write("report.nomi", `import {
    app/models/users.User
    models/users as u
    models/roles.{admin}
}

pub fn report(p: User): Int { u.make() + admin() }
`)
	write("audit.nomi", "import models/users\nimport models/users.User.{self}\n\npub fn audit(): Int { users.make() }\npub fn name(u: User): String { u.name }\n")
	s := NewServer()
	s.docs.SetWorkspaceRoot(dir)
	s.docs.Open(pathToURI(filepath.Join(dir, "main.nomi")), main)
	return s, dir
}

// editsByFile renders a workspace edit as "file: line:col-line:col text"
// lines, sorted.
func editsByFile(dir string, we *protocol.WorkspaceEdit) []string {
	var out []string
	if we == nil {
		return nil
	}
	for uri, edits := range we.Changes {
		rel, _ := filepath.Rel(dir, uriToPath(string(uri)))
		for _, e := range edits {
			out = append(out, rel+": "+fmtRange(e.Range)+" "+e.NewText)
		}
	}
	sort.Strings(out)
	return out
}

func willRename(t *testing.T, s *Server, dir string, pairs ...string) []string {
	t.Helper()
	var files []protocol.FileRename
	for i := 0; i < len(pairs); i += 2 {
		files = append(files, protocol.FileRename{
			OldURI: pathToURI(filepath.Join(dir, pairs[i])),
			NewURI: pathToURI(filepath.Join(dir, pairs[i+1])),
		})
	}
	we, err := s.workspaceWillRenameFiles(nil, &protocol.RenameFilesParams{Files: files})
	if err != nil {
		t.Fatal(err)
	}
	return editsByFile(dir, we)
}

func wantEdits(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("edits:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// Renaming a file rewrites every import of it, in open and closed files,
// keeping a self-named path's module prefix, a selective import's names
// and an alias; a path-only import's qualifier follows the new last
// segment.
func TestWillRenameFiles_File(t *testing.T) {
	s, dir := renameProject(t)
	got := willRename(t, s, dir, "models/users.nomi", "models/people.nomi")
	wantEdits(t, got, []string{
		"audit.nomi: 0:7-0:19 models/people",
		"audit.nomi: 1:7-1:19 models/people",
		"audit.nomi: 3:22-3:27 people",
		"main.nomi: 0:7-0:19 models/people",
		"main.nomi: 4:8-4:13 people",
		"main.nomi: 5:8-5:13 people",
		"report.nomi: 1:4-1:20 app/models/people",
		"report.nomi: 2:4-2:16 models/people",
	})
}

// A move to another directory rewrites the whole path.
func TestWillRenameFiles_Move(t *testing.T) {
	s, dir := renameProject(t)
	got := willRename(t, s, dir, "models/roles.nomi", "auth/roles.nomi")
	wantEdits(t, got, []string{
		"main.nomi: 1:7-1:19 auth/roles",
		"report.nomi: 3:4-3:16 auth/roles",
	})
}

// Renaming a folder rewrites the imports of every file under it.
func TestWillRenameFiles_Folder(t *testing.T) {
	s, dir := renameProject(t)
	got := willRename(t, s, dir, "models", "domain")
	wantEdits(t, got, []string{
		"audit.nomi: 0:7-0:19 domain/users",
		"audit.nomi: 1:7-1:19 domain/users",
		"main.nomi: 0:7-0:19 domain/users",
		"main.nomi: 1:7-1:19 domain/roles",
		"report.nomi: 1:4-1:20 app/domain/users",
		"report.nomi: 2:4-2:16 domain/users",
		"report.nomi: 3:4-3:16 domain/roles",
	})
}

// A new name that is no import path segment (a space, a keyword) gets no
// edit, and nor does a rename of a file nothing imports.
func TestWillRenameFiles_NoEdit(t *testing.T) {
	s, dir := renameProject(t)
	for _, to := range []string{"models/old users.nomi", "models/fn.nomi"} {
		if got := willRename(t, s, dir, "models/users.nomi", to); len(got) != 0 {
			t.Errorf("rename to %q: %v, want no edits", to, got)
		}
	}
	if got := willRename(t, s, dir, "main.nomi", "start.nomi"); len(got) != 0 {
		t.Errorf("rename of main.nomi: %v, want no edits", got)
	}
}

// The server advertises willRename for .nomi files and folders.
func TestWillRenameFiles_Advertised(t *testing.T) {
	s := NewServer()
	res, err := s.initialize(nil, &protocol.InitializeParams{})
	if err != nil {
		t.Fatal(err)
	}
	caps := res.(initializeResult).Capabilities
	ops := caps.Workspace.FileOperations
	if ops == nil || ops.WillRename == nil || len(ops.WillRename.Filters) != 2 || ops.WillRename.Filters[0].Pattern.Glob != "**/*.nomi" {
		t.Fatalf("willRename not advertised for **/*.nomi: %+v", ops)
	}
}
