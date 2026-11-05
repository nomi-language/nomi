package lsp

import (
	"path/filepath"
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// `embed` in the editor: the name's hover says what it holds, a pattern's
// hover what it matched, go to definition on a pattern opens what it
// matched, and a pattern that matches nothing is a diagnostic at it.

const embedMain = "import std::embed.Files\n\n/// The site.\nembed site `static page.html`\n\nfn main() {\n    _ = Files.paths(site)\n}\n"

func embedProject() map[string]string {
	return map[string]string{
		"nomi.toml":           "[module]\nname = \"site\"\nentry_points = [\"main\"]\n",
		"main.nomi":           embedMain,
		"static/index.html":   "<h1>hi</h1>\n",
		"static/css/site.css": "body {}\n",
		"page.html":           "<p>page</p>\n",
	}
}

func TestEmbed_HoverOnTheNameSaysWhatItHolds(t *testing.T) {
	s, uri := openProject(t, embedProject(), "main.nomi")
	for _, at := range []int{strings.Index(embedMain, "site `") + 1, strings.Index(embedMain, "paths(site)") + 7} {
		got := messageHoverText(t, s, uri, offsetPosition(embedMain, at))
		for _, want := range []string{
			"embed site `static page.html`: Files",
			"The site.",
			"3 files, 32 B, embedded when the program is compiled",
			"- `page.html` (12 B)",
			"- `static/css/site.css` (8 B)",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("hover at %d lacks %q:\n%s", at, want, got)
			}
		}
	}
}

func TestEmbed_HoverOnAPatternSaysWhatItMatched(t *testing.T) {
	s, uri := openProject(t, embedProject(), "main.nomi")
	got := messageHoverText(t, s, uri, offsetPosition(embedMain, strings.Index(embedMain, "static page")+2))
	want := "`static` embeds 2 files, 20 B, embedded when the program is compiled\n- `static/css/site.css` (8 B)\n- `static/index.html` (12 B)"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestEmbed_DefinitionOnAPatternOpensWhatItMatched(t *testing.T) {
	s, uri := openProject(t, embedProject(), "main.nomi")
	root := filepath.Dir(uriToPath(uri))
	for pattern, want := range map[string]string{"static ": "static", "page.html": "page.html"} {
		got, err := s.textDocumentDefinition(nil, &protocol.DefinitionParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
			Position:     offsetPosition(embedMain, strings.Index(embedMain, pattern)+1),
		}})
		if err != nil {
			t.Fatal(err)
		}
		locs, ok := got.([]protocol.Location)
		if !ok || len(locs) != 1 || uriToPath(string(locs[0].URI)) != filepath.Join(root, want) {
			t.Errorf("%s: got %#v, want %s", pattern, got, want)
		}
	}
}

func TestEmbed_APatternThatMatchesNothingIsADiagnosticAtIt(t *testing.T) {
	files := embedProject()
	files["main.nomi"] = strings.Replace(embedMain, "page.html", "nope.html", 1)
	root := writeProject(t, files)
	s, published := recordingServer()
	s.docs.SetWorkspaceRoot(root)
	uri := pathToURI(filepath.Join(root, "main.nomi"))
	s.docs.Open(uri, files["main.nomi"])
	s.publish(s.notify, uri)
	var got []protocol.Diagnostic
	for u, ds := range published() {
		if u == uri {
			got = ds
		}
	}
	if len(got) != 1 || !strings.HasPrefix(got[0].Message, "the `embed` pattern `nope.html` matches no file") ||
		got[0].Range.Start != (protocol.Position{Line: 3, Character: 19}) || got[0].Range.End != (protocol.Position{Line: 3, Character: 28}) {
		t.Fatalf("diagnostics %+v", got)
	}
}

// A nested file's pattern names files from the module root: go to
// definition on web/pages.nomi's `static` opens the root's static.
func TestEmbed_DefinitionOnANestedFilesPatternOpensFromTheRoot(t *testing.T) {
	pages := "pub embed site `static`\n"
	files := embedProject()
	files["main.nomi"] = "import {\n    std::embed.Files\n    web::pages\n}\n\nfn main() {\n    _ = Files.paths(pages.site)\n}\n"
	files["web/pages.nomi"] = pages
	s, uri := openProject(t, files, "web/pages.nomi")
	root := filepath.Dir(filepath.Dir(uriToPath(uri)))
	got, err := s.textDocumentDefinition(nil, &protocol.DefinitionParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)},
		Position:     offsetPosition(pages, strings.Index(pages, "static")+1),
	}})
	if err != nil {
		t.Fatal(err)
	}
	locs, ok := got.([]protocol.Location)
	if !ok || len(locs) != 1 || uriToPath(string(locs[0].URI)) != filepath.Join(root, "static") {
		t.Fatalf("got %#v, want %s", got, filepath.Join(root, "static"))
	}
}

// A literal path that names no embedded file is a diagnostic at the
// literal.
func TestEmbed_ALiteralPathNamingNoEmbeddedFileIsADiagnosticAtIt(t *testing.T) {
	files := embedProject()
	main := "import std::embed.Files\n\nembed site `static page.html`\n\nfn main() {\n    _ = Files.read_text(site, \"static/indx.html\")\n}\n"
	files["main.nomi"] = main
	root := writeProject(t, files)
	s, published := recordingServer()
	s.docs.SetWorkspaceRoot(root)
	uri := pathToURI(filepath.Join(root, "main.nomi"))
	s.docs.Open(uri, main)
	s.publish(s.notify, uri)
	var got []protocol.Diagnostic
	for u, ds := range published() {
		if u == uri {
			got = ds
		}
	}
	if len(got) != 1 || !strings.HasPrefix(got[0].Message, "`static/indx.html` is not embedded in `site`") ||
		got[0].Range.Start != (protocol.Position{Line: 5, Character: 30}) || got[0].Range.End != (protocol.Position{Line: 5, Character: 48}) {
		t.Fatalf("diagnostics %+v", got)
	}
}
