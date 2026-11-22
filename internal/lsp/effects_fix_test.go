package lsp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/fileuri"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/vmhost"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// applyFixEdit is text with e, a fix's edit in 1-based lines and byte
// columns, applied.
func applyFixEdit(t *testing.T, text string, e parser.Edit) string {
	t.Helper()
	offset := func(p parser.Pos) int {
		at := 0
		for line := 1; line < p.Line; line++ {
			i := strings.IndexByte(text[at:], '\n')
			if i < 0 {
				t.Fatalf("the edit's line %d is past the text's end", p.Line)
			}
			at += i + 1
		}
		return at + p.Col - 1
	}
	return text[:offset(e.Start)] + e.Text + text[offset(e.End):]
}

// effectFixOf lowers the entry at path whose text is src and answers
// its one effect-not-allowed error's fix, which must carry an edit to
// the project's nomi.toml.
func effectFixOf(t *testing.T, path, src string) parser.ParseError {
	t.Helper()
	found, err := vmhost.CheckLowering(context.Background(), path, src)
	problems := found.Problems
	_, fixes := problemDiagnosticsAndFixes(path, src, problems, err)
	if len(fixes) != 1 {
		t.Fatalf("the lowering's fixes are %+v (problems %v, err %v); want one", fixes, problems, err)
	}
	return fixes[0]
}

// The fix of a call nomi.toml's tables do not allow edits nomi.toml, at the
// key of the table the help names: self's std, or std_in_tests when only a
// test reaches the call; a dependency's entry under [effects.deps.modules],
// with deps' std too when that does not allow it either, in one action; and
// deps' std or std_in_tests alone. Every other line, comments included, stays, and with the
// edited file the call is allowed.
func TestEffectFix_EditsEachList(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	const module = "# The app.\n[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n"
	// pricingModule is module naming the dependency the case's files hold.
	const pricingModule = module + "[dependencies]\npricing = { dir = \"../pricing\" }\n\n"
	const testsMain = "import\n    std::fs\n    std::io\nend\n\nfn main()\n    io.print(\"hi\")\nend\n\n" +
		"test \"reads\"\n    assert fs.read_text(\"notes.txt\") is Err(_)\nend\n"
	const pricingMain = "import\n    std::io\n    pricing::rules\nend\n\nfn main()\n    io.print(rules.load(\"prices.txt\"))\nend\n"
	const pricingTestMain = "import\n    std::io\n    pricing::rules\nend\n\nfn main()\n    io.print(\"hi\")\nend\n\n" +
		"test \"loads\"\n    assert rules.load(\"x\") == \"\"\nend\n"
	pricing := map[string]string{
		"pricing/nomi.toml":  "[module]\nname = \"pricing\"\n",
		"pricing/rules.nomi": "import std::fs\n\npub fn load(path: String) -> String\n    fs.read_text(path) |> Result.with_default(\"\")\nend\n",
	}
	cases := []struct {
		name, main, manifest, title, want string
		// app is the project's directory under the test's, files the
		// files beside it.
		app   string
		files map[string]string
	}{{
		name:     "self's std",
		main:     effectMain,
		manifest: module + "[effects.self]\n# what main prints\nstd = [\"std::io\"]  # output\n",
		title:    "Allow std::fs for this module in nomi.toml",
		want:     module + "[effects.self]\n# what main prints\nstd = [\"std::io\", \"std::fs\"]  # output\n",
	}, {
		name:     "self's std_in_tests",
		main:     testsMain,
		manifest: module + "[effects.self]\nstd = [\"std::io\"]\n\n# tests\nstd_in_tests = []\n",
		title:    "Allow std::fs for this module's tests in nomi.toml",
		want:     module + "[effects.self]\nstd = [\"std::io\"]\n\n# tests\nstd_in_tests = [\"std::fs\"]\n",
	}, {
		name: "a dependency's entry and deps' std",
		main: pricingMain,
		manifest: pricingModule + "[effects.deps]\nstd = [] # all\n\n[effects.deps.modules]\n" +
			"# prices\n\"pricing\" = []\n",
		title: "Allow std::fs for pricing in nomi.toml",
		want: pricingModule + "[effects.deps]\nstd = [\"std::fs\"] # all\n\n[effects.deps.modules]\n" +
			"# prices\n\"pricing\" = [\"std::fs\"]\n",
		app:   "app",
		files: pricing,
	}, {
		name:     "deps' std",
		main:     pricingMain,
		manifest: pricingModule + "[effects.deps]\n# the most any dependency reaches\nstd = []\n",
		title:    "Allow std::fs for dependencies in nomi.toml",
		want:     pricingModule + "[effects.deps]\n# the most any dependency reaches\nstd = [\"std::fs\"]\n",
		app:      "app",
		files:    pricing,
	}, {
		name:     "deps' std_in_tests",
		main:     pricingTestMain,
		manifest: pricingModule + "[effects.deps]\nstd = []\n",
		title:    "Allow std::fs for dependencies in tests in nomi.toml",
		want:     pricingModule + "[effects.deps]\nstd = []\nstd_in_tests = [\"std::fs\"]\n",
		app:      "app",
		files:    pricing,
	}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files := map[string]string{filepath.Join(c.app, "main.nomi"): c.main, filepath.Join(c.app, "nomi.toml"): c.manifest}
			for name, text := range c.files {
				files[name] = text
			}
			dir := filepath.Join(writeProject(t, files), c.app)
			path, manifest := filepath.Join(dir, "main.nomi"), filepath.Join(dir, "nomi.toml")
			e := effectFixOf(t, path, c.main)
			if e.Fix.Title != c.title || e.Fix.Path != manifest || len(e.Fix.Edits) != 1 {
				t.Fatalf("fix %q in %s with %d edits; want %q in %s with one", e.Fix.Title, e.Fix.Path, len(e.Fix.Edits), c.title, manifest)
			}
			got := applyFixEdit(t, c.manifest, e.Fix.Edits[0])
			if got != c.want {
				t.Fatalf("nomi.toml after the fix:\n%s\nwant:\n%s", got, c.want)
			}
			if err := os.WriteFile(manifest, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
			if diags := loweringDiagnostics(path, c.main, nil); len(diags) != 0 {
				t.Errorf("with the fix applied, the lowering reports %s", renderDiagnostics(diags))
			}
		})
	}
}

