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

// manifestProject is an app, app by its go.mod, whose main
// calls into a dependency it requires, pricing, which has no
// [effects] tables. manifest gets the [dependencies] entry for pricing at
// its end unless it writes its own. It answers the app's directory.
func manifestProject(t *testing.T, manifest string) string {
	t.Helper()
	if !strings.Contains(manifest, "[dependencies]") {
		manifest += "\n[dependencies]\npricing = { dir = \"../pricing\" }\n"
	}
	dir := writeProject(t, map[string]string{
		"app/nomi.toml":      manifest,
		"app/main.nomi":      "import\n    std::io\n    pricing::rules\nend\n\nfn main()\n    io.print(rules.load(\"prices.txt\"))\nend\n",
		"pricing/nomi.toml":  "[package]\nname = \"pricing\"\n",
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
// the [effects] tables that need the program come from the lowering
// of the open Nomi document. Each edit replaces them.
func TestManifestDocument_Diagnostics(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	const module = "[package]\nname = \"app\"\nentry_points = [\"main\"]\n\n"
	const head = module + "[effects.self]\n"
	const clean = head + "effects = [\"std::io\", \"std::fs\"]\n"
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
		{"an unknown key", clean + "stdd = []\n", 7, "[effects.self] has no key `stdd`"},
		{"a key [effects] held directly", module + "[effects]\neffects = []\n", 6, "[effects] has no key `effects`; it holds two tables"},
		{"an unknown table", clean + "\n[effect]\n", 8, "nomi.toml has no [effect] table; did you mean [effects]?"},
		{"a name of the wrong form", head + "effects = [\n    \"std::io\",\n    \"fs\",\n]\n", 8, "[effects.self] effects: \"fs\" is not a std module"},
		{"a name listed twice", head + "effects = [\"std::io\", \"std::fs\", \"std::io\"]\n", 6, "[effects.self] effects lists std::io twice"},
		{"a misspelled std name", head + "effects = [\n    \"std::io\",\n    \"std::fz\",\n]\n", 8,
			"[effects.self] effects names std::fz, which is not a std module\nhelp: did you mean \"std::fs\"?"},
		{"a go key", clean + "\n[effects.dependencies]\ngo = [\"pricing\"]\n", 9,
			"[effects.dependencies] has no key `go`; `effects` and `effects_in_tests` list the most effects any dependency may have"},
		{"a misspelled name in an entry's effects", clean + "\n[dependencies]\npricing = { dir = \"../pricing\", effects = [\"std::fz\"] }\n", 9,
			"[dependencies] pricing.effects names std::fz, which is not a std module\nhelp: did you mean \"std::fs\"?"},
		{"a dependency with no entry", clean + "\n[effects.dependencies]\nentries_required = true\n", 9,
			"[effects.dependencies] entries_required: the [dependencies] entry pricing has no effects key"},
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
// textkit, and a third whose files no program loads, with manifest as the
// app's nomi.toml, which writes the three entries.
func completionProject(t *testing.T, manifest string) string {
	t.Helper()
	dir := writeProject(t, map[string]string{
		"app/nomi.toml":      manifest,
		"app/main.nomi":      "import\n    std::io\n    pricing::rules\n    textkit::loud\nend\n\nfn main()\n    io.print(loud.shout(rules.load(\"prices.txt\")))\nend\n",
		"pricing/nomi.toml":  "[package]\nname = \"pricing\"\n",
		"pricing/rules.nomi": "import std::fs\n\npub fn load(path: String) -> String\n    fs.read_text(path) |> Result.with_default(\"\")\nend\n",
		"textkit/nomi.toml":  "[package]\nname = \"textkit\"\n",
		"textkit/loud.nomi":  "pub fn shout(s: String) -> String\n    String.uppercase(s)\nend\n",
		"other/nomi.toml":    "[package]\nname = \"other\"\n",
	})
	return filepath.Join(dir, "app")
}

// Completion in nomi.toml offers std modules in every list of std names, a
// [dependencies] entry's effects among them, and at a key's place in an
// entry's inline table with no effects key, the key with what the code the
// entry covers reaches, as entries_required's fix writes it: an entry the
// programs load files through, never one no program loads, and none that
// has the key. A name is quoted unless the cursor is inside a string.
func TestManifestCompletion(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	text := "[package]\nname = \"app\"\nentry_points = [\"main\"]\n[effects.self]\neffects = [\"std::io\", ]\neffects_in_tests = [\"std::f\"]\n\n" +
		"[effects.dependencies]\neffects = []\nentries_required = false\n# not a list: \"textkit\", \n\n" +
		"[dependencies]\npricing = { dir = \"../pricing\", effects = [] }\ntextkit = { dir = \"../textkit\" }\nother = { dir = \"../other\" }\n"
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
	// textkit's entry with a key being typed, and other's, which no program
	// loads files through.
	typing := strings.Replace(text, `textkit = { dir = "../textkit" }`, `textkit = { dir = "../textkit", e }`, 1)
	typing = strings.Replace(typing, `other = { dir = "../other" }`, `other = { dir = "../other", e }`, 1)
	cases := []struct {
		name, text string
		pos        protocol.Position
		label      string
		insert     string
		replaced   string
		excluded   []string
	}{
		{"in self's effects", text, at(text, 5, `"std::io", `), "std::fs", `"std::fs"`, "", []string{"pricing"}},
		{"in a string in effects_in_tests", text, at(text, 6, `"std::f`), "std::fs", "std::fs", "std::f", nil},
		{"in the ceiling's effects", text, at(text, 9, "["), "std::fs", `"std::fs"`, "", []string{"pricing"}},
		{"in an entry's effects", text, at(text, 14, "effects = ["), "std::fs", `"std::fs"`, "", []string{"pricing"}},
		{"an entry's effects key", typing, at(typing, 15, `"../textkit", e`), "effects", "effects = []", "e", nil},
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
	for _, pos := range []protocol.Position{at(text, 2, `"app`), at(text, 4, "[effec"), {Line: 10, Character: 0}, at(text, 11, `"textkit", `),
		at(text, 14, "{ "), at(text, 15, `"../textkit"`)} {
		if items := s.manifestCompletion(uri, text, pos); len(items) != 0 {
			t.Errorf("at %+v completion offers %d items; want none", pos, len(items))
		}
	}
	if items := s.manifestCompletion(uri, typing, at(typing, 16, `"../other", e`)); len(items) != 0 {
		t.Errorf("an entry no program loads files through is offered %d items; want none", len(items))
	}
}

// A std name nomi.toml's lists hold that nothing the list is for reaches,
// as `nomi effects --check` finds it unused, is a hint at the name in
// the open nomi.toml, saying why. Its quick fix writes the tables without
// the name, moving one only the tests reach to effects_in_tests, and with the
// edited text the hint is gone while the others stay.
func TestManifestDocument_UnusedNames(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	manifest := "[package]\nname = \"app\"\nentry_points = [\"main\"]\n\n" +
		"[dependencies]\npricing = { dir = \"../pricing\", effects = [\"std::fs\", \"std::instant\"] }\n\n" +
		"[effects.self]\n# what the app does\neffects = [\"std::io\", \"std::net\", \"std::instant\"]\neffects_in_tests = [\"std::io\"]\n\n" +
		"[effects.dependencies]\neffects = [\"std::fs\", \"std::http\"]\n"
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
		{6, "std::instant", "nothing the project reaches through pricing has the effect std::instant"},
		{10, "std::net", "nothing in this package's own code has the effect std::net"},
		{10, "std::instant", "only the tests have the effect std::instant, which effects_in_tests is for"},
		{11, "std::io", "effects already allows std::io to the tests"},
		{14, "std::http", "no dependency has the effect std::http"},
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
		{want[0], "Remove std::instant from effects of pricing", `effects = ["std::fs", "std::instant"] }`, `effects = ["std::fs"] }`},
		{want[1], "Remove std::net from effects", `effects = ["std::io", "std::net", "std::instant"]`, `effects = ["std::io", "std::instant"]`},
		{want[2], "Move std::instant to effects_in_tests", "effects = [\"std::io\", \"std::instant\"]\neffects_in_tests = [\"std::io\"]", "effects = [\"std::io\"]\neffects_in_tests = [\"std::io\", \"std::instant\"]"},
		{want[3], "Remove std::io from effects_in_tests", `effects_in_tests = ["std::io", "std::instant"]`, `effects_in_tests = ["std::instant"]`},
		{want[4], "Remove std::http from effects", `effects = ["std::fs", "std::http"]`, `effects = ["std::fs"]`},
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
	if !strings.Contains(text, "# what the app does\neffects = [\"std::io\"]") {
		t.Errorf("the fixes dropped the comment on effects:\n%s", text)
	}
}

// While a program of the project does not check, what it reaches is not
// known, and no name is said to be unused; once it checks, the hint comes.
func TestManifestDocument_NoUnusedNamesWhileAProgramDoesNotCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	manifest := "[package]\nname = \"app\"\nentry_points = [\"main\"]\n\n[effects.self]\neffects = [\"std::io\", \"std::net\"]\n"
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
	if len(got) != 1 || got[0].Message != "nothing in this package's own code has the effect std::net" {
		t.Fatalf("once the program checks, nomi.toml holds %s; want the hint at std::net", renderDiagnostics(got))
	}
}

// A hover on a std name in a [dependencies] entry's effects shows the calls
// in the code the entry covers that need it, as `nomi effects --why`
// prints them.
func TestManifestDocument_EntryNameHover(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	manifest := "[package]\nname = \"app\"\nentry_points = [\"main\"]\n\n[dependencies]\npricing = { dir = \"../pricing\", effects = [\"std::fs\", \"std::io\"] }\n"
	app := manifestProject(t, manifest)
	uri := fileuri.FromPath(filepath.Join(app, "nomi.toml"))
	s := NewServer()
	want := "```text\n1 call has the effect std::fs:\n  rules.nomi:4:5 in pricing\n    reached from `main`; `main` calls `load`\n```"
	if got := manifestHoverText(t, s, uri, manifestAt(t, manifest, 6, `"std::fs"`)); got != want {
		t.Errorf("hover on std::fs in pricing's effects says\n%s\nwant\n%s", got, want)
	}
	if got, want := manifestHoverText(t, s, uri, manifestAt(t, manifest, 6, `"std::io"`)), "Nothing the project reaches through pricing has the effect std::io."; got != want {
		t.Errorf("hover on std::io in pricing's effects says %q; want %q", got, want)
	}
	if got := manifestHoverText(t, s, uri, manifestAt(t, manifest, 3, `"main"`)); got != "" {
		t.Errorf("hover on an entry point says %q; want nothing", got)
	}
}

// With entries_required, a [dependencies] entry with no effects key is an
// error at the key, whose quick fix writes the key on the entry with what
// the code the entry covers reaches; completing the key in the entry
// writes the same, and with the edited text the error is gone.
func TestManifestDocument_MissingEntryFix(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	manifest := "[package]\nname = \"app\"\nentry_points = [\"main\"]\n\n[effects.self]\neffects = [\"std::io\"]\n\n" +
		"[effects.dependencies]\n# every dependency is listed\nentries_required = true\neffects = [\"std::fs\"]\n\n" +
		"[dependencies]\npricing = { dir = \"../pricing\" } # the prices\n"
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
	const message = "[effects.dependencies] entries_required: the [dependencies] entry pricing has no effects key"
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
		t.Fatalf("the missing key has actions %+v; want one edit of nomi.toml", actions)
	}
	if want := `Write effects = ["std::fs"] on pricing under [dependencies]`; actions[0].Title != want {
		t.Errorf("the fix is titled %q; want %q", actions[0].Title, want)
	}
	e := actions[0].Edit.Changes[protocol.DocumentUri(manifestURI)][0]
	edited := applyFixEdit(t, manifest, parser.Edit{
		Start: parser.Pos{Line: int(e.Range.Start.Line) + 1, Col: int(e.Range.Start.Character) + 1},
		End:   parser.Pos{Line: int(e.Range.End.Line) + 1, Col: int(e.Range.End.Character) + 1},
		Text:  e.NewText,
	})
	written := `effects = ["std::fs"]`
	if want := strings.Replace(manifest, `{ dir = "../pricing" }`, `{ dir = "../pricing", `+written+` }`, 1); edited != want {
		t.Fatalf("nomi.toml after the fix:\n%s\nwant:\n%s", edited, want)
	}
	// Completing the key in the entry writes what the fix wrote.
	typing := strings.Replace(manifest, `{ dir = "../pricing" }`, `{ dir = "../pricing", eff }`, 1)
	var offered []string
	for _, it := range s.manifestCompletion(manifestURI, typing, manifestAt(t, typing, 14, ` }`)) {
		offered = append(offered, it.TextEdit.(protocol.TextEdit).NewText)
	}
	if !slices.Equal(offered, []string{written}) {
		t.Errorf("completing the effects key offers %q; want the fix's %q", offered, written)
	}
	c.change(manifestURI, 2, edited)
	deadline = time.Now().Add(60 * time.Second)
	for len(errorsIn()) > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("with the key written, nomi.toml holds %s; want no error", renderDiagnostics(errorsIn()))
		}
		time.Sleep(10 * time.Millisecond)
	}
}
