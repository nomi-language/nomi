package lsp

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/fileuri"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// manifestProject is an app, example.com/app by its go.mod, whose main
// calls into a dependency it requires, example.com/pricing, which has no
// [capabilities] tables. It answers the app's directory.
func manifestProject(t *testing.T, manifest string) string {
	t.Helper()
	dir := writeProject(t, map[string]string{
		"app/go.mod":         "module example.com/app\n\ngo 1.27.0\n\nrequire example.com/pricing v0.0.0\n\nreplace example.com/pricing => ../pricing\n",
		"app/nomi.toml":      manifest,
		"app/main.nomi":      "import\n    std::io\n    pricing::rules\nend\n\nfn main()\n    io.print(rules.load(\"prices.txt\"))\nend\n",
		"pricing/go.mod":     "module example.com/pricing\n\ngo 1.27.0\n",
		"pricing/nomi.toml":  "[module]\nname = \"pricing\"\n",
		"pricing/rules.nomi": "import std::fs\n\npub fn load(path: String) -> String\n    fs.read_text(path) |> Result.with_default(\"\")\nend\n",
	})
	return filepath.Join(dir, "app")
}

// manifestDiags is what the server publishes in the nomi.toml at uri now.
func manifestDiags(s *Server, uri string) []protocol.Diagnostic {
	s.inputDiags.mu.Lock()
	defer s.inputDiags.mu.Unlock()
	return s.inputDiags.merged(uri)
}

