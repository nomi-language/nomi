package lsp

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/nomi-language/nomi/internal/fileuri"
	"github.com/nomi-language/nomi/internal/parser"
)

// workspaceProject is a workspace whose nomi-workspace.toml is text: two
// packages under libs/ (a and b), an app that imports a by its
// directory, and a package under vendor/. It answers the root.
func workspaceProject(t *testing.T, text string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range map[string]string{
		"nomi-workspace.toml": text,
		"app/nomi.toml":       "[package]\nname = \"app\"\nentry_points = [\"main\"]\n\n[dependencies]\na = { dir = \"../libs/a\" }\n",
		"app/main.nomi":       "import\n    std::io\n    a::text\nend\n\nfn main()\n    io.print(text.word())\nend\n",
		"libs/a/nomi.toml":    "[package]\nname = \"a\"\n",
		"libs/a/text.nomi":    "pub fn word() -> String\n    \"a\"\nend\n",
		"libs/b/nomi.toml":    "[package]\nname = \"b\"\n",
		"vendor/v/nomi.toml":  "[package]\nname = \"v\"\n",
	} {
		mustWriteFile(t, filepath.Join(root, filepath.FromSlash(rel)), content)
	}
	return root
}

// nomi-workspace.toml open in the editor is a document: what the workspace
// lookup finds wrong in its unsaved text is published at its line, and a
// text with nothing wrong clears it.
func TestManifestDocument_WorkspaceDiagnostics(t *testing.T) {
	const clean = "members = [\"app\", \"libs/*\"]\nexclude = [\"vendor\"]\n"
	root := workspaceProject(t, clean)
	uri := fileuri.FromPath(filepath.Join(root, "nomi-workspace.toml"))
	s := NewServer()
	c := startRPC(t, s, root)
	c.notify("textDocument/didOpen", protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{
		URI: protocol.DocumentUri(uri), LanguageID: "toml", Version: 1, Text: clean,
	}})
	cases := []struct {
		name, text string
		line       int
		want       string
	}{
		{"an unknown key", clean + "memberz = []\n", 3, "nomi-workspace.toml has no key `memberz`; did you mean `members`?"},
		{"an entry with no package", "members = [\n    \"app\",\n    \"docs\",\n]\n", 3, "members: docs holds no nomi.toml; a member is a package's directory"},
		{"a member excluded", "members = [\"app\", \"libs/*\"]\nexclude = [\"libs\"]\n", 2, "exclude: libs takes in libs/a, which members names"},
		{"no members", "exclude = [\"vendor\"]\n", 0, "a workspace needs members"},
	}
	wait := func(what string, ok func([]protocol.Diagnostic) bool) {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for !ok(manifestDiags(s, uri)) {
			if time.Now().After(deadline) {
				t.Fatalf("%s: nomi-workspace.toml holds %s", what, renderDiagnostics(manifestDiags(s, uri)))
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	for i, tc := range cases {
		c.change(uri, i+2, tc.text)
		wait(tc.name, func(ds []protocol.Diagnostic) bool {
			return slices.ContainsFunc(ds, func(d protocol.Diagnostic) bool {
				return (tc.line == 0 || int(d.Range.Start.Line)+1 == tc.line) && strings.Contains(d.Message, tc.want)
			})
		})
	}
	c.change(uri, len(cases)+2, clean)
	wait("the clean text", func(ds []protocol.Diagnostic) bool { return len(ds) == 0 })
}

// Completion in nomi-workspace.toml offers the keys and the tables its
// schema declares that the text does not write yet, where a key goes
// outside any table, and nothing inside a table or a value.
func TestManifestDocument_WorkspaceCompletion(t *testing.T) {
	root := workspaceProject(t, "members = [\"app\"]\n")
	uri := fileuri.FromPath(filepath.Join(root, "nomi-workspace.toml"))
	s := NewServer()
	labels := func(text string, pos protocol.Position) []string {
		var out []string
		for _, it := range s.manifestCompletion(uri, text, pos) {
			out = append(out, it.Label+" => "+it.TextEdit.(protocol.TextEdit).NewText)
		}
		return out
	}
	text := "members = [\"app\"]\nex\n"
	if got, want := labels(text, protocol.Position{Line: 1, Character: 2}), []string{"exclude => exclude = []"}; !slices.Equal(got, want) {
		t.Errorf("completion of a key offers %q; want %q", got, want)
	}
	if got := labels("\n", protocol.Position{Line: 0}); !slices.Equal(got, []string{"members => members = []", "exclude => exclude = []"}) {
		t.Errorf("completion in an empty file offers %q", got)
	}
	for _, pos := range []protocol.Position{{Line: 0, Character: 12}} {
		if got := labels(text, pos); len(got) != 0 {
			t.Errorf("completion in a value offers %q; want none", got)
		}
	}
	inTable := "members = [\"app\"]\n\n[bogus]\n\n"
	if got := labels(inTable, protocol.Position{Line: 3}); len(got) != 0 {
		t.Errorf("completion in a table offers %q; want none", got)
	}
}

// A hover on an entry of members names each package it takes in, by its
// directory and name, and on an entry of exclude each directory it names;
// in a package's nomi.toml, a hover on [package] or its name says whether
// the package is a member of the workspace above it, excluded from it, or
// under it unlisted.
func TestManifestDocument_WorkspaceHover(t *testing.T) {
	text := "members = [\"app\", \"libs/*\"]\nexclude = [\"vendor\"]\n"
	root := workspaceProject(t, text)
	uri := fileuri.FromPath(filepath.Join(root, "nomi-workspace.toml"))
	s := NewServer()
	if got, want := manifestHoverText(t, s, uri, manifestAt(t, text, 1, `"libs/*"`)), "Members:\n\n```text\nlibs/a: package a\nlibs/b: package b\n```"; got != want {
		t.Errorf("hover on libs/* says\n%s\nwant\n%s", got, want)
	}
	if got := manifestHoverText(t, s, uri, manifestAt(t, text, 2, `"vendor"`)); !strings.HasPrefix(got, "Excluded") || !strings.Contains(got, "\nvendor\n") {
		t.Errorf("hover on vendor says\n%s", got)
	}
	if got := manifestHoverText(t, s, uri, manifestAt(t, text, 1, "members")); got != "" {
		t.Errorf("hover on the key says %q; want nothing", got)
	}

	mustWriteFile(t, filepath.Join(root, "stray", "nomi.toml"), "[package]\nname = \"stray\"\n")
	for _, tc := range []struct{ dir, want string }{
		{"libs/a", "A member of the workspace at `" + root + "`"},
		{"vendor/v", "Excluded from the workspace at `" + root + "`"},
		{"stray", "Under the workspace at `" + root + "`, whose members do not list it: add `stray` to its members"},
	} {
		path := filepath.Join(root, filepath.FromSlash(tc.dir), "nomi.toml")
		manifest, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		u := fileuri.FromPath(path)
		for _, pos := range []protocol.Position{manifestAt(t, string(manifest), 1, "[package]"), manifestAt(t, string(manifest), 2, "name")} {
			if got := manifestHoverText(t, s, u, pos); !strings.HasPrefix(got, tc.want) {
				t.Errorf("hover at %+v in %s/nomi.toml says\n%s\nwant it to start\n%s", pos, tc.dir, got, tc.want)
			}
		}
	}
}

// An open member is checked again when nomi-workspace.toml changes on
// disk: a workspace that stops listing it makes its program an unlisted
// package, whose quick fix adds it back to members, and listing it again
// clears the error.
func TestManifestDocument_WorkspaceChangeRechecksMembers(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	const listed = "members = [\"app\", \"libs/*\"]\n"
	root := workspaceProject(t, listed)
	wsPath := filepath.Join(root, "nomi-workspace.toml")
	wsURI := fileuri.FromPath(wsPath)
	mainPath := filepath.Join(root, "app", "main.nomi")
	uri := fileuri.FromPath(mainPath)
	main, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer()
	c := startRPC(t, s, root)
	c.open(uri, string(main))
	c.waitDiagnostics(uri)

	const unlisted = "members = [\"libs/*\"]\n"
	mustWriteFile(t, wsPath, unlisted)
	c.notify("workspace/didChangeWatchedFiles", protocol.DidChangeWatchedFilesParams{Changes: []protocol.FileEvent{{URI: protocol.DocumentUri(wsURI), Type: protocol.FileChangeTypeChanged}}})
	p := c.waitMessage(uri, "does not list it")
	var msg string
	for _, m := range p.msgs {
		if strings.Contains(m, "does not list it") {
			msg = m
		}
	}
	appToml := filepath.Join(root, "app", "nomi.toml")
	if want := appToml + ":1: this package is under the workspace"; !strings.HasPrefix(msg, want) {
		t.Errorf("main.nomi shows %q; want the error of the package's nomi.toml, %s...", msg, want)
	}
	diag := protocol.Diagnostic{Range: protocol.Range{End: protocol.Position{Character: 1}}, Message: msg}
	actions := c.codeActions(uri, diag)
	if len(actions) != 1 || actions[0].Edit == nil || len(actions[0].Edit.Changes[protocol.DocumentUri(wsURI)]) != 1 {
		t.Fatalf("the unlisted package has actions %+v; want one edit of nomi-workspace.toml", actions)
	}
	if want := `Add "app" to the workspace's members`; actions[0].Title != want {
		t.Errorf("the fix is titled %q; want %q", actions[0].Title, want)
	}
	// The package's nomi.toml shows the error at its first line, with the
	// same fix.
	appTomlURI := fileuri.FromPath(appToml)
	var inToml []protocol.Diagnostic
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if inToml = manifestDiags(s, appTomlURI); len(inToml) == 1 && inToml[0].Range.Start.Line == 0 && strings.HasPrefix(inToml[0].Message, "this package is under the workspace") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("app/nomi.toml holds %s; want the unlisted package's error at line 1", renderDiagnostics(inToml))
		}
	}
	if fromToml := c.codeActions(appTomlURI, inToml[0]); len(fromToml) != 1 || fromToml[0].Title != actions[0].Title ||
		!slices.Equal(fromToml[0].Edit.Changes[protocol.DocumentUri(wsURI)], actions[0].Edit.Changes[protocol.DocumentUri(wsURI)]) {
		t.Fatalf("app/nomi.toml offers %+v; want the fix main.nomi offers, %+v", fromToml, actions)
	}
	e := actions[0].Edit.Changes[protocol.DocumentUri(wsURI)][0]
	edited := applyFixEdit(t, unlisted, parser.Edit{
		Start: parser.Pos{Line: int(e.Range.Start.Line) + 1, Col: int(e.Range.Start.Character) + 1},
		End:   parser.Pos{Line: int(e.Range.End.Line) + 1, Col: int(e.Range.End.Character) + 1},
		Text:  e.NewText,
	})
	if want := "members = [\"libs/*\", \"app\"]\n"; edited != want {
		t.Fatalf("nomi-workspace.toml after the fix:\n%s\nwant:\n%s", edited, want)
	}

	mustWriteFile(t, wsPath, edited)
	c.notify("workspace/didChangeWatchedFiles", protocol.DidChangeWatchedFilesParams{Changes: []protocol.FileEvent{{URI: protocol.DocumentUri(wsURI), Type: protocol.FileChangeTypeChanged}}})
	deadline := time.After(30 * time.Second)
	for {
		select {
		case p := <-c.diags:
			if p.uri == uri && !slices.ContainsFunc(p.msgs, func(m string) bool { return strings.Contains(m, "does not list it") }) {
				return
			}
		case <-deadline:
			t.Fatal("listing the package again did not clear its error")
		}
	}
}

func mustWriteFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}
