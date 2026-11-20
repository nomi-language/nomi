package lsp

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/fileuri"
	"github.com/nomi-language/nomi/internal/parser"
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
	// A text with no error clears them. Its std::fs, which only the
	// dependency reaches, is a hint (TestManifestDocument_UnusedNames).
	c.change(manifestURI, len(cases)+2, clean)
	deadline := time.Now().Add(60 * time.Second)
	errs := func() []protocol.Diagnostic {
		return slices.DeleteFunc(manifestDiags(s, manifestURI), func(d protocol.Diagnostic) bool {
			return *d.Severity == protocol.DiagnosticSeverityHint
		})
	}
	for len(errs()) > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("a manifest with no error keeps %s", renderDiagnostics(errs()))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// completionProject is manifestProject with a second dependency,
// example.com/textkit, whose code binds Go, and a third go.mod requires
// whose files no program loads, with manifest as the app's nomi.toml.
func completionProject(t *testing.T, manifest string) string {
	t.Helper()
	dir := writeProject(t, map[string]string{
		"app/go.mod": "module example.com/app\n\ngo 1.27.0\n\nrequire (\n\texample.com/pricing v0.0.0\n\texample.com/textkit v0.0.0\n\texample.com/other v0.0.0\n)\n\n" +
			"replace example.com/pricing => ../pricing\n\nreplace example.com/textkit => ../textkit\n\nreplace example.com/other => ../other\n",
		"app/nomi.toml":      manifest,
		"app/main.nomi":      "import\n    std::io\n    pricing::rules\n    textkit::loud\nend\n\nfn main()\n    io.print(loud.shout(rules.load(\"prices.txt\")))\nend\n",
		"pricing/go.mod":     "module example.com/pricing\n\ngo 1.27.0\n",
		"pricing/nomi.toml":  "[module]\nname = \"pricing\"\n",
		"pricing/rules.nomi": "import std::fs\n\npub fn load(path: String) -> String\n    fs.read_text(path) |> Result.with_default(\"\")\nend\n",
		"textkit/go.mod":     "module example.com/textkit\n\ngo 1.27.0\n",
		"textkit/nomi.toml":  "[module]\nname = \"textkit\"\n",
		"textkit/loud.nomi":  "gopkg \"strings\" as gostrings\n\npub fn shout(s: String) -> String go gostrings.ToUpper\n",
		"other/go.mod":       "module example.com/other\n\ngo 1.27.0\n",
		"other/nomi.toml":    "[module]\nname = \"other\"\n",
	})
	return filepath.Join(dir, "app")
}

// Completion in nomi.toml offers std modules in every list of std names;
// in [capabilities.deps]' go, the dependencies whose code binds Go; and as
// a key under [capabilities.deps.modules], each dependency with its whole
// entry, what its own files reach, as the missing entry's fix writes it.
// A dependency is a module the programs load, never the project's own nor
// one go.mod requires and no program loads, and one the list or table
// already names is not offered. A name is quoted unless the cursor is
// inside a string.
func TestManifestCompletion(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	text := "[module]\nname = \"app\"\nentry_points = [\"main\"]\n[capabilities.self]\nstd = [\"std::io\", ]\nstd_in_tests = [\"std::f\"]\n\n" +
		"[capabilities.deps]\nstd = []\ngo = []\ngo_in_tests = [\"example.com/textkit\", ]\n\n[capabilities.deps.modules]\n\"example.com/pricing\" = []\n\n"
	app := completionProject(t, text)
	uri := fileuri.FromPath(filepath.Join(app, "nomi.toml"))
	s := NewServer()
	at := func(text string, line int, after string) protocol.Position {
		l := strings.Split(text, "\n")[line-1]
		i := strings.Index(l, after)
		if i < 0 {
			t.Fatalf("line %d %q has no %q", line, l, after)
		}
		return protocol.Position{Line: uint32(line - 1), Character: uint32(i + len(after))}
	}
	// Without pricing's entry, the empty key line is 14, and line 15 is
	// a key being typed, its closing quote written by the editor.
	unlisted := strings.Replace(text, "\"example.com/pricing\" = []\n\n", "\n\"example.com/pr\"\n", 1)
	pricingEntry := `"example.com/pricing" = ["std::fs"]`
	cases := []struct {
		name, text string
		pos        protocol.Position
		label      string
		insert     string
		replaced   string
		excluded   []string
	}{
		{"in self's std", text, at(text, 5, `"std::io", `), "std::fs", `"std::fs"`, "", []string{"example.com/pricing"}},
		{"in a string in std_in_tests", text, at(text, 6, `"std::f`), "std::fs", "std::fs", "std::f", nil},
		{"in deps' std", text, at(text, 9, "["), "std::fs", `"std::fs"`, "", []string{"example.com/pricing"}},
		{"in deps' go, the dependency that binds Go", text, at(text, 10, "["), "example.com/textkit", `"example.com/textkit"`, "",
			[]string{"example.com/pricing", "example.com/app", "example.com/other"}},
		{"in a dependency's entry", text, at(text, 14, "= ["), "std::fs", `"std::fs"`, "", []string{"example.com/pricing"}},
		{"a key under [capabilities.deps.modules]", text, protocol.Position{Line: 14, Character: 0}, "example.com/textkit", `"example.com/textkit" = []`, "",
			[]string{"example.com/pricing", "example.com/app", "example.com/other"}},
		{"a key the table lacks", unlisted, protocol.Position{Line: 13, Character: 0}, "example.com/pricing", pricingEntry, "", nil},
		{"a key being typed", unlisted, at(unlisted, 15, `"example.com/pr`), "example.com/pricing", pricingEntry, `"example.com/pr"`, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			items := s.manifestCompletion(uri, c.text, c.pos)
			var labels []string
			found := false
			for _, it := range items {
				labels = append(labels, it.Label)
				if it.Label != c.label {
					continue
				}
				found = true
				edit, ok := it.TextEdit.(protocol.TextEdit)
				if !ok || edit.NewText != c.insert {
					t.Errorf("%s inserts %+v; want %q", c.label, it.TextEdit, c.insert)
					continue
				}
				line := strings.Split(c.text, "\n")[edit.Range.Start.Line]
				if got := line[edit.Range.Start.Character:edit.Range.End.Character]; got != c.replaced {
					t.Errorf("%s replaces %q; want %q", c.label, got, c.replaced)
				}
			}
			if !found || slices.ContainsFunc(c.excluded, func(e string) bool { return slices.Contains(labels, e) }) {
				t.Errorf("completion offers %v; want %s and none of %q", labels, c.label, c.excluded)
			}
		})
	}
	for _, pos := range []protocol.Position{at(text, 2, `"app`), at(text, 4, "[capab"), {Line: 10, Character: 0}, at(text, 11, `"example.com/textkit", `)} {
		if items := s.manifestCompletion(uri, text, pos); len(items) != 0 {
			t.Errorf("at %+v completion offers %d items; want none", pos, len(items))
		}
	}
}