// nomi.toml open in the editor is a document: the errors `nomi check`
// reports about it are published in it at their lines, from its unsaved
// text. Those the parser finds come from the text itself; those about
// the [capabilities] tables that need the program come from the lowering
// of the open Nomi document. Each edit replaces them.
func TestManifestDocument_Diagnostics(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	const module = "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n"
	const head = module + "[capabilities.self]\n"
	const clean = head + "std = [\"std::io\", \"std::fs\"]\n"
	app := manifestProject(t, clean)
	uri := fileuri.FromPath(filepath.Join(app, "main.nomi"))
	manifestURI := fileuri.FromPath(filepath.Join(app, "nomi.toml"))
	main := "import\n    std::io\n    pricing::rules\nend\n\nfn main()\n    io.print(rules.load(\"prices.txt\"))\nend\n"
	s := NewServer()
	s.lowering.delay = 50 * time.Millisecond
	c := startRPC(t, s, app)
	c.open(uri, main)
	c.notify("textDocument/didOpen", protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{
		URI: protocol.DocumentUri(manifestURI), LanguageID: "toml", Version: 1, Text: clean,
	}})
	cases := []struct {
		name, text string
		line       int
		want       string
	}{
		{"an unknown key", clean + "stdd = []\n", 7, "[capabilities.self] has no key `stdd`"},
		{"a key [capabilities] held directly", module + "[capabilities]\nstd = []\n", 6, "[capabilities] has no key `std`; it holds two tables"},
		{"an unknown table", clean + "\n[capabilitys]\n", 8, "nomi.toml has no [capabilitys] table; did you mean [capabilities]?"},
		{"a name of the wrong form", head + "std = [\n    \"std::io\",\n    \"fs\",\n]\n", 8, "[capabilities.self] std: \"fs\" is not a std module"},
		{"a name listed twice", head + "std = [\"std::io\", \"std::fs\", \"std::io\"]\n", 6, "[capabilities.self] std lists std::io twice"},
		{"a misspelled std name", head + "std = [\n    \"std::io\",\n    \"std::fz\",\n]\n", 8,
			"[capabilities.self] std names std::fz, which is not a std module\nhelp: did you mean \"std::fs\"?"},
		{"a module go.mod does not require", clean + "\n[capabilities.deps]\ngo = [\"example.com/nope\"]\n", 9,
			"[capabilities.deps] go names example.com/nope, which go.mod does not require"},
		{"the project's own path", clean + "\n[capabilities.deps.modules]\n\"example.com/app\" = []\n", 9,
			"[capabilities.deps.modules] names example.com/app, which is this project"},
		{"a dependency with no entry", clean + "\n[capabilities.deps]\nentries_required = true\n", 9,
			"[capabilities.deps] entries_required: example.com/pricing has no entry"},
	}
	for i, tc := range cases {
		c.change(manifestURI, i+2, tc.text)
		deadline := time.Now().Add(60 * time.Second)
		for {
			got := manifestDiags(s, manifestURI)
			if slices.ContainsFunc(got, func(d protocol.Diagnostic) bool {
				return int(d.Range.Start.Line)+1 == tc.line && strings.HasPrefix(d.Message, tc.want)
			}) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: nomi.toml holds %s; want %q at line %d", tc.name, renderDiagnostics(got), tc.want, tc.line)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	// A text with no error clears them.
	c.change(manifestURI, len(cases)+2, clean)
	deadline := time.Now().Add(60 * time.Second)
	for len(manifestDiags(s, manifestURI)) > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("a manifest with no error keeps %s", renderDiagnostics(manifestDiags(s, manifestURI)))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Completion in nomi.toml offers std modules in every list of std names,
// and the dependencies' paths in [capabilities.deps]' go and as keys under
// [capabilities.deps.modules], never the project's own; quoted unless the
// cursor is inside a string.
func TestManifestCompletion(t *testing.T) {
	text := "[module]\nname = \"app\"\n\n[capabilities.self]\nstd = [\"std::io\", ]\nstd_in_tests = [\"std::f\"]\n\n" +
		"[capabilities.deps]\nstd = []\ngo = []\n\n[capabilities.deps.modules]\n\"example.com/pricing\" = []\n\n"
	app := manifestProject(t, text)
	uri := fileuri.FromPath(filepath.Join(app, "nomi.toml"))
	at := func(line int, after string) protocol.Position {
		l := strings.Split(text, "\n")[line-1]
		i := strings.Index(l, after)
		if i < 0 {
			t.Fatalf("line %d %q has no %q", line, l, after)
		}
		return protocol.Position{Line: uint32(line - 1), Character: uint32(i + len(after))}
	}
	cases := []struct {
		name     string
		pos      protocol.Position
		label    string
		insert   string
		excluded string
	}{
		{"in self's std", at(5, `"std::io", `), "std::fs", `"std::fs"`, "example.com/pricing"},
		{"in a string in std_in_tests", at(6, `"std::f`), "std::fs", "std::fs", ""},
		{"in deps' std", at(9, "["), "std::fs", `"std::fs"`, "example.com/pricing"},
		{"in deps' go", at(10, "["), "example.com/pricing", `"example.com/pricing"`, "example.com/app"},
		{"in a dependency's entry", at(13, "= ["), "std::fs", `"std::fs"`, "example.com/pricing"},
		{"a key under [capabilities.deps.modules]", protocol.Position{Line: 13, Character: 0}, "example.com/pricing", `"example.com/pricing"`, "example.com/app"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			items := manifestCompletion(uri, text, c.pos)
			var labels []string
			found := false
			for _, it := range items {
				labels = append(labels, it.Label)
				if it.Label == c.label {
					found = true
					if edit, ok := it.TextEdit.(protocol.TextEdit); !ok || edit.NewText != c.insert {
						t.Errorf("%s inserts %+v; want %q", c.label, it.TextEdit, c.insert)
					}
				}
			}
			if !found || c.excluded != "" && slices.Contains(labels, c.excluded) {
				t.Errorf("completion offers %v; want %s and not %q", labels, c.label, c.excluded)
			}
		})
	}
	for _, pos := range []protocol.Position{at(2, `"app`), at(4, "[capab"), {Line: 10, Character: 0}} {
		if items := manifestCompletion(uri, text, pos); len(items) != 0 {
			t.Errorf("at %+v completion offers %d items; want none", pos, len(items))
		}
	}
}