// A call over a dependency's own list (effect-not-declared) has no
// fix: what to change is in the dependency's nomi.toml.
func TestEffectFix_NoneForADependencysOwnList(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	main := "import\n    std::io\n    pricing::rules\nend\n\nfn main()\n    io.print(rules.load(\"prices.txt\"))\nend\n"
	dir := writeProject(t, map[string]string{
		"app/nomi.toml":      "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[effects.self]\nstd = [\"std::io\"]\n\n[effects.deps]\nstd = [\"std::io\"]\n\n[dependencies]\npricing = { dir = \"../pricing\" }\n",
		"app/main.nomi":      main,
		"pricing/nomi.toml":  "[module]\nname = \"pricing\"\n\n[effects.self]\nstd = [\"std::io\"]\n",
		"pricing/rules.nomi": "import std::fs\n\npub fn load(path: String) -> String\n    fs.read_text(path) |> Result.with_default(\"\")\nend\n",
	})
	path := filepath.Join(dir, "app", "main.nomi")
	found, err := vmhost.CheckLowering(context.Background(), path, main)
	problems := found.Problems
	diags := problemDiagnostics(path, main, problems, err)
	if len(diags) != 1 || diags[0].Code == nil || diags[0].Code.Value != "effect-not-declared" {
		t.Fatalf("lowering diagnostics %s; want one effect-not-declared", renderDiagnostics(diags))
	}
	_, fixes := problemDiagnosticsAndFixes(path, main, problems, err)
	if len(fixes) != 0 {
		t.Errorf("a call over a dependency's own list has fixes %+v; want none", fixes)
	}
	if actions := buildParseFixActions(main, fileuri.FromPath(path), nil, fixes, diags); len(actions) != 0 {
		t.Errorf("code actions %+v; want none", actions)
	}
}

