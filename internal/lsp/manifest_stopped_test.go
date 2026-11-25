package lsp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/nomi-language/nomi/internal/fileuri"
)

// A nomi.toml in error stops the build of every Nomi file of its project.
// Each open Nomi document shows that one error at its top, naming the
// nomi.toml and the line, and nothing else; the nomi.toml shows it at its
// line, closed or open, once.
func TestManifestDocument_AManifestErrorIsShownInEveryDocumentItStops(t *testing.T) {
	root := t.TempDir()
	const manifest = "[package]\nname = \"app\"\nentry_point = [\"main\"]\n"
	files := map[string]string{
		"nomi.toml": manifest,
		"main.nomi": "import\n    std::io\n    app::util\nend\n\nfn main()\n    io.print(util.word())\nend\n",
		"util.nomi": "pub fn word() -> String\n    undefined_here()\nend\n",
	}
	for rel, content := range files {
		mustWriteFile(t, filepath.Join(root, rel), content)
	}
	tomlPath := filepath.Join(root, "nomi.toml")
	tomlURI := fileuri.FromPath(tomlPath)
	const message = "[package] has no key `entry_point`; did you mean `entry_points`?"
	s := NewServer()
	c := startRPC(t, s, root)
	for _, rel := range []string{"main.nomi", "util.nomi"} {
		uri := fileuri.FromPath(filepath.Join(root, rel))
		c.open(uri, files[rel])
		p := c.waitMessage(uri, message)
		if want := tomlPath + ":3: " + message; len(p.msgs) != 1 || !strings.HasPrefix(p.msgs[0], want) {
			t.Errorf("%s shows %q; want one error, %s", rel, p.msgs, want)
		}
	}
	// wantOne waits for the nomi.toml to hold the error once, from the
	// documents' analysis and, open, from its own text too.
	wantOne := func(what string, open bool) {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for {
			s.inputDiags.mu.Lock()
			_, own := s.inputDiags.bySource[tomlURI][manifestSource(tomlURI)]
			s.inputDiags.mu.Unlock()
			ds := manifestDiags(s, tomlURI)
			if own == open && len(ds) == 1 && ds[0].Range.Start.Line == 2 && ds[0].Message == message {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: nomi.toml holds %s\nwant one diagnostic at line 3: %s", what, renderDiagnostics(ds), message)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	wantOne("closed", false)
	c.notify("textDocument/didOpen", protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{
		URI: protocol.DocumentUri(tomlURI), LanguageID: "toml", Version: 1, Text: manifest,
	}})
	wantOne("open", true)
}

// A nomi-workspace.toml in error stops each member's build: the open
// member shows the error at its top, naming the file and line, and the
// open nomi-workspace.toml shows it once, at its line or, for a problem no
// line writes, at its start.
func TestManifestDocument_AWorkspaceErrorIsShownInTheMemberAndOnceInTheFile(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		line       int
		message    string
	}{
		{"an unknown key", "members = [\"app\", \"libs/*\"]\nbogus = 1\n", 2, "nomi-workspace.toml has no key `bogus`; its keys are `members`, `exclude`"},
		{"no members", "exclude = [\"vendor\"]\n", 1, "a workspace needs members, the directories of its packages from here, such as members = [\"billing\", \"lib/*\"]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := workspaceProject(t, tc.text)
			wsPath := filepath.Join(root, "nomi-workspace.toml")
			wsURI := fileuri.FromPath(wsPath)
			mainPath := filepath.Join(root, "app", "main.nomi")
			uri := fileuri.FromPath(mainPath)
			s := NewServer()
			c := startRPC(t, s, root)
			main, err := os.ReadFile(mainPath)
			if err != nil {
				t.Fatal(err)
			}
			c.open(uri, string(main))
			p := c.waitMessage(uri, tc.message)
			if want := fmt.Sprintf("%s:%d: %s", wsPath, tc.line, tc.message); len(p.msgs) != 1 || !strings.HasPrefix(p.msgs[0], want) {
				t.Errorf("main.nomi shows %q; want one error, %s", p.msgs, want)
			}
			c.notify("textDocument/didOpen", protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{
				URI: protocol.DocumentUri(wsURI), LanguageID: "toml", Version: 1, Text: tc.text,
			}})
			deadline := time.Now().Add(30 * time.Second)
			for {
				s.inputDiags.mu.Lock()
				_, own := s.inputDiags.bySource[wsURI][manifestSource(wsURI)]
				_, build := s.inputDiags.bySource[wsURI][uri]
				s.inputDiags.mu.Unlock()
				ds := manifestDiags(s, wsURI)
				if own && build && len(ds) == 1 && int(ds[0].Range.Start.Line) == tc.line-1 && ds[0].Message == tc.message {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("nomi-workspace.toml holds %s\nwant one diagnostic at line %d: %s", renderDiagnostics(ds), tc.line, tc.message)
				}
				time.Sleep(5 * time.Millisecond)
			}
		})
	}
}