// A std name nomi.toml's lists hold that nothing the list is for reaches,
// as `nomi capabilities --check` finds it unused, is a hint at the name in
// the open nomi.toml, saying why. Its quick fix writes the tables without
// the name, moving one only the tests reach to std_in_tests, and with the
// edited text the hint is gone while the others stay.
func TestManifestDocument_UnusedNames(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	manifest := "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n" +
		"[capabilities.self]\n# what the app does\nstd = [\"std::io\", \"std::net\", \"std::instant\"]\nstd_in_tests = [\"std::io\"]\n\n" +
		"[capabilities.deps]\nstd = [\"std::fs\", \"std::http\"]\n\n" +
		"[capabilities.deps.modules]\n\"example.com/pricing\" = [\"std::fs\", \"std::instant\"]\n"
	app := manifestProject(t, manifest)
	if err := os.WriteFile(filepath.Join(app, "main_test.nomi"), []byte("import std::instant.{Instant}\n\ntest \"the clock has started\"\n    assert Instant.to_seconds(Instant.now()) > 0\nend\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	uri := fileuri.FromPath(filepath.Join(app, "nomi.toml"))
	s := NewServer()
	c := startRPC(t, s, app)
	c.notify("textDocument/didOpen", protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{
		URI: protocol.DocumentUri(uri), LanguageID: "toml", Version: 1, Text: manifest,
	}})
	type hint struct {
		line          int
		name, message string
	}
	want := []hint{
		{7, "std::net", "nothing in this module's own code reaches std::net"},
		{7, "std::instant", "only the tests reach std::instant, which std_in_tests is for"},
		{8, "std::io", "std already allows std::io to the tests"},
		{11, "std::http", "no dependency reaches std::http"},
		{14, "std::instant", "nothing in example.com/pricing reaches std::instant"},
	}
	hintsOf := func(text string, ds []protocol.Diagnostic) []hint {
		var out []hint
		for _, d := range ds {
			if d.Severity == nil || *d.Severity != protocol.DiagnosticSeverityHint {
				continue
			}
			// Hints of an earlier text may be past this one's lines.
			name := "?"
			if lines := strings.Split(text, "\n"); int(d.Range.Start.Line) < len(lines) && int(d.Range.End.Character) <= len(lines[d.Range.Start.Line]) {
				name = lines[d.Range.Start.Line][d.Range.Start.Character+1 : d.Range.End.Character-1]
			}
			out = append(out, hint{int(d.Range.Start.Line) + 1, name, d.Message})
		}
		return out
	}
	wait := func(text string, want []hint) []protocol.Diagnostic {
		t.Helper()
		deadline := time.Now().Add(60 * time.Second)
		for {
			ds := manifestDiags(s, uri)
			got := hintsOf(text, ds)
			if slices.Equal(got, want) {
				return ds
			}
			if time.Now().After(deadline) {
				t.Fatalf("nomi.toml's hints are %+v; want %+v", got, want)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	ds := wait(manifest, want)
	fixOf := func(text string, ds []protocol.Diagnostic, h hint) (string, string) {
		t.Helper()
		for _, d := range ds {
			if d.Message != h.message {
				continue
			}
			actions := c.codeActions(uri, d)
			if len(actions) != 1 || actions[0].Edit == nil || len(actions[0].Edit.Changes[protocol.DocumentUri(uri)]) != 1 {
				t.Fatalf("the hint %q has actions %+v; want one edit of nomi.toml", h.message, actions)
			}
			e := actions[0].Edit.Changes[protocol.DocumentUri(uri)][0]
			return actions[0].Title, applyFixEdit(t, text, parser.Edit{
				Start: parser.Pos{Line: int(e.Range.Start.Line) + 1, Col: int(e.Range.Start.Character) + 1},
				End:   parser.Pos{Line: int(e.Range.End.Line) + 1, Col: int(e.Range.End.Character) + 1},
				Text:  e.NewText,
			})
		}
		t.Fatalf("no hint says %q", h.message)
		return "", ""
	}
	fixes := []struct {
		hint  hint
		title string
		from  string
		to    string
	}{
		{want[0], "Remove std::net from std", `std = ["std::io", "std::net", "std::instant"]`, `std = ["std::io", "std::instant"]`},
		{want[1], "Move std::instant to std_in_tests", "std = [\"std::io\", \"std::instant\"]\nstd_in_tests = [\"std::io\"]", "std = [\"std::io\"]\nstd_in_tests = [\"std::io\", \"std::instant\"]"},
		{want[2], "Remove std::io from std_in_tests", `std_in_tests = ["std::io", "std::instant"]`, `std_in_tests = ["std::instant"]`},
		{want[3], "Remove std::http from std", `std = ["std::fs", "std::http"]`, `std = ["std::fs"]`},
		{want[4], "Remove std::instant from the entry for example.com/pricing", `"example.com/pricing" = ["std::fs", "std::instant"]`, `"example.com/pricing" = ["std::fs"]`},
	}
	text := manifest
	for i, f := range fixes {
		title, edited := fixOf(text, ds, f.hint)
		if wantText := strings.Replace(text, f.from, f.to, 1); title != f.title || edited != wantText {
			t.Fatalf("the fix of %q is %q and writes\n%s\nwant %q writing\n%s", f.hint.message, title, edited, f.title, wantText)
		}
		text = edited
		c.change(uri, i+2, text)
		// Each fix writes the list on its own line, so the others stay put.
		ds = wait(text, want[i+1:])
	}
	if !strings.Contains(text, "# what the app does\nstd = [\"std::io\"]") {
		t.Errorf("the fixes dropped the comment on std:\n%s", text)
	}
}

// While a program of the project does not check, what it reaches is not
// known, and no name is said to be unused; once it checks, the hint comes.
func TestManifestDocument_NoUnusedNamesWhileAProgramDoesNotCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	manifest := "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[capabilities.self]\nstd = [\"std::io\", \"std::net\"]\n"
	app := manifestProject(t, manifest)
	main := filepath.Join(app, "main.nomi")
	good, err := os.ReadFile(main)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(main, []byte(strings.Replace(string(good), "io.print(", "io.print(1 + ", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	uri := fileuri.FromPath(filepath.Join(app, "nomi.toml"))
	s := NewServer()
	s.unused.opened(uri)
	s.publishUnused(uri, 0)
	if got := manifestDiags(s, uri); len(got) != 0 {
		t.Fatalf("with a program that does not check, nomi.toml holds %s; want nothing", renderDiagnostics(got))
	}
	if err := os.WriteFile(main, good, 0o644); err != nil {
		t.Fatal(err)
	}
	s.reaches.changed()
	s.publishUnused(uri, 0)
	got := manifestDiags(s, uri)
	if len(got) != 1 || got[0].Message != "nothing in this module's own code reaches std::net" {
		t.Fatalf("once the program checks, nomi.toml holds %s; want the hint at std::net", renderDiagnostics(got))
	}
}

// A hover on a dependency's path in nomi.toml, in [capabilities.deps]' go
// and as a key under [capabilities.deps.modules], shows what its own files
// reach, as `nomi add` prints it.
func TestManifestDocument_ModuleHover(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	manifest := "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[capabilities.deps]\ngo = [\"example.com/pricing\"]\n\n" +
		"[capabilities.deps.modules]\n\"example.com/pricing\" = [\"std::fs\"]\n"
	app := manifestProject(t, manifest)
	uri := fileuri.FromPath(filepath.Join(app, "nomi.toml"))
	s := NewServer()
	want := "```text\nexample.com/pricing\n  std::fs\n```"
	for _, line := range []int{6, 9} {
		if got := manifestHoverText(t, s, uri, manifestAt(t, manifest, line, `"example.com/pricing"`)); got != want {
			t.Errorf("hover on the module's path at line %d says\n%s\nwant\n%s", line, got, want)
		}
	}
	if got := manifestHoverText(t, s, uri, manifestAt(t, manifest, 3, `"main"`)); got != "" {
		t.Errorf("hover on an entry point says %q; want nothing", got)
	}
}

// With entries_required, a dependency with no entry is an error at the key,
// whose quick fix writes the entry with what the dependency's own files
// reach, as `nomi add` writes it; with the edited text the error is gone.
func TestManifestDocument_MissingEntryFix(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	manifest := "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[capabilities.self]\nstd = [\"std::io\"]\n\n" +
		"[capabilities.deps]\n# every dependency is listed\nentries_required = true\nstd = [\"std::fs\"]\n"
	app := manifestProject(t, manifest)
	uri := fileuri.FromPath(filepath.Join(app, "main.nomi"))
	manifestURI := fileuri.FromPath(filepath.Join(app, "nomi.toml"))
	main, err := os.ReadFile(filepath.Join(app, "main.nomi"))
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer()
	s.lowering.delay = 50 * time.Millisecond
	c := startRPC(t, s, app)
	c.open(uri, string(main))
	c.notify("textDocument/didOpen", protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{
		URI: protocol.DocumentUri(manifestURI), LanguageID: "toml", Version: 1, Text: manifest,
	}})
	const message = "[capabilities.deps] entries_required: example.com/pricing has no entry"
	errorsIn := func() []protocol.Diagnostic {
		return slices.DeleteFunc(manifestDiags(s, manifestURI), func(d protocol.Diagnostic) bool {
			return *d.Severity != protocol.DiagnosticSeverityError
		})
	}
	var missing protocol.Diagnostic
	deadline := time.Now().Add(60 * time.Second)
	for {
		if ds := errorsIn(); len(ds) == 1 && strings.HasPrefix(ds[0].Message, message) && ds[0].Range.Start.Line == 9 {
			missing = ds[0]
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("nomi.toml holds %s; want %q at line 10", renderDiagnostics(errorsIn()), message)
		}
		time.Sleep(10 * time.Millisecond)
	}
	actions := c.codeActions(manifestURI, missing)
	if len(actions) != 1 || actions[0].Edit == nil || len(actions[0].Edit.Changes[protocol.DocumentUri(manifestURI)]) != 1 {
		t.Fatalf("the missing entry has actions %+v; want one edit of nomi.toml", actions)
	}
	if want := `Write "example.com/pricing" = ["std::fs"] under [capabilities.deps.modules]`; actions[0].Title != want {
		t.Errorf("the fix is titled %q; want %q", actions[0].Title, want)
	}
	e := actions[0].Edit.Changes[protocol.DocumentUri(manifestURI)][0]
	edited := applyFixEdit(t, manifest, parser.Edit{
		Start: parser.Pos{Line: int(e.Range.Start.Line) + 1, Col: int(e.Range.Start.Character) + 1},
		End:   parser.Pos{Line: int(e.Range.End.Line) + 1, Col: int(e.Range.End.Character) + 1},
		Text:  e.NewText,
	})
	if want := manifest + "\n[capabilities.deps.modules]\n\"example.com/pricing\" = [\"std::fs\"]\n"; edited != want {
		t.Fatalf("nomi.toml after the fix:\n%s\nwant:\n%s", edited, want)
	}
	// Completing the key writes the entry the fix wrote.
	keyed := manifest + "\n[capabilities.deps.modules]\n\n"
	written := strings.TrimPrefix(edited, manifest+"\n[capabilities.deps.modules]\n")
	var offered []string
	for _, it := range s.manifestCompletion(manifestURI, keyed, protocol.Position{Line: uint32(strings.Count(keyed, "\n") - 1)}) {
		offered = append(offered, it.TextEdit.(protocol.TextEdit).NewText+"\n")
	}
	if !slices.Equal(offered, []string{written}) {
		t.Errorf("completing a key under [capabilities.deps.modules] offers %q; want the fix's %q", offered, written)
	}
	c.change(manifestURI, 2, edited)
	deadline = time.Now().Add(60 * time.Second)
	for len(errorsIn()) > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("with the entry written, nomi.toml holds %s; want no error", renderDiagnostics(errorsIn()))
		}
		time.Sleep(10 * time.Millisecond)
	}
}
