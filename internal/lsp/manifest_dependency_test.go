package lsp

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/nomi-language/nomi/internal/fileuri"
)

// A problem in the nomi.toml of a package the project names by `dir` is
// shown in the project's nomi.toml at the entry that names the package,
// with the message the build stops with, and in the package's own
// nomi.toml at its line when that file is open. A problem in the nomi.toml
// of a workspace's member is shown in nomi-workspace.toml at the members
// entry that names it.
func TestManifestDocument_ADependencyInErrorIsShownWhereItIsNamed(t *testing.T) {
	root := t.TempDir()
	const appToml = "[package]\nname = \"app\"\nentry_points = [\"main\"]\n\n[dependencies]\nkit = { dir = \"../kit\" }\n"
	const kitToml = "[package]\nname = \"kit\"\n\n[dependancies]\nother = { dir = \"../other\" }\n"
	const wsText = "members = [\n    \"billing\",\n    \"shop\",\n]\n"
	for rel, content := range map[string]string{
		"app/nomi.toml":          appToml,
		"app/main.nomi":          "import\n    std::io\n    kit\nend\n\nfn main()\n    io.print(\"${kit.two()}\")\nend\n",
		"kit/nomi.toml":          kitToml,
		"kit/kit.nomi":           "pub fn two() -> Int\n    2\nend\n",
		"ws/nomi-workspace.toml": wsText,
		"ws/billing/nomi.toml":   "[package]\nname = \"billing\"\n",
		"ws/shop/nomi.toml":      kitToml,
	} {
		mustWriteFile(t, filepath.Join(root, filepath.FromSlash(rel)), content)
	}
	kitPath := filepath.Join(root, "kit", "nomi.toml")
	problem := kitPath + ":4: nomi.toml has no [dependancies] table; did you mean [dependencies]?"
	s := NewServer()
	c := startRPC(t, s, root)
	for _, f := range []struct {
		rel, text string
		line      int
		want      string
	}{
		{"app/nomi.toml", appToml, 6, problem + "; [dependencies] kit of " + filepath.Join(root, "app", "nomi.toml") + " names this package"},
		{"kit/nomi.toml", kitToml, 4, "nomi.toml has no [dependancies] table; did you mean [dependencies]?"},
		{"ws/nomi-workspace.toml", wsText, 3, filepath.Join(root, "ws", "shop", "nomi.toml") + strings.TrimPrefix(problem, kitPath) +
			"; members entry \"shop\" of " + filepath.Join(root, "ws", "nomi-workspace.toml") + " names this package"},
	} {
		uri := fileuri.FromPath(filepath.Join(root, filepath.FromSlash(f.rel)))
		c.notify("textDocument/didOpen", protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{
			URI: protocol.DocumentUri(uri), LanguageID: "toml", Version: 1, Text: f.text,
		}})
		deadline := time.Now().Add(30 * time.Second)
		for {
			ds := manifestDiags(s, uri)
			if len(ds) == 1 && int(ds[0].Range.Start.Line)+1 == f.line && ds[0].Message == f.want {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s holds %s\nwant one diagnostic at line %d: %s", f.rel, renderDiagnostics(ds), f.line, f.want)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
}