// codeActions asks the server for the code actions at diag in uri.
func (c *rpcClient) codeActions(uri string, diag protocol.Diagnostic) []protocol.CodeAction {
	c.t.Helper()
	var actions []protocol.CodeAction
	if err := c.conn.Call(context.Background(), "textDocument/codeAction", map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"range":        diag.Range,
		"context":      map[string]any{"diagnostics": []protocol.Diagnostic{diag}},
	}, &actions); err != nil {
		c.t.Fatalf("codeAction: %v", err)
	}
	return actions
}

// waitLowering waits until the server holds lowering diagnostics for uri
// for which done is true.
func waitLowering(t *testing.T, s *Server, uri, what string, done func([]protocol.Diagnostic) bool) []protocol.Diagnostic {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		got := s.lowering.diagnostics(uri)
		if done(got) {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: the server holds %s", what, renderDiagnostics(got))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// The server offers the fix as a code action on the diagnostic, a
// WorkspaceEdit of nomi.toml. The client's applying it to an open, unsaved
// nomi.toml clears the diagnostic; closing that buffer brings it back, as
// the disk still refuses the call; saving the edit clears it again.
func TestEffectFix_CodeActionClearsTheDiagnostic(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	manifest := "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[effects.self]\n# printing\nstd = [\"std::io\"]\n"
	dir := writeProject(t, map[string]string{"main.nomi": effectMain, "nomi.toml": manifest})
	uri := fileuri.FromPath(filepath.Join(dir, "main.nomi"))
	manifestPath := filepath.Join(dir, "nomi.toml")
	manifestURI := fileuri.FromPath(manifestPath)
	s := NewServer()
	s.lowering.delay = 50 * time.Millisecond
	c := startRPC(t, s, dir)
	c.open(uri, effectMain)
	refused := func(ds []protocol.Diagnostic) bool {
		return len(ds) == 1 && ds[0].Code != nil && ds[0].Code.Value == "effect-not-allowed"
	}
	clean := func(ds []protocol.Diagnostic) bool { return len(ds) == 0 }
	diag := waitLowering(t, s, uri, "the refused call", refused)[0]

	actions := c.codeActions(uri, diag)
	var fix *protocol.CodeAction
	for i, a := range actions {
		if a.Title == "Allow std::fs for this module in nomi.toml" {
			fix = &actions[i]
		}
	}
	if fix == nil || fix.Edit == nil || len(fix.Edit.Changes) != 1 || len(fix.Edit.Changes[protocol.DocumentUri(manifestURI)]) != 1 {
		raw, _ := json.Marshal(actions)
		t.Fatalf("no action edits nomi.toml alone; actions %s", raw)
	}
	if fix.IsPreferred != nil && *fix.IsPreferred {
		t.Errorf("the fix that widens the lists is preferred")
	}
	edit := fix.Edit.Changes[protocol.DocumentUri(manifestURI)][0]
	edited := applyFixEdit(t, manifest, parser.Edit{
		Start: parser.Pos{Line: int(edit.Range.Start.Line) + 1, Col: int(edit.Range.Start.Character) + 1},
		End:   parser.Pos{Line: int(edit.Range.End.Line) + 1, Col: int(edit.Range.End.Character) + 1},
		Text:  edit.NewText,
	})
	if want := strings.Replace(manifest, `["std::io"]`, `["std::io", "std::fs"]`, 1); edited != want {
		t.Fatalf("nomi.toml after the action:\n%s\nwant:\n%s", edited, want)
	}

	// Unsaved: the editor holds the edited text.
	c.notify("textDocument/didOpen", protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{
		URI: protocol.DocumentUri(manifestURI), LanguageID: "toml", Version: 1, Text: edited,
	}})
	waitLowering(t, s, uri, "the unsaved edit allows std::fs", clean)
	c.notify("textDocument/didClose", protocol.DidCloseTextDocumentParams{TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(manifestURI)}})
	waitLowering(t, s, uri, "the closed buffer leaves the disk's list", refused)

	// Saved.
	if err := os.WriteFile(manifestPath, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	c.notify("workspace/didChangeWatchedFiles", protocol.DidChangeWatchedFilesParams{Changes: []protocol.FileEvent{
		{URI: protocol.DocumentUri(manifestURI), Type: protocol.FileChangeTypeChanged},
	}})
	waitLowering(t, s, uri, "the saved edit allows std::fs", clean)
}
