package lsp

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/fileuri"
	"github.com/nomi-language/nomi/internal/parser"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// selfModulesProject is a project of three modules: main prints what
// pricing computes and what store loads, store reads a file, and pricing
// reaches nothing. It answers the project's directory.
func selfModulesProject(t *testing.T, manifest string) string {
	t.Helper()
	return writeProject(t, map[string]string{
		"nomi.toml":    manifest,
		"main.nomi":    "import\n    std::io\n    pricing\n    store\nend\n\nfn main()\n    io.print(\"${pricing.double(2)} ${store.load(\"prices.txt\")}\")\nend\n",
		"pricing.nomi": "pub fn double(n: Int) -> Int\n    n * 2\nend\n",
		"store.nomi":   "import std::fs\n\npub fn load(path: String) -> String\n    fs.read_text(path) |> Result.with_default(\"\")\nend\n",
	})
}

// selfModulesManifest is selfModulesProject's nomi.toml with entries, the
// lines of its [effects.self.modules].
func selfModulesManifest(entries string) string {
	return "[package]\nname = \"app\"\nentry_points = [\"main\"]\n\n[effects.self]\nstd = [\"std::io\", \"std::fs\"]\n\n[effects.self.modules]\n" + entries
}

// In nomi.toml, a key under [effects.self.modules] that names no module
// of the project is an error at the key, naming the nearest; with
// entries_required, a module with no entry is an error at that key whose
// quick fix writes the entry with what the module reaches, and completing
// a key writes the same entry. With the entry written, the error is gone.
func TestManifestDocument_SelfModulesErrorsAndFix(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	clean := selfModulesManifest("\"store\" = [\"std::fs\"]\n\"main\" = [\"std::io\"]\n")
	app := selfModulesProject(t, clean)
	uri := fileuri.FromPath(filepath.Join(app, "main.nomi"))
	manifestURI := fileuri.FromPath(filepath.Join(app, "nomi.toml"))
	s := NewServer()
	s.lowering.delay = 50 * time.Millisecond
	c := startRPC(t, s, app)
	c.open(uri, "import\n    std::io\n    pricing\n    store\nend\n\nfn main()\n    io.print(\"${pricing.double(2)} ${store.load(\"prices.txt\")}\")\nend\n")
	c.notify("textDocument/didOpen", protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{
		URI: protocol.DocumentUri(manifestURI), LanguageID: "toml", Version: 1, Text: clean,
	}})
	errorsIn := func() []protocol.Diagnostic {
		return slices.DeleteFunc(manifestDiags(s, manifestURI), func(d protocol.Diagnostic) bool {
			return *d.Severity != protocol.DiagnosticSeverityError
		})
	}
	waitFor := func(line int, message string) protocol.Diagnostic {
		t.Helper()
		deadline := time.Now().Add(60 * time.Second)
		for {
			ds := errorsIn()
			if i := slices.IndexFunc(ds, func(d protocol.Diagnostic) bool {
				return int(d.Range.Start.Line)+1 == line && strings.HasPrefix(d.Message, message)
			}); i >= 0 && len(ds) == 1 {
				return ds[i]
			}
			if time.Now().After(deadline) {
				t.Fatalf("nomi.toml holds %s; want only %q at line %d", renderDiagnostics(ds), message, line)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	c.change(manifestURI, 2, clean+"\"pricng\" = []\n")
	waitFor(11, "[effects.self.modules] names pricng, which is no module of this project\nhelp: did you mean \"pricing\"?")

	required := strings.Replace(clean, "[effects.self]\n", "[effects.self]\nentries_required = true\n", 1)
	c.change(manifestURI, 3, required)
	missing := waitFor(6, "[effects.self] entries_required: pricing has no entry")
	actions := c.codeActions(manifestURI, missing)
	if len(actions) != 1 || actions[0].Edit == nil || len(actions[0].Edit.Changes[protocol.DocumentUri(manifestURI)]) != 1 {
		t.Fatalf("the missing entry has actions %+v; want one edit of nomi.toml", actions)
	}
	if want := `Write "pricing" = [] under [effects.self.modules]`; actions[0].Title != want {
		t.Errorf("the fix is titled %q; want %q", actions[0].Title, want)
	}
	e := actions[0].Edit.Changes[protocol.DocumentUri(manifestURI)][0]
	edited := applyFixEdit(t, required, parser.Edit{
		Start: parser.Pos{Line: int(e.Range.Start.Line) + 1, Col: int(e.Range.Start.Character) + 1},
		End:   parser.Pos{Line: int(e.Range.End.Line) + 1, Col: int(e.Range.End.Character) + 1},
		Text:  e.NewText,
	})
	if want := required + "\"pricing\" = []\n"; edited != want {
		t.Fatalf("nomi.toml after the fix:\n%s\nwant:\n%s", edited, want)
	}
	keyed := required + "\n"
	var offered []string
	for _, it := range s.manifestCompletion(manifestURI, keyed, protocol.Position{Line: uint32(strings.Count(keyed, "\n") - 1)}) {
		offered = append(offered, it.TextEdit.(protocol.TextEdit).NewText+"\n")
	}
	if !slices.Equal(offered, []string{"\"pricing\" = []\n"}) {
		t.Errorf("completing a key under [effects.self.modules] offers %q; want the fix's entry", offered)
	}
	c.change(manifestURI, 4, edited)
	deadline := time.Now().Add(60 * time.Second)
	for len(errorsIn()) > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("with the entry written, nomi.toml holds %s; want no error", renderDiagnostics(errorsIn()))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Completion as a key under [effects.self.modules] offers each module
// of the project the programs load with its whole entry, and none the
// table already names; a hover on a key shows what the module's own files
// reach, and one on a std name in its entry the calls that need it there.
func TestManifestDocument_SelfModulesCompletionAndHover(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	text := selfModulesManifest("\"store\" = [\"std::fs\"]\n\n")
	app := selfModulesProject(t, text)
	uri := fileuri.FromPath(filepath.Join(app, "nomi.toml"))
	s := NewServer()
	items := s.manifestCompletion(uri, text, protocol.Position{Line: 9, Character: 0})
	got := map[string]string{}
	for _, it := range items {
		got[it.Label] = it.TextEdit.(protocol.TextEdit).NewText
	}
	if want := map[string]string{"main": `"main" = ["std::io"]`, "pricing": `"pricing" = []`}; len(got) != len(want) || got["main"] != want["main"] || got["pricing"] != want["pricing"] {
		t.Errorf("completing a key offers %v; want %v", got, want)
	}

	if hover := manifestHoverText(t, s, uri, manifestAt(t, text, 9, `"store"`)); hover != "```text\nstore\n  std::fs\n```" {
		t.Errorf("hover on the key store says\n%s", hover)
	}
	want := "```text\n1 call has the effect std::fs:\n  store.nomi:4:5\n    reached from `main`; `main` calls `load`\n```"
	if hover := manifestHoverText(t, s, uri, manifestAt(t, text, 9, `"std::fs"`)); hover != want {
		t.Errorf("hover on std::fs in store's entry says\n%s\nwant\n%s", hover, want)
	}
}

// A std name an entry under [effects.self.modules] holds that its
// module does not reach is a hint at the name, whose quick fix writes the
// entry without it.
func TestManifestDocument_UnusedNameInASelfModulesEntry(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	manifest := selfModulesManifest("\"store\" = [\"std::fs\"]\n\"pricing\" = [\"std::fs\"]\n")
	app := selfModulesProject(t, manifest)
	uri := fileuri.FromPath(filepath.Join(app, "nomi.toml"))
	s := NewServer()
	c := startRPC(t, s, app)
	c.notify("textDocument/didOpen", protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{
		URI: protocol.DocumentUri(uri), LanguageID: "toml", Version: 1, Text: manifest,
	}})
	const message = "nothing in pricing has the effect std::fs"
	var hint protocol.Diagnostic
	deadline := time.Now().Add(60 * time.Second)
	for {
		ds := manifestDiags(s, uri)
		if len(ds) == 1 && ds[0].Message == message && ds[0].Range.Start.Line == 9 {
			hint = ds[0]
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("nomi.toml holds %s; want the hint %q at line 10", renderDiagnostics(ds), message)
		}
		time.Sleep(10 * time.Millisecond)
	}
	actions := c.codeActions(uri, hint)
	if len(actions) != 1 || actions[0].Edit == nil || len(actions[0].Edit.Changes[protocol.DocumentUri(uri)]) != 1 {
		t.Fatalf("the hint has actions %+v; want one edit of nomi.toml", actions)
	}
	e := actions[0].Edit.Changes[protocol.DocumentUri(uri)][0]
	edited := applyFixEdit(t, manifest, parser.Edit{
		Start: parser.Pos{Line: int(e.Range.Start.Line) + 1, Col: int(e.Range.Start.Character) + 1},
		End:   parser.Pos{Line: int(e.Range.End.Line) + 1, Col: int(e.Range.End.Character) + 1},
		Text:  e.NewText,
	})
	if want := strings.Replace(manifest, `"pricing" = ["std::fs"]`, `"pricing" = []`, 1); actions[0].Title != "Remove std::fs from the entry for pricing" || edited != want {
		t.Errorf("the fix is %q and writes\n%s\nwant %q writing\n%s", actions[0].Title, edited, "Remove std::fs from the entry for pricing", want)
	}
}
